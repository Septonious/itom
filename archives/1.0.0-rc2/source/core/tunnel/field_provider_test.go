package tunnel

import (
	"bytes"
	"crypto/tls"
	_ "embed"
	"encoding/json"
	"io"
	"itom.local/core/mailbox"
	"itom.local/core/wire"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

// Recorded FIELD46 wrapper durations are diagnostic stress inputs, not tagged
// wire service times. Physical access latency is shaped separately. This model
// is explicitly uncalibrated and never authenticates a real provider result.
type fieldProviderSession struct {
	imapserver.Session
	replay      *fieldLatencyReplay
	account     string
	connection  net.Conn
	receiveRole *atomic.Bool
}

//go:embed testdata/field46-replay-inputs.json
var fieldLatencyJSON []byte

//go:embed testdata/rev1-field-wrapper-stress.json
var rev1WrapperJSON []byte

type fieldLatencyReplay struct {
	samples          map[string][]int64
	indices          map[string]*atomic.Uint64
	stallUS          int64
	stallAccounts    map[string]*atomic.Bool
	start            time.Time
	stallEnabled     bool
	syntheticService bool
	endpointScoped   bool
	service          *providerServiceState
}

func newFieldLatencyReplay(t *testing.T) *fieldLatencyReplay {
	t.Helper()
	var input struct {
		Schema     int                           `json:"schema"`
		Operations map[string]map[string][]int64 `json:"operation_samples"`
	}
	if err := json.Unmarshal(fieldLatencyJSON, &input); err != nil || input.Schema != 1 {
		t.Fatal("invalid field latency fixture", err)
	}
	r := &fieldLatencyReplay{samples: map[string][]int64{}, indices: map[string]*atomic.Uint64{}, start: time.Now(), stallEnabled: os.Getenv("ITOM_FIELD_READ_STALLS") == "1", stallAccounts: map[string]*atomic.Bool{"a02": {}, "a06": {}, "a08": {}}}
	for side, series := range input.Operations {
		for name, values := range series {
			key := name
			if side == "server" {
				key = "server/" + name
			}
			for _, us := range values {
				if us < 0 {
					t.Fatal("negative command duration")
				}
				// Explicit long-tail injection is separate from regular service.
				if name == "fetch_page" && us > 500000 {
					r.stallUS = max(r.stallUS, us)
					continue
				}
				r.samples[key] = append(r.samples[key], us)
			}
			r.indices[key] = &atomic.Uint64{}
		}
	}

	if r.stallEnabled && r.stallUS == 0 {
		t.Fatal("no recorded FETCH tail for stall model")
	}
	return r
}

func (r *fieldLatencyReplay) delay(name string, fallback time.Duration) time.Duration {
	if r == nil {
		return fallback
	}
	values := r.samples[name]
	if len(values) == 0 || r.indices[name] == nil {
		return fallback
	}
	i := r.indices[name].Add(1) - 1
	// Spread the observed samples instead of replaying a benign prefix only.
	return time.Duration(values[(i*257+17)%uint64(len(values))]) * time.Microsecond
}

func (s fieldProviderSession) delay(name string, fallback time.Duration) time.Duration {
	if s.replay == nil {
		return fallback
	}
	conn := s.connection
	if secured, ok := conn.(*tls.Conn); ok {
		conn = secured.NetConn()
	}
	if pc, ok := conn.(*providerTestConn); ok && (pc.network.mobileEnabled || s.replay.endpointScoped) && !pc.mobileClient.Load() {
		name = "server/" + name
	}
	if s.replay.endpointScoped {
		prefix := ""
		if len(name) > 7 && name[:7] == "server/" {
			prefix = "server/"
			name = name[7:]
		}
		name = prefix + s.account + "/" + name
	}
	return s.replay.delay(name, fallback) + s.replay.service.extraDelay(s.account)
}

func (s fieldProviderSession) Select(f string, o *imap.SelectOptions) (*imap.SelectData, error) {
	release, err := s.admit()
	if err != nil {
		return nil, err
	}
	defer release()
	return (delayedSession{s.Session, s.delay("ensure", 20*time.Millisecond)}).Select(f, o)
}

type replayLiteral struct {
	*bytes.Reader
	size int64
}

func (r replayLiteral) Size() int64 { return r.size }
func (s fieldProviderSession) Append(f string, r imap.LiteralReader, o *imap.AppendOptions) (*imap.AppendData, error) {
	release, admitErr := s.admit()
	if admitErr != nil {
		return nil, admitErr
	}
	defer release()
	raw, err := io.ReadAll(io.LimitReader(r, mailbox.MaxMIME+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > mailbox.MaxMIME {
		return nil, mailbox.ErrLimit
	}
	name := "append_control"
	// Classify the actual envelope, not MIME length: a short DATA batch is DATA.
	if body, err := mailbox.Decode(raw); err == nil && (bytes.HasPrefix(body, []byte(wire.DataBatchMagic)) || bytes.HasPrefix(body, []byte("ITQ1")) || bytes.HasPrefix(body, []byte(wire.DataEnvelopeMagic))) {
		name = "append_data"
	}
	return (delayedSession{s.Session, s.delay(name, 60*time.Millisecond)}).Append(f, replayLiteral{bytes.NewReader(raw), int64(len(raw))}, o)
}

func (s fieldProviderSession) Fetch(w *imapserver.FetchWriter, n imap.NumSet, o *imap.FetchOptions) error {
	release, err := s.admit()
	if err != nil {
		return err
	}
	defer release()
	name := "cleanup_fetch_exact"
	if set, ok := n.(imap.UIDSet); ok {
		for _, part := range set {
			if part.Stop >= part.Start+3 {
				name = "fetch_page"
				break
			}
		}
	}
	if s.receiveRole != nil && s.receiveRole.Load() {
		name = "fetch_page"
	}
	delay := s.delay(name, 16*time.Millisecond)
	if r := s.replay; r != nil && r.stallEnabled && name == "fetch_page" && time.Since(r.start) >= 20*time.Second {
		if flag := r.stallAccounts[s.account]; flag != nil && flag.CompareAndSwap(false, true) {
			delay = time.Duration(r.stallUS) * time.Microsecond
		}
	}
	return (delayedSession{s.Session, delay}).Fetch(w, n, o)
}
func (s fieldProviderSession) Search(k imapserver.NumKind, c *imap.SearchCriteria, o *imap.SearchOptions) (*imap.SearchData, error) {
	release, err := s.admit()
	if err != nil {
		return nil, err
	}
	defer release()
	return (delayedSession{s.Session, s.delay("presence", 25*time.Millisecond)}).Search(k, c, o)
}
func (s fieldProviderSession) Store(w *imapserver.FetchWriter, n imap.NumSet, f *imap.StoreFlags, o *imap.StoreOptions) error {
	release, err := s.admit()
	if err != nil {
		return err
	}
	defer release()
	delay := 55 * time.Millisecond
	if s.replay != nil {
		delay = s.delay("delete_observed_batch", 110*time.Millisecond) / 2
	}
	return (delayedSession{s.Session, delay}).Store(w, n, f, o)
}
func (s fieldProviderSession) Expunge(w *imapserver.ExpungeWriter, n *imap.UIDSet) error {
	release, err := s.admit()
	if err != nil {
		return err
	}
	defer release()
	delay := 55 * time.Millisecond
	if s.replay != nil {
		delay = s.delay("delete_observed_batch", 110*time.Millisecond) / 2
	}
	return (delayedSession{s.Session, delay}).Expunge(w, n)
}

// Service costs are explicit assumptions, independent of path propagation.
// FIELD46 wrapper durations cannot be deconvolved into service times without
// synchronized wire traces. They remain a separately named stress profile.
func newProviderServiceProfile(t *testing.T) *fieldLatencyReplay {
	t.Helper()
	r := &fieldLatencyReplay{service: &providerServiceState{start: time.Now(), correlated: os.Getenv("ITOM_FIELD_CORRELATED_QUEUE") == "1", limit: loadEnvInt(t, "ITOM_FIELD_COMMAND_LIMIT", 0, 0, 16)}, samples: map[string][]int64{}, indices: map[string]*atomic.Uint64{}, start: time.Now(), syntheticService: true, stallEnabled: os.Getenv("ITOM_FIELD_READ_STALLS") == "1", stallUS: 3_000_000, stallAccounts: map[string]*atomic.Bool{"a02": {}, "a06": {}, "a08": {}}}
	// Explicit heterogeneous-account stress, not a deconvolution of observed
	// wrapper times. Both endpoints and all sockets share each backend delay.
	if os.Getenv("ITOM_FIELD_HETEROGENEOUS") == "1" {
		r.service.accountDelay = map[string]time.Duration{"a01": 180 * time.Millisecond, "a02": 240 * time.Millisecond, "a05": 40 * time.Millisecond, "a10": 90 * time.Millisecond}
	}
	for name, us := range map[string]int64{"ensure": 20_000, "append_data": 60_000, "append_control": 60_000, "fetch_page": 16_000, "cleanup_fetch_exact": 16_000, "presence": 25_000, "delete_observed_batch": 110_000} {
		for _, prefix := range []string{"", "server/"} {
			r.samples[prefix+name] = []int64{us}
			r.indices[prefix+name] = &atomic.Uint64{}
		}
	}
	return r
}

func (s fieldProviderSession) admit() (func(), error) {
	if s.replay == nil || s.replay.service == nil {
		return func() {}, nil
	}
	return s.replay.service.admit(s.account)
}

// Classify an established watcher by its actual IDLE use, not UID page size.
// Hot two-UID pages must still participate in receive-fault injection.
func (s fieldProviderSession) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	if s.receiveRole != nil {
		s.receiveRole.Store(true)
	}
	return s.Session.Idle(w, stop)
}

// This is a declared stress input, not calibrated wire service or field proof.
func newRev1WrapperStress(t *testing.T) *fieldLatencyReplay {
	t.Helper()
	var input struct {
		Schema  int                `json:"schema"`
		Samples map[string][]int64 `json:"operation_samples"`
	}
	if e := json.Unmarshal(rev1WrapperJSON, &input); e != nil || input.Schema != 1 {
		t.Fatal("invalid Rev1 wrapper input", e)
	}
	r := &fieldLatencyReplay{samples: input.Samples, indices: map[string]*atomic.Uint64{}, endpointScoped: true, start: time.Now()}
	for key, values := range input.Samples {
		if len(values) != 64 {
			t.Fatal("invalid quantile sample", key)
		}
		for _, v := range values {
			if v <= 0 {
				t.Fatal("invalid wrapper duration", key)
			}
		}
		r.indices[key] = &atomic.Uint64{}
	}
	limit := int64(loadEnvInt(t, "ITOM_FIELD_WRAPPER_MAX_US", 0, 0, 3000000))
	if limit > 0 {
		removed, total := 0, 0
		for key, values := range r.samples {
			filtered := []int64{}
			for _, v := range values {
				total++
				if v <= limit {
					filtered = append(filtered, v)
				} else {
					removed++
				}
			}
			if len(filtered) == 0 {
				t.Fatal("empty regular-service stratum", key)
			}
			r.samples[key] = filtered
		}
		t.Logf("wrapper_regular_stratum maximum_us=%d excluded_samples=%d total_samples=%d excluded_tail_is_separate_failure_test=true", limit, removed, total)
	}
	t.Log("provider_model=FIELD_DERIVED_WRAPPER_STRESS endpoint_asymmetry=true data_fetch_proxy=control_fetch field_qualified=false")
	return r
}
func TestRev2WrapperStressPreservesEndpointAndAccountSamples(t *testing.T) {
	r := newRev1WrapperStress(t)
	for key, values := range r.samples {
		seen := map[int64]int{}
		want := map[int64]int{}
		for _, v := range values {
			want[v]++
		}
		for range values {
			seen[r.delay(key, 0).Microseconds()]++
		}
		if len(seen) != len(want) {
			t.Fatal("sample cycle lost tail", key)
		}
		for k, v := range want {
			if seen[k] != v {
				t.Fatal("wrong sample multiplicity", key)
			}
		}
	}
	if r.delay("missing", 17*time.Millisecond) != 17*time.Millisecond {
		t.Fatal("missing DATA observation fallback changed")
	}
	if r.delay("a01/append_data", 0) == 0 || r.delay("server/a01/append_data", 0) == 0 {
		t.Fatal("endpoint APPEND missing")
	}
}

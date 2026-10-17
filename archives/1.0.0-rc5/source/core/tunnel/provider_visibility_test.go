package tunnel

import (
	"bytes"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

type visibilityKey struct {
	account, folder string
	uid             imap.UID
}

// Successfully appended UIDs may be allocated before they become visible to
// readers. This is an IMAP replica/index delay, independent of packet loss.
type providerVisibility struct {
	delay      time.Duration
	now        func() time.Time
	mu         sync.Mutex
	until      map[visibilityKey]time.Time
	suppressed atomic.Int64
}

type visibilitySession struct {
	imapserver.Session
	visibility      *providerVisibility
	account, folder string
}

func (s *visibilitySession) Select(folder string, opts *imap.SelectOptions) (*imap.SelectData, error) {
	data, err := s.Session.Select(folder, opts)
	if err == nil {
		s.folder = folder
	}
	return data, err
}

func (s *visibilitySession) Append(folder string, r imap.LiteralReader, opts *imap.AppendOptions) (*imap.AppendData, error) {
	data, err := s.Session.Append(folder, r, opts)
	if err == nil && data != nil && data.UID != 0 && data.UID%11 == 0 {
		v := s.visibility
		v.mu.Lock()
		if v.until == nil {
			v.until = make(map[visibilityKey]time.Time)
		}
		v.until[visibilityKey{s.account, folder, data.UID}] = v.clock().Add(v.delay)
		v.mu.Unlock()
	}
	return data, err
}

func (s *visibilitySession) Fetch(w *imapserver.FetchWriter, set imap.NumSet, opts *imap.FetchOptions) error {
	ids, ok := set.(imap.UIDSet)
	if !ok {
		return s.Session.Fetch(w, set, opts)
	}
	nums, static := ids.Nums()
	if !static {
		return s.Session.Fetch(w, set, opts)
	}
	var visible imap.UIDSet
	for _, uid := range s.visibility.visible(s.account, s.folder, nums) {
		visible.AddNum(uid)
	}
	if len(visible) == 0 {
		return nil
	}
	return s.Session.Fetch(w, visible, opts)
}

func TestSupervisorDelayedUIDVisibilityPreservesStreams(t *testing.T) {
	t.Setenv("ITOM_LOAD_VISIBILITY_DELAY_MS", "400")
	f := newProductionSupervisorFixture(t)
	logical, generation := f.client.logicalSession, f.client.generation
	p := f.openOn(t, "a01")
	_ = p.SetDeadline(time.Now().Add(30 * time.Second))
	want := bytes.Repeat([]byte("real-provider-visibility-gap/"), 40000)
	done := make(chan error, 1)
	go func() { done <- productionWriteFull(p, want) }()
	got := make([]byte, len(want))
	if _, err := io.ReadFull(p, got); err != nil {
		t.Fatal("same application socket failed", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, got) {
		t.Fatal("payload integrity/order")
	}
	for i := 0; i < 20; i++ {
		if err := supervisorEcho(p, i); err != nil {
			t.Fatal("subsequent interactive stream failed", err)
		}
	}
	if f.network.visibility.suppressed.Load() == 0 {
		t.Fatal("visibility fault was not exercised")
	}
	if f.client.logicalSession != logical || f.client.generation != generation {
		t.Fatal("logical session replaced")
	}
	for _, id := range []string{"a01", "a02"} {
		f.waitLane(t, id, 0)
	}
	t.Logf("visibility_delay_ms=400 suppressed_uid_reads=%d same_socket_payload_bytes=%d unexpected_carrier_exits=%d", f.network.visibility.suppressed.Load(), len(want), f.carrierExits.unexpected.Load())
}

func (v *providerVisibility) clock() time.Time {
	if v.now != nil {
		return v.now()
	}
	return time.Now()
}
func (v *providerVisibility) visible(account, folder string, nums []imap.UID) []imap.UID {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.clock()
	for key, until := range v.until {
		if !now.Before(until) {
			delete(v.until, key)
		}
	}
	visible := make([]imap.UID, 0, len(nums))
	for _, uid := range nums {
		if until := v.until[visibilityKey{account, folder, uid}]; !until.IsZero() && now.Before(until) {
			v.suppressed.Add(1)
			continue
		}
		visible = append(visible, uid)
	}
	return visible
}

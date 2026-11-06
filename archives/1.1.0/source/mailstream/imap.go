package mailstream

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

type MailAccount struct {
	Reclamation *ReclamationCoordinator `json:"-"`
	ID          string                  `json:"id"`
	Host        string                  `json:"host"`
	Port        int                     `json:"port"`
	Username    string                  `json:"username"`
	Password    string                  `json:"password"`
	Roots       *x509.CertPool          `json:"-"` // trusted test CA; verification always enabled
	DialIP      string                  `json:"dial_ip,omitempty"`
	Dialer      NetworkDialer           `json:"-"`
}
type NetworkDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

// Unverified provider storage is always charged against this finite budget.
// Only audited collection may refund it; unavailable cleanup applies backpressure.
const prototypePhysicalBudget = 64 << 20

// A durable, finite escape hatch for authenticated storage recovery only.
// Ordinary application/control traffic cannot borrow it.
const maintenanceReserve = 64 << 10
const maximumRetentionBudget = prototypePhysicalBudget + maintenanceReserve
const maxMailPlain = 256 << 10

type mailWork struct {
	body     []byte
	control  bool
	feedback bool
	recovery bool
	charge   int
}
type writerOperation struct {
	cleanup     *cleanupJob
	maintenance bool
	enable      bool
	done        chan error
}
type mailCommand struct {
	run  func(*mailPipe) error
	done chan error
}
type mailAccountState struct {
	link                                *IMAPLink
	config                              MailAccount
	ctx                                 context.Context
	cancel                              context.CancelFunc
	mu                                  sync.Mutex
	ready, failed                       bool
	stage                               string
	failure                             string
	startupAttempts                     uint32
	startupRetryAt                      time.Time
	control, writer, data               *mailPipe
	dataDone                            chan struct{}
	attachStop, dataStop                context.CancelFunc
	id                                  SessionID
	attachMu                            sync.Mutex
	fast                                chan mailWork
	bulk                                chan mailWork
	spent                               int
	queuedBytes                         int
	bulkBlocked                         bool
	reserved, recovered                 int
	storageKey                          []byte
	appended, received                  atomic.Uint64
	dataAppendCount, controlAppendCount atomic.Uint64
	dataMIMEBytes, controlMIMEBytes     atomic.Uint64
	appendLatency                       atomic.Int64
	retired                             atomic.Uint64
	segments                            map[segmentKey]*segmentRecord
	remoteSegments                      map[segmentKey]*segmentRecord
	routes                              map[uint32]uint32
	routesChanged                       chan struct{}
	routeSession                        SessionID
	routeConsumed                       uint32
	parking, reclaiming                 atomic.Bool
	collectRemote                       bool // Account.mu: alternate eligible ownership directions
	writerOps                           chan writerOperation
	delivery                            [replaySlots]deliverySample
	streams                             map[uint32]bool
	rtt, rttAt                          time.Duration
	upBytes, downBytes                  atomic.Uint64
	recoveryFailure                     string
	maintenance                         retentionMaintenance
}

type IMAPLink struct {
	lastBulk            atomic.Int64
	reclaimer           *ReclamationCoordinator
	ctx                 context.Context
	cancel              context.CancelFunc
	identity            string
	client              bool
	mu                  sync.Mutex
	accounts            []*mailAccountState
	attached            SessionID
	roundRobin          uint64
	bulkAccount         uint64
	bulkBytes           int
	bulkPackets         int
	bulkStartupBytes    int
	feedbackAccount     uint64
	feedbackPackets     int
	feedbackAt          time.Time
	changed             chan struct{}
	incoming            chan []byte
	wg                  sync.WaitGroup
	closeOnce           sync.Once
	login               chan struct{}
	retirements         chan Retirement
	events              []MailEvent
	eventNext           int
	receivedRoutes      map[receivedRoute]string
	routeKeys           [replaySlots]receivedRoute
	routeNext           int
	peerSession         SessionID
	peers               map[string]bool
	store               *retentionStore
	closeErr            error
	handshakes          map[SessionID]time.Time
	handshakeGeneration uint64
}

func validIdentity(s string) bool {
	if len(s) == 0 || len(s) > 40 {
		return false
	}
	for _, c := range []byte(s) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func NewIMAPLink(ctx context.Context, identity string, accounts []MailAccount, client bool) (*IMAPLink, error) {
	return newIMAPLink(ctx, identity, accounts, client, nil)
}

func newIMAPLink(ctx context.Context, identity string, accounts []MailAccount, client bool, store *retentionStore) (*IMAPLink, error) {
	if !validIdentity(identity) || len(accounts) == 0 || len(accounts) > 20 {
		return nil, ErrProtocol
	}
	seen := map[string]bool{}
	physical := map[string]bool{}
	for _, a := range accounts {
		if !validIdentity(a.ID) || seen[a.ID] || a.Host == "" || a.Port < 1 || a.Port > 65535 || a.Username == "" || a.Password == "" {
			return nil, ErrProtocol
		}
		seen[a.ID] = true
		key := string(retentionKey(a))
		if physical[key] {
			return nil, ErrProtocol
		}
		physical[key] = true
	}
	lifetime, cancel := context.WithCancel(ctx)
	l := &IMAPLink{ctx: lifetime, cancel: cancel, identity: identity, client: client, changed: make(chan struct{}), incoming: make(chan []byte, 512), login: make(chan struct{}, 4)}
	l.store = store
	l.reclaimer = accounts[0].Reclamation
	if l.reclaimer == nil {
		l.reclaimer = NewReclamationCoordinator()
	}
	l.receivedRoutes = map[receivedRoute]string{}
	l.peers = map[string]bool{}
	l.retirements = make(chan Retirement, 64)
	for _, config := range accounts {
		life, stop := context.WithCancel(lifetime)
		a := &mailAccountState{link: l, config: config, ctx: life, cancel: stop, fast: make(chan mailWork, 32), bulk: make(chan mailWork, 16)}
		a.segments = map[segmentKey]*segmentRecord{}
		a.remoteSegments = map[segmentKey]*segmentRecord{}
		a.routes = map[uint32]uint32{}
		a.routesChanged = make(chan struct{})
		a.streams = map[uint32]bool{}
		a.writerOps = make(chan writerOperation, 1)
		a.storageKey = retentionKey(config)
		if store != nil {
			value, err := store.load(a.storageKey)
			if err != nil {
				cancel()
				return nil, err
			}
			a.spent, a.reserved, a.recovered = value, value, value
		}
		l.accounts = append(l.accounts, a)
	}
	for _, a := range l.accounts {
		l.wg.Add(1)
		go func() { defer l.wg.Done(); a.run() }()
	}
	l.wg.Add(1)
	go func() { defer l.wg.Done(); l.collectSegments() }()
	l.wg.Add(1)
	go func() { defer l.wg.Done(); l.maintainRetention() }()
	return l, nil
}

func (l *IMAPLink) Receive() <-chan []byte { return l.incoming }
func (l *IMAPLink) controlFolder(out bool) string {
	uplink := l.client == out
	direction := "s2c"
	if uplink {
		direction = "c2s"
	}
	return "itom3-" + l.identity + "-control-v7-" + direction
}
func (l *IMAPLink) dataFolder(id SessionID, out bool) string {
	direction := "s2c"
	if l.client == out {
		direction = "c2s"
	}
	return "itom3-" + l.identity + "-" + hex.EncodeToString(id[:]) + "-" + direction
}
func (l *IMAPLink) notify() {
	l.mu.Lock()
	close(l.changed)
	l.changed = make(chan struct{})
	l.mu.Unlock()
}
func (l *IMAPLink) Close() error {
	l.closeOnce.Do(func() {
		l.cancel()
		l.wg.Wait()
		for _, a := range l.accounts {
			a.attachMu.Lock()
			a.closeData()
			a.attachMu.Unlock()
		}
		if l.store != nil {
			for _, a := range l.accounts {
				a.mu.Lock()
				l.closeErr = errors.Join(l.closeErr, l.store.put(a.storageKey, a.spent))
				a.mu.Unlock()
			}
			l.closeErr = errors.Join(l.closeErr, l.store.close())
		}
		close(l.incoming)
	})
	return l.closeErr
}

func (l *IMAPLink) Send(ctx context.Context, body []byte, priority bool) error {
	_, err := l.send(ctx, body, priority, nil)
	return err
}

// SendInteractive hedges an isolated small packet over two distinct ready
// accounts. Both copies have identical authenticated ciphertext and nonce;
// receiver replay handling applies it once. Bulk and credit/receipt traffic
// never use this path. An unavailable second account never delays the first.
func (l *IMAPLink) SendInteractive(ctx context.Context, body []byte) error {
	if len(body) > 4096+13+headerSize+16 {
		return l.Send(ctx, body, true)
	}
	first, err := l.send(ctx, body, true, nil)
	if err != nil {
		return err
	}
	if len(l.accounts) > 1 {
		_, _ = l.send(ctx, body, true, first)
	}
	return nil
}

func (l *IMAPLink) send(ctx context.Context, body []byte, priority bool, exclude *mailAccountState) (*mailAccountState, error) {
	kind, _, seq, err := parseHeader(body)
	recovery := err == nil && len(body) <= 512 && (kind == hello || kind == accept || kind == sealed && seq == 0)
	return l.sendClass(ctx, body, priority, exclude, recovery)
}
func (l *IMAPLink) SendHousekeeping(ctx context.Context, body []byte) error {
	_, err := l.sendClass(ctx, body, true, nil, true)
	return err
}

// Feedback remains on the priority/control carrier. Route a short burst to the
// same writer so receipt and WINDOW packets share bounded MIME publications.
// Interactive application/control requests retain their immediate path.
func (l *IMAPLink) SendFeedback(ctx context.Context, body []byte) error {
	_, err := l.sendClassMode(ctx, body, true, nil, false, true)
	return err
}
func (l *IMAPLink) sendClass(ctx context.Context, body []byte, priority bool, exclude *mailAccountState, recovery bool) (*mailAccountState, error) {
	return l.sendClassMode(ctx, body, priority, exclude, recovery, false)
}
func (l *IMAPLink) sendClassMode(ctx context.Context, body []byte, priority bool, exclude *mailAccountState, recovery, feedback bool) (*mailAccountState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l.ctx.Err() != nil {
		return nil, ErrClosed
	}
	kind, packetID, _, parseErr := parseHeader(body)
	if parseErr != nil {
		return nil, parseErr
	}
	l.mu.Lock()
	matched := kind == sealed && l.attached != (SessionID{}) && l.attached == packetID
	var peerAllowed [20]bool
	for i, a := range l.accounts {
		peerAllowed[i] = (!l.client && priority) || !matched || l.peerSession != packetID || len(l.peers) == 0 || l.peers[a.config.ID]
	}
	start := int(l.roundRobin % uint64(len(l.accounts)))
	if feedback {
		if l.feedbackAt.IsZero() || time.Since(l.feedbackAt) >= 2*time.Millisecond || l.feedbackPackets >= 16 {
			l.feedbackAccount = l.roundRobin % uint64(len(l.accounts))
			l.roundRobin++
			l.feedbackAt, l.feedbackPackets = time.Now(), 0
		}
		start = int(l.feedbackAccount)
	} else if priority {
		l.roundRobin++
	} else {
		start = int(l.bulkAccount % uint64(len(l.accounts)))
	}
	if !priority {
		now := time.Now()
		last := l.lastBulk.Swap(now.UnixNano())
		if last == 0 || now.Sub(time.Unix(0, last)) > time.Second {
			l.bulkStartupBytes = initialDeliveryWindow
			l.bulkBytes, l.bulkPackets = 0, 0
		}
	}
	l.mu.Unlock()
	work := mailWork{body: bytes.Clone(body), control: priority, feedback: feedback, recovery: recovery, charge: len(body)*3/2 + 512}
	failed := 0
	for n := 0; n < len(l.accounts); n++ {
		a := l.accounts[(start+n)%len(l.accounts)]
		if !peerAllowed[(start+n)%len(l.accounts)] {
			continue
		}
		if a == exclude || !a.mu.TryLock() {
			continue
		}
		if a.failed {
			failed++
		}
		budget := prototypePhysicalBudget
		if recovery {
			budget = maximumRetentionBudget
		}
		if !priority {
			budget -= reservationQuantum
		} // preserve control and retirement capacity
		available := a.ready && !a.failed && !a.reclaiming.Load() && (priority || a.id != (SessionID{}) && a.writer != nil && !a.parking.Load() && !a.bulkBlocked) && a.spent+a.queuedBytes+work.charge <= budget
		if matched {
			available = available && a.id == packetID
		}
		if !available {
			a.mu.Unlock()
			continue
		}
		q := a.bulk
		if priority {
			q = a.fast
		}
		select {
		case q <- work:
			a.queuedBytes += work.charge
			a.mu.Unlock()
			a.sentPacket(body)
			if feedback {
				l.mu.Lock()
				l.feedbackAccount = uint64((start + n) % len(l.accounts))
				l.feedbackPackets++
				l.mu.Unlock()
			}
			if !priority {
				// Fill one bounded publication quantum before striping to
				// the next account. Per-packet striping left every writer
				// with tiny APPENDs even during a sustained bulk transfer.
				// No timer or minimum batch delays an isolated write; a
				// full queue immediately spills to another ready account.
				l.mu.Lock()
				index := uint64((start + n) % len(l.accounts))
				if l.bulkAccount != index {
					l.bulkAccount, l.bulkBytes, l.bulkPackets = index, 0, 0
				}
				l.bulkBytes += len(body) + 4
				l.bulkPackets++
				// Fill by actual packet size, including bundle framing. TLS
				// records are often much smaller than maxPayload; a fixed
				// worst-case quantum left most client containers half empty.
				quantum := maxMailPlain
				if l.bulkStartupBytes > 0 {
					// Spread the unchanged first flight across ready accounts.
					// Two large publication quanta left most writers idle
					// throughout the first provider round trip.
					quantum = 64 << 10
					l.bulkStartupBytes -= len(body)
				}
				if l.bulkPackets >= 16 || l.bulkBytes+len(body)+4+6 > quantum {
					l.bulkAccount = (index + 1) % uint64(len(l.accounts))
					l.bulkBytes, l.bulkPackets = 0, 0
				}
				l.mu.Unlock()
			}
			return a, nil
		default:
			a.mu.Unlock()
		}
	}
	if failed == len(l.accounts) {
		return nil, ErrClosed
	}
	return nil, ErrCapacity
}

func (l *IMAPLink) Attach(ctx context.Context, id SessionID) error {
	if id == (SessionID{}) {
		return ErrProtocol
	}
	if !l.client {
		l.reclaimer.sessionStarted(l)
	}
	l.mu.Lock()
	l.attached = id
	l.mu.Unlock()
	for {
		results := make(chan error, len(l.accounts))
		started := 0
		for _, a := range l.accounts {
			if !l.peerAllows(a.config.ID, id) {
				continue
			}
			a.mu.Lock()
			ready := a.ready && !a.failed
			done := ready && a.id == id
			a.mu.Unlock()
			if done {
				return nil
			}
			if ready {
				started++
				go func() { results <- a.attach(id) }()
			}
		}
		for range started {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-l.ctx.Done():
				return ErrClosed
			case err := <-results:
				if err == nil {
					return nil
				}
			}
		}
		l.mu.Lock()
		change := l.changed
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-l.ctx.Done():
			return ErrClosed
		case <-change:
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// Detach joins every old DATA reader before releasing physical admission.
// Control discovery stays up; the server parks its extra APPEND connection.
func (l *IMAPLink) Detach(id SessionID) {
	l.mu.Lock()
	if l.attached != id {
		l.mu.Unlock()
		return
	}
	l.attached = SessionID{}
	l.mu.Unlock()
	for _, a := range l.accounts {
		a.mu.Lock()
		stop := a.attachStop
		a.mu.Unlock()
		if stop != nil {
			stop()
		}
	}
	for _, a := range l.accounts {
		a.attachMu.Lock()
		a.mu.Lock()
		current := a.id == id
		a.mu.Unlock()
		if current {
			a.closeData()
		}
		if !l.client {
			_ = a.changeWriter(false)

		}
		a.attachMu.Unlock()
	}
	l.notify()
	if !l.client {
		l.reclaimer.sessionEnded(l)
	}
}

func (a *mailAccountState) fail() {
	a.mu.Lock()
	a.failed = true
	if a.failure == "" {
		a.failure = "MAIL_ACCOUNT_UNAVAILABLE"
	}
	a.ready = false
	a.mu.Unlock()
	a.cancel()
	a.link.notify()
}
func (a *mailAccountState) phase(stage string) {
	a.mu.Lock()
	a.stage = stage
	a.mu.Unlock()
	a.link.notify()
}
func (a *mailAccountState) failedStage(stage string, err error) {
	a.mu.Lock()
	if a.failure == "" {
		a.stage = stage
		if err != nil {
			switch {
			case errors.Is(err, ErrClosed):
				a.failure = "MAIL_CARRIER_CLOSED"
			case errors.Is(err, ErrProtocol):
				a.failure = "MAIL_PROTOCOL"
			case errors.Is(err, ErrAuth):
				a.failure = "MAIL_AUTH"
			case errors.Is(err, ErrCapacity):
				a.failure = "MAIL_CAPACITY"
			case errors.Is(err, ErrStorage):
				a.failure = "MAIL_STORAGE_IO"
			case errors.Is(err, context.Canceled):
				a.failure = "CANCELED"
			default:
				a.failure = "MAIL_ACCOUNT_UNAVAILABLE"
			}
		}
	}
	a.mu.Unlock()
}
func (a *mailAccountState) closeData() {
	a.mu.Lock()
	old, done := a.data, a.dataDone
	stop := a.dataStop
	a.dataStop = nil
	a.data = nil
	a.dataDone = nil
	a.id = SessionID{}
	a.mu.Unlock()
	if stop != nil {
		stop()
	}
	if old != nil {
		old.close()
	}
	if done != nil {
		<-done
	}
}
func (a *mailAccountState) attach(id SessionID) error {
	if !a.link.peerAllows(a.config.ID, id) {
		return ErrCapacity
	}
	a.attachMu.Lock()
	defer a.attachMu.Unlock()
	a.link.mu.Lock()
	current := a.link.attached
	a.link.mu.Unlock()
	if current != id {
		return ErrClosed
	}
	a.mu.Lock()
	if a.failed || !a.ready {
		a.mu.Unlock()
		return ErrClosed
	}
	if a.id == id {
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()
	a.closeData()
	life, stop := context.WithCancel(a.ctx)
	attached := false
	a.mu.Lock()
	a.attachStop = stop
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.attachStop = nil
		a.mu.Unlock()
		if !attached {
			stop()
		}
	}()
	if !a.link.client {
		if err := a.changeWriter(true); err != nil {
			return err
		}
	}
	a.beginEpoch(id)
	p, err := openMailPipe(life, a.config)
	if err != nil {
		if life.Err() != nil {
			return ErrClosed
		}
		a.failedStage("data_login", err)
		a.fail()
		return err
	}
	stopSelect := p.limit(15 * time.Second)
	defer stopSelect()
	if _, err = p.selectLog(a.link.segmentFolder(id, false, 0), false); err != nil {
		if life.Err() != nil {
			p.close()
			return ErrClosed
		}
		a.failedStage("data_select", err)
		p.close()
		a.fail()
		return err
	}
	stopSelect()
	if !a.link.client {
		p.fetchBatch = 32
	} // Server upload reader amortizes slow body FETCH.
	a.configureSegments(p, id)
	p.onPacket = func(packet []byte) {
		if len(packet) > 4096 {
			a.link.lastBulk.Store(time.Now().UnixNano())
		}
		a.downBytes.Add(uint64(len(packet)))
		a.link.note("received", a.config.ID, packet, p.generation, false)
	}
	a.link.mu.Lock()
	current = a.link.attached
	a.link.mu.Unlock()
	if current != id {
		p.close()
		return ErrClosed
	}
	done := make(chan struct{})
	a.mu.Lock()
	a.data, a.dataDone, a.id = p, done, id
	a.dataStop = stop
	attached = true
	clear(a.streams)
	a.mu.Unlock()
	go func() {
		defer close(done)
		if err := p.receive(a.ctx, a.link.incoming); err != nil && a.ctx.Err() == nil {
			a.mu.Lock()
			current := a.data == p
			a.mu.Unlock()
			if current {
				a.failedStage("data_receive", err)
				a.fail()
			}
		}
	}()
	a.link.notify()
	return nil
}

func (a *mailAccountState) changeWriter(enable bool) error {
	a.mu.Lock()
	interrupt := a.maintenance.cancel
	a.mu.Unlock()
	if interrupt != nil {
		interrupt()
	}
	a.parking.Store(!enable)
	if !enable {
		a.mu.Lock()
		writer := a.writer
		a.mu.Unlock()
		if writer != nil {
			writer.conn.NetConn().Close()
		}
	}
	op := writerOperation{enable: enable, done: make(chan error, 1)}
	select {
	case a.writerOps <- op:
	case <-a.ctx.Done():
		return ErrClosed
	}
	select {
	case err := <-op.done:
		return err
	case <-a.ctx.Done():
		return ErrClosed
	}
}

func (a *mailAccountState) run() {
	defer a.fail()
	control, writer, err := a.startControls()
	if err != nil {
		return
	}
	defer control.close()
	var appendWait sync.WaitGroup
	defer func() {
		if writer != nil {
			writer.close()
		}
		appendWait.Wait()
	}()
	a.mu.Lock()
	a.control, a.writer, a.ready = control, writer, true
	a.stage, a.failure = "serving", ""
	a.mu.Unlock()
	a.link.notify()
	a.link.mu.Lock()
	id := a.link.attached
	a.link.mu.Unlock()
	if id != (SessionID{}) {
		if a.link.peerAllows(a.config.ID, id) {
			go func() { _ = a.attach(id) }()
		}
	}
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		if err := control.receive(a.ctx, a.link.incoming); err != nil {
			a.failedStage("control_receive", err)
			a.fail()
		}
	}()
	defer func() { a.cancel(); control.close(); <-readDone }()
	created := map[string]bool{a.link.controlFolder(true): true}
	dataAppends := map[SessionID]uint32{}
	completed := make(chan error, 4)
	inFlight := 0
	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()
	lastWrite := time.Now()
	var lastBulkWrite time.Time
	var carry *mailWork
	var operation *writerOperation
	for {
		if operation == nil {
			select {
			case op := <-a.writerOps:
				operation = &op
			default:
			}
		}
		if operation != nil {
			if operation.maintenance {
				a.processMaintenance(&writer, completed, &inFlight, &appendWait)
				operation = nil
				continue
			}
			if operation.cleanup != nil {
				a.processCleanup(*operation.cleanup, &writer, completed, &inFlight, &appendWait)
				operation = nil
				continue
			}
			// Only the APPEND owner opens/closes its writer. Drain tagged
			// completions before returning the physical slot to admission.
			for inFlight > 0 {
				select {
				case err := <-completed:
					inFlight--
					if err != nil && !a.parking.Load() {
						operation.done <- err
						return
					}
				case <-a.ctx.Done():
					operation.done <- ErrClosed
					return
				}
			}
			if operation.enable && writer == nil {
				select {
				case a.link.login <- struct{}{}:
				case <-a.ctx.Done():
					return
				}
				writer, err = openMailPipe(a.ctx, a.config)
				<-a.link.login
				if err != nil && !a.parking.Load() {
					operation.done <- err
					a.failedStage("writer_login", err)
					return
				}
			} else if !operation.enable && writer != nil {
				writer.close()
				writer = nil
			}
			a.mu.Lock()
			a.writer = writer
			a.mu.Unlock()
			operation.done <- nil
			operation = nil
			continue
		}
		if inFlight == 4 {
			select {
			case <-a.ctx.Done():
				return
			case err := <-completed:
				inFlight--
				if err != nil && !a.parking.Load() {
					return
				}
			}
		}
		var work mailWork
		select {
		case err := <-completed:
			inFlight--
			if err != nil && !a.parking.Load() {
				return
			}
			continue
		case work = <-a.fast:
		default:
			if carry != nil {
				work = *carry
				carry = nil
			} else {
				select {
				case <-a.ctx.Done():
					return
				case op := <-a.writerOps:
					operation = &op
					continue
				case <-keepalive.C:
					if writer != nil && inFlight == 0 && time.Since(lastWrite) >= 30*time.Second {
						// An unselected APPEND connection cannot use IDLE.
						// Keep its login alive without blocking the fast queue
						// while waiting for the tagged NOOP completion.
						cmd := writer.client.Noop()
						lastWrite = time.Now()
						inFlight++
						appendWait.Add(1)
						go func() {
							defer appendWait.Done()
							err := cmd.Wait()
							if err != nil && !a.parking.Load() && !a.reclaiming.Load() {
								a.failedStage("writer_keepalive", err)
							}
							select {
							case completed <- err:
							case <-a.ctx.Done():
							}
						}()
					}
					continue
				case err := <-completed:
					inFlight--
					if err != nil && !a.parking.Load() {
						return
					}
					continue
				case work = <-a.fast:
				case work = <-a.bulk:
				}
			}
		}
		folder := a.link.controlFolder(true)
		var dataID SessionID
		var generation uint32
		if !work.control {
			_, packetID, _, err := parseHeader(work.body)
			if err != nil {
				return
			}
			dataID = packetID
			a.link.mu.Lock()
			current := a.link.attached
			a.link.mu.Unlock()
			if packetID != current {
				a.mu.Lock()
				a.queuedBytes -= work.charge
				a.mu.Unlock()
				continue
			}
			generation = dataAppends[packetID] / segmentAppends
			a.mu.Lock()
			record, prepareErr := a.prepareSegmentLocked(packetID, generation)
			if prepareErr == nil {
				folder = record.folder
			}
			a.mu.Unlock()
			if prepareErr != nil {
				carry = &work
				select {
				case <-a.ctx.Done():
					return
				case <-time.After(time.Millisecond):
				}
				continue
			}

		}
		// Interactive requests never wait. Bulk and feedback may briefly fill
		// their already-bounded MIME container to amortize provider command cost.
		packets := [][]byte{work.body}
		queuedCharge := work.charge
		batchBytes := 6 + 4 + len(work.body)
		q := a.bulk
		if work.control {
			q = a.fast
		}
		var coalesce *time.Timer
		var coalesced <-chan time.Time
		if work.feedback {
			coalesce = time.NewTimer(2 * time.Millisecond)
			coalesced = coalesce.C
		}
		if !work.control {
			// Spend at most eight milliseconds on an established bulk
			// container when tagged APPEND service is expensive. Small
			// interactive control bypasses this timer.
			delay := 2 * time.Millisecond
			if inFlight > 0 && time.Since(lastBulkWrite) < 250*time.Millisecond {
				delay = min(8*time.Millisecond, max(time.Millisecond, time.Duration(a.appendLatency.Load())/16))
			}
			coalesce = time.NewTimer(delay)
			coalesced = coalesce.C
		}
	collect:
		for len(packets) < 16 && carry == nil {
			var next mailWork
			select {
			case next = <-q:
			default:
				if coalesced == nil || !work.control && len(a.fast) > 0 {
					break collect
				}
				select {
				case next = <-q:
				case <-coalesced:
					break collect
				case <-a.ctx.Done():
					coalesce.Stop()
					return
				}
			}
			_, firstID, _, e1 := parseHeader(work.body)
			_, nextID, _, e2 := parseHeader(next.body)
			if e1 != nil || e2 != nil {
				if coalesce != nil {
					coalesce.Stop()
				}
				return
			}
			if next.feedback != work.feedback || next.recovery != work.recovery || batchBytes+4+len(next.body) > maxMailPlain || !work.control && firstID != nextID {
				carry = &next
				break collect
			}
			packets = append(packets, next.body)
			queuedCharge += next.charge
			batchBytes += 4 + len(next.body)
		}
		if coalesce != nil {
			coalesce.Stop()
		}
		encoded := encodeMail(packMail(packets))
		a.mu.Lock()
		overBudget := a.spent+len(encoded) > maximumRetentionBudget
		a.mu.Unlock()
		if overBudget {
			a.failedStage("retention_admission_invariant", ErrStorage)
			return
		}
		if writer == nil {
			if !work.control {
				a.mu.Lock()
				a.queuedBytes -= queuedCharge
				a.mu.Unlock()
				continue
			} // queued DATA from a replaced session
			a.mu.Lock()
			a.queuedBytes -= queuedCharge
			chargeErr := a.chargeLocked(len(encoded))
			a.mu.Unlock()
			if chargeErr != nil {
				a.failedStage("storage_charge", chargeErr)
				return
			}
			// Before admission the server owns one selected control socket.
			// Its receive owner serializes this small bootstrap APPEND with
			// IDLE. Active traffic always has an independent APPEND writer.
			appendStarted := time.Now()
			err := control.execute(a.ctx, func(p *mailPipe) error {
				cmd, err := p.startAppend(folder, encoded)
				if err != nil {
					return err
				}
				result, err := cmd.Wait()
				if err == nil {
					a.controlAppended(result, len(encoded))
				}
				return err
			})
			if err != nil {
				a.failedStage("bootstrap_append", err)
				return
			}
			a.appendCompleted(true, len(encoded), appendStarted)
			for _, packet := range packets {
				a.upBytes.Add(uint64(len(packet)))
			}
			continue
		}
		if !created[folder] {
			// Both authenticated endpoints may create the same new namespace.
			// Tagged CREATE refusal is tolerated; APPEND's actual completion is
			// still required and failures retire only this account.
			if err := writer.client.Create(folder, nil).Wait(); err != nil {
				var status *imap.Error
				if !errors.As(err, &status) {
					if a.parking.Load() {
						a.mu.Lock()
						a.queuedBytes -= queuedCharge
						a.mu.Unlock()
						continue
					}
					return
				}
			}
			created[folder] = true
		}
		a.mu.Lock()
		a.queuedBytes -= queuedCharge
		if err := a.chargeLocked(len(encoded)); err != nil {
			a.mu.Unlock()
			a.failedStage("storage_charge", err)
			return
		} // unknown APPEND is charged, never assumed absent
		var segment *segmentRecord
		if !work.control {
			key := segmentKey{dataID, generation}
			record := a.segments[key]
			record.bytes += len(encoded)
			record.appendPending++
			record.appends++
			segment = record
			for _, packet := range packets {
				_, _, seq, _ := parseHeader(packet)
				record.awaiting[seq] = true
			}
			dataAppends[dataID]++
		}
		a.mu.Unlock()
		for _, packet := range packets {
			a.link.note("append", a.config.ID, packet, generation, work.control)
		}
		appendStarted := time.Now()
		cmd, startErr := writer.startAppend(folder, encoded)
		lastWrite = time.Now()
		if !work.control {
			lastBulkWrite = lastWrite
		}
		if startErr != nil {
			if a.parking.Load() {
				continue
			}
			return
		}
		inFlight++
		appendWait.Add(1)
		go func() {
			defer appendWait.Done()
			result, err := cmd.Wait()
			if err == nil {
				if segment == nil {
					a.controlAppended(result, len(encoded))
				}
				if segment != nil {
					a.mu.Lock()
					segment.appendPending--
					if result == nil || result.UID == 0 || result.UIDValidity == 0 || segment.validity != 0 && segment.validity != result.UIDValidity {
						segment.proofUnknown = true
					} else {
						segment.validity = result.UIDValidity
						segment.uids.AddNum(result.UID)
					}
					a.mu.Unlock()
				}
				a.appendCompleted(segment == nil, len(encoded), appendStarted)
				for _, packet := range packets {
					a.upBytes.Add(uint64(len(packet)))
				}
			}
			select {
			case completed <- err:
			case <-a.ctx.Done():
			}
		}()
	}
}

type MailSnapshot struct {
	Reclamation    ReclamationSnapshot   `json:"reclamation"`
	Accounts       int                   `json:"accounts"`
	Ready          int                   `json:"ready_accounts"`
	Writers        int                   `json:"writer_accounts"`
	Data           int                   `json:"data_accounts"`
	Failed         int                   `json:"failed_accounts"`
	Appended       uint64                `json:"appended_messages"`
	Retired        uint64                `json:"retired_segments"`
	RetainedBytes  int                   `json:"retained_bytes"`
	ReservedBytes  int                   `json:"reserved_retention_bytes"`
	RecoveredBytes int                   `json:"recovered_retention_bytes"`
	Durable        bool                  `json:"retention_durable"`
	Lines          []MailAccountSnapshot `json:"lines"`
}
type MailAccountSnapshot struct {
	ControlOutgoingBytes       int    `json:"control_outgoing_retained_bytes"`
	ControlIncomingBytes       int    `json:"control_incoming_retained_bytes"`
	ControlOutstanding         int    `json:"control_outstanding"`
	ControlCollected           uint64 `json:"control_messages_collected"`
	AbandonedEmptied           uint64 `json:"abandoned_folders_emptied"`
	StorageRecoveryDeferred    bool   `json:"storage_recovery_deferred"`
	StoragePressure            bool   `json:"storage_pressure"`
	PeerStoragePressure        bool   `json:"peer_storage_pressure"`
	StorageRecoveryReady       bool   `json:"storage_recovery_ready"`
	MaintenanceFailure         string `json:"maintenance_failure,omitempty"`
	Housekeeping               bool   `json:"housekeeping"`
	RecoveryFailure            string `json:"recovery_failure,omitempty"`
	ControlPhase               int32  `json:"control_phase"`
	ControlCursor              uint32 `json:"control_cursor"`
	ControlCount               uint32 `json:"control_count"`
	DataAppended               uint64 `json:"data_appended_messages"`
	ControlAppended            uint64 `json:"control_appended_messages"`
	DataMIMEBytes              uint64 `json:"data_mime_bytes"`
	ControlMIMEBytes           uint64 `json:"control_mime_bytes"`
	AppendCompletionMS         int64  `json:"append_completion_ms"`
	QueuedMIMEBytes            int    `json:"queued_mime_bytes"`
	StartupAttempts            uint32 `json:"startup_attempts"`
	StartupRetryAtMS           int64  `json:"startup_retry_at_ms,omitempty"`
	PeerVerified               bool   `json:"peer_verified"`
	Account                    string `json:"account"`
	Stage                      string `json:"stage"`
	Failure                    string `json:"failure,omitempty"`
	Ready                      bool   `json:"ready"`
	Writer                     bool   `json:"writer"`
	Data                       bool   `json:"data"`
	RetainedBytes              int    `json:"retained_bytes"`
	ReservedBytes              int    `json:"reserved_retention_bytes"`
	RecoveredBytes             int    `json:"recovered_retention_bytes"`
	DataPhase                  int32  `json:"data_phase"`
	DataCursor                 uint32 `json:"data_cursor"`
	DataCount                  uint32 `json:"data_count"`
	DataGeneration             uint32 `json:"data_generation"`
	RTTMillis                  int64  `json:"rtt_ms"`
	RTTFresh                   bool   `json:"rtt_fresh"`
	IMAPLatencyMillis          int64  `json:"imap_latency_ms"`
	UploadBytes, DownloadBytes uint64
}

func (l *IMAPLink) Snapshot() MailSnapshot {
	s := MailSnapshot{Accounts: len(l.accounts), Durable: l.store != nil, Reclamation: l.reclaimer.snapshot()}
	l.mu.Lock()
	var verified [20]bool
	for i, a := range l.accounts {
		verified[i] = l.peers[a.config.ID]
	}
	l.mu.Unlock()
	for i, a := range l.accounts {
		a.mu.Lock()
		if a.ready {
			s.Ready++
		}
		writer := !a.failed && a.writer != nil
		if writer {
			s.Writers++
		}
		if !a.failed && a.ready && a.id != (SessionID{}) {
			s.Data++
		}
		if a.failed {
			s.Failed++
		}
		s.RetainedBytes += a.spent
		s.ReservedBytes += a.reserved
		s.RecoveredBytes += a.recovered
		line := MailAccountSnapshot{Housekeeping: a.reclaiming.Load(), Account: a.config.ID, Stage: a.stage, Failure: a.failure, Ready: a.ready, Writer: writer, Data: !a.failed && a.ready && a.id != (SessionID{}), RetainedBytes: a.spent}
		line.DataAppended, line.ControlAppended = a.dataAppendCount.Load(), a.controlAppendCount.Load()
		line.DataMIMEBytes, line.ControlMIMEBytes = a.dataMIMEBytes.Load(), a.controlMIMEBytes.Load()
		line.AppendCompletionMS = time.Duration(a.appendLatency.Load()).Milliseconds()
		line.QueuedMIMEBytes = a.queuedBytes
		line.StartupAttempts = a.startupAttempts
		if !a.startupRetryAt.IsZero() {
			line.StartupRetryAtMS = a.startupRetryAt.UnixMilli()
		}
		line.ReservedBytes, line.RecoveredBytes = a.reserved, a.recovered
		line.ControlOutstanding = len(a.maintenance.writes)
		line.ControlOutgoingBytes = a.maintenance.controlBytes
		line.ControlIncomingBytes = a.maintenance.incomingBytes
		line.ControlCollected = a.maintenance.controlMessages
		line.AbandonedEmptied = a.maintenance.emptiedFolders
		line.StorageRecoveryDeferred = !a.maintenance.recoveryReady && !a.maintenance.recovered && !a.historyRequiredLocked()
		line.StoragePressure = a.storagePressureLocked()
		line.PeerStoragePressure = a.maintenance.peerPressure
		line.StorageRecoveryReady = a.maintenance.recoveryReady || a.maintenance.recovered
		line.MaintenanceFailure = a.maintenance.failure
		line.PeerVerified = verified[i]
		line.RecoveryFailure = a.recoveryFailure
		if a.control != nil {
			line.ControlPhase = a.control.phase.Load()
			line.ControlCursor = a.control.cursor.Load()
			if box := a.control.client.Mailbox(); box != nil {
				line.ControlCount = box.NumMessages
			}
		}
		line.RTTMillis = measuredMillis(a.rtt)
		line.RTTFresh = a.rttAt != 0 && time.Duration(time.Now().UnixNano())-a.rttAt < 30*time.Second
		line.UploadBytes, line.DownloadBytes = a.upBytes.Load(), a.downBytes.Load()
		if a.control != nil {
			line.IMAPLatencyMillis = measuredMillis(time.Duration(a.control.latency.Load()))
		}
		if p := a.data; p != nil {
			line.DataPhase = p.phase.Load()
			line.DataCursor = p.cursor.Load()
			line.DataGeneration = p.generationDebug.Load()
			if selected := p.client.Mailbox(); selected != nil {
				line.DataCount = selected.NumMessages
			}
		}
		s.Lines = append(s.Lines, line)
		a.mu.Unlock()
		s.Appended += a.appended.Load()
		s.Retired += a.retired.Load()
	}
	return s
}

type mailPipe struct {
	fetchBatch        uint32 // immutable after attachment; zero preserves the eight-container default
	client            *imapclient.Client
	conn              *tls.Conn
	cancel            func() bool
	wake              chan struct{}
	invalid           atomic.Bool
	after             uint32
	done              chan struct{}
	closeOnce         sync.Once
	commands          chan mailCommand
	rotate            func(context.Context) error
	onPacket          func([]byte)
	onControlConsumed func(imap.UID, uint32, int)
	generation        uint32
	phase             atomic.Int32
	cursor            atomic.Uint32
	generationDebug   atomic.Uint32
	confirmed         uint32
	consumed          imap.UIDSet
	allowExpunge      atomic.Bool
	validity          uint32
	service           time.Duration
	knownFolders      map[string]bool // SELECT owner only; never shared with a writer
	uidControl        atomic.Bool
	latency           atomic.Int64
}

func openMailPipe(ctx context.Context, a MailAccount) (*mailPipe, error) {
	return openMailPipeAttempt(ctx, ctx, a)
}
func openMailPipeAttempt(attempt, ctx context.Context, a MailAccount) (*mailPipe, error) {
	dial, stop := context.WithTimeout(attempt, 15*time.Second)
	defer stop()
	var d NetworkDialer = a.Dialer
	if d == nil {
		d = &net.Dialer{}
	}
	address := a.Host
	if a.DialIP != "" {
		address = a.DialIP
	}
	raw, err := d.DialContext(dial, "tcp", net.JoinHostPort(address, strconv.Itoa(a.Port)))
	if err != nil {
		return nil, ErrClosed
	}
	conn := tls.Client(raw, &tls.Config{ServerName: a.Host, RootCAs: a.Roots, MinVersion: tls.VersionTLS12})
	stopDialIO := context.AfterFunc(dial, func() { raw.Close() })
	defer stopDialIO()
	if err = conn.HandshakeContext(dial); err != nil {
		raw.Close()
		return nil, ErrAuth
	}
	p := &mailPipe{conn: conn, wake: make(chan struct{}, 1), done: make(chan struct{})}
	p.commands = make(chan mailCommand, 1)

	changed := func() {
		select {
		case p.wake <- struct{}{}:
		default:
		}
	}
	p.client = imapclient.New(conn, &imapclient.Options{UnilateralDataHandler: &imapclient.UnilateralDataHandler{Mailbox: func(*imapclient.UnilateralDataMailbox) { changed() }, Expunge: func(uint32) {
		if !p.allowExpunge.Load() && !p.uidControl.Load() {
			p.invalid.Store(true)
		}
		changed()
	}}})
	p.cancel = context.AfterFunc(ctx, func() { raw.Close(); p.client.Close() })
	if err = p.client.Login(a.Username, a.Password).Wait(); err != nil {
		p.close()
		return nil, ErrAuth
	}
	if !stopDialIO() {
		p.close()
		return nil, ErrAuth
	}
	return p, nil
}

// The IMAP decoder changes read deadlines internally; a socket deadline alone
// cannot bound LOGIN/SELECT. A timer interrupts the underlying transport.
func (p *mailPipe) limit(d time.Duration) func() bool {
	timer := time.AfterFunc(d, func() { p.conn.NetConn().Close(); p.client.Close() })
	return timer.Stop
}

func (p *mailPipe) close() {
	p.closeOnce.Do(func() {
		close(p.done)
		p.cancel()
		// TLS CloseNotify can wait for a peer which is stuck in an IMAP
		// command. Close the underlying transport first so reader teardown
		// never serializes ten five-second TLS shutdown waits.
		p.conn.NetConn().Close()
		p.client.Close()
	})
}
func (p *mailPipe) execute(ctx context.Context, run func(*mailPipe) error) error {
	cmd := mailCommand{run: run, done: make(chan error, 1)}
	select {
	case p.commands <- cmd:
	case <-p.done:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-cmd.done:
		return err
	case <-p.done:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (p *mailPipe) runCommand(cmd mailCommand) error {
	err := cmd.run(p)
	cmd.done <- err
	return err
}
func (p *mailPipe) selectLog(folder string, tail bool) (*imap.SelectData, error) {
	begin := time.Now()
	p.phase.Store(2)
	var selected *imap.SelectData
	var err error
	if tail {
		// Existing control folders must be opened without a redundant
		// CREATE. Some providers serialize or stall that metadata mutation
		// even though SELECT/STATUS on the same folder are responsive.
		selected, err = p.client.Select(folder, nil).Wait()
		if err != nil && !missingMailbox(err) {
			if taggedNo(err) {
				return nil, ErrProtocol
			}
			return nil, ErrClosed
		}
		if err != nil {
			selected = nil
		}
	}
	if selected == nil {
		if !p.knownFolders[folder] {
			if err := p.client.Create(folder, nil).Wait(); err != nil && !taggedNo(err) {
				return nil, ErrClosed
			}
		}
		selected, err = p.client.Select(folder, nil).Wait()
	}
	if err != nil {
		if !taggedNo(err) {
			return nil, ErrClosed
		}
		return nil, ErrProtocol
	}
	if selected == nil || selected.UIDValidity == 0 {
		return nil, ErrProtocol
	}
	if p.knownFolders == nil {
		p.knownFolders = make(map[string]bool)
	}
	p.knownFolders[folder] = true
	p.after = 0
	p.confirmed = selected.NumMessages
	p.validity = selected.UIDValidity
	p.service = time.Since(begin)
	p.consumed = nil
	p.latency.Store(int64(p.service))
	if tail {
		p.after = selected.NumMessages
		if p.uidControl.Load() {
			if selected.UIDNext == 0 {
				return nil, ErrProtocol
			}
			p.after = uint32(selected.UIDNext) - 1
		}
	}
	p.cursor.Store(p.after)
	return selected, nil
}
func (p *mailPipe) startAppend(folder string, body []byte) (*imapclient.AppendCommand, error) {
	cmd := p.client.Append(folder, int64(len(body)), nil)
	if _, err := cmd.Write(body); err != nil {
		return nil, ErrClosed
	}
	if err := cmd.Close(); err != nil {
		return nil, ErrClosed
	}
	return cmd, nil
}
func (p *mailPipe) receive(ctx context.Context, out chan<- []byte) error {
	if p.uidControl.Load() {
		return p.receiveControl(ctx, out)
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if p.invalid.Load() {
			return ErrProtocol
		}
		select {
		case cmd := <-p.commands:
			if err := p.runCommand(cmd); err != nil {
				return err
			}
		default:
		}
		mbox := p.client.Mailbox()
		if mbox == nil {
			return ErrProtocol
		}
		messages := max(mbox.NumMessages, p.confirmed)
		if messages < p.after {
			return ErrProtocol
		}
		if p.rotate != nil && p.after == segmentAppends {
			if messages != segmentAppends {
				return ErrProtocol
			}
			if err := p.rotate(ctx); err != nil {
				return err
			}
			continue
		}
		if messages > p.after {
			p.phase.Store(1)
			batch := p.fetchBatch
			if batch == 0 {
				batch = 8
			}
			// Fetch only the available backlog. Small reads never wait for a batch.
			stop := min(messages, p.after+batch)
			if p.rotate != nil {
				stop = min(stop, segmentAppends)
			}
			cmd := p.client.Fetch(imap.SeqSet{{Start: p.after + 1, Stop: stop}}, &imap.FetchOptions{UID: true, RFC822Size: true, BodySection: []*imap.FetchItemBodySection{{Peek: true}}})
			for row := cmd.Next(); row != nil; row = cmd.Next() {
				if p.invalid.Load() || row.SeqNum != p.after+1 || row.SeqNum > stop {
					p.conn.NetConn().Close()
					cmd.Close()
					return ErrProtocol
				}
				var raw []byte
				size := int64(-1)
				for item := row.Next(); item != nil; item = row.Next() {
					switch value := item.(type) {
					case imapclient.FetchItemDataUID:
						if p.rotate != nil {
							if value.UID == 0 {
								return ErrProtocol
							}
							p.consumed.AddNum(value.UID)
						}
					case imapclient.FetchItemDataRFC822Size:
						size = value.Size
					case imapclient.FetchItemDataBodySection:
						if raw != nil || value.Literal == nil {
							p.conn.NetConn().Close()
							cmd.Close()
							return ErrProtocol
						}
						var err error
						raw, err = io.ReadAll(io.LimitReader(value.Literal, 2*maxMailPlain+1))
						if err != nil || len(raw) > 2*maxMailPlain {
							p.conn.NetConn().Close()
							cmd.Close()
							return ErrCapacity
						}
					}
				}
				if raw == nil || int64(len(raw)) != size {
					p.conn.NetConn().Close()
					cmd.Close()
					return ErrProtocol
				}
				packets, err := decodeMail(raw)
				if err == nil {
					for _, body := range packets {
						if p.onPacket != nil {
							p.onPacket(body)
						}
						select {
						case out <- body:
						case <-p.done:
							p.conn.NetConn().Close()
							cmd.Close()
							return ErrClosed
						case <-ctx.Done():
							p.conn.NetConn().Close()
							cmd.Close()
							return ctx.Err()
						}
					}
				}
				p.after = row.SeqNum
				p.cursor.Store(p.after)
			}
			if err := cmd.Close(); err != nil {
				return ErrClosed
			}
			if p.after != stop {
				return ErrProtocol
			}
			continue
		}
		// A control reader polls without entering IDLE. DONE followed by
		// FETCH otherwise adds a whole extra provider round trip, and an
		// update queued just before IDLE can be held until a repair tick.
		// DATA retains IDLE and batch FETCH for high-throughput delivery.
		if p.rotate != nil && p.client.Caps().Has(imap.CapIdle) {
			p.phase.Store(3)
			idle, err := p.client.Idle()
			if err != nil {
				return ErrClosed
			}
			idleDone := make(chan error, 1)
			go func() { idleDone <- idle.Wait() }()
			repair := time.NewTimer(min(100*time.Millisecond, max(25*time.Millisecond, 4*p.service)))
			refresh := false
			// An EXISTS arriving between the check above and IDLE must not
			// sleep until another message. The bounded wake edge is retained.
			select {
			case <-p.done:
				repair.Stop()
				idle.Close()
				<-idleDone
				return ErrClosed
			case <-ctx.Done():
				repair.Stop()
				p.conn.NetConn().Close()
				idle.Close()
				<-idleDone
				return ctx.Err()
			case <-idleDone:
				repair.Stop()
				return ErrClosed
			case cmd := <-p.commands:
				repair.Stop()
				if err = idle.Close(); err != nil {
					<-idleDone
					return ErrClosed
				}
				<-idleDone
				if err = p.runCommand(cmd); err != nil {
					return err
				}
				continue
			case <-p.wake:
			case <-repair.C:
				refresh = true
			}
			repair.Stop()
			if err = idle.Close(); err != nil {
				<-idleDone
				return ErrClosed
			}
			<-idleDone
			if refresh {
				// A timer and EXISTS may become ready together. Freshly
				// announced mail does not need a second tail-discovery round
				// trip. Missing notifications still require a fresh selection audit.
				if current := p.client.Mailbox(); current != nil && current.Name == mbox.Name && current.NumMessages > p.after {
					continue
				}
				// EXISTS/IDLE is a wake hint, not proof of the log's tail. In
				// particular the final APPEND of a sealed segment must not be
				// stranded forever by a lost or coalesced notification.
				p.phase.Store(6)
				begin := time.Now()
				status, err := p.inventoryMailbox(mbox.Name, &imap.StatusOptions{NumMessages: true, UIDValidity: true})
				if err != nil {
					return ErrClosed
				}
				if status.NumMessages == nil || status.UIDValidity != p.validity || *status.NumMessages < p.after {
					return ErrProtocol
				}
				p.confirmed = *status.NumMessages
				p.service = (3*p.service + time.Since(begin)) / 4
			}
		} else {
			begin := time.Now()
			if err := p.client.Noop().Wait(); err != nil {
				return ErrClosed
			}
			p.latency.Store(int64(time.Since(begin)))
			delay := min(5*time.Millisecond, max(time.Millisecond, time.Since(begin)/16))
			select {
			case <-p.done:
				return ErrClosed
			case <-ctx.Done():
				return ctx.Err()
			case <-p.wake:
			case <-time.After(delay):
			case cmd := <-p.commands:
				if err := p.runCommand(cmd); err != nil {
					return err
				}
			}
		}
	}
}

func encodeMail(body []byte) []byte {
	var out bytes.Buffer
	out.WriteString("From: transport@itom.invalid\r\nTo: transport@itom.invalid\r\nSubject: ITOM3\r\nMIME-Version: 1.0\r\nContent-Type: application/octet-stream\r\nContent-Transfer-Encoding: base64\r\n\r\n")
	b := base64.StdEncoding.EncodeToString(body)
	for len(b) > 0 {
		n := min(76, len(b))
		out.WriteString(b[:n])
		out.WriteString("\r\n")
		b = b[n:]
	}
	return out.Bytes()
}
func decodeMail(raw []byte) ([][]byte, error) {
	if len(raw) > 2*maxMailPlain {
		return nil, ErrCapacity
	}
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, ErrProtocol
	}
	if message.Header.Get("Content-Type") != "application/octet-stream" || !strings.EqualFold(message.Header.Get("Content-Transfer-Encoding"), "base64") {
		return nil, ErrProtocol
	}
	body, err := io.ReadAll(io.LimitReader(base64.NewDecoder(base64.StdEncoding, message.Body), maxMailPlain+1))
	if err != nil || len(body) > maxMailPlain {
		return nil, ErrProtocol
	}
	if bytes.HasPrefix(body, []byte("ITM3")) {
		if _, _, _, err = parseHeader(body); err != nil {
			return nil, err
		}
		return [][]byte{body}, nil
	}
	if len(body) < 6 || !bytes.Equal(body[:4], []byte("ITB3")) {
		return nil, ErrProtocol
	}
	n := int(binary.BigEndian.Uint16(body[4:6]))
	if n < 2 || n > 16 {
		return nil, ErrProtocol
	}
	body = body[6:]
	packets := make([][]byte, 0, n)
	for range n {
		if len(body) < 4 {
			return nil, ErrProtocol
		}
		size := int(binary.BigEndian.Uint32(body[:4]))
		body = body[4:]
		if size > len(body) {
			return nil, ErrProtocol
		}
		packet := body[:size]
		body = body[size:]
		if _, _, _, err = parseHeader(packet); err != nil {
			return nil, err
		}
		packets = append(packets, packet)
	}
	if len(body) != 0 {
		return nil, ErrProtocol
	}
	return packets, nil
}
func packMail(packets [][]byte) []byte {
	if len(packets) == 1 {
		return packets[0]
	}
	body := []byte{'I', 'T', 'B', '3', 0, byte(len(packets))}
	for _, packet := range packets {
		body = binary.BigEndian.AppendUint32(body, uint32(len(packet)))
		body = append(body, packet...)
	}
	return body
}

// Tagged publication service time is different from the control reader's NOOP
// RTT. Physical message counts/bytes expose coalescing and provider-command
// cost without exporting credentials, packet contents or a synthetic speed.
func (a *mailAccountState) appendCompleted(control bool, n int, started time.Time) {
	a.appended.Add(1)
	if control {
		a.controlAppendCount.Add(1)
		a.controlMIMEBytes.Add(uint64(n))
	} else {
		a.dataAppendCount.Add(1)
		a.dataMIMEBytes.Add(uint64(n))
	}
	sample := int64(time.Since(started))
	for old := a.appendLatency.Load(); ; old = a.appendLatency.Load() {
		next := sample
		if old > 0 {
			next = (7*old + sample) / 8
		}
		if a.appendLatency.CompareAndSwap(old, next) {
			break
		}
	}
}

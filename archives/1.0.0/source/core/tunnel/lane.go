package tunnel

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"itom.local/core/datapath"
	"itom.local/core/flow"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/secure"
	"itom.local/core/servercfg"
	"itom.local/core/session"
	"itom.local/core/wallet"
	"itom.local/core/wire"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

const (
	transportHelloMagic            = "ITH10"
	transportProbeMagic            = "ITP10"
	transportChallengeMagic        = "ITC10"
	transportWireVersion           = 10
	capDataBatch            uint64 = 1 << iota
	capStripedData
	capLogicalControl
	capGrantRecovery
	capCoordinatedRekey
	capServerMessages
	capSmallRecoveryGrants
	capSelfDescribingData
	capSplitData
	capTwoDataShards
	capFourDataShards
	transportCapabilities = capDataBatch | capStripedData | capLogicalControl | capGrantRecovery | capCoordinatedRekey | capServerMessages | capSmallRecoveryGrants | capSelfDescribingData
	// One logical actor and receive/flight budget per user/session, shared by
	// every carrier. These bounds are not multiplied by account count.
	runtimeLogicalBytes       = 32 << 20
	runtimeAccountBytes       = 32 << 20
	runtimeReceiveBytes       = 32 << 20
	runtimeOwnerStreams       = 128
	runtimeStreamWindow       = 1 << 20
	runtimeInteractiveReserve = 64 << 10
)

type preauthProbe struct {
	Version      int
	Capabilities uint64
	ClientNonce  wire.ID
	MAC          []byte
}
type preauthChallenge struct {
	Version      int
	Capabilities uint64
	ClientNonce  wire.ID
	ServerNonce  wire.ID
	MAC          []byte
}
type hello struct {
	Version        int
	Capabilities   uint64
	LogicalSession wire.ID
	Generation     uint64
	CarrierEpoch   uint64
	ClientNonce    wire.ID
	ServerNonce    wire.ID
	Binding        secure.Binding
	Reply          bool
	Payload        []byte
}
type outgoing struct {
	usefulBytes     int64
	rateCounted     bool
	receiptStarted  time.Time
	receiptEligible bool
	ref             journal.AttemptRef
	counters        []uint64
	records         int
	ack             bool
	attempts        []uint64
	owner           *lane
}
type remoteLogicalAttempt struct {
	owner *lane
	id    uint64
}
type request struct {
	ctx    context.Context
	target wire.Connect
	id     uint64
	done   chan openResult
}
type openResult struct {
	socket *pipe
	err    error
}

var errLaneUnhealthy = errors.New("LANE_UNHEALTHY")

const (
	openHealthMaxSilence   = 15 * time.Second
	transportProgressLimit = 25 * time.Second
	logicalProgressLimit   = 90 * time.Second
	openHealthQuarantine   = 10 * time.Second
)

var openOwnerResultLimit = 4 * time.Second

type opening struct {
	request   request
	started   time.Time
	protocol  *session.OpenRequest
	early     []wire.Frame
	abandoned bool
}
type dialDone struct {
	id     uint64
	result datapath.DialResult
}
type lane struct {
	notices                                             *noticeReceiver
	accountView                                         *accountView
	traffic                                             *userTraffic
	controlRepeat                                       controlRepeat
	ctx                                                 context.Context
	cancel                                              context.CancelFunc
	ownerCtx                                            context.Context
	ownerCancel                                         context.CancelFunc
	ownerFrames                                         chan wire.Frame
	actorReady                                          chan struct{}
	ownerStarted                                        bool
	ownerWorkers                                        sync.WaitGroup
	serviceMu                                           sync.Mutex
	serviceNS, serviceBytes, serviceSamples             uint64
	fastServiceNS, fastServiceBytes, fastServiceSamples uint64
	peerReadBatchTarget                                 uint64
	peerReadSlowCount, peerReadFastCount                uint32
	peerReadAt                                          time.Time
	peerReadSamples                                     uint64
	physicalMu                                          sync.Mutex
	physicalClosing                                     bool
	physicalWriters                                     sync.WaitGroup
	physicalActive                                      atomic.Int64
	quiescing                                           atomic.Bool
	side                                                bool
	allowPrivate                                        bool
	internalDial                                        func(context.Context, wire.Connect) (*net.TCPConn, bool, error)
	account                                             servercfg.Account
	user                                                servercfg.User
	logicalSession                                      wire.ID
	generation, carrierEpoch                            uint64
	preauthClient, preauthServer                        wire.ID
	preauthOfferDigest                                  [32]byte
	preauthReply                                        []byte
	preauthReplyAt                                      time.Time
	preauthReplyUntil                                   time.Time
	preauthReplyRetries                                 atomic.Uint32
	reattachClient, reattachServer                      wire.ID
	dir, sendFolder, recvFolder                         string
	availableCaps, negotiatedCaps                       uint64
	dataShards                                          int
	dataTurn                                            atomic.Uint32
	dataPages                                           chan pageResult
	dataReaderStarted                                   bool // lane actor; activation precedes DATA consumption
	outCursors                                          map[string]mailbox.Cursor
	mail                                                mailbox.Transport
	control, data                                       *journal.Store
	cipher                                              *secure.Epoch
	envelopeKey                                         []byte
	wallet                                              *wallet.Wallet
	gate                                                *session.Gate
	boot                                                *session.Bootstrap
	pump                                                *datapath.Pump
	exit                                                *datapath.TCPExit
	started                                             time.Time
	ready                                               atomic.Bool
	deferredAdmissionTrace                              admissionTraceSampler
	grantRolloverTrace                                  admissionTraceSampler
	// Monotonic evidence that this physical carrier completed bootstrap.
	everReady atomic.Bool
	// Actor-owned startup evidence, retained while grants are leased by workers.
	fastGrantSeen, bulkGrantSeen                bool
	rekeyRequested                              bool
	nextRekeyRequest                            time.Time
	rekeyDrainToken                             uint64
	rekeyPeerDrained, rekeyDrainAckSent         bool
	rekeyDrainStableAt                          time.Time
	rejoinPending                               atomic.Bool
	rtt                                         atomic.Int64
	rttAt                                       atomic.Int64
	smoothedRTT                                 atomic.Int64
	idleRTT                                     atomic.Int64
	idleRTTAt                                   atomic.Int64
	providerBaseline                            time.Duration
	active                                      atomic.Int64
	admitted                                    atomic.Int64
	readHotUntil                                atomic.Int64
	readHotGeneration                           atomic.Uint64
	lastProgress                                atomic.Int64
	openQuarantineUntil                         atomic.Int64
	readWake                                    chan struct{}
	inputs                                      chan datapath.Input
	requests                                    chan request
	dials                                       chan dialDone
	grants, fastGrants                          chan wire.QuotaKey
	grantLimits                                 map[wire.QuotaKey]wire.Quota
	grantFast                                   map[wire.QuotaKey]bool
	grantRecoverySent                           map[wire.QuotaKey]time.Time
	stripe                                      *stripeGroup
	physicalSlots                               chan struct{}
	fastPhysicalSlots                           chan struct{}
	mu                                          sync.Mutex
	localGrantMu                                sync.Mutex
	out                                         []outgoing
	obsolete                                    map[journal.AttemptRef]bool
	remote                                      map[wire.QuotaKey][]remoteLogicalAttempt
	known                                       map[uint64]mailbox.Message
	pending                                     map[[32]byte]mailbox.Message
	reports                                     map[[32]byte]wire.UsageReport
	badDataProofs                               map[[32]byte]time.Time
	lastBadDataProofExpiry                      time.Time
	descriptorRepairs                           map[wire.QuotaKey]descriptorRepair
	opens                                       map[uint64]*opening
	nextID                                      uint64
	issued                                      map[wire.QuotaKey]bool
	issuedFast                                  map[wire.QuotaKey]bool
	queued                                      map[wire.QuotaKey]bool
	outCursor, recvCursor                       mailbox.Cursor
	outCache                                    []mailbox.Message
	lastRX, lastEcho, nextEcho, nextMaintenance time.Time
	echoIdle                                    bool
	nextRemoteCleanup, nextLocalCleanup         time.Time
	gcLocalPhase, gcPressurePhase               uint8
	gcPreferRemote                              bool
	echo                                        uint64
	ackDirty                                    bool
	controlQueue                                chan queuedControl
	receiptQueue                                chan queuedControl
	urgentQueue                                 chan queuedControl
	maintenanceQueue                            chan queuedControl
	writeErrors                                 chan error
	helloUID, helloValidity                     uint32
	lastReceipt, nextWallet                     time.Time
	diag                                        *diagnostic
	workers                                     sync.WaitGroup
	remoteGC                                    map[journal.AttemptRef]remoteCleanup
	remoteRecoveryCursor                        mailbox.Cursor
	recoveredLocal                              []journal.AttemptRef
	retireSent                                  map[wire.QuotaKey]time.Time
	pongs                                       atomic.Uint64
	progressRequests                            chan chan laneProgress
	pages                                       chan pageResult
	cleanupWake                                 chan struct{}
	gcDone                                      chan cleanupResult
	gcBusy                                      bool
	gcActive                                    atomic.Bool
	cleanupWanted                               atomic.Bool
	cleanupQueued                               bool
	cleanupQueuedKind                           cleanupKind
	bulkControlBlocked                          atomic.Bool
	controlDebt                                 atomic.Uint64
	controlWriting                              atomic.Int64
	controlWriteStarted                         atomic.Int64
	peerReadNewestPendingAt                     atomic.Int64
	peerReadPendingBodies                       atomic.Int64
	unreceivedLocalRecords                      atomic.Int64
	unreceivedLocalBytes                        atomic.Int64
	flowControlMu                               sync.Mutex
	flowControlLatest                           map[flowControlKey]queuedControl
	quotaControlMu                              sync.Mutex
	quotaControlLatest                          map[wire.QuotaKey]queuedControl
	maintenanceIO                               atomic.Int64
	gcInitial                                   bool
	mailReceipts                                map[uint64]bool
}

func attachedBinding(server wire.ID, u servercfg.User, a string, logical wire.ID, generation, carrierEpoch, capabilities uint64, clientNonce, serverNonce wire.ID) secure.Binding {
	h := sha256.New()
	_, _ = h.Write([]byte("ITOM/tunnel/10\x00"))
	_, _ = h.Write([]byte(u.ID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(a))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(u.Up))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(u.Down))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(logical[:])
	_, _ = h.Write(clientNonce[:])
	_, _ = h.Write(serverNonce[:])
	var meta [24]byte
	binary.BigEndian.PutUint64(meta[0:8], generation)
	binary.BigEndian.PutUint64(meta[8:16], carrierEpoch)
	binary.BigEndian.PutUint64(meta[16:24], capabilities)
	_, _ = h.Write(meta[:])
	sum := h.Sum(nil)
	var config [32]byte
	copy(config[:], sum)
	var pair wire.ID
	copy(pair[:], config[:16])
	return secure.Binding{Server: server, User: u.ID, Pair: pair, ConfigHash: config, Epoch: 1}
}

func preauthMAC(key []byte, label string, server wire.ID, u servercfg.User, account string, clientNonce, serverNonce wire.ID, capabilities uint64) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte("ITOM/preauth/10\x00"))
	_, _ = h.Write([]byte(label))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(server[:])
	_, _ = h.Write([]byte(u.ID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(account))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(u.Up))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(u.Down))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(clientNonce[:])
	_, _ = h.Write(serverNonce[:])
	var meta [8]byte
	binary.BigEndian.PutUint64(meta[:], capabilities)
	_, _ = h.Write(meta[:])
	return h.Sum(nil)
}
func validProbe(key []byte, server wire.ID, u servercfg.User, account string, p preauthProbe) bool {
	if p.Version != transportWireVersion || !validCapabilities(p.Capabilities) || p.ClientNonce == (wire.ID{}) || len(p.MAC) != sha256.Size {
		return false
	}
	want := preauthMAC(key, "probe", server, u, account, p.ClientNonce, wire.ID{}, p.Capabilities)
	return hmac.Equal(p.MAC, want)
}
func validChallenge(key []byte, server wire.ID, u servercfg.User, account string, c preauthChallenge, clientNonce wire.ID) bool {
	if c.Version != transportWireVersion || !validCapabilities(c.Capabilities) || c.ClientNonce != clientNonce || c.ServerNonce == (wire.ID{}) || len(c.MAC) != sha256.Size {
		return false
	}
	want := preauthMAC(key, "challenge", server, u, account, c.ClientNonce, c.ServerNonce, c.Capabilities)
	return hmac.Equal(c.MAC, want)
}
func mailboxTail(ctx context.Context, m mailbox.Transport, folder string) (mailbox.Cursor, error) {
	if tailer, ok := m.(interface {
		Tail(context.Context, string) (mailbox.Cursor, error)
	}); ok {
		return tailer.Tail(ctx, folder)
	}
	cur := mailbox.Cursor{}
	for pages := 0; pages < 64; pages++ {
		_, next, err := m.Page(ctx, folder, cur)
		if err != nil {
			return cur, err
		}
		if next == cur {
			return cur, nil
		}
		cur = next
	}
	return cur, mailbox.ErrLimit
}
func mailboxRecent(ctx context.Context, m mailbox.Transport, folder string, limit int) ([]mailbox.Message, mailbox.Cursor, error) {
	if recent, ok := m.(interface {
		Recent(context.Context, string, int) ([]mailbox.Message, mailbox.Cursor, error)
	}); ok {
		return recent.Recent(ctx, folder, limit)
	}
	cur, err := mailboxTail(ctx, m, folder)
	return nil, cur, err
}
func randomID() (wire.ID, error) {
	var id wire.ID
	_, err := rand.Read(id[:])
	return id, err
}

func freshAttachment() (wire.ID, uint64, error) {
	var logical wire.ID
	if _, err := rand.Read(logical[:]); err != nil {
		return wire.ID{}, 0, err
	}
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return wire.ID{}, 0, err
	}
	generation := binary.BigEndian.Uint64(raw[:])
	if generation == 0 {
		generation = 1
	}
	return logical, generation, nil
}
func pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

const (
	runtimeMaintenanceInterval   = time.Second
	runtimeRemoteCleanupInterval = 1500 * time.Millisecond
	runtimeLocalCleanupInterval  = 150 * time.Millisecond
)

func runtimePhase(account string, side bool, label byte, period time.Duration) time.Duration {
	h := sha256.New()
	_, _ = h.Write([]byte("ITOM/runtime-phase/1\x00"))
	_, _ = h.Write([]byte(account))
	if side {
		_, _ = h.Write([]byte{1})
	} else {
		_, _ = h.Write([]byte{0})
	}
	_, _ = h.Write([]byte{label})
	sum := h.Sum(nil)
	ms := period / time.Millisecond
	if ms <= 1 {
		return 0
	}
	return time.Duration(binary.BigEndian.Uint32(sum[:4])%uint32(ms)) * time.Millisecond
}

func advancePeriodic(next time.Time, period time.Duration, now time.Time) time.Time {
	if next.IsZero() {
		return now.Add(period)
	}
	for !next.After(now) {
		next = next.Add(period)
	}
	return next
}

func newLane(parent context.Context, server wire.ID, u servercfg.User, a servercfg.Account, dir string, side bool, m mailbox.Transport) (*lane, error) {
	return newLaneLogged(parent, server, u, a, dir, side, m, nil)
}
func newLaneLogged(parent context.Context, server wire.ID, u servercfg.User, a servercfg.Account, dir string, side bool, m mailbox.Transport, diag *diagnostic) (*lane, error) {
	var logical wire.ID
	var generation uint64
	if !side {
		var err error
		logical, generation, err = freshAttachment()
		if err != nil {
			return nil, err
		}
	}
	return newLaneAttached(parent, server, u, a, dir, side, m, diag, logical, generation, 1)
}

func newLaneAttached(parent context.Context, server wire.ID, u servercfg.User, a servercfg.Account, dir string, side bool, m mailbox.Transport, diag *diagnostic, logical wire.ID, generation, carrierEpoch uint64) (*lane, error) {
	ctx, cancel := context.WithCancel(parent)
	ownerCtx, ownerCancel := context.WithCancel(parent)
	l := &lane{ownerCtx: ownerCtx, ownerCancel: ownerCancel, ownerFrames: make(chan wire.Frame, 32), actorReady: make(chan struct{}), ctx: ctx, cancel: cancel, side: side, account: a, user: u, logicalSession: logical, generation: generation, carrierEpoch: carrierEpoch, dir: dir, mail: m, started: time.Now(), inputs: make(chan datapath.Input, 224), requests: make(chan request), dials: make(chan dialDone, 16), grants: make(chan wire.QuotaKey, 32), fastGrants: make(chan wire.QuotaKey, fastGrantReserve), grantLimits: map[wire.QuotaKey]wire.Quota{}, grantFast: map[wire.QuotaKey]bool{}, obsolete: map[journal.AttemptRef]bool{}, remote: map[wire.QuotaKey][]remoteLogicalAttempt{}, known: map[uint64]mailbox.Message{}, pending: map[[32]byte]mailbox.Message{}, reports: map[[32]byte]wire.UsageReport{}, opens: map[uint64]*opening{}, nextID: 1, issued: map[wire.QuotaKey]bool{}, issuedFast: map[wire.QuotaKey]bool{}, queued: map[wire.QuotaKey]bool{}, readWake: make(chan struct{}, 1)}
	now := time.Now()
	l.nextMaintenance = now.Add(runtimePhase(a.ID, side, 'm', runtimeMaintenanceInterval))
	l.nextEcho = now.Add(runtimePhase(a.ID, side, 'e', 5*time.Second))
	l.nextRemoteCleanup = now.Add(runtimePhase(a.ID, side, 'r', runtimeRemoteCleanupInterval))
	l.nextLocalCleanup = now.Add(runtimePhase(a.ID, side, 'l', runtimeLocalCleanupInterval))
	l.remoteGC = map[journal.AttemptRef]remoteCleanup{}
	l.retireSent = map[wire.QuotaKey]time.Time{}
	l.mailReceipts = map[uint64]bool{}
	l.progressRequests = make(chan chan laneProgress, 1)
	l.pages = make(chan pageResult, 1)
	l.dataPages = make(chan pageResult, 2)
	l.outCursors = map[string]mailbox.Cursor{}
	l.availableCaps = transportCapabilities
	if io := physicalMail(m); io != nil {
		caps, stageErr := io.stageTopology(ctx)
		if stageErr != nil {
			cancel()
			ownerCancel()
			io.Close()
			return nil, stageErr
		}
		l.availableCaps = caps
	}
	l.gcDone = make(chan cleanupResult, 1)
	l.cleanupWake = make(chan struct{}, 1)
	l.gcInitial = true
	l.diag = diag
	l.controlQueue = make(chan queuedControl, 512)
	l.receiptQueue = make(chan queuedControl, 1)
	l.urgentQueue = make(chan queuedControl, 64)
	l.maintenanceQueue = make(chan queuedControl, 512)
	l.writeErrors = make(chan error, 1)
	if !side {
		if probe, ok := m.(interface {
			Ping(context.Context) (time.Duration, error)
		}); ok {
			calibrationCtx, stopCalibration := context.WithTimeout(ctx, 2*time.Second)
			rtt, err := probe.Ping(calibrationCtx)
			stopCalibration()
			if err != nil {
				cancel()
				ownerCancel()
				return nil, err
			}
			l.providerBaseline = rtt
		}
	}
	l.stage("recovery")
	l.sendFolder, l.recvFolder = u.Up, u.Down
	if side {
		l.sendFolder, l.recvFolder = u.Down, u.Up
	}
	if diag != nil {
		diag.mu.Lock()
		diag.liveFolder = l.recvFolder
		diag.mu.Unlock()
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		cancel()
		ownerCancel()
		return nil, e
	}
	var e error
	l.control, e = ledger(dir, "control", a, u, true)
	if e != nil {
		cancel()
		ownerCancel()
		return nil, e
	}
	l.data, e = ledger(dir, "data", a, u, false)
	if e != nil {
		l.close()
		return nil, e
	}
	if diag != nil {
		for name, store := range map[string]*journal.Store{"control": l.control, "data": l.data} {
			effective, _ := store.Limit(a.ID, u.ID)
			diag.event(fmt.Sprintf("event=journal_profile kind=%s existing=%t bytes=%d messages=%d run=%d", name, store.Existing(), effective.Bytes, effective.Messages, store.Run()))
		}
	}
	for _, f := range l.data.Config().Accounts[0].Users[0].Folders {
		if e = m.Ensure(ctx, f); e != nil {
			l.close()
			return nil, e
		}
	}
	for _, s := range []*journal.Store{l.control, l.data} {
		var recovered []journal.AttemptRef
		for _, folder := range l.ownedFolders(s, true) {
			var refs []journal.AttemptRef
			refs, e = recoverLocal(ctx, m, s, folder)
			if e != nil {
				break
			}
			recovered = append(recovered, refs...)
		}
		if e != nil {
			l.close()
			return nil, e
		}
		if s == l.data {
			l.recoveredLocal = append(l.recoveredLocal, recovered...)
		}
		if s == l.control {
			e = s.EnableAfterInventory(a.ID, u.ID, s.Run())
		}
		if e != nil {
			l.close()
			return nil, e
		}
	}
	if side {
		rows, err := l.inventoryOwned(l.data, false)
		if err != nil {
			l.close()
			return nil, err
		}
		removed, err := reclaim(ctx, m, l.data, rows, func(a journal.Attempt) bool { return a.Ordinal > 0 })
		if err != nil {
			l.close()
			return nil, err
		}
		if err = l.retainRecoveredRemote(rows, removed); err != nil {
			l.close()
			return nil, err
		}
	}
	for _, folder := range l.data.Config().Accounts[0].Users[0].Folders {
		if e = m.Ensure(ctx, folder); e != nil {
			l.close()
			return nil, e
		}
	}
	l.stage("handshake_wait")
	if e = l.handshake(server); e != nil {
		l.close()
		return nil, e
	}
	if e = l.activateTopology(); e != nil {
		l.close()
		return nil, e
	}
	l.envelopeKey, e = hex.DecodeString(u.Key)
	if e != nil || len(l.envelopeKey) != 32 {
		l.close()
		return nil, errors.New("DATA_ENVELOPE_KEY")
	}
	if side {
		if e = l.queueRegisteredRemoteRecovery(); e != nil {
			l.close()
			return nil, e
		}
	}
	if io, ok := m.(interface {
		Activate(context.Context, string) error
	}); ok {
		if e = io.Activate(ctx, l.recvFolder); e != nil {
			l.close()
			return nil, e
		}
	}
	l.startControlWriter()
	l.stage("activation")
	role := session.Client
	if side {
		role = session.Server
	}
	var action session.GateAction
	l.gate, action, e = session.NewGate(role, 1, l.now(), transportPolicy())
	if e != nil {
		l.close()
		return nil, e
	}

	for _, f := range action.Send {
		if e = l.SendControl(ctx, f); e != nil {
			l.close()
			return nil, e
		}
	}
	l.markRemoteProgress()
	return l, nil
}
func (l *lane) now() time.Duration { return time.Since(l.started) }

func (l *lane) markRemoteProgress() {
	now := time.Now()
	l.lastRX = now
	l.lastProgress.Store(now.UnixNano())
}

func (l *lane) remoteProgressAge(now time.Time) time.Duration {
	ns := l.lastProgress.Load()
	if ns <= 0 {
		return time.Duration(1<<63 - 1)
	}
	age := now.Sub(time.Unix(0, ns))
	if age < 0 {
		return 0
	}
	return age
}

func (l *lane) openHealthy(now time.Time) bool {
	if l == nil || !l.ready.Load() || l.rejoinPending.Load() || l.ctx.Err() != nil {
		return false
	}
	if until := l.openQuarantineUntil.Load(); until > 0 && now.UnixNano() < until {
		return false
	}
	return l.remoteProgressAge(now) <= openHealthMaxSilence
}

func (l *lane) quarantineOpen() {
	l.openQuarantineUntil.Store(time.Now().Add(openHealthQuarantine).UnixNano())
	if l.stripe != nil {
		l.stripe.signal()
	}
}
func (l *lane) handshake(server wire.ID) error {
	if p, ok := l.mail.(interface{ PrepareHandshake(string) }); ok {
		p.PrepareHandshake(l.recvFolder)
	}
	ctx := l.ctx
	if !l.side {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(l.ctx, 90*time.Second)
		defer cancel()
		if l.logicalSession == (wire.ID{}) || l.generation == 0 || l.carrierEpoch == 0 {
			return errors.New("ATTACHMENT_CONTEXT")
		}
	}
	key, err := hex.DecodeString(l.user.Key)
	if err != nil {
		return err
	}
	defer clear(key)

	// Pre-authentication messages are short, HMAC/Noise-bound and replay-fenced
	// by fresh nonces. They are deliberately NOT charged to the durable runtime
	// control journal: an old/administratively purged mailbox must never exhaust
	// normal control admission and prevent a new session from authenticating.
	sendBody := func(magic string, v any) error {
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		return appendEphemeralControl(ctx, l.mail, l.sendFolder, append([]byte(magic), raw...))
	}
	sendHello := func(h hello) error { return sendBody(transportHelloMagic, h) }

	cur, err := mailboxTail(ctx, l.mail, l.recvFolder)
	if err != nil {
		return err
	}

	if l.side {
		// ITH9 is server-first. A fresh per-lane server nonce is published as a
		// signed challenge before any client message is required. This avoids a
		// fragile probe->challenge dependency while preserving restart replay
		// fencing: historical hellos bind a different server nonce and cannot be
		// accepted after this lane starts.
		serverNonce, nonceErr := randomID()
		if nonceErr != nil {
			return nonceErr
		}
		sendChallenge := func(clientNonce wire.ID) error {
			challenge := preauthChallenge{Version: transportWireVersion, Capabilities: l.capabilities(), ClientNonce: clientNonce, ServerNonce: serverNonce}
			challenge.MAC = preauthMAC(key, "challenge", server, l.user, l.account.ID, clientNonce, serverNonce, l.capabilities())
			if err := sendBody(transportChallengeMagic, challenge); err != nil {
				return err
			}
			if l.diag != nil {
				l.diag.event("event=preauth_challenge_tx")
			}
			return nil
		}
		if err = sendChallenge(wire.ID{}); err != nil {
			return err
		}
		for {
			rows, next, err := l.mail.Page(ctx, l.recvFolder, cur)
			if err != nil {
				return err
			}
			cur = next
			sortMessages(rows)
			for _, m := range rows {
				body, decodeErr := mailbox.Decode(m.MIME)
				if decodeErr != nil || len(body) > 16384 {
					continue
				}
				// A probe is only a fallback for a client that encountered an
				// already-active server lane. While this lane is still waiting we
				// answer it too, but the initial challenge never depends on it.
				if bytes.HasPrefix(body, []byte(transportProbeMagic)) {
					var p preauthProbe
					if json.Unmarshal(body[len(transportProbeMagic):], &p) == nil && validProbe(key, server, l.user, l.account.ID, p) {
						if err = sendChallenge(p.ClientNonce); err != nil {
							return err
						}
					}
					continue
				}
				if !bytes.HasPrefix(body, []byte(transportHelloMagic)) {
					continue
				}
				var h hello
				if json.Unmarshal(body[len(transportHelloMagic):], &h) != nil || h.Version != transportWireVersion || !validCapabilities(h.Capabilities) || h.Capabilities & ^l.capabilities() != 0 || h.Reply || h.ClientNonce == (wire.ID{}) || h.ServerNonce != serverNonce {
					continue
				}
				if h.LogicalSession == (wire.ID{}) || h.Generation == 0 || h.CarrierEpoch == 0 || h.Binding.Session == (wire.ID{}) || h.Binding.Handshake == (wire.ID{}) {
					continue
				}
				expected := attachedBinding(server, l.user, l.account.ID, h.LogicalSession, h.Generation, h.CarrierEpoch, h.Capabilities, h.ClientNonce, serverNonce)
				expected.Session = h.Binding.Session
				expected.Handshake = h.Binding.Handshake
				if h.Binding != expected {
					continue
				}
				hs, hsErr := secure.NewHandshake(h.Binding, key, false)
				if hsErr != nil || hs.Read(h.Payload) != nil {
					continue
				}
				if l.diag != nil {
					l.diag.event("event=preauth_hello_rx")
				}
				reply, writeErr := hs.Write(nil)
				if writeErr != nil {
					return writeErr
				}
				l.negotiatedCaps = h.Capabilities
				l.logicalSession, l.generation, l.carrierEpoch = h.LogicalSession, h.Generation, h.CarrierEpoch
				l.preauthClient, l.preauthServer = h.ClientNonce, serverNonce
				cachedReply, marshalErr := json.Marshal(hello{Version: transportWireVersion, Capabilities: h.Capabilities, LogicalSession: h.LogicalSession, Generation: h.Generation, CarrierEpoch: h.CarrierEpoch, ClientNonce: h.ClientNonce, ServerNonce: serverNonce, Binding: h.Binding, Reply: true, Payload: reply})
				if marshalErr != nil {
					return marshalErr
				}
				l.preauthOfferDigest = sha256.Sum256(body)
				l.preauthReply = append([]byte(transportHelloMagic), cachedReply...)
				l.preauthReplyAt = time.Now()
				l.preauthReplyUntil = time.Now().Add(30 * time.Second)
				if err = appendEphemeralControl(ctx, l.mail, l.sendFolder, l.preauthReply); err != nil {
					return err
				}
				if l.diag != nil {
					l.diag.event("event=preauth_reply_tx")
				}
				l.helloUID, l.helloValidity = m.UID, m.Validity
				l.recvCursor = mailbox.Cursor{Validity: m.Validity, After: m.UID}
				l.cipher, err = hs.Epoch()
				return err
			}
			if err = l.waitHandshake(ctx); err != nil {
				return err
			}
		}
	}

	// The client waits for a fresh server-first challenge. If the server still
	// owns an older authenticated lane, a signed probe is sent only as a fallback
	// to make that active lane publish a reattach challenge. On rejoin, an
	// anonymous challenge from mailbox history belongs to the old physical
	// lifetime: require an answer bound to this new client nonce instead.
	requireBoundChallenge := l.carrierEpoch > 1
challengeRounds:
	for {
		clientNonce, idErr := randomID()
		if idErr != nil {
			return idErr
		}
		probe := preauthProbe{Version: transportWireVersion, Capabilities: l.capabilities(), ClientNonce: clientNonce}
		probe.MAC = preauthMAC(key, "probe", server, l.user, l.account.ID, clientNonce, wire.ID{}, l.capabilities())
		recentRows, recentTail, recentErr := mailboxRecent(ctx, l.mail, l.recvFolder, 32)
		if recentErr != nil {
			return recentErr
		}
		cur = recentTail
		waitStarted := time.Now()
		var lastProbe time.Time
		if requireBoundChallenge {
			if err = sendBody(transportProbeMagic, probe); err != nil {
				return err
			}
			lastProbe = time.Now()
		}
		var serverNonce wire.ID
		selectedCaps := transportCapabilities
		sortMessages(recentRows)
		for i := len(recentRows) - 1; i >= 0; i-- {
			m := recentRows[i]
			body, decodeErr := mailbox.Decode(m.MIME)
			if decodeErr != nil || len(body) > 16384 || !bytes.HasPrefix(body, []byte(transportChallengeMagic)) {
				continue
			}
			var challenge preauthChallenge
			if json.Unmarshal(body[len(transportChallengeMagic):], &challenge) != nil {
				continue
			}
			if challenge.ClientNonce != clientNonce && (requireBoundChallenge || challenge.ClientNonce != (wire.ID{})) {
				continue
			}
			if validChallenge(key, server, l.user, l.account.ID, challenge, challenge.ClientNonce) {
				serverNonce = challenge.ServerNonce
				selectedCaps = intersectCapabilities(l.capabilities(), challenge.Capabilities)
				if l.diag != nil {
					l.diag.event("event=preauth_challenge_rx_recent")
				}
				break
			}
		}
		for serverNonce == (wire.ID{}) {
			rows, next, pageErr := l.mail.Page(ctx, l.recvFolder, cur)
			if pageErr != nil {
				return pageErr
			}
			cur = next
			sortMessages(rows)
			for _, m := range rows {
				body, decodeErr := mailbox.Decode(m.MIME)
				if decodeErr != nil || len(body) > 16384 || !bytes.HasPrefix(body, []byte(transportChallengeMagic)) {
					continue
				}
				var challenge preauthChallenge
				if json.Unmarshal(body[len(transportChallengeMagic):], &challenge) != nil {
					continue
				}
				if challenge.ClientNonce != clientNonce && (requireBoundChallenge || challenge.ClientNonce != (wire.ID{})) {
					continue
				}
				if validChallenge(key, server, l.user, l.account.ID, challenge, challenge.ClientNonce) {
					serverNonce = challenge.ServerNonce
					selectedCaps = intersectCapabilities(l.capabilities(), challenge.Capabilities)
					cur = mailbox.Cursor{Validity: m.Validity, After: m.UID}
					if l.diag != nil {
						l.diag.event("event=preauth_challenge_rx")
					}
					break
				}
			}
			if serverNonce != (wire.ID{}) {
				break
			}
			if time.Since(waitStarted) >= 2*time.Second && (lastProbe.IsZero() || time.Since(lastProbe) >= 2*time.Second) {
				if err = sendBody(transportProbeMagic, probe); err != nil {
					return err
				}
				if l.diag != nil {
					l.diag.event("event=preauth_probe_tx")
				}
				lastProbe = time.Now()
			}
			if err = l.waitHandshake(ctx); err != nil {
				return err
			}
		}

		b := attachedBinding(server, l.user, l.account.ID, l.logicalSession, l.generation, l.carrierEpoch, selectedCaps, clientNonce, serverNonce)
		if _, err = rand.Read(b.Session[:]); err != nil {
			return err
		}
		if _, err = rand.Read(b.Handshake[:]); err != nil {
			return err
		}
		hs, hsErr := secure.NewHandshake(b, key, true)
		if hsErr != nil {
			return hsErr
		}
		payload, writeErr := hs.Write(nil)
		if writeErr != nil {
			return writeErr
		}
		offer := hello{Version: transportWireVersion, Capabilities: selectedCaps, LogicalSession: l.logicalSession, Generation: l.generation, CarrierEpoch: l.carrierEpoch, ClientNonce: clientNonce, ServerNonce: serverNonce, Binding: b, Payload: payload}
		if err = sendHello(offer); err != nil {
			return err
		}
		if l.diag != nil {
			l.diag.event("event=preauth_hello_tx")
		}
		replyDeadline := time.Now().Add(10 * time.Second)
		retryInterval := min(3*time.Second, max(time.Second, 6*l.providerBaseline))
		retryAt, replyRetries := time.Now().Add(retryInterval), 0
		for time.Now().Before(replyDeadline) {
			rows, next, pageErr := l.mail.Page(ctx, l.recvFolder, cur)
			if pageErr != nil {
				return pageErr
			}
			cur = next
			sortMessages(rows)
			for _, m := range rows {
				body, decodeErr := mailbox.Decode(m.MIME)
				if decodeErr != nil || len(body) > 16384 {
					continue
				}
				if bytes.HasPrefix(body, []byte(transportChallengeMagic)) {
					var challenge preauthChallenge
					if json.Unmarshal(body[len(transportChallengeMagic):], &challenge) == nil && challenge.ServerNonce != serverNonce &&
						(challenge.ClientNonce == (wire.ID{}) || challenge.ClientNonce == clientNonce) &&
						validChallenge(key, server, l.user, l.account.ID, challenge, challenge.ClientNonce) {
						// The authenticated old carrier accepted our reattach and
						// restarted. Negotiate its fresh nonce immediately, rather
						// than spending ten seconds waiting for an obsolete reply.
						requireBoundChallenge = true
						continue challengeRounds
					}
					continue
				}
				if !bytes.HasPrefix(body, []byte(transportHelloMagic)) {
					continue
				}
				var h hello
				if json.Unmarshal(body[len(transportHelloMagic):], &h) != nil || h.Version != transportWireVersion || h.Capabilities != selectedCaps || !h.Reply || h.ClientNonce != clientNonce || h.ServerNonce != serverNonce {
					continue
				}
				if h.LogicalSession != l.logicalSession || h.Generation != l.generation || h.CarrierEpoch != l.carrierEpoch || h.Binding != b || hs.Read(h.Payload) != nil {
					continue
				}
				l.negotiatedCaps = selectedCaps
				l.preauthClient, l.preauthServer = clientNonce, serverNonce
				if l.diag != nil {
					l.diag.event("event=preauth_reply_rx")
				}
				l.helloUID, l.helloValidity = m.UID, m.Validity
				l.recvCursor = mailbox.Cursor{Validity: m.Validity, After: m.UID}
				l.cipher, err = hs.Epoch()
				return err
			}
			// A missing handshake reply must not require a different cipher or a
			// new carrier epoch. Repeat the exact Noise offer; the authenticated
			// server can resend its cached reply without mutating either session.
			if replyRetries < 3 && time.Now().After(retryAt) {
				if err = sendHello(offer); err != nil {
					return err
				}
				replyRetries++
				retryAt = time.Now().Add(retryInterval)
			}
			if err = l.waitHandshake(ctx); err != nil {
				return err
			}
		}
		// The server may have restarted after issuing the challenge. Start a new
		// probe/challenge round rather than waiting on a stale mailbox epoch.
		requireBoundChallenge = true
	}
}

// Before activation no independent writer/reader exists. Complete IDLE/DONE
// synchronously on the base socket before any pre-auth APPEND or tail FETCH.
func (l *lane) waitHandshake(ctx context.Context) error {
	if w, ok := l.mail.(interface {
		WaitRead(context.Context, string, time.Duration) (bool, bool, error)
	}); ok {
		// A challenge/reply can arrive between the preceding FETCH and IDLE.
		// Audit this short startup phase promptly even if that EXISTS was lost.
		used, _, err := w.WaitRead(ctx, l.recvFolder, 500*time.Millisecond)
		if err != nil || used {
			return err
		}
	}
	return pause(ctx, 500*time.Millisecond)
}
func (l *lane) reconcileRecoveredCrossOwners() error {
	if l.stripe == nil || len(l.recoveredLocal) == 0 {
		l.recoveredLocal = nil
		return nil
	}
	for _, ref := range l.recoveredLocal {
		for _, entry := range l.stripe.takeLocal(l.account.ID, ref) {
			if entry.id == 0 {
				continue
			}
			if err := l.cleanedFor(entry.owner, entry.id); err != nil {
				return err
			}
		}
	}
	l.recoveredLocal = nil
	return nil
}

func (l *lane) close() {
	l.ready.Store(false)
	if l.stripe != nil {
		l.stripe.detach(l)
	}
	l.physicalMu.Lock()
	l.physicalClosing = true
	l.cancel()
	l.physicalMu.Unlock()
	l.physicalWriters.Wait()
	if !l.ownerStarted {
		if l.ownerCancel != nil {
			l.ownerCancel()
		}
		l.ownerWorkers.Wait()
		if l.pump != nil && (l.stripe == nil || l.stripe.accountOwner(l.account.ID) == l) {
			l.pump.Close()
		}
		if l.exit != nil {
			l.exit.Stop()
		}
	}
	if !l.ownerStarted && l.stripe != nil {
		l.stripe.releaseOwner(l)
	}
	l.workers.Wait()
	if l.control != nil && l.data != nil {
		l.health()
	}
	if l.stripe == nil {
		for _, o := range l.opens {
			select {
			case o.request.done <- openResult{err: context.Canceled}:
			default:
			}
		}
	}
	if l.wallet != nil {
		l.wallet.Close()
	}
	if l.control != nil {
		l.control.Close()
	}
	if l.data != nil {
		l.data.Close()
	}
	clear(l.envelopeKey)
}
func (l *lane) SendControl(ctx context.Context, f wire.Frame) error {
	if f.Stream != 0 && l.stripe != nil {
		return l.stripe.sendStreamControl(ctx, l, f)
	}
	return l.sendCarrierControl(ctx, f)
}

func (l *lane) sendCarrierControl(ctx context.Context, f wire.Frame) error {
	if _, e := wire.DecodeControl(f); e != nil {
		return e
	}
	if l.controlRepeat.duplicate(f, time.Now()) {
		return nil
	}
	if l.diag != nil {
		l.diag.mu.Lock()
		switch f.Type {
		case wire.Window:
			l.diag.line.WindowSent++
			l.diag.line.MaxWindowSent = max(l.diag.line.MaxWindowSent, f.Offset)
		case wire.Receipt:
			l.diag.line.ReceiptSent++
		}
		l.diag.mu.Unlock()
	}
	l.traceFrame("frame_queued", f, nil)
	f.Payload = append([]byte(nil), f.Payload...)
	queued := queuedControl{Frame: f, added: time.Now()}
	if f.Type == wire.GCStatus {
		return l.enqueueQuotaControl(ctx, queued)
	}
	if f.Type == wire.MailReceipt {
		select {
		case l.receiptQueue <- queued:
			return nil
		default:
		}
		select {
		case <-l.receiptQueue:
		default:
		}
		select {
		case l.receiptQueue <- queued:
			return nil
		case <-l.ctx.Done():
			return l.ctx.Err()
		}
	}
	if (f.Type == wire.Window && !latencyControl(f)) || f.Type == wire.Receipt || f.Type == wire.Status {
		return l.enqueueFlowControl(ctx, queued)
	}
	queue := l.controlQueue
	if latencyControl(f) {
		// Writer urgency and reader wake are separate policies. Responses remain
		// urgent, but only request-like control that should cause fresh peer mail
		// interrupts IDLE and opens a bounded active receive window. This prevents
		// PONG/OPEN_OK/GRANT_USAGE reply traffic from keeping both sides in a
		// permanent polling loop under load.
		if hotFor := readerResponseWindow(f); hotFor > 0 {
			l.armReaderHot(l.readerResponseBudget(hotFor))
		}
		timer := time.NewTimer(250 * time.Millisecond)
		defer timer.Stop()
		select {
		case l.urgentQueue <- queued:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-l.ctx.Done():
			return l.ctx.Err()
		case <-timer.C:
			return errors.New("CONTROL_QUEUE_LIMIT")
		}
	}
	if maintenanceControl(f) {
		maintenance := l.maintenanceQueue
		if maintenance == nil {
			maintenance = l.controlQueue
		}
		timer := time.NewTimer(500 * time.Millisecond)
		defer timer.Stop()
		select {
		case maintenance <- queued:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-l.ctx.Done():
			return l.ctx.Err()
		case <-timer.C:
			return errors.New("CONTROL_QUEUE_LIMIT")
		}
	}
	select {
	case queue <- queued:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-l.ctx.Done():
		return l.ctx.Err()
	default:
		return errors.New("CONTROL_QUEUE_LIMIT")
	}
}
func (l *lane) controlSend(m wire.Control) error {
	f, e := wire.EncodeControl(0, m)
	if e != nil {
		return e
	}
	return l.SendControl(l.ctx, f)
}
func (l *lane) Receive(ctx context.Context) (datapath.Input, error) {
	select {
	case <-ctx.Done():
		return datapath.Input{}, ctx.Err()
	case in := <-l.inputs:
		return in, nil
	}
}
func (l *lane) input(f wire.Frame) error {
	select {
	case l.inputs <- datapath.Input{Generation: 1, Frame: &f}:
		return nil
	case <-l.logicalContext().Done():
		return l.logicalContext().Err()
	}
}
func (l *lane) cleaned(id uint64) error {
	select {
	case l.inputs <- datapath.Input{Generation: 1, Cleaned: id}:
		return nil
	case <-l.logicalContext().Done():
		return l.logicalContext().Err()
	}
}
func (l *lane) stripedOutstandingOwners() []*lane {
	l.mu.Lock()
	defer l.mu.Unlock()
	seen := map[*lane]bool{}
	for _, entries := range l.remote {
		for _, entry := range entries {
			if entry.owner != nil && entry.owner != l {
				seen[entry.owner] = true
			}
		}
	}
	for _, o := range l.out {
		if o.owner != nil && o.owner != l {
			seen[o.owner] = true
		}
	}
	out := make([]*lane, 0, len(seen))
	for owner := range seen {
		out = append(out, owner)
	}
	return out
}

func (l *lane) cleanedFor(owner *lane, id uint64) error {
	if owner == nil {
		owner = l
	}
	if owner.logicalContext().Err() != nil {
		// The logical owner is gone; retaining physical accounting was conservative,
		// but there is no live Engine left to refund. Never fail a healthy carrier.
		return nil
	}
	return owner.cleaned(id)
}

var sizingPayload [wire.MaxData]byte // read-only; sizing depends on length, never bytes

func (l *lane) SizeMetadata(f wire.Frame, payloadBytes uint64) (uint64, error) {
	if f.Type != wire.Data || len(f.Payload) != 0 || payloadBytes > wire.MaxData {
		return 0, wire.ErrLimit
	}
	f.Payload = sizingPayload[:payloadBytes]
	return l.Size(f)
}

func (l *lane) Size(f wire.Frame) (uint64, error) {
	if l.side {
		return datapath.Size(f, nil)
	}
	if l.stripe != nil {
		if n, err := l.stripe.maxRemoteSize(f); err == nil {
			return n, nil
		}
	}
	k := wire.QuotaKey{Store: wire.ID{1}, Epoch: 1, Grant: 1, Session: l.cipher.Context().Session, User: l.user.ID, Account: l.account.ID}
	return datapath.Size(f, &k)
}

// captureAppender retains only the authenticated counter, never plaintext.
type captureAppender struct {
	mailbox.Transport
	counter uint64
}

func (c *captureAppender) Append(ctx context.Context, f string, raw []byte) error {
	body, e := mailbox.Decode(raw)
	if e != nil {
		return e
	}
	if len(body) < wire.HeaderSize {
		return wire.ErrFormat
	}
	h, e := wire.ParseHeader(body[:wire.HeaderSize])
	if e != nil {
		return e
	}
	c.counter = h.Counter
	return c.Transport.Append(ctx, f, raw)
}
func (l *lane) Write(job *datapath.Job) (result datapath.Completion) {
	start := time.Now()
	if l.diag != nil {
		l.diag.mu.Lock()
		l.diag.line.DataAttempts++
		l.diag.line.DataRecords++
		l.diag.line.DataPayloadBytes += int64(job.Attempt().PayloadBytes)
		l.diag.mu.Unlock()
	}
	defer func() {
		if l.diag != nil && l.diag.tracer != nil {
			b, _ := json.Marshal(map[string]any{"attempt": job.Attempt(), "outcome": result.Outcome})
			l.diag.trace(traceEvent{Event: "data_completion", Code: errorCode(result.Err), DurationUS: time.Since(start).Microseconds(), Details: b})
		}
	}()
	grantAcquired := func() {
		if l.diag == nil {
			return
		}
		wait := time.Since(start)
		l.diag.mu.Lock()
		l.diag.line.DataWaitMS = wait.Milliseconds()
		l.diag.line.MaxDataWaitMS = max(l.diag.line.MaxDataWaitMS, wait.Milliseconds())
		l.diag.mu.Unlock()
		if l.diag.tracer != nil {
			b, _ := json.Marshal(job.Attempt())
			l.diag.trace(traceEvent{Event: "data_grant_acquired", DurationUS: wait.Microseconds(), Details: b})
		}
	}

	if l.side {
		g, e := l.waitLocalGrantContextClass(l.ctx, job.Attempt().EncodedBytes, job.Interactive())
		if e != nil {
			return datapath.Completion{Err: fmt.Errorf("data_writer: %w", e)}
		}
		grantAcquired()
		cap := &captureAppender{Transport: l.mail}
		l.traceFrame("data_writer_begin", job.FrameInfo(), nil)
		r := job.WriteLocal(l.data, g.Ref, l.nextDataFolder(), l.cipher, cap)
		e = l.data.CloseGrant(g.Ref)
		r.Err = errors.Join(r.Err, e)
		if r.Err != nil {
			r.Err = fmt.Errorf("data_writer: %w", r.Err)
		}
		if r.Local != nil {
			l.registerOutgoing(outgoing{ref: *r.Local, counters: []uint64{cap.counter}, records: 1, attempts: []uint64{r.Attempt}, owner: l})
		}
		return r
	}
	key, grantErr := l.takeRemoteGrant(job.Interactive())
	if grantErr != nil {
		return datapath.Completion{Err: grantErr}
	}
	grantAcquired()
	l.traceFrame("data_writer_begin", job.FrameInfo(), nil)
	r := job.WriteRemote(l.wallet, key, l.nextDataFolder(), l.cipher, l.mail)
	if r.Usage != nil {
		l.registerRemoteLogical(key, l, []uint64{r.Attempt})
		bookErr := l.publishRemoteUsage(key, *r.Usage)
		r.NonFatal = r.Outcome == flow.CommitUnknown && bookErr == nil
		r.Err = errors.Join(r.Err, bookErr)
		// The adapter published the durable report once; Pump must not repeat it.
		r.Usage = nil
	} else {
		// No durable Spend happened, so returning this lease is safe and prevents
		// transient pre-commit failures from silently eroding the grant pool.
		r.Err = errors.Join(r.Err, l.returnUnusedRemoteGrant(key))
	}
	return r
}

const (
	// Each physical DATA message remains capped at 512 KiB, but one storage
	// grant can lease multiple message ordinals. The existing wallet/journal model
	// is already cumulative, so this removes grant/release churn without changing
	// the 8 MiB maximum uncertain physical exposure or wire format.
	dataBatchMIMEHardCap   = 512 << 10
	pendingDataMIMEHardCap = 12 << 20

	// Bulk keeps the same 2 MiB byte lease but allows enough physical APPEND
	// ordinals for the byte quota, rather than a tiny ordinal cap, to be the normal
	// limiter. Real batches are commonly ~100-120 KiB; four/sixteen ordinals could
	// fence a lease with useful byte headroom still reserved. Increasing only the
	// ordinal budget does not increase byte exposure, per-message size, or ITH9.
	dataLeaseMessages = 32
	dataLeaseBytes    = 2 << 20
	fastLeaseBytes    = dataBatchMIMEHardCap
	fastLeaseMessages = 32

	// One fast lease plus two bulk leases reserve 4.5 MiB, down from the old
	// three-bulk-equivalent 6 MiB reservation. The same two bulk leases remain
	// available for throughput while local full-duplex DATA gains headroom.
	remoteBulkLeases      = 2
	localFastReserveBytes = dataBatchMIMEHardCap
	fastGrantReserve      = 1
)

func failBatch(jobs []*datapath.Job, results []datapath.Completion, err error) []datapath.Completion {
	for len(results) < len(jobs) {
		results = append(results, jobs[len(results)].RejectBeforeCommit(err))
	}
	for i := range results {
		results[i].Outcome = flow.NotCommitted
		if results[i].Err == nil {
			results[i].Err = err
		}
	}
	return results
}

func (l *lane) traceBatchStage(event string, started time.Time, code string, details map[string]any) {
	if l.diag == nil || l.diag.tracer == nil {
		return
	}
	raw, _ := json.Marshal(details)
	l.diag.trace(traceEvent{Event: event, Code: code, DurationUS: time.Since(started).Microseconds(), Details: raw})
}

func (l *lane) recordPumpQueueWait(jobs []*datapath.Job, fast bool) {
	if l.diag == nil || len(jobs) == 0 {
		return
	}
	now := time.Now()
	var wait time.Duration
	for _, job := range jobs {
		if d := job.QueueWait(now); d > wait {
			wait = d
		}
	}
	ms := wait.Milliseconds()
	l.diag.mu.Lock()
	l.diag.line.PumpQueueWaitMS = ms
	l.diag.line.MaxPumpQueueWaitMS = max(l.diag.line.MaxPumpQueueWaitMS, ms)
	if fast {
		l.diag.line.FastPumpQueueWaitMS = ms
		l.diag.line.MaxFastPumpQueueWaitMS = max(l.diag.line.MaxFastPumpQueueWaitMS, ms)
	}
	l.diag.mu.Unlock()
}

func (l *lane) recordDataWait(started time.Time, fast, local bool) {
	if l.diag == nil {
		return
	}
	wait := time.Since(started)
	ms := wait.Milliseconds()
	l.diag.mu.Lock()
	l.diag.line.DataWaitMS = ms
	l.diag.line.MaxDataWaitMS = max(l.diag.line.MaxDataWaitMS, ms)
	if local {
		l.diag.line.LocalGrantWaitMS = ms
		l.diag.line.MaxLocalGrantWaitMS = max(l.diag.line.MaxLocalGrantWaitMS, ms)
	} else {
		l.diag.line.RemoteGrantWaitMS = ms
		l.diag.line.MaxRemoteGrantWaitMS = max(l.diag.line.MaxRemoteGrantWaitMS, ms)
	}
	if fast {
		l.diag.line.FastDataWaitMS = ms
		l.diag.line.MaxFastDataWaitMS = max(l.diag.line.MaxFastDataWaitMS, ms)
	}
	l.diag.mu.Unlock()
}

func (l *lane) recordStripeWait(started time.Time, fast bool) {
	if l.diag == nil {
		return
	}
	ms := time.Since(started).Milliseconds()
	l.diag.mu.Lock()
	l.diag.line.StripeWaitMS = ms
	l.diag.line.MaxStripeWaitMS = max(l.diag.line.MaxStripeWaitMS, ms)
	if fast {
		l.diag.line.FastStripeWaitMS = ms
		l.diag.line.MaxFastStripeWaitMS = max(l.diag.line.MaxFastStripeWaitMS, ms)
	}
	l.diag.mu.Unlock()
}

func (l *lane) recordGrantSelectWait(started time.Time, fast bool) {
	if l.diag == nil {
		return
	}
	ms := time.Since(started).Milliseconds()
	l.diag.mu.Lock()
	l.diag.line.GrantSelectWaitMS = ms
	l.diag.line.MaxGrantSelectWaitMS = max(l.diag.line.MaxGrantSelectWaitMS, ms)
	if fast {
		l.diag.line.FastGrantSelectWaitMS = ms
		l.diag.line.MaxFastGrantSelectWaitMS = max(l.diag.line.MaxFastGrantSelectWaitMS, ms)
	}
	l.diag.mu.Unlock()
}

func (l *lane) acquirePhysicalAppendSlot(ctx context.Context, fast bool) (stripeLease, error) {
	if l.stripe == nil {
		return stripeLease{}, nil
	}
	started := time.Now()
	lease, err := l.stripe.acquireLaneForClass(ctx, l, fast)
	l.recordStripeWait(started, fast)
	l.traceBatchStage("data_batch_physical_slot", started, errorCode(err), map[string]any{"fast": fast, "account": l.account.ID})
	return lease, err
}

// takeRemoteGrant keeps physical admission QoS separate from logical byte
// scheduling. Fast DATA may borrow a bulk permit when available, but bulk DATA
// cannot consume the reserved fast permits. This preserves the global physical
// exposure bound because both classes are issued from the same 8 MiB journal.
func (l *lane) takeRemoteGrant(fast bool) (wire.QuotaKey, error) {
	return l.takeRemoteGrantContext(l.ctx, fast)
}

func (l *lane) takeRemoteGrantContext(ctx context.Context, fast bool) (wire.QuotaKey, error) {
	if fast {
		select {
		case key := <-l.fastGrants:
			return key, nil
		default:
		}
		select {
		case key := <-l.grants:
			return key, nil
		default:
		}
		select {
		case key := <-l.fastGrants:
			return key, nil
		case key := <-l.grants:
			return key, nil
		case <-ctx.Done():
			return wire.QuotaKey{}, ctx.Err()
		}
	}
	select {
	case key := <-l.grants:
		return key, nil
	case <-ctx.Done():
		return wire.QuotaKey{}, ctx.Err()
	}
}

func (l *lane) tryTakeRemoteGrant(fast bool) (wire.QuotaKey, bool) {
	if fast {
		select {
		case key := <-l.fastGrants:
			return key, true
		default:
		}
		select {
		case key := <-l.grants:
			return key, true
		default:
			return wire.QuotaKey{}, false
		}
	}
	select {
	case key := <-l.grants:
		return key, true
	default:
		return wire.QuotaKey{}, false
	}
}

func (l *lane) registerRemoteLogical(key wire.QuotaKey, owner *lane, ids []uint64) {
	l.mu.Lock()
	for _, id := range ids {
		l.remote[key] = append(l.remote[key], remoteLogicalAttempt{owner: owner, id: id})
	}
	l.mu.Unlock()
	if owner != nil && l.stripe != nil {
		l.stripe.rememberRemote(l.account.ID, key, owner, ids)
	}
}

// requeueRemoteGrant returns an open cumulative storage lease to its original
// QoS class. A channel entry is exclusive ownership of that lease: at most one
// physical batch can Spend a given grant at a time, while different grants may
// still run in parallel across DATA writers.
func (l *lane) returnUnusedRemoteGrant(key wire.QuotaKey) error {
	l.mu.Lock()
	fast, ok := l.grantFast[key]
	l.mu.Unlock()
	if !ok {
		return errors.New("GRANT_STATE")
	}
	queue := l.grants
	if fast {
		queue = l.fastGrants
	}
	select {
	case queue <- key:
		if l.stripe != nil {
			l.stripe.signal()
		}
		return nil
	default:
		return errors.New("GRANT_QUEUE_LIMIT")
	}
}

func (l *lane) requeueRemoteGrant(key wire.QuotaKey, total wire.Quota) (bool, error) {
	l.mu.Lock()
	limit, ok := l.grantLimits[key]
	fast := l.grantFast[key]
	l.mu.Unlock()
	if !ok || limit.Messages == 0 || total.Messages == 0 || total.Messages > limit.Messages || total.Bytes > limit.Bytes {
		return false, errors.New("GRANT_STATE")
	}
	if total.Messages >= limit.Messages || total.Bytes >= limit.Bytes {
		return false, nil
	}
	// Do not discard the last partial byte window merely because it cannot fit
	// the theoretical 512 KiB maximum. The writer knows the exact next physical
	// size: WALLET_EXHAUSTED is rollback-safe, so it fences this key and retries
	// the same unsent encrypted batch on the next grant. This lets ordinary
	// ~100 KiB batches consume almost all of a 2 MiB lease.
	queue := l.grants
	if fast {
		queue = l.fastGrants
	}
	select {
	case queue <- key:
		if l.stripe != nil {
			l.stripe.signal()
		}
		return true, nil
	default:
		return false, errors.New("GRANT_QUEUE_LIMIT")
	}
}

func (l *lane) fenceRemoteGrant(key wire.QuotaKey) error {
	release, err := l.wallet.Fence(key)
	if err != nil {
		return err
	}
	return l.deferDurablePublication(l.controlSend(release))
}

// Pending usage and fenced release are already durable in the wallet. Queue
// pressure must not destroy their logical stream owner or refund this debt.
// maintenance replays the exact descriptors/release until acknowledged.
func (l *lane) deferDurablePublication(err error) error {
	if err == nil || errorCode(err) != "CONTROL_QUEUE_LIMIT" {
		return err
	}
	if l.diag != nil {
		l.diag.event("event=wallet_publication_deferred code=CONTROL_QUEUE_LIMIT")
	}
	return nil
}

// publishRemoteUsage publishes the per-message descriptor immediately (the
// receiver needs it before decrypting DATA), but amortizes grant/release control
// over several APPENDs. The wire format is unchanged: StorageGrant was already
// cumulative, we simply stop fencing it after every first Spend.
func (l *lane) publishRemoteUsage(key wire.QuotaKey, usage wire.UsageReport) error {
	if l.ctx.Err() != nil {
		_, err := l.wallet.Fence(key)
		return err
	}
	if err := l.controlSend(usage); err != nil {
		if l.ctx.Err() != nil {
			_, fenceErr := l.wallet.Fence(key)
			return fenceErr
		}
		return errors.Join(l.deferDurablePublication(err), l.fenceRemoteGrant(key))
	}
	return l.finishRemoteUsage(key, usage.Total)
}

// Return the exclusive grant only after physical APPEND has completed. An early
// descriptor may overlap that APPEND, but never authorizes another concurrent Spend.
func (l *lane) finishRemoteUsage(key wire.QuotaKey, total wire.Quota) error {
	if l.ctx.Err() != nil {
		_, err := l.wallet.Fence(key)
		return err
	}

	reused, err := l.requeueRemoteGrant(key, total)
	if err != nil {
		if l.ctx.Err() != nil {
			_, fenceErr := l.wallet.Fence(key)
			return fenceErr
		}
		return errors.Join(err, l.fenceRemoteGrant(key))
	}
	if reused {
		return nil
	}
	_, fenceErr := l.wallet.Fence(key)
	if fenceErr != nil {
		return fenceErr
	}
	if l.ctx.Err() != nil {
		return nil
	}
	return l.fenceRemoteGrant(key)
}

// BatchParallelism exposes only physical DATA APPEND capacity. Pump keeps
// flow/accounting state serialized, so parallelism never weakens byte credit or
// Unknown-outcome handling.
func (l *lane) BatchParallelism() int {
	if l.stripe != nil {
		return l.stripe.capacity()
	}
	return dataParallelism(l.mail)
}

// FastBatchParallelism reserves one Pump scheduler worker for short DATA when
// the lane has more than one physical writer worth of capacity. The worker is
// a queue-consumer reserve, not a new IMAP permit: per-carrier physicalSlots and
// fast-waiter priority still enforce the real APPEND concurrency.
func (l *lane) FastBatchParallelism() int {
	if l.BatchParallelism() > 1 {
		return 1
	}
	return 0
}

// Fast physical DATA and mailbox cleanup must never overlap on the same lane.
// State -1 means cleanup owns the mailbox maintenance class; non-negative values
// count latency-sensitive physical DATA operations. CAS makes the selection ->
// append handoff race-free without serializing bulk DATA across cleaner links.
// The shared-socket fallback needs an exclusive service turn. Independent
// DATA and maintenance roles instead rely on journal transactions and exact
// eligible BODY proofs; holding one behind the other only adds head of line.
func (l *lane) independentDataMaintenance() bool {
	io, ok := l.mail.(interface{ DataMaintenanceIsolated() bool })
	return ok && io.DataMaintenanceIsolated()
}
func (l *lane) cleanupBlocksFast() bool {
	return !l.independentDataMaintenance() && (l.gcActive.Load() || l.cleanupWanted.Load())
}
func (l *lane) tryBeginFastPhysical() bool {
	if l.independentDataMaintenance() {
		return true
	}
	for {
		// Once cleanup declares intent, stop admitting new latency-sensitive DATA
		// to this physical lane. Existing fast I/O is allowed to finish; the cleanup
		// guard can then acquire without losing every race to a continuous speedtest.
		if l.cleanupWanted.Load() || l.gcActive.Load() {
			return false
		}
		s := l.maintenanceIO.Load()
		if s < 0 {
			return false
		}
		if l.maintenanceIO.CompareAndSwap(s, s+1) {
			// Close the intent race: cleanup may have announced itself between the
			// pre-check and our CAS. Yield the slot in that case.
			if l.cleanupWanted.Load() || l.gcActive.Load() {
				l.maintenanceIO.Add(-1)
				return false
			}
			return true
		}
	}
}
func (l *lane) endFastPhysical() {
	if !l.independentDataMaintenance() {
		l.maintenanceIO.Add(-1)
	}
}
func (l *lane) tryBeginCleanupIO() bool { return l.maintenanceIO.CompareAndSwap(0, -1) }
func (l *lane) endCleanupIO() {
	if !l.maintenanceIO.CompareAndSwap(-1, 0) {
		panic("ITOM cleanup I/O guard corrupt")
	}
}

// WriteBatch stripes one logical Pump across healthy carriers in the same
// authenticated LogicalSession. Only physical mail I/O moves; stream ownership,
// offsets and flow accounting remain serialized in the owner Pump.
func (l *lane) WriteBatch(jobs []*datapath.Job) []datapath.Completion {
	if len(jobs) == 0 {
		return []datapath.Completion{{Err: wire.ErrLimit}}
	}
	a := l.AdmitBatch(jobs)
	completed := make(map[*datapath.Job]datapath.Completion, len(a.Jobs))
	if a.Write != nil {
		for i, r := range a.Write() {
			completed[a.Jobs[i]] = r
		}
	}
	results := make([]datapath.Completion, len(jobs))
	deferred := 0
	for i, j := range jobs {
		if r, ok := completed[j]; ok {
			results[i] = r
		} else {
			results[i] = a.Immediate[deferred]
			deferred++
		}
	}
	return results
}

// Admit in collector order, before any physical worker can overtake another
// batch's stream prefixes. This is nonblocking logical flight admission only.
func (l *lane) AdmitBatch(jobs []*datapath.Job) datapath.BatchAdmission {
	if len(jobs) == 0 {
		return datapath.BatchAdmission{Immediate: []datapath.Completion{{Err: wire.ErrLimit}}}
	}
	if l.stripe == nil {
		return datapath.BatchAdmission{Jobs: jobs, Write: func() []datapath.Completion { return l.writeBatchPhysical(l, jobs) }}
	}
	fastBatch := true
	for _, job := range jobs {
		if !job.Interactive() {
			fastBatch = false
			break
		}
	}
	admissionStarted := time.Now()
	reservation, admissionErr := l.stripe.reserveDeliveryBatch(l.logicalContext(), jobs)
	// A deferred DATA member is a normal flow-control event. During busy TCP
	// multiplexing it can occur hundreds of times per second. Trace a sampled
	// aggregate instead of serializing every retry into the ring buffer.
	if errors.Is(admissionErr, errDeliveryDeferred) {
		if emit, total, sinceLast := l.deferredAdmissionTrace.observe(time.Now()); emit {
			l.traceBatchStage("delivery_admission_wait", admissionStarted, errorCode(admissionErr), map[string]any{"fast": fastBatch, "deferred_total": total, "deferred_since_last": sinceLast})
		}
	} else {
		selected, constraints := 0, (deliveryPlanDebug{})
		if reservation != nil {
			selected, constraints = len(reservation.jobs), reservation.debug
		}
		l.traceBatchStage("delivery_admission_wait", admissionStarted, errorCode(admissionErr), map[string]any{"fast": fastBatch, "batch_records": len(jobs), "admitted_records": selected, "constraints": constraints})
	}
	if admissionErr != nil {
		rejected := failBatch(jobs, nil, admissionErr)
		if errors.Is(admissionErr, errDeliveryStopped) || errors.Is(admissionErr, errDeliveryDeferred) {
			// A RESET/socket close can overtake an unstarted batch waiting on flight
			// credit. Complete it exactly once without failing sibling stream owners.
			for i := range rejected {
				rejected[i].NonFatal = true
				var deferred *deliveryDeferredError
				if errors.As(admissionErr, &deferred) {
					rejected[i].RetryWake = l.stripe.deliveryRetryWake(jobs[i])
				}
			}
		}
		return datapath.BatchAdmission{Immediate: rejected}
	}
	selected := make(map[*datapath.Job]bool, len(reservation.jobs))
	for _, j := range reservation.jobs {
		selected[j] = true
	}
	a := datapath.BatchAdmission{Jobs: reservation.jobs, Write: func() []datapath.Completion { return l.writeAdmittedBatch(reservation.jobs, reservation, fastBatch) }}
	for _, j := range jobs {
		if !selected[j] {
			r := j.RejectBeforeCommit(errDeliveryDeferred)
			r.NonFatal = true
			r.RetryWake = l.stripe.deliveryRetryWake(j)
			a.Immediate = append(a.Immediate, r)
		}
	}
	return a
}

func (l *lane) writeAdmittedBatch(jobs []*datapath.Job, reservation *deliveryReservation, fastBatch bool) (results []datapath.Completion) {
	defer func() { reservation.finish(jobs, results) }()
	if !fastBatch {
		release, err := l.AcquireBulkPreparation(l.logicalContext())
		if err != nil {
			return failBatch(jobs, nil, err)
		}
		defer release()
	}
	l.recordPumpQueueWait(jobs, fastBatch)
	selectStarted := time.Now()
	var target *lane
	var reservedGrant *wire.QuotaKey
	var reservedLocal *journal.Grant
	var reservedPhysical *stripeLease
	// Announce priority before choosing a grant: bulk must not overtake the
	// short packet between account selection, fsync and physical reservation.
	fastPending := fastBatch
	if fastPending {
		l.stripe.fastWaiters.Add(1)
		l.stripe.signal()
	}
	defer func() {
		if fastPending {
			l.stripe.fastWaiters.Add(-1)
			l.stripe.signal()
		}
	}()
	var localBytes uint64
	var err error
	var preferred *lane
	if fastBatch {
		preferred = l.stripe.shortTarget(jobs)
	}
	if l.side {
		localBytes, err = localBatchMIMESize(jobs)
		if err != nil {
			return failBatch(jobs, nil, err)
		}
		if len(l.envelopeKey) == 32 {
			// Conservative quota reservation before the account is selected.
			// Prepare charges the exact body; CloseGrant returns only unused,
			// definitively unspent headroom, never physical attempt debt.
			localBytes += uint64((512 + 2) / 3 * 4)
			if localBytes > dataBatchMIMEHardCap {
				return failBatch(jobs, nil, mailbox.ErrLimit)
			}
		}
	}
	for {
		if !l.side {
			var key wire.QuotaKey
			target, key, err = l.stripe.takeRemoteGrantPreferred(l.logicalContext(), l, preferred, fastBatch)
			if err == nil {
				reservedGrant = &key
			}
		} else {
			var grant journal.Grant
			target, grant, err = l.stripe.takeLocalGrantPreferred(l.logicalContext(), l, preferred, fastBatch, localBytes)
			if err == nil {
				reservedLocal = &grant
			}
		}
		if err != nil || target == nil || !fastBatch {
			break
		}
		if target.tryBeginFastPhysical() {
			if lease, ok := l.stripe.tryAcquireFastLane(target); ok {
				reservedPhysical = &lease
				defer target.endFastPhysical()
				l.stripe.fastWaiters.Add(-1)
				fastPending = false
				l.stripe.signal()
				break
			}
			target.endFastPhysical()
		}
		if reservedGrant != nil {
			_ = target.returnUnusedRemoteGrant(*reservedGrant)
			reservedGrant = nil
		}
		if reservedLocal != nil {
			_ = target.releaseReservedLocalGrant(*reservedLocal)
			reservedLocal = nil
		}
		// Cleanup won the race after selection. Its gcActive flag is now visible;
		// retry round-robin selection instead of waiting behind maintenance.
		runtime.Gosched()
	}
	l.recordGrantSelectWait(selectStarted, fastBatch)
	l.traceBatchStage("stripe_select_wait", selectStarted, errorCode(err), map[string]any{"fast": fastBatch})
	if err != nil {
		return failBatch(jobs, nil, err)
	}
	if target == nil {
		return failBatch(jobs, nil, errStripeUnavailable)
	}
	if l.diag != nil && l.diag.tracer != nil {
		details, _ := json.Marshal(map[string]any{
			"owner_account":  l.account.ID,
			"target_account": target.account.ID,
			"records":        len(jobs),
			"remote":         target != l,
		})
		l.diag.trace(traceEvent{Event: "stripe_select", Details: details})
	}
	l.stripe.bindDeliveryCarrier(jobs, target.account.ID)
	return target.writeBatchPhysicalPrepared(l, jobs, reservedGrant, reservedLocal, reservedPhysical)
}

// WriteBatch is the S1 physical DATA writer. Several independently encrypted
// DATA records from one authenticated user/session share one RFC822 APPEND and
// therefore one durable physical attempt. Logical attempts stay distinct in
// the byte engine; cleanup of the single mail proves cleanup for every member.
func (l *lane) writeBatchPhysical(owner *lane, jobs []*datapath.Job) []datapath.Completion {
	return l.writeBatchPhysicalReserved(owner, jobs, nil)
}

func (l *lane) writeBatchPhysicalReserved(owner *lane, jobs []*datapath.Job, reservedGrant *wire.QuotaKey) []datapath.Completion {
	return l.writeBatchPhysicalWithReservations(owner, jobs, reservedGrant, nil)
}

func (l *lane) writeBatchPhysicalWithReservations(owner *lane, jobs []*datapath.Job, reservedGrant *wire.QuotaKey, reservedLocal *journal.Grant) []datapath.Completion {
	return l.writeBatchPhysicalPrepared(owner, jobs, reservedGrant, reservedLocal, nil)
}

func (l *lane) writeBatchPhysicalPrepared(owner *lane, jobs []*datapath.Job, reservedGrant *wire.QuotaKey, reservedLocal *journal.Grant, reservedPhysical *stripeLease) (results []datapath.Completion) {
	defer func() {
		if reservedPhysical != nil {
			reservedPhysical.release()
		}
	}()
	acquireSlot := func(ctx context.Context, fast bool) (stripeLease, error) {
		if reservedPhysical != nil {
			lease := *reservedPhysical
			reservedPhysical = nil
			return lease, nil
		}
		return l.acquirePhysicalAppendSlot(ctx, fast)
	}
	physicalAdmitted := reservedLocal != nil
	defer func() {
		if reservedLocal != nil {
			_ = l.data.CloseGrant(reservedLocal.Ref)
		}
		if physicalAdmitted {
			l.endPhysicalWrite()
		}
	}()

	reservedPending := reservedGrant != nil
	defer func() {
		if reservedPending && reservedGrant != nil {
			_ = l.returnUnusedRemoteGrant(*reservedGrant)
		}
	}()
	if owner == nil {
		owner = l
	}
	if !physicalAdmitted {
		if !l.beginPhysicalWrite() {
			return l.retryBeforeAppend(owner, jobs, nil, context.Canceled, nil)
		}
		physicalAdmitted = true
	}
	opCtx := l.ctx
	cancelOp := func() {}
	if owner != l {
		var cancel context.CancelFunc
		opCtx, cancel = context.WithCancel(l.ctx)
		stopOwner := context.AfterFunc(owner.logicalContext(), cancel)
		cancelOp = func() {
			stopOwner()
			cancel()
		}
	}
	defer cancelOp()
	started := time.Now()
	if len(jobs) == 0 || len(jobs) > wire.MaxDataBatchRecords {
		return []datapath.Completion{{Err: wire.ErrLimit}}
	}
	fastBatch := true
	for _, job := range jobs {
		if !job.Interactive() {
			fastBatch = false
			break
		}
	}
	results = make([]datapath.Completion, 0, len(jobs))
	packets := make([][]byte, 0, len(jobs))
	counters := make([]uint64, 0, len(jobs))
	attempts := make([]uint64, 0, len(jobs))
	defer func() {
		for _, packet := range packets {
			clear(packet)
		}
	}()
	for _, job := range jobs {
		r, packet, counter := job.SealForBatch(l.cipher)
		results = append(results, r)
		attempts = append(attempts, r.Attempt)
		if r.Err != nil {
			return failBatch(jobs, results, r.Err)
		}
		packets = append(packets, packet)
		counters = append(counters, counter)
		l.traceFrameSize("data_writer_member", job.FrameInfo(), &counter, int(job.Attempt().PayloadBytes))
	}
	var batch wire.ID
	if _, err := rand.Read(batch[:]); err != nil {
		return failBatch(jobs, results, err)
	}
	body, err := wire.PackDataBatch(batch, packets)
	folder := l.nextDataFolder()
	if fastBatch && l.dataShards > 0 {
		folder = l.sendFolder
	}
	if err != nil {
		return failBatch(jobs, results, err)
	}
	defer clear(body)
	payloadBytes := int64(0)
	for _, job := range jobs {
		payloadBytes += int64(job.Attempt().PayloadBytes)
	}
	if l.diag != nil {
		l.diag.mu.Lock()
		l.diag.line.DataAttempts++
		l.diag.line.DataRecords += int64(len(jobs))
		l.diag.line.DataPayloadBytes += payloadBytes
		l.diag.line.DataBatchCount++
		l.diag.line.DataBatchRecords += int64(len(jobs))
		l.diag.line.DataBatchBodyBytes += int64(len(body))
		l.diag.mu.Unlock()
	}
	if l.diag != nil && l.diag.tracer != nil {
		details, _ := json.Marshal(map[string]any{"batch": batch, "records": len(jobs), "payload_bytes": len(body), "attempts": attempts, "fast": fastBatch})
		defer func() {
			code := "OK"
			for _, r := range results {
				if r.Err != nil {
					code = errorCode(r.Err)
					break
				}
			}
			l.diag.trace(traceEvent{Event: "data_batch_completion", Code: code, DurationUS: time.Since(started).Microseconds(), Details: details})
		}()
	}

	if l.side {
		encoded, encErr := mailbox.EncodedSize(len(body))
		if len(l.envelopeKey) == 32 {
			encoded += (512 + 2) / 3 * 4
		}
		if encErr != nil || encoded > dataBatchMIMEHardCap {
			if encErr == nil {
				encErr = mailbox.ErrLimit
			}
			return failBatch(jobs, results, encErr)
		}
		grantStarted := time.Now()
		var g journal.Grant
		var grantErr error
		if reservedLocal != nil {
			g = *reservedLocal
			if g.Limit.Bytes < encoded || g.Limit.Messages != 1 {
				grantErr = errors.New("LOCAL_BATCH_RESERVATION_SIZE")
			}
		} else {
			g, grantErr = l.waitLocalGrantContextClass(opCtx, encoded, fastBatch)
		}
		l.recordDataWait(grantStarted, fastBatch, true)
		l.traceBatchStage("data_batch_grant", grantStarted, errorCode(grantErr), map[string]any{"records": len(jobs), "encoded_bytes": encoded})
		if grantErr != nil {
			return l.retryBeforeAppend(owner, jobs, results, fmt.Errorf("data_writer: %w", grantErr), nil)
		}
		var physical wire.ID
		if _, grantErr = rand.Read(physical[:]); grantErr != nil {
			_ = l.data.CloseGrant(g.Ref)
			return failBatch(jobs, results, grantErr)
		}
		physicalBody := body
		if len(l.envelopeKey) == 32 {
			key := wire.QuotaKey{Store: g.Ref.Store, Epoch: g.Run, Grant: g.Ref.ID, Session: g.Ref.Scope.Session, User: g.Ref.Scope.User, Account: g.Ref.Scope.Account}
			_, physicalBody, encErr = wire.BindDataEnvelope(key, 1, physical, 0, folder, body, l.envelopeKey, canonicalDataSize)
			if encErr != nil {
				_ = l.data.CloseGrant(g.Ref)
				return failBatch(jobs, results, encErr)
			}
			defer clear(physicalBody)
		}
		mime, encErr := mailbox.Encode(physicalBody)
		encoded = uint64(len(mime))
		if encErr != nil || encoded > g.Limit.Bytes || encoded > dataBatchMIMEHardCap {
			clear(mime)
			_ = l.data.CloseGrant(g.Ref)
			if encErr == nil {
				encErr = mailbox.ErrLimit
			}
			return failBatch(jobs, results, encErr)
		}
		// Reserve actual service before transferring durable writer permission.
		// Preparation + Start is one fsynced transaction; no APPEND is possible
		// until it returns success, and an unknown commit reply stays charged.
		lease, slotErr := acquireSlot(opCtx, fastBatch)
		if slotErr != nil {
			clear(mime)
			cleanupErr := l.data.CloseGrant(g.Ref)
			return l.retryBeforeAppend(owner, jobs, results, slotErr, cleanupErr)
		}
		if opCtx.Err() != nil {
			lease.release()
			clear(mime)
			cleanupErr := l.data.CloseGrant(g.Ref)
			return l.retryBeforeAppend(owner, jobs, results, opCtx.Err(), cleanupErr)
		}
		prepareStarted := time.Now()
		a, prepErr := l.data.PrepareStarted(g.Ref, folder, physical, encoded, physicalBody)
		l.traceBatchStage("data_journal_prepare_start", prepareStarted, errorCode(prepErr), map[string]any{"encoded_bytes": encoded})
		if prepErr != nil {
			lease.release()
			clear(mime)
			_ = l.data.CloseGrant(g.Ref)
			return failBatch(jobs, results, fmt.Errorf("data_writer: %w", prepErr))
		}
		if opCtx.Err() != nil {
			lease.release()
			clear(mime)
			// This writer has definitively not invoked APPEND; unlike a lost
			// network response, cancellation here proves no physical write.
			cleanupErr := l.data.CompleteAndClose(a.Ref, journal.RejectedBeforeCommit)
			reservedLocal = nil
			return l.retryBeforeAppend(owner, jobs, results, opCtx.Err(), cleanupErr)
		}
		appendStarted := time.Now()
		appendErr := appendData(opCtx, l.mail, folder, mime, fastBatch)
		l.observeDataServiceClass(len(mime), time.Since(appendStarted), appendErr == nil, fastBatch)
		lease.release()
		l.traceBatchStage("data_batch_append", appendStarted, errorCode(appendErr), map[string]any{"records": len(jobs), "encoded_bytes": encoded, "direction": "s2c"})
		clear(mime)
		journalOutcome := journal.Accepted
		logicalOutcome := flow.Committed
		if appendErr != nil {
			journalOutcome = journal.Uncertain
			logicalOutcome = flow.CommitUnknown
		}
		completeStarted := time.Now()
		storeErr := l.data.CompleteAndClose(a.Ref, journalOutcome)
		l.traceBatchStage("data_journal_complete_close", completeStarted, errorCode(storeErr), nil)
		if storeErr == nil {
			reservedLocal = nil
		}
		combined := errors.Join(appendErr, storeErr)
		// An unknown APPEND is recoverable at the logical byte layer iff the
		// journal successfully retained that uncertainty. Keep the owner Pump
		// alive; authenticated progress/NACK will either prove delivery or repair
		// the missing range through another healthy carrier.
		nonFatalUnknown := appendErr != nil && storeErr == nil
		receiptEligible := appendErr == nil && storeErr == nil && !fastBatch
		l.registerOutgoing(outgoing{ref: a.Ref, counters: counters, records: len(counters), attempts: attempts, owner: owner, usefulBytes: payloadBytes,
			receiptStarted: appendStarted, receiptEligible: receiptEligible})
		for i := range results {
			results[i].Outcome = logicalOutcome
			results[i].NonFatal = nonFatalUnknown
			if combined != nil {
				results[i].Err = fmt.Errorf("data_writer: %w", combined)
			}
		}
		return results
	}

	grantStarted := time.Now()
	var key wire.QuotaKey
	var grantErr error
	if reservedGrant != nil {
		key = *reservedGrant
		reservedPending = false
	} else {
		key, grantErr = l.takeRemoteGrantContext(opCtx, fastBatch)
	}
	l.recordDataWait(grantStarted, fastBatch, false)
	class := "bulk"
	if fastBatch {
		class = "fast"
	}
	l.traceBatchStage("data_batch_grant", grantStarted, errorCode(grantErr), map[string]any{"records": len(jobs), "direction": "c2s", "class": class})
	if grantErr != nil {
		return failBatch(jobs, results, grantErr)
	}
	var lease stripeLease
	var usage wire.UsageReport
	var physicalBody []byte
	for {
		var slotErr error
		lease, slotErr = acquireSlot(opCtx, fastBatch)
		if slotErr != nil {
			returnErr := l.returnUnusedRemoteGrant(key)
			return l.retryBeforeAppend(owner, jobs, results, slotErr, returnErr)
		}
		spendStarted := time.Now()
		var spendErr error
		if len(l.envelopeKey) == 32 {
			usage, physicalBody, spendErr = l.wallet.SpendEnvelope(key, folder, body, l.envelopeKey, canonicalDataSize)
		} else {
			usage, physicalBody, spendErr = l.wallet.Spend(key, folder, body, canonicalDataSize)
		}
		// Spending failure on a full cumulative lease is an expected, atomic
		// rollover event under MULTI load, not evidence of corrupt accounting.
		// Sample it instead of writing potentially thousands of JSON events and
		// displacing the application-failure window from the bounded trace ring.
		if errors.Is(spendErr, wallet.ErrExhausted) {
			if l.diag != nil && l.diag.tracer != nil {
				if emit, total, delta := l.grantRolloverTrace.observe(time.Now()); emit {
					l.traceBatchStage("grant_rollover", spendStarted, "WALLET_EXHAUSTED", map[string]any{
						"records": len(jobs), "physical_batch_bytes": len(body),
						"rollover_total": total, "rollover_since_last": delta, "fast": fastBatch,
					})
				}
			}
		} else {
			l.traceBatchStage("data_batch_spend", spendStarted, errorCode(spendErr), map[string]any{"records": len(jobs), "decoded_bytes": len(physicalBody)})
		}
		if !errors.Is(spendErr, wallet.ErrExhausted) {
			if spendErr != nil {
				lease.release()
				returnErr := l.returnUnusedRemoteGrant(key)
				return failBatch(jobs, results, fmt.Errorf("data_writer: %w", errors.Join(spendErr, returnErr)))
			}
			break
		}

		// This cumulative grant is valid but its remaining byte allowance cannot
		// fit the exact next physical batch. Spend rolled back atomically, so no
		// descriptor or nonce binding was committed. Fence the exhausted lease and
		// retry these already-sealed (but never sent) records on the next grant.
		lease.release()
		if fenceErr := l.fenceRemoteGrant(key); fenceErr != nil {
			return failBatch(jobs, results, fmt.Errorf("data_writer: %w", errors.Join(spendErr, fenceErr)))
		}
		if next, ok := l.tryTakeRemoteGrant(fastBatch); ok {
			key = next
			continue
		}
		if l.stripe != nil && owner.logicalContext().Err() == nil && l.ctx.Err() == nil {
			// Spend was rolled back and this key is durably fenced: there is
			// no physical APPEND or uncertain debt for these jobs. Yield them
			// to the logical actor so a fresh handoff selects a grant across
			// the whole pool, instead of pinning a worker to this account's GC.
			results = failBatch(jobs, results, spendErr)
			for i := range results {
				results[i].NonFatal = true
			}
			return results
		}
		retryStarted := time.Now()
		key, grantErr = l.takeRemoteGrantContext(opCtx, fastBatch)
		l.recordDataWait(retryStarted, fastBatch, false)
		l.traceBatchStage("data_batch_grant_rollover", retryStarted, errorCode(grantErr), map[string]any{"records": len(jobs), "class": class})
		if grantErr != nil {
			return l.retryBeforeAppend(owner, jobs, results, fmt.Errorf("data_writer: %w", grantErr), nil)
		}
	}
	defer clear(physicalBody)
	mime, encErr := mailbox.Encode(physicalBody)
	if encErr != nil || len(mime) > dataBatchMIMEHardCap {
		clear(mime)
		if encErr == nil {
			encErr = mailbox.ErrLimit
		}
		// Spend is durable already: this grant cannot be reused even though APPEND
		// never started. Fence it and let recovery reconcile the reserved debt.
		release, fenceErr := l.wallet.Fence(key)
		_ = release
		lease.release()
		return failBatch(jobs, results, fmt.Errorf("data_writer: %w", errors.Join(encErr, fenceErr)))
	}
	// Spend is durable and the exact physical body is fixed. Publish the
	// authenticated descriptor concurrently with APPEND for both DATA classes.
	// Waiting for APPEND's tagged completion first adds a provider round trip
	// before the receiver may accept the body. Registration must precede either
	// publication; early reports never return debt or release this writer's grant.
	l.registerRemoteLogical(key, owner, attempts)
	earlyUsage := len(l.envelopeKey) == 32 || l.controlSend(usage) == nil
	appendStarted := time.Now()
	if fastBatch && owner.stripe != nil && !jobs[0].Attempt().Repair {
		owner.stripe.expectFastResponseOn(owner, l)
	}
	appendErr := appendData(opCtx, l.mail, folder, mime, fastBatch)
	l.observeDataServiceClass(len(mime), time.Since(appendStarted), appendErr == nil, fastBatch)
	lease.release()
	l.traceBatchStage("data_batch_append", appendStarted, errorCode(appendErr), map[string]any{"records": len(jobs), "encoded_bytes": len(mime), "direction": "c2s"})
	if appendErr == nil && l.accountView != nil {
		l.accountView.upload.Add(uint64(payloadBytes))
	}
	clear(mime)
	logicalOutcome := flow.Committed
	if appendErr != nil {
		logicalOutcome = flow.CommitUnknown
	}
	// Only descriptor enqueue may be retried after early backpressure. APPEND
	// remains one-shot, including an unknown physical completion.
	var bookErr error
	if earlyUsage {
		bookErr = l.finishRemoteUsage(key, usage.Total)
	} else {
		bookErr = l.publishRemoteUsage(key, usage)
	}
	combined := errors.Join(appendErr, bookErr)
	nonFatalUnknown := appendErr != nil && bookErr == nil
	for i := range results {
		results[i].Outcome = logicalOutcome
		results[i].NonFatal = nonFatalUnknown
		if combined != nil {
			results[i].Err = fmt.Errorf("data_writer: %w", combined)
		}
	}
	return results
}

func (l *lane) startPump() error {
	if l.stripe != nil {
		owner, err := l.stripe.claimOwner(l)
		if err != nil {
			return err
		}
		if owner != l {
			select {
			case <-owner.actorReady:
			case <-owner.logicalContext().Done():
				return errStripeUnavailable
			case <-l.ctx.Done():
				return l.ctx.Err()
			}
			l.pump = owner.pump
			if err := l.queueRejoinProbe(); err != nil {
				return err
			}
			if l.side {
				return l.offer()
			}
			return nil
		}

	}
	if l.pump != nil {
		return nil
	}
	cfg := datapath.DefaultConfig(1, []string{l.account.ID})
	cfg.Limits.Streams = runtimeOwnerStreams
	cfg.Limits.Logical = runtimeLogicalBytes
	cfg.Limits.Items = 1024
	cfg.Limits.Attempts = 16384
	cfg.Metadata = 65536
	cfg.IsolateStreamTimeouts = true
	cfg.CombinedReceiveProgress = true
	cfg.AccountCaps[l.account.ID] = runtimeAccountBytes
	// Keep one MiB of the existing 4 MiB connection/account budget available
	// to interactive streams while allowing one saturated TCP stream a 3 MiB
	// bandwidth-delay window. This supports ~10 Mbps even around 2.4 s end-to-end
	// delay without increasing the existing total receive/account memory bound.
	cfg.StreamWindow = runtimeStreamWindow
	cfg.MaxStreamWindow = flow.MaxStreamWindow
	cfg.ProbeInterval = 2 * time.Second
	// STATUS is an urgent control and may arrive before an accepted DATA BODY.
	// Let that BODY become visible before a NACK can spend another mail permit.
	cfg.DeliveryGrace = 1750 * time.Millisecond
	cfg.InteractiveDeliveryGrace = 250 * time.Millisecond
	cfg.InteractiveProbeInterval = 400 * time.Millisecond
	cfg.ReceiveBytes = runtimeReceiveBytes
	var e error
	l.pump, e = datapath.StartPump(l.logicalContext(), datapath.PumpConfig{SocketFailure: l.traceSocketFailure, Engine: cfg, Account: l.account.ID, ControlQueue: 256, InteractiveReserve: runtimeInteractiveReserve, InteractiveAttemptReserve: runtimeOwnerStreams, InteractiveItemReserve: runtimeOwnerStreams, BulkAdmissionLimit: l.bulkAdmissionLimit, Acknowledged: func(n uint64) {
		if l.stripe != nil {
			l.stripe.bulkDelivered.Add(int64(n))
		}
	}, DeliveryProgress: func(id, frontier uint64) {
		if l.stripe != nil {
			l.stripe.deliveryProgress(id, frontier)
		}
	}, DeliveryStopped: func(id uint64) {
		if l.stripe != nil {
			l.stripe.deliveryStopped(id)
		}
	}, ReadCoalesce: 5 * time.Millisecond, Retired: func(id uint64) {
		if l.side && l.exit != nil {
			_ = l.exit.Retire(id)
		}
		if l.stripe != nil {
			l.stripe.retire(id, l)
		}
	}}, l)
	if e != nil {
		return e
	}
	if l.side {
		l.exit, e = datapath.NewTCPExit(l.logicalContext(), datapath.ExitConfig{Generation: 1, MaxSockets: runtimeOwnerStreams, MaxDials: 16, Metadata: 65536, AllowPrivate: l.allowPrivate, InternalDial: l.internalDial})
		if e != nil {
			return e
		}
		if l.stripe != nil {
			l.startOwnerActor()
		}
		return l.offer()
	}
	if l.stripe != nil {
		l.startOwnerActor()
	}
	return nil
}
func (l *lane) issuedGrantCounts() (fastActive, bulkActive int) {
	for key, pending := range l.issued {
		if !pending {
			continue
		}
		if l.issuedFast[key] {
			fastActive++
		} else {
			bulkActive++
		}
	}
	return fastActive, bulkActive
}

func (l *lane) issueRemoteGrant(limit pool.Amount, fast bool) error {
	return l.issueRemoteGrantBounded(limit, fast, false)
}

func (l *lane) issueRemoteGrantBounded(limit pool.Amount, fast, recovery bool) error {
	// Incoming bulk produces receive-window, receipt and cleanup controls on
	// this endpoint. Stop its refill before the reserved critical slots fill;
	// fast leases retain service while proven cleanup drains the debt.
	if !fast && l.stripe != nil && l.control != nil {
		control, err := l.control.Held(l.account.ID, l.user.ID)
		if err != nil {
			return err
		}
		if control.Messages >= 64 {
			return journal.ErrLimit
		}
	}
	l.localGrantMu.Lock()
	held, heldErr := l.data.Held(l.account.ID, l.user.ID)
	if heldErr != nil {
		l.localGrantMu.Unlock()
		return heldErr
	}
	// Remote leases must never consume the final local latency reserve. This
	// same bound also caps the temporary bulk replacement below, so cleanup RTT
	// can be pipelined without increasing the existing 8 MiB uncertainty cap.
	profile, profileErr := l.data.Limit(l.account.ID, l.user.ID)
	if profileErr != nil {
		l.localGrantMu.Unlock()
		return profileErr
	}
	// Legacy reservations remain charged, but must not strand bootstrap behind
	// a fixed 2 MiB / 32-message lease. Borrow only unused directional headroom,
	// retaining one maximum local bulk BODY and the local latency reserve.
	if recovery {
		var err error
		limit, err = l.recoveryQuotaLocked(profile, held, fast)
		if err != nil {
			l.localGrantMu.Unlock()
			return err
		}
	}
	if l.stripe != nil {
		_, remote, err := l.data.HeldByOrigin(l.account.ID, l.user.ID)
		if err != nil {
			l.localGrantMu.Unlock()
			return err
		}
		// Bound each direction within the existing journal while preserving the
		// established inbound lease shapes. Count actual durable debt by origin.
		_, remoteShare := duplexDataShares(profile.Bytes)
		if !recovery && (remote.Bytes+limit.Bytes > remoteShare || remote.Messages+limit.Messages > profile.Messages-profile.Messages/4) {
			l.localGrantMu.Unlock()
			return journal.ErrLimit
		}
	}
	if held.Bytes+limit.Bytes > profile.Bytes-localFastReserveBytes {
		l.localGrantMu.Unlock()
		return journal.ErrLimit
	}
	g, e := l.data.IssueRemote(journal.Scope{Account: l.account.ID, User: l.user.ID, Session: l.cipher.Context().Session}, limit)
	l.localGrantMu.Unlock()
	if e != nil {
		return e
	}
	offer, e := l.data.RemoteOffer(g.Ref)
	if e != nil {
		return e
	}
	l.issued[offer.Key] = true
	l.issuedFast[offer.Key] = fast
	if recovery && l.diag != nil {
		l.diag.event(fmt.Sprintf("event=grant_recovery_capacity bytes=%d messages=%d held_bytes=%d held_messages=%d", limit.Bytes, limit.Messages, held.Bytes, held.Messages))
	}
	if fast && limit.Messages < fastLeaseMessages {
		return l.controlSend(wire.RecoveryFastGrant{Offer: offer})
	}
	return l.controlSend(offer)
}

func (l *lane) issueBulkGrant() error {
	err := l.issueRemoteGrant(pool.Amount{Bytes: dataLeaseBytes, Messages: dataLeaseMessages}, false)
	if errors.Is(err, journal.ErrLimit) {
		return l.issueRemoteGrantBounded(pool.Amount{}, false, true)
	}
	return err
}

// offerBulkReplacement pipelines a replacement only after the peer has fenced
// one bulk lease. The other normal bulk lease remains usable while the closed
// grant is physically cleaned. At most one temporary replacement may coexist
// with the two baseline leases, and the unchanged 8 MiB journal bound remains
// authoritative.
func (l *lane) offerBulkReplacement() error {
	_, bulkActive := l.issuedGrantCounts()
	if bulkActive >= remoteBulkLeases+1 {
		return nil
	}
	e := l.issueBulkGrant()
	if errors.Is(e, journal.ErrLimit) {
		return nil
	}
	if e == nil && l.diag != nil {
		l.diag.mu.Lock()
		l.diag.line.BulkReplacementGrants++
		l.diag.mu.Unlock()
	}
	return e
}

func (l *lane) offer() error {
	if l.quiescing.Load() {
		return nil
	}
	if err := l.reconcileUnpublishedGrants(); err != nil {
		return err
	}
	fastActive, bulkActive := l.issuedGrantCounts()

	// Publish the latency lease first. The peer classifies it by its distinct
	// bounded quota, so control scheduling cannot accidentally turn a bulk grant
	// into the sole fast reserve.
	for fastActive < fastGrantReserve {
		e := l.issueRemoteGrant(pool.Amount{Bytes: fastLeaseBytes, Messages: fastLeaseMessages}, true)
		if errors.Is(e, journal.ErrLimit) {
			e = l.issueRemoteGrantBounded(pool.Amount{}, true, true)
		}
		if errors.Is(e, journal.ErrLimit) {
			break
		}
		if e != nil {
			return e
		}
		fastActive++
	}
	for bulkActive < remoteBulkLeases {
		e := l.issueBulkGrant()
		if errors.Is(e, journal.ErrLimit) {
			break
		}
		if e != nil {
			return e
		}
		bulkActive++
	}
	// A quota-limited offer pass is not successful bootstrap. Do not admit
	// traffic until both service classes have actually been published. Once
	// activated, temporary depletion is ordinary backpressure, not a reconnect.
	if fastActive > 0 && bulkActive > 0 {
		l.markGrantReady()
	} else if l.diag != nil {
		limit, limitErr := l.data.Limit(l.account.ID, l.user.ID)
		held, heldErr := l.data.Held(l.account.ID, l.user.ID)
		reason := "DATA_GRANTS_PENDING"
		if limitErr == nil && heldErr == nil {
			if held.Messages > limit.Messages || limit.Messages-held.Messages < 4 {
				reason = "DATA_MESSAGE_HEADROOM"
			} else if held.Bytes > limit.Bytes || limit.Bytes-held.Bytes < localFastReserveBytes+2*dataBatchMIMEHardCap {
				reason = "DATA_BYTE_HEADROOM"
			}
		}
		l.diag.mu.Lock()
		l.diag.line.GrantWaitReason = reason
		l.diag.mu.Unlock()
	}
	return nil
}

func (l *lane) markGrantReady() {
	if l.quiescing.Load() {
		return
	}
	l.everReady.Store(true)
	if l.diag != nil {
		l.diag.mu.Lock()
		l.diag.line.GrantWaitReason = ""
		l.diag.mu.Unlock()
	}
	l.ready.Store(true)
	if l.stripe != nil {
		l.stripe.completeRekey(l)
		l.stripe.signal()
	}
	l.stage("ready")
}

// Bootstrap gets its own deadline AFTER activation, not while the gate waits.
func (l *lane) initBootstrap() error {
	if l.boot != nil {
		return nil
	}
	role := session.Client
	var store wire.ID
	var run uint64
	if l.side {
		role = session.Server
		store = l.data.StoreID()
		run = l.data.Run()
	}
	var e error
	l.boot, e = session.NewBootstrap(role, 1, l.now(), store, run)
	if e == nil {
		l.stage("coordinator")
	}
	return e
}
func (l *lane) bootAction(a session.BootstrapAction) error {
	if a.Proposed != nil {
		var e error
		l.wallet, e = wallet.OpenBound(filepath.Join(l.dir, "wallet.db"), a.Proposed.Store, l.cipher)
		if e != nil {
			return e
		}
		a, e = l.boot.CommitStore(1, l.now(), a.Proposed.Store)
		if e != nil {
			return e
		}
	}
	if a.Send != nil {
		if e := l.SendControl(l.ctx, *a.Send); e != nil {
			return e
		}
	}
	if a.Ready {
		l.stage("grants")
		if e := l.startPump(); e != nil {
			return e
		}
		if e := l.replayBootstrapData(); e != nil {
			return e
		}
		l.startDataReader()
		return nil
	}
	return nil
}
func (l *lane) process(f wire.Frame) error {
	if f.Type == wire.MailReceipt {
		m, e := wire.DecodeControl(f)
		if e != nil {
			return e
		}
		l.mu.Lock()
		l.mailReceipts = make(map[uint64]bool, len(m.(wire.MailReceived).Counters))
		for _, counter := range m.(wire.MailReceived).Counters {
			l.mailReceipts[counter] = true
		}
		for i := range l.out {
			o := &l.out[i]
			pending := o.counters[:0]
			for _, counter := range o.counters {
				if !l.mailReceipts[counter] {
					pending = append(pending, counter)
				}
			}
			o.counters = pending
			if !o.ack && len(pending) == 0 {
				l.obsolete[o.ref] = true
			}
			l.observeDownloadDeliveryLocked(o)
		}
		l.refreshUnreceivedLocalLocked()
		l.mu.Unlock()
		return nil
	}
	if f.Type == wire.Confirm || f.Type == wire.Ready || f.Type == wire.ReadyAck {
		a, e := l.gate.Receive(1, l.now(), f)
		if e != nil {
			return e
		}
		if l.gate.Ready() {
			if e = l.initBootstrap(); e != nil {
				return e
			}
		}
		for _, out := range a.Send {
			if e = l.SendControl(l.ctx, out); e != nil {
				return e
			}
		}
		return nil
	}
	if !l.gate.Ready() {
		return errors.New("SESSION_NOT_ACTIVATED")
	}
	if f.Type == wire.CoordinatorInfo || f.Type == wire.CoordinatorAck {
		a, e := l.boot.Receive(1, l.now(), f)
		if e != nil {
			return e
		}
		return l.bootAction(a)
	}
	if !l.boot.Ready() {
		if deferred, err := l.deferBootstrapReconciliation(f); deferred || err != nil {
			return err
		}
		return errors.New("COORDINATOR_NOT_READY")
	}
	if f.Stream != 0 && l.stripe != nil {
		return l.stripe.dispatchStream(l, f)
	}
	if f.Type == wire.Data {
		return l.deliver(f)
	}
	c, e := wire.DecodeControl(f)
	if e != nil {
		return e
	}
	if f.Stream != 0 {
		return l.processStream(f)
	}
	switch m := c.(type) {
	case wire.Notice:
		return l.receiveNotice(m)
	case wire.NoticeAck:
		if !l.side || l.stripe == nil {
			return errors.New("SERVER_MESSAGE_DIRECTION")
		}
		l.stripe.noticeDelivery.acknowledge(m.Revision)
		return nil
	case wire.GrantRecovery:
		return l.reconcileGrantOffer(m.Offer)
	case wire.RecoveryFastGrant:
		return l.acceptRemoteGrant(m.Offer, true)
	case wire.StorageGrant:
		return l.acceptRemoteGrant(m, m.Limit.Bytes == fastLeaseBytes && m.Limit.Messages == fastLeaseMessages)
	case wire.UsageReport:
		if !l.side {
			return errors.New("GRANT_DIRECTION")
		}
		status, applyErr := l.data.ApplyRemote(l.user.ID, l.data.Run(), m)
		if applyErr != nil {
			if errors.Is(applyErr, journal.ErrMissing) {
				return nil
			}
			if errors.Is(applyErr, journal.ErrOrder) && m.Attempt != nil {
				if repaired, err := l.repairDescriptorGap(m); repaired || err != nil {
					return err
				}
			}
			return applyErr
		}
		if status.Registered.Messages > 0 || status.Released.Messages > 0 {
			// Acknowledge the durable descriptor prefix without waiting for BODY
			// visibility or physical GC. Registered is not Released: this prunes
			// usage retries while retaining every byte of physical debt.
			if e := l.publishCleanup(status); e != nil {
				return e
			}
		}
		if m.Attempt != nil {
			if l.dataProofQuarantined(m.Attempt.Digest) {
				return nil
			}
			if m.Key.Session != l.cipher.Context().Session {
				// Old DATA is never delivered in this epoch. Retain just its
				// durable cleanup descriptor, not an unbounded replay cache of
				// already reclaimed old ordinals.
				raw := l.pending[m.Attempt.Digest]
				delete(l.pending, m.Attempt.Digest)
				return l.remoteData(raw, m)
			}
			if _, exists := l.reports[m.Attempt.Digest]; !exists && len(l.reports) >= 128 {
				// Prefer shedding one expired or invalid physical retry cache
				// over disconnecting a healthy carrier. Durable debt is retained.
				l.evictOldestBadDataProof()
				if len(l.reports) >= 128 {
					return errors.New("DESCRIPTOR_LIMIT")
				}
			}
			l.reports[m.Attempt.Digest] = m
			if raw, ok := l.pending[m.Attempt.Digest]; ok {
				delete(l.pending, m.Attempt.Digest)
				if e := l.remoteData(raw, m); e != nil {
					return e
				}
			}
		}
	case wire.StorageRelease:
		if !l.side {
			return errors.New("GRANT_DIRECTION")
		}
		wasUsable := l.issued[m.Key]
		status, e := l.data.ApplyRemote(l.user.ID, l.data.Run(), m)
		if errors.Is(e, journal.ErrMissing) {
			return nil
		}
		if e != nil {
			return e
		}
		// StorageRelease fences the lease at the peer.  Its used physical bytes
		// remain charged in the journal until proof cleanup, but it must no longer
		// count as a usable grant.  Otherwise one failed replacement attempt can
		// leave the active window short until the whole old lease is deleted.
		if wasUsable && status.Closed {
			l.issued[m.Key] = false
		}
		return l.publishCleanup(status)
	case wire.CleanupStatus:
		return l.handleCleanupStatus(m)
	case wire.DescriptorRequest:
		return l.replayDescriptors(m)
	case wire.Retirement:
		if l.side {
			ack, e := l.data.RetireRemote(l.user.ID, l.data.Run(), m)
			if e != nil {
				return e
			}
			delete(l.issued, m.Key)
			delete(l.issuedFast, m.Key)
			if e = l.controlSend(ack); e != nil {
				return e
			}
			return l.offer()
		}
		if !m.Ack {
			return errors.New("RETIRE_DIRECTION")
		}
		if e = l.wallet.CompleteRetirement(m); e != nil {
			if l.diag != nil {
				l.diag.event(fmt.Sprintf("event=wallet_transition_error operation=retirement code=%s grant=%d", errorCode(e), m.Key.Grant))
			}
			return e
		}
		delete(l.queued, m.Key)
		delete(l.retireSent, m.Key)
	case wire.Echo:
		if !m.Reply {
			if m.Token&rekeyRequestToken != 0 {
				if l.side {
					// Reply only after the old physical DATA and its authenticated
					// receive evidence have drained. The client serializes accounts.
					l.quiesceForRekey(m.Token)
					return nil
				}
				l.rekeyRequested = true
			}
			m.Reply = true
			return l.controlSend(m)
		}
		if !l.side && l.quiescing.Load() && m.Token == l.rekeyDrainToken {
			l.rekeyPeerDrained = true
		}
		l.pongs.Add(1)
		l.completeEchoReply(m.Token)
	case wire.EndSession:
		return errors.New("PEER_CLOSED")
	default:
		return l.deliver(f)
	}
	return nil
}
func (l *lane) acceptRemoteGrant(m wire.StorageGrant, fastClass bool) error {
	if l.side || m.Key.Account != l.account.ID {
		return errors.New("GRANT_SCOPE")
	}
	accepted, e := l.wallet.AcceptCurrentOffer(m)
	if e != nil {
		if l.diag != nil {
			l.diag.event(fmt.Sprintf("event=wallet_transition_error operation=offer code=%s grant=%d", errorCode(e), m.Key.Grant))
		}
		return e
	}
	if !accepted {
		return nil
	}
	if !l.queued[m.Key] {

		queue := l.grants
		if fastClass {
			queue = l.fastGrants
		}
		// Publish the metadata before handing the key to concurrent workers.
		// Channel delivery is the visibility boundary, not the later wake.
		l.mu.Lock()
		l.grantLimits[m.Key] = m.Limit
		l.grantFast[m.Key] = fastClass
		l.mu.Unlock()
		select {
		case queue <- m.Key:
		default:
			return errors.New("GRANT_QUEUE_LIMIT")
		}
		l.queued[m.Key] = true
		if fastClass {
			l.fastGrantSeen = true
		} else {
			l.bulkGrantSeen = true
		}
	}
	if l.fastGrantSeen && l.bulkGrantSeen {
		l.markGrantReady()
	}
	return nil
}

func (l *lane) deliver(f wire.Frame) error {
	if l.stripe != nil {
		return l.stripe.dispatchStream(l, f)
	}
	return l.deliverOwned(f)
}

func (l *lane) deliverOwned(f wire.Frame) error {
	if !l.side {
		if o := l.opens[f.Stream]; o != nil && !o.protocol.DataAllowed() {
			if len(o.early) >= 16 {
				return errors.New("EARLY_FRAME_LIMIT")
			}
			o.early = append(o.early, f)
			return nil
		}
	}
	return l.input(f)
}

type remoteCleanup struct {
	message mailbox.Message
	key     wire.QuotaKey
	digest  [32]byte
}

func remoteAttemptRef(attempts []journal.Attempt, key wire.QuotaKey, ordinal uint64) journal.AttemptRef {
	for _, a := range attempts {
		if a.Ref.Grant == grantRef(key) && a.Ordinal == ordinal {
			return a.Ref
		}
	}
	return journal.AttemptRef{}
}

// Invalid DATA evidence must not tear down an otherwise healthy IMAP carrier.
// A bounded per-lane quarantine only suppresses retries of the same physical
// proof for a short interval. No DATA is delivered or acknowledged and the
// registered journal debt remains untouched until a later valid proof/GC.
const invalidDataProofLimit = 128
const invalidDataProofRetry = time.Minute

func (l *lane) dataProofQuarantined(digest [32]byte) bool {
	if l.badDataProofs == nil {
		return false
	}
	until, exists := l.badDataProofs[digest]
	if exists && time.Now().Before(until) {
		return true
	}
	// Keep expired entries available for bounded eviction. Their small cached
	// descriptor may still be needed if the provider later exposes valid DATA.
	return false
}

// Under adversarial or broken-provider workloads, never keep unbounded cached
// descriptors. Eviction drops only in-memory retry state, not the durable
// remote grant or its charged physical DATA debt.
func (l *lane) evictOldestBadDataProof() bool {
	var oldest [32]byte
	var at time.Time
	for key, expiry := range l.badDataProofs {
		if at.IsZero() || expiry.Before(at) {
			oldest, at = key, expiry
		}
	}
	if at.IsZero() {
		return false
	}
	delete(l.badDataProofs, oldest)
	delete(l.reports, oldest)
	delete(l.pending, oldest)
	return true
}

func (l *lane) quarantineDataProof(digest [32]byte) {
	l.quarantineDataProofAt(digest, time.Now())
}

// The system clock can return the same timestamp for many consecutive calls,
// especially on Windows. The expiry is also the eviction order: a tie makes
// map iteration randomly retain an older descriptor while newer, unrelated
// corrupt proofs churn through the bounded cache. Ensure a strict order even
// when clock resolution is coarser than the rate of quarantines.
func (l *lane) quarantineDataProofAt(digest [32]byte, now time.Time) {
	if l.badDataProofs == nil {
		l.badDataProofs = make(map[[32]byte]time.Time)
	}
	if _, exists := l.badDataProofs[digest]; !exists && len(l.badDataProofs) >= invalidDataProofLimit {
		l.evictOldestBadDataProof()
	}
	until := now.Add(invalidDataProofRetry)
	if !until.After(l.lastBadDataProofExpiry) {
		until = l.lastBadDataProofExpiry.Add(time.Nanosecond)
	}
	l.lastBadDataProofExpiry = until
	l.badDataProofs[digest] = until
	// Do not discard the durable descriptor cache: peer GC registration is
	// already acknowledged and the peer may never retransmit the descriptor.
	// Keeping it allows a later valid physical representation to be retried
	// without inventing a receipt or requiring a new APPEND.
	delete(l.pending, digest)
}

func (l *lane) remoteData(m mailbox.Message, report wire.UsageReport) error {
	return l.remoteDataPrepared(m, report, nil, false)
}

// remoteDataPrepared shares the already-decoded ITQ1 payload from the reader
// with cryptographic verification. A stored pending row still follows the
// independent decode path; no physical MIME or durable proof is modified.
func (l *lane) remoteDataPrepared(m mailbox.Message, report wire.UsageReport, decoded []byte, prepared bool) error {
	if report.Attempt != nil && l.dataProofQuarantined(report.Attempt.Digest) {
		return nil
	}
	ref, e := l.data.RemoteAttempt(report.Key, report.Total.Messages)
	if e != nil && !errors.Is(e, journal.ErrMissing) {
		return e
	}
	if ref.ID == 0 {
		status, err := l.data.RemoteStatus(grantRef(report.Key))
		if errors.Is(err, journal.ErrMissing) || err == nil && status.Registered.Messages >= report.Total.Messages && status.Registered.Bytes >= report.Total.Bytes {
			// Registered ordinals are durable. An absent row in that prefix was
			// already physically reclaimed, or its fully proven grant retired.
			// This descriptor was authenticated before this function was called;
			// discard the delayed duplicate without delivery or another refund.
			delete(l.reports, report.Attempt.Digest)
			l.traceEnvelopeResult("RECLAIMED_DUPLICATE", int(report.Total.Messages))
			return nil
		}
		return errors.New("REMOTE_DESCRIPTOR_MISSING")
	}
	if _, done := l.remoteGC[ref]; done {
		l.traceEnvelopeResult("GC_QUEUED_DUPLICATE", int(report.Total.Messages))
		return nil
	}
	if report.Key.Session == l.cipher.Context().Session {
		// Normalize only for descriptor/AEAD verification. The immutable physical
		// MIME in m is retained for journal and exact UID deletion proof.
		var charged uint64
		var normalizeErr error
		if prepared {
			charged, normalizeErr = mailbox.DataProofCharge(m.MIME, decoded)
		} else {
			decoded, charged, normalizeErr = mailbox.DecodeDataProof(m.MIME)
			defer clear(decoded)
		}
		var frames []wire.Frame
		if normalizeErr != nil {
			e = normalizeErr
		} else {
			frames, e = datapath.OpenRemoteBatchBody(decoded, charged, report, l.cipher)
		}
		if e != nil {
			if l.diag != nil {
				// Failed DATA proofs reveal only bounded lengths and a closed-set
				// validation stage, never raw mail, grants, hashes or identities.
				// In production a provider MIME rewrite can preserve all encrypted
				// bytes while changing the RFC822 literal size; these counters make
				// that failure distinguishable from a bad authenticated descriptor.
				expectedBytes := uint64(0)
				if report.Attempt != nil {
					expectedBytes = report.Attempt.EncodedBytes
				}
				info, _ := json.Marshal(map[string]any{
					"stage":                  datapath.ValidationStage(e),
					"expected_encoded_bytes": expectedBytes,
					"charged_bytes":          charged,
					"physical_mime_bytes":    len(m.MIME),
					"decoded_bytes":          len(decoded),
				})
				l.diag.trace(traceEvent{Event: "data_verification_failed", Code: errorCode(e), Details: info})
			}
			// Validation has completed before any frame delivery. Isolating this
			// physical proof is safe; application/flow errors after validation
			// MUST still propagate, and no remoteGC/ACK/refund is fabricated.
			l.quarantineDataProof(report.Attempt.Digest)
			return nil
		}
		for i := range frames {
			f := frames[i]
			l.traceFrame("frame_rx", f, nil)
			if l.diag != nil {
				l.diag.mu.Lock()
				l.diag.line.ReceivedRecords++
				l.diag.mu.Unlock()
			}
			if e = l.deliver(f); e != nil {
				for j := i + 1; j < len(frames); j++ {
					clear(frames[j].Payload)
				}
				return e
			}
		}
		l.markRemoteProgress()
		if l.diag != nil {
			members := make([]map[string]uint64, 0, len(frames))
			for _, frame := range frames {
				members = append(members, map[string]uint64{"stream": frame.Stream, "offset": frame.Offset, "bytes": uint64(len(frame.Payload))})
			}
			details, _ := json.Marshal(map[string]any{"records": len(frames), "ordinal": report.Total.Messages, "members": members})
			l.diag.trace(traceEvent{Event: "data_envelope_delivery", Details: details})
		}
	} else {
		l.traceEnvelopeResult("OLD_SESSION_CLEANUP_ONLY", int(report.Total.Messages))
	}
	delete(l.badDataProofs, report.Attempt.Digest)
	l.remoteGC[ref] = remoteCleanup{message: m, key: report.Key, digest: report.Attempt.Digest}
	return nil
}
func (l *lane) read(rows []mailbox.Message) error {
	sortMessages(rows)
	for _, m := range rows {
		body, e := mailbox.Decode(m.MIME)
		if e != nil {
			continue
		}
		if bytes.HasPrefix(body, []byte(transportProbeMagic)) {
			if l.side && len(body) < 16384 {
				var probe preauthProbe
				key, keyErr := hex.DecodeString(l.user.Key)
				if keyErr == nil && json.Unmarshal(body[len(transportProbeMagic):], &probe) == nil && validProbe(key, l.cipher.Context().Server, l.user, l.account.ID, probe) && probe.ClientNonce != l.preauthClient {
					if probe.ClientNonce != l.reattachClient || l.reattachServer == (wire.ID{}) {
						l.reattachClient = probe.ClientNonce
						l.reattachServer, keyErr = randomID()
					}
					if keyErr == nil {
						challenge := preauthChallenge{Version: transportWireVersion, Capabilities: l.capabilities(), ClientNonce: l.reattachClient, ServerNonce: l.reattachServer}
						challenge.MAC = preauthMAC(key, "challenge", l.cipher.Context().Server, l.user, l.account.ID, l.reattachClient, l.reattachServer, l.capabilities())
						raw, marshalErr := json.Marshal(challenge)
						if marshalErr == nil {
							keyErr = appendEphemeralControl(l.ctx, l.mail, l.sendFolder, append([]byte(transportChallengeMagic), raw...))
						}
					}
				}
				clear(key)
				if keyErr != nil {
					return keyErr
				}
			}
			continue
		}
		if bytes.HasPrefix(body, []byte(transportChallengeMagic)) {
			// A live client can observe a new server-first challenge if the server
			// independently rebuilt this carrier. Gate14 ignored that authenticated
			// reattach signal and waited for the 25 s transport watchdog while fetching
			// mail from the obsolete epoch. A fresh PSK-authenticated challenge with a
			// new server nonce is sufficient to reattach immediately; stale/forged mail
			// remains inert.
			if !l.side && m.Validity == l.helloValidity && m.UID > l.helloUID && len(body) < 16384 {
				var challenge preauthChallenge
				if json.Unmarshal(body[len(transportChallengeMagic):], &challenge) == nil && challenge.ServerNonce != l.preauthServer && (challenge.ClientNonce == (wire.ID{}) || challenge.ClientNonce == l.preauthClient) {
					key, keyErr := hex.DecodeString(l.user.Key)
					valid := keyErr == nil && validChallenge(key, l.cipher.Context().Server, l.user, l.account.ID, challenge, challenge.ClientNonce)
					clear(key)
					if valid {
						return errors.New("CARRIER_REATTACH")
					}
				}
			}
			continue
		}
		if bytes.HasPrefix(body, []byte(transportHelloMagic)) {
			if l.side && len(l.preauthReply) != 0 && sha256.Sum256(body) == l.preauthOfferDigest && l.preauthReplyRetries.Load() < 3 && time.Since(l.preauthReplyAt) >= time.Second && time.Now().Before(l.preauthReplyUntil) {
				if err := appendEphemeralControl(l.ctx, l.mail, l.sendFolder, l.preauthReply); err != nil {
					return err
				}
				l.preauthReplyAt = time.Now()
				l.preauthReplyRetries.Add(1)
				continue
			}
			if l.side && m.Validity == l.helloValidity && m.UID > l.helloUID && len(body) < 16384 {
				var offer hello
				if json.Unmarshal(body[len(transportHelloMagic):], &offer) == nil && !offer.Reply && offer.Version == transportWireVersion && validCapabilities(offer.Capabilities) && offer.Capabilities & ^l.capabilities() == 0 && offer.Binding.Session != l.cipher.Context().Session {
					clientNonce, serverNonce := l.preauthClient, l.preauthServer
					if offer.ClientNonce == l.reattachClient && offer.ServerNonce == l.reattachServer && l.reattachClient != (wire.ID{}) && l.reattachServer != (wire.ID{}) {
						clientNonce, serverNonce = l.reattachClient, l.reattachServer
					}
					if offer.ClientNonce != clientNonce || offer.ServerNonce != serverNonce {
						continue
					}
					expected := attachedBinding(l.cipher.Context().Server, l.user, l.account.ID, offer.LogicalSession, offer.Generation, offer.CarrierEpoch, offer.Capabilities, clientNonce, serverNonce)
					expected.Session = offer.Binding.Session
					expected.Handshake = offer.Binding.Handshake
					if offer.Binding == expected && offer.LogicalSession != (wire.ID{}) && offer.Generation != 0 && offer.CarrierEpoch != 0 {
						key, _ := hex.DecodeString(l.user.Key)
						hs, err := secure.NewHandshake(offer.Binding, key, false)
						clear(key)
						if err == nil && hs.Read(offer.Payload) == nil {
							if offer.LogicalSession == l.logicalSession && offer.Generation == l.generation {
								if offer.CarrierEpoch > l.carrierEpoch {
									return errors.New("CARRIER_REATTACH")
								}
							} else {
								return errors.New("SESSION_SUPERSEDED")
							}
						}
					}
				}
			}
			continue
		}
		if bytes.HasPrefix(body, []byte(wire.DataEnvelopeMagic)) {
			if e = l.receiveDataEnvelope(m, body); e != nil {
				clear(body)
				return e
			}
			clear(body)
			continue
		}
		if bytes.HasPrefix(body, []byte(wire.DataBatchMagic)) && !l.side {
			_, records, unpackErr := wire.UnpackDataBatch(body)
			if unpackErr != nil {
				continue
			}
			for _, record := range records {
				if e = l.acceptRecord(m, record, false); e != nil {
					return e
				}
			}
			continue
		}
		if bytes.HasPrefix(body, []byte("ITQ1")) && l.side {
			// ITQ1 used to contain exactly one encrypted DATA record. S1 places an
			// ITD3 container inside the same authenticated physical wrapper, so a
			// legitimate message can be much larger than wire.MaxRecord. Keep a
			// tight decoded cap (ITD3 maximum plus bounded ITQ1 metadata) instead
			// of silently discarding multi-record batches.
			if len(body) > wire.MaxDataBatchBody+256 {
				continue
			}
			hash := sha256.Sum256(body)
			if l.dataProofQuarantined(hash) {
				continue
			}
			if report, ok := l.reports[hash]; ok {
				e = l.remoteDataPrepared(m, report, body, true)
				clear(body)
				if e != nil {
					return e
				}
			} else if e = l.queuePendingRemote(m, body); e != nil {
				return e
			}
			continue
		}
		records, control, err := unpackControl(body)
		if err != nil {
			continue
		}
		for _, record := range records {
			if e = l.acceptRecord(m, record, control); e != nil {
				return e
			}
		}
	}
	return l.sendReceipt()
}
func (l *lane) maintenance() error {
	if e := l.sendReceipt(); e != nil {
		return e
	}
	// Reconciliation must start after the durable wallet bind, even when old
	// reservations leave too little space to issue a fresh grant. The server
	// tolerates these repeatable controls overtaking its coordinator ACK.
	if l.wallet != nil && (l.boot == nil || !l.boot.Ready()) {
		return nil
	}
	if time.Now().Before(l.nextWallet) {
		return nil
	}
	l.nextWallet = time.Now().Add(5 * time.Second)
	if l.wallet != nil {
		entries, e := l.wallet.Entries()
		if e != nil {
			return e
		}
		for _, entry := range entries {
			for _, report := range entry.Pending {
				if e = l.controlSend(report); e != nil {
					return e
				}
			}
			if entry.Fenced && !entry.Retired {
				if e = l.controlSend(wire.StorageRelease{Key: entry.Offer.Key, Final: entry.Used}); e != nil {
					return e
				}
			}
		}
		retire, e := l.wallet.Retirements()
		if e != nil {
			return e
		}
		for _, r := range retire {
			if e = l.sendRetirement(r); e != nil {
				return e
			}
		}
	}
	return nil
}
func maintenanceRetryable(err error) bool {
	if err == nil {
		return false
	}
	switch errorCode(err) {
	case "TIMEOUT", "IO_OR_PROTOCOL_ERROR", "IMAP_APPEND_OUTCOME_UNKNOWN", "CONTROL_QUEUE_LIMIT", "REMOTE_GC_INCOMPLETE":
		return true
	default:
		return false
	}
}

func (l *lane) deferMaintenanceError(err error) bool {
	if err == nil || l.ctx.Err() != nil || !maintenanceRetryable(err) {
		return false
	}
	if l.diag != nil {
		l.diag.event("event=maintenance_deferred code=" + errorCode(err))
	}
	if repair, ok := l.mail.(interface{ RepairCleanup(context.Context) error }); ok {
		_ = repair.RepairCleanup(l.ctx)
	}
	now := time.Now().Add(time.Second)
	l.nextLocalCleanup = now
	l.nextRemoteCleanup = now
	return true
}

func (l *lane) run() error {
	defer l.close()
	if l.stripe != nil && l.providerBaseline > 0 {
		l.stripe.observeProviderBaseline(l.providerBaseline)
	}
	if l.mail != nil {
		l.startReader()
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		var pumpDone <-chan struct{}
		if l.pump != nil {
			pumpDone = l.pump.Done()
		}
		requests, dials := l.requests, l.dials
		if l.stripe != nil {
			requests, dials = nil, nil
		}
		select {
		case <-l.ctx.Done():
			return l.ctx.Err()
		case reply := <-l.progressRequests:
			reply <- l.captureProgress()
		case e := <-l.writeErrors:
			return e
		case <-pumpDone:
			return fmt.Errorf("pump: %w", l.pump.Wait())
		case page := <-l.dataPages:
			// A ready control page gets the next actor turn before DATA.
			select {
			case control := <-l.pages:
				if control.err != nil {
					return fmt.Errorf("receive: %w", control.err)
				}
				if e := l.read(control.rows); e != nil {
					return e
				}
			default:
			}
			if e := l.read(page.rows); e != nil {
				return fmt.Errorf("data_receive: %w", e)
			}
		case page := <-l.pages:
			if page.err != nil {
				return fmt.Errorf("receive: %w", page.err)
			}
			if e := l.read(page.rows); e != nil {
				return fmt.Errorf("receive: %w", e)
			}
		case <-l.cleanupWake:
			// Only a retained FIFO request may use this wake; periodic due work
			// stays on its normal cadence. Admission is rechecked under g.mu.
			if l.cleanupQueued {
				l.startDueCleanup(time.Now())
			}
		case done := <-l.gcDone:
			l.gcBusy = false
			if e := l.finishCleanup(done); e != nil {
				if l.deferMaintenanceError(e) {
					continue
				}
				return fmt.Errorf("maintenance: %w", e)
			}
			// Completed read-only/physical proof can expose another due turn.
			// Resume without an extra 100 ms actor tick; timers/FIFO still govern.
			l.startDueCleanup(time.Now())
		case req := <-requests:
			if e := l.handleOpenRequest(req); e != nil {
				return e
			}
		case d := <-dials:
			if e := l.handleDial(d); e != nil {
				return e
			}
		case <-tick.C:
			if err := l.advanceRekey(time.Now()); err != nil {
				return err
			}
			if l.stripe != nil {
				l.stripe.maintainDelivery(time.Now())
			}
			if !l.gate.Ready() {
				a, e := l.gate.Poll(1, l.now())
				if e != nil {
					return e
				}
				for _, f := range a.Send {
					if e = l.SendControl(l.ctx, f); e != nil {
						return e
					}
				}
			} else if !l.boot.Ready() {
				a, e := l.boot.Poll(1, l.now())
				if e != nil {
					return e
				}
				if e = l.bootAction(a); e != nil {
					return e
				}
			}
			if !l.ownerStarted {
				if e := l.pollOpens(); e != nil {
					return e
				}
			}
			if l.remoteProgressAge(time.Now()) > transportProgressLimit {
				return errors.New("TRANSPORT_TIMEOUT")
			}
			now := time.Now()
			if l.ready.Load() && !now.Before(l.nextEcho) {
				l.echo++
				l.echoIdle = l.echoCalibrationAllowed()
				l.lastEcho = now
				l.nextEcho = advancePeriodic(l.nextEcho, 5*time.Second, now)
				if e := l.controlSend(wire.Echo{Token: l.echo}); e != nil {
					return e
				}
			}
			l.startDueCleanup(now)
			if !now.Before(l.nextMaintenance) {
				if e := l.maintenance(); e != nil {
					if !l.deferMaintenanceError(e) {
						return fmt.Errorf("maintenance: %w", e)
					}
				}
				if l.side && l.pump != nil {
					if e := l.offer(); e != nil {
						return e
					}
				}
				l.health()
				l.nextMaintenance = advancePeriodic(l.nextMaintenance, runtimeMaintenanceInterval, now)
			}
		}
	}
}
func (l *lane) startDueCleanup(now time.Time) {
	if !l.gcBusy {
		if l.cleanupQueued {
			_ = l.startCleanup(l.cleanupQueuedKind)
		} else {
			localDue := !now.Before(l.nextLocalCleanup)
			startedLocal := false
			// Sustained DATA can keep remoteGC continuously non-empty. Give a
			// pressured local journal one bounded cleanup turn before remote GC
			// so control debt cannot reach the hard writer stop at 112 records.
			if localDue && !l.remoteCleanupTurn(now) {
				if pressured, ok := l.pressuredLocalCleanup(); ok {
					if l.startCleanup(pressured) {
						l.nextLocalCleanup = advancePeriodic(l.nextLocalCleanup, runtimeLocalCleanupInterval, now)
						startedLocal = true
					}
				}
			}
			if !startedLocal && len(l.remoteGC) > 0 && !now.Before(l.nextRemoteCleanup) {
				if l.startCleanup(cleanupRemote) {
					l.nextRemoteCleanup = advancePeriodic(l.nextRemoteCleanup, runtimeRemoteCleanupInterval, now)
				}
			} else if !startedLocal && localDue {
				phase := []cleanupKind{cleanupLocalData, cleanupPresence, cleanupLocalData, cleanupLocalControl}[l.gcLocalPhase%4]
				l.gcLocalPhase++
				_ = l.startCleanup(phase)
				l.nextLocalCleanup = advancePeriodic(l.nextLocalCleanup, runtimeLocalCleanupInterval, now)
			}
		}
	}
}

// reserveOpen is called under Client.mu before selection can observe this
// owner again. The reservation covers pending OPEN as well as active sockets.
func (l *lane) reserveOpen() (*lane, uint64, error) {
	if !l.openHealthy(time.Now()) {
		return nil, 0, errLaneUnhealthy
	}
	owner := l
	if l.stripe != nil {
		owner = l.stripe.accountOwner(l.account.ID)
		if owner == nil {
			return nil, 0, errLaneUnhealthy
		}
		id, err := l.stripe.allocate(owner)
		if err == nil {
			l.stripe.mu.Lock()
			l.stripe.streamAccount[id] = l.account.ID
			l.stripe.mu.Unlock()
		}
		return owner, id, err
	}
	return owner, 0, nil
}

func (l *lane) open(ctx context.Context, target wire.Connect) (*pipe, error) {
	owner, id, err := l.reserveOpen()
	if err != nil {
		return nil, err
	}
	return l.openReserved(ctx, target, owner, id)
}

func (l *lane) openReserved(ctx context.Context, target wire.Connect, owner *lane, id uint64) (*pipe, error) {
	allocated := id != 0 && l.stripe != nil
	submitted := false
	reqCtx, cancelReq := context.WithCancel(ctx)
	defer cancelReq()
	req := request{ctx: reqCtx, target: target, id: id, done: make(chan openResult)}
	cleanup := func() {
		if allocated && !submitted && l.stripe != nil {
			l.stripe.retire(id, owner)
		}
	}
	select {
	case owner.requests <- req:
		submitted = true // actor retains route for a late OPEN_OK/OPEN_ERROR
	case <-ctx.Done():
		cleanup()
		return nil, ctx.Err()
	case <-owner.logicalContext().Done():
		cleanup()
		return nil, owner.logicalContext().Err()
	}
	health := time.NewTicker(time.Second)
	defer health.Stop()
	ownerDeadline := time.NewTimer(openOwnerResultLimit)
	defer ownerDeadline.Stop()
	for {
		select {
		case r := <-req.done:
			if r.err != nil {
				cleanup()
			}
			return r.socket, r.err
		case <-ctx.Done():
			cleanup()
			return nil, ctx.Err()
		case <-owner.logicalContext().Done():
			cleanup()
			return nil, owner.logicalContext().Err()
		case <-health.C:
			if !l.openHealthy(time.Now()) {
				cancelReq()
				cleanup()
				l.quarantineOpen()
				return nil, errLaneUnhealthy
			}
		case <-ownerDeadline.C:
			// OPEN ownership is lane-bound even though DATA can stripe over other
			// carriers. A lane may therefore continue receiving unrelated authenticated
			// traffic while this particular OPEN is black-holed on its peer owner. Do
			// not wait for the 90 s protocol deadline: abandon this request cleanly,
			// quarantine the owner for new connections, and let Client.Open retry on
			// another healthy carrier. A late OPEN_OK is consumed by the actor and its
			// socket is closed because reqCtx is canceled.
			cancelReq()
			cleanup()
			l.quarantineOpen()
			return nil, errLaneUnhealthy
		}
	}
}

// Dial is injectable for protected Android sockets and trusted local TLS tests.
type Dial func(context.Context, mailbox.Config, []string) (*mailbox.Client, error)

func NetworkDial(d mailbox.NetworkDialer) Dial {
	return func(ctx context.Context, c mailbox.Config, f []string) (*mailbox.Client, error) {
		return mailbox.DialWithNetwork(ctx, c, f, d)
	}
}

func (l *lane) answer(r request, result openResult) {
	logical := l.logicalContext()
	// A logical owner outlives its original physical carrier. Checking l.ctx
	// here randomly discarded successful OPEN_OK after rejoin, closing the new
	// TCP socket while the application waited for a reply that never arrived.
	if r.ctx.Err() == nil && logical.Err() == nil {
		select {
		case r.done <- result:
			return
		case <-r.ctx.Done():
		case <-logical.Done():
		}
	}
	if result.socket != nil {
		result.socket.Close()
	}
}

// Physical absence frees storage. Refill the active grant window immediately;
// metadata retirement stays mandatory, but is not an extra data-path RTT.
func grantFullyReleased(status wire.CleanupStatus) bool {
	return status.Closed && status.Used == status.Registered && status.Registered == status.Released
}

func (l *lane) publishCleanup(status wire.CleanupStatus) error {
	if err := l.controlSend(status); err != nil {
		return err
	}
	if grantFullyReleased(status) {
		delete(l.issued, status.Key)
		delete(l.issuedFast, status.Key)
	}
	// Once a peer has fenced a grant, any later physical release can create
	// headroom for its replacement. Retry the normal baseline window immediately
	// instead of waiting for a one-second maintenance tick or full retirement.
	// IssueRemote still accounts all unreclaimed physical debt, so this cannot
	// exceed the existing 8 MiB exposure bound.
	if status.Closed {
		return l.offer()
	}
	return nil
}

// Repeat only on the retry timer, not on duplicate/intermediate GC_STATUS.
func (l *lane) sendRetirement(r wire.Retirement) error {
	if sent, ok := l.retireSent[r.Key]; ok && time.Since(sent) < 5*time.Second {
		return nil
	}
	if err := l.controlSend(r); err != nil {
		return err
	}
	l.retireSent[r.Key] = time.Now()
	return nil
}

func (l *lane) handleCleanupStatus(m wire.CleanupStatus) error {
	if l.side {
		return errors.New("GRANT_DIRECTION")
	}
	if e := l.wallet.Acknowledge(m); e != nil {
		// A cleanup status can arrive after its durable retirement ACK already
		// deleted the grant. That stale authenticated status is idempotent. Any
		// other WALLET_STATE is a real accounting divergence and remains fatal.
		if errors.Is(e, wallet.ErrMissing) {
			return nil
		}
		if l.diag != nil {
			l.diag.event(fmt.Sprintf("event=wallet_transition_error operation=acknowledgement code=%s grant=%d", errorCode(e), m.Key.Grant))
		}
		return e
	}
	if grantFullyReleased(m) {
		l.mu.Lock()
		entries := append([]remoteLogicalAttempt(nil), l.remote[m.Key]...)
		delete(l.remote, m.Key)
		delete(l.grantLimits, m.Key)
		delete(l.grantFast, m.Key)
		delete(l.queued, m.Key)
		l.mu.Unlock()
		if l.stripe != nil {
			for _, entry := range l.stripe.takeRemote(l.account.ID, m.Key) {
				entries = appendUniqueLogical(entries, entry.owner, []uint64{entry.id})
			}
		}
		for _, entry := range entries {
			if entry.id == 0 {
				continue
			}
			if e := l.cleanedFor(entry.owner, entry.id); e != nil {
				return e
			}
		}
	}
	retire, e := l.wallet.Retirement(m.Key)
	if e != nil {
		return e
	}
	if retire != nil {
		return l.sendRetirement(*retire)
	}

	return nil
}

// A replacement only carries stream traffic after a fresh authenticated echo.
// Put the probe on the normal FIFO behind a possible CoordinatorAck: allowing
// the urgent queue to overtake bootstrap would create a new readiness race.
func (l *lane) queueRejoinProbe() error {
	l.echo++
	l.echoIdle = l.echoCalibrationAllowed()
	l.lastEcho = time.Now()
	l.nextEcho = l.lastEcho.Add(5 * time.Second)
	f, err := wire.EncodeControl(0, wire.Echo{Token: l.echo})
	if err != nil {
		return err
	}
	select {
	case l.controlQueue <- queuedControl{Frame: f, added: time.Now()}:
		return nil
	case <-l.ctx.Done():
		return l.ctx.Err()
	default:
		return errors.New("CONTROL_QUEUE_LIMIT")
	}
}
func (l *lane) streamCarrierReady() bool { return l.ready.Load() && !l.rejoinPending.Load() }

func (l *lane) echoCalibrationAllowed() bool {
	if l.stripe == nil {
		return true
	}
	l.stripe.mu.Lock()
	defer l.stripe.mu.Unlock()
	return l.stripe.active.Load() == 0 && l.stripe.delivery.bulk == 0 && l.stripe.delivery.fast == 0
}

func (l *lane) completeEchoReply(token uint64) {
	if token != l.echo {
		return
	}
	now := time.Now()
	l.rtt.Store(max(int64(1), now.Sub(l.lastEcho).Milliseconds()))
	sample := l.rtt.Load()
	if old := l.smoothedRTT.Load(); old > 0 {
		sample = (7*old + sample) / 8
	}
	l.smoothedRTT.Store(sample)
	l.rttAt.Store(now.UnixMilli())
	if l.stripe != nil {
		if l.echoIdle && l.echoCalibrationAllowed() {
			base := l.rtt.Load()
			if old := l.idleRTT.Load(); old > 0 {
				base = (7*old + base) / 8
			}
			l.idleRTT.Store(base)
			l.idleRTTAt.Store(now.UnixMilli())
		}
	}
	if l.stripe != nil {
		l.stripe.signal()
	}
	// The server sees a different provider path; do not use a local VPS NOOP as
	// the mobile baseline or calibrate from a queue-inflated bulk sample.
	if l.side && l.stripe != nil && l.stripe.active.Load() == 0 {
		l.stripe.observeProviderBaseline(now.Sub(l.lastEcho))
	}
	if l.rejoinPending.Swap(false) && l.stripe != nil {
		l.stripe.completeRekey(l)
		l.stripe.signal()
		l.stripe.wakeReaders()
	}
}

package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
)

func physicalMail(m mailbox.Transport) *laneMail {
	switch v := m.(type) {
	case *laneMail:
		return v
	case *observedMail:
		return physicalMail(v.Transport)
	}
	return nil
}
func validCapabilities(c uint64) bool {
	if c&transportCapabilities != transportCapabilities || c & ^(transportCapabilities|capSplitData|capTwoDataShards|capFourDataShards) != 0 {
		return false
	}
	if c&capSplitData == 0 && c&(capTwoDataShards|capFourDataShards) != 0 {
		return false
	}
	return c&capFourDataShards == 0 || c&capTwoDataShards != 0
}
func intersectCapabilities(a, b uint64) uint64 { return a & b }
func (l *lane) capabilities() uint64 {
	if l.availableCaps == 0 {
		return transportCapabilities
	}
	return l.availableCaps
}
func (m *laneMail) dataWriterTarget() int {
	if m.writerTarget == 0 {
		return accountDataWorkers
	}
	return m.writerTarget
}
func shardCount(c uint64) int {
	if c&capSplitData == 0 {
		return 0
	}
	if c&capFourDataShards != 0 {
		return 4
	}
	if c&capTwoDataShards != 0 {
		return 2
	}
	return 1
}
func topologyFolders(u servercfg.User, shards int) []string {
	out := []string{u.Up, u.Down}
	for _, base := range []string{u.Up, u.Down} {
		for i := 0; i < shards; i++ {
			out = append(out, fmt.Sprintf("%s-data-%02d", base, i))
		}
	}
	return out
}

// Reserve a real independent receive socket before advertising split service.
// Folder count does not imply additional sockets, workers, credit or quota.
func (m *laneMail) stageTopology(ctx context.Context) (uint64, error) {
	t := m.topology.Effective()
	caps := uint64(transportCapabilities)
	if t.Layout == "mixed" {
		return caps, nil
	}
	// Auto layout must not wait a full provider LOGIN timeout before even
	// starting the handshake. Advertise split only after its real reader exists;
	// an explicit split configuration still fails rather than weakening it.
	setupCtx, stopSetup := context.WithTimeout(ctx, 2*time.Second)
	defer stopSetup()
	reader, err := m.dial(setupCtx)
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		if t.Layout == "split" || (!connectionLimitFallback(err) && setupCtx.Err() == nil) {
			return 0, fmt.Errorf("data_receive_connect: %w", err)
		}
		return caps, nil
	}
	m.topologyMu.Lock()
	m.dataReader = reader
	m.topologyMu.Unlock()
	caps |= capSplitData
	// One selected DATA reader serves one active mailbox. Advertising extra
	// shards without extra receive sockets turns every scan into serial SELECT
	// round trips and inflates the minimum delivery age on mobile links. Account
	// striping supplies independent paths; historical shard folders remain owned
	// and are inventoried/reclaimed without advertising fictional parallelism.
	return caps, nil
}
func (l *lane) activateTopology() error {
	l.dataShards = shardCount(l.negotiatedCaps)
	if io := physicalMail(l.mail); io != nil {
		if io.topology.Effective().Layout == "split" && l.dataShards == 0 {
			return errors.New("PEER_SPLIT_SERVICE_UNAVAILABLE")
		}
		io.topologyCaps = l.negotiatedCaps
		io.topologyMu.Lock()
		if l.dataShards == 0 && io.dataReader != nil {
			io.dataReader.Close()
			io.dataReader = nil
		}
		io.topologyMu.Unlock()
	}
	cfg := l.data.Config()
	owned := map[string]bool{}
	for _, f := range cfg.Accounts[0].Users[0].Folders {
		owned[f] = true
	}
	for _, f := range topologyFolders(l.user, l.dataShards) {
		if !owned[f] {
			cfg.Accounts[0].Users[0].Folders = append(cfg.Accounts[0].Users[0].Folders, f)
			owned[f] = true
		}
	}
	if err := l.data.ExtendDataFolders(cfg); err != nil {
		return err
	}
	for _, f := range cfg.Accounts[0].Users[0].Folders {
		if err := l.mail.Ensure(l.ctx, f); err != nil {
			return err
		}
	}
	return l.data.EnableAfterInventory(l.account.ID, l.user.ID, l.data.Run())
}
func (l *lane) nextDataFolder() string {
	if l.dataShards == 0 {
		return l.sendFolder
	}
	return fmt.Sprintf("%s-data-%02d", l.sendFolder, int(l.dataTurn.Add(1)-1)%l.dataShards)
}
func (l *lane) ownedFolders(s *journal.Store, send bool) []string {
	base := l.recvFolder
	if send {
		base = l.sendFolder
	}
	out := []string{base}
	if s != nil {
		for _, f := range s.Config().Accounts[0].Users[0].Folders {
			if strings.HasPrefix(f, base+"-data-") {
				out = append(out, f)
			}
		}
	}
	return out
}
func (l *lane) ownsReceiveFolder(folder string) bool {
	for _, f := range l.ownedFolders(l.data, false) {
		if f == folder {
			return true
		}
	}
	return false
}
func (l *lane) inventoryOwned(s *journal.Store, send bool) ([]mailbox.Message, error) {
	var out []mailbox.Message
	for _, folder := range l.ownedFolders(s, send) {
		rows, err := inventory(l.ctx, l.mail, folder)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

// DATA commands never acquire the control reader's mutex. A receive failure
// restarts this service only; it neither drops the carrier nor refunds debt.
func (m *laneMail) dataReaderRef() *mailbox.Client {
	m.topologyMu.Lock()
	defer m.topologyMu.Unlock()
	return m.dataReader
}
func (m *laneMail) pageData(ctx context.Context, folder string, cursor mailbox.Cursor, emit func(mailbox.Message) error) (mailbox.Cursor, error) {
	m.dataCommandMu.Lock()
	defer m.dataCommandMu.Unlock()
	reader := m.dataReaderRef()
	if reader == nil || !reader.Ready() {
		if reader != nil {
			m.retireDataReader(reader, folder)
		}
		// Restoring the sole DATA receiver is required for existing application
		// streams, including short DATA. It must share repair priority with the
		// control receiver rather than queue behind optional writer/cleaner
		// LOGINs after a correlated socket failure.
		fresh, err := m.dial(repairAdmission(ctx))
		if err != nil {
			return cursor, err
		}
		if err = ctx.Err(); err != nil {
			fresh.Close()
			return cursor, err
		}
		for _, snapshot := range m.dataDiscovery {
			if err = fresh.RestoreTailDiscovery(snapshot); err != nil {
				fresh.Close()
				return cursor, err
			}
		}
		m.topologyMu.Lock()
		m.dataReader = fresh
		m.topologyMu.Unlock()
		reader = fresh
		m.dataChecks = map[string]time.Time{}
		m.dataReadLimit = 0
	}
	if m.dataChecks == nil {
		m.dataChecks = map[string]time.Time{}
	}
	// The same bounded incarnation audit as the control reader, with separate
	// commands/state. Reconnect always forces a fresh SELECT before any delivery.
	checked := m.dataChecks[folder]
	verify := cursor.Validity == 0 || checked.IsZero() || time.Since(checked) >= 2*time.Second
	var next mailbox.Cursor
	limit := m.dataReadLimit
	if limit == 0 {
		limit = 1
	}
	var bodyBytes int
	var callbackWait time.Duration
	started := time.Now()
	err := m.command(ctx, workDataReceive, func() error {
		var paused atomic.Bool
		return guardReceivePaused(ctx, reader, m.receiveProgressBudget(), &paused, func(cmd context.Context) error {
			_, n, e := reader.PageTailStream(cmd, folder, cursor, verify, limit, func(row mailbox.Message) error {
				bodyBytes += len(row.MIME)
				paused.Store(true)
				defer paused.Store(false)
				callbackStart := time.Now()
				defer func() { callbackWait += time.Since(callbackStart) }()
				if err := cmd.Err(); err != nil {
					return err
				}
				if emit == nil {
					return nil
				}
				return emit(row)
			})
			next = n
			return e
		})
	})
	// Empty polls and tiny messages cannot prove that a large BODY is fast.
	// Begin with one, so paced but progressing input cannot turn four legal
	// bodies into a command exceeding its independent thirty-second cap.
	if err == nil && bodyBytes >= 32<<10 {
		m.dataReadLimit = 1
		// Judge rate as well as fixed propagation: a healthy multi-MiB page
		// can take over 250 ms on a shared link. At >=4 Mbit/s, even four
		// maximum transport bodies stay well within the absolute command cap.
		baseline := time.Duration(m.providerRTT.Load())
		if verify && cursor.Validity == 0 {
			baseline *= 2
		}
		budget := max(250*time.Millisecond, baseline+time.Duration(int64(bodyBytes)*8*int64(time.Second)/4_000_000))
		if time.Since(started)-callbackWait <= budget {
			m.dataReadLimit = 4
		}
	}
	if err != nil {
		m.retireDataReader(reader, folder)
	} else if verify {
		m.dataChecks[folder] = time.Now()
	}
	return next, err
}

// Retain one failed generation so the first DATA receive EOF/no-progress
// cause remains visible after recovery, just like control and mixed receive.
// Caller owns dataCommandMu; no cursor or physical quota proof is created.
func (m *laneMail) retireDataReader(reader *mailbox.Client, folder string) {
	m.retainDataDiscovery(reader, folder)
	_ = reader.Close()
	snapshot := reader.SocketSnapshot("data_receive_previous")
	m.previousDataReader.Store(&snapshot)
	m.topologyMu.Lock()
	if m.dataReader == reader {
		m.dataReader = nil
	}
	m.topologyMu.Unlock()
}

// Discovery is bounded by the existing per-folder UID-gap limit and owned
// shard count. A reconnect preserves original expiry; it creates no proof.
// Caller holds dataCommandMu.
func (m *laneMail) retainDataDiscovery(reader *mailbox.Client, folder string) {
	if m.dataDiscovery == nil {
		m.dataDiscovery = map[string]mailbox.TailDiscovery{}
	}
	for f := range m.dataChecks {
		m.dataDiscovery[f] = reader.TailDiscovery(f)
	}
	m.dataDiscovery[folder] = reader.TailDiscovery(folder)
}

// IDLE on the selected DATA folder avoids a per-account empty-FETCH storm.
// The bounded audit still revisits visibility gaps and other configured shards.
func (m *laneMail) waitData(ctx context.Context, folder string, budget time.Duration) (used, notified bool, err error) {
	m.dataCommandMu.Lock()
	defer m.dataCommandMu.Unlock()
	reader := m.dataReaderRef()
	if reader == nil || !reader.Ready() || !reader.IdleCapable() {
		return false, false, nil
	}
	reader.SetReceiveProgressBudget(m.receiveProgressBudget())
	err = m.command(ctx, workDataReceive, func() error {
		return guardReceive(ctx, reader, budget+m.receiveProgressBudget(), func(context.Context) error {
			var e error
			notified, _, e = reader.IdleWaitWakeAhead(ctx, folder, budget, nil)
			return e
		})
	})
	if err != nil {
		m.retireDataReader(reader, folder)
	}
	return true, notified, err
}

func (l *lane) readDataObserved(io *laneMail, folder string, cursor mailbox.Cursor, emit func(mailbox.Message) error) (mailbox.Cursor, error) {
	if l.diag == nil {
		return io.pageData(l.ctx, folder, cursor, emit)
	}
	d := l.diag
	var id uint64
	if d.tracer != nil {
		id = d.tracer.operations.Add(1)
	}
	start := time.Now()
	rows := 0
	bodyBytes := 0
	var callbackWait time.Duration
	var firstBody, lastBody time.Duration
	next, err := io.pageData(l.ctx, folder, cursor, func(row mailbox.Message) error {
		rows++
		bodyBytes += len(row.MIME)
		lastBody = time.Since(start)
		if rows == 1 {
			firstBody = lastBody
		}
		d.packet("packet_rx", id, row)
		callbackStart := time.Now()
		defer func() { callbackWait += time.Since(callbackStart) }()
		return emit(row)
	})
	elapsed := time.Since(start)
	d.mu.Lock()
	d.line.DataFetchPolls++
	d.line.DataFetchMessages += uint64(rows)
	d.line.DataFetchCallbackTotalMS += callbackWait.Milliseconds()
	d.line.DataFetchCallbackMaxMS = max(d.line.DataFetchCallbackMaxMS, callbackWait.Milliseconds())
	d.line.DataFetchPayloadBytes += int64(bodyBytes)
	if rows == 0 {
		d.line.DataEmptyFetchPolls++
	}
	if err == nil && rows > 0 {
		ms := elapsed.Milliseconds()
		if d.line.DataReceiveLatencyMillis == 0 {
			d.line.DataReceiveLatencyMillis = ms
		} else {
			d.line.DataReceiveLatencyMillis = (7*d.line.DataReceiveLatencyMillis + ms) / 8
		}
	}
	d.mu.Unlock()
	if rows > 0 || err != nil || elapsed >= 100*time.Millisecond {
		d.trace(traceEvent{Time: start.UTC().Format(time.RFC3339Nano), Event: "imap_begin", Name: "fetch_data_page", Operation: id})
		details, _ := json.Marshal(map[string]any{"rows": rows, "physical_mime_bytes": bodyBytes, "callback_wait_us": callbackWait.Microseconds(), "first_body_us": firstBody.Microseconds(), "last_body_us": lastBody.Microseconds()})
		d.trace(traceEvent{Event: "imap_end", Name: "fetch_data_page", Operation: id, Code: errorCode(err), DurationUS: elapsed.Microseconds(), Details: details})
	}
	return next, err
}

func (l *lane) startDataReader() {
	// Recovered mail can be immediately visible on rejoin. It must not enter
	// process() before the carrier activation/coordinator gates authorize DATA.
	// The control reader alone drives those gates. Repeated bootstrap messages
	// cannot start a second consumer of this physical role.
	if l.dataReaderStarted || l.gate == nil || !l.gate.Ready() || l.boot == nil || !l.boot.Ready() {
		return
	}
	if l.dataShards == 0 {
		return
	}
	folders := make([]string, l.dataShards)
	for i := range folders {
		folders[i] = fmt.Sprintf("%s-data-%02d", l.recvFolder, i)
	}
	io := physicalMail(l.mail)
	if io == nil {
		return
	}
	l.dataReaderStarted = true
	l.workers.Add(1)
	go func() {
		defer l.workers.Done()
		cursors := map[string]mailbox.Cursor{}
		failureDelay := 100 * time.Millisecond
		for l.ctx.Err() == nil {
			advanced := false
			failed := false
			for _, folder := range folders {
				before := cursors[folder]
				next, err := l.readDataObserved(io, folder, before, func(row mailbox.Message) error {
					select {
					case l.dataPages <- pageResult{rows: []mailbox.Message{row}}:
						return nil
					case <-l.ctx.Done():
						return l.ctx.Err()
					}
				})
				if err != nil {
					failed = true
					if l.diag != nil {
						l.diag.event("event=data_reader_repair code=" + errorCode(err))
					}
					break
				}
				cursors[folder] = next
				advanced = advanced || before != next
			}
			if failed {
				if pause(l.ctx, failureDelay) != nil {
					return
				}
				failureDelay = min(time.Second, 2*failureDelay)
				continue
			}
			failureDelay = 100 * time.Millisecond
			if !advanced {
				budget := 250 * time.Millisecond
				if len(folders) > 1 {
					budget = 75 * time.Millisecond
				}
				used, notified, err := io.waitData(l.ctx, folders[len(folders)-1], budget)
				if used && l.diag != nil {
					l.diag.mu.Lock()
					l.diag.line.DataIdleWaits++
					if notified {
						l.diag.line.DataIdleNotifies++
					}
					l.diag.mu.Unlock()
				}
				if err != nil || !used {
					if pause(l.ctx, 20*time.Millisecond) != nil {
						return
					}
				}
			}
		}
	}()
}

func classSlotChannel(l *lane, fast bool) chan struct{} {
	if fast && l.dataShards > 0 && l.fastPhysicalSlots != nil {
		return l.fastPhysicalSlots
	}
	return l.physicalSlots
}
func classSlotsFull(l *lane, fast bool) bool {
	if fast && l.dataShards > 0 && l.fastPhysicalSlots != nil {
		return len(l.fastPhysicalSlots) >= cap(l.fastPhysicalSlots)
	}
	return len(l.physicalSlots) >= physicalSlotLimit(l)
}
func tryAcquireClassSlot(l *lane, fast bool) bool {
	if classSlotsFull(l, fast) {
		return false
	}
	select {
	case classSlotChannel(l, fast) <- struct{}{}:
		return true
	default:
		return false
	}
}

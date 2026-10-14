package tunnel

import (
	"context"
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
	reader, err := m.dial(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		if t.Layout == "split" || !connectionLimitFallback(err) {
			return 0, fmt.Errorf("data_receive_connect: %w", err)
		}
		return caps, nil
	}
	m.topologyMu.Lock()
	m.dataReader = reader
	m.topologyMu.Unlock()
	caps |= capSplitData
	if t.DataShards >= 2 {
		caps |= capTwoDataShards
	}
	if t.DataShards >= 4 {
		caps |= capFourDataShards
	}
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
			m.retainDataDiscovery(reader, folder)
			reader.Close()
		}
		fresh, err := m.dial(ctx)
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
	}
	if m.dataChecks == nil {
		m.dataChecks = map[string]time.Time{}
	}
	// The same bounded incarnation audit as the control reader, with separate
	// commands/state. Reconnect always forces a fresh SELECT before any delivery.
	checked := m.dataChecks[folder]
	verify := cursor.Validity == 0 || checked.IsZero() || time.Since(checked) >= 2*time.Second
	var next mailbox.Cursor
	err := m.command(ctx, workDataReceive, func() error {
		var paused atomic.Bool
		return guardReceivePaused(ctx, reader, m.receiveProgressBudget(), &paused, func(cmd context.Context) error {
			_, n, e := reader.PageTailStream(cmd, folder, cursor, verify, 4, func(row mailbox.Message) error {
				paused.Store(true)
				defer paused.Store(false)
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
	if err != nil {
		m.retainDataDiscovery(reader, folder)
		reader.Close()
		m.topologyMu.Lock()
		if m.dataReader == reader {
			m.dataReader = nil
		}
		m.topologyMu.Unlock()
	} else if verify {
		m.dataChecks[folder] = time.Now()
	}
	return next, err
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
		m.retainDataDiscovery(reader, folder)
		reader.Close()
		m.topologyMu.Lock()
		if m.dataReader == reader {
			m.dataReader = nil
		}
		m.topologyMu.Unlock()
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
	next, err := io.pageData(l.ctx, folder, cursor, func(row mailbox.Message) error {
		rows++
		d.packet("packet_rx", id, row)
		return emit(row)
	})
	elapsed := time.Since(start)
	d.mu.Lock()
	d.line.DataFetchPolls++
	d.line.DataFetchMessages += uint64(rows)
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
		d.trace(traceEvent{Event: "imap_end", Name: "fetch_data_page", Operation: id, Code: errorCode(err), DurationUS: elapsed.Microseconds()})
	}
	return next, err
}

func (l *lane) startDataReader() {
	folders := l.ownedFolders(l.data, false)
	if len(folders) == 1 {
		return
	}
	folders = folders[1:]
	io := physicalMail(l.mail)
	if io == nil {
		return
	}
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

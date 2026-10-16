package tunnel

import "itom.local/core/mailbox"

// Snapshot actual physical workers, including the account DATA workers shared
// by server users. Role+socket ID avoids attributing an idle EOF to a lane index.
func (m *laneMail) SocketSnapshots() []mailbox.SocketSnapshot {
	out := []mailbox.SocketSnapshot{m.controlClient().SocketSnapshot("control")}
	if old := m.previousControl.Load(); old != nil {
		out = append(out, *old)
	}
	if reader := m.dataReaderRef(); reader != nil {
		out = append(out, reader.SocketSnapshot("data_receive"))
	}
	if reader := m.readerRef(); reader != nil && reader != m.controlClient() {
		out = append(out, reader.SocketSnapshot("receive"))
	}
	if previous := m.previousReader.Load(); previous != nil {
		out = append(out, *previous)
	}
	m.cleanerMu.Lock()
	cleaner := m.cleaner
	m.cleanerMu.Unlock()
	if cleaner != nil && cleaner != m.controlClient() {
		out = append(out, cleaner.SocketSnapshot("maintenance"))
	}
	m.writerMu.Lock()
	writers := append([]*mailbox.Client(nil), m.writers...)
	m.writerMu.Unlock()
	for id, c := range writers {
		if c != nil {
			state := c.SocketSnapshot("data")
			state.WorkerID = id
			out = append(out, state)
		}
	}
	if m.runtime != nil {
		m.runtime.mu.Lock()
		type worker struct {
			id     int
			client appendClient
		}
		clients := make([]worker, 0, len(m.runtime.workerSockets))
		for id, c := range m.runtime.workerSockets {
			clients = append(clients, worker{id, c})
		}
		m.runtime.mu.Unlock()
		for _, c := range clients {
			if client, ok := c.client.(*mailbox.Client); ok {
				state := client.SocketSnapshot("shared_data")
				state.WorkerID = c.id
				out = append(out, state)
			}
		}
	}
	return out
}

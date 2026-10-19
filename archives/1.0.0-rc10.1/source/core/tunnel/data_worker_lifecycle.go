package tunnel

import "itom.local/core/mailbox"

// Idle DATA sockets have the same lifetime owner as busy sockets. A decoder
// closure retires just that generation, wakes capacity waiters and schedules
// a bounded account-gated replacement before the next application burst.
func (m *laneMail) watchDataWriter(index int, writer *mailbox.Client) {
	m.writerMu.Lock()
	if m.closed || m.dataCtx == nil || m.dataCtx.Err() != nil ||
		index >= len(m.writers) || m.writers[index] != writer {
		m.writerMu.Unlock()
		return
	}
	ctx := m.dataCtx
	m.repairWG.Add(1)
	m.writerMu.Unlock()
	go func() {
		defer m.repairWG.Done()
		select {
		case <-ctx.Done():
		case <-writer.Closed():
			m.retireDataWriter(index, writer)
		}
	}()
}

func (r *accountRuntime) availableWorkers() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Unopened lazy workers are admissible; a known failed generation is not.
	return max(0, int(r.live.Load())-len(r.workerRepair))
}

func (r *accountRuntime) publishWorker(id int, writer appendClient) {
	r.mu.Lock()
	if r.workerSockets[id] == writer {
		r.mu.Unlock()
		return
	}
	r.workerSockets[id] = writer
	delete(r.workerRepair, id)
	lifecycle, observed := writer.(interface{ Closed() <-chan struct{} })
	if observed {
		r.workers.Add(1)
	}
	r.mu.Unlock()
	r.notifyCapacity()
	if !observed {
		return
	}
	go func() {
		defer r.workers.Done()
		select {
		case <-r.ctx.Done():
			return
		case <-lifecycle.Closed():
		}
		r.mu.Lock()
		current := r.ctx.Err() == nil && r.workerSockets[id] == writer
		if current {
			r.workerRepair[id] = true
			r.cond.Broadcast()
		}
		r.mu.Unlock()
		if current {
			r.notifyCapacity()
		}
	}()
}

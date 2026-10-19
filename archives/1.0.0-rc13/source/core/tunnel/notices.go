package tunnel

import (
	"context"
	"errors"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
	"path/filepath"
	"sync"
	"time"
)

type noticeReceiver struct {
	mu      sync.Mutex
	current wire.Notice
}

func (r *noticeReceiver) accept(n wire.Notice) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n.Revision == r.current.Revision && n.Text != r.current.Text {
		return errors.New("SERVER_MESSAGE_CONFLICT")
	}
	if n.Revision > r.current.Revision {
		r.current = n
	}
	return nil
}
func (r *noticeReceiver) snapshot() wire.Notice { r.mu.Lock(); defer r.mu.Unlock(); return r.current }

type noticeDelivery struct {
	mu          sync.Mutex
	desired     wire.Notice
	sent, acked uint64
	next        time.Time
}

func (d *noticeDelivery) prepare(n wire.Notice, now time.Time) (wire.Notice, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if n.Revision > d.desired.Revision {
		d.desired = n
		d.next = time.Time{}
	}
	if d.desired.Revision == 0 || d.desired.Revision <= d.acked || now.Before(d.next) {
		return wire.Notice{}, false
	}
	d.sent = d.desired.Revision
	d.next = now.Add(5 * time.Second)
	return d.desired, true
}
func (d *noticeDelivery) acknowledge(revision uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if revision == d.sent && revision > d.acked {
		d.acked = revision
	}
}
func (d *noticeDelivery) retrySoon() { d.mu.Lock(); d.next = time.Time{}; d.mu.Unlock() }
func (l *lane) receiveNotice(n wire.Notice) error {
	if l.side || l.notices == nil {
		return errors.New("SERVER_MESSAGE_DIRECTION")
	}
	if err := l.notices.accept(n); err != nil {
		return err
	}
	return l.controlSend(wire.NoticeAck{Revision: n.Revision})
}
func runServerNotices(ctx context.Context, hubs map[string]*stripeHub, read func() (servercfg.Config, error)) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		messages, err := read()
		if err != nil || messages.Notice.Revision == 0 {
			continue
		}
		for uid, h := range hubs {
			n := messages.NoticeForClient(uid)
			h.mu.Lock()
			groups := make([]*stripeGroup, 0, len(h.groups))
			for _, g := range h.groups {
				groups = append(groups, g)
			}
			h.mu.Unlock()
			for _, g := range groups {
				g.mu.Lock()
				var chosen *lane
				for _, l := range g.lanes {
					if l.streamCarrierReady() && (chosen == nil || l.rtt.Load() < chosen.rtt.Load() || l.rtt.Load() == chosen.rtt.Load() && l.account.ID < chosen.account.ID) {
						chosen = l
					}
				}
				g.mu.Unlock()
				if chosen == nil {
					continue
				}
				if update, ok := g.noticeDelivery.prepare(n, time.Now()); ok {
					if err := chosen.controlSend(update); err != nil {
						g.noticeDelivery.retrySoon()
					}
				}
			}
		}
	}
}
func noticeSource(dir string) func() (servercfg.Config, error) {
	return func() (servercfg.Config, error) {
		return servercfg.Load(filepath.Join(filepath.Dir(dir), "server.db"))
	}
}

package mailbox

import (
	"context"
	"time"
)

// Idle auxiliary sockets otherwise close together and cause a LOGIN burst on
// the next traffic/recovery turn. NOOP carries no message, credit or peer proof.
// Never wait behind a command: only start its timeout after owning the mutex,
// so a keepalive cannot cancel an unrelated in-progress BODY or IDLE command.
func (c *Client) keepIdleConnectionAlive() {
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-c.Closed():
			return
		case now := <-tick.C:
			c.keepAliveIfIdle(now)
		}
	}
}
func (c *Client) keepAliveIfIdle(now time.Time) {
	if !c.Ready() || !c.mu.TryLock() {
		return
	}
	defer c.mu.Unlock()
	if !c.Ready() || c.pendingIdle != nil || now.Sub(time.Unix(0, c.conn.lastIO.Load())) < 20*time.Second {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c.busy.Store(true)
	defer c.busy.Store(false)
	if _, err := c.pingLocked(ctx); err != nil {
		c.conn.noteClose("keepalive", FailureReason(err))
		c.closed.Store(true)
		_ = c.conn.Close()
	}
}

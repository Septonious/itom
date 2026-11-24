package mailbox

import "time"

// A healthy TCP session may pause through consecutive retransmission timers
// (roughly 1+2+4 seconds) under a transient queue/loss burst. Unloaded IMAP RTT
// is not a safe lower bound for that recovery. Keep a finite silence guard,
// separate from the independent thirty-second absolute command deadline.
const MinReceiveProgressBudget = 8 * time.Second
const MaxReceiveProgressBudget = 10 * time.Second

// InterruptReceive closes only this generation without waiting for its command
// mutex. The receive owner joins the command before replacement.
func (c *Client) InterruptReceive() {
	if c == nil || c.conn == nil {
		return
	}
	c.conn.noteClose("receive", "no_progress")
	c.closed.Store(true)
	_ = c.conn.Close()
}

// A receive owner may extend the transport recovery floor using provider RTT.
// IDLE's ordinary wake/audit deadline is independent and remains responsive.
func (c *Client) SetReceiveProgressBudget(budget time.Duration) {
	c.receiveProgressBudget.Store(int64(min(MaxReceiveProgressBudget, max(MinReceiveProgressBudget, budget))))
}
func (c *Client) ReceiveProgressBudget() time.Duration {
	return max(MinReceiveProgressBudget, time.Duration(c.receiveProgressBudget.Load()))
}

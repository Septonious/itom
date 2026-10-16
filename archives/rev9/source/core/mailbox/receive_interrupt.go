package mailbox

import "time"

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

// A receive owner may calibrate silence from its unloaded provider RTT. Keep
// this separate from the absolute operation cap and the event-driven IDLE wait.
func (c *Client) SetReceiveProgressBudget(budget time.Duration) {
	c.receiveProgressBudget.Store(int64(min(10*time.Second, max(1500*time.Millisecond, budget))))
}
func (c *Client) ReceiveProgressBudget() time.Duration {
	return max(1500*time.Millisecond, time.Duration(c.receiveProgressBudget.Load()))
}

package mailbox

import (
	"sync/atomic"
	"time"
)

var socketGeneration atomic.Uint64

// Closed observes the decoder lifetime, including a TLS close_notify received
// while no command is running. It is independent of application publication.
func (c *Client) Closed() <-chan struct{} { return c.c.Closed() }

type socketFailure struct{ Stage, Reason string }

// SocketSnapshot contains bounded categories and counters, never server text,
// addresses, credentials or message bodies. ID identifies one physical socket.
type SocketSnapshot struct {
	ID               uint64 `json:"id"`
	Generation       uint64 `json:"generation"`
	WorkerID         int    `json:"worker_id"`
	Role             string `json:"role"`
	State            string `json:"state"`
	FirstCloseStage  string `json:"first_close_stage,omitempty"`
	FirstCloseReason string `json:"first_close_reason,omitempty"`
	IdleMS           int64  `json:"idle_ms"`
	ReadBytes        int64  `json:"read_bytes"`
	WriteBytes       int64  `json:"write_bytes"`
}

func (c *cappedConn) noteClose(stage, reason string) {
	if reason == "" {
		reason = "decoder_closed"
	}
	c.firstClose.CompareAndSwap(nil, &socketFailure{stage, reason})
}

// Ready rejects a closure already observed by the socket or IMAP decoder. It
// deliberately does not claim to detect a failure that has not arrived yet.
func (c *Client) Ready() bool {
	if c == nil || c.c == nil || c.closed.Load() || c.conn.firstClose.Load() != nil {
		return false
	}
	select {
	case <-c.c.Closed():
		return false
	default:
		return true
	}
}

func (c *Client) SocketSnapshot(role string) SocketSnapshot {
	s := SocketSnapshot{Role: role, State: "dead"}
	if c == nil || c.conn == nil {
		return s
	}
	s.ID = c.conn.id
	s.Generation = c.conn.id
	s.ReadBytes, s.WriteBytes = c.conn.readBytes.Load(), c.conn.writeBytes.Load()
	if last := c.conn.lastIO.Load(); last != 0 {
		s.IdleMS = max(0, time.Since(time.Unix(0, last)).Milliseconds())
	}
	if f := c.conn.firstClose.Load(); f != nil {
		s.FirstCloseStage, s.FirstCloseReason = f.Stage, f.Reason
	}
	if c.Ready() {
		s.State = "ready"
		if c.busy.Load() {
			s.State = "busy"
		}
	}
	return s
}

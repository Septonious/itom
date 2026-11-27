package main

import "time"

// Diagnostic-only evidence. Track verified application bytes and phase-local
// terminal milestones; never affect scheduling, workload, deadlines or gates.
// A nil milestone is distinct from an observed timestamp of zero.
type flowProgress struct {
	VerifiedBytes      uint64   `json:"verified_bytes"`
	LastVerifiedMS     float64  `json:"last_verified_ms"`
	WriterDoneMS       *float64 `json:"writer_done_ms,omitempty"`
	WriterBytes        *uint64  `json:"writer_bytes,omitempty"`
	OriginEOFMS        *float64 `json:"origin_eof_ms,omitempty"`
	OriginBytes        *uint64  `json:"origin_bytes,omitempty"`
	ResponseVerifiedMS *float64 `json:"response_verified_ms,omitempty"`
}

func (c *clock) noteStreamBytesLocked(stream string, n int, now time.Time) {
	if c.streamProgress == nil {
		c.streamProgress = make(map[string]flowProgress)
	}
	f := c.streamProgress[stream]
	f.VerifiedBytes += uint64(n)
	f.LastVerifiedMS = float64(now.Sub(c.start).Microseconds()) / 1000
	c.streamProgress[stream] = f
}

func (c *clock) noteStreamStage(stream, stage string, n uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.streamProgress == nil {
		c.streamProgress = make(map[string]flowProgress)
	}
	f := c.streamProgress[stream]
	ms := float64(time.Since(c.start).Microseconds()) / 1000
	switch stage {
	case "writer_done":
		f.WriterDoneMS, f.WriterBytes = &ms, &n
	case "origin_eof":
		f.OriginEOFMS, f.OriginBytes = &ms, &n
	case "response_verified":
		f.ResponseVerifiedMS = &ms
	default:
		return
	}
	c.streamProgress[stream] = f
}

func (c *clock) copyStreamProgressLocked() map[string]flowProgress {
	out := make(map[string]flowProgress, len(c.streamProgress))
	for id, f := range c.streamProgress {
		out[id] = f
	}
	return out
}

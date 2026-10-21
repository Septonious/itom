package tunnel

import "time"

// responseWaitPolicy belongs exclusively to one receive worker. A request wake
// opens a bounded search; draining visible rows never sleeps. After two empty
// turns, IDLE supplies the wake rather than exponentially slower empty FETCHes.
// The audit is still required: EXISTS is advisory, not visibility/UID proof.
// This policy has no authority over physical grants, cursors or liveness.
type responseWaitPolicy struct{ emptyTurns int }

func (p *responseWaitPolicy) request() { p.emptyTurns = 0 }
func (p *responseWaitPolicy) empty(hot, advanced bool, rows int) bool {
	if !hot || advanced || rows > 0 {
		p.emptyTurns = 0
		return false
	}
	p.emptyTurns = min(p.emptyTurns+1, 3)
	return p.emptyTurns >= 3
}
func (*responseWaitPolicy) poll(direct time.Duration) time.Duration {
	if direct <= 0 {
		return 10 * time.Millisecond
	}
	return min(10*time.Millisecond, max(2*time.Millisecond, direct/4))
}
func (*responseWaitPolicy) audit(direct time.Duration) time.Duration {
	if direct <= 0 {
		return 100 * time.Millisecond
	}
	return min(150*time.Millisecond, max(20*time.Millisecond, direct*2))
}

// Entering notification wait carries another command turn. Restrict the new
// search to unloaded routes where that turn is at most twice the normal hot
// polling cap. Slower or unknown paths keep the RC2 receive policy; loaded
// tunnel echoes are not propagation evidence for enabling this optimization.
func (*responseWaitPolicy) enabled(direct time.Duration, isolated bool) bool {
	return isolated && direct > 0 && direct <= 20*time.Millisecond
}

package session

import (
	"time"

	"itom.local/core/wire"
)

// OpenRequest is the client half of one OPEN transaction. The session owner
// allocates an unused odd ID and starts it only after Gate.Ready(). Retries
// reuse the logical request, but each returned frame gets a fresh Noise counter.
type OpenRequest struct {
	generation, stream          uint64
	target                      wire.Connect
	policy                      ControlPolicy
	lastNow, deadline, nextSend time.Duration
	sends                       int
	done, stopped               bool
	code                        wire.Reason
}

func NewOpenRequest(generation, stream uint64, target wire.Connect, now time.Duration, policy ControlPolicy) (*OpenRequest, *wire.Frame, error) {
	if generation == 0 || now < 0 || !policy.valid() || now > time.Duration(1<<63-1)-policy.Timeout {
		return nil, nil, ErrState
	}
	f, err := wire.EncodeControl(stream, target)
	if err != nil {
		return nil, nil, err
	}
	r := &OpenRequest{generation: generation, stream: stream, target: target, policy: policy,
		lastNow: now, deadline: now + policy.Timeout, nextSend: now + policy.RetryInterval, sends: 1}
	return r, &f, nil
}
func (r *OpenRequest) check(generation uint64, now time.Duration) error {
	if generation != r.generation {
		return ErrGeneration
	}
	if r.stopped || now < r.lastNow {
		return ErrState
	}
	r.lastNow = now
	if !r.done && now >= r.deadline {
		r.done, r.code = true, wire.ReasonTimeout
		return ErrDeadline
	}
	return nil
}
func (r *OpenRequest) Poll(generation uint64, now time.Duration) (*wire.Frame, error) {
	if err := r.check(generation, now); err != nil {
		return nil, err
	}
	if r.done || r.sends >= r.policy.MaxSends || now < r.nextSend {
		return nil, nil
	}
	r.sends++
	r.nextSend = r.deadline
	if r.policy.RetryInterval < r.deadline-now {
		r.nextSend = now + r.policy.RetryInterval
	}
	f, err := wire.EncodeControl(r.stream, r.target)
	return &f, err
}
func (r *OpenRequest) Receive(generation uint64, now time.Duration, f wire.Frame) (bool, error) {
	if err := r.check(generation, now); err != nil {
		return false, err
	}
	m, err := wire.DecodeControl(f)
	if err != nil {
		return false, err
	}
	result, ok := m.(wire.ConnectResult)
	if !ok || f.Stream != r.stream {
		return false, ErrProtocol
	}
	if r.done {
		if result.Code != r.code {
			return false, ErrProtocol
		}
		return false, nil
	}
	r.done, r.code = true, result.Code
	return true, nil
}
func (r *OpenRequest) Result() (wire.Reason, bool) { return r.code, r.done }
func (r *OpenRequest) DataAllowed() bool           { return !r.stopped && r.done && r.code == wire.ReasonNone }
func (r *OpenRequest) Stop()                       { r.stopped = true }

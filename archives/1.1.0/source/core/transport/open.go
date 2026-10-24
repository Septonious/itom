package transport

import (
	"context"
	"net"
	"time"

	"itom.local/core/wire"
)

type openState uint8

const (
	openEstablished openState = iota // explicit Add is component-only/manual mode
	openAwaitPeer
	openAwaitExit
	openRejected
)

type OpenFailure uint8

const (
	OpenDenied OpenFailure = iota + 1
	OpenUnreachable
	OpenBusy
)

func (f OpenFailure) Error() string {
	switch f {
	case OpenDenied:
		return "V2_OPEN_DENIED"
	case OpenBusy:
		return "V2_OPEN_CAPACITY"
	default:
		return "V2_OPEN_UNREACHABLE"
	}
}

func (s *Session) configureOpen(role uint8, window uint64, cipher *Cipher) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.openRole != 0 || len(s.flows) != 0 || window == 0 || window > s.limits.StreamBytes {
		return ErrProtocol
	}
	if s.crypto != nil {
		direction, _ := s.crypto.SessionDirection()
		if role != uint8(direction)+1 {
			return ErrProtocol
		}
	}
	if cipher != nil {
		if err := s.bindCipherLocked(cipher, wire.Direction(role-1)); err != nil {
			return err
		}
	}
	s.openRole, s.openWindow, s.nextOpenID = role, window, 1
	s.openChanged = make(chan struct{})
	return nil
}
func (s *Session) signalOpenLocked() {
	if s.openChanged != nil {
		close(s.openChanged)
		s.openChanged = make(chan struct{})
	}
}

// ClientDialer exposes a stream only after authenticated OPEN_OK. The original
// 1500ms OPEN maximum remains in force. Its context never owns a carrier or the
// lifetime of a successfully established application connection.
type ClientDialer struct{ session *Session }

func NewClientDialer(s *Session, receiveWindow uint64) (*ClientDialer, error) {
	if s == nil {
		return nil, ErrProtocol
	}
	if err := s.configureOpen(1, receiveWindow, nil); err != nil {
		return nil, err
	}
	return &ClientDialer{session: s}, nil
}
func (d *ClientDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" {
		return nil, ErrProtocol
	}
	target, err := ParseTarget(address)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	s := d.session
	s.mu.Lock()
	id := s.nextOpenID
	if id == 0 || id > maxStreamID-2 {
		s.mu.Unlock()
		return nil, ErrCapacity
	}
	if err = s.addLocked(id, 0, s.openWindow); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.nextOpenID += 2
	f := s.flows[id]
	f.opening, f.openPending, f.openTarget = openAwaitPeer, true, target
	payload, _ := target.encode()
	f.openFrame = Record{Kind: Open, Generation: s.generation, Stream: id, ReceiveLimit: f.receiveLimit(), Payload: payload}
	s.signalOutgoing()
	s.mu.Unlock()
	for {
		s.mu.Lock()
		if err = ctx.Err(); err != nil {
			if s.flows[id] == f && !f.stopped() && !s.closed {
				s.dropLocked(f)
				f.localReset, f.applicationClosed = true, true
				s.signalOutgoing()
			}
			s.mu.Unlock()
			return nil, err
		}
		if f.openFailure != 0 {
			s.mu.Unlock()
			return nil, f.openFailure
		}
		if s.closed || s.flows[id] != f || f.stopped() {
			s.mu.Unlock()
			return nil, ErrClosed
		}
		if f.opening == openEstablished {
			s.mu.Unlock()
			return s.Conn(id)
		}
		wake := f.notify
		s.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-wake:
		}
	}
}

func (s *Session) receiveOpenLocked(r Record) error {
	if s.openRole != 2 || r.Stream&1 != 1 || r.ReceiveLimit > s.limits.StreamBytes {
		return ErrProtocol
	}
	target, _ := decodeTarget(r.Payload) // Record.valid already checked canonical form
	if f := s.flows[r.Stream]; f != nil {
		if f.stopped() {
			return ErrClosed
		}
		if f.openTarget != target || f.openCredit != r.ReceiveLimit {
			return ErrProtocol
		}
		return nil // no duplicate dialing, reply or physical retry
	}
	if !s.ids.available(r.Stream) {
		return ErrClosed
	}
	if err := s.addLocked(r.Stream, r.ReceiveLimit, s.openWindow); err != nil {
		return err
	}
	f := s.flows[r.Stream]
	f.opening, f.openTarget, f.openCredit = openAwaitExit, target, r.ReceiveLimit
	s.signalOpenLocked()
	return nil
}

func (s *Session) resolveOpenPublicationsLocked(id uint64) {
	for token, dispatch := range s.dispatches {
		if dispatch.uncertain && dispatch.record.Stream == id && (dispatch.record.Kind == Open || dispatch.record.Kind == OpenOK || dispatch.record.Kind == OpenError) {
			delete(s.dispatches, token) // logical evidence only; physical debt unchanged
		}
	}
}
func (s *Session) receiveOpenResultLocked(f *stream, r Record) error {
	if s.openRole != 1 || !f.openSent {
		return ErrProtocol
	}
	if r.Kind == OpenOK {
		if r.ReceiveLimit > s.limits.StreamBytes || f.opening != openAwaitPeer && f.opening != openEstablished {
			return ErrProtocol
		}
		f.peerCredit = max(f.peerCredit, r.ReceiveLimit)
		f.opening = openEstablished
	} else {
		if f.opening != openAwaitPeer {
			return ErrProtocol
		}
		f.openFailure = OpenFailure(r.Payload[0])
		s.dropLocked(f)
		f.opening, f.localReset, f.applicationClosed = openRejected, true, true
	}
	s.resolveOpenPublicationsLocked(f.id)
	f.signal()
	s.signalOutgoing()
	return nil
}

// A RESET may overtake OPEN on independent physical carriers. Keep its bounded
// tombstone until RESET_ACK publication, so a later OPEN cannot dial a target.
func (s *Session) receiveEarlyResetLocked(id uint64) error {
	if len(s.flows) >= s.limits.Streams {
		return ErrCapacity
	}
	f := &stream{id: id, remoteReset: true, resetAckPending: true, applicationClosed: true, notify: make(chan struct{})}
	s.flows[id] = f
	s.order = append(s.order, id)
	s.ids.commit(id)
	s.signalOutgoing()
	return nil
}

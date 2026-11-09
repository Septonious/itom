package mailstream

import (
	"context"
	"time"
)

// These 64-bit nonces use existing authenticated PING/PONG framing. Older
// peers respond with the *same* nonce; the new peer returns a different
// supported nonce. That makes feature discovery backward-compatible while
// preserving the existing Noise/READY handshake and wire frame version.
const (
	mailCodecOfferNonce     uint64 = 0x49544f4d38354f31
	mailCodecSupportedNonce uint64 = 0x49544f4d38355331
	mailCodecActivateNonce  uint64 = 0x49544f4d38354131
	mailCodecActiveNonce    uint64 = 0x49544f4d38354b31
)

// The transport is deliberately optional for loopback and test Links.
// This avoids introducing a dependency on real IMAP in the Session engine.
type negotiatedMailCodec interface {
	setOptimizedMailCodec(bool)
}

func (s *Session) offerOptimizedMailCodec() {
	if s.server {
		return
	}
	if _, ok := s.link.(negotiatedMailCodec); !ok {
		return
	}
	// The initial offer is still encoded as Base64. A legacy 1.1.5 peer
	// simply returns an ordinary echo PONG; it cannot activate ASCII85.
	go func() {
		ctx, cancel := context.WithTimeout(s.ctx, 2*time.Second)
		defer cancel()
		_ = s.send(ctx, frame{kind: framePing, offset: mailCodecOfferNonce}, true)
	}()
}

func (s *Session) applyMailCodecNegotiation(f frame) (handled bool, err error) {
	if f.stream != 0 || len(f.data) != 0 {
		return false, nil
	}
	codec, ok := s.link.(negotiatedMailCodec)
	if !ok {
		return false, nil
	}
	switch {
	case s.server && f.kind == framePing && f.offset == mailCodecOfferNonce:
		// Optional transport optimization must never terminate an otherwise
		// healthy session when the bounded priority queue is briefly full.
		_ = s.trySend(frame{kind: framePong, offset: mailCodecSupportedNonce}, true)
		return true, nil
	case !s.server && f.kind == framePong && f.offset == mailCodecSupportedNonce:
		// Do not repeatedly switch on retransmitted capability replies.
		if !s.mailCodecActivated.CompareAndSwap(false, true) {
			return true, nil
		}
		codec.setOptimizedMailCodec(true)
		err := s.trySend(frame{kind: framePing, offset: mailCodecActivateNonce}, true)
		if err != nil {
			s.mailCodecActivated.Store(false)
			codec.setOptimizedMailCodec(false)
		}
		return true, nil
	case s.server && f.kind == framePing && f.offset == mailCodecActivateNonce:
		codec.setOptimizedMailCodec(true)
		_ = s.trySend(frame{kind: framePong, offset: mailCodecActiveNonce}, true)
		return true, nil
	case !s.server && f.kind == framePong && f.offset == mailCodecActiveNonce:
		return true, nil
	}
	return false, nil
}

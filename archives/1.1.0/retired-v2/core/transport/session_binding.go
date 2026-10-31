package transport

import "itom.local/core/wire"

// Bind every physical route of this logical owner to one exact Noise epoch and
// endpoint role, across all accounts/planes/ledgers. Equal public bindings do
// not make independently established keys/counters interchangeable. Rekeying
// needs an explicit coordinated owner transition, not a replacement publisher.
func (s *Session) bindCipher(c *Cipher, direction wire.Direction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bindCipherLocked(c, direction)
}

func (s *Session) bindCipherLocked(c *Cipher, direction wire.Direction) error {
	if s.closed {
		return ErrClosed
	}
	if c == nil || c.epoch == nil {
		return ErrProtocol
	}
	actual, session := c.epoch.SessionDirection()
	if !session || actual != direction || s.openRole != 0 && s.openRole != uint8(direction)+1 || s.crypto != nil && s.crypto != c.epoch {
		return ErrProtocol
	}
	s.crypto = c.epoch
	return nil
}

package transport

import "itom.local/core/secure"

// Cipher binds the independent v2 record format to the domain-separated Noise
// epoch. Carrier routing is outside the cipher; Pair remains the cryptographic
// authority and cannot be replaced by a physical account placement decision.
type Cipher struct{ epoch *secure.Epoch }

func NewCipher(epoch *secure.Epoch) (*Cipher, error) {
	if epoch == nil {
		return nil, ErrProtocol
	}
	return &Cipher{epoch}, nil
}
func (c *Cipher) Seal(r Record) ([]byte, error) {
	b, err := r.MarshalBinary()
	if err != nil {
		return nil, err
	}
	defer clear(b)
	return c.epoch.SealSession(b, r.Kind != Data)
}
func (c *Cipher) Open(packet []byte) (Record, error) {
	b, err := c.epoch.OpenSession(packet)
	if err != nil {
		return Record{}, err
	}
	defer clear(b)
	return DecodeRecord(b)
}

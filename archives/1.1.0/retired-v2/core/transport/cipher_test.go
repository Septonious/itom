package transport_test

import (
	"bytes"
	"itom.local/core/secure"
	"itom.local/core/transport"
	"itom.local/core/wire"
	"testing"
)

func TestNewProtocolHasIndependentKeysAndAuthenticatesFullSizeRecords(t *testing.T) {
	b := secure.Binding{Server: wire.ID{1}, User: "12345678", Pair: wire.ID{2}, Session: wire.ID{3}, Handshake: wire.ID{4}, Epoch: 1, ConfigHash: [32]byte{5}}
	key := bytes.Repeat([]byte{8}, 32)
	client, e := secure.NewSessionHandshake(b, key, true)
	if e != nil {
		t.Fatal(e)
	}
	server, e := secure.NewSessionHandshake(b, key, false)
	if e != nil {
		t.Fatal(e)
	}
	first, e := client.Write(nil)
	if e != nil {
		t.Fatal(e)
	}
	old, e := secure.NewHandshake(b, key, false)
	if e != nil {
		t.Fatal(e)
	}
	if e = old.Read(first); e == nil {
		t.Fatal("old protocol authenticated new protocol handshake")
	}
	if e = server.Read(first); e != nil {
		t.Fatal(e)
	}
	second, e := server.Write(nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = client.Read(second); e != nil {
		t.Fatal(e)
	}
	ce, e := client.Epoch()
	if e != nil {
		t.Fatal(e)
	}
	se, e := server.Epoch()
	if e != nil {
		t.Fatal(e)
	}
	c, e := transport.NewCipher(ce)
	if e != nil {
		t.Fatal(e)
	}
	s, e := transport.NewCipher(se)
	if e != nil {
		t.Fatal(e)
	}
	record := transport.Record{Kind: transport.Data, Generation: 7, Stream: 1, Payload: bytes.Repeat([]byte{77}, transport.MaxPayload)}
	packet, e := c.Seal(record)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Open(packet); e == nil {
		t.Fatal("own mailbox loopback accepted")
	}
	tampered := append([]byte(nil), packet...)
	tampered[len(tampered)-1] ^= 1
	if _, e = s.Open(tampered); e == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	got, e := s.Open(packet)
	if e != nil || !bytes.Equal(got.Payload, record.Payload) {
		t.Fatal("full-size body lost", e)
	}
	if _, e = s.Open(packet); e == nil {
		t.Fatal("duplicate provider body advanced application twice")
	}
	if _, e = ce.Seal(wire.Frame{Type: wire.Ping}); e == nil {
		t.Fatal("legacy wire selected under v2 keys")
	}
}

package wire

import "testing"

func TestRecoveryFastGrantClassIsAuthenticatedAndBounded(t *testing.T) {
	offer := grantCases()[0].message.(StorageGrant)
	offer.Limit = Quota{Bytes: 512 << 10, Messages: 2}
	message := RecoveryFastGrant{Offer: offer}
	frame, err := EncodeControl(0, message)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Type != RecoveryFastWriteGrant {
		t.Fatal("class disappeared")
	}
	decoded, err := DecodeControl(frame)
	if err != nil || decoded.(RecoveryFastGrant) != message {
		t.Fatal(decoded, err)
	}
	legacy := frame
	legacy.Type = WriteGrant
	normal, err := DecodeControl(legacy)
	if err != nil || normal.(StorageGrant) != offer {
		t.Fatal("physical allocation changed with service class", err)
	}
	for n := 0; n < len(frame.Payload); n++ {
		bad := frame
		bad.Payload = frame.Payload[:n]
		if _, err := DecodeControl(bad); err == nil {
			t.Fatal("truncated grant accepted", n)
		}
	}
	for _, bad := range []Frame{{Type: frame.Type, Stream: 1, Payload: frame.Payload}, {Type: frame.Type, Logical: 1, Payload: frame.Payload}, {Type: frame.Type, Payload: append(append([]byte(nil), frame.Payload...), 0)}} {
		if _, err := DecodeControl(bad); err == nil {
			t.Fatal("invalid class frame accepted")
		}
	}
	if _, err := EncodeControl(0, RecoveryFastGrant{Offer: StorageGrant{Key: offer.Key}}); err == nil {
		t.Fatal("empty grant accepted")
	}
}

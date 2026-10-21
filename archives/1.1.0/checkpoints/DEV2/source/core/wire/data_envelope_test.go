package wire

import (
	"bytes"
	"testing"
)

func envelopeFixture(t *testing.T) (UsageReport, []byte, []byte) {
	t.Helper()
	k := QuotaKey{Store: ID{1}, Epoch: 2, Grant: 3, Session: ID{4}, User: "12345678", Account: "a128"}
	key := bytes.Repeat([]byte{7}, 32)
	r, body, err := BindDataEnvelope(k, 2, ID{5}, 900, "itom-u-up-data-00", []byte("opaque encrypted DATA batch"), key, func(b []byte) (uint64, error) { return uint64(len(b) + 100), nil })
	if err != nil {
		t.Fatal(err)
	}
	return r, body, key
}

func TestDataEnvelopeAuthenticationBindingAndCanonicalCharge(t *testing.T) {
	r, body, key := envelopeFixture(t)
	got, payload, err := OpenDataEnvelope(body, key, r.Attempt.EncodedBytes)
	if err != nil || got.Key != r.Key || got.Total != r.Total || *got.Attempt != *r.Attempt || string(payload) != "opaque encrypted DATA batch" {
		t.Fatal("self describing envelope round trip", err)
	}
	if got.Total.Bytes != 900+got.Attempt.EncodedBytes || got.Total.Messages != 2 {
		t.Fatal("cumulative charge changed")
	}
	if _, _, err = OpenDataEnvelope(body, key, r.Attempt.EncodedBytes+2); err == nil {
		t.Fatal("physical MIME size substituted for canonical charge")
	}
	wrong := bytes.Repeat([]byte{8}, 32)
	if _, _, err = OpenDataEnvelope(body, wrong, r.Attempt.EncodedBytes); err == nil {
		t.Fatal("wrong authorization key accepted")
	}
	for i := range body {
		mutated := bytes.Clone(body)
		mutated[i] ^= 1
		if _, _, err = OpenDataEnvelope(mutated, key, r.Attempt.EncodedBytes); err == nil {
			t.Fatalf("unauthenticated mutation at %d accepted", i)
		}
	}
	if _, err = DataEnvelopePayload(body, r); err != nil {
		t.Fatal(err)
	}
	r.Key.Account = "a127"
	if _, err = DataEnvelopePayload(body, r); err == nil {
		t.Fatal("descriptor from another account accepted")
	}
}

func TestDataEnvelopeRejectsInvalidCountersAndTruncation(t *testing.T) {
	r, body, key := envelopeFixture(t)
	for i := 0; i < len(body); i++ {
		if _, _, err := OpenDataEnvelope(body[:i], key, r.Attempt.EncodedBytes); err == nil {
			t.Fatalf("truncation %d accepted", i)
		}
	}
	for _, prev := range []uint64{1 << 40, ^uint64(0)} {
		if _, _, err := BindDataEnvelope(r.Key, 1, ID{5}, prev, "itom-up", []byte{1}, key, func([]byte) (uint64, error) { return 300, nil }); err == nil {
			t.Fatal("cumulative overflow accepted")
		}
	}
	if _, _, err := BindDataEnvelope(r.Key, 0, ID{5}, 0, "itom-up", []byte{1}, key, func([]byte) (uint64, error) { return 300, nil }); err == nil {
		t.Fatal("zero ordinal accepted")
	}
}

func FuzzDataEnvelope(f *testing.F) {
	f.Add([]byte("ITE2"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, raw []byte) { _, _, _ = OpenDataEnvelope(raw, bytes.Repeat([]byte{1}, 32), 500) })
}

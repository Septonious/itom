package secure

import (
	"bytes"
	"errors"
	"itom.local/core/wire"
	"testing"
)

func batchEpochs(t *testing.T) (*Epoch, *Epoch) {
	t.Helper()
	b := Binding{Server: wire.ID{1}, User: "12345678", Pair: wire.ID{2}, Session: wire.ID{3}, Handshake: wire.ID{4}, Epoch: 1, ConfigHash: [32]byte{5}}
	client, err := NewSessionHandshake(b, bytes.Repeat([]byte{6}, 32), true)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewSessionHandshake(b, bytes.Repeat([]byte{6}, 32), false)
	if err != nil {
		t.Fatal(err)
	}
	first, err := client.Write(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = server.Read(first); err != nil {
		t.Fatal(err)
	}
	second, err := server.Write(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Read(second); err != nil {
		t.Fatal(err)
	}
	a, err := client.Epoch()
	if err != nil {
		t.Fatal(err)
	}
	bEpoch, err := server.Epoch()
	if err != nil {
		t.Fatal(err)
	}
	return a, bEpoch
}

func TestRejectedBatchChargesDecryptUsageWithoutCommittingReplay(t *testing.T) {
	sender, receiver := batchEpochs(t)
	var packets [][]byte
	var charge uint64
	for _, text := range []string{"first", "last"} {
		packet, err := sender.SealSession([]byte(text), false)
		if err != nil {
			t.Fatal(err)
		}
		packets = append(packets, packet)
		charge += uint64(sessionHeader + len(text))
	}
	reject := errors.New("invalid transport record")
	if plains, err := receiver.OpenSessionBatch(packets, func([][]byte) error { return reject }); !errors.Is(err, reject) || plains != nil {
		t.Fatal("validator failure exposed plaintext", err)
	}
	if receiver.rxBytes != charge {
		t.Fatal("failed validation erased key usage", receiver.rxBytes)
	}
	if _, err := receiver.OpenSessionBatch(packets, nil); err != nil || receiver.rxBytes != 2*charge {
		t.Fatal("failed validation consumed replay or lost decrypt charge", err)
	}
}

func TestBatchHardByteCeilingCannotPartiallyCommitReplay(t *testing.T) {
	sender, receiver := batchEpochs(t)
	first, err := sender.SealSession([]byte("first"), false)
	if err != nil {
		t.Fatal(err)
	}
	last, err := sender.SealSession([]byte("last"), false)
	if err != nil {
		t.Fatal(err)
	}
	receiver.rxBytes = HardBytes - sessionHeader - uint64(len("first"))
	if plains, err := receiver.OpenSessionBatch([][]byte{first, last}, nil); !errors.Is(err, ErrKeyLimit) || plains != nil {
		t.Fatal("batch passed the original hard byte ceiling", err)
	}
	if receiver.rxBytes != HardBytes || receiver.replay.check(0) != nil || receiver.replay.check(1) != nil {
		t.Fatal("partial batch reset usage or committed replay")
	}
}

func TestBatchRetainsOriginalAuthenticationFailureCeiling(t *testing.T) {
	sender, receiver := batchEpochs(t)
	packet, err := sender.SealSession([]byte("valid"), false)
	if err != nil {
		t.Fatal(err)
	}
	bad := append([]byte(nil), packet...)
	bad[len(bad)-1] ^= 1
	receiver.rxFailures = 1023
	if _, err = receiver.OpenSessionBatch([][]byte{bad}, nil); !errors.Is(err, ErrAuth) {
		t.Fatal(err)
	}
	if _, err = receiver.OpenSessionBatch([][]byte{packet}, nil); !errors.Is(err, ErrKeyLimit) || receiver.rxFailures != 1024 {
		t.Fatal("batch bypassed the authentication-failure ceiling", err)
	}
}

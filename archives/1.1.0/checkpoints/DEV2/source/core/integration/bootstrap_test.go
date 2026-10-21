package integration

import (
	"itom.local/core/session"
	"itom.local/core/wallet"
	"itom.local/core/wire"
	"path/filepath"
	"testing"
)

func TestAuthenticatedStoreBindsWalletToNoiseSession(t *testing.T) {
	clientCipher, serverCipher := epochs(t)
	store := wire.ID{21}
	server, e := session.NewBootstrap(session.Server, 1, 0, store, 4)
	if e != nil {
		t.Fatal(e)
	}
	client, e := session.NewBootstrap(session.Client, 1, 0, wire.ID{}, 0)
	if e != nil {
		t.Fatal(e)
	}
	offer, e := server.Poll(1, 0)
	if e != nil {
		t.Fatal(e)
	}
	result, e := client.Receive(1, 0, throughNoise(t, serverCipher, clientCipher, *offer.Send))
	if e != nil || result.Proposed == nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "wallet.db")
	w, e := wallet.OpenBound(path, result.Proposed.Store, clientCipher)
	if e != nil {
		t.Fatal(e)
	}
	if w.Session() != clientCipher.Context().Session {
		t.Fatal("wallet and Noise sessions diverged")
	}
	ack, e := client.CommitStore(1, 0, result.Proposed.Store)
	if e != nil {
		t.Fatal(e)
	}
	accepted, e := server.Receive(1, 0, throughNoise(t, clientCipher, serverCipher, *ack.Send))
	if e != nil || !accepted.Ready {
		t.Fatal(e)
	}
	if e = w.Close(); e != nil {
		t.Fatal(e)
	}
	if same, e := wallet.OpenBound(path, store, clientCipher); e == nil {
		same.Close()
		t.Fatal("wallet resumed an old Noise session")
	}
	if wrong, e := wallet.Open(path, "12345678", wire.ID{99}); e == nil {
		wrong.Close()
		t.Fatal("store changed without binding")
	}
}

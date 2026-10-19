package tunnel

import (
	"bytes"
	"testing"

	"itom.local/core/mailbox"
	"itom.local/core/wire"
)

func TestEnvelopePublicTransportUsesBothDirections(t *testing.T) {
	f := newProductionSupervisorFixtureAccounts(t, 3)
	for _, id := range []string{"a01", "a02", "a03"} {
		p := f.openOn(t, id)
		for i := 0; i < 4; i++ {
			if err := supervisorEcho(p, i+1); err != nil {
				t.Fatal(id, err)
			}
		}
		f.client.mu.Lock()
		client := f.client.lanes[id]
		f.client.mu.Unlock()
		f.mu.Lock()
		server := f.serverLanes[id]
		f.mu.Unlock()
		if client.dataShards != 1 || server.dataShards != 1 {
			t.Fatalf("split service missing: client=%d server=%d", client.dataShards, server.dataShards)
		}
		if len(client.data.Config().Accounts[0].Users[0].Folders) != 4 {
			t.Fatal("DATA scope not extended")
		}
		if len(client.envelopeKey) != 32 || len(server.envelopeKey) != 32 {
			t.Fatal("public endpoint did not negotiate envelope authentication")
		}
		if err := server.data.Audit(); err != nil {
			t.Fatal(err)
		}
		if err := client.data.Audit(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEnvelopePendingRecoveryDoesNotRequireLegacyWrapper(t *testing.T) {
	l := &lane{side: true, recvFolder: "itom-up", pending: map[[32]byte]mailbox.Message{}}
	key := bytes.Repeat([]byte{7}, 32)
	k := wire.QuotaKey{Store: wire.ID{1}, Epoch: 1, Grant: 1, Session: wire.ID{2}, User: "12345678", Account: "a01"}
	r, body, err := wire.BindDataEnvelope(k, 2, wire.ID{3}, 1000, "itom-up", []byte("opaque encrypted future ordinal"), key, canonicalDataSize)
	if err != nil {
		t.Fatal(err)
	}
	mime, err := mailbox.Encode(body)
	if err != nil {
		t.Fatal(err)
	}
	m := mailbox.Message{Folder: "itom-up", UID: 10, Validity: 1, MIME: mime}
	if err = l.queuePendingRemote(m, body); err != nil {
		t.Fatal(err)
	}
	if len(l.pending) != 1 || l.pending[r.Attempt.Digest].UID != 10 {
		t.Fatal("forward envelope gap lost its BODY")
	}
	if err = l.queuePendingRemote(m, body); err != nil || len(l.pending) != 1 {
		t.Fatal("duplicate discovery amplified pending state", err)
	}
}

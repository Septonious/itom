package transport_test

import (
	"context"
	"encoding/binary"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/secure"
	"itom.local/core/transport"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

func TestReadyControlUpdatesShareOneRealMailWithoutBorrowingDataOrWaitingForFill(t *testing.T) {
	f := serverPublications(t, transport.Fast, 4<<20)
	control := issuer(t, filepath.Join(t.TempDir(), "control.db"), 1<<20, 32)
	w, err := wallet.OpenBound(filepath.Join(t.TempDir(), "wallet.db"), control.StoreID(), f.clientEpoch)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	g, err := control.IssueRemote(f.scope, pool.Amount{Bytes: 1 << 20, Messages: 16})
	if err != nil {
		t.Fatal(err)
	}
	offer, err := control.RemoteOffer(g.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Accept(offer); err != nil {
		t.Fatal(err)
	}
	p, err := transport.NewClientPublisher(f.client, transport.Fast, transport.ControlLedger, f.clientKey, w, "account1", "fast", f.key)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint64{1, 3, 5} {
		if id != 1 {
			add(t, f.client, id, 1<<20, 1<<20)
			add(t, f.server, id, 1<<20, 1<<20)
		}
		if err = f.client.GrowReceive(id, 2<<20); err != nil {
			t.Fatal(err)
		}
	}
	prepared, err := p.PrepareControls(context.Background(), transport.MaxControlBytes)
	if err != nil || prepared == nil {
		t.Fatal("already-ready controls waited for a future fill", err)
	}
	box := &sharedMailbox{}
	cx, sx := box.selected(t, "fast"), box.selected(t, "fast")
	defer cx.Close()
	defer sx.Close()
	if outcome, err := prepared.Start(context.Background(), cx); err != nil || outcome != transport.Accepted {
		t.Fatal(outcome, err)
	}
	receiver, err := transport.NewRemoteReceiver(f.server, transport.ControlLedger, f.serverKey, control, "account1", "12345678", "fast", 9, f.key)
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := transport.NewServerInbox(sx, transport.DefaultInboxLimits(), receiver)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = inbox.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint64{1, 3, 5} {
		flow, _ := f.server.FlowSnapshot(id)
		if flow.PeerCredit != 2<<20 {
			t.Fatal("control member dropped", id, flow)
		}
	}
	entries, err := w.Entries()
	if err != nil || entries[0].Used.Messages != 1 || len(entries[0].Pending) != 1 {
		t.Fatal("control container spent/refunded multiple physical permissions", entries, err)
	}
	if err = f.client.Queue(1, []byte("DATA remains separate"), time.Now()); err != nil {
		t.Fatal(err)
	}
	if extra, err := p.PrepareControls(context.Background(), transport.MaxControlBytes); err != nil || extra != nil || f.client.Snapshot().SendBuffered == 0 {
		t.Fatal("control service borrowed DATA or created extra empty work", err)
	}
	if err = control.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestControlGrantFittingOneOriginalPacketDoesNotRequireContainerOverhead(t *testing.T) {
	f := serverPublications(t, transport.Fast, 4<<20)
	control := issuer(t, filepath.Join(t.TempDir(), "control.db"), 10000, 16)
	w, err := wallet.OpenBound(filepath.Join(t.TempDir(), "wallet.db"), control.StoreID(), f.clientEpoch)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	plain, _ := incoming(transport.Credit, 1, 2<<20, nil).MarshalBinary()
	packetSize, _ := secure.SessionPacketSize(len(plain))
	shape := wire.QuotaKey{Store: control.StoreID(), Epoch: control.Run(), Grant: 1, Session: f.scope.Session, Account: f.scope.Account, User: f.scope.User}
	size, err := wire.DataEnvelopeSize(shape, "fast", packetSize)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := mailbox.EncodedSize(size)
	g, err := control.IssueRemote(f.scope, pool.Amount{Bytes: encoded, Messages: 1})
	if err != nil {
		t.Fatal(err)
	}
	offer, _ := control.RemoteOffer(g.Ref)
	if err = w.Accept(offer); err != nil {
		t.Fatal(err)
	}
	p, err := transport.NewClientPublisher(f.client, transport.Fast, transport.ControlLedger, f.clientKey, w, "account1", "fast", f.key)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.client.GrowReceive(1, 2<<20); err != nil {
		t.Fatal(err)
	}
	prepared, err := p.PrepareControls(context.Background(), transport.MaxControlBytes)
	if err != nil || prepared == nil || prepared.Usage().Attempt.EncodedBytes != encoded {
		t.Fatal("optional batching stranded an original exact-size grant", err)
	}
}

func TestControlContainerAuthenticatesWholeBatchAndRejectsDataOpenAndResetBeforeReplay(t *testing.T) {
	client, server := cipherPair(t, wire.ID{88})
	records := []transport.Record{incoming(transport.Credit, 1, 100, nil), incoming(transport.Receipt, 3, 0, nil)}
	packet, err := client.SealControls(records)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = server.OpenRecordsFor(packet, transport.DataLedger); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("control bundle borrowed DATA authority", err)
	}
	bad := append([]byte(nil), packet...)
	bad[len(bad)-1] ^= 1
	if _, err = server.OpenRecordsFor(bad, transport.ControlLedger); !errors.Is(err, secure.ErrAuth) {
		t.Fatal("partially authenticated control batch accepted", err)
	}
	if decoded, err := server.OpenRecordsFor(packet, transport.ControlLedger); err != nil || len(decoded) != 2 {
		t.Fatal("rejected batch consumed an earlier member's replay slot", len(decoded), err)
	}
	for _, invalid := range []transport.Record{incoming(transport.Data, 1, 0, []byte("wrong ledger")), incoming(transport.Reset, 1, 0, nil)} {
		if _, err := client.SealControls([]transport.Record{records[0], invalid}); !errors.Is(err, transport.ErrProtocol) {
			t.Fatal("allocating/DATA member entered a progress container", invalid.Kind, err)
		}
	}
	// Construct a MAC/AEAD-valid but semantically forbidden container. Every
	// member's replay slot must remain eligible when the whole body is rejected.
	a, b := cipherPair(t, wire.ID{89})
	first, _ := a.Seal(records[0])
	reset, _ := a.Seal(incoming(transport.Reset, 3, 0, nil))
	container := make([]byte, 8)
	copy(container, "ICB2")
	binary.BigEndian.PutUint16(container[4:6], 2)
	for _, p := range [][]byte{first, reset} {
		container = binary.BigEndian.AppendUint32(container, uint32(len(p)))
		container = append(container, p...)
	}
	if _, err := b.OpenRecordsFor(container, transport.ControlLedger); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("RESET was coupled to progress-only admission", err)
	}
	if _, err := b.Open(first); err != nil {
		t.Fatal("semantic rejection poisoned valid receipt/credit replay", err)
	}
}

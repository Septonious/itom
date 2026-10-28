package transport_test

import (
	"context"
	"errors"
	"testing"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/transport"
	"itom.local/core/wire"
)

func TestEightAuthorityRecordsUseOneOriginalPhysicalControlTicket(t *testing.T) {
	a := authorityPair(t)
	ctx := context.Background()
	before, e := a.control.Held("account1", "12345678")
	if e != nil {
		t.Fatal(e)
	}
	for n := uint64(1); n <= 8; n++ {
		if _, e = a.server.Issue(ctx, transport.DataLedger, n, pool.Amount{Bytes: 8192, Messages: 2}); e != nil {
			t.Fatal(e)
		}
	}
	p, e := a.serverControl.PrepareControls(ctx, transport.MaxControlBytes)
	if e != nil || p == nil {
		t.Fatal(p, e)
	}
	after, e := a.control.Held("account1", "12345678")
	if e != nil || after.Messages != before.Messages+1 {
		t.Fatal("authority batch did not use one physical reservation", before, after, e)
	}
	box := &sharedMailbox{}
	x := box.selected(t, "fast")
	if out, e := p.Start(ctx, x); e != nil || out != transport.Accepted {
		t.Fatal(out, e)
	}
	body := fetchAt(t, x, 1)
	delivery, e := a.clientControlReceiver.Decode(9, 1, body.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	if e = delivery.Apply(); e != nil {
		t.Fatal(e)
	}
	entries, e := a.dataWallet.Entries()
	if e != nil || len(entries) != 8 || a.client.Stats(transport.DataLedger).AcceptedOffers != 8 {
		t.Fatal("batch lost real issued offers", len(entries), e)
	}
	if p.Usage().Attempt.EncodedBytes != uint64(len(p.MIME())) {
		t.Fatal("batch physical MIME charge changed")
	}
	for _, store := range []*journal.Store{a.control, a.f.store} {
		if e = store.Audit(); e != nil {
			t.Fatal(e)
		}
	}
}

func chargedAuthorityPacket(t *testing.T, a *authorityFixture, packet []byte) transport.Body {
	t.Helper()
	s := a.control
	k := wire.QuotaKey{Store: s.StoreID(), Epoch: s.Run(), Grant: 1, Account: "account1", User: "12345678", Session: a.f.scope.Session}
	ticket, _, e := s.ReserveLocalSized(a.f.scope, "fast", len(packet), len(packet), func(n int) (uint64, error) {
		size, e := wire.ServerEnvelopeSize(k, wire.ServerControl, "fast", n)
		if e != nil {
			return 0, e
		}
		return mailbox.EncodedSize(size)
	})
	if e != nil {
		t.Fatal(e)
	}
	usage, raw, e := wire.BindServerEnvelope(ticket.Key(), wire.ServerControl, wire.ID{99}, "fast", packet, a.f.key, func(b []byte) (uint64, error) { return mailbox.EncodedSize(len(b)) })
	if e != nil {
		t.Fatal(e)
	}
	attempt, e := ticket.Commit(wire.ID{99}, usage.Attempt.EncodedBytes, raw)
	if e != nil {
		t.Fatal(e)
	}
	mime, e := mailbox.Encode(raw)
	if e != nil {
		t.Fatal(e)
	}
	box := &sharedMailbox{}
	x := box.selected(t, "fast")
	c, e := x.Append(context.Background(), "fast", mime)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Wait(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = s.Complete(attempt.Ref, journal.Accepted); e != nil {
		t.Fatal(e)
	}
	return fetchAt(t, x, 1)
}
func TestAuthorityBatchRejectsForeignMemberBeforeAnyWalletMutation(t *testing.T) {
	a := authorityPair(t)
	ctx := context.Background()
	offers := make([]wire.StorageGrant, 2)
	for n := range offers {
		ref, e := a.server.Issue(ctx, transport.DataLedger, uint64(n+1), pool.Amount{Bytes: 8192, Messages: 2})
		if e != nil {
			t.Fatal(e)
		}
		offers[n], e = a.f.store.RemoteOffer(ref)
		if e != nil {
			t.Fatal(e)
		}
	}
	bad := offers[1]
	bad.Key.Account = "foreign"
	records := []transport.Record{authorityRecord(t, transport.DataLedger, offers[0]), authorityRecord(t, transport.DataLedger, bad)}
	records[1].Offset = 2
	packet, e := a.f.serverKey.SealControls(records)
	if e != nil {
		t.Fatal(e)
	}
	body := chargedAuthorityPacket(t, a, packet)
	if _, e = a.clientControlReceiver.Decode(9, 1, body.Bytes); !errors.Is(e, transport.ErrProtocol) {
		t.Fatal("foreign member admitted", e)
	}
	entries, e := a.dataWallet.Entries()
	if e != nil || len(entries) != 0 {
		t.Fatal("valid prefix mutated wallet before full-batch admission", entries, e)
	}
	// The original valid queued offers still work after the rejected packet.
	p, e := a.serverControl.PrepareControls(ctx, transport.MaxControlBytes)
	if e != nil || p == nil {
		t.Fatal(p, e)
	}
	box := &sharedMailbox{}
	x := box.selected(t, "fast")
	if out, e := p.Start(ctx, x); e != nil || out != transport.Accepted {
		t.Fatal(out, e)
	}
	physical := fetchAt(t, x, 1)
	d, e := a.clientControlReceiver.Decode(9, 1, physical.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	if e = d.Apply(); e != nil {
		t.Fatal(e)
	}
	entries, e = a.dataWallet.Entries()
	if e != nil || len(entries) != 2 {
		t.Fatal("rejected packet poisoned valid later authority", len(entries), e)
	}
}
func TestAuthorityBatchesCannotMixLiveProgressOrExceedTheirBound(t *testing.T) {
	a := authorityPair(t)
	ref, e := a.server.Issue(context.Background(), transport.DataLedger, 1, pool.Amount{Bytes: 8192, Messages: 2})
	if e != nil {
		t.Fatal(e)
	}
	offer, e := a.f.store.RemoteOffer(ref)
	if e != nil {
		t.Fatal(e)
	}
	r := authorityRecord(t, transport.DataLedger, offer)
	if _, e = a.f.serverKey.SealControls([]transport.Record{r, {Kind: transport.Receipt, Generation: 7, Stream: 1}}); !errors.Is(e, transport.ErrProtocol) {
		t.Fatal("authority was mixed with live progress", e)
	}
	records := make([]transport.Record, 9)
	for n := range records {
		records[n] = r
		records[n].Offset = uint64(n + 1)
	}
	if _, e = a.f.serverKey.SealControls(records); !errors.Is(e, transport.ErrCapacity) {
		t.Fatal("oversized authority batch accepted", e)
	}
}

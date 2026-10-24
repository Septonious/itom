package transport_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/transport"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

func TestClientInboxIgnoresOwnBodyWithoutWalletReconciliationOrPeerReplay(t *testing.T) {
	f := serverPublications(t, transport.Fast, 4<<20)
	w, err := wallet.OpenBound(filepath.Join(t.TempDir(), "wallet.db"), f.store.StoreID(), f.clientEpoch)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	g, err := f.store.IssueRemote(f.scope, pool.Amount{Bytes: 2 << 20, Messages: 16})
	if err != nil {
		t.Fatal(err)
	}
	offer, err := f.store.RemoteOffer(g.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Accept(offer); err != nil {
		t.Fatal(err)
	}
	publisher, err := transport.NewClientPublisher(f.client, transport.Fast, transport.DataLedger, f.clientKey, w, "account1", "fast", f.key)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.client.Queue(1, []byte("request bytes"), time.Now()); err != nil {
		t.Fatal(err)
	}
	p, err := publisher.Prepare(context.Background(), transport.MaxPayload)
	if err != nil || p == nil {
		t.Fatal(err)
	}
	decode, err := transport.NewClientInboxDecoder(f.incoming)
	if err != nil {
		t.Fatal(err)
	}
	before := f.client.Snapshot()
	if delivery, err := decode(9, transport.Body{UID: 11, Bytes: p.MIME()}); err != nil || delivery != nil {
		t.Fatal("own CLIENT body became peer traffic", delivery, err)
	}
	if after := f.client.Snapshot(); after != before {
		t.Fatal("own body changed logical receipt/state", before, after)
	}
	entries, err := w.Entries()
	if err != nil || len(entries) != 1 || len(entries[0].Pending) != 1 || entries[0].Used != p.Usage().Total {
		t.Fatal("echo reconciled wallet history", entries, err)
	}
	remote, err := transport.NewRemoteReceiver(f.server, transport.DataLedger, f.serverKey, f.store, "account1", "12345678", "fast", 9, f.key)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := remote.Decode(9, 11, p.MIME())
	if err != nil {
		t.Fatal("client own scan consumed server peer replay", err)
	}
	if err = delivery.Apply(); err != nil {
		t.Fatal(err)
	}
	response := serverBody(t, f, []byte("actual peer response"))
	peer, err := decode(9, transport.Body{UID: 12, Bytes: response.MIME()})
	if err != nil || peer == nil {
		t.Fatal("valid peer not classified", err)
	}
	if err = peer.Apply(); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 64)
	n, err := f.client.Read(1, got)
	if err != nil || !bytes.Equal(got[:n], []byte("actual peer response")) {
		t.Fatal("wrong peer bytes", n, err)
	}
	entries, err = w.Entries()
	if err != nil || len(entries[0].Pending) != 1 || entries[0].Used != p.Usage().Total {
		t.Fatal("peer DATA released physical client debt", entries, err)
	}
}

func TestClientInboxScopeRejectionDoesNotPoisonLaterAuthenticPeer(t *testing.T) {
	f := serverPublications(t, transport.Fast, 4<<20)
	p := serverBody(t, f, []byte("valid after rejections"))
	for _, field := range []string{"store", "run", "account", "folder", "incarnation", "ledger"} {
		o := f.origin
		class := transport.DataLedger
		switch field {
		case "store":
			o.Store[0]++
		case "run":
			o.Run++
		case "account":
			o.Account = "other"
		case "folder":
			o.Folder = "bulk"
		case "incarnation":
			o.UIDValidity++
		case "ledger":
			class = transport.ControlLedger
		}
		r, err := transport.NewClientReceiver(f.client, class, f.clientKey, o, f.key)
		if err != nil {
			t.Fatal(field, err)
		}
		decode, err := transport.NewClientInboxDecoder(r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = decode(9, transport.Body{UID: 11, Bytes: p.MIME()}); !errors.Is(err, transport.ErrProtocol) {
			t.Fatal("wrong negotiated scope accepted", field, err)
		}
	}
	body, err := mailbox.Decode(p.MIME())
	if err != nil {
		t.Fatal(err)
	}
	_, usage, packet, err := wire.OpenServerEnvelope(body, f.key, uint64(len(p.MIME())))
	if err != nil {
		t.Fatal(err)
	}
	usage.Key.Session[0]++
	_, foreign, err := wire.BindServerEnvelope(usage.Key, wire.ServerData, usage.Attempt.Batch, "fast", packet, f.key, func(b []byte) (uint64, error) { return mailbox.EncodedSize(len(b)) })
	if err != nil {
		t.Fatal(err)
	}
	foreignMIME, err := mailbox.Encode(foreign)
	if err != nil {
		t.Fatal(err)
	}
	decode, err := transport.NewClientInboxDecoder(f.incoming)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = decode(9, transport.Body{UID: 11, Bytes: foreignMIME}); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("foreign session accepted", err)
	}
	delivery, err := decode(9, transport.Body{UID: 11, Bytes: p.MIME()})
	if err != nil || delivery == nil {
		t.Fatal("scope rejection consumed replay", err)
	}
	if err = delivery.Apply(); err != nil {
		t.Fatal(err)
	}
}

func TestScopedInboxConstructorsRejectWrongPhysicalRoutesBeforeIO(t *testing.T) {
	f := serverPublications(t, transport.Fast, 4<<20)
	remote, err := transport.NewRemoteReceiver(f.server, transport.DataLedger, f.serverKey, f.store, "account1", "12345678", "fast", 9, f.key)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []struct{ account, folder string }{{"other", "fast"}, {"account1", "bulk"}} {
		x := selectedAccountPeer(t, route.account, route.folder, func(c net.Conn) {
			if _, err := bufio.NewReader(c).Peek(1); err == nil {
				t.Error("scope rejection initiated mailbox I/O")
			}
		})
		if _, err := transport.NewClientInbox(x, transport.DefaultInboxLimits(), f.incoming); !errors.Is(err, transport.ErrProtocol) {
			t.Fatal("client accepted wrong physical route", err)
		}
		if _, err := transport.NewServerInbox(x, transport.DefaultInboxLimits(), remote); !errors.Is(err, transport.ErrProtocol) {
			t.Fatal("server accepted wrong physical route", err)
		}
		x.Close()
	}
	for _, routes := range [][]*transport.ClientReceiver{nil, {nil}, {f.incoming, f.incoming}} {
		if _, err := transport.NewClientInboxDecoder(routes...); !errors.Is(err, transport.ErrProtocol) {
			t.Fatal("ambiguous client routes accepted", err)
		}
	}
}

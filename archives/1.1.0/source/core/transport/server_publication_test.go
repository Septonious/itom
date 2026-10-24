package transport_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/secure"
	"itom.local/core/transport"
	"itom.local/core/wire"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func issuer(t *testing.T, path string, limit uint64, messages uint64) *journal.Store {
	t.Helper()
	cfg := journal.Config{Accounts: []journal.Account{{ID: "account1", Provider: "fixture", Limit: pool.Amount{Bytes: limit, Messages: messages}, Users: []journal.User{{ID: "12345678", Limit: pool.Amount{Bytes: limit, Messages: messages}, Folders: []string{"fast", "bulk"}}}}}, MaxGrants: 128, MaxAttempts: 256}
	s, err := journal.Open(path, cfg, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err = s.EnableAfterInventory("account1", "12345678", s.Run()); err != nil {
		t.Fatal(err)
	}
	return s
}

type serverFixture struct {
	client, server       *transport.Session
	clientKey, serverKey *transport.Cipher
	clientEpoch          *secure.Epoch
	store                *journal.Store
	publisher            *transport.ServerPublisher
	incoming             *transport.ClientReceiver
	origin               transport.ServerOrigin
	scope                journal.Scope
	key                  []byte
	folder               string
}

func serverPublications(t *testing.T, plane transport.Plane, limit uint64) *serverFixture {
	t.Helper()
	f := &serverFixture{client: session(t, transport.DefaultLimits()), server: session(t, transport.DefaultLimits()), folder: "fast", key: bytes.Repeat([]byte{7}, 32)}
	if plane == transport.Bulk {
		f.folder = "bulk"
	}
	add(t, f.client, 1, 1<<20, 1<<20)
	add(t, f.server, 1, 1<<20, 1<<20)
	// This fixture needs one shared Noise exchange, including the client epoch
	// used later to bind the duplex wallet. Do not create independent key pairs.
	ce, se := sessionEpochs(t, wire.ID{43})
	f.clientEpoch = ce
	var err error
	f.clientKey, err = transport.NewCipher(ce)
	if err != nil {
		t.Fatal(err)
	}
	f.serverKey, err = transport.NewCipher(se)
	if err != nil {
		t.Fatal(err)
	}
	f.store = issuer(t, filepath.Join(t.TempDir(), "issuer.db"), limit, 128)
	f.scope = journal.Scope{Account: "account1", User: "12345678", Session: wire.ID{43}}
	f.origin = transport.ServerOrigin{Store: f.store.StoreID(), Run: f.store.Run(), Account: f.scope.Account, User: f.scope.User, Folder: f.folder, UIDValidity: 9}
	f.publisher, err = transport.NewServerPublisher(f.server, plane, transport.DataLedger, f.serverKey, f.store, f.scope, f.folder, f.key)
	if err != nil {
		t.Fatal(err)
	}
	f.incoming, err = transport.NewClientReceiver(f.client, transport.DataLedger, f.clientKey, f.origin, f.key)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func serverBody(t *testing.T, f *serverFixture, payload []byte) *transport.PreparedPublication {
	t.Helper()
	if err := f.server.Queue(1, payload, time.Now()); err != nil {
		t.Fatal(err)
	}
	var p *transport.PreparedPublication
	var err error
	if f.folder == "bulk" {
		p, err = f.publisher.PrepareBulk(context.Background(), transport.MaxBulkBytes)
	} else {
		p, err = f.publisher.Prepare(context.Background(), transport.MaxPayload)
	}
	if err != nil || p == nil {
		t.Fatal("server physical publication not prepared", err)
	}
	return p
}

func TestServerBulkBodyIsDurablyChargedBeforeIOAndClientCannotRefundIt(t *testing.T) {
	f := serverPublications(t, transport.Bulk, 4<<20)
	payload := bytes.Repeat([]byte{19, 0, 0xff, 4}, 128<<10)
	p := serverBody(t, f, payload)
	ref, ok := p.ServerAttempt()
	if !ok {
		t.Fatal("server body has no durable recovery identity")
	}
	a, err := f.store.GetAttempt(ref)
	if err != nil || a.State != journal.Started || a.EncodedBytes != uint64(len(p.MIME())) {
		t.Fatal("uncommitted body acquired I/O permission", a, err)
	}
	g, err := f.store.GetGrant(ref.Grant)
	if err != nil || !g.Closed || g.Remote || g.Used.Messages != 1 {
		t.Fatal("server body used remote client authority", g, err)
	}
	if _, err = f.store.ApplyRemote(f.scope.User, f.store.Run(), p.Usage()); !errors.Is(err, journal.ErrScope) {
		t.Fatal("local server descriptor accepted as client grant usage", err)
	}
	delivery, err := f.incoming.Decode(9, 1, p.MIME())
	if err != nil {
		t.Fatal(err)
	}
	if err = delivery.Apply(); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if n, err := f.client.Read(1, got); err != nil || n != len(got) || !bytes.Equal(got, payload) {
		t.Fatal("download data corrupted", n, err)
	}
	if _, err = f.incoming.Decode(9, 1, p.MIME()); !errors.Is(err, secure.ErrReplay) {
		t.Fatal("stored download was delivered twice", err)
	}
	if held, err := f.store.Held(f.scope.Account, f.scope.User); err != nil || held != g.Used {
		t.Fatal("client receipt/read refunded physical storage", held, err)
	}
}

func TestServerBodyScopeAndLedgerMismatchCannotPoisonLaterCorrectDelivery(t *testing.T) {
	f := serverPublications(t, transport.Fast, 1<<20)
	p := serverBody(t, f, []byte("short response"))
	for _, field := range []string{"store", "run", "account", "folder", "ledger", "incarnation"} {
		origin := f.origin
		class := transport.DataLedger
		switch field {
		case "store":
			origin.Store[0] ^= 1
		case "run":
			origin.Run++
		case "account":
			origin.Account = "other_account"
		case "folder":
			origin.Folder = "other_folder"
		case "ledger":
			class = transport.ControlLedger
		case "incarnation":
			origin.UIDValidity++
		}
		other, err := transport.NewClientReceiver(f.client, class, f.clientKey, origin, f.key)
		if err != nil {
			t.Fatal(field, err)
		}
		if _, err = other.Decode(9, 1, p.MIME()); !errors.Is(err, transport.ErrProtocol) {
			t.Fatal("mismatched receiver accepted physical body", field, err)
		}
	}
	delivery, err := f.incoming.Decode(9, 1, p.MIME())
	if err != nil {
		t.Fatal("scope rejection consumed valid replay state", err)
	}
	if err = delivery.Apply(); err != nil {
		t.Fatal(err)
	}
}

func TestLostServerAppendReplyRequiresDurableExactProofWithoutLogicalReceipt(t *testing.T) {
	f := serverPublications(t, transport.Bulk, 4<<20)
	payload := bytes.Repeat([]byte{13}, 512<<10)
	p := serverBody(t, f, payload)
	count := f.server.Snapshot().ActiveDispatches
	first := selectedPeerOn(t, "bulk", func(conn net.Conn) {
		readAppendBody(t, conn, bufio.NewReader(conn))
	})
	if outcome, err := p.Start(context.Background(), first); outcome != transport.Uncertain || !errors.Is(err, transport.ErrUnknown) {
		t.Fatal("lost server APPEND reply did not retain uncertainty", outcome, err)
	}
	ref, _ := p.ServerAttempt()
	if a, err := f.store.GetAttempt(ref); err != nil || a.State != journal.Unknown || f.server.PendingPublication(1) != count {
		t.Fatal("lost reply erased journal/logical recovery state", a, err)
	}
	exact := p.MIME()
	wrongAccount := selectedAccountPeer(t, "account2", "bulk", rejectUnexpectedIO(t))
	if err := p.ConfirmStored(context.Background(), wrongAccount, 17); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("other account's mailbox became local proof authority", err)
	}
	wrongAccount.Close()
	proof := selectedPeerOn(t, "bulk", func(conn net.Conn) {
		line := readLine(t, bufio.NewReader(conn))
		fmt.Fprintf(conn, "* 1 FETCH (UID 17 BODY[] {%d}\r\n", len(exact))
		conn.Write(exact)
		fmt.Fprintf(conn, ")\r\n%s OK fetched\r\n", tag(line))
	})
	if err := p.ConfirmStored(context.Background(), proof, 17); err != nil {
		t.Fatal(err)
	}
	if a, err := f.store.GetAttempt(ref); err != nil || a.State != journal.Committed || a.Location != (journal.Location{UIDValidity: 9, UID: 17}) {
		t.Fatal("storage proof did not durably bind location", a, err)
	}
	state := f.server.Snapshot()
	if state.SendBuffered != uint64(len(payload)) || state.RetainedRecords != count || state.ActiveDispatches != 0 {
		t.Fatal("own storage proof invented application delivery", state)
	}
	if held, err := f.store.Held(f.scope.Account, f.scope.User); err != nil || held != (pool.Amount{Bytes: uint64(len(exact)), Messages: 1}) {
		t.Fatal("storage proof refunded server debt", held, err)
	}
}

package transport_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/secure"
	"itom.local/core/transport"
	"itom.local/core/wallet"
	"itom.local/core/wire"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func cipherPair(t *testing.T, session wire.ID) (*transport.Cipher, *transport.Cipher) {
	t.Helper()
	x, y := sessionEpochs(t, session)
	c, e := transport.NewCipher(x)
	if e != nil {
		t.Fatal(e)
	}
	s, e := transport.NewCipher(y)
	if e != nil {
		t.Fatal(e)
	}
	return c, s
}

func sessionEpochs(t *testing.T, session wire.ID) (*secure.Epoch, *secure.Epoch) {
	t.Helper()
	binding := secure.Binding{Server: wire.ID{1}, User: "12345678", Pair: wire.ID{2}, Session: session, Handshake: wire.ID{4}, Epoch: 1, ConfigHash: [32]byte{5}}
	key := bytes.Repeat([]byte{6}, 32)
	a, e := secure.NewSessionHandshake(binding, key, true)
	if e != nil {
		t.Fatal(e)
	}
	b, e := secure.NewSessionHandshake(binding, key, false)
	if e != nil {
		t.Fatal(e)
	}
	first, e := a.Write(nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = b.Read(first); e != nil {
		t.Fatal(e)
	}
	second, e := b.Write(nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Read(second); e != nil {
		t.Fatal(e)
	}
	x, e := a.Epoch()
	if e != nil {
		t.Fatal(e)
	}
	y, e := b.Epoch()
	if e != nil {
		t.Fatal(e)
	}
	return x, y
}

type publicationFixture struct {
	sender, receiver *transport.Session
	wallet           *wallet.Wallet
	store            *journal.Store
	publisher        *transport.ClientPublisher
	incoming         *transport.RemoteReceiver
	key              []byte
}

func publications(t *testing.T, limit uint64, messages uint64) *publicationFixture {
	return publicationsOn(t, limit, messages, transport.Fast)
}
func publicationsOn(t *testing.T, limit uint64, messages uint64, plane transport.Plane) *publicationFixture {
	t.Helper()
	folder := "fast"
	if plane == transport.Bulk {
		folder = "bulk"
	}
	user := "12345678"
	account := "account1"
	dir := t.TempDir()
	config := journal.Config{Accounts: []journal.Account{{ID: account, Provider: "fixture", Limit: pool.Amount{Bytes: limit, Messages: messages}, Users: []journal.User{{ID: user, Limit: pool.Amount{Bytes: limit, Messages: messages}, Folders: []string{"fast", "bulk"}}}}}, MaxGrants: 32, MaxAttempts: 64}
	store, e := journal.Open(filepath.Join(dir, "issuer.db"), config, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { store.Close() })
	if e = store.EnableAfterInventory(account, user, store.Run()); e != nil {
		t.Fatal(e)
	}
	w, e := wallet.Open(filepath.Join(dir, "wallet.db"), user, store.StoreID())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { w.Close() })
	grant, e := store.IssueRemote(journal.Scope{Account: account, User: user, Session: w.Session()}, pool.Amount{Bytes: limit, Messages: messages})
	if e != nil {
		t.Fatal(e)
	}
	offer, e := store.RemoteOffer(grant.Ref)
	if e != nil {
		t.Fatal(e)
	}
	if e = w.Accept(offer); e != nil {
		t.Fatal(e)
	}
	sender := session(t, transport.DefaultLimits())
	receiver := session(t, transport.DefaultLimits())
	add(t, sender, 1, 1<<20, 1<<20)
	add(t, receiver, 1, 1<<20, 1<<20)
	c, s := cipherPair(t, w.Session())
	key := bytes.Repeat([]byte{7}, 32)
	publisher, e := transport.NewClientPublisher(sender, plane, transport.DataLedger, c, w, account, folder, key)
	if e != nil {
		t.Fatal(e)
	}
	incoming, e := transport.NewRemoteReceiver(receiver, transport.DataLedger, s, store, account, user, folder, 9, key)
	if e != nil {
		t.Fatal(e)
	}
	return &publicationFixture{sender, receiver, w, store, publisher, incoming, key}
}
func prepare(t *testing.T, f *publicationFixture, b []byte) *transport.PreparedPublication {
	t.Helper()
	if e := f.sender.Queue(1, b, time.Now()); e != nil {
		t.Fatal(e)
	}
	p, e := f.publisher.Prepare(context.Background(), transport.MaxPayload)
	if e != nil || p == nil {
		t.Fatal("could not prepare admitted physical body", e)
	}
	return p
}
func TestPreparedBodyFitsCanonicalGrantBeforeEncryptionAndIsChargedBeforeIO(t *testing.T) {
	f := publications(t, 2500, 10)
	p := prepare(t, f, bytes.Repeat([]byte{42}, 4096))
	usage := p.Usage()
	raw := p.MIME()
	if usage.Attempt == nil || uint64(len(raw)) != usage.Attempt.EncodedBytes || usage.Total.Bytes > 2500 {
		t.Fatal("incorrect canonical charge", usage)
	}
	entries, e := f.wallet.Entries()
	if e != nil || len(entries) != 1 || entries[0].Used != usage.Total || entries[0].Fenced || len(entries[0].Pending) != 1 {
		t.Fatal("permit not durably charged before APPEND", entries, e)
	}
	body, e := mailbox.Decode(raw)
	if e != nil {
		t.Fatal(e)
	}
	charge, e := mailbox.EncodedSize(len(body))
	if e != nil || charge != uint64(len(raw)) {
		t.Fatal("MIME estimator changed", charge, e)
	}
	report, packet, e := wire.OpenDataEnvelope(body, f.key, uint64(len(raw)))
	if e != nil || report.Key != usage.Key {
		t.Fatal(e)
	}
	expected, e := wire.DataEnvelopeSize(usage.Key, "fast", len(packet))
	if e != nil || expected != len(body) {
		t.Fatal("pre-encryption size does not match bound body", expected, len(body), e)
	}
}
func TestAccountingGapDoesNotConsumeCipherReplayOrForceCarrierReconnect(t *testing.T) {
	f := publications(t, 50000, 10)
	first := prepare(t, f, []byte("first"))
	second := prepare(t, f, []byte("second"))
	if _, e := f.incoming.Decode(9, 2, second.MIME()); !errors.Is(e, journal.ErrOrder) {
		t.Fatal("out-of-order descriptor did not defer safely", e)
	}
	one, e := f.incoming.Decode(9, 1, first.MIME())
	if e != nil {
		t.Fatal(e)
	}
	if e = one.Apply(); e != nil {
		t.Fatal(e)
	}
	two, e := f.incoming.Decode(9, 2, second.MIME())
	if e != nil {
		t.Fatal("valid packet lost its replay slot during accounting gap", e)
	}
	if e = two.Apply(); e != nil {
		t.Fatal(e)
	}
	b := make([]byte, 11)
	n, e := f.receiver.Read(1, b)
	if e != nil || n != 11 || string(b) != "firstsecond" {
		t.Fatal(n, string(b), e)
	}
	if _, e = f.incoming.Decode(9, 2, second.MIME()); !errors.Is(e, secure.ErrReplay) {
		t.Fatal("duplicate provider BODY delivered twice", e)
	}
}
func TestPreparedBodyCanSurviveCanceledUnstartedCommandWithoutSecondSpend(t *testing.T) {
	f := publications(t, 50000, 10)
	p := prepare(t, f, []byte("payload"))
	received := make(chan []byte, 1)
	x := selectedPeer(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		line := readLine(t, r)
		at := strings.LastIndex(line, "{")
		end := strings.LastIndex(line, "}")
		if at < 0 || end < at {
			t.Error("missing literal")
			return
		}
		n, e := strconv.Atoi(line[at+1 : end])
		if e != nil {
			t.Error(e)
			return
		}
		fmt.Fprint(c, "+\r\n")
		body := make([]byte, n+2)
		if _, e = io.ReadFull(r, body); e != nil {
			t.Error(e)
			return
		}
		received <- body[:n]
		fmt.Fprintf(c, "%s OK stored\r\n", tag(line))
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	outcome, e := p.Start(ctx, x)
	if !errors.Is(e, context.Canceled) || outcome != transport.NotStarted {
		t.Fatal(outcome, e)
	}
	outcome, e = p.Start(context.Background(), x)
	if e != nil || outcome != transport.Accepted {
		t.Fatal(outcome, e)
	}
	if !bytes.Equal(<-received, p.MIME()) {
		t.Fatal("unstarted retry changed physical bytes")
	}
	entries, e := f.wallet.Entries()
	if e != nil || entries[0].Used.Messages != 1 || len(entries[0].Pending) != 1 {
		t.Fatal("retry consumed another ordinal", entries, e)
	}
	if _, e = p.Start(context.Background(), x); !errors.Is(e, transport.ErrProtocol) {
		t.Fatal("already started physical body re-APPENDed", e)
	}
}
func TestWrongMailboxIncarnationCannotRegisterOrDeliver(t *testing.T) {
	f := publications(t, 50000, 10)
	p := prepare(t, f, []byte("payload"))
	if _, e := f.incoming.Decode(10, 1, p.MIME()); !errors.Is(e, transport.ErrProtocol) {
		t.Fatal(e)
	}
	delivery, e := f.incoming.Decode(9, 1, p.MIME())
	if e != nil {
		t.Fatal("wrong incarnation poisoned later correct receive", e)
	}
	if e = delivery.Apply(); e != nil {
		t.Fatal(e)
	}
}

func TestProtocolRecordsCannotSpendDataAllowanceInTheFastMailbox(t *testing.T) {
	f := publications(t, 50000, 10)
	if err := f.sender.Receive(incoming(transport.Data, 1, 0, []byte("peer request"))); err != nil {
		t.Fatal(err)
	}
	if !f.sender.ReadyFor(transport.Fast, transport.ControlLedger) {
		t.Fatal("receipt demand missing")
	}
	if f.sender.ReadyFor(transport.Fast, transport.DataLedger) {
		t.Fatal("protocol work classified as DATA")
	}
	p, err := f.publisher.Prepare(context.Background(), transport.MaxPayload)
	if err != nil || p != nil {
		t.Fatal("DATA publisher drained protocol ledger work", p, err)
	}
	entries, err := f.wallet.Entries()
	if err != nil || entries[0].Used.Messages != 0 {
		t.Fatal("protocol frame charged to DATA", entries, err)
	}
}

// A real verified TLS/SELECT boundary for local stored-body proof tests. This
// fixture asserts command ownership; it is not calibrated provider service.
func selectedPeer(t *testing.T, script func(net.Conn)) *transport.Executor {
	t.Helper()
	return selectedPeerOn(t, "fast", script)
}
func selectedPeerOn(t *testing.T, folder string, script func(net.Conn)) *transport.Executor {
	t.Helper()
	return selectedAccountPeer(t, "account1", folder, script)
}

func selectedAccountPeer(t *testing.T, account, folder string, script func(net.Conn)) *transport.Executor {
	return selectedAccountPeerWithLimits(t, account, folder, transport.DefaultIMAPLimits(), script)
}

func selectedAccountPeerWithLimits(t *testing.T, account, folder string, limits transport.IMAPLimits, script func(net.Conn)) *transport.Executor {
	t.Helper()
	cert, roots, err := certificate()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// This bounded byte-integrity fixture is not a timed throughput gate.
		// Guard missing I/O progress, not total lifetime across many commands.
		conn = &fixtureProgressConn{Conn: conn, timeout: 4 * time.Second}
		reader := bufio.NewReader(conn)
		fmt.Fprint(conn, "* OK provider\r\n")
		readLine(t, reader)
		fmt.Fprint(conn, "BOOT1 OK\r\n")
		readLine(t, reader)
		fmt.Fprint(conn, "* CAPABILITY IMAP4rev1\r\nBOOT2 OK\r\n")
		readLine(t, reader)
		fmt.Fprint(conn, "* OK [UIDVALIDITY 9] selected\r\nBOOT3 OK\r\n")
		script(&bufferedConn{Conn: conn, reader: reader})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	selected, err := transport.DialSelected(ctx, &net.Dialer{}, transport.Endpoint{Address: listener.Addr().String(), AccountID: account, ServerName: "fixture.local", Username: "fixture", Password: "test-only", Mailbox: folder, TLS: &tls.Config{RootCAs: roots}}, limits)
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	listener.Close()
	t.Cleanup(func() {
		selected.Executor.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("proof provider did not stop")
		}
	})
	return selected.Executor
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

type fixtureProgressConn struct {
	net.Conn
	timeout time.Duration
}

func (c *fixtureProgressConn) Read(p []byte) (int, error) {
	if err := c.Conn.SetReadDeadline(time.Now().Add(c.timeout)); err != nil {
		return 0, err
	}
	return c.Conn.Read(p)
}
func (c *fixtureProgressConn) Write(p []byte) (int, error) {
	if err := c.Conn.SetWriteDeadline(time.Now().Add(c.timeout)); err != nil {
		return 0, err
	}
	return c.Conn.Write(p)
}

func (c *bufferedConn) Read(b []byte) (int, error) { return c.reader.Read(b) }
func TestOwnStoredBodyProofResolvesAcceptanceButCannotFabricatePeerDelivery(t *testing.T) {
	f := publications(t, 50000, 10)
	p := prepare(t, f, []byte("not peer delivered"))
	first := selectedPeer(t, func(conn net.Conn) {
		reader := bufio.NewReader(conn)
		line := readLine(t, reader)
		at, end := strings.LastIndex(line, "{"), strings.LastIndex(line, "}")
		if at < 0 || end <= at {
			t.Error("bad APPEND header")
			return
		}
		n, err := strconv.Atoi(line[at+1 : end])
		if err != nil {
			t.Error(err)
			return
		}
		fmt.Fprint(conn, "+\r\n")
		if _, err = io.CopyN(io.Discard, reader, int64(n+2)); err != nil {
			t.Error(err)
		}
	})
	outcome, err := p.Start(context.Background(), first)
	if outcome != transport.Uncertain || !errors.Is(err, transport.ErrUnknown) {
		t.Fatal("lost reply did not retain uncertainty", outcome, err)
	}
	if f.sender.Snapshot().ActiveDispatches != 1 {
		t.Fatal("uncertain body lost its recovery owner")
	}
	exact := p.MIME()
	decoded, err := mailbox.Decode(exact)
	if err != nil {
		t.Fatal(err)
	}
	decoded[len(decoded)-1] ^= 1
	wrong, err := mailbox.Encode(decoded)
	if err != nil {
		t.Fatal(err)
	}
	second := selectedPeer(t, func(conn net.Conn) {
		reader := bufio.NewReader(conn)
		for _, body := range [][]byte{wrong, exact} {
			line := readLine(t, reader)
			fmt.Fprintf(conn, "* 1 FETCH (UID 17 BODY[] {%d}\r\n", len(body))
			conn.Write(body)
			fmt.Fprintf(conn, ")\r\n%s OK fetched\r\n", tag(line))
		}
	})
	if err = p.ConfirmStored(context.Background(), second, 17); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("different physical body accepted as proof", err)
	}
	if f.sender.PendingPublication(1) != 1 {
		t.Fatal("failed proof erased uncertainty")
	}
	if err = p.ConfirmStored(context.Background(), second, 17); err != nil {
		t.Fatal("exact local FETCH did not resolve acceptance", err)
	}
	state := f.sender.Snapshot()
	if state.SendBuffered != uint64(len("not peer delivered")) || state.RetainedRecords != 1 || state.ActiveDispatches != 0 {
		t.Fatal("own mailbox loopback fabricated delivery", state)
	}
	entries, err := f.wallet.Entries()
	if err != nil || entries[0].Used.Messages != 1 || len(entries[0].Pending) != 1 {
		t.Fatal("stored proof refunded or respent quota", entries, err)
	}
}

func TestWalletNoiseSessionMismatchFailsBeforeSpendingOrEncryption(t *testing.T) {
	f := publications(t, 50000, 10)
	different, _ := cipherPair(t, wire.ID{77})
	if _, err := transport.NewClientPublisher(f.sender, transport.Fast, transport.DataLedger, different, f.wallet, "account1", "fast", f.key); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("wallet from another session was admitted", err)
	}
	entries, err := f.wallet.Entries()
	if err != nil || entries[0].Used.Messages != 0 {
		t.Fatal("scope rejection consumed a grant", entries, err)
	}
}

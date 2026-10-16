package datapath

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"io"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/mailcontrol"
	"itom.local/core/pool"
	"itom.local/core/secure"
	"itom.local/core/session"
	"itom.local/core/wallet"
	"itom.local/core/wire"
	"net"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func epochs(t *testing.T, sid wire.ID) (*secure.Epoch, *secure.Epoch) {
	t.Helper()
	b := secure.Binding{Server: wire.ID{1}, User: "12345678", Pair: wire.ID{2}, Session: sid, Handshake: wire.ID{3}, Epoch: 1, ConfigHash: [32]byte{4}}
	left, err := secure.NewHandshake(b, bytes.Repeat([]byte{9}, 32), true)
	if err != nil {
		t.Fatal(err)
	}
	right, err := secure.NewHandshake(b, bytes.Repeat([]byte{9}, 32), false)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := left.Write(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = right.Read(msg); err != nil {
		t.Fatal(err)
	}
	msg, err = right.Write(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = left.Read(msg); err != nil {
		t.Fatal(err)
	}
	l, err := left.Epoch()
	if err != nil {
		t.Fatal(err)
	}
	r, err := right.Epoch()
	if err != nil {
		t.Fatal(err)
	}
	return l, r
}
func authenticated(t *testing.T, tx, rx *secure.Epoch, f wire.Frame) wire.Frame {
	t.Helper()
	packet, err := tx.Seal(f)
	if err != nil {
		t.Fatal(err)
	}
	got, err := rx.Open(packet)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
func controls(t *testing.T, tx, rx *secure.Epoch, m wire.Control) wire.Control {
	t.Helper()
	f, err := wire.EncodeControl(0, m)
	if err != nil {
		t.Fatal(err)
	}
	out, err := wire.DecodeControl(authenticated(t, tx, rx, f))
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func mailPair(t *testing.T, ctx context.Context) (*mailbox.Client, *mailbox.Client) {
	t.Helper()
	certServer := httptest.NewTLSServer(nil)
	cert := certServer.TLS.Certificates[0]
	roots := x509.NewCertPool()
	roots.AddCert(certServer.Certificate())
	certServer.Close()
	mem := imapmemserver.New()
	mem.AddUser(imapmemserver.NewUser("test", "secret"))
	server := imapserver.New(&imapserver.Options{NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
		return mem.NewSession(), nil, nil
	}, Caps: imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}}})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{cert}}))
	t.Cleanup(func() { server.Close(); listener.Close() })
	cfg := mailbox.Config{Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, TLS: "tls", Username: "test", Password: "secret"}
	var clients []*mailbox.Client
	for i := 0; i < 2; i++ {
		c, err := mailbox.DialWithRoots(ctx, cfg, []string{"u-up", "u-down"}, roots)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		for _, f := range []string{"u-up", "u-down"} {
			if err = c.Ensure(ctx, f); err != nil {
				t.Fatal(err)
			}
		}
		clients = append(clients, c)
	}
	return clients[0], clients[1]
}
func TestDATAOverTLSIMAPToRealTCPAndBack(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	mailClient, mailServer := mailPair(t, ctx)
	cfg := journal.Config{MaxGrants: 16, MaxAttempts: 32, Accounts: []journal.Account{{ID: "a01", Provider: "test", Limit: pool.Amount{Bytes: 4 << 20, Messages: 64}, Users: []journal.User{{ID: "12345678", Limit: pool.Amount{Bytes: 4 << 20, Messages: 64}, Folders: []string{"u-up", "u-down"}}}}}}
	ledger, err := journal.Open(filepath.Join(t.TempDir(), "server.db"), cfg, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	if err = ledger.EnableAfterInventory("a01", "12345678", ledger.Run()); err != nil {
		t.Fatal(err)
	}
	var sid wire.ID
	if _, err = rand.Read(sid[:]); err != nil {
		t.Fatal(err)
	}
	clientCipher, serverCipher := epochs(t, sid)

	// Unlike earlier revisions, every control below uses journaled APPEND/FETCH
	// over the same real TLS IMAP account as DATA. One dispatcher per endpoint.
	clientDispatch, err := mailbox.NewDispatcher(ctx, mailClient, mailbox.DispatchConfig{Routes: []mailbox.RouteConfig{{User: "12345678", Folders: []string{"u-up", "u-down"}}}, QueuePerUser: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer clientDispatch.Close()
	serverDispatch, err := mailbox.NewDispatcher(ctx, mailServer, mailbox.DispatchConfig{Routes: []mailbox.RouteConfig{{User: "12345678", Folders: []string{"u-up", "u-down"}}}, QueuePerUser: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer serverDispatch.Close()
	clientRoute, _ := clientDispatch.Route("12345678")
	serverRoute, _ := serverDispatch.Route("12345678")
	type pendingControl struct {
		sender  *mailcontrol.Sender
		ref     journal.AttemptRef
		message mailbox.Message
	}
	var controlGC []pendingControl
	senders := map[*secure.Epoch]*mailcontrol.Sender{}
	receivers := map[*secure.Epoch]*mailbox.Route{clientCipher: serverRoute, serverCipher: clientRoute}
	folders := map[*secure.Epoch]string{clientCipher: "u-up", serverCipher: "u-down"}
	cursors := map[*secure.Epoch]mailbox.Cursor{}
	controlLedgers := map[*secure.Epoch]*journal.Store{}
	for i, cipher := range []*secure.Epoch{clientCipher, serverCipher} {
		controlCfg := cfg
		controlCfg.MaxGrants = 64
		controlCfg.MaxAttempts = 128
		j, e := journal.Open(filepath.Join(t.TempDir(), "control.db"), controlCfg, time.Second)
		if e != nil {
			t.Fatal(e)
		}
		defer j.Close()
		if e = j.EnableAfterInventory("a01", "12345678", j.Run()); e != nil {
			t.Fatal(e)
		}
		var route mailbox.Transport = clientRoute
		if i == 1 {
			route = serverRoute
		}
		sender, e := mailcontrol.NewSender(j, journal.Scope{Account: "a01", User: "12345678", Session: sid}, folders[cipher], cipher, route)
		if e != nil {
			t.Fatal(e)
		}
		senders[cipher] = sender
		controlLedgers[cipher] = j
	}
	authenticated := func(t *testing.T, tx, rx *secure.Epoch, f wire.Frame) wire.Frame {
		t.Helper()
		result := senders[tx].Send(ctx, f)
		if result.Err != nil || result.Attempt == nil {
			t.Fatal(result.Err)
		}
		row, e := controlLedgers[tx].GetAttempt(*result.Attempt)
		if e != nil {
			t.Fatal(e)
		}
		for page := 0; page < 32; page++ {
			messages, next, e := receivers[tx].Page(ctx, folders[tx], cursors[tx])
			if e != nil {
				t.Fatal(e)
			}
			cursors[tx] = next
			for _, m := range messages {
				body, e := mailbox.Decode(m.MIME)
				if e != nil {
					t.Fatal(e)
				}
				if sha256.Sum256(body) != row.Digest {
					continue
				} // DATA remains owned by its scanner.
				got, e := mailcontrol.Open(m.MIME, rx)
				if e != nil {
					t.Fatal(e)
				}
				controlGC = append(controlGC, pendingControl{senders[tx], *result.Attempt, m})
				return got
			}
		}
		t.Fatal("control message not found")
		return wire.Frame{}
	}
	controls := func(t *testing.T, tx, rx *secure.Epoch, m wire.Control) wire.Control {
		t.Helper()
		f, e := wire.EncodeControl(0, m)
		if e != nil {
			t.Fatal(e)
		}
		got, e := wire.DecodeControl(authenticated(t, tx, rx, f))
		if e != nil {
			t.Fatal(e)
		}
		return got
	}
	// DATA scanners share each folder with control records. The dispatcher owns
	// wire access; protocol discrimination below happens before DATA decryption.
	dataPage := func(route *mailbox.Route, folder string, cur mailbox.Cursor) ([]mailbox.Message, mailbox.Cursor, error) {
		messages, next, e := route.Page(ctx, folder, cur)
		if e != nil {
			return nil, cur, e
		}
		var out []mailbox.Message
		for _, m := range messages {
			body, e := mailbox.Decode(m.MIME)
			if e != nil {
				return nil, cur, e
			}
			if !mailcontrol.IsControlBody(body) {
				out = append(out, m)
			}
		}
		return out, next, nil
	}
	// Activation and coordinator binding also travel through TLS IMAP.
	clientGate, confirm, err := session.NewGate(session.Client, 1, 0, session.DefaultControlPolicy())
	if err != nil {
		t.Fatal(err)
	}
	serverGate, _, err := session.NewGate(session.Server, 1, 0, session.DefaultControlPolicy())
	if err != nil {
		t.Fatal(err)
	}
	ready, err := serverGate.Receive(1, 0, authenticated(t, clientCipher, serverCipher, confirm.Send[0]))
	if err != nil {
		t.Fatal(err)
	}
	ack, err := clientGate.Receive(1, 0, authenticated(t, serverCipher, clientCipher, ready.Send[0]))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = serverGate.Receive(1, 0, authenticated(t, clientCipher, serverCipher, ack.Send[0])); err != nil || !clientGate.Ready() || !serverGate.Ready() {
		t.Fatal(err)
	}
	serverBootstrap, err := session.NewBootstrap(session.Server, 1, 0, ledger.StoreID(), ledger.Run())
	if err != nil {
		t.Fatal(err)
	}
	clientBootstrap, err := session.NewBootstrap(session.Client, 1, 0, wire.ID{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	info, err := serverBootstrap.Poll(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := clientBootstrap.Receive(1, 0, authenticated(t, serverCipher, clientCipher, *info.Send))
	if err != nil || proposal.Proposed == nil {
		t.Fatal(err)
	}
	w, err := wallet.OpenBound(filepath.Join(t.TempDir(), "wallet.db"), proposal.Proposed.Store, clientCipher)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	committed, err := clientBootstrap.CommitStore(1, 0, proposal.Proposed.Store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = serverBootstrap.Receive(1, 0, authenticated(t, clientCipher, serverCipher, *committed.Send)); err != nil || !serverBootstrap.Ready() {
		t.Fatal(err)
	}
	scope := journal.Scope{Account: "a01", User: "12345678", Session: sid}
	grant, err := ledger.IssueRemote(scope, pool.Amount{Bytes: 1 << 20, Messages: 16})
	if err != nil {
		t.Fatal(err)
	}
	offer, err := ledger.RemoteOffer(grant.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Accept(controls(t, serverCipher, clientCipher, offer).(wire.StorageGrant)); err != nil {
		t.Fatal(err)
	}
	local, err := ledger.Issue(scope, pool.Amount{Bytes: 1 << 20, Messages: 16})
	if err != nil {
		t.Fatal(err)
	}
	a, b := pair(t)
	// Real destination: receives upload until half-close, then returns a response
	// larger than one DATA record. Public-only policy is overridden for this test.
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	upload := bytes.Repeat([]byte("request-data:"), 4000)
	download := bytes.Repeat([]byte("response-data:"), 5000)
	destination := make(chan error, 1)
	go func() {
		c, err := listener.AcceptTCP()
		if err != nil {
			destination <- err
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(10 * time.Second))
		data, err := io.ReadAll(c)
		if err == nil && !bytes.Equal(data, upload) {
			err = ErrState
		}
		if err == nil {
			_, err = c.Write(download)
		}
		if err == nil {
			err = c.CloseWrite()
		}
		destination <- err
	}()
	exit, err := NewTCPExit(ctx, ExitConfig{Generation: 1, MaxSockets: 2, MaxDials: 1, Metadata: 4, AllowPrivate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer exit.Stop()
	open := authenticated(t, clientCipher, serverCipher, control(t, 1, wire.Connect{Host: "127.0.0.1", Port: uint16(listener.Addr().(*net.TCPAddr).Port)}))
	action, err := exit.Request(0, open)
	if err != nil || action.Dial == nil {
		t.Fatal(action, err)
	}
	duplicate, err := exit.Request(time.Millisecond, open)
	if err != nil || duplicate.Dial != nil {
		t.Fatal("duplicate dial", err)
	}
	result := exit.Dial(*action.Dial)
	opened, socket, err := exit.Complete(2*time.Millisecond, result)
	if err != nil || socket == nil {
		t.Fatal(err)
	}
	if opened.Reply == nil {
		t.Fatal("missing OPEN_OK")
	}
	_ = authenticated(t, serverCipher, clientCipher, *opened.Reply)
	_ = socket.SetDeadline(time.Now().Add(10 * time.Second))
	var upCursor, downCursor mailbox.Cursor
	for start := 0; start < len(upload); {
		end := min(start+wire.MaxData, len(upload))
		id, err := a.Queue(1, 1, upload[start:end])
		if err != nil {
			t.Fatal(err)
		}
		frame, _ := a.Frame(1, id)
		n, err := Size(frame, &offer.Key)
		if err != nil {
			t.Fatal(err)
		}
		job, err := a.Begin(1, id, "a01", n, 0)
		if err != nil {
			t.Fatal(err)
		}
		copyJob := *job
		completion := job.WriteRemote(w, offer.Key, "u-up", clientCipher, clientRoute)
		if completion.Err != nil || completion.Usage == nil {
			t.Fatal(completion)
		}
		if again := copyJob.WriteRemote(w, offer.Key, "u-up", clientCipher, clientRoute); again.Err == nil {
			t.Fatal("copied job authorized duplicate APPEND")
		}
		usage := controls(t, clientCipher, serverCipher, *completion.Usage).(wire.UsageReport)
		status, err := ledger.ApplyRemote("12345678", ledger.Run(), usage)
		if err != nil {
			t.Fatal(err)
		}
		if err = w.Acknowledge(controls(t, serverCipher, clientCipher, status).(wire.CleanupStatus)); err != nil {
			t.Fatal(err)
		}
		messages, next, err := dataPage(serverRoute, "u-up", upCursor)
		if err != nil || len(messages) != 1 {
			t.Fatal(len(messages), err)
		}
		upCursor = next
		m := messages[0]
		frame, err = OpenRemote(m.MIME, usage, serverCipher)
		if err != nil {
			t.Fatal(err)
		}
		receipts, err := b.Receive(1, frame)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range receipts {
			if _, err = a.Receive(1, authenticated(t, serverCipher, clientCipher, f)); err != nil {
				t.Fatal(err)
			}
		}
		// Receipt arrives before APPEND completion, then the exact bytes reach TCP.
		if err = a.Complete(1, completion.Attempt, completion.Outcome); err != nil {
			t.Fatal(err)
		}
		for {
			data, err := b.Peek(1, 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(data) == 0 {
				break
			}
			written := WriteSocket(ctx, socket, data)
			credit, err := b.Delivered(1, 1, uint64(written.N))
			if err != nil || written.Err != nil {
				t.Fatal(err, written.Err)
			}
			if _, err = a.Receive(1, authenticated(t, serverCipher, clientCipher, credit)); err != nil {
				t.Fatal(err)
			}
		}
		rows, err := ledger.ScanAttempts(0, 32)
		if err != nil || len(rows) != 1 {
			t.Fatal(rows, err)
		}
		body, _ := mailbox.Decode(m.MIME)
		loc := journal.Location{UIDValidity: m.Validity, UID: m.UID}
		if err = ledger.Observe(rows[0].Ref, ledger.Run(), journal.Observation{Account: "a01", Folder: "u-up", EncodedBytes: uint64(len(m.MIME)), Location: loc, Decoded: body}); err != nil {
			t.Fatal(err)
		}
		if err = mailServer.Delete(ctx, m); err != nil {
			t.Fatal(err)
		}
		if err = ledger.Deleted(rows[0].Ref, ledger.Run(), loc); err != nil {
			t.Fatal(err)
		}
		if err = a.Cleaned(1, completion.Attempt); err != nil {
			t.Fatal(err)
		}
		start = end
	}
	fin, _ := a.Finish(1, 1)
	reply, err := b.Receive(1, authenticated(t, clientCipher, serverCipher, fin))
	if err != nil || !b.HalfClosed(1, 1) {
		t.Fatal(err)
	}
	for _, f := range reply {
		_, err = a.Receive(1, authenticated(t, serverCipher, clientCipher, f))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = HalfCloseSocket(socket); err != nil {
		t.Fatal(err)
	}
	var received bytes.Buffer
	buffer := make([]byte, wire.MaxData)
	for {
		n, readErr := socket.Read(buffer)
		if n > 0 {
			id, err := b.Queue(1, 1, buffer[:n])
			if err != nil {
				t.Fatal(err)
			}
			frame, _ := b.Frame(1, id)
			size, err := Size(frame, nil)
			if err != nil {
				t.Fatal(err)
			}
			job, err := b.Begin(1, id, "a01", size, 0)
			if err != nil {
				t.Fatal(err)
			}
			completion := job.WriteLocal(ledger, local.Ref, "u-down", serverCipher, serverRoute)
			if completion.Err != nil {
				t.Fatal(completion.Err)
			}
			if err = b.Complete(1, completion.Attempt, completion.Outcome); err != nil {
				t.Fatal(err)
			}
			messages, next, err := dataPage(clientRoute, "u-down", downCursor)
			if err != nil || len(messages) != 1 {
				t.Fatal(len(messages), err)
			}
			downCursor = next
			m := messages[0]
			body, _ := mailbox.Decode(m.MIME)
			frame, err = clientCipher.Open(body)
			if err != nil {
				t.Fatal(err)
			}
			receipts, err := a.Receive(1, frame)
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range receipts {
				if _, err = b.Receive(1, authenticated(t, clientCipher, serverCipher, f)); err != nil {
					t.Fatal(err)
				}
			}
			data, err := a.Peek(1, 1)
			if err != nil {
				t.Fatal(err)
			}
			received.Write(data)
			credit, err := a.Delivered(1, 1, uint64(len(data)))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = b.Receive(1, authenticated(t, clientCipher, serverCipher, credit)); err != nil {
				t.Fatal(err)
			}
			loc := journal.Location{UIDValidity: m.Validity, UID: m.UID}
			if err = ledger.Observe(*completion.Local, ledger.Run(), journal.Observation{Account: "a01", Folder: "u-down", Location: loc, Decoded: body}); err != nil {
				t.Fatal(err)
			}
			if err = mailServer.Delete(ctx, m); err != nil {
				t.Fatal(err)
			}
			if err = ledger.Deleted(*completion.Local, ledger.Run(), loc); err != nil {
				t.Fatal(err)
			}
			if err = b.Cleaned(1, completion.Attempt); err != nil {
				t.Fatal(err)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
	}
	if !bytes.Equal(received.Bytes(), download) {
		t.Fatal("TCP response changed")
	}
	if err = <-destination; err != nil {
		t.Fatal(err)
	}
	fin, _ = b.Finish(1, 1)
	reply, err = a.Receive(1, authenticated(t, serverCipher, clientCipher, fin))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range reply {
		if _, err = b.Receive(1, authenticated(t, clientCipher, serverCipher, f)); err != nil {
			t.Fatal(err)
		}
	}
	if err = a.Retire(1, 1); err != nil {
		t.Fatal(err)
	}
	if err = b.Retire(1, 1); err != nil {
		t.Fatal(err)
	}
	release, err := w.Fence(offer.Key)
	if err != nil {
		t.Fatal(err)
	}
	status, err := ledger.ApplyRemote("12345678", ledger.Run(), controls(t, clientCipher, serverCipher, release))
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Acknowledge(controls(t, serverCipher, clientCipher, status).(wire.CleanupStatus)); err != nil {
		t.Fatal(err)
	}
	retiring, err := w.Retirements()
	if err != nil || len(retiring) != 1 {
		t.Fatal(retiring, err)
	}
	retired, err := ledger.RetireRemote("12345678", ledger.Run(), controls(t, clientCipher, serverCipher, retiring[0]).(wire.Retirement))
	if err != nil {
		t.Fatal(err)
	}
	if err = w.CompleteRetirement(controls(t, serverCipher, clientCipher, retired).(wire.Retirement)); err != nil {
		t.Fatal(err)
	}
	if err = ledger.CloseGrant(local.Ref); err != nil {
		t.Fatal(err)
	}
	for _, e := range []*Engine{a, b} {
		tx, rx := e.Snapshot()
		if tx.Logical != 0 || tx.PhysicalExposure != 0 || rx.Buffered != 0 {
			t.Fatal(tx, rx)
		}
		if err = e.Check(); err != nil {
			t.Fatal(err)
		}
	}
	if err = exit.Retire(1); err != nil {
		t.Fatal(err)
	}
	// Both test session owners are now fenced; all controls are obsolete.
	clientGate.Stop()
	serverGate.Stop()
	clientBootstrap.Stop()
	serverBootstrap.Stop()
	for _, sender := range senders {
		sender.Stop()
	}
	for _, p := range controlGC {
		if e := p.sender.Reclaim(ctx, p.ref, p.message); e != nil {
			t.Fatal(e)
		}
	}
	for _, j := range controlLedgers {
		rows, e := j.ScanAttempts(0, 256)
		if e != nil || len(rows) != 0 {
			t.Fatal("control quota leaked", rows, e)
		}
	}

}

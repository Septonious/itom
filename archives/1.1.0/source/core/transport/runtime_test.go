package transport_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/transport"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

type runtimeClientFixture struct {
	session  *transport.Session
	cipher   *transport.Cipher
	dialer   *transport.ClientDialer
	carriers []transport.RuntimeCarrier
	stores   map[transport.LedgerClass]*journal.Store
	wallets  map[transport.LedgerClass]*wallet.Wallet
	key      []byte
}

// Pre-established Noise/grants isolate service ownership and failure handling.
// Cold OPEN/TLS/HTTP uses the same Runtime in open_imap_test.go; neither fixture
// is authenticated production bootstrap or independent rate qualification.
func runtimeClient(t *testing.T, fast func() *transport.Executor) *runtimeClientFixture {
	t.Helper()
	f := &runtimeClientFixture{session: session(t, transport.DefaultLimits()), stores: make(map[transport.LedgerClass]*journal.Store), wallets: make(map[transport.LedgerClass]*wallet.Wallet), key: bytes.Repeat([]byte{41}, 32)}
	t.Cleanup(func() { f.session.Close() })
	ce, _ := sessionEpochs(t, wire.ID{103})
	var err error
	f.cipher, err = transport.NewCipher(ce)
	if err != nil {
		t.Fatal(err)
	}
	f.dialer, err = transport.NewClientDialer(f.session, 256<<10)
	if err != nil {
		t.Fatal(err)
	}
	for _, class := range []transport.LedgerClass{transport.DataLedger, transport.ControlLedger} {
		store := issuer(t, filepath.Join(t.TempDir(), "issuer.db"), 64<<20, 128)
		w, err := wallet.OpenBound(filepath.Join(t.TempDir(), "wallet.db"), store.StoreID(), ce)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { w.Close() })
		g, err := store.IssueRemote(journal.Scope{Account: "account1", User: "12345678", Session: wire.ID{103}}, pool.Amount{Bytes: 32 << 20, Messages: 32})
		if err != nil {
			t.Fatal(err)
		}
		offer, err := store.RemoteOffer(g.Ref)
		if err != nil {
			t.Fatal(err)
		}
		if err = w.Accept(offer); err != nil {
			t.Fatal(err)
		}
		f.stores[class], f.wallets[class] = store, w
	}
	for _, plane := range []transport.Plane{transport.Fast, transport.Bulk} {
		folder := "fast"
		if plane == transport.Bulk {
			folder = "bulk"
		}
		var x *transport.Executor
		if plane == transport.Fast && fast != nil {
			x = fast()
		} else {
			box := &sharedMailbox{}
			x = box.selected(t, folder)
		}
		t.Cleanup(func() { x.Close() })
		var publishers []*transport.ClientPublisher
		var receivers []*transport.ClientReceiver
		for _, class := range []transport.LedgerClass{transport.DataLedger, transport.ControlLedger} {
			if plane == transport.Bulk && class == transport.ControlLedger {
				continue
			}
			p, err := transport.NewClientPublisher(f.session, plane, class, f.cipher, f.wallets[class], "account1", folder, f.key)
			if err != nil {
				t.Fatal(err)
			}
			store := f.stores[class]
			r, err := transport.NewClientReceiver(f.session, class, f.cipher, transport.ServerOrigin{Store: store.StoreID(), Run: store.Run(), Account: "account1", User: "12345678", Folder: folder, UIDValidity: 9}, f.key)
			if err != nil {
				t.Fatal(err)
			}
			publishers, receivers = append(publishers, p), append(receivers, r)
		}
		i, err := transport.NewClientInbox(x, transport.DefaultInboxLimits(), receivers...)
		if err != nil {
			t.Fatal(err)
		}
		r, err := transport.NewInboxWorker(i, transport.DefaultInboxServiceLimits())
		if err != nil {
			t.Fatal(err)
		}
		w, err := transport.NewClientPublicationWorker(x, plane, publishers...)
		if err != nil {
			t.Fatal(err)
		}
		f.carriers = append(f.carriers, transport.RuntimeCarrier{Reader: r, Writer: w})
	}
	return f
}

func ownRuntime(t *testing.T, f *runtimeClientFixture) *transport.Runtime {
	t.Helper()
	r, err := transport.NewRuntime(f.session, nil, transport.RuntimeBudget{Connections: 2, PerAccount: 2}, f.carriers...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func runRuntime(t *testing.T, r *transport.Runtime) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		r.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("runtime failed to join final shutdown")
		}
	})
}

func waitRuntime(t *testing.T, r *transport.Runtime, ready func(transport.RuntimeSnapshot) bool) transport.RuntimeSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		changed, snapshot := r.Changes(), r.Snapshot()
		if ready(snapshot) {
			return snapshot
		}
		select {
		case <-changed:
		case <-ctx.Done():
			t.Fatal("runtime did not reach expected state", snapshot)
		}
	}
}

func TestRuntimeReadFailurePausesOnlyItsWriterAndPreservesOtherCarrier(t *testing.T) {
	f := runtimeClient(t, func() *transport.Executor {
		return selectedPeer(t, func(c net.Conn) {
			r := bufio.NewReader(c)
			readLine(t, r)
			fmt.Fprint(c, "* BYE carrier failed\r\n")
			io.Copy(io.Discard, c)
		})
	})
	r := ownRuntime(t, f)
	runRuntime(t, r)
	snapshot := waitRuntime(t, r, func(s transport.RuntimeSnapshot) bool {
		return s.Carriers[0].Read == transport.ServiceFailed && s.Carriers[0].Write != transport.ServiceServing && s.Carriers[0].Write != transport.ServicePrepared && s.Carriers[1].Read == transport.ServiceServing && s.Carriers[1].Write == transport.ServiceServing
	})
	if snapshot.Phase != transport.ServiceServing || snapshot.Session.Closed {
		t.Fatal("one read failure destroyed unrelated service", snapshot)
	}
	before := f.carriers[1].Reader.Stats().Turns
	waitInboxWorker(t, f.carriers[1].Reader, func(s transport.InboxStats) bool { return s.Turns >= before+2 })
	if snapshot.Carriers[0].ReadFailure != "carrier" {
		t.Fatal("failure not attributed to actual carrier", snapshot)
	}
	for _, w := range f.wallets {
		entries, err := w.Entries()
		if err != nil || len(entries) != 1 || entries[0].Used != (wire.Quota{}) {
			t.Fatal("idle carrier failure created/refunded spend", entries, err)
		}
	}
}

func TestRuntimeWriteFailureKeepsReceptionAndTheExactUnstartedCharge(t *testing.T) {
	f := runtimeClient(t, func() *transport.Executor {
		limits := transport.DefaultIMAPLimits()
		limits.LiteralBytes = 128
		return selectedAccountPeerWithLimits(t, "account1", "fast", limits, func(c net.Conn) {
			r := bufio.NewReader(c)
			for {
				if _, err := r.Peek(1); err != nil {
					return
				}
				line := readLine(t, r)
				if !strings.Contains(line, " UID FETCH ") && !strings.Contains(line, " NOOP") {
					t.Error("unstarted over-limit publication reached wire", line)
					return
				}
				fmt.Fprintf(c, "%s OK healthy\r\n", tag(line))
			}
		})
	})
	r := ownRuntime(t, f)
	runRuntime(t, r)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dialDone := make(chan error, 1)
	go func() {
		conn, err := f.dialer.DialContext(ctx, "tcp", "example.test:443")
		if conn != nil {
			conn.Close()
		}
		dialDone <- err
	}()
	waitRuntime(t, r, func(s transport.RuntimeSnapshot) bool {
		return s.Carriers[0].Write == transport.ServiceFailed && s.Carriers[0].Read == transport.ServiceServing
	})
	cancel()
	if err := <-dialDone; err == nil {
		t.Fatal("no peer OPEN_OK was manufactured")
	}
	pending := r.PendingPublication(0)
	if pending == nil || f.carriers[0].Writer.Stats().Prepared != 1 {
		t.Fatal("write failure lost original charged body")
	}
	before := f.carriers[0].Reader.Stats().Turns
	waitInboxWorker(t, f.carriers[0].Reader, func(s transport.InboxStats) bool { return s.Turns >= before+2 })
	r.Close()
	entries, err := f.wallets[transport.ControlLedger].Entries()
	if err != nil || len(entries) != 1 || entries[0].Used.Messages != 1 || len(entries[0].Pending) != 1 || r.PendingPublication(0) != pending {
		t.Fatal("runtime shutdown lost/reserved again/refunded original debt", entries, err)
	}
	if status := r.Snapshot(); status.Phase != transport.ServiceClosed || !status.Session.Closed {
		t.Fatal("final owner shutdown did not close session", status)
	}
	if err = f.stores[transport.ControlLedger].Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeCannotAdoptRunningServicesOrStartUnlistedWorkersAfterClaim(t *testing.T) {
	f := runtimeClient(t, nil)
	box := &sharedMailbox{}
	x := box.selected(t, "bulk") // explicit extra socket for this negative ownership case only
	defer x.Close()
	p, err := transport.NewClientPublisher(f.session, transport.Bulk, transport.DataLedger, f.cipher, f.wallets[transport.DataLedger], "account1", "bulk", f.key)
	if err != nil {
		t.Fatal(err)
	}
	w, err := transport.NewClientPublicationWorker(x, transport.Bulk, p)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	changed := w.Changes()
	go func() { done <- w.Run(ctx) }()
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("independent worker did not start")
	}
	if _, err = transport.NewRuntime(f.session, nil, transport.RuntimeBudget{Connections: 2, PerAccount: 2}, f.carriers...); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("runtime adopted an owner with unlisted live service", err)
	}
	cancel()
	if err = <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if f.session.Snapshot().ActiveServices != 0 {
		t.Fatal("stopped independent service leaked ownership")
	}
	r := ownRuntime(t, f)
	if err = w.Run(ctx); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("unlisted worker competed with runtime ownership", err)
	}
	r.Close()
}

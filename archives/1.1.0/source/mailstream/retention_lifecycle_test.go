package mailstream

import (
	"bytes"
	"context"
	"crypto/rand"
	"github.com/emersion/go-imap/v2/imapserver"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
)

func waitRetention(t *testing.T, ctx context.Context, check func() bool, description string) {
	t.Helper()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if check() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(description, ctx.Err())
		case <-tick.C:
		}
	}
}

// User-visible sequence: a full old ledger, retained control and partial DATA,
// an ordinary HTTPS session, hundreds of light requests, user stop, new session.
// Cleanup must exclude user mail and live DATA, then remove fenced leftovers.
func TestAutomaticControlAndAbandonedSessionCleanup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fixture := &deletionLockFixture{release: make(chan struct{})}
	accounts, _ := applicationMailServerWrapped(t, true, false, func(s imapserver.Session) imapserver.Session { return &deletionLockSession{Session: s, owner: fixture} })
	t.Cleanup(func() { close(fixture.release) })
	state := t.TempDir()
	store, e := openRetention(state, "retention-life", true)
	if e != nil {
		t.Fatal(e)
	}
	probe, e := openMailPipe(ctx, accounts[0])
	if e != nil {
		t.Fatal(e)
	}
	defer probe.close()
	oldName := "itom3-retention-life-" + "01000000000000000000000000000000" + "-c2s-00000000"
	foreign := "personal-mail-keep"
	for _, name := range []string{oldName, "itom3-retention-life-control-v6-c2s", foreign} {
		if e = probe.client.Create(name, nil).Wait(); e != nil {
			t.Fatal(e)
		}
		command, err := probe.startAppend(name, []byte("Subject: retained fixture\r\n\r\nold body"))
		if err != nil {
			t.Fatal(err)
		}
		if _, e = command.Wait(); e != nil {
			t.Fatal(e)
		}
	}
	for _, a := range accounts {
		if e = store.put(retentionKey(a), prototypePhysicalBudget); e != nil {
			t.Fatal(e)
		}
	}
	if e = store.close(); e != nil {
		t.Fatal(e)
	}
	serverState := t.TempDir()
	serverStore, e := openRetention(serverState, "retention-life", false)
	if e != nil {
		t.Fatal(e)
	}
	for _, a := range accounts {
		if e = serverStore.put(retentionKey(a), prototypePhysicalBudget); e != nil {
			t.Fatal(e)
		}
	}
	if e = serverStore.close(); e != nil {
		t.Fatal(e)
	}
	server, e := NewStoredIMAPLink(ctx, "retention-life", accounts, false, serverState)
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close()
	key := make([]byte, 32)
	rand.Read(key)
	go ServeWithOptions(ctx, server, "retention-life", key, (&net.Dialer{}).DialContext, ServerOptions{IdleTimeout: 2 * time.Second})
	client, e := NewStoredIMAPLink(ctx, "retention-life", accounts, true, state)
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if client != nil {
			client.Close()
		}
	}()
	session, e := Connect(ctx, client, "retention-life", key)
	if e != nil {
		t.Fatal("recovery bootstrap could not connect with a full old ledger", e)
	}
	defer func() {
		if session != nil {
			session.Close()
		}
	}()
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("SERVER snapshot=%+v", server.Snapshot())
			if client != nil {
				t.Logf("CLIENT snapshot=%+v", client.Snapshot())
			}
			for _, pair := range []*IMAPLink{server, client} {
				if pair == nil {
					continue
				}
				for _, a := range pair.accounts {
					a.mu.Lock()
					m := a.maintenance
					t.Logf("role_client=%v account=%s epoch=%x remote=%+v start=%+v cin=%+v cout=%+v boundary=%+v scan=%v index=%d folders=%v ready=%v client_recovered=%v spent=%d recovered=%d failure=%s", pair.client, a.config.ID, m.epoch, m.remoteRead, m.remoteStartOut, m.clearedIn, m.clearedOut, m.boundaryOut, m.inventoried, m.folderIndex, m.folders, m.recoveryReady, m.recovered, a.spent, a.recovered, m.failure)
					a.mu.Unlock()
				}
			}
		}
	})

	waitRetention(t, ctx, func() bool {
		for _, a := range client.accounts {
			a.mu.Lock()
			ready := a.maintenance.recovered && a.recovered == 0
			a.mu.Unlock()
			if !ready {
				return false
			}
		}
		return true
	}, "verified old-scope reconciliation did not finish")
	status, e := probe.client.Status(oldName, &imap.StatusOptions{NumMessages: true}).Wait()
	if e != nil || status.NumMessages == nil || *status.NumMessages != 0 {
		t.Fatal("partial old DATA was not emptied", e)
	}
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "verified small HTTPS") }))
	defer origin.Close()
	tr := origin.Client().Transport.(*http.Transport).Clone()
	defer tr.CloseIdleConnections()
	tr.DialContext = func(ctx context.Context, _, target string) (net.Conn, error) { return session.OpenTCP(ctx, target) }
	hc := &http.Client{Transport: tr, Timeout: 10 * time.Second}
	maximum := uint32(0)
	for i := 0; i < 180; i++ {
		response, err := hc.Get(origin.URL)
		if err != nil {
			t.Fatalf("HTTPS request %d: %v", i, err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || !bytes.Equal(body, []byte("verified small HTTPS")) {
			t.Fatal("HTTPS integrity", err)
		}
		if i%20 == 0 {
			for _, name := range []string{client.controlFolder(true), client.controlFolder(false)} {
				s, err := probe.client.Status(name, &imap.StatusOptions{NumMessages: true}).Wait()
				if err != nil || s.NumMessages == nil {
					t.Fatal(err)
				}
				maximum = max(maximum, *s.NumMessages)
			}
		}
	}
	if client.Snapshot().Failed != 0 || server.Snapshot().Failed != 0 {
		t.Fatal("online control EXPUNGE killed a reader")
	}
	for _, name := range []string{client.controlFolder(true), client.controlFolder(false)} {
		s, err := probe.client.Status(name, &imap.StatusOptions{NumMessages: true}).Wait()
		if err != nil || s.NumMessages == nil {
			t.Fatal(err)
		}
		maximum = max(maximum, *s.NumMessages)
	}
	if maximum > 192 {
		t.Fatalf("light requests accumulated uncleared control messages: %d", maximum)
	}
	session.Close()
	client.Close()
	session = nil
	client = nil
	tr.CloseIdleConnections()
	// Empty folders remain: DELETE can account-lock the field provider. Idle
	// control-tail collection must still work without any manual provider purge.
	waitRetention(t, ctx, func() bool {
		server.mu.Lock()
		idle := server.attached == (SessionID{})
		server.mu.Unlock()
		return idle
	}, "server did not release stopped session")
	user, e := probe.client.Status(foreign, &imap.StatusOptions{NumMessages: true}).Wait()
	if e != nil || user.NumMessages == nil || *user.NumMessages != 1 {
		t.Fatal("cleanup touched unrelated user mail", e)
	}
	for _, name := range []string{server.controlFolder(true), server.controlFolder(false)} {
		waitRetention(t, ctx, func() bool {
			s, err := probe.client.Status(name, &imap.StatusOptions{NumMessages: true}).Wait()
			return err == nil && s.NumMessages != nil && *s.NumMessages == 0
		}, "idle control tail was not automatically emptied")
	}
	if fixture.deletes.Load() != 0 || fixture.uidExpunges.Load() != 0 {
		t.Fatal("cleanup used an account-locking provider command")
	}
	client, e = NewStoredIMAPLink(ctx, "retention-life", accounts, true, state)
	if e != nil {
		t.Fatal(e)
	}
	session, e = Connect(ctx, client, "retention-life", key)
	if e != nil {
		t.Fatal("reconnect after automatic cleanup", e)
	}
	if _, e = session.Ping(ctx); e != nil {
		t.Fatal("post-cleanup round trip", e)
	}
	t.Logf("full-ledger recovery; HTTPS=180; maximum observed control messages=%d; partial DATA and idle control emptied; DELETE never attempted; unrelated mail preserved; reconnect succeeded", maximum)
}

// The first EXPUNGE deletes the messages but loses its successful outcome.
// No caller may refund on that response. A later audited retry must recover
// automatically without retiring a carrier or requiring a provider purge.
type lostCollectionResult struct {
	imapserver.Session
	once *atomic.Bool
}

func (s *lostCollectionResult) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	err := s.Session.Expunge(w, uids)
	if err == nil && s.once.CompareAndSwap(false, true) {
		return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "fixture lost collection outcome"}
	}
	return err
}
func TestCleanupRetriesUnknownOutcomeWithoutManualPurge(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var lost atomic.Bool
	accounts, _ := applicationMailServerWrapped(t, true, false, func(s imapserver.Session) imapserver.Session { return &lostCollectionResult{s, &lost} })
	probe, err := openMailPipe(ctx, accounts[0])
	if err != nil {
		t.Fatal(err)
	}
	defer probe.close()
	folder := "itom3-retry-life-control-v6-c2s"
	if err = probe.client.Create(folder, nil).Wait(); err != nil {
		t.Fatal(err)
	}
	cmd, err := probe.startAppend(folder, []byte("Subject: old control\r\n\r\nretained"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	server, err := NewStoredIMAPLink(ctx, "retry-life", accounts, false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := NewStoredIMAPLink(ctx, "retry-life", accounts, true, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	key := make([]byte, 32)
	rand.Read(key)
	go Serve(ctx, server, "retry-life", key, (&net.Dialer{}).DialContext, nil)
	session, err := Connect(ctx, client, "retry-life", key)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	waitRetention(t, ctx, func() bool { return server.Snapshot().Reclamation.Paused }, "lost outcome was not quarantined")
	before := server.Snapshot().Reclamation
	if before.RetryAtMS <= time.Now().UnixMilli() {
		t.Fatal("failure has no future retry", before)
	}
	waitRetention(t, ctx, func() bool {
		server.accounts[0].mu.Lock()
		done := server.accounts[0].maintenance.recoveryReady
		server.accounts[0].mu.Unlock()
		return done && !server.Snapshot().Reclamation.Paused
	}, "automatic audited recovery never resumed")
	status, err := probe.client.Status(folder, &imap.StatusOptions{NumMessages: true}).Wait()
	if err != nil || status.NumMessages == nil || *status.NumMessages != 0 {
		t.Fatal("retry failed to verify empty folder", err)
	}
	if _, err = session.Ping(ctx); err != nil {
		t.Fatal("post-retry round trip", err)
	}
	if client.Snapshot().Failed != 0 || server.Snapshot().Failed != 0 {
		t.Fatal("optional cleanup killed an account")
	}
	t.Log("unknown EXPUNGE result quarantined; bounded retry audited emptiness and resumed; all carriers and round trip healthy")
}

func TestRecoveryReserveCannotCarryOrdinaryTraffic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var id SessionID
	id[0] = 9
	a := &mailAccountState{ctx: ctx, ready: true, spent: prototypePhysicalBudget, fast: make(chan mailWork, 4), bulk: make(chan mailWork, 4), config: MailAccount{ID: "a01"}}
	l := &IMAPLink{ctx: ctx, accounts: []*mailAccountState{a}, changed: make(chan struct{})}
	a.link = l
	body := header(sealed, id, 1)
	if _, err := l.sendClass(ctx, body, true, nil, false); err != ErrCapacity {
		t.Fatal("ordinary traffic borrowed recovery-only capacity", err)
	}
	if _, err := l.sendClass(ctx, body, true, nil, true); err != nil {
		t.Fatal("small authenticated housekeeping could not recover a full ledger", err)
	}
	work := <-a.fast
	if !work.recovery {
		t.Fatal("recovery work lost its accounting class")
	}
	a.mu.Lock()
	err := a.chargeLocked(maximumRetentionBudget - a.spent)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.sendClass(ctx, body, true, nil, true); err != ErrCapacity {
		t.Fatal("recovery exceeded finite hard bound", err)
	}
}

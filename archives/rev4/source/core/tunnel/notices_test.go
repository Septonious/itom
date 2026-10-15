package tunnel

import (
	"context"
	"fmt"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
	"path/filepath"
	"testing"
	"time"
)

func TestServerNoticeBroadcastAcrossSessions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg, err := servercfg.New("message-test")
	if err != nil {
		t.Fatal(err)
	}
	folders := []string{}
	for i := 0; i < 2; i++ {
		u, err := cfg.AddUser(fmt.Sprintf("client-%d", i))
		if err != nil {
			t.Fatal(err)
		}
		folders = append(folders, u.Up, u.Down)
	}
	seedClient, seedServer := tlsMailPairWithNetwork(t, ctx, folders, newProviderTestNetwork(0), "a01")
	rawDial := seedClient.(*laneMail).dial
	seedClient.(*laneMail).Close()
	seedServer.(*laneMail).Close()
	dial := func(ctx context.Context, _ mailbox.Config, _ []string) (*mailbox.Client, error) { return rawDial(ctx) }
	cfg.Accounts = []servercfg.Account{{ID: "a01", Mail: mailbox.Config{Host: "mail.example", Port: 993, TLS: "tls", Username: "test", Password: "secret"}}}
	if err = cfg.SetNotice("Initial broadcast"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	db := filepath.Join(root, "server.db")
	if err = servercfg.Update(db, func(out *servercfg.Config) error { *out = cfg; return nil }); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	serverDir := filepath.Join(root, "tunnel-server")
	go func() {
		done <- serveWithOptions(ctx, cfg, serverDir, func(string) {}, serverOptions{dial: dial, readNotice: noticeSource(serverDir)})
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("server did not stop")
		}
	}()
	clients := []*Client{}
	for _, u := range cfg.Users {
		c, err := StartClient(ctx, servercfg.Profile{Schema: 1, Server: cfg.Server, Provider: cfg.Provider, User: u, Accounts: cfg.Accounts}, t.TempDir(), dial)
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, c)
		defer c.Close()
	}
	wait := func(revision uint64, text string) {
		t.Helper()
		deadline := time.Now().Add(12 * time.Second)
		for time.Now().Before(deadline) {
			matches := 0
			for _, c := range clients {
				s := c.Snapshot()
				if s.ServerMessageRevision == revision && s.ServerMessage == text {
					matches++
				}
			}
			if matches == len(clients) {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		for _, c := range clients {
			t.Logf("client state: %+v", c.Snapshot())
		}
		t.Fatal("announcement was not broadcast to every registered client session")
	}
	wait(1, "Initial broadcast")
	if err = servercfg.Update(db, func(c *servercfg.Config) error { return c.SetNotice("Updated broadcast") }); err != nil {
		t.Fatal(err)
	}
	wait(2, "Updated broadcast")
}

func TestServerNoticeDeduplicationAndRevisionOrdering(t *testing.T) {
	var rx noticeReceiver
	for _, n := range []wire.Notice{{Revision: 1, Text: "first"}, {Revision: 1, Text: "first"}, {Revision: 2, Text: "second"}, {Revision: 1, Text: "first"}} {
		if err := rx.accept(n); err != nil {
			t.Fatal(err)
		}
	}
	if rx.snapshot().Text != "second" {
		t.Fatal("old message replaced current message")
	}
	if rx.accept(wire.Notice{Revision: 2, Text: "conflict"}) == nil {
		t.Fatal("mutable revision accepted")
	}
	var tx noticeDelivery
	now := time.Now()
	n := wire.Notice{Revision: 1, Text: "first"}
	if _, ok := tx.prepare(n, now); !ok {
		t.Fatal("initial message not sent")
	}
	if _, ok := tx.prepare(n, now.Add(time.Second)); ok {
		t.Fatal("unbounded repeat")
	}
	tx.acknowledge(999)
	if _, ok := tx.prepare(n, now.Add(6*time.Second)); !ok {
		t.Fatal("unsent acknowledgment suppressed retry")
	}
	tx.acknowledge(1)
	if _, ok := tx.prepare(n, now.Add(time.Hour)); ok {
		t.Fatal("acknowledged message resent")
	}
	if _, ok := tx.prepare(wire.Notice{Revision: 2, Text: ""}, now.Add(7*time.Second)); !ok {
		t.Fatal("clear update suppressed")
	}
	tx.acknowledge(1)
	if _, ok := tx.prepare(wire.Notice{Revision: 2, Text: ""}, now.Add(13*time.Second)); !ok {
		t.Fatal("old ACK suppressed new revision")
	}
}
func TestServerNoticeLiveUpdateWithoutRestart(t *testing.T) {
	var db string
	f := newProductionSupervisorFixtureState(t, 2, func(serverDir, _ string, c servercfg.Config, _ servercfg.User) {
		db = filepath.Join(filepath.Dir(serverDir), "server.db")
		if err := c.SetNotice("Initial notice"); err != nil {
			t.Fatal(err)
		}
		if err := servercfg.Update(db, func(out *servercfg.Config) error { *out = c; return nil }); err != nil {
			t.Fatal(err)
		}
	})
	wait := func(revision uint64, text string) {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			s := f.client.Snapshot()
			if s.ServerMessageRevision == revision && s.ServerMessage == text {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("server notice did not arrive", revision, f.client.Snapshot().ServerMessage)
	}
	wait(1, "Initial notice")
	logical, generation := f.client.logicalSession, f.client.generation
	if err := servercfg.Update(db, func(c *servercfg.Config) error { return c.SetNotice("Updated notice") }); err != nil {
		t.Fatal(err)
	}
	wait(2, "Updated notice")
	if err := servercfg.Update(db, func(c *servercfg.Config) error { return c.SetNotice("") }); err != nil {
		t.Fatal(err)
	}
	wait(3, "")
	if f.client.logicalSession != logical || f.client.generation != generation {
		t.Fatal("message update restarted transport")
	}
}

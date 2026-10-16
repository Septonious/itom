package tunnel

import (
	"context"
	"fmt"
	"testing"
	"time"

	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

// The field trace had four successful server logins, no client login, 41 empty
// SELECT polls and reconnect at ~16s. Retain four real TLS IMAP connections for
// 17s without a client, bound SELECT traffic, then authenticate on those SAME
// sockets. This checks startup, not Mail.ru speed or a claimed rate-limit value.
func TestFieldFourAccountsWaitForDelayedClientWithoutSelectFlood(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg, err := servercfg.New("startup")
	if err != nil {
		t.Fatal(err)
	}
	u, err := cfg.AddUser("startup")
	if err != nil {
		t.Fatal(err)
	}
	servers := make([]*laneMail, 4)
	clients := make([]*lane, 4)
	done := make(chan error, 8)
	before := make([]uint64, 4)
	for i := range servers {
		cm, sm := tlsMailPair(t, ctx, []string{u.Up, u.Down})
		servers[i] = sm.(*laneMail)
		before[i], _, _ = servers[i].ReaderCommandCounts()
		a := servercfg.Account{ID: fmt.Sprintf("a%02d", i+1)}
		s := &lane{ctx: ctx, mail: sm, side: true, user: u, account: a, recvFolder: u.Up, sendFolder: u.Down}
		clients[i] = &lane{ctx: ctx, mail: cm, user: u, account: a, recvFolder: u.Down, sendFolder: u.Up, logicalSession: wire.ID{1}, generation: 1, carrierEpoch: uint64(i + 1)}
		go func() { done <- s.handshake(cfg.Server) }()
	}
	if err := pause(ctx, 17*time.Second); err != nil {
		t.Fatal(err)
	}
	for i, m := range servers {
		selects, fetches, _ := m.ReaderCommandCounts()
		if selects-before[i] > 12 {
			t.Errorf("account%d idle SELECT flood: %d", i+1, selects-before[i])
		}
		t.Logf("account%d wait_s=17 select_commands=%d fetch_commands=%d", i+1, selects-before[i], fetches)
	}
	select {
	case err := <-done:
		t.Fatal("server stopped before client", err)
	default:
	}
	for _, c := range clients {
		go func() { done <- c.handshake(cfg.Server) }()
	}
	for range 8 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("delayed client handshake timeout")
		}
	}
}

package transport_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"itom.local/core/journal"
	"itom.local/core/transport"
	"net"
	"testing"
)

func rejectUnexpectedIO(t *testing.T) func(net.Conn) {
	t.Helper()
	return func(c net.Conn) {
		var b [1]byte
		if n, _ := c.Read(b[:]); n != 0 {
			t.Error("rejected physical authority issued an IMAP command")
		}
	}
}

func TestPreparedPublicationRequiresBoundAccountAndPlaneBeforeAnyIO(t *testing.T) {
	for _, direction := range []string{"client", "server"} {
		t.Run(direction, func(t *testing.T) {
			var p *transport.PreparedPublication
			var s *transport.Session
			var local *serverFixture
			if direction == "client" {
				f := publications(t, 1<<20, 16)
				p = prepare(t, f, []byte("one immutable request"))
				s = f.sender
			} else {
				local = serverPublications(t, transport.Fast, 1<<20)
				p = serverBody(t, local, []byte("one immutable response"))
				s = local.server
			}
			for _, scope := range []struct{ account, folder string }{{"", "fast"}, {"account2", "fast"}, {"account1", "bulk"}} {
				x := selectedAccountPeer(t, scope.account, scope.folder, rejectUnexpectedIO(t))
				if outcome, err := p.Start(context.Background(), x); outcome != transport.NotStarted || !errors.Is(err, transport.ErrProtocol) {
					t.Fatal("unscoped/misrouted body acquired writer", outcome, err)
				}
				x.Close()
			}
			if s.Snapshot().ActiveDispatches != 1 {
				t.Fatal("rejected route erased prepared work")
			}
			correct := selectedPeer(t, func(c net.Conn) {
				line, body := readAppendBody(t, c, bufio.NewReader(c))
				if len(body) != len(p.MIME()) {
					t.Error("reusing prepared permission changed its literal")
				}
				fmt.Fprintf(c, "%s OK stored\r\n", tag(line))
			})
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if outcome, err := p.Start(ctx, correct); outcome != transport.NotStarted || !errors.Is(err, context.Canceled) {
				t.Fatal("unstarted cancel lost prepared permission", outcome, err)
			}
			if outcome, err := p.Start(context.Background(), correct); outcome != transport.Accepted || err != nil {
				t.Fatal("correct bound account could not publish retained body", outcome, err)
			}
			if local != nil {
				ref, _ := p.ServerAttempt()
				if a, err := local.store.GetAttempt(ref); err != nil || a.State != journal.Committed {
					t.Fatal("accepted server outcome not durably recorded", a, err)
				}
				if held, err := local.store.Held(local.scope.Account, local.scope.User); err != nil || held.Messages != 1 || held.Bytes != uint64(len(p.MIME())) {
					t.Fatal("rejected routes/cancel multiplied or refunded debt", held, err)
				}
			}
		})
	}
}

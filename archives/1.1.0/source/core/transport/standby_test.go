package transport

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func standbyPeer(t *testing.T) (*Executor, net.Conn) {
	t.Helper()
	client, peer := net.Pipe()
	l := DefaultIMAPLimits()
	l.ProgressTimeout = 900 * time.Millisecond
	x, err := NewSelectedExecutor(client, bufio.NewReader(client), l)
	if err != nil {
		t.Fatal(err)
	}
	x.account = "a02"
	t.Cleanup(func() { x.Close(); peer.Close() })
	return x, peer
}

func TestStandbyHandoffDrainsItsNoopWithoutAdmittingPublicCommands(t *testing.T) {
	x, peer := standbyPeer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	g := startStandby(ctx, cancel, []RosterLane{{Executor: x, Plane: Fast}}, nil)
	defer g.requestStop()
	r := bufio.NewReader(peer)
	line, err := r.ReadString('\n')
	if err != nil || !strings.HasSuffix(line, " NOOP\r\n") {
		t.Fatal("standby must issue only NOOP", line, err)
	}
	x.mu.Lock()
	allowed := len(x.pending) == 1 && x.onlyStandbyPendingLocked()
	x.mu.Unlock()
	if !allowed {
		t.Fatal("private NOOP did not retain its ownership marker")
	}
	g.requestStop()
	stopped := make(chan error, 1)
	go func() { stopped <- g.stop(ctx) }()
	select {
	case err = <-stopped:
		t.Fatal("handoff abandoned an issued tag", err)
	default:
	}
	fmt.Fprintf(peer, "%s OK alive\r\n", strings.Fields(line)[0])
	if err = <-stopped; err != nil {
		t.Fatal(err)
	}
	s := x.Snapshot()
	if s.Closed || s.PendingTags != 0 || s.Noop.Completed != 1 || s.Append.Started != 0 || s.Fetch.Started != 0 || s.Store.Started != 0 {
		t.Fatal("keepalive changed body ownership or left a command behind", s)
	}
	command := make(chan *Command, 1)
	go func() { c, _ := x.Noop(ctx); command <- c }()
	line, err = r.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	x.mu.Lock()
	allowed = x.onlyStandbyPendingLocked()
	x.mu.Unlock()
	if allowed {
		t.Fatal("ordinary public NOOP bypassed roster ownership")
	}
	fmt.Fprintf(peer, "%s OK alive\r\n", strings.Fields(line)[0])
	if _, err = (<-command).Wait(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestStandbyReportsClosedNonanchorAndCancelsWaitingOwner(t *testing.T) {
	x, peer := standbyPeer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g := startStandby(ctx, cancel, []RosterLane{{Executor: x, Plane: Bulk}}, nil)
	peer.Close()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("standing owner kept waiting after a required lane closed")
	}
	<-g.done
	if !errors.Is(g.failure(), ErrSelectedLaneClosed) {
		t.Fatal("closed lane was mislabeled as a roster protocol violation", g.failure())
	}
}

func TestCanceledStandbyJoinsIssuedNoopAfterExecutorOwnerCloses(t *testing.T) {
	x, peer := standbyPeer(t)
	ctx, cancel := context.WithCancel(context.Background())
	g := startStandby(ctx, cancel, []RosterLane{{Executor: x, Plane: Bulk}}, nil)
	if _, err := bufio.NewReader(peer).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	cancel()
	g.requestStop()
	x.Close()
	select {
	case <-g.done:
	case <-time.After(time.Second):
		t.Fatal("cancellation stranded the keepalive waiter")
	}
	if g.failure() != nil || x.Snapshot().PendingTags != 0 {
		t.Fatal("intentional shutdown was reported as provider failure", g.failure())
	}
}

func TestCarrierCloseDiagnosticsKeepFirstCauseWithoutProviderText(t *testing.T) {
	for _, reason := range []string{"peer_eof", "provider_bye", "command_timeout", "owner_closed"} {
		t.Run(reason, func(t *testing.T) {
			x, peer := standbyPeer(t)
			command := make(chan *Command, 1)
			go func() { c, _ := x.Noop(context.Background()); command <- c }()
			if _, err := bufio.NewReader(peer).ReadString('\n'); err != nil {
				t.Fatal(err)
			}
			c := <-command
			switch reason {
			case "peer_eof":
				peer.Close()
			case "provider_bye":
				fmt.Fprint(peer, "* BYE secret-provider-text-private.example\r\n")
			case "owner_closed":
				x.Close()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err := c.Wait(ctx)
			want := ErrUnknown
			if reason == "owner_closed" {
				want = ErrClosed
			}
			if !errors.Is(err, want) {
				t.Fatal("close diagnostics changed publication outcome semantics", err)
			}
			x.Close()
			s := x.Snapshot()
			if s.CloseReason != reason || !s.Closed || s.PendingTags != 0 || len(s.CloseCommands) != 1 || s.CloseCommands[0] != "noop" {
				t.Fatal("owner cleanup overwrote original fixed close reason", s)
			}
		})
	}
}

package mailbox

import (
	"context"
	"errors"
	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestObservedIdleEOFRejectsAppendBeforePublication(t *testing.T) {
	peer := make(chan *imapserver.Conn, 1)
	cfg, tc := testServerWithCaps(t, "tls", imap.CapSet{imap.CapIMAP4rev1: {}}, nil, func(c *imapserver.Conn) { peer <- c })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := dial(ctx, cfg, []string{"itom-idle"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// Simulate transport closure while no APPEND exists. The real IMAP
	// decoder must observe it before admission; no mock Ready flag is used.
	_ = (<-peer).NetConn().Close()
	select {
	case <-c.c.Closed():
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	before := c.conn.writeBytes.Load()
	raw, _ := Encode([]byte("never published"))
	err = c.Append(ctx, "itom-idle", raw)
	if !errors.Is(err, ErrNotStarted) || errors.Is(err, ErrUnknown) || c.Ready() || c.conn.writeBytes.Load() != before {
		t.Fatalf("dead idle socket attempted publication: %v %+v", err, c.SocketSnapshot("data"))
	}
}

func TestFirstSocketFailureSurvivesLaterClosedErrors(t *testing.T) {
	a, b := net.Pipe()
	c := &cappedConn{Conn: a}
	c.left.Store(32)
	_ = b.Close()
	_, _ = c.Read(make([]byte, 1))
	_, _ = c.Write([]byte("x"))
	c.noteClose("local", "canceled")
	if f := c.firstClose.Load(); f == nil || f.Stage != "read" || f.Reason != "eof" {
		t.Fatalf("first cause overwritten: %+v", f)
	}
	_ = a.Close()
}

type lostAppendReplySession struct {
	imapserver.Session
	accepted func()
}

func (s lostAppendReplySession) Append(folder string, literal imap.LiteralReader, options *imap.AppendOptions) (*imap.AppendData, error) {
	data, err := s.Session.Append(folder, literal, options)
	if err == nil {
		s.accepted()
	}
	return data, err
}

func TestAcceptedAppendWithLostTaggedReplyStaysUnknownAndIsNotResent(t *testing.T) {
	var target atomic.Pointer[imapserver.Conn]
	var accepted atomic.Int32
	cfg, tc := testServerWithCaps(t, "tls", imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}}, func(session imapserver.Session) imapserver.Session {
		return lostAppendReplySession{session, func() {
			accepted.Add(1)
			if c := target.Load(); c != nil {
				_ = c.NetConn().Close()
			}
		}}
	}, func(peer *imapserver.Conn) { target.Store(peer) })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := dial(ctx, cfg, []string{"itom-loss"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.Ensure(ctx, "itom-loss"); err != nil {
		t.Fatal(err)
	}
	raw, _ := Encode([]byte("accepted publication"))
	err = c.Append(ctx, "itom-loss", raw)
	if !errors.Is(err, ErrUnknown) || errors.Is(err, ErrNotStarted) {
		t.Fatal("accepted publication lost unknown accounting", err)
	}
	observer, err := dial(ctx, cfg, []string{"itom-loss"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	rows, _, err := observer.Page(ctx, "itom-loss", Cursor{})
	if err != nil || len(rows) != 1 || accepted.Load() != 1 {
		t.Fatal("accepted publication absent or resent", len(rows), accepted.Load(), err)
	}
}

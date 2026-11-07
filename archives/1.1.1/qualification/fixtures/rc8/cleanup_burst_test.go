package mailstream

import (
	"bytes"
	"context"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

type expungeDuringUIDStore struct{ imapserver.Session }

func (s *expungeDuringUIDStore) Store(w *imapserver.FetchWriter, set imap.NumSet, f *imap.StoreFlags, o *imap.StoreOptions) error {
	if e := s.Session.Store(w, set, f, o); e != nil {
		return e
	}
	// UID STORE is allowed to return unsolicited EXPUNGE updates before its
	// tagged completion. Model another provider cleanup removing the messages.
	time.Sleep(5 * time.Millisecond)
	return s.Session.Expunge(&imapserver.ExpungeWriter{}, nil)
}

// A clean fixture never exceeded the streaming reply buffer. An updated real
// account can have hundreds of retained control messages before the next VPN
// starts. They must be collected and the borrowed writer must still close.
func TestRetainedControlCleanupWithEarlyExpungeBurst(t *testing.T) {
	accounts, _ := applicationMailServerWrapped(t, true, false, func(s imapserver.Session) imapserver.Session { return &expungeDuringUIDStore{s} })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, e := openMailPipe(ctx, accounts[0])
	if e != nil {
		t.Fatal(e)
	}
	folder := "itom3-cleanupburst-control-v7-c2s"
	if e = p.client.Create(folder, nil).Wait(); e != nil {
		t.Fatal(e)
	}
	raw := encodeMail(packMail([][]byte{bytes.Repeat([]byte{0x41}, 64)}))
	for i := 0; i < 300; i++ {
		cmd, e := p.startAppend(folder, raw)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = cmd.Wait(); e != nil {
			t.Fatal(e)
		}
	}
	p.allowExpunge.Store(true)
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		proof, n, e := p.collectPrefix(NewReclamationCoordinator(), folder, uidWatermark{}, true)
		if e == nil && (proof.UID != 300 || n != 300) {
			e = ErrProtocol
		}
		done <- e
	}()
	select {
	case e = <-done:
		if e != nil {
			t.Fatal("retained control cleanup", e)
		}
	case <-time.After(time.Second):
		// Do not defer an old Close which reproduces the decoder deadlock.
		p.conn.NetConn().Close()
		t.Fatal("retained control cleanup did not finish after 300 early EXPUNGE replies")
	}
	closeDone := make(chan struct{})
	go func() { p.close(); close(closeDone) }()
	select {
	case <-closeDone:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("cleanup writer did not close promptly")
	}
	t.Logf("audited 300 retained messages and closed writer in %s", time.Since(start))
}

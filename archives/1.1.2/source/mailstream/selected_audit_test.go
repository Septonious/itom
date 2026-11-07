package mailstream

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

// STATUS on a selected mailbox is discouraged by IMAP and is not a portable
// post-EXPUNGE fence. LOGIN, SELECT, APPEND and ordinary EXPUNGE still work.
type selectedStatusRefusal struct {
	imapserver.Session
	selected               string
	refusals               *atomic.Int32
	afterMutation, mutated bool
}

func (s *selectedStatusRefusal) Select(name string, opts *imap.SelectOptions) (*imap.SelectData, error) {
	d, err := s.Session.Select(name, opts)
	if err == nil {
		s.selected = name
	}
	return d, err
}
func (s *selectedStatusRefusal) Status(name string, opts *imap.StatusOptions) (*imap.StatusData, error) {
	if name == s.selected && (!s.afterMutation || s.mutated) {
		s.refusals.Add(1)
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Text: "selected mailbox STATUS unsupported in fixture"}
	}
	return s.Session.Status(name, opts)
}

func (s *selectedStatusRefusal) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	err := s.Session.Expunge(w, uids)
	if err == nil {
		s.mutated = true
	}
	return err
}

func TestReclamationAuditsSelectedMailboxWithoutStatus(t *testing.T) {
	var refusals atomic.Int32
	accounts, _ := applicationMailServerWrapped(t, true, false, func(s imapserver.Session) imapserver.Session {
		return &selectedStatusRefusal{Session: s, refusals: &refusals}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, err := openMailPipe(ctx, accounts[0])
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	folder := "itom3-selected-audit-data"
	if err = p.client.Create(folder, nil).Wait(); err != nil {
		t.Fatal(err)
	}
	rec := &segmentRecord{folder: folder}
	for range segmentAppends {
		cmd, e := p.startAppend(folder, []byte("Subject: bounded data\r\n\r\nowned"))
		if e != nil {
			t.Fatal(e)
		}
		d, e := cmd.Wait()
		if e != nil {
			t.Fatal(e)
		}
		rec.uids.AddNum(d.UID)
		rec.validity = d.UIDValidity
	}
	stop := p.limit(2 * time.Second)
	defer stop()
	if err = p.reclaim(cleanupJob{record: rec}, NewReclamationCoordinator()); err != nil {
		t.Fatal("audited collection failed although SELECT and EXPUNGE work", err)
	}
	if refusals.Load() != 0 {
		t.Fatalf("used selected STATUS %d times", refusals.Load())
	}
	box, err := p.client.Select(folder, nil).Wait()
	if err != nil || box.NumMessages != 0 {
		t.Fatalf("deletion not proved: %+v %v", box, err)
	}
}

func TestRepeatedHTTPSExceedsPhysicalBudgetWithPortableCollection(t *testing.T) {
	var refusals atomic.Int32
	accounts, _ := applicationMailServerWrapped(t, true, false, func(s imapserver.Session) imapserver.Session {
		return &selectedStatusRefusal{Session: s, refusals: &refusals, afterMutation: true}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	server, err := NewStoredIMAPLink(ctx, "sustained-collection", accounts, false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := NewStoredIMAPLink(ctx, "sustained-collection", accounts, true, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	key := bytes.Repeat([]byte{5}, 32)
	go Serve(ctx, server, "sustained-collection", key, (&net.Dialer{}).DialContext, nil)
	session, err := Connect(ctx, client, "sustained-collection", key)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	payload := make([]byte, 16<<20)
	if _, err = rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body, e := io.ReadAll(r.Body)
			if e != nil || !bytes.Equal(body, payload) {
				http.Error(w, "upload integrity", 500)
				return
			}
			io.WriteString(w, "verified")
			return
		}
		w.Write(payload)
	}))
	defer origin.Close()
	tr := origin.Client().Transport.(*http.Transport).Clone()
	tr.ForceAttemptHTTP2 = false
	tr.DialContext = func(ctx context.Context, _, target string) (net.Conn, error) { return session.OpenTCP(ctx, target) }
	defer tr.CloseIdleConnections()
	h := &http.Client{Transport: tr, Timeout: 20 * time.Second}
	for round := 0; round < 8; round++ {
		for _, upload := range []bool{true, false} {
			var response *http.Response
			if upload {
				response, err = h.Post(origin.URL, "application/octet-stream", bytes.NewReader(payload))
			} else {
				response, err = h.Get(origin.URL)
			}
			if err != nil {
				t.Fatalf("round%d upload=%v failed while collection should release capacity: %v", round, upload, err)
			}
			body, e := io.ReadAll(response.Body)
			response.Body.Close()
			want := payload
			if upload {
				want = []byte("verified")
			}
			if e != nil || response.StatusCode != 200 || !bytes.Equal(body, want) {
				t.Fatalf("round%d upload=%v integrity failed: %v bytes=%d", round, upload, e, len(body))
			}
		}
	}
	for _, link := range []*IMAPLink{client, server} {
		d := link.Snapshot()
		if d.Failed != 0 || d.Ready != len(accounts) || d.Retired == 0 {
			t.Fatalf("sustained transfer lost healthy roles or never reclaimed storage: %+v", d)
		}
		for _, line := range d.Lines {
			if line.RetainedBytes > maximumRetentionBudget {
				t.Fatal("physical budget exceeded", line)
			}
		}
	}
	if refusals.Load() != 0 {
		t.Fatal("used incompatible selected STATUS after mutation")
	}
	t.Log("128 MiB upload and128 MiB download verified across2 physical accounts; finite128 MiB aggregate storage reused by audited collection")
}

package mailstream

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

type downloadBatchObserver struct {
	imapserver.Session
	selected       string
	calls, largest *atomic.Int32
}

func (s *downloadBatchObserver) Select(folder string, opts *imap.SelectOptions) (*imap.SelectData, error) {
	d, e := s.Session.Select(folder, opts)
	if e == nil {
		s.selected = folder
	}
	return d, e
}
func (s *downloadBatchObserver) Fetch(w *imapserver.FetchWriter, set imap.NumSet, opts *imap.FetchOptions) error {
	if strings.Contains(s.selected, "-s2c-") && opts != nil && len(opts.BodySection) > 0 {
		s.calls.Add(1)
		if seq, ok := set.(imap.SeqSet); ok {
			var rows int32
			for _, r := range seq {
				if r.Start > 0 && r.Stop >= r.Start {
					rows += int32(r.Stop - r.Start + 1)
				}
			}
			for old := s.largest.Load(); rows > old; old = s.largest.Load() {
				if s.largest.CompareAndSwap(old, rows) {
					break
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return s.Session.Fetch(w, set, opts)
}

// Exercise attachment and the real TLS reader against a pre-existing backlog.
// A delayed body command must amortize download work just as upload work does;
// the subsequent single container must not wait for a full batch to arrive.
func TestClientDownloadAmortizesDelayedFetchWithoutWaitingForBatch(t *testing.T) {
	var calls, largest atomic.Int32
	accounts, _ := applicationMailServerWrapped(t, false, false, func(s imapserver.Session) imapserver.Session {
		return &downloadBatchObserver{Session: s, calls: &calls, largest: &largest}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	link, e := NewStoredIMAPLink(ctx, "download-batch", accounts[:1], true, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer link.Close()
	id := SessionID{19}
	p, e := openMailPipe(ctx, accounts[0])
	if e != nil {
		t.Fatal(e)
	}
	defer p.close()
	folder := link.segmentFolder(id, false, 0)
	if e = p.client.Create(folder, nil).Wait(); e != nil {
		t.Fatal(e)
	}
	appendPacket := func(n uint64) {
		t.Helper()
		cmd, e := p.startAppend(folder, encodeMail(header(sealed, id, n)))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = cmd.Wait(); e != nil {
			t.Fatal(e)
		}
	}
	for n := uint64(1); n <= 48; n++ {
		appendPacket(n)
	}
	if e = link.Attach(ctx, id); e != nil {
		t.Fatal(e)
	}
	readPacket := func(n uint64) {
		t.Helper()
		select {
		case got := <-link.Receive():
			if !bytes.Equal(got, header(sealed, id, n)) {
				t.Fatalf("download container %d changed or arrived out of order", n)
			}
		case <-ctx.Done():
			t.Fatalf("download container %d missing: %v", n, ctx.Err())
		}
	}
	for n := uint64(1); n <= 48; n++ {
		readPacket(n)
	}
	if calls.Load() > 2 || largest.Load() <= 8 {
		t.Fatalf("download still pays a slow FETCH for each tiny batch: calls=%d largest=%d", calls.Load(), largest.Load())
	}
	appendPacket(49)
	readPacket(49)
	t.Logf("48 backlog containers in %d FETCH commands; isolated tail delivered without waiting", calls.Load()-1)
}

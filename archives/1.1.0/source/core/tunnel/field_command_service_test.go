package tunnel

import (
	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"time"
)

// Provider-processing sensitivity, not a measured server fault or a calibrated
// replay. Transport propagation remains independently shaped at each endpoint.
type fieldCommandSession struct{ delayedSession }

func (s fieldCommandSession) Append(f string, r imap.LiteralReader, o *imap.AppendOptions) (*imap.AppendData, error) {
	time.Sleep(50 * time.Millisecond)
	return s.Session.Append(f, r, o)
}
func (s fieldCommandSession) Fetch(w *imapserver.FetchWriter, n imap.NumSet, o *imap.FetchOptions) error {
	time.Sleep(10 * time.Millisecond)
	return s.Session.Fetch(w, n, o)
}
func (s fieldCommandSession) Store(w *imapserver.FetchWriter, n imap.NumSet, f *imap.StoreFlags, o *imap.StoreOptions) error {
	time.Sleep(25 * time.Millisecond)
	return s.Session.Store(w, n, f, o)
}
func (s fieldCommandSession) Expunge(w *imapserver.ExpungeWriter, n *imap.UIDSet) error {
	time.Sleep(25 * time.Millisecond)
	return s.Session.Expunge(w, n)
}

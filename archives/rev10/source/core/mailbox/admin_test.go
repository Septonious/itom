package mailbox

import (
	"context"
	"errors"
	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"strings"
	"testing"
	"time"
)

type protectedSession struct {
	imapserver.Session
	denied bool
}

func (s *protectedSession) Delete(name string) error {
	if name == "EmptyFirst" {
		status, err := s.Session.Status(name, &imap.StatusOptions{NumMessages: true})
		if err != nil {
			return err
		}
		if *status.NumMessages != 0 {
			return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "empty first"}
		}
	}
	if name == "Protected" || name == "Denied" || name == "INBOX" {
		return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "protected folder"}
	}
	// Require deepest-first deletion for a hierarchy.
	if name == "parent" {
		_, err := s.Session.Select("parent/child", nil)
		if err == nil {
			return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "children remain"}
		}
	}
	return s.Session.Delete(name)
}
func (s *protectedSession) Select(name string, opt *imap.SelectOptions) (*imap.SelectData, error) {
	if name == "Denied" && s.denied {
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Text: "access denied"}
	}
	return s.Session.Select(name, opt)
}

func TestAdminPurgeAllAndProtectedInbox(t *testing.T) {
	for _, mode := range []string{"tls", "starttls"} {
		t.Run(mode, func(t *testing.T) {
			cfg, tc := testServerWithSession(t, mode, false, func(s imapserver.Session) imapserver.Session { return &protectedSession{Session: s} })
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, err := dialMode(ctx, cfg, nil, tc, true)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			a := &Admin{client: c}
			for _, name := range []string{"INBOX", "parent", "parent/child", "Архив 2026", "Protected", "EmptyFirst"} {
				if err := c.c.Create(name, nil).Wait(); err != nil {
					t.Fatal(err)
				}
				cmd := c.c.Append(name, 4, nil)
				_, err = cmd.Write([]byte("mail"))
				if err != nil {
					t.Fatal(err)
				}
				if err = cmd.Close(); err != nil {
					t.Fatal(err)
				}
				if _, err = cmd.Wait(); err != nil {
					t.Fatal(err)
				}
			}
			report, err := a.PurgeAll(ctx)
			if err != nil || !report.Verified || len(report.Remaining) != 2 {
				t.Fatalf("%+v %v", report, err)
			}
			for _, name := range report.Remaining {
				if name != "INBOX" && name != "Protected" {
					t.Fatal(name)
				}
				s, err := c.c.Select(name, nil).Wait()
				if err != nil || s.NumMessages != 0 {
					t.Fatal(s, err)
				}
			}
			for _, item := range report.Items {
				if item.Name == "parent" && item.Outcome != "DELETED" && item.Outcome != "UNSUBSCRIBE_REFUSED" {
					t.Fatal("wrong deletion order", report)
				}
			}
			// A cancelled pass must not modify the remaining folders.
			cancelled, stop := context.WithCancel(ctx)
			stop()
			r, err := a.PurgeAll(cancelled)
			if !errors.Is(err, context.Canceled) || r.Verified {
				t.Fatal(r, err)
			}
		})
	}
}

func TestAdminReportsPartialFailureAndTransportScope(t *testing.T) {
	cfg, tc := testServerWithSession(t, "tls", false, func(s imapserver.Session) imapserver.Session { return &protectedSession{Session: s, denied: true} })
	ctx := context.Background()
	c, err := dialMode(ctx, cfg, nil, tc, true)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, name := range []string{"Denied", "Z-delete"} {
		if err = c.c.Create(name, nil).Wait(); err != nil {
			t.Fatal(err)
		}
	}
	a := &Admin{client: c}
	r, err := a.PurgeAll(ctx)
	if err == nil || r.Verified || !strings.Contains(err.Error(), "INCOMPLETE") {
		t.Fatal(r, err)
	}
	if len(r.Remaining) != 1 || r.Remaining[0] != "Denied" {
		t.Fatal(r)
	}
	// A network failure never becomes a verified empty result.
	c.Close()
	r, err = a.PurgeAll(ctx)
	if err == nil || r.Verified {
		t.Fatal(r, err)
	}
	if c, err := dial(ctx, cfg, nil, tc); err == nil {
		c.Close()
		t.Fatal("transport accepted admin scope")
	}
}

package mailbox

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

type mailboxStateFault struct {
	imapserver.Session
	remaining *atomic.Int32
	creates   *atomic.Int32
}

func (s mailboxStateFault) Select(folder string, opts *imap.SelectOptions) (*imap.SelectData, error) {
	if s.remaining.Load() > 0 && s.remaining.Add(-1) >= 0 {
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeServerBug, Text: "Mailbox uidvalidity changed (private provider text)"}
	}
	return s.Session.Select(folder, opts)
}
func (s mailboxStateFault) Create(folder string, opts *imap.CreateOptions) error {
	s.creates.Add(1)
	return s.Session.Create(folder, opts)
}

func TestRev5MailboxStateSetupUsesFreshBoundedRetryWithoutCreate(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		t.Run(map[bool]string{false: "transient", true: "persistent"}[persistent], func(t *testing.T) {
			var remaining, creates, attempts atomic.Int32
			cfg, tc := testServerWithSession(t, "tls", true, func(s imapserver.Session) imapserver.Session { return mailboxStateFault{s, &remaining, &creates} })
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			seed, err := dial(ctx, cfg, []string{"owned"}, tc)
			if err != nil {
				t.Fatal(err)
			}
			if err = seed.Ensure(ctx, "owned"); err != nil {
				t.Fatal(err)
			}
			seed.Close()
			creates.Store(0)
			remaining.Store(1)
			if persistent {
				remaining.Store(100)
			}
			err = checkFolders(ctx, []string{"owned"}, func(ctx context.Context) (*Client, error) {
				attempts.Add(1)
				return dial(ctx, cfg, []string{"owned"}, tc)
			})
			if attempts.Load() != 2 || creates.Load() != 0 {
				t.Fatal("setup retried CREATE or exceeded fresh dial bound", attempts.Load(), creates.Load())
			}
			if persistent {
				if !errors.Is(err, ErrMailboxStateChanged) || FailureReason(err) != "mailbox_state_changed" || strings.Contains(err.Error(), "private") {
					t.Fatal("persistent rejection lost bounded reason", err)
				}
			} else if err != nil {
				t.Fatal("transient setup did not recover", err)
			}
		})
	}
}

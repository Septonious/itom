package probe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
)

// CheckIMAP authenticates exactly one account using the same default dial path
// as Serve. It performs NOOP only: no folder creation, APPEND, transport session,
// journal or quota mutation. It is useful when a server cannot reach IMAP at all.
func CheckIMAP(ctx context.Context, cfg servercfg.Config, account string, out io.Writer) error {
	return checkIMAP(ctx, cfg, account, out, mailbox.Dial)
}

func checkIMAP(ctx context.Context, cfg servercfg.Config, account string, out io.Writer, dial func(context.Context, mailbox.Config, []string) (*mailbox.Client, error)) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	var selected *servercfg.Account
	for i := range cfg.Accounts {
		if strings.EqualFold(cfg.Accounts[i].Mail.Username, account) || cfg.Accounts[i].ID == account {
			selected = &cfg.Accounts[i]
			break
		}
	}
	if selected == nil {
		return errors.New("IMAP_CHECK_ACCOUNT_NOT_FOUND")
	}
	var folders []string
	for _, u := range cfg.Users {
		if u.Enabled {
			folders = append(folders, u.Up, u.Down)
		}
	}
	if len(folders) == 0 {
		return errors.New("IMAP_CHECK_NO_ENABLED_USER")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	start := time.Now()
	client, err := dial(ctx, selected.Mail, folders)
	if err != nil {
		detail := mailbox.ConnectionDetail(err)
		if detail.Stage == "" {
			detail = mailbox.ConnectionFailure{Stage: "connect", Reason: "other"}
		}
		fmt.Fprintf(out, "account=%s outcome=failed stage=%s reason=%s elapsed_ms=%d\n", selected.Mail.Username, detail.Stage, detail.Reason, time.Since(start).Milliseconds())
		return errors.New("IMAP_CHECK_FAILED")
	}
	defer client.Close()
	connectedMS := time.Since(start).Milliseconds()
	rtt, err := client.Ping(ctx)
	if err != nil {
		fmt.Fprintf(out, "account=%s outcome=failed stage=noop reason=failed elapsed_ms=%d\n", selected.Mail.Username, time.Since(start).Milliseconds())
		return errors.New("IMAP_CHECK_FAILED")
	}
	fmt.Fprintf(out, "account=%s outcome=ok stage=noop connect_ms=%d noop_ms=%d\n", selected.Mail.Username, connectedMS, rtt.Milliseconds())
	return nil
}

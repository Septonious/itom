package probe

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/transport"
)

// CheckTransactions compares the session executor with the original standard
// IMAP adapter. It is an explicit offline diagnostic on one selected account.
// Neither implementation opens the live transport's folders or financial state.
func CheckTransactions(ctx context.Context, cfg servercfg.Config, account string, out io.Writer) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	var selected *servercfg.Account
	for n := range cfg.Accounts {
		a := &cfg.Accounts[n]
		if account == "" || a.ID == account || strings.EqualFold(a.Mail.Username, account) {
			selected = a
			break
		}
	}
	if selected == nil {
		return errors.New("IMAP_CHECK_ACCOUNT_NOT_FOUND")
	}
	cfgMail := selected.Mail
	address := cfgMail.Host
	if cfgMail.DialIP != "" {
		address = cfgMail.DialIP
	}
	endpoint := transport.Endpoint{Address: net.JoinHostPort(address, strconv.Itoa(cfgMail.Port)), ServerName: cfgMail.Host, Username: cfgMail.Username, Password: cfgMail.Password, AccountID: selected.ID}
	encoder := json.NewEncoder(out)
	var outputErr error
	emit := func(step transport.TransactionProbeStep) {
		if outputErr == nil {
			outputErr = encoder.Encode(struct {
				Account string `json:"account"`
				transport.TransactionProbeStep
			}{selected.ID, step})
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	failed := false
	for _, grouped := range []bool{false, true} {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		operationCtx, stopOperation := context.WithTimeout(ctx, 45*time.Second)
		if err := transport.ProbeMailboxTransaction(operationCtx, &net.Dialer{}, endpoint, grouped, emit); err != nil {
			failed = true
		}
		stopOperation()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		operationCtx, stopOperation = context.WithTimeout(ctx, 45*time.Second)
		if err := probeReferenceTransaction(operationCtx, cfgMail, grouped, emit); err != nil {
			failed = true
		}
		stopOperation()
	}
	if outputErr != nil {
		return outputErr
	}
	if failed {
		return errors.New("IMAP_TRANSACTIONS_FAILED")
	}
	return nil
}
func probeReferenceTransaction(ctx context.Context, cfg mailbox.Config, grouped bool, emit func(transport.TransactionProbeStep)) error {
	return probeReferenceWithDial(ctx, cfg, grouped, emit, mailbox.Dial)
}
func probeReferenceWithDial(ctx context.Context, cfg mailbox.Config, grouped bool, emit func(transport.TransactionProbeStep), dial func(context.Context, mailbox.Config, []string) (*mailbox.Client, error)) error {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return err
	}
	folder := "itom-wirecheck-" + hex.EncodeToString(token[:])
	mode := "single"
	count := 1
	if grouped {
		mode = "batch"
		count = 3
	}
	step := func(stage string, operation func() error) error {
		begin := time.Now()
		err := operation()
		record := transport.TransactionProbeStep{Implementation: "reference_imap_adapter", Mode: mode, Stage: stage, Outcome: "ok", ElapsedMS: time.Since(begin).Milliseconds()}
		if err != nil {
			record.Outcome = "failed"
			record.Failure = "reference_operation_failed"
			if errors.Is(err, context.Canceled) {
				record.Failure = "canceled"
			}
			if errors.Is(err, context.DeadlineExceeded) {
				record.Failure = "timeout"
			}
		}
		emit(record)
		return err
	}
	var client *mailbox.Client
	if err := step("connect_select", func() error {
		var err error
		client, err = dial(ctx, cfg, []string{folder})
		if err != nil {
			return err
		}
		return client.Ensure(ctx, folder)
	}); err != nil {
		if client != nil {
			client.Close()
		}
		return err
	}
	defer client.Close()
	if err := step("verify_empty", func() error {
		rows, _, err := client.Recent(ctx, folder, 4)
		if err == nil && len(rows) != 0 {
			return mailbox.ErrScope
		}
		return err
	}); err != nil {
		return err
	}
	expected := map[[32]byte]bool{}
	for n := 0; n < count; n++ {
		body := make([]byte, 96)
		if _, err := rand.Read(body); err != nil {
			return err
		}
		mime, err := mailbox.Encode(body)
		if err != nil {
			return err
		}
		expected[sha256.Sum256(mime)] = true
		if err = step("append", func() error { return client.Append(ctx, folder, mime) }); err != nil {
			return err
		}
	}
	var messages []mailbox.Message
	if err := step("fetch_verify", func() error {
		rows, _, err := client.Recent(ctx, folder, 4)
		if err != nil {
			return err
		}
		if len(rows) != count {
			return mailbox.ErrScope
		}
		seen := map[[32]byte]bool{}
		for _, row := range rows {
			hash := sha256.Sum256(row.MIME)
			if !expected[hash] || seen[hash] {
				return mailbox.ErrScope
			}
			seen[hash] = true
		}
		messages = rows
		return nil
	}); err != nil {
		return err
	}
	if err := step("verified_delete", func() error {
		if grouped {
			return client.DeleteMany(ctx, messages)
		}
		return client.Delete(ctx, messages[0])
	}); err != nil {
		return err
	}
	return step("verify_absence", func() error {
		rows, _, err := client.Recent(ctx, folder, 4)
		if err == nil && len(rows) != 0 {
			return mailbox.ErrScope
		}
		return err
	})
}

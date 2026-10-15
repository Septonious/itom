package manager

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeAdmin struct {
	purged, closed bool
	err            error
}

func (a *fakeAdmin) Folders(context.Context) ([]mailbox.Folder, error) {
	return []mailbox.Folder{{Name: "old"}}, nil
}
func (a *fakeAdmin) PurgeAll(context.Context) (mailbox.PurgeReport, error) {
	a.purged = true
	return mailbox.PurgeReport{Items: []mailbox.PurgeItem{{Name: "old", Outcome: "DELETED"}}, Verified: a.err == nil}, a.err
}
func (a *fakeAdmin) Close() error { a.closed = true; return nil }

func accountUI(t *testing.T, input string) (*UI, *bytes.Buffer) {
	t.Helper()
	out := new(bytes.Buffer)
	u := &UI{In: bufio.NewReader(strings.NewReader(input)), Out: out, State: t.TempDir(), stoppedCheck: func() error { return nil }}
	err := servercfg.Update(u.db(), func(c *servercfg.Config) error {
		n, e := servercfg.New("provider")
		*c = n
		c.Accounts = []servercfg.Account{{ID: "a01", Mail: mailbox.Config{Host: "imap.example.org", Port: 993, TLS: "tls", Username: "one@example.org", Password: "secret"}}}
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	return u, out
}
func TestRemoveAccountSelectionPreservesJournal(t *testing.T) {
	u, out := accountUI(t, "3\nONE@example.org\n")
	journal := filepath.Join(u.State, "control-server", "a01", "12345678.db")
	if err := os.MkdirAll(filepath.Dir(journal), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journal, []byte("persistent debt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := u.accounts(); err != nil {
		t.Fatal(err)
	}
	c, err := servercfg.Load(u.db())
	if err != nil || len(c.Accounts) != 0 {
		t.Fatal(c, err)
	}
	data, err := os.ReadFile(journal)
	if err != nil || string(data) != "persistent debt" {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "DELETE ") {
		t.Fatal("extra confirmation")
	}
}
func TestPurgeSelectionAndStoppedService(t *testing.T) {
	u, out := accountUI(t, "4\none@example.org\n")
	a := &fakeAdmin{}
	u.adminDial = func(context.Context, mailbox.Config) (accountAdmin, error) { return a, nil }
	if err := u.accounts(); err != nil {
		t.Fatal(err)
	}
	if !a.purged || !a.closed || strings.Contains(out.String(), "PURGE ") {
		t.Fatal(out.String())
	}
	c, err := servercfg.Load(u.db())
	if err != nil || len(c.Accounts) != 1 {
		t.Fatal(c, err)
	}
	u, _ = accountUI(t, "4\none@example.org\n")
	u.stoppedCheck = func() error { return errors.New("active") }
	u.adminDial = func(context.Context, mailbox.Config) (accountAdmin, error) {
		t.Fatal("dialed while active")
		return nil, nil
	}
	if err := u.accounts(); err == nil {
		t.Fatal("active service ignored")
	}
	u, _ = accountUI(t, "0\n")
	if err := u.accounts(); err != nil {
		t.Fatal(err)
	}
	c, err = servercfg.Load(u.db())
	if err != nil || len(c.Accounts) != 1 {
		t.Fatal(c, err)
	}
}
func TestPurgeDisplaysPartialResult(t *testing.T) {
	u, out := accountUI(t, "4\none@example.org\n")
	a := &fakeAdmin{err: errors.New("disconnected")}
	u.adminDial = func(context.Context, mailbox.Config) (accountAdmin, error) { return a, nil }
	if err := u.accounts(); err == nil || !strings.Contains(out.String(), "Full purge was not confirmed") || !strings.Contains(out.String(), "folder deleted") {
		t.Fatal(out.String(), err)
	}
}

func TestPurgeAllAccountsIsSequential(t *testing.T) {
	u, out := accountUI(t, "5\n")
	if err := servercfg.Update(u.db(), func(c *servercfg.Config) error {
		c.Accounts = append(c.Accounts, servercfg.Account{ID: "a02", Mail: mailbox.Config{Host: "imap.example.org", Port: 993, TLS: "tls", Username: "two", Password: "secret"}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	u.pauseAdmin = func(context.Context, time.Duration) error { return nil }
	var mu sync.Mutex
	active, maxActive, calls := 0, 0, []string{}
	u.adminDial = func(_ context.Context, cfg mailbox.Config) (accountAdmin, error) {
		return &serialAdmin{name: cfg.Username, enter: func(name string) {
			mu.Lock()
			defer mu.Unlock()
			active++
			if active > maxActive {
				maxActive = active
			}
			calls = append(calls, name)
		}, leave: func() {
			mu.Lock()
			active--
			mu.Unlock()
		}}, nil
	}
	if err := u.accounts(); err != nil {
		t.Fatal(err)
	}
	if maxActive != 1 || len(calls) != 2 || calls[0] != "one@example.org" || calls[1] != "two" {
		t.Fatalf("purge was not serial: max=%d calls=%v", maxActive, calls)
	}
	if !strings.Contains(out.String(), "All mail accounts were purged serially") {
		t.Fatal(out.String())
	}
}

type serialAdmin struct {
	name  string
	enter func(string)
	leave func()
	once  sync.Once
}

func (a *serialAdmin) Folders(context.Context) ([]mailbox.Folder, error) { return nil, nil }
func (a *serialAdmin) PurgeAll(context.Context) (mailbox.PurgeReport, error) {
	a.enter(a.name)
	return mailbox.PurgeReport{Verified: true}, nil
}
func (a *serialAdmin) Close() error {
	a.once.Do(a.leave)
	return nil
}

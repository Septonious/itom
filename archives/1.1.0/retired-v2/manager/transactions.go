package manager

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"

	"itom.local/core/probe"
	"itom.local/core/servercfg"
)

// RunIMAPTransactions requires the service to be stopped so this explicit probe
// does not consume a live endpoint's socket budget. Reports contain fixed labels
// and counters, never credentials, provider text or message contents.
func RunIMAPTransactions(state, account string, out io.Writer) error {
	ui := New(state)
	ui.Out = out
	return ui.imapTransactions(account)
}
func (u *UI) imapTransactions(account string) error {
	if err := u.stopped(); err != nil {
		return err
	}
	cfg, err := servercfg.Load(u.db())
	if err != nil {
		return err
	}
	fmt.Fprintln(u.Out, "Testing one account in isolated random probe mailboxes. Live transport mail, wallets and journals are untouched. The probe may take several minutes.")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var report bytes.Buffer
	err = probe.CheckTransactions(ctx, cfg, account, io.MultiWriter(u.Out, &report))
	dir := filepath.Join(u.State, "tunnel-server")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	temp, e := os.CreateTemp(dir, ".imap-transactions-*")
	if e != nil {
		return e
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, e = temp.Write(report.Bytes()); e == nil {
		e = temp.Sync()
	}
	closeErr := temp.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	if e = os.Rename(name, filepath.Join(dir, "imap-transactions.jsonl")); e != nil {
		return e
	}
	archive, e := os.CreateTemp("/tmp", "ITOM_imap_transactions_*.zip")
	if e != nil {
		return e
	}
	archiveName := archive.Name()
	archiveOK := false
	defer func() {
		archive.Close()
		if !archiveOK {
			os.Remove(archiveName)
		}
	}()
	bundle := zip.NewWriter(archive)
	entry, e := bundle.Create("imap-transactions.jsonl")
	if e != nil {
		return e
	}
	if _, e = entry.Write(report.Bytes()); e != nil {
		return e
	}
	entry, e = bundle.Create("capture.json")
	if e != nil {
		return e
	}
	if e = json.NewEncoder(entry).Encode(map[string]any{"schema": 1, "diagnostic": "isolated_imap_transactions_v1", "created_utc": time.Now().UTC(), "go": runtime.Version(), "credentials_included": false, "plaintext_payload_included": false, "transport_journals_changed": false}); e != nil {
		return e
	}
	if e = bundle.Close(); e != nil {
		return e
	}
	if e = archive.Close(); e != nil {
		return e
	}
	archiveOK = true
	fmt.Fprintln(u.Out, "Diagnostic archive:", archiveName)
	fmt.Fprintln(u.Out, "Use installed server menu option 7 to start the service again. This tool does not install or update ITOM.")
	return err
}

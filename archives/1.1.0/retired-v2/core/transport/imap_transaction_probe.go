package transport

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"itom.local/core/mailbox"
)

// TransactionProbeStep contains fixed phase/error labels and counters only.
// The random probe mailbox, provider response text, credentials, UIDs and BODYs
// never enter the report. No live transport journal or wallet is opened.
type TransactionProbeStep struct {
	Implementation string            `json:"implementation"`
	Mode           string            `json:"mode"`
	Stage          string            `json:"stage"`
	Outcome        string            `json:"outcome"`
	ElapsedMS      int64             `json:"elapsed_ms"`
	Failure        string            `json:"failure,omitempty"`
	IMAP           *ExecutorSnapshot `json:"imap,omitempty"`
}

// ProbeMailboxTransaction creates a private unpredictable mailbox and mutates
// only the one/three random messages whose complete bytes it verifies there.
// It preserves the original executor watchdog. Failure leaves the tiny isolated
// probe mailbox for inspection; it never purges or visits a transport folder.
func ProbeMailboxTransaction(ctx context.Context, d NetworkDialer, endpoint Endpoint, grouped bool, emit func(TransactionProbeStep)) error {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return err
	}
	endpoint.Mailbox = "itom-wirecheck-" + hex.EncodeToString(token[:])
	endpoint.CreateMailbox = true
	mode := "single"
	count := 1
	if grouped {
		mode = "batch"
		count = 3
	}
	var x *Executor
	step := func(stage string, operation func() error) error {
		begin := time.Now()
		err := operation()
		record := TransactionProbeStep{Implementation: "session_executor", Mode: mode, Stage: stage, Outcome: "ok", ElapsedMS: time.Since(begin).Milliseconds()}
		if err != nil {
			record.Outcome = "failed"
			record.Failure = retainedFailureCode(err)
			if phase := IMAPStartupStage(err); phase != "" {
				record.Failure = "imap_" + phase
			}
		}
		if x != nil {
			snapshot := x.Snapshot()
			record.IMAP = &snapshot
		}
		emit(record)
		return err
	}
	if err := step("connect_select", func() error {
		selected, err := DialSelected(ctx, d, endpoint, DefaultIMAPLimits())
		if err == nil {
			x = selected.Executor
		}
		return err
	}); err != nil {
		return err
	}
	defer x.Close()
	if !x.uidPlus {
		return step("uidplus", func() error { return ErrUIDPlusRequired })
	}
	inventory := func() ([]Body, error) {
		cmd, err := x.Enumerate(ctx)
		if err != nil {
			return nil, err
		}
		return cmd.Wait(ctx)
	}
	if err := step("verify_empty", func() error {
		rows, err := inventory()
		if err == nil && len(rows) != 0 {
			return ErrProtocol
		}
		return err
	}); err != nil {
		return err
	}
	expected := map[[32]byte]bool{}
	for n := 0; n < count; n++ {
		raw := make([]byte, 96)
		if _, err := rand.Read(raw); err != nil {
			return err
		}
		mime, err := mailbox.Encode(raw)
		if err != nil {
			return err
		}
		expected[sha256.Sum256(mime)] = true
		if err = step("append", func() error {
			cmd, err := x.Append(ctx, endpoint.Mailbox, mime)
			if err != nil {
				return err
			}
			_, err = cmd.Wait(ctx)
			return err
		}); err != nil {
			return err
		}
	}
	var uids []uint32
	if err := step("fetch_verify", func() error {
		rows, err := inventory()
		if err != nil {
			return err
		}
		if len(rows) != count {
			return ErrProtocol
		}
		observed := map[[32]byte]bool{}
		for _, row := range rows {
			cmd, err := x.Fetch(ctx, row.UID, row.UID)
			if err != nil {
				return err
			}
			bodies, err := cmd.Wait(ctx)
			if err != nil {
				return err
			}
			if len(bodies) != 1 {
				return ErrProtocol
			}
			hash := sha256.Sum256(bodies[0].Bytes)
			if !expected[hash] || observed[hash] {
				return ErrProtocol
			}
			observed[hash] = true
			uids = append(uids, row.UID)
		}
		return nil
	}); err != nil {
		return err
	}
	if err := step("store_deleted", func() error {
		cmd, err := x.deleteUIDSet(ctx, uids, false)
		if err != nil {
			return err
		}
		_, err = cmd.Wait(ctx)
		return err
	}); err != nil {
		return err
	}
	if err := step("uid_expunge", func() error {
		cmd, err := x.deleteUIDSet(ctx, uids, true)
		if err != nil {
			return err
		}
		_, err = cmd.Wait(ctx)
		return err
	}); err != nil {
		return err
	}
	return step("verify_absence", func() error {
		rows, err := inventory()
		if err == nil && len(rows) != 0 {
			return errors.New("PROBE_BODY_STILL_PRESENT")
		}
		return err
	})
}

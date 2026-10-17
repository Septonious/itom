package tunnel

import (
	"context"
	"crypto/sha256"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
	"testing"
)

type oneFolderDeleteMail struct {
	*memoryMail
	batches int
}

func (m *oneFolderDeleteMail) DeleteObservedMany(ctx context.Context, rows []mailbox.Message) error {
	for _, row := range rows {
		if row.Folder != rows[0].Folder || row.Validity != rows[0].Validity {
			return mailbox.ErrScope
		}
	}
	m.batches++
	for _, row := range rows {
		if err := m.Delete(ctx, row); err != nil {
			return err
		}
	}
	return nil
}
func TestCrossFolderSameUIDCleanupKeepsExactProof(t *testing.T) {
	ctx := context.Background()
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
	s, err := ledger(t.TempDir(), "data", a, u, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := s.Config()
	cfg.Accounts[0].Users[0].Folders = topologyFolders(u, 1)
	if err = s.ExtendDataFolders(cfg); err != nil {
		t.Fatal(err)
	}
	if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
		t.Fatal(err)
	}
	g, err := s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{2}}, pool.Amount{Bytes: 10000, Messages: 4})
	if err != nil {
		t.Fatal(err)
	}
	key := wire.QuotaKey{Account: a.ID, User: u.ID, Session: wire.ID{2}, Store: s.StoreID(), Epoch: s.Run(), Grant: g.Ref.ID}
	m := &oneFolderDeleteMail{memoryMail: &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}}
	var rows []mailbox.Message
	var bytes uint64
	for i, folder := range []string{u.Up, u.Up + "-data-00"} {
		batch := wire.ID{byte(i + 1)}
		body, err := wire.BindPhysical(key, uint64(i+1), batch, []byte("encrypted DATA"))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := mailbox.Encode(body)
		if err != nil {
			t.Fatal(err)
		}
		d := wire.PhysicalDescriptor{Folder: folder, Batch: batch, Digest: sha256.Sum256(body), EncodedBytes: uint64(len(raw)), DecodedBytes: uint64(len(body))}
		bytes += d.EncodedBytes
		if _, err = s.ApplyRemote(u.ID, s.Run(), wire.UsageReport{Key: key, Total: wire.Quota{Bytes: bytes, Messages: uint64(i + 1)}, Attempt: &d}); err != nil {
			t.Fatal(err)
		}
		m.Ensure(ctx, folder)
		m.Append(ctx, folder, raw)
		rows = append(rows, mailbox.Message{Folder: folder, Validity: 1, UID: 1, MIME: raw})
	}
	removed, err := reclaimBounded(ctx, m, s, rows, func(a journal.Attempt) bool { return a.Ordinal > 0 }, 32)
	if err != nil || len(removed) != 2 || m.batches != 2 {
		t.Fatal("cross-folder proof mismatch", len(removed), m.batches, err)
	}
	if err = s.Audit(); err != nil {
		t.Fatal(err)
	}
}
func TestPresenceProofSameUIDInDifferentFolders(t *testing.T) {
	m := &batchProofMail{memoryMail: &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}}
	ctx := context.Background()
	m.Ensure(ctx, "itom-up")
	m.Ensure(ctx, "itom-up-data-00")
	m.Append(ctx, "itom-up-data-00", []byte("present"))
	l := &lane{ctx: ctx, mail: m, recvFolder: "itom-up"}
	known := map[uint64]mailbox.Message{1: {Folder: "itom-up", Validity: 1, UID: 1}, 2: {Folder: "itom-up-data-00", Validity: 1, UID: 1}}
	result := l.cleanup(cleanupPresence, nil, nil, known, false, nil)
	if result.err != nil || result.presentCounters[1] || !result.presentCounters[2] || m.calls != 2 {
		t.Fatal(result.err, result.presentCounters, m.calls)
	}
}

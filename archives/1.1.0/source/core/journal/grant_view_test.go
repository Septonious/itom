package journal_test

import (
	"path/filepath"
	"testing"

	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/wire"
)

func TestPolicyGrantScanUsesCurrentCommittedRowsAndDetachedValues(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "journal.db"))
	defer s.Close()
	enable(t, s)
	g, err := s.Issue(journal.Scope{Account: "account1", User: "12345678", Session: wire.ID{1}}, pool.Amount{Bytes: 9000, Messages: 9})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.ScanGrants(0, 20)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	rows[0].Limit.Bytes = 1
	rows[0].Closed = true
	again, err := s.ScanGrants(0, 20)
	if err != nil || again[0] != g {
		t.Fatal("inspection mutated cached grant", again, err)
	}
	if err = s.CloseGrant(g.Ref); err != nil {
		t.Fatal(err)
	}
	now, err := s.ScanGrants(0, 20)
	if err != nil || !now[0].Closed || now[0].Limit != g.Limit {
		t.Fatal("committed row did not invalidate policy view", now, err)
	}
	if again[0] != g {
		t.Fatal("prior inspection result changed")
	}
}

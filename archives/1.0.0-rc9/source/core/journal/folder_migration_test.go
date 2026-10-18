package journal

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"itom.local/core/pool"
)

func TestDataFolderExtensionPreservesDirtyDebtAndLegacyIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	old := testConfig(1)
	s := openTest(t, path, old)
	g := issueTest(t, s, "a0", "12345678", pool.Amount{Bytes: 5000, Messages: 5})
	a := prepareTest(t, s, g)
	if _, err := s.Start(a.Ref); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(a.Ref, Uncertain); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = openTest(t, path, old)
	next := s.Config()
	for i := range next.Accounts {
		for j := range next.Accounts[i].Users {
			folders := append([]string(nil), next.Accounts[i].Users[j].Folders...)
			for _, f := range folders {
				next.Accounts[i].Users[j].Folders = append(next.Accounts[i].Users[j].Folders, f+"-data-00", f+"-data-01")
			}
		}
	}
	held, _ := s.Held("a0", "12345678")
	before, _ := s.ScanAttempts(0, 128)
	id := s.StoreID()
	if err := s.ExtendDataFolders(next); err != nil {
		t.Fatal(err)
	}
	if err := s.ExtendDataFolders(next); err != nil {
		t.Fatal("extension not idempotent", err)
	}
	if got, _ := s.Held("a0", "12345678"); got != held {
		t.Fatal("folder count multiplied capacity or erased debt")
	}
	after, _ := s.ScanAttempts(0, 128)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("dirty attempt changed")
	}
	if err := s.Audit(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err := OpenCompatible(path, next, []Config{old}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.StoreID() != id {
		t.Fatal("journal identity replaced")
	}
	if err = s.ExtendDataFolders(old); !errors.Is(err, ErrScope) {
		t.Fatal("old debt folder removed", err)
	}
	bad := s.Config()
	bad.Accounts[0].Limit.Bytes++
	if err = s.ExtendDataFolders(bad); !errors.Is(err, ErrScope) {
		t.Fatal("folder extension increased capacity", err)
	}
	bad = s.Config()
	bad.Accounts[0].Users[0].Folders = append(bad.Accounts[0].Users[0].Folders, "foreign-data-00")
	if err = s.ExtendDataFolders(bad); !errors.Is(err, ErrScope) {
		t.Fatal("foreign namespace authorized", err)
	}
	bad = s.Config()
	bad.Accounts[0].Users[0].Folders = append(bad.Accounts[0].Users[0].Folders, bad.Accounts[0].Users[0].Folders[0]+"-data-04")
	if err = s.ExtendDataFolders(bad); !errors.Is(err, ErrScope) {
		t.Fatal("unbounded shard index authorized", err)
	}
}

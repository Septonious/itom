package manager

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"os"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func seedCapacity(t *testing.T, dir, identity, direction string, value uint64) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "mailstream-"+identity+"-"+direction+".db")
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		b, e := tx.CreateBucketIfNotExists([]byte("mailstream-retention-v1"))
		if e != nil {
			return e
		}
		if e = b.Put([]byte("binding"), []byte(identity+"/"+direction)); e != nil {
			return e
		}
		return b.Put([]byte("account/fixture"), binary.BigEndian.AppendUint64(nil, value))
	})
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	return path
}
func capacityValue(t *testing.T, path string) uint64 {
	t.Helper()
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value uint64
	if err = db.View(func(tx *bolt.Tx) error {
		value = binary.BigEndian.Uint64(tx.Bucket([]byte("mailstream-retention-v1")).Get([]byte("account/fixture")))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return value
}
func TestManualCapacityResetPreservesProfilesAndBindings(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "server.db")
	if err := os.WriteFile(config, []byte("private profile and credentials"), 0600); err != nil {
		t.Fatal(err)
	}
	first := seedCapacity(t, dir, "alpha", "s2c", 67_000_000)
	second := seedCapacity(t, filepath.Join(dir, "tunnel-server"), "beta", "s2c", 12_000_000)
	client := seedCapacity(t, dir, "alpha", "c2s", 1234)
	if err := ResetCapacityState(dir); err != nil {
		t.Fatal(err)
	}
	if capacityValue(t, first) != 0 || capacityValue(t, second) != 0 {
		t.Fatal("server capacity did not reset completely")
	}
	if capacityValue(t, client) != 1234 {
		t.Fatal("server action changed client private history")
	}
	body, _ := os.ReadFile(config)
	if string(body) != "private profile and credentials" {
		t.Fatal("profile changed")
	}
	if err := ResetCapacityState(dir); err != nil {
		t.Fatal("binding was not preserved", err)
	}
}
func TestCapacityMigrationPreservesSubsequentCrashDebt(t *testing.T) {
	dir := t.TempDir()
	path := seedCapacity(t, filepath.Join(dir, "tunnel-server"), "alpha", "s2c", 67_000_000)
	if err := PrepareCapacityPolicy(dir); err != nil {
		t.Fatal(err)
	}
	if capacityValue(t, path) != 0 {
		t.Fatal("first update did not clear old counters")
	}
	seedCapacity(t, filepath.Join(dir, "tunnel-server"), "alpha", "s2c", 123456)
	if err := PrepareCapacityPolicy(dir); err != nil {
		t.Fatal(err)
	}
	if capacityValue(t, path) != 123456 {
		t.Fatal("crash restart reset capacity")
	}
	if err := ResetCapacityState(dir); err != nil {
		t.Fatal(err)
	}
	if capacityValue(t, path) != 0 {
		t.Fatal("manual stop did not reset capacity")
	}
}
func TestCapacityResetRefusesLiveStore(t *testing.T) {
	dir := t.TempDir()
	path := seedCapacity(t, dir, "alpha", "s2c", 123456)
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = ResetCapacityState(dir); err == nil {
		db.Close()
		t.Fatal("reset raced a live transport")
	}
	db.Close()
	if capacityValue(t, path) != 123456 {
		t.Fatal("failed reset changed charged capacity")
	}
}
func TestUpdateResetsCapacityAfterServiceStops(t *testing.T) {
	dir := t.TempDir()
	active := true
	var sequence []string
	run := func(args ...string) error {
		sequence = append(sequence, args[0])
		switch args[0] {
		case "is-active":
			if !active {
				return errors.New("inactive")
			}
		case "stop":
			active = false
		case "restart":
			active = true
		}
		return nil
	}
	reset := func() error {
		if active {
			t.Fatal("capacity reset ran while service was active")
		}
		sequence = append(sequence, "reset")
		return nil
	}
	if err := installFiles([]byte("updated executable"), filepath.Join(dir, "itom"), filepath.Join(dir, "itom.service"), run, io.Discard, reset); err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatal("update left running service stopped")
	}
	found := false
	for i := 1; i+1 < len(sequence); i++ {
		if sequence[i] == "reset" {
			found = sequence[i-1] == "stop" && sequence[i+1] == "restart"
		}
	}
	if !found {
		t.Fatalf("wrong update ordering: %v", sequence)
	}
}

func TestFailedPurgeStillResetsLocalCapacity(t *testing.T) {
	dir := t.TempDir()
	path := seedCapacity(t, filepath.Join(dir, "tunnel-server"), "alpha", "s2c", 67_000_000)
	ui := New(dir)
	ui.Out = io.Discard
	ui.stoppedCheck = func() error { return nil }
	ui.adminDial = func(context.Context, mailbox.Config) (accountAdmin, error) {
		return nil, errors.New("provider folders unavailable")
	}
	cfg := servercfg.Config{Accounts: []servercfg.Account{{ID: "a01", Mail: mailbox.Config{Username: "fixture"}}}}
	if err := ui.purgeAllAccounts(cfg); err == nil {
		t.Fatal("provider purge failure was hidden")
	}
	if capacityValue(t, path) != 0 {
		t.Fatal("failed provider purge kept local capacity charged")
	}
}

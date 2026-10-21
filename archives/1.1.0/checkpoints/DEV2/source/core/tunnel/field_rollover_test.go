package tunnel

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"itom.local/core/datapath"
	"itom.local/core/flow"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

func TestFieldExhaustedGrantYieldsUnsentBatchInsteadOfPinningWorker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cipher := cleanupFixtureEpoch(t)
	w, err := wallet.OpenBound(filepath.Join(t.TempDir(), "wallet.db"), wire.ID{1}, cipher)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	key := wire.QuotaKey{Store: wire.ID{1}, Epoch: 1, Grant: 1, Session: w.Session(), User: "12345678", Account: "a01"}
	if err = w.Accept(wire.StorageGrant{Key: key, Limit: wire.Quota{Bytes: 5120, Messages: 32}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = w.Spend(key, "up", []byte("older-uncertain-body"), func([]byte) (uint64, error) { return 4096, nil }); err != nil {
		t.Fatal(err)
	}
	cfg := datapath.DefaultConfig(1, []string{"a01"})
	e, err := datapath.New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	p, err := datapath.New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	if _, err = e.Activate(1, 1); err != nil {
		t.Fatal(err)
	}
	window, err := p.Activate(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Receive(1, window); err != nil {
		t.Fatal(err)
	}
	id, err := e.Queue(1, 1, bytes.Repeat([]byte{0x54}, 32<<10))
	if err != nil {
		t.Fatal(err)
	}
	job, err := e.Begin(1, id, "a01", 64<<10, 0)
	if err != nil {
		t.Fatal(err)
	}
	m := &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}
	m.Ensure(ctx, "up")
	l := &lane{ctx: ctx, account: servercfg.Account{ID: "a01"}, user: servercfg.User{ID: "12345678"}, sendFolder: "up", cipher: cipher, wallet: w, mail: m, urgentQueue: make(chan queuedControl, 8), controlQueue: make(chan queuedControl, 8), maintenanceQueue: make(chan queuedControl, 8), grants: make(chan wire.QuotaKey, 8), fastGrants: make(chan wire.QuotaKey, 1)}
	l.ready.Store(true)
	g := newStripeGroup(wire.ID{1}, 1)
	l.logicalSession, l.generation, l.carrierEpoch = g.logical, 1, 1
	if err = g.attach(l); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	results := l.writeBatchPhysicalReserved(l, []*datapath.Job{job}, &key)
	if time.Since(start) > 200*time.Millisecond || len(results) != 1 || !results[0].NonFatal || results[0].Outcome != flow.NotCommitted {
		t.Fatal("exhausted account retained an unsent worker instead of yielding to the pool", time.Since(start), results)
	}
	if err = e.Complete(1, results[0].Attempt, results[0].Outcome); err != nil {
		t.Fatal(err)
	}
	if !e.RetryReady(id) {
		t.Fatal("unsent logical bytes were discarded")
	}
	entries, err := w.Entries()
	if err != nil || len(entries) != 1 || !entries[0].Fenced || entries[0].Used != (wire.Quota{Bytes: 4096, Messages: 1}) || len(entries[0].Pending) != 1 {
		t.Fatal("rollback/fence invented durable spend", entries, err)
	}
	rows, _, err := m.Page(ctx, "up", mailbox.Cursor{})
	if err != nil || len(rows) != 0 {
		t.Fatal("APPEND occurred before successful Spend", err)
	}
}

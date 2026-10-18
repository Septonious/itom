package tunnel

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"itom.local/core/flow"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

type unavailableAppendMail struct{ *memoryMail }

func (m *unavailableAppendMail) DataAvailable() bool { return false }

// A physical carrier's writer disappears after grant selection but before
// APPEND, while its carrier context and logical owner are still alive. This
// deterministic race previously returned a fatal STRIPE_UNAVAILABLE to Pump.
func TestSelectedCarrierUnavailableBeforeAppendKeepsTCPRetry(t *testing.T) {
	ctx := context.Background()
	cipher := cleanupFixtureEpoch(t)
	w, err := wallet.OpenBound(filepath.Join(t.TempDir(), "wallet.db"), wire.ID{1}, cipher)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	key := wire.QuotaKey{Store: wire.ID{1}, Epoch: 1, Grant: 1, Session: w.Session(), User: "12345678", Account: "a01"}
	offer := wire.StorageGrant{Key: key, Limit: wire.Quota{Bytes: dataLeaseBytes, Messages: dataLeaseMessages}}
	if err = w.Accept(offer); err != nil {
		t.Fatal(err)
	}
	m := &unavailableAppendMail{&memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}}
	if err = m.Ensure(ctx, "up"); err != nil {
		t.Fatal(err)
	}
	g := newStripeGroup(wire.ID{1}, 1)
	l := &lane{ctx: ctx, account: servercfg.Account{ID: "a01"}, user: servercfg.User{ID: "12345678"}, sendFolder: "up", cipher: cipher, wallet: w, mail: m, logicalSession: g.logical, generation: 1, carrierEpoch: 1, grants: make(chan wire.QuotaKey, 8), fastGrants: make(chan wire.QuotaKey, 1), grantFast: map[wire.QuotaKey]bool{key: false}, grantLimits: map[wire.QuotaKey]wire.Quota{key: offer.Limit}, remote: map[wire.QuotaKey][]remoteLogicalAttempt{}}
	l.ready.Store(true)
	if err = g.attach(l); err != nil {
		t.Fatal(err)
	}
	jobs := deliveryJobs(t, 1, 1, 32768)
	results := l.writeBatchPhysicalReserved(l, jobs, &key)
	if len(results) != 1 || results[0].Outcome != flow.NotCommitted || !results[0].NonFatal || !errors.Is(results[0].Err, errStripeUnavailable) {
		t.Fatal("pre-APPEND selection loss closed logical TCP", results)
	}
	entries, err := w.Entries()
	if err != nil || len(entries) != 1 || entries[0].Used != (wire.Quota{}) || len(entries[0].Pending) != 0 || entries[0].Fenced {
		t.Fatal("selection loss created physical usage", entries, err)
	}
	if len(l.grants) != 1 {
		t.Fatal("unused grant not returned")
	}
	rows, _, err := m.Page(ctx, "up", mailbox.Cursor{})
	if err != nil || len(rows) != 0 {
		t.Fatal("physical APPEND before usable writer", rows, err)
	}
	if l.ctx.Err() != nil || l.logicalContext().Err() != nil {
		t.Fatal("fixture canceled instead of exercising availability race")
	}
	// Failed cancellation must remain fatal; uncertain accounting cannot be
	// converted to an uncharged retry even when carrier availability is lost.
	results = l.retryBeforeAppend(l, deliveryJobs(t, 3, 1, 32), nil, errStripeUnavailable, wallet.ErrIO)
	if results[0].NonFatal || !errors.Is(results[0].Err, wallet.ErrIO) {
		t.Fatal("failed cleanup marked safe to retry", results)
	}
}

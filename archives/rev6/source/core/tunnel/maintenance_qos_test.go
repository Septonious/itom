package tunnel

import (
	"context"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

func checkFastPhysicalExcludesCleanupIO(t *testing.T) {
	l := &lane{}
	if !l.tryBeginCleanupIO() {
		t.Fatal("cleanup guard unavailable")
	}
	if l.tryBeginFastPhysical() {
		t.Fatal("fast physical entered during cleanup")
	}
	l.endCleanupIO()
	if !l.tryBeginFastPhysical() {
		t.Fatal("fast physical guard unavailable")
	}
	if l.tryBeginCleanupIO() {
		t.Fatal("cleanup entered during fast physical")
	}
	l.endFastPhysical()
	if !l.tryBeginCleanupIO() {
		t.Fatal("cleanup guard did not recover")
	}
	l.endCleanupIO()
}

func checkOwnerCleanupPressureThresholds(t *testing.T) {
	l := &lane{remoteGC: map[journal.AttemptRef]remoteCleanup{}, known: map[uint64]mailbox.Message{}}
	for i := 1; i <= 15; i++ {
		l.remoteGC[journal.AttemptRef{ID: uint64(i)}] = remoteCleanup{}
	}
	if l.ownerCleanupPressure(cleanupRemote) {
		t.Fatal("remote cleanup pressured too early")
	}
	l.remoteGC[journal.AttemptRef{ID: 16}] = remoteCleanup{}
	if !l.ownerCleanupPressure(cleanupRemote) {
		t.Fatal("remote cleanup pressure not detected")
	}
	for i := 1; i <= 255; i++ {
		l.known[uint64(i)] = mailbox.Message{}
	}
	if l.ownerCleanupPressure(cleanupPresence) {
		t.Fatal("presence cleanup pressured too early")
	}
	l.known[256] = mailbox.Message{}
	if !l.ownerCleanupPressure(cleanupPresence) {
		t.Fatal("presence cleanup pressure not detected")
	}
}

func checkStripeRecognizesActiveOwner(t *testing.T) {
	g := newStripeGroup([16]byte{}, 1)
	l := &lane{}
	g.owners[1] = l
	if !g.ownsActiveStream(l) {
		t.Fatal("active owner not recognized")
	}
	delete(g.owners, 1)
	if g.ownsActiveStream(l) {
		t.Fatal("retired owner remained active")
	}
}

func checkStripeDetectsUnownedReadyCleanupCarrier(t *testing.T) {
	g := newStripeGroup([16]byte{}, 1)
	a := &lane{}
	b := &lane{}
	a.ready.Store(true)
	b.ready.Store(true)
	g.lanes["a"] = a
	g.lanes["b"] = b
	g.owners[1] = a
	if !g.hasUnownedReadyCarrier() {
		t.Fatal("ready non-owner carrier not detected")
	}
	g.owners[3] = b
	if g.hasUnownedReadyCarrier() {
		t.Fatal("all-owner topology incorrectly reported cleanup spare")
	}
	b.ready.Store(false)
	delete(g.owners, 3)
	if g.hasUnownedReadyCarrier() {
		t.Fatal("unready non-owner carrier must not defer owner cleanup")
	}
}

func checkRemoteCleanupRunsWhenEveryReadyCarrierOwnsStream(t *testing.T) {
	g := newStripeGroup([16]byte{}, 1)
	a := &lane{stripe: g, remoteGC: map[journal.AttemptRef]remoteCleanup{}}
	b := &lane{stripe: g, remoteGC: map[journal.AttemptRef]remoteCleanup{}}
	a.ready.Store(true)
	b.ready.Store(true)
	g.lanes["a"] = a
	g.lanes["b"] = b
	g.owners[1] = a
	g.owners[3] = b
	a.remoteGC[journal.AttemptRef{ID: 1}] = remoteCleanup{}
	if a.ownerCleanupPressure(cleanupRemote) {
		t.Fatal("regression setup unexpectedly reached ordinary remote pressure")
	}
	if a.deferOwnerCleanup(cleanupRemote) {
		t.Fatal("all-owner topology deferred remote cleanup and can exhaust grants")
	}
	delete(g.owners, 3)
	if !a.deferOwnerCleanup(cleanupRemote) {
		t.Fatal("owner cleanup was not deferred while a ready non-owner carrier existed")
	}
	if !a.deferOwnerCleanup(cleanupLocalData) {
		t.Fatal("local DATA cleanup must retain ordinary owner deferral without pressure")
	}
}

func TestPressuredLocalControlPreemptsContinuousRemoteGC(t *testing.T) {
	cfg, err := servercfg.New("test")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Accounts = []servercfg.Account{{
		ID:   "a01",
		Mail: mailbox.Config{Host: "mail.example", Port: 993, TLS: "tls", Username: "user", Password: "test"},
	}}
	u, err := cfg.AddUser("test")
	if err != nil {
		t.Fatal(err)
	}
	control, err := ledger(t.TempDir(), "control", cfg.Accounts[0], u, true)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	if err = control.EnableAfterInventory("a01", u.ID, control.Run()); err != nil {
		t.Fatal(err)
	}
	grant, err := control.Issue(journal.Scope{Account: "a01", User: u.ID, Session: wire.ID{1}}, pool.Amount{Bytes: 48 * 128, Messages: 48})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 48; i++ {
		attempt, prepErr := control.Prepare(grant.Ref, u.Down, wire.ID{byte(i + 1)}, 128, []byte("control-pressure"))
		if prepErr != nil {
			t.Fatalf("prepare %d: %v", i, prepErr)
		}
		if _, err = control.Start(attempt.Ref); err != nil {
			t.Fatal(err)
		}
		if err = control.Complete(attempt.Ref, journal.Uncertain); err != nil {
			t.Fatal(err)
		}
	}
	if err = control.CloseGrant(grant.Ref); err != nil {
		t.Fatal(err)
	}
	l := &lane{
		account:  cfg.Accounts[0],
		user:     u,
		control:  control,
		remoteGC: map[journal.AttemptRef]remoteCleanup{},
	}
	for i := 1; i <= 16; i++ {
		l.remoteGC[journal.AttemptRef{ID: uint64(i)}] = remoteCleanup{}
	}
	if !l.ownerCleanupPressure(cleanupRemote) {
		t.Fatal("remote cleanup pressure missing from regression setup")
	}
	kind, ok := l.pressuredLocalCleanup()
	if !ok || kind != cleanupLocalControl {
		t.Fatalf("continuous remote GC would preempt pressured local control: kind=%v ok=%v", kind, ok)
	}
}

func checkCleanupReservationBlocksNewFastPhysical(t *testing.T) {
	l := &lane{}
	if !l.tryBeginFastPhysical() {
		t.Fatal("initial fast physical admission failed")
	}
	// Simulate cleanup declaring intent while one fast operation is still in
	// flight. New fast work must stop so the existing operation can drain.
	l.cleanupWanted.Store(true)
	if l.tryBeginFastPhysical() {
		t.Fatal("fast physical bypassed cleanup reservation")
	}
	l.endFastPhysical()
	if !l.tryBeginCleanupIO() {
		t.Fatal("cleanup could not acquire after existing fast I/O drained")
	}
	l.endCleanupIO()
	l.cleanupWanted.Store(false)
	if !l.tryBeginFastPhysical() {
		t.Fatal("fast physical did not recover after cleanup reservation cleared")
	}
	l.endFastPhysical()
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioMaintenanceQos(t *testing.T) {
	t.Log("check=checkFastPhysicalExcludesCleanupIO")
	checkFastPhysicalExcludesCleanupIO(t)
	t.Log("check=checkOwnerCleanupPressureThresholds")
	checkOwnerCleanupPressureThresholds(t)
	t.Log("check=checkStripeRecognizesActiveOwner")
	checkStripeRecognizesActiveOwner(t)
	t.Log("check=checkStripeDetectsUnownedReadyCleanupCarrier")
	checkStripeDetectsUnownedReadyCleanupCarrier(t)
	t.Log("check=checkRemoteCleanupRunsWhenEveryReadyCarrierOwnsStream")
	checkRemoteCleanupRunsWhenEveryReadyCarrierOwnsStream(t)
	t.Log("check=checkCleanupReservationBlocksNewFastPhysical")
	checkCleanupReservationBlocksNewFastPhysical(t)
}

func TestIndependentDataMaintenanceDoesNotBlockShortAppend(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cm, _ := tlsMailPair(t, ctx, []string{"up", "down"})
	m := cm.(*laneMail)
	if err := m.Activate(ctx, "down"); err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	l := &lane{mail: &observedMail{Transport: m}}
	if !l.independentDataMaintenance() || !l.tryBeginCleanupIO() {
		t.Fatal("missing independent roles")
	}
	l.gcActive.Store(true)
	l.cleanupWanted.Store(true)
	if l.cleanupBlocksFast() || !l.tryBeginFastPhysical() {
		t.Fatal("cleaner excluded independent short APPEND")
	}
	raw, _ := mailbox.Encode([]byte("short DATA during maintenance"))
	if err := m.AppendDataPriority(ctx, "up", raw, true); err != nil {
		t.Fatal(err)
	}
	l.endFastPhysical()
	if l.maintenanceIO.Load() != -1 {
		t.Fatal("fast completion corrupted destructive service lease")
	}
	l.endCleanupIO()
	rows, _, err := m.Client.Page(ctx, "up", mailbox.Cursor{})
	if err != nil || len(rows) != 1 || string(rows[0].MIME) != string(raw) {
		t.Fatal("concurrent publication lost", err)
	}
}

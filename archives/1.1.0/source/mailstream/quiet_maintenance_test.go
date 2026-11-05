package mailstream

import (
	"bytes"
	"testing"
	"time"
)

// RC9 kept reporting every housekeeping-generated control UID once a second
// until historical recovery finished. Reports caused receipts and collection
// reports, which created more control UIDs and sustained the loop at idle.
func TestUnfinishedHistoryDoesNotCreateIdleControlFeedback(t *testing.T) {
	id := SessionID{1}
	l := &IMAPLink{client: true, attached: id}
	p := &mailPipe{validity: 17}
	p.cursor.Store(100)
	a := &mailAccountState{link: l, config: MailAccount{ID: "a01"}, control: p}
	a.maintenance.epoch = id
	a.maintenance.initialOut = uidWatermark{17, 100}
	l.accounts = []*mailAccountState{a}
	first := l.maintenanceFrames(id)
	if len(first) != 1 || first[0].kind != frameControlRead {
		t.Fatal("initial recovery watermark missing", first)
	}
	extra := 0
	for i := 1; i <= 12; i++ {
		p.cursor.Store(100 + uint32(2*i))
		a.maintenance.lastReport = time.Now().Add(-2 * time.Second)
		extra += len(l.maintenanceFrames(id))
	}
	if extra != 0 {
		t.Fatalf("idle housekeeping sustained its own reporting loop: %d extra reports without storage pressure", extra)
	}
}

func TestPeerPressureRequestsRecoveryWithoutForgivingDebt(t *testing.T) {
	id := SessionID{1}
	l := &IMAPLink{client: true, attached: id}
	a := &mailAccountState{link: l, config: MailAccount{ID: "a01"}, control: &mailPipe{}, spent: 1 << 20, recovered: 1 << 20}
	a.maintenance.epoch = id
	l.accounts = []*mailAccountState{a}
	packet, err := (frame{kind: frameStoragePressure, data: append([]byte("a01"), 1)}).encode()
	if err != nil {
		t.Fatal(err)
	}
	f, err := decodeFrame(packet)
	if err != nil || !bytes.Equal(f.data, append([]byte("a01"), 1)) {
		t.Fatal("pressure frame roundtrip", err)
	}
	if err = l.applyMaintenance(id, f); err != nil {
		t.Fatal(err)
	}
	if !a.historyRequiredLocked() || a.spent != 1<<20 || a.recovered != 1<<20 {
		t.Fatal("pressure must request audited work, never refund debt")
	}
	f.data[len(f.data)-1] = 0
	if err = l.applyMaintenance(id, f); err != nil {
		t.Fatal(err)
	}
	if a.historyRequiredLocked() || a.spent != 1<<20 || a.recovered != 1<<20 {
		t.Fatal("cleared peer pressure must stop urgent history without forgiving debt")
	}
}

func TestCurrentDataBacklogDoesNotRequestHistoricalSweep(t *testing.T) {
	a := &mailAccountState{spent: prototypePhysicalBudget * 7 / 8}
	if a.storagePressureLocked() || a.needsMaintenanceLocked() {
		t.Fatal("current DATA backlog diverted collection into unrelated historical folders")
	}
	a.recovered = 1 << 20
	if !a.storagePressureLocked() || !a.needsMaintenanceLocked() {
		t.Fatal("historical debt under real capacity pressure was not scheduled")
	}
}

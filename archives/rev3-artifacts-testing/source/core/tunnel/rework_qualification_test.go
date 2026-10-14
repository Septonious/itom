package tunnel

import (
	"bytes"
	"io"
	"os"
	"testing"
	"time"
)

// This test measures useful application bytes on the real public supervisors.
// Its PASS is a declared laboratory case only. No Go test can certify a real
// Android/provider path without independent device/provider measurements.
func TestTransportReworkQualification(t *testing.T) {
	if os.Getenv("ITOM_REWORK_QUALIFICATION") != "1" {
		t.Skip("run tools/transport_qualification.py; a skipped qualification test is not a passing performance gate")
	}
	if loadEnvInt(t, "ITOM_LOAD_SECONDS", 60, 60, 7200) < 60 {
		t.Fatal("qualification duration")
	}
	testSupervisedProductionLoad(t)
}
func TestPublicSplitReaderRepairPreservesSessionAndTCP(t *testing.T) {
	f := newProductionSupervisorFixture(t)
	p := f.openOn(t, "a01")
	p.SetDeadline(time.Now().Add(30 * time.Second))
	logical, generation := f.client.logicalSession, f.client.generation
	f.client.mu.Lock()
	l := f.client.lanes["a01"]
	f.client.mu.Unlock()
	m := physicalMail(l.mail)
	m.topologyMu.Lock()
	reader := m.dataReader
	m.topologyMu.Unlock()
	if reader == nil {
		t.Fatal("independent DATA reader missing")
	}
	reader.Close()
	// Bulk DATA requires the failed service, rather than only fast/control mail.
	payload := make([]byte, 256<<10)
	for i := range payload {
		payload[i] = byte(i * 31)
	}
	done := make(chan error, 1)
	go func() { done <- productionWriteFull(p, payload) }()
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(p, got); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, got) || f.client.logicalSession != logical || f.client.generation != generation {
		t.Fatal("reader repair replaced TCP/session or corrupted DATA")
	}
}

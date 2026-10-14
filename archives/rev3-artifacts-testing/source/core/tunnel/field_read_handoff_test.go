package tunnel

import (
	"bytes"
	"context"
	"testing"
	"time"

	"itom.local/core/mailbox"
)

func TestReceiveWorkerHasOneFlightAndNeverBorrowsCleaner(t *testing.T) {
	t.Setenv("ITOM_FIELD_PROVIDER_MODEL", "1")
	network := newProviderTestNetwork(0)
	network.fieldLatency = newFieldLatencyReplay(t)
	r := network.fieldLatency
	for name := range r.samples {
		r.samples[name] = []int64{1000}
	}
	r.stallEnabled, r.stallUS, r.start = true, 800_000, time.Now().Add(-21*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cm, _ := tlsMailPairWithNetwork(t, ctx, []string{"itom-up", "itom-down"}, network, "a02")
	m := cm.(*laneMail)
	if err := m.Activate(ctx, "itom-down"); err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	reader, cleaner := m.reader, m.cleaner
	raw, _ := mailbox.Encode([]byte("field response"))
	if err := m.Client.Append(ctx, "itom-down", raw); err != nil {
		t.Fatal(err)
	}
	accepted := network.accepted.Load()
	callbacks := 0
	begin := time.Now()
	rows, cur, err := m.PageStream(ctx, "itom-down", mailbox.Cursor{}, func(mailbox.Message) error { callbacks++; return nil })
	if err != nil || len(rows) != 1 || callbacks != 1 || time.Since(begin) < 700*time.Millisecond {
		t.Fatalf("stalled read: rows=%d callbacks=%d duration=%s error=%v", len(rows), callbacks, time.Since(begin), err)
	}
	if !r.stallAccounts["a02"].Load() || m.ReaderHandoffs() != 0 {
		t.Fatal("slow FETCH escaped its receive worker")
	}
	if _, err := m.Client.Ping(ctx); err != nil {
		t.Fatal("control socket blocked", err)
	}
	if _, _, err := m.Page(ctx, "itom-down", cur); err != nil {
		t.Fatal(err)
	}
	if err := pause(ctx, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Page(ctx, "itom-down", cur); err != nil {
		t.Fatal(err)
	}
	if callbacks != 1 || m.reader != reader || m.cleaner != cleaner || network.accepted.Load() != accepted {
		t.Fatal("handoff duplicated callbacks, dialed, or failed to rejoin")
	}
	checkFieldLargeFetchMakesProgressWithoutReadHedge(t)
	checkFieldCleanerFailureDoesNotBecomeReaderFailure(t)
}

func checkFieldLargeFetchMakesProgressWithoutReadHedge(t *testing.T) {
	network := newProviderTestNetwork(8)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cm, _ := tlsMailPairWithNetwork(t, ctx, []string{"itom-up", "itom-down"}, network, "a03")
	m := cm.(*laneMail)
	if err := m.Activate(ctx, "itom-down"); err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	raw, _ := mailbox.Encode(bytes.Repeat([]byte{0x73}, 1<<20))
	if err := m.Client.Append(ctx, "itom-down", raw); err != nil {
		t.Fatal(err)
	}
	begin := time.Now()
	rows, _, err := m.Page(ctx, "itom-down", mailbox.Cursor{})
	if err != nil || len(rows) != 1 || time.Since(begin) <= 350*time.Millisecond || m.ReaderHandoffs() != 0 {
		t.Fatalf("healthy large BODY hedged: rows=%d duration=%s handoffs=%d err=%v", len(rows), time.Since(begin), m.ReaderHandoffs(), err)
	}
}

// A maintenance failure must never migrate receive ownership or its cursor.
func checkFieldCleanerFailureDoesNotBecomeReaderFailure(t *testing.T) {
	network := newProviderTestNetwork(0)
	network.fieldLatency = newFieldLatencyReplay(t)
	r := network.fieldLatency
	for name := range r.samples {
		r.samples[name] = []int64{1000}
	}
	r.stallEnabled, r.stallUS, r.start = true, 800_000, time.Now().Add(-21*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	cm, _ := tlsMailPairWithNetwork(t, ctx, []string{"itom-up", "itom-down"}, network, "a02")
	m := cm.(*laneMail)
	if err := m.Activate(ctx, "itom-down"); err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	raw, _ := mailbox.Encode([]byte("optional-reader-debt"))
	if err := m.Client.Append(ctx, "itom-down", raw); err != nil {
		t.Fatal(err)
	}
	_, cur, err := m.Page(ctx, "itom-down", mailbox.Cursor{})
	if err != nil || m.ReaderHandoffs() != 0 {
		t.Fatal("receive worker failed", err)
	}
	if err := m.cleaner.Close(); err != nil {
		t.Fatal(err)
	}
	_, next, err := m.Page(ctx, "itom-down", cur)
	if err != nil || next != cur {
		t.Fatal("optional socket failure changed primary cursor/liveness", next, cur, err)
	}

	if _, _, err := m.Page(ctx, "itom-down", cur); err != nil {
		t.Fatal("primary reader did not resume", err)
	}
	if _, err := m.Client.Ping(ctx); err != nil {
		t.Fatal("control socket was closed", err)
	}
}

func TestReceiveWorkerRepairsNoProgressAndKeepsApplicationOwner(t *testing.T) {
	t.Setenv("ITOM_FIELD_PROVIDER_MODEL", "1")
	network := newProviderTestNetwork(0)
	network.fieldLatency = newFieldLatencyReplay(t)
	replay := network.fieldLatency
	for name := range replay.samples {
		replay.samples[name] = []int64{1000}
	}
	replay.stallEnabled, replay.stallUS, replay.start = true, 3_000_000, time.Now().Add(-21*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cm, _ := tlsMailPairWithNetwork(t, ctx, []string{"itom-up", "itom-down"}, network, "a02")
	m := cm.(*laneMail)
	if err := m.Activate(ctx, "itom-down"); err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	reader, control := m.readerRef(), m.Client
	raw, _ := mailbox.Encode([]byte("same logical receiver"))
	if err := control.Append(ctx, "itom-down", raw); err != nil {
		t.Fatal(err)
	}
	begin := time.Now()
	callbacks := 0
	rows, next, err := m.PageStream(ctx, "itom-down", mailbox.Cursor{}, func(mailbox.Message) error { callbacks++; return nil })
	if err != nil || len(rows) != 1 || callbacks != 1 || next.After != 1 {
		t.Fatal("repair lost/duplicated delivery", rows, next, callbacks, err)
	}
	if time.Since(begin) >= 2800*time.Millisecond || m.readerRef() == reader || m.readerRepairs.Load() != 1 || m.Client != control {
		t.Fatal("receive role was not repaired independently", time.Since(begin), m.readerRepairs.Load())
	}
	if _, err := control.Ping(ctx); err != nil {
		t.Fatal("control owner failed", err)
	}
}

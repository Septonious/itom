package tunnel

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"itom.local/core/datapath"
	"itom.local/core/mailbox"
)

// The field impairment is downstream-only and per physical DATA socket.
// APPEND and short controls remain fast. Keep the original application streams
// alive, learn receipt service, then verify a bounded next-transfer prefix.
func TestRev5PacedDownloadKeepsApplicationsAndNextPrefixUsable(t *testing.T) {
	t.Setenv("ITOM_LOAD_COMMAND_DELAY_MS", "15")
	t.Setenv("ITOM_LOAD_LITERAL_PLUS", "1")
	t.Setenv("ITOM_LOAD_DIAGNOSTICS", "1")
	f := newProductionSupervisorFixtureAccounts(t, 4)
	readers := map[*laneMail]*mailbox.Client{}
	f.client.mu.Lock()
	for _, l := range f.client.lanes {
		m := physicalMail(l.mail)
		if m != nil && m.dataReaderRef() != nil {
			readers[m] = m.dataReaderRef()
		}
	}
	f.client.mu.Unlock()
	if len(readers) != 4 {
		t.Fatal("missing independent DATA readers", len(readers))
	}
	for _, r := range readers {
		paceProviderSocket(t, f.network, r, .3)
	}
	chat := rev4Open(t, f)
	if err := rev4Exchange(chat, 1, 1024, 1100*time.Millisecond); err != nil {
		t.Fatal("initial short exchange", err)
	}
	var workers sync.WaitGroup
	errors := make(chan error, 4)
	for i := 0; i < 4; i++ {
		c := rev4Open(t, f)
		workers.Add(1)
		go func(i int, c net.Conn) {
			defer workers.Done()
			defer c.Close()
			errors <- rev4Exchange(c, 100+i, 256<<10, 60*time.Second)
		}(i, c)
	}
	done := make(chan struct{})
	go func() { workers.Wait(); close(done) }()
	probe := time.NewTicker(500 * time.Millisecond)
	defer probe.Stop()
	probes := 0
loop:
	for {
		select {
		case <-done:
			break loop
		case <-probe.C:
			if err := rev4Exchange(chat, 200+probes, 128, 1100*time.Millisecond); err != nil {
				t.Fatal("short application blocked by paced BODY", err)
			}
			probes++
		case <-f.ctx.Done():
			t.Fatal(f.ctx.Err())
		}
	}
	for i := 0; i < 4; i++ {
		if err := <-errors; err != nil {
			t.Fatal("paced bulk corrupt or stalled", err)
		}
	}
	if probes < 4 {
		t.Fatal("bulk/probe overlap not exercised", probes)
	}
	f.mu.Lock()
	var owner *lane
	trained := 0
	for _, l := range f.serverLanes {
		owner = l
		l.serviceMu.Lock()
		samples := l.peerReadSamples
		t.Logf("peer_read_policy account=%s target=%d samples=%d slow=%d fast=%d provider_baseline=%s", l.account.ID, l.peerReadBatchTarget, l.peerReadSamples, l.peerReadSlowCount, l.peerReadFastCount, l.providerBaseline)
		l.serviceMu.Unlock()
		if l.serviceBatchTarget() < datapath.BulkBatchTargetBytes && samples > 0 {
			trained++
		}
	}
	f.mu.Unlock()
	if trained < 2 {
		t.Fatal("paced physical receipts did not train bulk carriers", trained)
	}
	if target, _ := owner.BulkBatchPolicy(); target > 64<<10 {
		t.Fatal("quiet readers hid paced bulk service", target)
	}
	// A 256 KiB batch takes about nine seconds on this path. The trained
	// next transfer must expose an intact first record in under 3 s + 10%.
	next := rev4Open(t, f)
	if err := rev5PacedPrefix(next, 256<<10, 3300*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := rev4Exchange(chat, 300, 1024, 1100*time.Millisecond); err != nil {
		t.Fatal("application unavailable after load", err)
	}
	for m, r := range readers {
		if m.dataReaderRef() != r || !r.Ready() {
			t.Fatal("paced healthy DATA socket was replaced")
		}
	}
	t.Logf("downstream_per_tcp_mbps=0.3 streams=4 intact_bulk_bytes=%d short_probes=%d short_deadline_ms=1100 next_prefix_deadline_ms=3300 trained_bulk_accounts=%d", 4*(256<<10), probes, trained)
}

func rev5PacedPrefix(c net.Conn, size int, deadline time.Duration) error {
	want := make([]byte, size)
	for i := range want {
		want[i] = byte((i*137 + 19) % 251)
	}
	if err := c.SetDeadline(time.Now().Add(deadline)); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- productionWriteFull(c, want) }()
	got := make([]byte, 32<<10)
	if _, err := io.ReadFull(c, got); err != nil {
		return fmt.Errorf("paced next-transfer first record: %w", err)
	}
	if !bytes.Equal(got, want[:len(got)]) {
		return fmt.Errorf("paced prefix corrupt")
	}
	if err := c.SetDeadline(time.Now().Add(20 * time.Second)); err != nil {
		return err
	}
	rest := make([]byte, size-len(got))
	if _, err := io.ReadFull(c, rest); err != nil {
		return err
	}
	if err := <-done; err != nil {
		return err
	}
	if !bytes.Equal(rest, want[len(got):]) {
		return fmt.Errorf("paced transfer tail corrupt")
	}
	return nil
}

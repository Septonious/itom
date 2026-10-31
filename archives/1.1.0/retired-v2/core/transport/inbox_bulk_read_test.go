package transport_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"itom.local/core/secure"
	"itom.local/core/transport"
)

// Actual SERVER-origin publications commit their original journal attempts and
// traverse TLS APPEND before timing begins. The measurement covers ONLY later
// stored-mail discovery, decryption and application consumption over an 80ms
// read path. It excludes upload/startup and is NOT sustained tunnel throughput.
func TestSizedBulkReadsDeliverActualEncryptedPublicationsWithoutExtraAllowance(t *testing.T) {
	for _, batch := range []int{1, 2} {
		t.Run(fmt.Sprintf("batch_%d", batch), func(t *testing.T) {
			f := serverPublications(t, transport.Bulk, 64<<20)
			box := &sharedMailbox{}
			upload := box.selectedAccountService(t, "account1", "bulk", nil, false, false)
			const payloadSize = 256 << 10
			for n := range 8 {
				id := uint64(2*n + 1)
				if n > 0 {
					add(t, f.server, id, 1<<20, 1<<20)
					add(t, f.client, id, 1<<20, 1<<20)
				}
				if err := f.server.Queue(id, bytes.Repeat([]byte{byte(n + 1)}, payloadSize), time.Now()); err != nil {
					t.Fatal(err)
				}
				p, err := f.publisher.PrepareBulk(context.Background(), transport.MaxBulkBytes)
				if err != nil || p == nil {
					t.Fatal("real durable publication unavailable", err)
				}
				if outcome, err := p.Start(context.Background(), upload); err != nil || outcome != transport.Accepted {
					t.Fatal("actual APPEND failed", outcome, err)
				}
			}
			download := box.selectedAccountNetworkService(t, "account1", "bulk", nil, false, false, newWireRoute([]time.Duration{80 * time.Millisecond}, time.Second, 62_500_000))
			limits := transport.DefaultInboxLimits()
			limits.BodyReadBatch = batch
			inbox, err := transport.NewClientInbox(download, limits, f.incoming)
			if err != nil {
				t.Fatal(err)
			}
			heldBefore, err := f.store.Held(f.scope.Account, f.scope.User)
			if err != nil || heldBefore.Messages != 8 {
				t.Fatal("publications lack original physical debt", heldBefore, err)
			}
			started := time.Now()
			progress, err := inbox.Sweep(context.Background())
			if err != nil || progress.Applied != 8 {
				t.Fatal("bounded encrypted BODY delivery failed", progress, err)
			}
			consumed := 0
			for n := range 8 {
				got := make([]byte, payloadSize)
				count, err := f.client.Read(uint64(2*n+1), got)
				if err != nil || count != payloadSize || !bytes.Equal(got, bytes.Repeat([]byte{byte(n + 1)}, payloadSize)) {
					t.Fatal("actual application bytes changed", n, count, err)
				}
				consumed += count
			}
			elapsed := time.Since(started)
			if reads := download.Snapshot().Fetch.Completed; reads != uint64(8/batch) {
				t.Fatal("encrypted ready bodies still paid individual turns", reads)
			}
			// Reading/delivering cannot refund an issuer without its original
			// own-BODY/client-MAC/expunge/absence proof chain.
			if err := f.store.Audit(); err != nil {
				t.Fatal(err)
			}
			if held, err := f.store.Held(f.scope.Account, f.scope.User); err != nil || held != heldBefore {
				t.Fatal("a grouped BODY read released physical debt", held, err)
			}
			box.mu.Lock()
			first := bytes.Clone(box.bodies[0])
			box.mu.Unlock()
			if _, err := f.incoming.Decode(9, 1, first); !errors.Is(err, secure.ErrReplay) {
				t.Fatal("grouped delivery failed to retain actual replay state", err)
			}
			t.Logf("stored_bulk_read batch=%d consumed_bytes=%d body_commands=%d elapsed_ns=%d", batch, consumed, download.Snapshot().Fetch.Completed, elapsed.Nanoseconds())
		})
	}
}

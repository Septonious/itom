package tunnel

import (
	"bytes"
	"context"
	"itom.local/core/datapath"
	"itom.local/core/flow"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/wallet"
	"itom.local/core/wire"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type delayedUsageAppend struct {
	*memoryMail
	entered, release chan struct{}
	calls            atomic.Int32
	unknown          bool
	unknownBefore    bool
}

func (m *delayedUsageAppend) Append(ctx context.Context, f string, raw []byte) error {
	m.calls.Add(1)
	close(m.entered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.release:
	}
	if m.unknownBefore {
		return mailbox.ErrUnknown
	}
	if err := m.memoryMail.Append(ctx, f, raw); err != nil {
		return err
	}
	if m.unknown {
		return mailbox.ErrUnknown
	}
	return nil
}

func TestDescriptorOverlapsAppendWithoutEarlyGrantReuseOrDebtRefund(t *testing.T) {
	for _, class := range []string{"fast", "bulk"} {
		t.Run(class, func(t *testing.T) {

			for _, mode := range []string{"accepted", "unknown-after-effect", "unknown-before-effect", "early-queue-full"} {
				t.Run(mode, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					cipher := cleanupFixtureEpoch(t)
					w, err := wallet.OpenBound(filepath.Join(t.TempDir(), "wallet.db"), wire.ID{1}, cipher)
					if err != nil {
						t.Fatal(err)
					}
					defer w.Close()
					key := wire.QuotaKey{Store: wire.ID{1}, Epoch: 1, Grant: 1, Session: w.Session(), User: "12345678", Account: "a01"}
					limit := wire.Quota{Bytes: fastLeaseBytes, Messages: fastLeaseMessages}
					if err = w.Accept(wire.StorageGrant{Key: key, Limit: limit}); err != nil {
						t.Fatal(err)
					}
					cfg := datapath.DefaultConfig(1, []string{"a01"})
					engine, err := datapath.New(ctx, cfg)
					if err != nil {
						t.Fatal(err)
					}
					defer engine.Stop()
					peer, err := datapath.New(ctx, cfg)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Stop()
					if _, err = engine.Activate(1, 1); err != nil {
						t.Fatal(err)
					}
					window, err := peer.Activate(1, 1)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = engine.Receive(1, window); err != nil {
						t.Fatal(err)
					}
					payload := []byte("short interactive packet")
					if class == "bulk" {
						payload = bytes.Repeat([]byte{0x6d}, 32<<10)
					}
					id, err := engine.Queue(1, 1, payload)
					if err != nil {
						t.Fatal(err)
					}
					frame, err := engine.Frame(1, id)
					if err != nil {
						t.Fatal(err)
					}
					size, err := datapath.Size(frame, &key)
					if err != nil {
						t.Fatal(err)
					}
					job, err := engine.Begin(1, id, "a01", size, 0)
					if err != nil {
						t.Fatal(err)
					}
					m := &delayedUsageAppend{memoryMail: &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}, entered: make(chan struct{}), release: make(chan struct{}), unknown: mode == "unknown-after-effect", unknownBefore: mode == "unknown-before-effect"}
					m.Ensure(ctx, "itom-u-up")
					l := &lane{ctx: ctx, account: servercfg.Account{ID: "a01"}, user: servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}, sendFolder: "itom-u-up", cipher: cipher, wallet: w, mail: m, urgentQueue: make(chan queuedControl, 1), controlQueue: make(chan queuedControl, 8), grants: make(chan wire.QuotaKey, 32), fastGrants: make(chan wire.QuotaKey, 1), grantLimits: map[wire.QuotaKey]wire.Quota{key: limit}, grantFast: map[wire.QuotaKey]bool{key: class == "fast"}, remote: map[wire.QuotaKey][]remoteLogicalAttempt{}}
					if mode == "early-queue-full" {
						l.urgentQueue <- queuedControl{Frame: wire.Frame{Type: wire.Ping}}
					}
					done := make(chan []datapath.Completion, 1)
					go func() { done <- l.writeBatchPhysicalReserved(l, []*datapath.Job{job}, &key) }()
					select {
					case <-m.entered:
					case <-ctx.Done():
						t.Fatal("physical APPEND did not start")
					}
					entries, err := w.Entries()
					if err != nil || len(entries) != 1 || entries[0].Used.Messages != 1 || len(entries[0].Pending) != 1 {
						close(m.release)
						<-done
						t.Fatal("descriptor preceded durable spend", entries, err)
					}
					if len(l.fastGrants) != 0 || len(l.grants) != 0 {
						close(m.release)
						<-done
						t.Fatal("live physical write requeued exclusive grant")
					}
					queued := <-l.urgentQueue
					if mode == "early-queue-full" {
						if queued.Type != wire.Ping {
							t.Fatal("unexpected early fallback", queued.Type)
						}
					} else {
						msg, err := wire.DecodeControl(queued.Frame)
						if err != nil {
							t.Fatal(err)
						}
						usage, ok := msg.(wire.UsageReport)
						if !ok || usage.Key != key || usage.Total != entries[0].Used {
							t.Fatal("early descriptor was not exact durable usage", msg)
						}
					}
					l.mu.Lock()
					registered := len(l.remote[key])
					l.mu.Unlock()
					if registered != 1 {
						close(m.release)
						<-done
						t.Fatal("early cleanup could precede logical registration", registered)
					}
					close(m.release)
					result := <-done
					returned := len(l.grants)
					if class == "fast" {
						returned = len(l.fastGrants)
					}
					if len(result) != 1 || m.calls.Load() != 1 || returned != 1 {
						t.Fatal("APPEND repeated or grant lost", result, m.calls.Load(), len(l.fastGrants))
					}
					if mode == "unknown-after-effect" || mode == "unknown-before-effect" {
						if result[0].Outcome != flow.CommitUnknown || !result[0].NonFatal {
							t.Fatal("unknown APPEND relabeled", result)
						}
					} else if result[0].Err != nil || result[0].Outcome != flow.Committed {
						t.Fatal(result)
					}
					if mode == "early-queue-full" {
						msg, err := wire.DecodeControl((<-l.urgentQueue).Frame)
						if err != nil {
							t.Fatal(err)
						}
						if _, ok := msg.(wire.UsageReport); !ok {
							t.Fatal("descriptor fallback lost", msg)
						}
					}
					if len(l.urgentQueue) != 0 {
						t.Fatal("successful early descriptor repeated")
					}
					after, err := w.Entries()
					if err != nil || after[0].Used != entries[0].Used || len(after[0].Pending) != 1 {
						t.Fatal("completion refunded unproved physical debt", after, err)
					}
					if err = engine.Complete(1, result[0].Attempt, result[0].Outcome); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
}

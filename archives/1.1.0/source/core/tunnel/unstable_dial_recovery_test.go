package tunnel

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"itom.local/core/mailbox"
)

// Emulates a provider which drops new TCP/IMAP sessions and then comes back.
// Crucially, no APPEND is even attempted during the disconnection: the queued
// physical request must survive rather than create an uncertain journal debt.
func TestUnstableDataDialRetryAfterNetworkRestored(t *testing.T) {
	var attempts atomic.Int32
	writer := &countingAppendClient{}
	r := &accountRuntime{dial: func(context.Context) (appendClient, error) {
		if attempts.Add(1) <= 2 {
			return nil, syscall.ENETUNREACH
		}
		return writer, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	start := time.Now()
	recovered, err := r.waitDataWriter(ctx, false)
	if err != nil || recovered != writer {
		t.Fatalf("transient outage was not retried: dial=%d err=%v", attempts.Load(), err)
	}
	if attempts.Load() != 3 {
		t.Fatalf("unexpected dial retry count %d", attempts.Load())
	}
	if time.Since(start) < time.Second {
		t.Fatal("network outage retried without paced backoff")
	}
	if writer.appended.Load() != 0 {
		t.Fatal("data was APPENDed during failed dial")
	}
}

func TestDataRepairJitterRespectsBackoffCeiling(t *testing.T) {
	for i := 0; i < 512; i++ {
		var firstThree time.Duration
		for _, backoff := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 30 * time.Second} {
			delay := dataRepairDelay(backoff)
			if delay < backoff-backoff/5 || delay > backoff {
				t.Fatalf("jitter exceeded paced backoff bounds: %v %v", backoff, delay)
			}
			if backoff <= 4*time.Second {
				firstThree += delay
			}
		}
		if firstThree > 7*time.Second {
			t.Fatal("first three retries exceed recovery budget", firstThree)
		}
	}
	if dataRepairDelay(0) != 0 {
		t.Fatal("zero delay changed")
	}
}

type countingAppendClient struct{ appended atomic.Int32 }

func (c *countingAppendClient) Append(context.Context, string, []byte) error {
	c.appended.Add(1)
	return nil
}
func (c *countingAppendClient) Close() error { return nil }

func TestUnstableDialPolicyRejectsPermanentErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"permission", syscall.EACCES},
		{"not_found", &net.DNSError{IsNotFound: true, Name: "invalid.test"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var count int
			r := &accountRuntime{dial: func(context.Context) (appendClient, error) { count++; return nil, tc.err }}
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			_, err := r.waitDataWriter(ctx, false)
			if !errors.Is(err, tc.err) || count != 1 {
				t.Fatalf("permanent failure retried: count=%d err=%v", count, err)
			}
		})
	}
}

func TestUnstableDialPolicyCancellationBounded(t *testing.T) {
	r := &accountRuntime{dial: func(context.Context) (appendClient, error) { return nil, syscall.ENETUNREACH }}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	_, err := r.waitDataWriter(ctx, false)
	if !errors.Is(err, context.Canceled) || time.Since(started) > 100*time.Millisecond {
		t.Fatalf("cancelled retry loop stalled: %v", err)
	}
}

func TestUnstableAccountGatePacesUnreachableAndTemporary(t *testing.T) {
	for _, code := range []error{syscall.ENETUNREACH, syscall.EHOSTUNREACH, syscall.ECONNRESET} {
		gate := newAccountDialGate()
		count := 0
		err := gate.run(context.Background(), func() error { count++; return code })
		if !errors.Is(err, code) || count != 1 {
			t.Fatal("wrong dial result", err)
		}
		gate.mu.Lock()
		next, delay := gate.next, gate.delay
		gate.mu.Unlock()
		if delay != time.Second || time.Until(next) <= 0 {
			t.Fatalf("unreachable endpoint has no cooldown: %v %v", delay, next)
		}
	}
	if !transientIMAPDialReason("temporary") || transientIMAPDialReason("permission") || transientIMAPDialReason("certificate_name") || transientIMAPDialReason("rejected") {
		t.Fatal("transient dial classification unsafe")
	}
	if mailbox.FailureReason(syscall.EACCES) != "permission" {
		t.Fatal("unexpected certificate/permission classifier")
	}
}

// An admitted DATA job occupies a bounded physical slot while the provider
// rejects logins. It must be delivered once when the endpoint returns, with
// no phantom physical APPEND and no leaked work outstanding.
func TestUnstableAccountRuntimePreservesAlreadyAdmittedDATA(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var attempts atomic.Int32
	writer := &countingAppendClient{}
	r := newAccountRuntime(ctx, []string{"u1"}, func(context.Context) (appendClient, error) {
		if attempts.Add(1) <= 2 {
			return nil, syscall.ECONNREFUSED
		}
		return writer, nil
	})
	defer r.Close()
	result := make(chan error, 2)
	go func() { result <- r.Append(ctx, "u1", "inbox", []byte("first")) }()
	go func() { result <- r.Append(ctx, "u1", "inbox", []byte("second")) }()
	for i := 0; i < 2; i++ {
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("transient pre-append dial rejected durable request: %v", err)
			}
		case <-ctx.Done():
			t.Fatal("bounded APPEND queue never recovered", ctx.Err())
		}
	}
	snap := r.snapshot()
	if writer.appended.Load() != 2 || snap.Completed != 2 || snap.Failed != 0 || snap.PendingBytes != 0 || snap.BusyWorkers != 0 || snap.QueuedRequests != 0 {
		t.Fatalf("physical DATA disappeared/duplicated during outage: appended=%d state=%+v", writer.appended.Load(), snap)
	}
}

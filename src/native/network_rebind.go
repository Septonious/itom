package mobile

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"itom.local/core/socks"
	"itom.local/core/tunnel"
	"itom.local/core/wire"
)

// The pool slot owns one transport generation. Taking a snapshot of the
// pointer never holds a lock over provider I/O or waits for the old session.
type transportPool interface {
	Close() error
	Stats() tunnel.Stats
	Snapshot() tunnel.ClientSnapshot
	Failure() string
	Diagnostics() string
	Probe() tunnel.ProbeSnapshot
	Open(context.Context, wire.Connect) (socks.Stream, netip.AddrPort, error)
	PingAccount(context.Context, string) (time.Duration, error)
	RecordNetworkEvent(string, int64)
	RecordApplicationCopyEvent(uint64, string, int64, error, string)
}

var _ transportPool = (*tunnel.Client)(nil)

func (c *Client) currentPool() transportPool {
	c.poolMu.RLock()
	defer c.poolMu.RUnlock()
	return c.pool
}
func (c *Client) swapPool(next transportPool) transportPool {
	c.poolMu.Lock()
	defer c.poolMu.Unlock()
	old := c.pool
	c.pool = next
	return old
}

// RebindNetwork requests a fresh provider generation on Android's newly
// selected underlying network. Host.Protect must protect AND bind every new
// socket to that network. This method returns immediately to the caller.
// Existing TUN, SOCKS listener, routing policy and fake-IP database survive;
// established tunneled TCP flows cannot survive the fresh Noise session.
// Never fall back to direct for a VPN verdict during the outage.
func (c *Client) RebindNetwork() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.rebindRequests == nil {
		return
	}
	c.rebinding.Store(true)
	c.rebindFailure.Store(nil)
	c.rebindEpoch++
	if c.rebindCancel != nil {
		c.rebindCancel()
		c.rebindCancel = nil
	}
	select {
	case c.rebindRequests <- struct{}{}:
	default: // already queued
	}
}

func (c *Client) startNetworkRebind() {
	c.rebindRequests = make(chan struct{}, 1)
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer c.rebinding.Store(false)
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-c.rebindRequests:
			}
			// Collapse the common burst of Android available/capabilities callbacks.
			if !waitRebind(c.ctx, 250*time.Millisecond) {
				return
			}
			c.mu.Lock()
			select {
			case <-c.rebindRequests:
			default:
			}
			epoch := c.rebindEpoch
			c.mu.Unlock()
			if c.ctx.Err() != nil {
				return
			}
			err := c.reconnectTransport(epoch)
			c.mu.Lock()
			// Another network change received during login must be serviced rather
			// than incorrectly marking the new network as ready.
			if c.rebindEpoch == epoch {
				c.rebinding.Store(false)
				if err != nil && c.ctx.Err() == nil {
					reason := "NETWORK_REBIND_FAILED"
					c.rebindFailure.Store(&reason)
				}
			}
			c.mu.Unlock()
		}
	}()
}

func waitRebind(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// Backoff is bounded, but retry count is not: an outage may outlast three
// 45-second startup attempts. The foreground VPN is kept fail-closed until
// a generation succeeds, the network changes again, or the user stops it.
func rebindBackoff(attempt int) time.Duration {
	return time.Duration(min(attempt, 5)) * time.Second
}

func (c *Client) reconnectTransport(epoch uint64) error {
	old := c.swapPool(nil)
	if old != nil {
		old.RecordNetworkEvent("NETWORK_REBIND_BEGIN", 0)
		_ = old.Close()
	}
	if c.startTransport == nil {
		return errors.New("NETWORK_REBIND_UNAVAILABLE")
	}
	// This context belongs to the bearer generation, not a single attempt.
	// A newer callback cancels both login and retry backoff immediately.
	c.mu.Lock()
	if c.closed || c.ctx.Err() != nil || c.rebindEpoch != epoch {
		c.mu.Unlock()
		return context.Canceled
	}
	life, cancel := context.WithCancel(c.ctx)
	c.rebindCancel = cancel
	c.mu.Unlock()
	keep := false
	defer func() {
		if !keep {
			cancel()
		}
	}()
	for attempt := 0; ; attempt = min(attempt+1, 5) {
		if !waitRebind(life, rebindBackoff(attempt)) || life.Err() != nil {
			return context.Canceled
		}
		fresh, err := c.startTransport(life)
		if err != nil || fresh == nil {
			if fresh != nil {
				_ = fresh.Close()
			}
			continue
		}
		c.mu.Lock()
		if c.closed || life.Err() != nil || c.rebindEpoch != epoch {
			c.mu.Unlock()
			_ = fresh.Close()
			return context.Canceled
		}
		// Set policy before making this generation visible to application opens.
		if kind := c.networkType.Load(); kind != nil {
			if pool, ok := fresh.(interface{ SetNetworkType(string) }); ok {
				pool.SetNetworkType(*kind)
			}
		}
		c.swapPool(fresh)
		c.rebindFailure.Store(nil)
		keep = true
		c.mu.Unlock()
		fresh.RecordNetworkEvent("NETWORK_REBIND_READY", 0)
		if c.networkType.Load() != nil {
			fresh.RecordNetworkEvent("NETWORK_BEARER_RESTORED", 0)
		}
		return nil
	}
}

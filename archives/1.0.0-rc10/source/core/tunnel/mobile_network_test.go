package tunnel

import (
	"math"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// This is a bounded reliable-stream impairment model, not kernel TCP/netem.
// Segments are 1400 bytes. Lost segments are retransmitted after one simulated
// RTT (parallel within each chunk), keeping all bytes and their original order.
// It models propagation, retransmission and wire cost, not TCP congestion control.
type mobileLinkProfile struct {
	RTT               time.Duration
	LossPercent, Mbps float64
	Seed              int64
	SharedMedium      bool
}
type mobileLinkState struct {
	profile                  atomic.Pointer[mobileLinkProfile]
	uplink, downlink, shared providerRateLane
	segments, lost, retries  atomic.Uint64
}

func (m *mobileLinkState) set(profile mobileLinkProfile) bool {
	if profile.RTT < 0 || profile.RTT > time.Second || profile.Mbps < 0 || math.IsNaN(profile.Mbps) || math.IsInf(profile.Mbps, 0) || profile.LossPercent < 0 || profile.LossPercent > 5 || math.IsNaN(profile.LossPercent) {
		return false
	}
	m.uplink.setMbps(profile.Mbps)
	m.downlink.setMbps(profile.Mbps)
	m.shared.setMbps(profile.Mbps)
	m.profile.Store(&profile)
	return true
}

type mobileRelay struct {
	net.Conn
	raw, bridge net.Conn
	owner       *providerTestConn
	done        chan struct{}
	once        sync.Once
	wg          sync.WaitGroup
}
type mobileChunk struct {
	body       []byte
	at         time.Time
	retryBytes int
}

func newMobileRelay(raw net.Conn, owner *providerTestConn) *mobileRelay {
	application, bridge := net.Pipe()
	r := &mobileRelay{Conn: application, raw: raw, bridge: bridge, owner: owner, done: make(chan struct{})}
	r.copyDirection(raw, bridge, true)
	r.copyDirection(bridge, raw, false)
	return r
}
func (r *mobileRelay) Close() error {
	r.once.Do(func() {
		close(r.done)
		if r.owner.closed.CompareAndSwap(false, true) && r.owner.done != nil {
			close(r.owner.done)
		}
		r.owner.network.unregister(r.owner.id)
		r.Conn.Close()
		r.bridge.Close()
		r.raw.Close()
	})
	return nil
}

// Endpoint role selects a separate access link. Provider traffic is not
// charged twice to one artificial phone/server shared bottleneck.
func (r *mobileRelay) pathState() *mobileLinkState {
	if r.owner.mobileClient.Load() {
		return &r.owner.network.mobile
	}
	if r.owner.network.healthyEndpoints {
		return &r.owner.network.serverPath
	}
	return nil
}
func (r *mobileRelay) copyDirection(src, dst net.Conn, up bool) {
	queue := make(chan mobileChunk, 1024) // <=32MiB per direction; covers 600Mbps/300ms BDP and a full bounded FETCH page
	r.wg.Add(2)
	go func() {
		defer r.wg.Done()
		defer close(queue)
		seed := int64(r.owner.id) * 1000003
		if !up {
			seed += 17011
		}
		rng := rand.New(rand.NewSource(seed))
		var appliedSeed int64
		buf := make([]byte, 32<<10)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				chunk := mobileChunk{body: append([]byte(nil), buf[:n]...), at: time.Now()}
				m := r.pathState()
				if m != nil && m.profile.Load() != nil {
					profile := m.profile.Load()
					if appliedSeed != profile.Seed {
						appliedSeed = profile.Seed
						rng = rand.New(rand.NewSource(seed + profile.Seed))
					}
					chunk.at = chunk.at.Add(profile.RTT / 2)
					retransmissions := 0
					for offset := 0; offset < n; offset += 1400 {
						size := min(1400, n-offset)
						m.segments.Add(1)
						attempts := 0
						for rng.Float64()*100 < profile.LossPercent && attempts < 16 {
							attempts++
							m.lost.Add(1)
							m.retries.Add(1)
							chunk.retryBytes += size
						}
						retransmissions = max(retransmissions, attempts)
					}
					chunk.at = chunk.at.Add(time.Duration(retransmissions) * profile.RTT)
				}
				select {
				case queue <- chunk:
				case <-r.done:
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		defer r.wg.Done()
		defer r.Close()
		for {
			select {
			case chunk, ok := <-queue:
				if !ok {
					return
				}
				if delay := time.Until(chunk.at); delay > 0 && !mobileWait(delay, r.done) {
					return
				}
				if m := r.pathState(); m != nil && m.profile.Load() != nil {
					rate := &m.downlink
					if up {
						rate = &m.uplink
					}
					if profile := m.profile.Load(); profile != nil && profile.SharedMedium {
						rate = &m.shared
					}
					if !rate.waitUntilAt(len(chunk.body)+chunk.retryBytes, chunk.at, r.done) {
						return
					}
				}
				for body := chunk.body; len(body) > 0; {
					n, err := dst.Write(body)
					if err != nil || n <= 0 {
						return
					}
					body = body[n:]
				}
			case <-r.done:
				return
			}
		}
	}()
}
func mobileWait(delay time.Duration, done <-chan struct{}) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-done:
		return false
	}
}

func (p mobileLinkProfile) medium() string {
	if p.SharedMedium {
		return "aggregate-shared-medium"
	}
	return "full-duplex-account-shared"
}

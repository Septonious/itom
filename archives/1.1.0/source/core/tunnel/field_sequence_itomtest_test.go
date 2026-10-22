//go:build itomtest

package tunnel

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

// Reinstall left one field account with many separate historical grants, not
// merely one large reservation. No wallet exists and no zero-spend fence is made.
func fieldSequenceHistory(t *testing.T) func(string, string, servercfg.Config, servercfg.User) {
	return func(serverDir, _ string, cfg servercfg.Config, u servercfg.User) {
		for _, a := range cfg.Accounts {
			if a.ID != "a10" {
				continue
			}
			dir := filepath.Join(serverDir, a.ID, u.ID)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			s, err := journal.Open(filepath.Join(dir, "data.db"), ledgerConfig(a, u, dataProfile12), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 64; i++ {
				if _, err = s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{99}}, pool.Amount{Bytes: 64 << 10, Messages: 1}); err != nil {
					t.Fatal(err)
				}
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// Complement the unchanged sixty-second duplex gates with three short
// download->upload runs, no warmup, reused endpoint state, startup deadlines,
// half-second byte clocks and verified application payloads in both directions.
func TestFieldSpeedtestSequence(t *testing.T) {
	t.Setenv("ITOM_HEALTHY_SERVER", "1")
	t.Setenv("ITOM_LOAD_COMMAND_DELAY_MS", "0")
	streams := loadEnvInt(t, "ITOM_FIELD_SEQUENCE_STREAMS", 1, 1, 32)
	var f *productionSupervisorFixture
	if os.Getenv("ITOM_FIELD_EARLY_START") == "1" {
		f = newProductionSupervisorFixtureState(t, 15, fieldSequenceHistory(t), func(d Dial) Dial { return d })
		f.waitLane(t, "a01", 0) // start with one working line, not an all-account warmup
	} else {
		f = newProductionSupervisorFixtureState(t, 15, fieldSequenceHistory(t))
	}
	logical, generation := f.client.logicalSession, f.client.generation
	for run := 1; run <= 3; run++ {
		for _, upload := range []bool{false, true} {
			t.Run(fmt.Sprintf("run%d-upload%t", run, upload), func(t *testing.T) {
				target := newProductionLoadTarget(t, fmt.Sprintf("%x/field/%d/%t", logical, run, upload))
				var workers sync.WaitGroup
				var stop atomic.Bool
				failures := make(chan error, streams+1)
				var sockets []net.Conn
				defer func() {
					stop.Store(true)
					target.close()
					for _, p := range sockets {
						p.Close()
					}
					workers.Wait()
				}()
				open := func(dst wire.Connect) net.Conn {
					ctx, cancel := context.WithTimeout(f.ctx, 1500*time.Millisecond)
					defer cancel()
					at := time.Now()
					c, _, err := f.client.Open(ctx, dst)
					if err != nil {
						t.Fatal("short startup", run, upload, err)
					}
					p, ok := c.(net.Conn)
					if !ok {
						c.Close()
						t.Fatal("no stream deadlines")
					}
					sockets = append(sockets, p)
					t.Logf("field_open run=%d upload=%t ms=%.3f", run, upload, float64(time.Since(at))/float64(time.Millisecond))
					return p
				}
				probe := open(target.echoConnect())
				if err := rev4Exchange(probe, run, 32, 750*time.Millisecond); err != nil {
					t.Fatal("light request before phase", err)
				}
				for index := 0; index < streams; index++ {
					p := open(target.bulkConnect(upload, index))
					workers.Add(1)
					go func(p net.Conn, index int) {
						defer workers.Done()
						if upload {
							<-target.startCh
							pattern := newProductionPattern(target.streamIdentity(true, index))
							buf := make([]byte, target.chunkBytes)
							for !stop.Load() {
								pattern.fill(buf)
								if err := productionWriteFull(p, buf); err != nil {
									if !stop.Load() {
										failures <- err
									}
									return
								}
							}
						} else if err := productionReadVerifiedObserver(p, target.streamIdentity(false, index), &stop, &target.downloadDelivered, func(n int) { target.recordWindow(false, n) }); err != nil && !stop.Load() {
							failures <- err
						}
					}(p, index)
				}
				target.startBulk()
				result, err := productionMeasure(f.ctx, productionMeasureOptions{Duration: 12 * time.Second, Cadence: 2 * time.Second, ProbeOffset: time.Second, BandwidthCadence: 500 * time.Millisecond, ProbeDeadline: 1500 * time.Millisecond}, []*productionLoadTarget{target}, []net.Conn{probe}, f.carrierExits.failures, failures, t.Logf)
				if err != nil {
					t.Fatal("short transfer", run, upload, err)
				}
				var first, last int64
				zeros := 0
				for _, w := range result.Bandwidth {
					n := w.Download
					if upload {
						n = w.Upload
					}
					if w.Index <= 4 {
						first += n
					}
					if w.Index > 20 {
						last += n
					}
					if w.Index > 4 && n == 0 {
						zeros++
					}
					t.Logf("field_window run=%d upload=%t index=%d duration_ms=%d bytes=%d", run, upload, w.Index, w.Duration.Milliseconds(), n)
				}
				n := result.Download[0]
				if upload {
					n = result.Upload[0]
				}
				rate := float64(n*8) / result.Elapsed.Seconds() / 1e6
				t.Logf("field_sequence variant=%s streams=%d run=%d upload=%t useful_mbps=%.6f first2_mbps=%.6f last2_mbps=%.6f empty_half_seconds=%d provider_sensitivity=%s original_duplex_gates_unchanged=true field_qualified=false", os.Getenv("ITOM_EXPERIMENT_LABEL"), streams, run, upload, rate, float64(first*8)/2/1e6, float64(last*8)/2/1e6, zeros, os.Getenv("ITOM_FIELD_COMMAND_SERVICE"))
				if zeros > 0 {
					t.Error("half-second useful-delivery stalls after first two seconds", run, upload, zeros)
				}
				if rate < 100 {
					t.Error("short phase below existing100Mbps goal", run, upload, rate)
				}
				if err = target.firstError(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
	if f.client.logicalSession != logical || f.client.generation != generation {
		t.Fatal("sequential runs replaced logical session")
	}
}

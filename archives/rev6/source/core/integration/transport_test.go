// These are deterministic fault-injection tests of real core packages.
// There is deliberately no simulated backend in the Android application.
package integration

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"itom.local/core/flow"
	"itom.local/core/recovery"
	"itom.local/core/secure"
	"itom.local/core/wire"
)

func epochs(t *testing.T) (*secure.Epoch, *secure.Epoch) {
	t.Helper()
	binding := secure.Binding{Server: wire.ID{1}, User: "12345678", Pair: wire.ID{2},
		Session: wire.ID{3}, Handshake: wire.ID{4}, Epoch: 1, ConfigHash: [32]byte{5}}
	psk := bytes.Repeat([]byte{9}, 32)
	a, err := secure.NewHandshake(binding, psk, true)
	if err != nil {
		t.Fatal(err)
	}
	b, err := secure.NewHandshake(binding, psk, false)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := a.Write(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Read(msg); err != nil {
		t.Fatal(err)
	}
	msg, err = b.Write(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Read(msg); err != nil {
		t.Fatal(err)
	}
	left, err := a.Epoch()
	if err != nil {
		t.Fatal(err)
	}
	right, err := b.Epoch()
	if err != nil {
		t.Fatal(err)
	}
	return left, right
}
func TestEncryptedDeliveryWithFaults(t *testing.T) {
	for seed := int64(0); seed < 16; seed++ {
		checkScenarioBoundary(t, fmt.Sprint(seed), func(t *testing.T) {
			left, right := epochs(t)
			tx, err := flow.NewTx(flow.DefaultLimits(), map[string]uint64{"a": 1 << 20, "b": 1 << 20})
			if err != nil {
				t.Fatal(err)
			}
			rx, err := flow.NewRx(16<<20, 128, 4096)
			if err != nil {
				t.Fatal(err)
			}
			for _, err := range []error{tx.OpenStream(1), tx.ConnectionLimit(16 << 20), tx.StreamLimit(1, 1<<20), rx.OpenStream(1, 1<<20)} {
				if err != nil {
					t.Fatal(err)
				}
			}
			count := 8
			if seed == 0 {
				count = 1
			} // first, last and only-record loss
			drop := int(seed) % count
			var expected, sink []byte
			var packets [][]byte
			var attempts []flow.Attempt
			for i := 0; i < count; i++ {
				payload := bytes.Repeat([]byte{byte(i + 1)}, 1024)
				expected = append(expected, payload...)
				id, err := tx.Admit(1, payload)
				if err != nil {
					t.Fatal(err)
				}
				if err = tx.Reserve(id, "a"); err != nil {
					t.Fatal(err)
				}
				a, err := tx.Begin(id, 2048)
				if err != nil {
					t.Fatal(err)
				}
				attempts = append(attempts, a)
				frame, err := tx.Frame(id)
				if err != nil {
					t.Fatal(err)
				}
				packet, err := left.Seal(frame)
				if err != nil {
					t.Fatal(err)
				}
				packets = append(packets, packet)
			}
			deliver := func(packet []byte) {
				f, err := right.Open(packet)
				if err != nil {
					t.Fatal(err)
				}
				frontier, _, err := rx.Receive(f)
				if err != nil {
					t.Fatal(err)
				}
				// Even ACKs go through the actual encrypted reverse direction.
				ackFrame, err := wire.EncodeControl(1, wire.Received{Frontier: frontier})
				if err != nil {
					t.Fatal(err)
				}
				ack, err := right.Seal(ackFrame)
				if err != nil {
					t.Fatal(err)
				}
				receipt, err := left.Open(ack)
				if err != nil {
					t.Fatal(err)
				}
				receiptMessage, err := wire.DecodeControl(receipt)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = tx.Receipt(1, receiptMessage.(wire.Received).Frontier); err != nil {
					t.Fatal(err)
				}
				for {
					p, err := rx.Peek(1, 127)
					if err != nil {
						t.Fatal(err)
					}
					if len(p) == 0 {
						break
					}
					sink = append(sink, p...)
					if err = rx.Delivered(1, uint64(len(p))); err != nil {
						t.Fatal(err)
					}
				}
				if err = tx.Check(); err != nil {
					t.Fatal(err)
				}
				if err = rx.Check(); err != nil {
					t.Fatal(err)
				}
			}
			order := rand.New(rand.NewSource(seed)).Perm(count)
			for _, i := range order {
				if i == drop {
					if err = tx.Complete(attempts[i].ID, flow.CommitUnknown); err != nil {
						t.Fatal(err)
					}
					continue
				}
				deliver(packets[i]) // receiver may get bytes before APPEND response
				if _, err = right.Open(packets[i]); err != secure.ErrReplay {
					t.Fatal("replayed ciphertext accepted", err)
				}
				if err = tx.Cleanup(attempts[i].ID); err != nil {
					t.Fatal(err)
				}
				if err = tx.Complete(attempts[i].ID, flow.Committed); err != nil {
					t.Fatal(err)
				}
			}
			watch, err := recovery.New(0, 30*time.Second, time.Second, 3)
			if err != nil {
				t.Fatal(err)
			}
			if action, err := watch.Poll(time.Second); err != nil || action != recovery.ProbeStatus {
				t.Fatal(action, err)
			}
			// Discover the gap from an authenticated STATUS; the harness no
			// longer supplies the missing offset to receiver recovery.
			high, err := tx.AttemptedEnd(1)
			if err != nil {
				t.Fatal(err)
			}
			status, err := wire.EncodeControl(1, wire.Progress{Attempted: high})
			if err != nil {
				t.Fatal(err)
			}
			packet, err := left.Seal(status)
			if err != nil {
				t.Fatal(err)
			}
			status, err = right.Open(packet)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := wire.DecodeControl(status)
			if err != nil {
				t.Fatal(err)
			}
			holes, err := rx.Missing(1, decoded.(wire.Progress).Attempted, wire.MaxNackRanges)
			if err != nil || len(holes) != 1 {
				t.Fatal(holes, err)
			}
			nack, err := wire.EncodeControl(1, wire.Missing{ID: 1, Ranges: holes})
			if err != nil {
				t.Fatal(err)
			}
			packet, err = right.Seal(nack)
			if err != nil {
				t.Fatal(err)
			}
			nack, err = left.Open(packet)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err = wire.DecodeControl(nack)
			if err != nil {
				t.Fatal(err)
			}
			missing := decoded.(wire.Missing)
			r := recovery.NewRepair()
			if !r.Nack(missing.ID) || r.Nack(missing.ID) {
				t.Fatal("repair idempotency")
			}
			a := attempts[drop]
			repair, err := tx.BeginRepair(a.Item, "b", 2048, missing.Ranges[0].Start, missing.Ranges[0].End)
			if err != nil {
				t.Fatal(err)
			}
			frame, err := tx.Frame(a.Item)
			if err != nil {
				t.Fatal(err)
			}
			packet, err = left.Seal(frame)
			if err != nil {
				t.Fatal(err)
			}
			deliver(packet)
			if err = tx.Complete(repair.ID, flow.Committed); err != nil {
				t.Fatal(err)
			}
			for _, id := range []uint64{a.ID, repair.ID} {
				if err = tx.Cleanup(id); err != nil {
					t.Fatal(err)
				}
			}
			watch.Receipt()
			r.Complete()
			if !bytes.Equal(sink, expected) {
				t.Fatal("missing, reordered or duplicate application bytes")
			}
			if snapshot := tx.Snapshot(); snapshot.Logical != 0 || snapshot.PhysicalExposure != 0 || snapshot.Attempts != 0 {
				t.Fatal(snapshot)
			}
			if err = tx.Check(); err != nil {
				t.Fatal(err)
			}
			if err = rx.Check(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

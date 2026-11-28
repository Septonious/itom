package v160

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func ledger(t *testing.T) (*Registry, Lease, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "registry.db")
	r, e := OpenRegistry(p)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.RegisterUser("abcd"); e != nil {
		t.Fatal(e)
	}
	l, e := r.Begin(Key{"abcd", 1})
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Activate(l); e != nil {
		t.Fatal(e)
	}
	return r, l, p
}
func makeSlot(t *testing.T, r *Registry, l Lease, kind Traffic, acct string) FolderLease {
	t.Helper()
	fl, e := r.Prepare(l, acct, Folder{l.Key, kind, 0})
	if e != nil {
		t.Fatal(e)
	}
	if e = r.ConfirmCreated(fl, 1, 0); e != nil {
		t.Fatal(e)
	}
	return fl
}
func sizes(n int) []int {
	s := make([]int, n)
	for i := range s {
		s[i] = 100
	}
	return s
}
func TestV160MailboxHardCaps(t *testing.T) {
	for _, kind := range []Traffic{DataUp, ControlUp} {
		t.Run(kind.String(), func(t *testing.T) {
			r, l, _ := ledger(t)
			defer r.Close()
			fl := makeSlot(t, r, l, kind, "acct")
			rs, e := r.Reserve(fl, sizes(kind.OrdinaryLimit()), false)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = r.Reserve(fl, sizes(1), false); !errors.Is(e, ErrCapacity) {
				t.Fatal("ordinary ceiling", e)
			}
			for i, res := range rs {
				if i%2 == 0 {
					e = r.UnknownAppend(res)
				} else {
					e = r.ConfirmAppend(res, 1, uint32(i+1))
				}
				if e != nil {
					t.Fatal(e)
				}
			}
			if e = r.CancelUnsent(rs[0]); e == nil {
				t.Fatal("unknown append refunded")
			}
			if kind.Capacity() > kind.OrdinaryLimit() {
				if _, e = r.Reserve(fl, sizes(128), true); e != nil {
					t.Fatal(e)
				}
			}
			if _, e = r.Reserve(fl, sizes(1), true); !errors.Is(e, ErrCapacity) {
				t.Fatal("hard cap", e)
			}
			u, e := r.Usage(fl)
			if e != nil || u.Messages != kind.Capacity() || u.Uncertain == 0 {
				t.Fatal(u, e)
			}
			if e = r.Seal(fl, true); e == nil {
				t.Fatal("sealed unreceipted live data")
			}
			if e = r.Fence(l); e != nil {
				t.Fatal(e)
			}
			if e = r.Release(l); e == nil {
				t.Fatal("released before cleanup")
			}
			if e = r.Seal(fl, true); e != nil {
				t.Fatal(e)
			}
			if e = r.VerifyDeleted(fl, false); e == nil {
				t.Fatal("refunded without absence proof")
			}
			if e = r.VerifyDeleted(fl, true); e != nil {
				t.Fatal(e)
			}
			if e = r.Release(l); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestV160RegistryMultiDeviceCrash(t *testing.T) {
	r, l, p := ledger(t)
	other, e := r.Begin(Key{"abcd", 2})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.Begin(l.Key); !errors.Is(e, ErrConflict) {
		t.Fatal("collision", e)
	}
	if e = r.Activate(other); e != nil {
		t.Fatal(e)
	}
	f1 := makeSlot(t, r, l, DataUp, "same")
	f2 := makeSlot(t, r, other, ControlDown, "same")
	rs, e := r.Reserve(f1, sizes(3), false)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.ConfirmAppend(rs[0], 1, 1); e != nil {
		t.Fatal(e)
	}
	if e = r.Fence(l); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Reserve(f2, sizes(1), false); e != nil {
		t.Fatal("other device affected", e)
	}
	if e = r.Close(); e != nil {
		t.Fatal(e)
	}
	r, e = OpenRegistry(p)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	u, e := r.Usage(f1)
	if e != nil || u.Confirmed != 1 || u.Uncertain != 2 {
		t.Fatal(u, e)
	}
	if _, e = r.Reserve(f1, sizes(1), false); e == nil {
		t.Fatal("stale producer writes")
	}
	if e = r.CancelUnsent(rs[1]); e == nil {
		t.Fatal("crash uncertainty refunded")
	}
	if _, e = r.Begin(l.Key); !errors.Is(e, ErrConflict) {
		t.Fatal("reused dirty session")
	}
	if e = r.Seal(f1, true); e != nil {
		t.Fatal(e)
	}
	if e = r.VerifyDeleted(f1, true); e != nil {
		t.Fatal(e)
	}
	if e = r.Release(l); e != nil {
		t.Fatal(e)
	}
	newL, e := r.Begin(l.Key)
	if e != nil || newL.Epoch == l.Epoch {
		t.Fatal(e)
	}
	if e = r.Fence(l); !errors.Is(e, ErrConflict) {
		t.Fatal("old epoch accepted", e)
	}
	u, e = r.Usage(f2)
	if e != nil || u.Uncertain != 1 {
		t.Fatal("other device cleanup", u, e)
	}
}
func TestV160ConcurrentReservation(t *testing.T) {
	r, l, _ := ledger(t)
	defer r.Close()
	fl := makeSlot(t, r, l, DataDown, "acct")
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 32 {
				_, e := r.Reserve(fl, []int{100}, false)
				if e != nil && !errors.Is(e, ErrCapacity) {
					t.Error(e)
				}
			}
		}()
	}
	wg.Wait()
	u, e := r.Usage(fl)
	if e != nil || u.Messages != 256 || u.Bytes != 25600 {
		t.Fatal(u, e)
	}
}
func TestV160SlotReuseFence(t *testing.T) {
	r, l, _ := ledger(t)
	defer r.Close()
	f := makeSlot(t, r, l, DataUp, "acct")
	rs, e := r.Reserve(f, []int{128}, false)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.ConfirmAppend(rs[0], 2, 1); !errors.Is(e, ErrConflict) {
		t.Fatal("UIDVALIDITY mismatch", e)
	}
	if e = r.ConfirmAppend(rs[0], 1, 1); e != nil {
		t.Fatal(e)
	}
	if e = r.Receipt(rs[0]); e != nil {
		t.Fatal(e)
	}
	if e = r.Seal(f, true); e != nil {
		t.Fatal(e)
	}
	if e = r.VerifyDeleted(f, true); e != nil {
		t.Fatal(e)
	}
	next := makeSlot(t, r, l, DataUp, "acct")
	if next.Generation == f.Generation {
		t.Fatal("slot generation reused")
	}
	if e = r.UnknownAppend(rs[0]); !errors.Is(e, ErrConflict) {
		t.Fatal("stale callback accepted", e)
	}
}

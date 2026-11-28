package v160

import (
	"math"
	"testing"
)

func TestV160FolderRoundTrip(t *testing.T) {
	for _, id := range []SessionID{1, 62, math.MaxUint32} {
		for kind := DataUp; kind <= ControlDown; kind++ {
			for slot := uint8(0); slot < 64; slot++ {
				f := Folder{Key{"a7K9", id}, kind, slot}
				n, e := f.Name()
				if e != nil || len(n) != 16 {
					t.Fatal(n, e)
				}
				got, e := ParseFolder(n)
				if e != nil || got != f {
					t.Fatal(got, e)
				}
			}
		}
	}
	for _, n := range []string{"a7K9-CU-000000-0", "a7K9-CD-zzzzzz-0", "a7K9-XX-000001-0", "a7-9-DD-000001-0", "a7K9-DU-000001-PK", "a7K9-DU-000001-/", "INBOX"} {
		if _, err := ParseFolder(n); err == nil {
			t.Fatalf("accepted %q", n)
		}
	}
}
func FuzzV160Folder(f *testing.F) {
	f.Add("a7K9-DU-000001--")
	f.Fuzz(func(t *testing.T, s string) {
		x, e := ParseFolder(s)
		if e == nil {
			n, e := x.Name()
			if e != nil || n != s {
				t.Fatal("noncanonical")
			}
		}
	})
}

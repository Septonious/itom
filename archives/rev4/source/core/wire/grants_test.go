package wire

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func grantCases() []controlCase {
	k := QuotaKey{Store: ID{1}, Epoch: 3, Grant: 9, Session: ID{2}, User: "12345678", Account: "mail01"}
	d := PhysicalDescriptor{Batch: ID{4}, EncodedBytes: 1000, DecodedBytes: 700, Digest: [32]byte{5}, Folder: "itom-up"}
	return []controlCase{
		{name: "grant", message: StorageGrant{Key: k, Limit: Quota{Bytes: 10000, Messages: 10}}},
		{name: "usage", message: UsageReport{Key: k, Total: Quota{Bytes: 2000, Messages: 2}}},
		{name: "descriptor", message: UsageReport{Key: k, Total: Quota{Bytes: 2000, Messages: 2}, Attempt: &d}},
		{name: "release", message: StorageRelease{Key: k, Final: Quota{Bytes: 2000, Messages: 2}}},
		{name: "release_empty", message: StorageRelease{Key: k}},
		{name: "gc", message: CleanupStatus{Key: k, Used: Quota{Bytes: 2000, Messages: 2}, Registered: Quota{Bytes: 2000, Messages: 2}, Released: Quota{Bytes: 1000, Messages: 1}, Closed: true}},
		{name: "proof_request", message: DescriptorRequest{Key: k, Next: 3}},
	}
}
func checkGrantControlGoldenVectors(t *testing.T) {
	raw, e := os.ReadFile("testdata/grants2.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixtures map[string]string
	if e = json.Unmarshal(raw, &fixtures); e != nil {
		t.Fatal(e)
	}
	if len(fixtures) != len(grantCases()) {
		t.Fatal("fixture set drift")
	}
	for _, c := range grantCases() {
		checkScenarioBoundary(t, c.name, func(t *testing.T) {
			f, e := EncodeControl(0, c.message)
			if e != nil {
				t.Fatal(e)
			}
			b, e := f.MarshalBinary()
			if e != nil || hex.EncodeToString(b) != fixtures[c.name] {
				t.Fatal(e, hex.EncodeToString(b))
			}
			decoded, e := DecodeControl(f)
			if e != nil || !reflect.DeepEqual(decoded, c.message) {
				t.Fatal(decoded, e)
			}
			for n := 0; n < len(f.Payload); n++ {
				bad := f
				bad.Payload = f.Payload[:n]
				if _, e := DecodeControl(bad); e == nil {
					t.Fatal("truncation", n)
				}
			}
			bad := f
			bad.Payload = append(append([]byte(nil), f.Payload...), 0)
			if _, e := DecodeControl(bad); e == nil {
				t.Fatal("trailing")
			}
			bad = f
			bad.Stream = 1
			if _, e := DecodeControl(bad); e == nil {
				t.Fatal("stream scope")
			}
		})
	}
}
func checkGrantControlRejectsInvalidAmountsAndMetadata(t *testing.T) {
	k := grantCases()[0].message.(StorageGrant).Key
	d := *grantCases()[2].message.(UsageReport).Attempt
	bad := []Control{
		StorageGrant{Key: k}, StorageGrant{Key: k, Limit: Quota{Bytes: 1<<40 + 1, Messages: 1}},
		UsageReport{Key: k, Total: Quota{Bytes: 1}}, UsageReport{Key: k, Total: Quota{Bytes: 1, Messages: 2}},
		UsageReport{Key: k, Total: Quota{Bytes: 1000, Messages: 1}, Attempt: &PhysicalDescriptor{}},
		CleanupStatus{Key: k, Registered: Quota{Bytes: 1000, Messages: 1}, Released: Quota{Bytes: 2000, Messages: 2}},
		DescriptorRequest{Key: k}, DescriptorRequest{Key: k, Next: 1<<24 + 1},
	}
	for _, folder := range []string{"", "INBOX", "a\x00b", string([]byte{255})} {
		copyD := d
		copyD.Folder = folder
		bad = append(bad, UsageReport{Key: k, Total: Quota{Bytes: 1000, Messages: 1}, Attempt: &copyD})
	}
	for _, m := range bad {
		if _, e := EncodeControl(0, m); e == nil {
			t.Fatalf("accepted %#v", m)
		}
	}
	for _, change := range []func(*QuotaKey){func(k *QuotaKey) { k.User = "02345678" }, func(k *QuotaKey) { k.User = "abcdefgh" }, func(k *QuotaKey) { k.Account = "a/b" }, func(k *QuotaKey) { k.Store = ID{} }, func(k *QuotaKey) { k.Session = ID{} }, func(k *QuotaKey) { k.Epoch = 0 }, func(k *QuotaKey) { k.Grant = 0 }} {
		copyK := k
		change(&copyK)
		if _, e := EncodeControl(0, StorageGrant{Key: copyK, Limit: Quota{Bytes: 1000, Messages: 1}}); e == nil {
			t.Fatal(copyK)
		}
	}
	var nilUsage *UsageReport
	if _, e := EncodeControl(0, nilUsage); e == nil {
		t.Fatal("typed nil")
	}
	f, _ := EncodeControl(0, UsageReport{Key: k, Total: Quota{Bytes: 1000, Messages: 1}, Attempt: &d})
	m, e := DecodeControl(f)
	if e != nil {
		t.Fatal(e)
	}
	clear(f.Payload)
	if !reflect.DeepEqual(m.(UsageReport).Attempt, &d) {
		t.Fatal("decoded data aliases input")
	}
}
func checkPhysicalBindingOwnershipAndBounds(t *testing.T) {
	k := grantCases()[0].message.(StorageGrant).Key
	batch := ID{4}
	raw, e := BindPhysical(k, 2, batch, []byte("encrypted"))
	if e != nil || !CheckPhysical(raw, k, 2, batch) {
		t.Fatal(e)
	}
	if CheckPhysical(raw, k, 1, batch) || CheckPhysical(raw, k, 2, ID{5}) {
		t.Fatal("aliased physical ownership")
	}
	other := k
	other.Grant++
	if CheckPhysical(raw, other, 2, batch) {
		t.Fatal("cross-grant proof")
	}
	if _, e = BindPhysical(k, 2, batch, make([]byte, 1<<20)); e == nil {
		t.Fatal("wrapper exceeds allocation bound")
	}
	if _, e = BindPhysical(k, 2, batch, nil); e == nil {
		t.Fatal("empty body")
	}
	for n := 0; n < len(raw)-len("encrypted")+1; n++ {
		if CheckPhysical(raw[:n], k, 2, batch) {
			t.Fatal("truncated binding", n)
		}
	}
}
func FuzzGrantControls(f *testing.F) {
	frame, _ := EncodeControl(0, GrantRecovery{Offer: grantCases()[0].message.(StorageGrant)})
	raw, _ := frame.MarshalBinary()
	f.Add(raw)
	for _, c := range grantCases() {
		frame, _ := EncodeControl(0, c.message)
		raw, _ := frame.MarshalBinary()
		f.Add(raw)
	}
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, raw []byte) {
		frame, e := ParseFrame(raw)
		if e != nil {
			return
		}
		message, e := DecodeControl(frame)
		if e != nil {
			return
		}
		out, e := EncodeControl(frame.Stream, message)
		if e != nil {
			t.Fatal(e)
		}
		encoded, e := out.MarshalBinary()
		if e != nil || !bytes.Equal(encoded, raw) {
			t.Fatal("noncanonical", e)
		}
	})
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioGrants(t *testing.T) {
	offer := grantCases()[0].message.(StorageGrant)
	f, err := EncodeControl(0, GrantRecovery{Offer: offer})
	if err != nil {
		t.Fatal(err)
	}
	c, err := DecodeControl(f)
	if err != nil || c.(GrantRecovery).Offer != offer {
		t.Fatal("recovery offer codec", c, err)
	}
	for n := 0; n < len(f.Payload); n++ {
		bad := f
		bad.Payload = f.Payload[:n]
		if _, err := DecodeControl(bad); err == nil {
			t.Fatal("truncated recovery", n)
		}
	}
	t.Log("check=checkGrantControlGoldenVectors")
	checkGrantControlGoldenVectors(t)
	t.Log("check=checkGrantControlRejectsInvalidAmountsAndMetadata")
	checkGrantControlRejectsInvalidAmountsAndMetadata(t)
	t.Log("check=checkPhysicalBindingOwnershipAndBounds")
	checkPhysicalBindingOwnershipAndBounds(t)
}

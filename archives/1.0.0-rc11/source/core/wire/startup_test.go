package wire

import (
	"reflect"
	"testing"
)

func TestStartupRetirementCodec(t *testing.T) {
	k := QuotaKey{Store: ID{1}, Epoch: 2, Grant: 3, Session: ID{4}, User: "12345678", Account: "a01"}
	for _, m := range []Control{Coordinator{Store: ID{1}, Run: 3, Features: 1}, Coordinator{Store: ID{1}, Run: 3, Features: 1, Ack: true}, Retirement{Key: k, Final: Quota{1000, 1}}, Retirement{Key: k, Ack: true}} {
		f, e := EncodeControl(0, m)
		if e != nil {
			t.Fatal(e)
		}
		got, e := DecodeControl(f)
		if e != nil || !reflect.DeepEqual(m, got) {
			t.Fatal(got, e)
		}
		f.Payload = append(f.Payload, 0)
		if _, e = DecodeControl(f); e == nil {
			t.Fatal("trailing data accepted")
		}
	}
	for _, m := range []Control{Coordinator{Store: ID{1}, Run: 3}, Coordinator{Run: 3, Features: 1}, Retirement{Key: k, Final: Quota{1, 2}}} {
		if _, e := EncodeControl(0, m); e == nil {
			t.Fatal("invalid startup accepted")
		}
	}
}

package wire

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

type controlCase struct {
	name    string
	stream  uint64
	message Control
}

func controlCases() []controlCase {
	return []controlCase{
		{"confirm", 0, Activation{Kind: Confirm}}, {"ready", 0, Activation{Kind: Ready}}, {"ready_ack", 0, Activation{Kind: ReadyAck}},
		{"open", 1, Connect{Host: "example.com", Port: 443}}, {"open_ipv6", 3, Connect{Host: "2001:db8::1", Port: 80}},
		{"open_ok", 1, ConnectResult{}}, {"open_error", 1, ConnectResult{Code: ReasonRefused}},
		{"receipt", 1, Received{Frontier: 123}}, {"window", 1, Credit{MaxStream: 4096, MaxData: 16384}},
		{"status", 1, Progress{Attempted: 100, Receipt: 20, MaxStream: 4096, MaxData: 16384}},
		{"status_final", 1, Progress{Attempted: 100, Receipt: 20, MaxStream: 4096, MaxData: 16384, HasFinal: true, Final: 120}},
		{"status_request", 1, Probe{}}, {"nack", 1, Missing{ID: 7, Ranges: []ByteRange{{0, 32}, {64, 96}}}},
		{"fin", 1, Finish{Final: 123}}, {"fin_ack", 1, Finish{Ack: true, Final: 123}},
		{"reset", 1, Abort{Final: 123, Code: ReasonNetwork}}, {"reset_ack", 1, Abort{Ack: true, Final: 123, Code: ReasonNetwork}},
		{"ping", 0, Echo{Token: 42}}, {"pong", 0, Echo{Reply: true, Token: 42}},
		{"session_close", 0, EndSession{}}, {"session_error", 0, EndSession{Failure: true, Code: ReasonProtocol}},
	}
}
func checkControlGoldenVectors(t *testing.T) {
	data, err := os.ReadFile("testdata/control2.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors map[string]string
	if err = json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) != len(controlCases()) {
		t.Fatal("fixture set drift")
	}
	for _, c := range controlCases() {
		checkScenarioBoundary(t, c.name, func(t *testing.T) {
			f, err := EncodeControl(c.stream, c.message)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := f.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			if hex.EncodeToString(encoded) != vectors[c.name] {
				t.Fatal("golden mismatch", hex.EncodeToString(encoded))
			}
			parsed, err := ParseFrame(encoded)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodeControl(parsed)
			if err != nil || !reflect.DeepEqual(decoded, c.message) {
				t.Fatal(decoded, err)
			}
		})
	}
}
func checkControlRejectsMalformedFields(t *testing.T) {
	cases := []Control{
		Activation{Kind: Data}, Connect{Host: "example.com", Port: 0}, Connect{Host: "https://example.com", Port: 443},
		Connect{Host: "Example.com", Port: 443}, Connect{Host: "127.0.0.01", Port: 443}, Connect{Host: "a..com", Port: 443}, Connect{Host: "-a.com", Port: 443},
		Connect{Host: "fe80::1%eth0", Port: 80}, Connect{Host: "127.0.0.1\x00", Port: 80},
		ConnectResult{Code: Reason(99)}, Echo{}, EndSession{Failure: true}, Abort{Code: ReasonNone},
		Credit{MaxData: MaxOffset + 1}, Progress{Receipt: 5, MaxStream: 4}, Progress{Final: 1},
		Progress{Attempted: 3, HasFinal: true, Final: 2}, Missing{ID: 1}, Missing{ID: 0, Ranges: []ByteRange{{0, 1}}},
		Missing{ID: 1, Ranges: []ByteRange{{2, 2}}}, Missing{ID: 1, Ranges: []ByteRange{{0, 4}, {3, 7}}},
		Missing{ID: 1, Ranges: []ByteRange{{0, 4}, {4, 7}}}, Missing{ID: 1, Ranges: []ByteRange{{0, MaxOffset + 1}}},
	}
	for _, m := range cases {
		stream := uint64(0)
		if m.controlKind().streamScoped() {
			stream = 1
		}
		if _, err := EncodeControl(stream, m); err == nil {
			t.Fatalf("accepted %#v", m)
		}
	}
	var pointer *Connect
	if _, err := EncodeControl(1, pointer); err == nil {
		t.Fatal("typed nil accepted")
	}
	if _, err := DecodeControl(Frame{Type: RekeyRequest}); err != ErrUnsupportedControl {
		t.Fatal(err)
	}
	if _, err := DecodeControl(Frame{Type: Open, Stream: 1, Payload: []byte{1, 187, 255, 255}}); err == nil {
		t.Fatal("unbounded host accepted")
	}
	f, _ := EncodeControl(1, Missing{ID: 1, Ranges: []ByteRange{{0, 1}}})
	f.Payload[8] = 255
	if _, err := DecodeControl(f); err == nil {
		t.Fatal("unbounded NACK count accepted")
	}
}
func checkControlRejectsTruncationTrailingAndIgnoredOffset(t *testing.T) {
	for _, c := range controlCases() {
		f, err := EncodeControl(c.stream, c.message)
		if err != nil {
			t.Fatal(err)
		}
		for n := 0; n < len(f.Payload); n++ {
			bad := f
			bad.Payload = f.Payload[:n]
			if _, err = DecodeControl(bad); err == nil {
				t.Fatal(c.name, "truncated", n)
			}
		}
		bad := f
		bad.Payload = append(append([]byte(nil), f.Payload...), 0)
		if _, err = DecodeControl(bad); err == nil {
			t.Fatal(c.name, "trailing")
		}
	}
	f, _ := EncodeControl(1, Connect{Host: "example.com", Port: 443})
	f.Offset = 1
	if _, err := DecodeControl(f); err == nil {
		t.Fatal("OPEN offset ignored")
	}
	f, _ = EncodeControl(1, Progress{})
	f.Payload[32] = 2
	if _, err := DecodeControl(f); err == nil {
		t.Fatal("unknown status flag")
	}
}
func checkControlDecodeOwnsPayload(t *testing.T) {
	f, _ := EncodeControl(1, Missing{ID: 1, Ranges: []ByteRange{{0, 10}}})
	m, err := DecodeControl(f)
	if err != nil {
		t.Fatal(err)
	}
	clear(f.Payload)
	if m.(Missing).Ranges[0].End != 10 {
		t.Fatal("aliased untrusted buffer")
	}
}
func FuzzDecodeControl(f *testing.F) {
	for _, c := range controlCases() {
		frame, _ := EncodeControl(c.stream, c.message)
		raw, _ := frame.MarshalBinary()
		f.Add(raw)
	}
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, raw []byte) {
		frame, err := ParseFrame(raw)
		if err != nil {
			return
		}
		m, err := DecodeControl(frame)
		if err != nil {
			return
		}
		canonical, err := EncodeControl(frame.Stream, m)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := canonical.MarshalBinary()
		if err != nil || !bytes.Equal(encoded, raw) {
			t.Fatal("noncanonical control", err)
		}
	})
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioControl(t *testing.T) {
	t.Log("check=checkControlGoldenVectors")
	checkControlGoldenVectors(t)
	t.Log("check=checkControlRejectsMalformedFields")
	checkControlRejectsMalformedFields(t)
	t.Log("check=checkControlRejectsTruncationTrailingAndIgnoredOffset")
	checkControlRejectsTruncationTrailingAndIgnoredOffset(t)
	t.Log("check=checkControlDecodeOwnsPayload")
	checkControlDecodeOwnsPayload(t)
}

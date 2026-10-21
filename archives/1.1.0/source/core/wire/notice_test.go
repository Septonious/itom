package wire

import (
	"strings"
	"testing"
)

func TestServerNoticeCanonicalBounds(t *testing.T) {
	for _, c := range []Control{Notice{Revision: 1, Text: "Maintenance at 18:00 UTC."}, Notice{Revision: 2, Text: ""}, NoticeAck{Revision: 2}} {
		f, err := EncodeControl(0, c)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeControl(f)
		if err != nil || got != c {
			t.Fatal(got, err)
		}
		if _, err := EncodeControl(1, c); err == nil {
			t.Fatal("server notice accepted a stream scope")
		}
	}
	for _, c := range []Control{Notice{Text: "no revision"}, Notice{Revision: 1, Text: "bad\x1btext"}, Notice{Revision: 1, Text: string([]byte{0xff})}, Notice{Revision: 1, Text: strings.Repeat("x", MaxNoticeText+1)}, NoticeAck{Revision: 0}} {
		if _, err := EncodeControl(0, c); err == nil {
			t.Fatal("invalid server message accepted", c)
		}
	}
}

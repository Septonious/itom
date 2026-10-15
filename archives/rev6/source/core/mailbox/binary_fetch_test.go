package mailbox

import (
	"bytes"
	"context"
	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"sync/atomic"
	"testing"
	"time"
)

type binaryFallbackSession struct {
	imapserver.Session
	refused *atomic.Int64
	code    imap.ResponseCode
}

func (s binaryFallbackSession) Fetch(w *imapserver.FetchWriter, set imap.NumSet, options *imap.FetchOptions) error {
	if len(options.BinarySection) != 0 {
		s.refused.Add(1)
		return &imap.Error{Type: imap.StatusResponseTypeNo, Code: s.code}
	}
	return s.Session.Fetch(w, set, options)
}

// A real TLS IMAP round trip covers zero bytes, arbitrary octets, canonical
// journal evidence, scoped deletion, and a lying capability without reconnect.
func TestBinaryFetchPreservesMIMEAndFallsBackOnSameConnection(t *testing.T) {
	for _, scenario := range []string{"binary", "unsupported_folder", "unclassified_no", "rewritten_header"} {
		t.Run(scenario, func(t *testing.T) {
			refuse := scenario == "unsupported_folder" || scenario == "unclassified_no"
			var refused atomic.Int64
			caps := imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}, imap.CapBinary: {}}
			cfg, tls := testServerWithCaps(t, "tls", caps, func(session imapserver.Session) imapserver.Session {
				if refuse {
					code := imap.ResponseCodeUnknownCTE
					if scenario == "unclassified_no" {
						code = ""
					}
					return binaryFallbackSession{session, &refused, code}
				}
				return session
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client, err := dial(ctx, cfg, []string{"itom-binary"}, tls)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if !client.BinaryFetchEnabled() {
				t.Fatal("BINARY capability not negotiated")
			}
			if err = client.Ensure(ctx, "itom-binary"); err != nil {
				t.Fatal(err)
			}
			payload := bytes.Repeat([]byte{0, 255, 13, 10, 128, 1, 2, 3}, 8192)
			raw, _ := Encode(payload)
			if scenario == "rewritten_header" {
				raw = append([]byte("X-Provider: preserved\r\n"), raw...)
			}
			if err = client.Append(ctx, "itom-binary", raw); err != nil {
				t.Fatal(err)
			}
			// Recovery uses the physical BODY even when BINARY is advertised.
			recovered, _, err := client.Page(ctx, "itom-binary", Cursor{})
			if err != nil || len(recovered) != 1 || !bytes.Equal(recovered[0].MIME, raw) || refused.Load() != 0 {
				t.Fatal("recovery touched binary or lost MIME", err)
			}
			rows, _, err := client.PageTailStream(ctx, "itom-binary", Cursor{}, true, 4, func(Message) error { return nil })
			if err != nil || len(rows) != 1 {
				t.Fatalf("rows%d err%v", len(rows), err)
			}
			if !bytes.Equal(rows[0].MIME, raw) {
				t.Fatal("physical MIME/journal evidence changed")
			}
			decoded, err := Decode(rows[0].MIME)
			if err != nil || !bytes.Equal(decoded, payload) {
				t.Fatal("binary payload changed")
			}
			if client.BinaryFetchEnabled() != (scenario == "binary") || refuse && refused.Load() != 1 {
				t.Fatal("bad receive fallback", refused.Load())
			}
			if err = client.Delete(ctx, rows[0]); err != nil {
				t.Fatal("same authenticated connection unusable", err)
			}
			absent, err := client.Absent(ctx, "itom-binary", rows[0].Validity, rows[0].UID)
			if err != nil || !absent {
				t.Fatal("missing deletion evidence", err)
			}
		})
	}
}

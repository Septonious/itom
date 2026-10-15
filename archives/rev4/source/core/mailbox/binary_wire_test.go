package mailbox

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// The compact field trace has no wire payload. This standards-shaped peer
// reproduces its MISSING_BODY failure: SIZE + HEADER precede BINARY[1]<0>.
// The old in-memory IMAP server never wrote the partial origin for BINARY.
func TestBinaryWirePartialOriginAndMissingBodyFallback(t *testing.T) {
	for _, scenario := range []string{"header_first", "binary_first", "no_origin", "missing_binary", "missing_header", "nil_binary", "empty_binary", "wrong_origin", "wrong_part", "recovery_body", "truncated_binary", "truncated_body"} {
		t.Run(scenario, func(t *testing.T) {
			a, b := net.Pipe()
			t.Cleanup(func() { a.Close(); b.Close() })
			a.SetDeadline(time.Now().Add(3 * time.Second))
			b.SetDeadline(time.Now().Add(3 * time.Second))
			cc := &cappedConn{Conn: a}
			cc.left.Store(4 << 20)
			client := &Client{c: imapclient.New(cc, nil), conn: cc}
			defer client.Close()
			client.binaryFetch.Store(scenario != "truncated_body")
			payload := []byte{0, 255, 13, 10, 128, 42}
			raw, _ := Encode(payload)
			done := make(chan error, 1)
			go func() {
				defer b.Close()
				if _, err := fmt.Fprint(b, "* PREAUTH [CAPABILITY IMAP4rev1 BINARY] ready\r\n"); err != nil {
					done <- err
					return
				}
				reader := bufio.NewReader(b)
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						done <- err
						return
					}
					fields := strings.Fields(line)
					if len(fields) < 2 {
						done <- fmt.Errorf("short command")
						return
					}
					if fields[1] == "NOOP" {
						_, err = fmt.Fprintf(b, "%s OK noop\r\n", fields[0])
						done <- err
						return
					}
					if len(fields) < 4 || fields[1] != "UID" || fields[2] != "FETCH" {
						done <- fmt.Errorf("unexpected command")
						return
					}
					if scenario == "truncated_binary" || scenario == "truncated_body" {
						if scenario == "truncated_binary" {
							_, err = fmt.Fprintf(b, "* 1 FETCH (UID 1 RFC822.SIZE %d BODY[HEADER]<0> {%d}\r\n%s BINARY[1]<0> ~{8192}\r\nshort", len(raw), len(mimeHeader), mimeHeader)
						} else {
							_, err = fmt.Fprintf(b, "* 1 FETCH (UID 1 RFC822.SIZE %d BODY[]<0> {8192}\r\nshort", len(raw))
						}
						done <- err
						return
					}
					if strings.Contains(line, "BINARY.PEEK") {
						if scenario == "recovery_body" {
							done <- fmt.Errorf("recovery used unqualified binary")
							return
						}
						origin := "<0>"
						part := "1"
						if scenario == "no_origin" {
							origin = ""
						}
						if scenario == "wrong_origin" {
							origin = "<1>"
						}
						if scenario == "wrong_part" {
							part = "2"
						}
						header := fmt.Sprintf("BODY[HEADER]<0> {%d}\r\n%s", len(mimeHeader), mimeHeader)
						binary := fmt.Sprintf("BINARY[%s]%s ~{%d}\r\n%s", part, origin, len(payload), payload)
						if scenario == "nil_binary" {
							binary = "BINARY[1]<0> NIL"
						}
						if scenario == "empty_binary" {
							binary = "BINARY[1]<0> ~{0}\r\n"
						}
						items := header + " " + binary
						if scenario == "binary_first" {
							items = binary + " " + header
						}
						if scenario == "missing_binary" {
							items = header
						}
						if scenario == "missing_header" {
							items = binary
						}
						_, err = fmt.Fprintf(b, "* 1 FETCH (UID 1 RFC822.SIZE %d %s)\r\n%s OK fetched\r\n", len(raw), items, fields[0])
					} else {
						_, err = fmt.Fprintf(b, "* 1 FETCH (UID 1 RFC822.SIZE %d BODY[]<0> {%d}\r\n%s)\r\n%s OK fetched\r\n", len(raw), len(raw), raw, fields[0])
					}
					if err != nil {
						done <- err
						return
					}
				}
			}()
			var delivered []Message
			var emit func(Message) error
			if scenario != "recovery_body" {
				emit = func(m Message) error { delivered = append(delivered, m); return nil }
			}
			rows, err := client.fetchSetStreaming(imap.UIDSetNum(1), 1, true, emit)
			if strings.HasPrefix(scenario, "truncated_") {
				if err == nil || errors.Is(err, ErrLimit) || !errors.Is(err, io.ErrUnexpectedEOF) || len(delivered) != 0 {
					t.Fatalf("truncated literal must preserve network EOF, not size limit: err=%v delivered=%d", err, len(delivered))
				}
				if err = <-done; err != nil {
					t.Fatal(err)
				}
				return
			}
			if scenario == "wrong_origin" || scenario == "wrong_part" {
				if err == nil || len(delivered) != 0 {
					t.Fatal("accepted wrong section/origin", err)
				}
				return
			}
			if err != nil || len(rows) != 1 || !bytes.Equal(rows[0].MIME, raw) {
				t.Fatalf("rows=%d detail=%s err=%v", len(rows), ScopeDetail(err), err)
			}
			if emit != nil && (len(delivered) != 1 || !bytes.Equal(delivered[0].MIME, raw)) {
				t.Fatal("lost or duplicate streamed body")
			}
			if (scenario == "missing_binary" || scenario == "missing_header" || scenario == "nil_binary" || scenario == "empty_binary") && client.BinaryFetchEnabled() {
				t.Fatal("incomplete binary kept enabled")
			}
			if err = client.c.Noop().Wait(); err != nil {
				t.Fatal("authenticated connection lost", err)
			}
			if err = <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

package imapclient_test

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// Unsolicited state updates must not consume the solicited UID's BODY slot.
// A raw peer controls item ordering, including SIZE following a streamed literal.
func TestUIDFetchUpdateBeforeBodyRetainsRequestedRows(t *testing.T) {
	for _, update := range []string{"UID 1", "UID 1 FLAGS (\\Seen)", "UID 1 MODSEQ (9)"} {
		for _, bodyFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/bodyFirst=%t", update, bodyFirst), func(t *testing.T) {
				a, b := net.Pipe()
				defer b.Close()
				_ = a.SetDeadline(time.Now().Add(3 * time.Second))
				_ = b.SetDeadline(time.Now().Add(3 * time.Second))
				peer := make(chan error, 1)
				go func() {
					_, err := fmt.Fprint(b, "* PREAUTH ready\r\n")
					if err != nil {
						peer <- err
						return
					}
					line, err := bufio.NewReader(b).ReadString('\n')
					if err != nil {
						peer <- err
						return
					}
					parts := strings.Fields(line)
					if len(parts) < 3 || parts[1] != "UID" || parts[2] != "FETCH" {
						peer <- fmt.Errorf("unexpected command %q", line)
						return
					}
					full := "* 1 FETCH (UID 1 RFC822.SIZE 3 BODY[] {3}\r\nabc)\r\n"
					if bodyFirst {
						full = "* 1 FETCH (UID 1 BODY[] {3}\r\nabc RFC822.SIZE 3)\r\n"
					}
					_, err = fmt.Fprintf(b, "* 1 FETCH (%s)\r\n* 1 FETCH (%s)\r\n* 1 FETCH (%s)\r\n%s* 2 FETCH (UID 2 RFC822.SIZE 3 BODY[] {3}\r\nxyz)\r\n* 1 FETCH (UID 1 FLAGS (\\Seen))\r\n%s OK fetched\r\n", update, update, update, full, parts[0])
					peer <- err
				}()
				client := imapclient.New(a, nil)
				defer client.Close()
				messages, err := client.Fetch(imap.UIDSetNum(1, 2), &imap.FetchOptions{UID: true, RFC822Size: true, BodySection: []*imap.FetchItemBodySection{{}}}).Collect()
				if err != nil {
					t.Fatal(err)
				}
				if err = <-peer; err != nil {
					t.Fatal(err)
				}
				bodies := map[imap.UID]string{}
				for _, msg := range messages {
					for _, body := range msg.BodySection {
						if _, duplicate := bodies[msg.UID]; duplicate {
							t.Fatal("duplicate BODY", msg.UID)
						}
						bodies[msg.UID] = string(body.Bytes)
					}
				}
				if bodies[1] != "abc" || bodies[2] != "xyz" || len(bodies) != 2 {
					t.Fatal("solicited BODY was stolen", bodies)
				}
			})
		}
	}
}

package imapclient_test

import (
	"bufio"
	"bytes"
	"fmt"
	"github.com/emersion/go-imap/v2/imapclient"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// A raw peer independently checks the literal marker before accepting bytes.
// This covers the 4096-byte boundary and servers with no literal extension.
func TestAppendLiteralCapabilityWireFallback(t *testing.T) {
	for _, capability := range []string{"", "LITERAL-", "LITERAL+"} {
		for _, size := range []int{100, 4096, 4097, 65536} {
			t.Run(fmt.Sprintf("%s/%d", capability, size), func(t *testing.T) {
				app, peer := net.Pipe()
				defer peer.Close()
				_ = peer.SetDeadline(time.Now().Add(3 * time.Second))
				client := imapclient.New(app, nil)
				defer client.Close()
				want := bytes.Repeat([]byte{0x5a}, size)
				done := make(chan error, 1)
				go func() {
					if _, err := fmt.Fprintf(peer, "* PREAUTH [CAPABILITY %s] ready\r\n", strings.TrimSpace("IMAP4rev1 UIDPLUS "+capability)); err != nil {
						done <- err
						return
					}
					reader := bufio.NewReader(peer)
					line, err := reader.ReadString('\n')
					if err != nil {
						done <- err
						return
					}
					fields := strings.Fields(line)
					nonSync := capability == "LITERAL+" || (capability == "LITERAL-" && size <= 4096)
					marker := fmt.Sprintf("{%d}", size)
					if nonSync {
						marker = fmt.Sprintf("{%d+}", size)
					}
					if len(fields) < 4 || fields[1] != "APPEND" || fields[len(fields)-1] != marker {
						done <- fmt.Errorf("unsupported literal marker: %q expected %q", line, marker)
						peer.Close()
						return
					}
					if !nonSync {
						if _, err = io.WriteString(peer, "+ send literal\r\n"); err != nil {
							done <- err
							return
						}
					}
					body := make([]byte, size)
					if _, err = io.ReadFull(reader, body); err != nil || !bytes.Equal(body, want) {
						done <- fmt.Errorf("body mismatch: %v", err)
						return
					}
					trailer := make([]byte, 2)
					if _, err = io.ReadFull(reader, trailer); err != nil || string(trailer) != "\r\n" {
						done <- fmt.Errorf("command terminator: %v %q", err, trailer)
						return
					}
					_, err = fmt.Fprintf(peer, "%s OK [APPENDUID 1 1] completed\r\n", fields[0])
					done <- err
				}()
				if err := client.WaitGreeting(); err != nil {
					t.Fatal(err)
				}
				command := client.Append("INBOX", int64(size), nil)
				if _, err := command.Write(want); err != nil {
					t.Fatal(err)
				}
				if err := command.Close(); err != nil {
					t.Fatal(err)
				}
				if _, err := command.Wait(); err != nil {
					t.Fatal(err)
				}
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

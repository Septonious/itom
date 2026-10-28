package mobile

import (
	"bytes"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// Checks the native application copy primitive on OS TCP, including a reply
// after the request direction closes. It is not Android TUN qualification.
func TestApplicationCopyPreservesReplyAfterUploadHalfClose(t *testing.T) {
	listener, e := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	result := make(chan error, 1)
	go func() {
		c, e := listener.AcceptTCP()
		if e != nil {
			result <- e
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(3 * time.Second))
		b, e := io.ReadAll(c)
		if e != nil {
			result <- e
			return
		}
		if string(b) != "request body" {
			result <- io.ErrUnexpectedEOF
			return
		}
		_, e = c.Write([]byte("complete response"))
		if e == nil {
			e = c.CloseWrite()
		}
		result <- e
	}()
	c, e := net.DialTCP("tcp", nil, listener.Addr().(*net.TCPAddr))
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	n, e, _ := copyApplicationSocket(c, strings.NewReader("request body"), make([]byte, 32<<10))
	if e != nil || n != 12 {
		t.Fatal(n, e)
	}
	if e = c.CloseWrite(); e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	n, e, _ = copyApplicationSocket(&out, c, make([]byte, 32<<10))
	if e != nil || n != 17 || out.String() != "complete response" {
		t.Fatal(n, out.String(), e)
	}
	if e = <-result; e != nil {
		t.Fatal(e)
	}
}
func TestExperimentalCandidateCannotMasqueradeAsStableRelease(t *testing.T) {
	if !strings.HasSuffix(Version(), "-rc1-10") {
		t.Fatal("field candidate must retain its explicit RC identity")
	}
}

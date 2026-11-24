package imapclient_test

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
)

// Closing a socket cannot interrupt a decoder that is waiting to deliver the
// 129th EXPUNGE notification into a 128-item command response buffer.
func TestCloseInterruptsUndrainedExpungeBurst(t *testing.T) {
	local, remote := net.Pipe()
	defer remote.Close()
	local.SetDeadline(time.Now().Add(3 * time.Second))
	remote.SetDeadline(time.Now().Add(3 * time.Second))
	sent := make(chan struct{})
	go func() {
		reader := bufio.NewReader(remote)
		_, _ = io.WriteString(remote, "* PREAUTH [CAPABILITY IMAP4rev1] fixture\r\n")
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		tag := strings.Fields(line)[0]
		_, _ = io.WriteString(remote, "* 300 EXISTS\r\n"+tag+" OK [READ-WRITE] selected\r\n")
		line, err = reader.ReadString('\n')
		if err != nil {
			return
		}
		tag = strings.Fields(line)[0]
		var replies strings.Builder
		for i := 0; i < 300; i++ {
			fmt.Fprint(&replies, "* 1 EXPUNGE\r\n")
		}
		fmt.Fprintf(&replies, "%s OK removed\r\n", tag)
		_, _ = io.WriteString(remote, replies.String())
		close(sent)
	}()
	c := imapclient.New(local, nil)
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	expunge := c.Expunge()
	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatal("provider fixture did not send burst")
	}
	time.Sleep(20 * time.Millisecond)
	done := make(chan struct{})
	go func() { _ = c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
		t.Error("Close is stuck behind an undrained EXPUNGE notification after socket closure")
	}
	_ = expunge.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("decoder never joined")
	}
}

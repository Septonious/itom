package imapclient_test

import (
	"errors"
	"github.com/emersion/go-imap/v2/imapclient"
	"io"
	"net"
	"testing"
	"time"
)

func TestUnsolicitedIdleEOFIsNotReportedAsSuccessfulClose(t *testing.T) {
	app, peer := net.Pipe()
	client := imapclient.New(app, nil)
	defer client.Close()
	go func() { _, _ = io.WriteString(peer, "* PREAUTH [CAPABILITY IMAP4rev1] ready\r\n"); peer.Close() }()
	if err := client.WaitGreeting(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.Closed():
	case <-time.After(time.Second):
		t.Fatal("idle decoder did not observe EOF")
	}
	if err := client.Close(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal("unsolicited EOF lost its terminal cause", err)
	}
}

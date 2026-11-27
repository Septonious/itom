package mailstream

import (
	"errors"
	"fmt"
	"io"
	"net"
	"testing"

	imap "github.com/emersion/go-imap/v2"
)

func TestRC16ControlWriterRecoveryFence(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"control EOF", controlWriterAppendError{io.EOF}, true},
		{"wrapped control close", fmt.Errorf("APPEND: %w", controlWriterAppendError{net.ErrClosed}), true},
		{"data EOF", io.EOF, false},
		{"data socket closed", net.ErrClosed, false},
		{"control protocol error", controlWriterAppendError{ErrProtocol}, false},
		{"control provider refusal", controlWriterAppendError{&imap.Error{Type: imap.StatusResponseTypeNo, Text: "quota"}}, false},
		{"unknown", controlWriterAppendError{errors.New("unknown")}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isControlWriterTransportFailure(tc.err); got != tc.want {
				t.Fatalf("recoverable = %v, want %v", got, tc.want)
			}
		})
	}
}

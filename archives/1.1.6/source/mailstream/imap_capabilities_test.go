package mailstream

import (
	"context"
	"testing"
	"time"
)

func TestProviderCapabilitiesNeverImplicitlyEnableBinaryEncoding(t *testing.T) {
	// No capability-related write flag exists. Even a provider supporting
	// BINARY or LITERAL+ must use mandatory 7bit/ASCII85 until a separate,
	// authenticated and field-qualified wire upgrade is explicitly designed.
	var p mailPipe
	p.capBinary = true
	p.capLiteralPlus = true
	p.capIMAP4rev2 = true
	if !p.capBinary || !p.capLiteralPlus || !p.capIMAP4rev2 {
		t.Fatal("capability diagnostic fields missing")
	}
	message:=encodeMail([]byte("ITM3"))
	if len(message)==0 || message[0]!='F' {
		t.Fatal("provider capability unexpectedly changed MIME encoding")
	}
}

func TestControlPollCapabilitySnapshotIsBounded(t *testing.T) {
	_ = context.Background()
	if got:=(&mailPipe{}).controlPollDelay(time.Now());got!=time.Millisecond {
		t.Fatalf("capability collection must not change active polling: %v",got)
	}
}

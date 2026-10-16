package tunnel

import (
	"bytes"
	"io"
	"testing"
	"time"
)

// Use actual IMAP-over-TLS sessions, production lanes, durable wallets and
// independent TCP endpoints. Only the provider backend appends extra CRLF to
// DATA's physical MIME. A previously unaffected PING/OPEN is NOT sufficient:
// the entire authenticated application echo must complete byte-for-byte.
func TestField77RealIMAPTLSProviderRewritesDATAWithoutBlackhole(t *testing.T) {
	t.Setenv("ITOM_TEST_PROVIDER_EOL", "1")
	f := newProductionSupervisorFixtureAccounts(t, 3)
	p := f.openOn(t, "a01")
	_ = p.SetDeadline(time.Now().Add(15 * time.Second))
	for i := 0; i < 3; i++ {
		payload := bytes.Repeat([]byte{byte(i + 1), 0x00, 0xFF, 0x0A}, 128*(i+1))
		if err := productionWriteFull(p, payload); err != nil {
			t.Fatalf("upload #%d: %v", i, err)
		}
		answer := make([]byte, len(payload))
		if _, err := io.ReadFull(p, answer); err != nil {
			t.Fatalf("download #%d: %v", i, err)
		}
		if !bytes.Equal(payload, answer) {
			t.Fatalf("DATA #%d changed", i)
		}
	}
	if f.targetDials.Load() != 1 {
		t.Fatalf("TCP connection replaced: %d", f.targetDials.Load())
	}
}

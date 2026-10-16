package tunnel

import (
	"context"
	"itom.local/core/mailbox"
	"testing"
	"time"
)

// The guard's short-lived command context ends as DONE is sent. Its cancellation
// must not close the reader before the overlapping FETCH joins tagged IDLE.
func TestReceiveIdleOverlapKeepsReaderGenerationAfterGuardReturns(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	network := newProviderTestNetwork(0)
	network.mobileEnabled = true
	if !network.mobile.set(mobileLinkProfile{RTT: 100 * time.Millisecond, Mbps: 100}) {
		t.Fatal("mobile fixture")
	}
	cm, _ := tlsMailPairWithNetwork(t, ctx, []string{"up", "down"}, network, "a01")
	m := cm.(*laneMail)
	defer m.Close()
	if err := m.Activate(ctx, "down"); err != nil {
		t.Fatal(err)
	}
	original := m.readerRef()
	for _, c := range []*mailbox.Client{m.Client, original} {
		if !network.markMobileClient(c.LocalAddr().String(), c.RemoteAddr().String()) {
			t.Fatal("mobile reader scope")
		}
	}
	_, cur, err := m.Page(ctx, "down", mailbox.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	for turn := 0; turn < 4; turn++ {
		used, _, _, err := m.receiveWait(ctx, "down", 100*time.Millisecond, nil)
		if err != nil || !used {
			t.Fatal("IDLE failed", turn, err)
		}
		_, next, err := m.Page(ctx, "down", cur)
		if err != nil || m.readerRef() != original || m.ReaderRepairs() != 0 {
			t.Fatal("guard return canceled pending DONE", turn, err)
		}
		cur = next
	}
	cancel()
	if _, _, err = m.Page(ctx, "down", cur); err == nil {
		t.Fatal("lane cancellation ignored")
	}
}

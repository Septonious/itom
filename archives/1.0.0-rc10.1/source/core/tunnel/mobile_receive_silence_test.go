package tunnel

import (
	"context"
	"itom.local/core/mailbox"
	"testing"
	"time"
)

// A high-RTT account plus a bounded provider pause is not a dead receive role.
// This tests actual TLS/FETCH, original UID cursor and a single callback.
func TestMobileProviderPauseDoesNotChurnReceiveSocket(t *testing.T) {
	t.Setenv("ITOM_FIELD_PROVIDER_MODEL", "1")
	network := newProviderTestNetwork(0)
	network.fieldLatency = newFieldLatencyReplay(t)
	replay := network.fieldLatency
	for name := range replay.samples {
		replay.samples[name] = []int64{1000}
	}
	replay.stallEnabled, replay.stallUS, replay.start = true, 2_000_000, time.Now().Add(-21*time.Second)
	network.mobileEnabled = true
	network.mobile.set(mobileLinkProfile{RTT: 300 * time.Millisecond, Mbps: 10, Seed: 19})
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	cm, sm := tlsMailPairWithNetwork(t, ctx, []string{"up", "down"}, network, "a02")
	m := cm.(*laneMail)
	defer m.Close()
	defer sm.(*laneMail).Close()
	if err := m.Activate(ctx, "down"); err != nil {
		t.Fatal(err)
	}
	reader := m.readerRef()
	for _, c := range []*mailbox.Client{m.Client, reader} {
		if !network.markMobileClient(c.LocalAddr().String(), c.RemoteAddr().String()) {
			t.Fatal("mobile scope")
		}
	}
	rtt, e := m.Ping(ctx)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := mailbox.Encode([]byte("one intact response after provider pause"))
	if e = m.Client.Append(ctx, "down", raw); e != nil {
		t.Fatal(e)
	}
	callbacks := 0
	rows, next, e := m.PageStream(ctx, "down", mailbox.Cursor{}, func(mailbox.Message) error { callbacks++; return nil })
	if e != nil || len(rows) != 1 || callbacks != 1 || next.After != 1 {
		t.Fatal("receive pause delivery", len(rows), next, callbacks, e)
	}
	if m.readerRef() != reader || m.ReaderRepairs() != 0 {
		t.Fatal("bounded mobile pause needlessly replaced receive socket", m.ReaderRepairs())
	}
	t.Logf("unloaded_provider_rtt_ms=%d pause_ms=2000 intact_rows=1 callbacks=1 receive_replacements=0", rtt.Milliseconds())
}

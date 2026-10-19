package tunnel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"itom.local/core/mailbox"
)

func TestRev2DATAReaderRepairRetainsAllocatedInvisibleUIDs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	network := newProviderTestNetwork(0)
	network.visibility.delay = 900 * time.Millisecond
	c, s := tlsMailPairWithNetwork(t, ctx, []string{"reader-gap"}, network, "a01")
	seed := c.(*laneMail)
	defer seed.Close()
	defer s.(*laneMail).Close()
	reader, err := seed.dial(ctx)
	if err != nil {
		t.Fatal(err)
	}
	m := &laneMail{dial: seed.dial, dataReader: reader}
	defer func() {
		if reader := m.dataReaderRef(); reader != nil {
			reader.Close()
		}
	}()
	for i := 0; i < 12; i++ {
		body, err := mailbox.Encode([]byte(fmt.Sprintf("gap-%d", i)))
		if err != nil {
			t.Fatal(err)
		}
		if err = seed.Client.Append(ctx, "reader-gap", body); err != nil {
			t.Fatal(err)
		}
	}
	var cursor mailbox.Cursor
	seen := map[uint32]bool{}
	emit := func(row mailbox.Message) error { seen[row.UID] = true; return nil }
	for cursor.After < 12 {
		cursor, err = m.pageData(ctx, "reader-gap", cursor, emit)
		if err != nil {
			t.Fatal(err)
		}
	}
	if seen[11] || !seen[12] || network.visibility.suppressed.Load() == 0 {
		t.Fatal("allocated but invisible UID fault was not exercised", seen)
	}
	reader.Close()
	if err = pause(ctx, 950*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	cursor, err = m.pageData(ctx, "reader-gap", cursor, emit)
	if err != nil || !seen[11] || cursor.After != 12 {
		t.Fatal("socket repair forgot an allocated invisible BODY behind its cursor", seen, cursor, err)
	}
}

func TestRev2PublicDATAIdleWakeAndDiagnostics(t *testing.T) {
	f := newProductionSupervisorFixture(t)
	l := f.waitLane(t, "a01", 0)
	if err := pause(f.ctx, 600*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	l.diag.mu.Lock()
	idle := l.diag.line.DataIdleWaits
	polls := l.diag.line.DataFetchPolls
	l.diag.mu.Unlock()
	if idle == 0 || polls == 0 {
		t.Fatal("independent DATA receiver was neither idling nor observed", idle, polls)
	}
	p := f.openOn(t, "a01")
	_ = p.SetDeadline(time.Now().Add(10 * time.Second))
	payload := bytes.Repeat([]byte("real-DATA-idle-wake"), 20000)
	done := make(chan error, 1)
	go func() { done <- productionWriteFull(p, payload) }()
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(p, got); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil || !bytes.Equal(got, payload) {
		t.Fatal("DATA IDLE wake failed application integrity", err)
	}
	var delivered uint64
	f.client.mu.Lock()
	for _, carrier := range f.client.lanes {
		carrier.diag.mu.Lock()
		delivered += carrier.diag.line.DataFetchMessages
		carrier.diag.mu.Unlock()
	}
	f.client.mu.Unlock()
	if delivered == 0 {
		t.Fatal("bulk DATA receipt absent from diagnostic counters")
	}
}

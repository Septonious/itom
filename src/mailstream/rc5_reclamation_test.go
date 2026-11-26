package mailstream

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestRC5CollectionFailurePreservesControlWriter(t *testing.T) {
	accounts, _ := applicationMailServerAccounts(t, true, false, nil, nil, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	data, e := openMailPipe(ctx, accounts[0])
	if e != nil {
		t.Fatal(e)
	}
	original := data
	defer func() {
		if data != nil {
			data.close()
		}
	}()
	control, e := openMailPipe(ctx, accounts[0])
	if e != nil {
		t.Fatal(e)
	}
	defer control.close()
	a := &mailAccountState{ctx: ctx, config: accounts[0], writer: data, controlWriter: control, spent: 1234, reserved: 5678, link: &IMAPLink{login: make(chan struct{}, 1)}}
	// A provider timeout closes only the borrowed DATA writer.
	data.close()
	completed := make(chan error, 4)
	n := 0
	var wg sync.WaitGroup
	a.reopenAppendWriters(ctx, &data, completed, &n, &wg)
	if data == nil || data == original || a.writer != data || a.controlWriter != control {
		t.Fatal("healthy control connection replaced or DATA writer not recovered")
	}
	if e = control.client.Noop().Wait(); e != nil {
		t.Fatalf("control connection no longer works: %v", e)
	}
	if a.spent != 1234 || a.reserved != 5678 {
		t.Fatal("recovery manufactured credit")
	}
}

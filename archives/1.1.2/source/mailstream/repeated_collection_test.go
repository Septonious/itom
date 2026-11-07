package mailstream

import (
	"context"
	imap "github.com/emersion/go-imap/v2"
	"testing"
	"time"
)

func TestCollectionPagesPreserveUnconsumedControlTail(t *testing.T) {
	accounts, _ := applicationMailServer(t, true, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := openMailPipe(ctx, accounts[0])
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	folder := "itom3-paged-control-v7-c2s"
	r := seedCollectionFolder(t, p, folder, 600)
	c := NewReclamationCoordinator()
	target := uidWatermark{r.validity, 400}
	first, n, done, err := p.collectPrefix(c, folder, target, false)
	if err != nil || done || n != 256 || first.UID != 256 {
		t.Fatalf("first bounded proof: %+v count=%d done=%v err=%v", first, n, done, err)
	}
	cmd, err := p.startAppend(folder, []byte("new unconsumed control"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	second, n, done, err := p.collectPrefix(c, folder, target, false)
	if err != nil || !done || n != 144 || second != target {
		t.Fatalf("consumption fence: %+v count=%d done=%v err=%v", second, n, done, err)
	}
	remaining, err := p.client.Fetch(imap.UIDSet{{Start: 401, Stop: 601}}, &imap.FetchOptions{UID: true}).Collect()
	if err != nil || len(remaining) != 201 {
		t.Fatalf("future controls were lost: count=%d err=%v", len(remaining), err)
	}
}

func TestAbandonedFolderNeedsAllPagesBeforeEmptyProof(t *testing.T) {
	accounts, _ := applicationMailServer(t, true, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := openMailPipe(ctx, accounts[0])
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	folder := "itom3-paged-old-data"
	seedCollectionFolder(t, p, folder, 600)
	for i, want := range []uint64{256, 256, 88} {
		_, n, done, e := p.collectPrefix(NewReclamationCoordinator(), folder, uidWatermark{}, true)
		if e != nil || n != want || done != (i == 2) {
			t.Fatalf("page%d count=%d done=%v err=%v", i, n, done, e)
		}
	}
}

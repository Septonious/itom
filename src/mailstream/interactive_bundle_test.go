package mailstream

import "testing"

func TestInteractiveBundlePreservesAccountAndSession(t *testing.T) {
	p, q := &mailPipe{}, &mailPipe{}
	id := SessionID{1}
	makeWork := func(target *mailPipe, sid SessionID, seq uint64) mailWork {
		return mailWork{target: target, body: header(sealed, sid, seq)}
	}
	first := makeWork(p, id, 1)
	queued := make(chan mailWork, 4)
	queued <- makeWork(p, id, 2)
	queued <- makeWork(p, id, 3)
	queued <- makeWork(q, id, 4)
	queued <- makeWork(p, id, 5)
	batch, carry := collectInteractive(first, queued)
	if len(batch) != 3 || carry == nil || carry.target != q || len(queued) != 1 {
		t.Fatal("batch crossed publisher boundary", len(batch), carry, len(queued))
	}
	batch, carry = collectInteractive(first, make(chan mailWork))
	if len(batch) != 1 || carry != nil {
		t.Fatal("isolated packet waited for bundle")
	}
}

package mailstream

import (
	"bytes"
	"context"
	"io"
	"math"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	imapclient "github.com/emersion/go-imap/v2/imapclient"
)

// Exercise the *real* in-memory IMAP server over verified TLS. Per RFC 3501
// and RFC 9051, a UID FETCH response must carry UID even when the FETCH
// data-item list does not explicitly request UID. The control reader still
// verifies UID, RFC822.SIZE and the exact message literal before consuming.
func TestRC117ImplicitUIDFetchWithRealTLSProvider(t *testing.T) {
	accounts, _ := applicationMailServer(t, false, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := openMailPipe(ctx, accounts[0])
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()

	const folder = "rc117-implicit-uid-test"
	if err := p.client.Create(folder, nil).Wait(); err != nil {
		t.Fatalf("create: %v", err)
	}
	id := SessionID{1}
	payload := append(header(sealed, id, 1), []byte("authenticated-synthetic-packet")...)
	message := encodeMail(payload)
	appendCmd, err := p.startAppend(folder, message)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := appendCmd.Wait(); err != nil {
		t.Fatalf("append completion: %v", err)
	}
	if _, err := p.client.Select(folder, nil).Wait(); err != nil {
		t.Fatalf("select: %v", err)
	}
	command := p.client.Fetch(
		imap.UIDSet{{Start: 1, Stop: imap.UID(math.MaxUint32)}},
		&imap.FetchOptions{RFC822Size: true, BodySection: []*imap.FetchItemBodySection{{Peek: true}}},
	)
	received := 0
	for row := command.Next(); row != nil; row = command.Next() {
		var uid imap.UID
		var size int64 = -1
		var raw []byte
		for item := row.Next(); item != nil; item = row.Next() {
			switch v := item.(type) {
			case imapclient.FetchItemDataUID:
				uid = v.UID
			case imapclient.FetchItemDataRFC822Size:
				size = v.Size
			case imapclient.FetchItemDataBodySection:
				if v.Literal == nil {
					t.Fatal("missing message literal")
				}
				raw, err = io.ReadAll(v.Literal)
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		if uid == 0 || size != int64(len(raw)) || !bytes.Equal(message, raw) {
			t.Fatalf("implicit UID/size/literal mismatch: uid=%d size=%d read=%d", uid, size, len(raw))
		}
		packets, err := decodeMail(raw)
		if err != nil || len(packets) != 1 || !bytes.Equal(packets[0], payload) {
			t.Fatalf("control packet corrupt: %v", err)
		}
		received++
	}
	if err := command.Close(); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if received != 1 {
		t.Fatalf("received %d packets, expected one", received)
	}
	// Empty subsequent UID FETCH remains correct when its starting UID
	// is beyond the tail; do not use n:* which can reread stale data.
	empty := p.client.Fetch(
		imap.UIDSet{{Start: imap.UID(math.MaxUint32 - 1), Stop: imap.UID(math.MaxUint32)}},
		&imap.FetchOptions{RFC822Size: true, BodySection: []*imap.FetchItemBodySection{{Peek: true}}},
	)
	if row := empty.Next(); row != nil {
		t.Fatal("UID FETCH of a forward range beyond tail returned an old message")
	}
	if err := empty.Close(); err != nil {
		t.Fatalf("empty fetch: %v", err)
	}
}

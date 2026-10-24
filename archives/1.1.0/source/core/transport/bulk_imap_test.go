package transport_test

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"itom.local/core/secure"
	"itom.local/core/transport"
	"net"
	"strings"
	"testing"
	"time"
)

// This component scenario uses two real TLS/IMAP sockets and durable admission.
// Logical OPEN is established by the fixture. It is neither a calibrated
// provider nor an Android/throughput qualification, and records no rate claim.
func TestBulkApplicationBytesCrossTLSAppendFetchAndDurableAccounting(t *testing.T) {
	f := publicationsOn(t, 8<<20, 32, transport.Bulk)
	payloads := make(map[uint64][]byte)
	for i, id := range []uint64{1, 3, 5} {
		if id != 1 {
			add(t, f.sender, id, 1<<20, 1<<20)
			add(t, f.receiver, id, 1<<20, 1<<20)
		}
		// Deliberately end on a full record: this scenario isolates bulk service;
		// separate classification tests exercise short fast-plane tails.
		payload := bytes.Repeat([]byte{byte(i), 0, 0xff, 0x7f}, 128<<10)
		payloads[id] = payload
		if err := f.sender.Queue(id, payload, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stored := make(chan []byte, 1)
	upload := selectedPeerOn(t, "bulk", func(conn net.Conn) {
		reader := bufio.NewReader(conn)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			// The main goroutine closes this executor after the last batch. Read
			// errors then signal ordinary fixture shutdown, not a failed test.
			if _, err := reader.Peek(1); err != nil {
				return
			}
			line, body := readAppendBody(t, conn, reader)
			if body == nil {
				return
			}
			select {
			case stored <- body:
			case <-ctx.Done():
				return
			}
			fmt.Fprintf(conn, "%s OK stored\r\n", tag(line))
		}
	})
	download := selectedPeerOn(t, "bulk", func(conn net.Conn) {
		reader := bufio.NewReader(conn)
		for uid := uint32(1); ; uid++ {
			if _, err := reader.Peek(1); err != nil {
				return
			}
			line := readLine(t, reader)
			if !strings.Contains(line, fmt.Sprintf("UID FETCH %d:%d", uid, uid)) {
				t.Error("FETCH ownership changed", line)
				return
			}
			var body []byte
			select {
			case body = <-stored:
			case <-ctx.Done():
				return
			}
			fmt.Fprintf(conn, "* %d FETCH (UID %d BODY[] {%d}\r\n", uid, uid, len(body))
			conn.Write(body)
			fmt.Fprintf(conn, ")\r\n%s OK fetched\r\n", tag(line))
		}
	})
	defer upload.Close()
	defer download.Close()
	plain, err := incoming(transport.Data, 1, 0, make([]byte, transport.MaxPayload)).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	packetBytes, err := secure.SessionPacketSize(len(plain))
	if err != nil {
		t.Fatal(err)
	}
	// Eight whole records per BODY, so this deliberately bulk-only scenario
	// has no short record tail requiring another mailbox service plane.
	bodyLimit := 8 + 8*(4+packetBytes)
	var physical int
	for uid := uint32(1); ; uid++ {
		p, err := f.publisher.PrepareBulk(ctx, bodyLimit)
		if err != nil {
			t.Fatal(err)
		}
		if p == nil {
			break
		}
		physical++
		if outcome, err := p.Start(ctx, upload); err != nil || outcome != transport.Accepted {
			t.Fatal("APPEND failed", outcome, err)
		}
		fetch, err := download.Fetch(ctx, uid, uid)
		if err != nil {
			t.Fatal(err)
		}
		bodies, err := fetch.Wait(ctx)
		if err != nil || len(bodies) != 1 || bodies[0].UID != uid {
			t.Fatal("FETCH failed", err)
		}
		delivery, err := f.incoming.Decode(9, uid, bodies[0].Bytes)
		if err != nil {
			t.Fatal("accounted BODY decode failed", err)
		}
		if err = delivery.Apply(); err != nil {
			t.Fatal("logical delivery failed", err)
		}
	}
	if physical != 3 {
		t.Fatal("bulk did not aggregate physical publications", physical)
	}
	for id, expected := range payloads {
		got := make([]byte, len(expected))
		if n, err := f.receiver.Read(id, got); err != nil || n != len(got) || !bytes.Equal(got, expected) {
			t.Fatal("application bytes differ after physical transfer", id, n, err)
		}
	}
	entries, err := f.wallet.Entries()
	if err != nil || entries[0].Used.Messages != uint64(physical) || len(entries[0].Pending) != physical {
		t.Fatal("physical and logical accounting diverged", err)
	}
}

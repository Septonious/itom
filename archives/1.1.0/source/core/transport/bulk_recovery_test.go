package transport_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"itom.local/core/mailbox"
	"itom.local/core/transport"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func readAppendBody(t *testing.T, conn net.Conn, reader *bufio.Reader) (string, []byte) {
	t.Helper()
	line := readLine(t, reader)
	at, end := strings.LastIndex(line, "{"), strings.LastIndex(line, "}")
	if at < 0 || end <= at {
		t.Error("missing APPEND literal", line)
		return line, nil
	}
	n, err := strconv.Atoi(line[at+1 : end])
	if err != nil || n < 1 || n > 2<<20 {
		t.Error("invalid APPEND literal", n, err)
		return line, nil
	}
	fmt.Fprint(conn, "+\r\n")
	body := make([]byte, n+2)
	if _, err = io.ReadFull(reader, body); err != nil || !bytes.Equal(body[n:], []byte("\r\n")) {
		t.Error("incomplete APPEND literal", err)
		return line, nil
	}
	return line, body[:n]
}

func prepareRecoveryBatch(t *testing.T) (*publicationFixture, *transport.PreparedPublication, []byte, int) {
	t.Helper()
	f := publicationsOn(t, 2<<20, 16, transport.Bulk)
	payload := bytes.Repeat([]byte("0123456789abcdef"), 32<<10)
	if err := f.sender.Queue(1, payload, time.Now()); err != nil {
		t.Fatal(err)
	}
	p, err := f.publisher.PrepareBulk(context.Background(), transport.MaxBulkBytes)
	if err != nil || p == nil {
		t.Fatal("batch preparation failed", err)
	}
	count := f.sender.Snapshot().ActiveDispatches
	if count < 2 {
		t.Fatal("fixture did not aggregate multiple records")
	}
	return f, p, payload, count
}

func TestLostBulkAppendReplyRetainsEveryRecordUntilExactScopedProof(t *testing.T) {
	f, p, payload, count := prepareRecoveryBatch(t)
	exact := p.MIME()
	stored := make(chan []byte, 1)
	first := selectedPeerOn(t, "bulk", func(conn net.Conn) {
		_, body := readAppendBody(t, conn, bufio.NewReader(conn))
		stored <- body
		// Store the whole literal, then lose the tagged APPEND completion.
	})
	outcome, err := p.Start(context.Background(), first)
	if outcome != transport.Uncertain || !errors.Is(err, transport.ErrUnknown) || !bytes.Equal(<-stored, exact) {
		t.Fatal("lost whole-body completion was not uncertain", outcome, err)
	}
	if f.sender.PendingPublication(1) != count || f.sender.Snapshot().ActiveDispatches != count {
		t.Fatal("uncertain batch lost a member's recovery ownership")
	}
	if another, err := f.publisher.PrepareBulk(context.Background(), transport.MaxBulkBytes); err != nil || another != nil {
		t.Fatal("uncertain batch was blindly retried", err)
	}
	if _, err = p.Start(context.Background(), first); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("uncertain physical body was started twice", err)
	}
	decoded, err := mailbox.Decode(exact)
	if err != nil {
		t.Fatal(err)
	}
	decoded[len(decoded)-1] ^= 1
	wrong, err := mailbox.Encode(decoded)
	if err != nil {
		t.Fatal(err)
	}
	proof := selectedPeerOn(t, "bulk", func(conn net.Conn) {
		reader := bufio.NewReader(conn)
		for _, body := range [][]byte{wrong, exact} {
			line := readLine(t, reader)
			fmt.Fprintf(conn, "* 1 FETCH (UID 19 BODY[] {%d}\r\n", len(body))
			conn.Write(body)
			fmt.Fprintf(conn, ")\r\n%s OK fetched\r\n", tag(line))
		}
	})
	if err = p.ConfirmStored(context.Background(), proof, 19); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("wrong whole-body proof accepted", err)
	}
	if f.sender.PendingPublication(1) != count {
		t.Fatal("wrong proof released batch members")
	}
	if err = p.ConfirmStored(context.Background(), proof, 19); err != nil {
		t.Fatal("exact stored proof did not settle batch acceptance", err)
	}
	state := f.sender.Snapshot()
	if state.ActiveDispatches != 0 || state.SendBuffered != uint64(len(payload)) || state.RetainedRecords != count {
		t.Fatal("storage proof invented peer receipt", state)
	}
	entries, err := f.wallet.Entries()
	if err != nil || entries[0].Used.Messages != 1 || len(entries[0].Pending) != 1 {
		t.Fatal("storage proof refunded or multiplied physical spend", err)
	}
	// Delivery is independent: only the authenticated receiver consumes records.
	delivery, err := f.incoming.Decode(9, 19, exact)
	if err != nil {
		t.Fatal(err)
	}
	if err = delivery.Apply(); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if n, err := f.receiver.Read(1, got); err != nil || n != len(got) || !bytes.Equal(payload, got) {
		t.Fatal("recovered batch lost application bytes", n, err)
	}
}

func TestCanceledUnstartedBulkAppendReusesOneExactBodyAndOneSpend(t *testing.T) {
	f, p, payload, _ := prepareRecoveryBatch(t)
	stored := make(chan []byte, 1)
	x := selectedPeerOn(t, "bulk", func(conn net.Conn) {
		line, body := readAppendBody(t, conn, bufio.NewReader(conn))
		stored <- body
		fmt.Fprintf(conn, "%s OK stored\r\n", tag(line))
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if outcome, err := p.Start(ctx, x); outcome != transport.NotStarted || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled queued body acquired command ownership", outcome, err)
	}
	if outcome, err := p.Start(context.Background(), x); outcome != transport.Accepted || err != nil {
		t.Fatal("unstarted body could not start later", outcome, err)
	}
	if !bytes.Equal(<-stored, p.MIME()) || f.sender.Snapshot().SendBuffered != uint64(len(payload)) {
		t.Fatal("unstarted retry changed bytes or pretended peer delivery")
	}
	entries, err := f.wallet.Entries()
	if err != nil || entries[0].Used.Messages != 1 || len(entries[0].Pending) != 1 {
		t.Fatal("unstarted retry consumed another physical ordinal", err)
	}
}

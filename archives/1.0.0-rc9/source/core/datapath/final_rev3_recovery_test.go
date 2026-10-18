package datapath

import (
	"context"
	"itom.local/core/flow"
	"itom.local/core/wire"
	"testing"
	"time"
)

func TestFinalRev3LostCreditRecoversAtInteractiveReserveBoundary(t *testing.T) {
	cfg := DefaultConfig(1, []string{"a01"})
	cfg.ReceiveBytes = 64 << 10
	cfg.StreamWindow = 64 << 10
	a, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Stop()
	b, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Stop()
	wa, err := a.Activate(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	wb, err := b.Activate(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Receive(1, wb); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Receive(1, wa); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		item, err := a.Queue(1, 1, make([]byte, 30<<10))
		if err != nil {
			t.Fatal(err)
		}
		f, err := a.Frame(1, item)
		if err != nil {
			t.Fatal(err)
		}
		job, err := a.Begin(1, item, "a01", 32<<10, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err = a.Complete(1, job.Attempt().ID, flow.Committed); err != nil {
			t.Fatal(err)
		}
		acks, err := b.Receive(1, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, ack := range acks {
			if _, err = a.Receive(1, ack); err != nil {
				t.Fatal(err)
			}
		}
		// Receiver writes the data, but lose its replenished WINDOW.
		if _, err = b.Delivered(1, 1, 30<<10); err != nil {
			t.Fatal(err)
		}
	}
	if n := a.AdmissionCapacityReserved(1, 1, 4<<10); n != 0 {
		t.Fatal("boundary not reproduced", n)
	}
	frames, err := a.Poll(1, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	queried := false
	for _, f := range frames {
		queried = queried || f.Type == wire.StatusRequest
		replies, err := b.Receive(1, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, reply := range replies {
			if _, err = a.Receive(1, reply); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !queried || a.AdmissionCapacityReserved(1, 1, 4<<10) == 0 {
		t.Fatal("lost credit could deadlock at reserved headroom")
	}
}

func TestFinalRev3UnpublishedWorkDoesNotSolicitRepairOrPeerCredit(t *testing.T) {
	a, b := pair(t)
	item, err := a.Queue(1, 1, []byte("not published"))
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.Begin(1, item, "a01", 256, 0)
	if err != nil {
		t.Fatal(err)
	}
	frames, err := a.Poll(1, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range frames {
		if f.Type == wire.StatusRequest {
			t.Fatal("local writer backlog queried available peer credit")
		}
		replies, err := b.Receive(1, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, reply := range replies {
			if reply.Type == wire.Nack {
				t.Fatal("unpublished worker handoff solicited repair")
			}
		}
	}
	if err := a.Complete(1, job.Attempt().ID, flow.NotCommitted); err != nil {
		t.Fatal(err)
	}
	frames, err = a.Poll(1, 4*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range frames {
		if f.Type == wire.StatusRequest {
			t.Fatal("local delivery yield queried available peer credit")
		}
	}
}

func TestFinalRev3WriterIndexSurvivesEarlyReceiptAndDuplicateCompletion(t *testing.T) {
	a, b := pair(t)
	item, err := a.Queue(1, 1, []byte("early receipt"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := a.Frame(1, item)
	if err != nil {
		t.Fatal(err)
	}
	j, err := a.Begin(1, item, "a01", 256, 0)
	if err != nil {
		t.Fatal(err)
	}
	acks, err := b.Receive(1, f)
	if err != nil {
		t.Fatal(err)
	}
	for _, ack := range acks {
		if _, err = a.Receive(1, ack); err != nil {
			t.Fatal(err)
		}
	}
	if len(a.pending) != 0 || len(a.writerItems) != 1 {
		t.Fatal("early receipt lost the live physical writer")
	}
	for i := 0; i < 2; i++ {
		if err = a.Complete(1, j.Attempt().ID, flow.CommitUnknown); err != nil {
			t.Fatal(err)
		}
	}
	if len(a.writerItems) != 0 {
		t.Fatal("completed writer retained actor metadata")
	}
	if n, err := a.tx.AttemptedEnd(1); err != nil || n != uint64(len(f.Payload)) {
		t.Fatal("early receipt hid unknown publication", n, err)
	}
	s, _ := a.Snapshot()
	if s.PhysicalExposure != 256 {
		t.Fatal("early receipt erased unknown physical debt")
	}
}

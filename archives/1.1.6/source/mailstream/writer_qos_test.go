package mailstream

import (
	"testing"
	"time"
)

func TestRC116WriterSlotReservedOnlyForRecentInteractiveWork(t *testing.T) {
	link := &IMAPLink{}
	now := time.Now()
	if link.reserveInteractiveWriterSlot(3, now) {
		t.Fatal("bulk-only link unexpectedly reserves a completion slot")
	}
	link.lastInteractive.Store(now.Add(-40*time.Millisecond).UnixNano())
	if !link.reserveInteractiveWriterSlot(3, now) {
		t.Fatal("interactive traffic must preserve fourth completion slot")
	}
	if link.reserveInteractiveWriterSlot(2, now) || link.reserveInteractiveWriterSlot(4, now) {
		t.Fatal("only a three-operation writer may reserve its next slot")
	}
	if link.reserveInteractiveWriterSlot(3, now.Add(190*time.Millisecond)) {
		t.Fatal("interactive reservation persisted beyond the burst")
	}
}

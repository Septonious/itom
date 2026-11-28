package mailstream

import (
	"testing"
	"time"
)

func TestDesktopQuietFeedbackUsesIdleDataOwnerOnly(t *testing.T) {
	l := &IMAPLink{}
	writer := &mailPipe{}
	a := &mailAccountState{link: l, id: SessionID{1}, writer: writer, controlWriter: &mailPipe{}, bulk: make(chan mailWork, 1)}
	work := mailWork{control: true, feedback: true}
	if !a.quietFeedbackOnData(work, writer, 0) {
		t.Fatal("idle data writer unavailable")
	}
	if a.quietFeedbackOnData(work, writer, 1) || a.quietFeedbackOnData(mailWork{control: true}, writer, 0) || a.quietFeedbackOnData(mailWork{control: true, feedback: true, recovery: true}, writer, 0) {
		t.Fatal("unsafe work moved")
	}
	a.bulk <- mailWork{}
	if a.quietFeedbackOnData(work, writer, 0) {
		t.Fatal("queued bulk")
	}
	<-a.bulk
	l.lastBulk.Store(time.Now().UnixNano())
	if a.quietFeedbackOnData(work, writer, 0) {
		t.Fatal("recent bulk")
	}
	l.lastBulk.Store(time.Now().Add(-time.Second).UnixNano())
	l.mobileFeedback.Store(true)
	if a.quietFeedbackOnData(work, writer, 0) {
		t.Fatal("mobile changed")
	}
	l.mobileFeedback.Store(false)
	a.parking.Store(true)
	if a.quietFeedbackOnData(work, writer, 0) {
		t.Fatal("parking")
	}
	a.parking.Store(false)
	if !a.quietFeedbackOnData(work, writer, 0) {
		t.Fatal("did not restore idle path")
	}
}

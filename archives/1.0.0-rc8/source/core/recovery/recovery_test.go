package recovery

import (
	"testing"
	"time"
)

func checkLastOnlyFrameGetsStatusProbeAndBoundedDeadline(t *testing.T) {
	w, err := New(0, 10*time.Second, time.Second, 3)
	if err != nil {
		t.Fatal(err)
	}
	probes, expired := 0, 0
	for i := 0; i <= 12; i++ {
		a, err := w.Poll(time.Duration(i) * time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if a == ProbeStatus {
			probes++
		}
		if a == DeadlineExpired {
			expired++
		}
	}
	if probes != 3 || expired != 1 {
		t.Fatal(probes, expired)
	}
}
func checkThirdAttemptWaitsForDeadlineAndDuplicateNackIsHarmless(t *testing.T) {
	r := NewRepair()
	if !r.Nack(1) || r.Nack(1) {
		t.Fatal("duplicate retry")
	}
	r.Complete()
	if !r.Nack(2) {
		t.Fatal("missing second repair")
	}
	r.Complete()
	if r.Nack(3) || r.Attempts() != 3 {
		t.Fatal("unbounded attempts")
	}
	w, err := New(0, 10*time.Second, time.Second, 3)
	if err != nil {
		t.Fatal(err)
	}
	a, err := w.Poll(time.Second)
	if err != nil || a == DeadlineExpired {
		t.Fatal("premature reset", err)
	}
	w.Receipt()
	a, err = w.Poll(11 * time.Second)
	if err != nil || a != Wait {
		t.Fatal(a, err)
	}
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioRecovery(t *testing.T) {
	t.Log("check=checkLastOnlyFrameGetsStatusProbeAndBoundedDeadline")
	checkLastOnlyFrameGetsStatusProbeAndBoundedDeadline(t)
	t.Log("check=checkThirdAttemptWaitsForDeadlineAndDuplicateNackIsHarmless")
	checkThirdAttemptWaitsForDeadlineAndDuplicateNackIsHarmless(t)
}

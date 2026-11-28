package tunnel

import (
	"testing"
	"time"
)

func TestSerialRecoveryFloor(t *testing.T) {
	var p cellularPolicy
	at := time.Unix(100, 0)
	if got := p.decideRecovery(8, 80, true, 100000, at); got != 8 {
		t.Fatal(got)
	}
	for i := 1; i <= 20; i++ {
		p.decideRecovery(8, 350, true, 100000, at.Add(time.Duration(i)*time.Second))
	}
	if p.limit < 4 {
		t.Fatal(p.limit)
	}
	for i := 21; i <= 50; i++ {
		p.decideRecovery(8, 1200, true, 0, at.Add(time.Duration(i)*time.Second))
	}
	if p.limit != 3 {
		t.Fatal(p.limit)
	}
	for i := 51; i <= 90; i++ {
		p.decideRecovery(8, 85, true, 100000, at.Add(time.Duration(i)*time.Second))
	}
	if p.limit != 8 {
		t.Fatal(p.limit)
	}
	if p.decideRecovery(2, 0, true, 0, at.Add(91*time.Second)) != 2 {
		t.Fatal(p.limit)
	}
}

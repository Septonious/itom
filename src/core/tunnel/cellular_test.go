package tunnel

import "testing"

func TestCellularPolicy(t *testing.T) {
	var p cellularPolicy
	if n := p.decide(8, 80, false); n != 2 {
		t.Fatal(n)
	}
	for i := 0; i < 6; i++ {
		p.decide(8, 80, true)
	}
	if p.limit != 8 {
		t.Fatal(p.limit)
	}
	p.decide(8, 350, true)
	p.decide(8, 350, true)
	if p.limit != 1 {
		t.Fatal(p.limit)
	}
	for i := 0; i < 6; i++ {
		p.decide(8, 80, true)
	}
	if p.limit != 8 {
		t.Fatal(p.limit)
	}
	for i := 0; i < 12; i++ {
		p.decide(8, 80, false)
	}
	if p.limit != 7 {
		t.Fatal(p.limit)
	}
	if n := p.decide(5, 0, true); n != 5 {
		t.Fatal(n)
	}
}

func TestCellularInitiallyPoor(t *testing.T) {
	var p cellularPolicy
	p.decide(8, 350, true)
	if n := p.decide(8, 350, true); n != 1 {
		t.Fatal(n)
	}
	for i := 0; i < 6; i++ {
		p.decide(8, 80, true)
	}
	if p.limit != 8 {
		t.Fatal(p.limit)
	}
}

package datapath

import (
	"testing"
	"time"
)

func TestSocketReadPolicyKeepsBurstAndBulkAggregation(t *testing.T) {
	var p socketReadPolicy
	now := time.Now()
	normal, quiet := 5*time.Millisecond, time.Millisecond
	if got := p.budget(32, now, normal, quiet); got != quiet {
		t.Fatal("quiet request delay", got)
	}
	if got := p.budget(32, now.Add(time.Millisecond), normal, quiet); got != normal {
		t.Fatal("fragmented burst lost aggregation", got)
	}
	if got := p.budget(32767, now.Add(time.Second), normal, quiet); got != normal {
		t.Fatal("bulk aggregation changed", got)
	}
	if got := p.budget(512, now.Add(2*time.Second), normal, quiet); got != quiet {
		t.Fatal("flow did not return to quiet", got)
	}
	if got := p.budget(32, now.Add(3*time.Second), normal, 0); got != normal {
		t.Fatal("disabled policy changed baseline", got)
	}
}

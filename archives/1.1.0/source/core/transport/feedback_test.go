package transport_test

import (
	"itom.local/core/transport"
	"testing"
)

func TestSmallApplicationReadsDoNotMultiplyBulkCreditMessages(t *testing.T) {
	s := session(t, transport.DefaultLimits())
	add(t, s, 1, 256<<10, 256<<10)
	controls, credits := 0, 0
	for block := 0; block < 2; block++ {
		if err := s.Receive(incoming(transport.Data, 1, uint64(block*64<<10), make([]byte, 64<<10))); err != nil {
			t.Fatal(err)
		}
		for range 64 {
			if n, err := s.Read(1, make([]byte, 1<<10)); err != nil || n != 1<<10 {
				t.Fatal(n, err)
			}
			for {
				d, ok := s.TakeFor(transport.Fast, 0, transport.ControlLedger)
				if !ok {
					break
				}
				controls++
				if d.Record.Kind == transport.Credit {
					credits++
					if d.Record.Offset != 384<<10 {
						t.Fatal("credit did not coalesce consumed bytes", d.Record)
					}
				}
				complete(t, d, transport.Accepted)
			}
		}
	}
	if credits != 1 || controls > 4 {
		t.Fatal("128 small reads amplified protocol publications", credits, controls)
	}
}

func TestCreditAtThePeerEdgeDoesNotWaitForABatchOrTimer(t *testing.T) {
	s := session(t, transport.DefaultLimits())
	add(t, s, 1, 64<<10, 64<<10)
	if err := s.Receive(incoming(transport.Data, 1, 0, make([]byte, 64<<10))); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Read(1, make([]byte, 1)); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	d := take(t, s, transport.Fast, 0)
	if d.Record.Kind != transport.Credit || d.Record.Offset != (64<<10)+1 || d.Record.Acknowledged != 64<<10 {
		t.Fatal("peer could stall despite newly available backed credit", d.Record)
	}
	complete(t, d, transport.Accepted)
}

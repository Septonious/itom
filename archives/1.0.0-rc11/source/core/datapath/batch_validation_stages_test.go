package datapath

import (
	"errors"
	"itom.local/core/wire"
	"testing"
)

func TestRemoteBatchInvalidProofNeverBecomesGenericIOError(t *testing.T) {
	if _, err := OpenRemoteBatch(nil, wire.UsageReport{}, nil); !errors.Is(err, ErrState) || ValidationStage(err) != "descriptor" {
		t.Fatalf("untyped descriptor rejection: %v", err)
	}
	if got := ValidationStage(errors.New("unclassified transport failure")); got != "unknown" {
		t.Fatalf("stage leaked: %q", got)
	}
}

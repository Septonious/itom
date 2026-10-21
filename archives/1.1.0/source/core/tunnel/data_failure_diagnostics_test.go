package tunnel

import (
	"errors"
	"itom.local/core/datapath"
	"itom.local/core/journal"
	"testing"
)

func TestDataIntegrityFailureIsNotGenericIOError(t *testing.T) {
	err := &datapath.BatchValidationError{Stage: "encoded_size"}
	if !errors.Is(err, datapath.ErrState) || errorCode(err) != "DATA_STATE" {
		t.Fatalf("bad classification: %s", errorCode(err))
	}
	if got := datapath.ValidationStage(err); got != "encoded_size" {
		t.Fatalf("stage: %s", got)
	}
	if errorCode(journal.ErrScope) != "JOURNAL_SCOPE" {
		t.Fatal("journal scope misclassified")
	}
}

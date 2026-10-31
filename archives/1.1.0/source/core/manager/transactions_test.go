package manager

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

func TestTransactionDiagnosticRefusesToOpenStateOrNetworkWhileServiceIsActive(t *testing.T) {
	var out bytes.Buffer
	ui := New(t.TempDir())
	ui.Out = &out
	blocked := errors.New("stop the running service first")
	ui.stoppedCheck = func() error { return blocked }
	if err := ui.imapTransactions(""); !errors.Is(err, blocked) {
		t.Fatal(err)
	}
	files, err := os.ReadDir(ui.State)
	if err != nil || len(files) != 0 || out.Len() != 0 {
		t.Fatal("active service diagnostic touched state", files, out.String(), err)
	}
}

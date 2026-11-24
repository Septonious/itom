package manager

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestSystemctlHonorsDeadlineInsteadOfHangingMenu(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux service manager")
	}
	dir := t.TempDir()
	// exec replaces the shell, so cancellation leaves no fixture child behind.
	if e := os.WriteFile(filepath.Join(dir, "systemctl"), []byte("#!/bin/sh\nexec /bin/sleep 60\n"), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	e := runSystemctl(ctx, io.Discard, "stop", "itom.service")
	if !errors.Is(e, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("unbounded manager command: %v elapsed=%s", e, time.Since(start))
	}
}

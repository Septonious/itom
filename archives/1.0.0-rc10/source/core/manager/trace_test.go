package manager

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestTraceBundleIncludesOnlyTraceFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"server.db", "wallet.db", "profile.json", "transport-trace.jsonl", "transport-trace.jsonl.1"} {
		if e := os.WriteFile(filepath.Join(dir, name), []byte(name), 0600); e != nil {
			t.Fatal(e)
		}
	}
	out := filepath.Join(t.TempDir(), "diagnostics.zip")
	if e := writeTraceBundle(dir, out); e != nil {
		t.Fatal(e)
	}
	z, e := zip.OpenReader(out)
	if e != nil {
		t.Fatal(e)
	}
	defer z.Close()
	if len(z.File) != 3 {
		t.Fatal("unexpected files", len(z.File))
	}
	for _, f := range z.File {
		if f.Name != "capture.json" && f.Name != "transport-trace.jsonl" && f.Name != "transport-trace.jsonl.1" {
			t.Fatal("private state included")
		}
		r, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		_, e = io.ReadAll(r)
		r.Close()
		if e != nil {
			t.Fatal(e)
		}
	}
	if e = writeTraceBundle(dir, out); e == nil {
		t.Fatal("existing export overwritten")
	}
}

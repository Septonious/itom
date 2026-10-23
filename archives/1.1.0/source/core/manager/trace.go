package manager

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"
)

func (u *UI) followTrace() error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("trace viewer requires a Linux server")
	}
	fmt.Fprintln(u.Out, "Full JSONL transport trace. Ctrl+C returns to the menu.")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cmd := exec.CommandContext(ctx, "tail", "-n", "0", "-F", "--", filepath.Join(u.State, "tunnel-server", "transport-trace.jsonl"))
	cmd.Stdout = u.Out
	cmd.Stderr = u.Out
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil
	}
	return err
}
func (u *UI) exportTrace() error {
	directory := "/tmp"
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	path := filepath.Join(directory, "ITOM_server_"+time.Now().UTC().Format("20060102T150405.000000000Z")+".zip")
	if err := writeTraceBundle(filepath.Join(u.State, "tunnel-server"), path); err != nil {
		return err
	}
	fmt.Fprintln(u.Out, "Diagnostics archive:", path)
	return nil
}
func writeTraceBundle(dir, path string) (err error) {
	type source struct {
		name string
		file *os.File
		size int64
	}
	var sources []source
	defer func() {
		for _, s := range sources {
			s.file.Close()
		}
	}()
	// Keep descriptors open through rotation and copy only the snapshotted length.
	for _, name := range []string{"transport-trace.jsonl.2", "transport-trace.jsonl.1", "transport-trace.jsonl"} {
		f, e := os.Open(filepath.Join(dir, name))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		st, e := f.Stat()
		if e != nil {
			f.Close()
			return e
		}
		sources = append(sources, source{name, f, min(st.Size(), 16<<20)})
	}
	if len(sources) == 0 {
		return fmt.Errorf("no trace available; start the service")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if e := file.Close(); err == nil {
			err = e
		}
		if err != nil {
			os.Remove(path)
		}
	}()
	z := zip.NewWriter(file)
	meta, e := z.Create("capture.json")
	if e != nil {
		return e
	}
	if e = json.NewEncoder(meta).Encode(map[string]any{"schema": 1, "revision": "itom-1-1-0-dev9", "created_utc": time.Now().UTC(), "live_snapshot_best_effort": true, "trace_files": len(sources), "go": runtime.Version(), "plaintext_payload": false}); e != nil {
		return e
	}
	for _, s := range sources {
		w, e := z.Create(s.name)
		if e != nil {
			return e
		}
		if _, e = io.Copy(w, io.LimitReader(s.file, s.size)); e != nil {
			return e
		}
	}
	return z.Close()
}

// ExportDiagnostics writes the same bounded trace bundle used by the CLI.
func ExportDiagnostics(state, path string) error {
	return writeTraceBundle(filepath.Join(state, "tunnel-server"), path)
}

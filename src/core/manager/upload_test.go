//go:build linux

package manager

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLatestUploadUsesActualServerBinary(t *testing.T) {
	compiled := filepath.Join(t.TempDir(), "server")
	cmd := exec.Command("go", "build", "-trimpath", "-o", compiled, "./cmd/itom")
	cmd.Dir = ".."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build ITOM: %v\n%s", err, out)
	}
	data, err := os.ReadFile(compiled)
	if err != nil {
		t.Fatal(err)
	}
	menu := exec.Command(compiled, "menu", "--state", t.TempDir())
	menu.Stdin = strings.NewReader("0\n")
	if out, err := menu.CombinedOutput(); err != nil || !strings.Contains(string(out), "18. Update from newest ITOM .bin in /tmp") {
		t.Fatalf("new manager option missing: %v %s", err, out)
	}
	write := func(t *testing.T, path string, contents []byte) {
		t.Helper()
		if err := os.WriteFile(path, contents, 0644); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("SFTP_preserved_mtime_and_mixed_case_names", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "ITOM-old.bin"), data)
		path := filepath.Join(dir, "new-itom-upload.BIN")
		write(t, path, data)
		past := time.Unix(946684800, 0)
		if err := os.Chtimes(path, past, past); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(dir, "unrelated.bin"), []byte("ignore"))
		write(t, filepath.Join(dir, "ITOM-download.bin.part"), []byte("unfinished"))
		if err := os.Mkdir(filepath.Join(dir, "ITOM-folder.bin"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(compiled, filepath.Join(dir, "ITOM-link.bin")); err != nil {
			t.Fatal(err)
		}
		selected, got, err := readLatestUpload(dir)
		if err != nil || selected != path || !bytes.Equal(got, data) {
			t.Fatalf("selected=%q error=%v", selected, err)
		}
	})
	t.Run("invalid_newest_never_falls_back_to_old_version", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "ITOM-old.bin"), data)
		write(t, filepath.Join(dir, "zz-ITOM-new.bin"), []byte("partial upload"))
		if _, _, err := readLatestUpload(dir); err == nil || !strings.Contains(err.Error(), "zz-ITOM-new.bin") {
			t.Fatalf("expected newest-file validation failure: %v", err)
		}
	})
	t.Run("wrong_architecture", func(t *testing.T) {
		wrong := append([]byte(nil), data...)
		machine := uint16(183) // AArch64
		if runtime.GOARCH == "arm64" {
			machine = 62
		}
		binary.LittleEndian.PutUint16(wrong[18:20], machine)
		if err := validateUpload(wrong); err == nil {
			t.Fatal("accepted another architecture")
		}
	})
	t.Run("another_Go_executable_is_not_ITOM", func(t *testing.T) {
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		other, err := os.ReadFile(self)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateUpload(other); err == nil {
			t.Fatal("accepted test runner as server")
		}
	})
	t.Run("writable_upload_rejected", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "ITOM.bin")
		write(t, path, data)
		if err := os.Chmod(path, 0666); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readLatestUpload(dir); err == nil {
			t.Fatal("accepted writable upload")
		}
	})
	t.Run("no_candidate", func(t *testing.T) {
		if _, _, err := readLatestUpload(t.TempDir()); err == nil {
			t.Fatal("expected no-upload error")
		}
	})
}

func TestUploadedInstallPreservesStateAndRollsBack(t *testing.T) {
	for _, fault := range []string{"", "daemon-reload", "enable", "restart", "health"} {
		name := fault
		if name == "" {
			name = "success"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			binaryPath, service := filepath.Join(dir, "itom"), filepath.Join(dir, "itom.service")
			state := filepath.Join(dir, "server.db")
			for path, body := range map[string]string{binaryPath: "old binary", service: "old unit", state: "private persistent state"} {
				if err := os.WriteFile(path, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			failed, activeCalls, restarts := false, 0, 0
			run := func(args ...string) error {
				stage := args[0]
				if stage == "is-active" {
					activeCalls++
					if activeCalls > 1 {
						stage = "health"
					}
				}
				if stage == "restart" {
					restarts++
				}
				if stage == fault && !failed {
					failed = true
					return errors.New("fixture systemd failure")
				}
				return nil
			}
			err := installFiles([]byte("new binary"), binaryPath, service, run, io.Discard)
			if (err == nil) != (fault == "") {
				t.Fatalf("fault=%q error=%v", fault, err)
			}
			want := "new binary"
			if fault != "" {
				want = "old binary"
			}
			got, _ := os.ReadFile(binaryPath)
			if string(got) != want {
				t.Fatalf("installed %q, want %q", got, want)
			}
			if fault != "" {
				unit, _ := os.ReadFile(service)
				if string(unit) != "old unit" {
					t.Fatal("unit not restored")
				}
			}
			preserved, _ := os.ReadFile(state)
			if string(preserved) != "private persistent state" {
				t.Fatal("configuration changed")
			}
			if restarts == 0 {
				t.Fatal("running service not restarted")
			}
		})
	}
	t.Run("stopped_service_stays_stopped", func(t *testing.T) {
		dir := t.TempDir()
		run := func(args ...string) error {
			if args[0] == "is-active" {
				return errors.New("inactive")
			}
			if args[0] == "restart" || args[0] == "start" {
				t.Fatal("started a deliberately stopped service")
			}
			return nil
		}
		if err := installFiles([]byte("new binary"), filepath.Join(dir, "itom"), filepath.Join(dir, "itom.service"), run, io.Discard); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("failed_first_install_leaves_no_partial_service", func(t *testing.T) {
		dir := t.TempDir()
		binaryPath, service := filepath.Join(dir, "itom"), filepath.Join(dir, "itom.service")
		run := func(args ...string) error {
			if args[0] == "is-active" || args[0] == "enable" {
				return errors.New("fixture failure")
			}
			return nil
		}
		if err := installFiles([]byte("new binary"), binaryPath, service, run, io.Discard); err == nil {
			t.Fatal("expected install failure")
		}
		for _, path := range []string{binaryPath, service} {
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("partial install remains: %s", path)
			}
		}
	})
}

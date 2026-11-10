package manager

import (
	"bytes"
	"crypto/sha256"
	"debug/buildinfo"
	"debug/elf"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const BuildRevision = "rc2-login-gate-fix1"

// InstallLatestUpload is a one-shot update from a manually uploaded file, not
// a watcher of the shared temporary directory. Only validated bytes enter the
// same atomic installer and restart/rollback path as menu item 11.
func InstallLatestUpload(out io.Writer) error {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return errors.New("installation requires Linux, sudo and systemd")
	}
	if err := installGuard(); err != nil {
		return err
	}
	path, data, err := readLatestUpload("/tmp")
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Newest upload: %s\nSHA256: %x\n", path, sha256.Sum256(data))
	return installData(data, out)
}

func readLatestUpload(dir string) (string, []byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil, err
	}
	var newest os.FileInfo
	var path string
	for _, entry := range entries {
		name := strings.ToLower(entry.Name())
		if !strings.Contains(name, "itom") || !strings.HasSuffix(name, ".bin") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return "", nil, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if newest == nil || uploadTime(info).After(uploadTime(newest)) ||
			(uploadTime(info).Equal(uploadTime(newest)) && info.Name() > newest.Name()) {
			newest, path = info, filepath.Join(dir, entry.Name())
		}
	}
	if newest == nil {
		return "", nil, fmt.Errorf("no matching ITOM .bin upload found in %s", dir)
	}
	f, err := openUpload(path)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return "", nil, err
	}
	if !os.SameFile(newest, before) || !before.Mode().IsRegular() || !trustedUpload(before) {
		return "", nil, errors.New("newest upload must be a regular file owned by root or the invoking sudo user, without group/other write access")
	}
	if before.Size() <= 0 || before.Size() > 100<<20 {
		return "", nil, errors.New("newest upload is empty or exceeds 100 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, (100<<20)+1))
	if err != nil {
		return "", nil, err
	}
	after, err := f.Stat()
	if err != nil {
		return "", nil, err
	}
	if int64(len(data)) != before.Size() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || !uploadTime(before).Equal(uploadTime(after)) {
		return "", nil, errors.New("newest upload changed while reading; finish the upload and retry")
	}
	if err := validateUpload(data); err != nil {
		return "", nil, fmt.Errorf("newest upload %s: %w", filepath.Base(path), err)
	}
	return path, data, nil
}

func validateUpload(data []byte) error {
	binary, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		return errors.New("not a Linux ELF executable")
	}
	defer binary.Close()
	arch := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}[runtime.GOARCH]
	if arch == elf.EM_NONE || binary.Machine != arch || binary.Class != elf.ELFCLASS64 || (binary.Type != elf.ET_EXEC && binary.Type != elf.ET_DYN) {
		return fmt.Errorf("binary architecture must match linux/%s", runtime.GOARCH)
	}
	info, err := buildinfo.Read(bytes.NewReader(data))
	if err != nil || info.Path != "itom.local/core/cmd/itom" {
		return errors.New("not an ITOM server executable")
	}
	settings := map[string]string{}
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if settings["GOOS"] != "linux" || settings["GOARCH"] != runtime.GOARCH {
		return errors.New("ITOM executable targets a different platform")
	}
	if mode := settings["-buildmode"]; mode != "exe" && mode != "pie" {
		return errors.New("ITOM binary must be a standalone executable")
	}
	return nil
}

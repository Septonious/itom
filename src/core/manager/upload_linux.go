//go:build linux

package manager

import (
	"os"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func uploadTime(info os.FileInfo) time.Time {
	stat := info.Sys().(*syscall.Stat_t)
	return time.Unix(int64(stat.Ctim.Sec), int64(stat.Ctim.Nsec))
}

func trustedUpload(info os.FileInfo) bool {
	owner := info.Sys().(*syscall.Stat_t).Uid
	sudo, err := strconv.ParseUint(os.Getenv("SUDO_UID"), 10, 32)
	return info.Mode().Perm()&0022 == 0 && (owner == 0 || owner == uint32(os.Geteuid()) || err == nil && owner == uint32(sudo))
}

func openUpload(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func reopenManager(state string) error {
	return syscall.Exec("/usr/local/bin/itom", []string{"itom", "menu", "--state", state}, os.Environ())
}

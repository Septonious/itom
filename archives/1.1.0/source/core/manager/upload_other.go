//go:build !linux

package manager

import (
	"errors"
	"os"
	"time"
)

func uploadTime(info os.FileInfo) time.Time { return info.ModTime() }
func trustedUpload(os.FileInfo) bool        { return false }
func openUpload(string) (*os.File, error)   { return nil, errors.New("upload updates require Linux") }
func reopenManager(string) error            { return errors.New("service management requires Linux") }

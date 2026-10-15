package manager

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func (u *UI) uninstall() (bool, error) {
	fmt.Fprintln(u.Out, "Uninstallation stops all client VPNs and removes the installed ITOM service and binary. Configuration, keys, quota journals and mail are preserved for reinstallation.")
	answer, err := u.ask("Uninstall ITOM from the server? (yes/no)")
	if err != nil || answer != "yes" {
		return false, err
	}
	if err = Uninstall(u.Out); err != nil {
		return false, err
	}
	return true, nil
}

// Uninstall deliberately preserves durable state and mailbox contents.
func Uninstall(out io.Writer) error {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return errors.New("uninstallation requires Linux, sudo and systemd")
	}
	run := func(args ...string) error {
		cmd := exec.Command("systemctl", args...)
		cmd.Stdout, cmd.Stderr = out, out
		return cmd.Run()
	}
	if err := uninstallFiles("/usr/local/bin/itom", "/etc/systemd/system/itom.service", run); err != nil {
		return err
	}
	fmt.Fprintln(out, "ITOM uninstalled. /var/lib/itom and mailboxes are preserved.")
	return nil
}
func uninstallFiles(binary, service string, run func(...string) error) error {
	stat, err := os.Lstat(service)
	if err != nil {
		return err
	}
	if !stat.Mode().IsRegular() {
		return errors.New("expected a regular ITOM service file")
	}
	body, err := os.ReadFile(service)
	if err != nil {
		return err
	}
	if !strings.Contains(string(body), "ExecStart=/usr/local/bin/itom serve --state /var/lib/itom\n") {
		return errors.New("service differs from the installed ITOM unit; automatic removal canceled")
	}
	if err = run("stop", "itom.service"); err != nil {
		return err
	}
	if err = run("disable", "itom.service"); err != nil {
		return err
	}
	// Remove only two explicit installed files; never recursively delete state.
	if err = os.Remove(service); err != nil {
		return err
	}
	if err = run("daemon-reload"); err != nil {
		return err
	}
	if err = os.Remove(binary); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
)

func liveLogCommand(ctx context.Context) *exec.Cmd {
	return exec.CommandContext(ctx, "journalctl", "--unit=itom.service", "--follow", "--lines=0", "--no-pager", "--output=short-iso")
}
func (u *UI) followLogs() error {
	if runtime.GOOS != "linux" {
		return errors.New("service logs require Linux and systemd")
	}
	fmt.Fprintln(u.Out, "Live ITOM logs. Ctrl+C returns to the menu; the service keeps running.")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cmd := liveLogCommand(ctx)
	cmd.Stdout = u.Out
	cmd.Stderr = u.Out
	err := cmd.Run()
	if ctx.Err() != nil {
		fmt.Fprintln(u.Out, "\nLog viewer closed.")
		return nil
	}
	return err
}

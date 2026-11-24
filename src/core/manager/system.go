package manager

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"
)

// The service's graceful stop has its own ten-second systemd bound. Do not let
// an unresponsive systemctl or a failed update/rollback keep the menu hung.
func runSystemctl(ctx context.Context, out io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "systemctl", args...)
	cmd.Stdout, cmd.Stderr = out, out
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		return fmt.Errorf("systemctl did not complete: %w", ctx.Err())
	}
	return err
}
func boundedSystemctl(out io.Writer, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return runSystemctl(ctx, out, args...)
}

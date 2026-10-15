package manager

import (
	"fmt"
	"io"
	"itom.local/core/servercfg"
	"path/filepath"
)

func ServerMessage(state, text string, change bool, out io.Writer) error {
	path := filepath.Join(state, "server.db")
	if change {
		if err := servercfg.Update(path, func(c *servercfg.Config) error { return c.SetNotice(text) }); err != nil {
			return err
		}
	}
	c, err := servercfg.Load(path)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Server message (revision %d):\n%s\n", c.Notice.Revision, c.Notice.Text)
	if change {
		fmt.Fprintln(out, "Saved. Active sessions receive this update automatically; no restart is required.")
	}
	return nil
}
func (u *UI) serverMessage() error {
	if err := ServerMessage(u.State, "", false, u.Out); err != nil {
		return err
	}
	choice, err := u.ask("1 edit, 2 clear, 0 back")
	if err != nil || choice == "0" {
		return err
	}
	text := ""
	if choice == "1" {
		text, err = u.ask("Message (up to 2048 UTF-8 bytes)")
		if err != nil {
			return err
		}
	} else if choice != "2" {
		return fmt.Errorf("unknown menu option")
	}
	return ServerMessage(u.State, text, true, u.Out)
}

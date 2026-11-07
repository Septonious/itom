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
	fmt.Fprintf(out, "Global message (revision %d):\n%s\nIndividual messages: %d\n", c.Notice.Revision, c.Notice.Text, len(c.ClientNotices))
	if change {
		fmt.Fprintln(out, "Saved. All individual messages cleared. Active clients update automatically; no restart is required.")
	}
	return nil
}

func ClientMessage(state, uid, text string, change bool, out io.Writer) error {
	path := filepath.Join(state, "server.db")
	if change {
		if err := servercfg.Update(path, func(c *servercfg.Config) error { return c.SetClientNotice(uid, text) }); err != nil {
			return err
		}
	}
	c, err := servercfg.Load(path)
	if err != nil {
		return err
	}
	for _, user := range c.Users {
		if user.ID != uid {
			continue
		}
		source := "global"
		if _, ok := c.ClientNotices[uid]; ok {
			source = "individual"
		}
		n := c.NoticeForClient(uid)
		fmt.Fprintf(out, "Message for %s | UID %s (%s, revision %d):\n%s\n", user.Name, uid, source, n.Revision, n.Text)
		if change {
			fmt.Fprintln(out, "Saved. Active sessions update automatically; no restart is required.")
		}
		return nil
	}
	return fmt.Errorf("client not found")
}

func (u *UI) serverMessage() error {
	fmt.Fprintln(u.Out, "Formatting: &0–&f colors, &l bold, &o italic, &n underline, &m strikethrough, &r reset; && for a literal &. Example: &cHello &aWorld")
	if err := ServerMessage(u.State, "", false, u.Out); err != nil {
		return err
	}
	choice, err := u.ask("1 edit global, 2 clear global, 3 individual client message, 0 back")
	if err != nil || choice == "0" {
		return err
	}
	switch choice {
	case "1":
		text, err := u.ask("Global message (up to 2048 UTF-8 bytes; clears all individual messages)")
		if err != nil {
			return err
		}
		return ServerMessage(u.State, text, true, u.Out)
	case "2":
		return ServerMessage(u.State, "", true, u.Out)
	case "3":
		c, err := servercfg.Load(u.db())
		if err != nil {
			return err
		}
		user, err := u.selectUser(c, false)
		if err != nil || user.ID == "" {
			return err
		}
		if err := ClientMessage(u.State, user.ID, "", false, u.Out); err != nil {
			return err
		}
		action, err := u.ask("1 edit individual message, 2 remove override and use global, 0 back")
		if err != nil || action == "0" {
			return err
		}
		text := ""
		if action == "1" {
			text, err = u.ask("Individual message (up to 2048 UTF-8 bytes; empty uses global)")
			if err != nil {
				return err
			}
		} else if action != "2" {
			return fmt.Errorf("unknown menu option")
		}
		return ClientMessage(u.State, user.ID, text, true, u.Out)
	default:
		return fmt.Errorf("unknown menu option")
	}
}

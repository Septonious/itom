package servercfg

import "itom.local/core/wire"

// A global update atomically removes every per-client override. Re-saving the
// same global text also clears overrides; a complete no-op keeps its revision.
func (c *Config) SetNotice(text string) error {
	if !wire.ValidNoticeText(text) {
		return ErrConfig
	}
	if text == c.Notice.Text && len(c.ClientNotices) == 0 {
		return nil
	}
	if c.Notice.Revision >= wire.MaxOffset {
		return ErrConfig
	}
	c.Schema = max(c.Schema, 4)
	c.Notice = wire.Notice{Revision: c.Notice.Revision + 1, Text: text}
	c.ClientNotices = nil
	return nil
}

// One monotonic revision covers all effective client views. Removing an
// override must send the global text at a newer revision, never roll back to
// an old global revision already superseded on that client.
func (c *Config) SetClientNotice(uid, text string) error {
	found := false
	for _, u := range c.Users {
		if u.ID == uid {
			found = true
			break
		}
	}
	if !found || !wire.ValidNoticeText(text) {
		return ErrConfig
	}
	if c.ClientNotices[uid] == text {
		return nil
	}
	if c.Notice.Revision >= wire.MaxOffset {
		return ErrConfig
	}
	next := make(map[string]string, len(c.ClientNotices)+1)
	for id, old := range c.ClientNotices {
		if id != uid {
			next[id] = old
		}
	}
	if text != "" {
		next[uid] = text
	}
	c.Schema = max(c.Schema, 5)
	c.ClientNotices = next
	c.Notice.Revision++
	return nil
}

func (c Config) NoticeForClient(uid string) wire.Notice {
	n := c.Notice
	if text, ok := c.ClientNotices[uid]; ok {
		n.Text = text
	}
	return n
}

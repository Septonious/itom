package servercfg

import "itom.local/core/wire"

func (c *Config) SetNotice(text string) error {
	if !wire.ValidNoticeText(text) {
		return ErrConfig
	}
	if text == c.Notice.Text {
		return nil
	}
	if c.Notice.Revision >= wire.MaxOffset {
		return ErrConfig
	}
	c.Schema = 4
	c.Notice = wire.Notice{Revision: c.Notice.Revision + 1, Text: text}
	return nil
}

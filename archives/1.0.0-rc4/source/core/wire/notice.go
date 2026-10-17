package wire

import "unicode/utf8"

const MaxNoticeText = 2048

type Notice struct {
	Revision uint64
	Text     string
}

func (Notice) controlKind() Type { return ServerMessage }

type NoticeAck struct{ Revision uint64 }

func (NoticeAck) controlKind() Type { return ServerMessageAck }
func ValidNoticeText(s string) bool {
	if len(s) > MaxNoticeText || !utf8.ValidString(s) {
		return false
	}
	for _, c := range s {
		if c < 32 && c != '\n' && c != '\t' || c == 127 {
			return false
		}
	}
	return true
}

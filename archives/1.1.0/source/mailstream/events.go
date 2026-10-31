package mailstream

// MailEvent is bounded protocol metadata. It never contains credentials,
// provider response strings, destinations, MIME headers or application bytes.
type MailEvent struct {
	Operation  string    `json:"operation"`
	Account    string    `json:"account"`
	Session    SessionID `json:"session"`
	Sequence   uint64    `json:"sequence"`
	Generation uint32    `json:"generation"`
	Control    bool      `json:"control"`
}

func (l *IMAPLink) note(operation, account string, packet []byte, generation uint32, control bool) {
	_, id, sequence, err := parseHeader(packet)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.events) == 512 {
		copy(l.events, l.events[1:])
		l.events = l.events[:511]
	}
	l.events = append(l.events, MailEvent{operation, account, id, sequence, generation, control})
}
func (l *IMAPLink) Events() []MailEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]MailEvent(nil), l.events...)
}

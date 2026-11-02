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
	event := MailEvent{operation, account, id, sequence, generation, control}
	if len(l.events) < 512 {
		l.events = append(l.events, event)
	} else {
		l.events[l.eventNext] = event
		l.eventNext = (l.eventNext + 1) % len(l.events)
	}
}
func (l *IMAPLink) Events() []MailEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	events := make([]MailEvent, 0, len(l.events))
	events = append(events, l.events[l.eventNext:]...)
	return append(events, l.events[:l.eventNext]...)
}

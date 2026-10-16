package tunnel

import (
	"sync"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

type limitedProviderSession struct {
	imapserver.Session
	once    sync.Once
	release func()
}

func (s *limitedProviderSession) Close() error {
	err := s.Session.Close()
	s.once.Do(s.release)
	return err
}

func (p *providerTestNetwork) admitSession(account string) (func(), error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sessionLimit > 0 && p.sessions[account] >= p.sessionLimit {
		p.sessionDenied++
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeLimit, Text: "test provider connection limit"}
	}
	p.sessions[account]++
	p.sessionPeak[account] = max(p.sessionPeak[account], p.sessions[account])
	return func() { p.mu.Lock(); p.sessions[account]--; p.mu.Unlock() }, nil
}

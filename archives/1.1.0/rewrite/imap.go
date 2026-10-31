package mailstream

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

type MailAccount struct {
	ID       string         `json:"id"`
	Host     string         `json:"host"`
	Port     int            `json:"port"`
	Username string         `json:"username"`
	Password string         `json:"password"`
	Roots    *x509.CertPool `json:"-"` // trusted test CA; verification always enabled
}

// Prototype limits are actual bounds, not evidence of sustained reclamation.
// This first implementation intentionally refuses publication once its finite
// session budget is exhausted. The integrated segment-retirement owner must
// replace this limit before the rewrite can become a sustained-use release.
const prototypePhysicalBudget = 64 << 20

type mailWork struct {
	body    []byte
	control bool
}
type mailAccountState struct {
	link                  *IMAPLink
	config                MailAccount
	ctx                   context.Context
	cancel                context.CancelFunc
	mu                    sync.Mutex
	ready, failed         bool
	control, writer, data *mailPipe
	dataDone              chan struct{}
	id                    SessionID
	attachMu              sync.Mutex
	fast                  chan mailWork
	bulk                  chan mailWork
	spent                 int
	appended, received    atomic.Uint64
}

type IMAPLink struct {
	ctx        context.Context
	cancel     context.CancelFunc
	identity   string
	client     bool
	mu         sync.Mutex
	accounts   []*mailAccountState
	attached   SessionID
	roundRobin uint64
	changed    chan struct{}
	incoming   chan []byte
	wg         sync.WaitGroup
	closeOnce  sync.Once
	login      chan struct{}
}

func validIdentity(s string) bool {
	if len(s) == 0 || len(s) > 32 {
		return false
	}
	for _, c := range []byte(s) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

func NewIMAPLink(ctx context.Context, identity string, accounts []MailAccount, client bool) (*IMAPLink, error) {
	if !validIdentity(identity) || len(accounts) == 0 || len(accounts) > 20 {
		return nil, ErrProtocol
	}
	seen := map[string]bool{}
	for _, a := range accounts {
		if !validIdentity(a.ID) || seen[a.ID] || a.Host == "" || a.Port < 1 || a.Port > 65535 || a.Username == "" || a.Password == "" {
			return nil, ErrProtocol
		}
		seen[a.ID] = true
	}
	lifetime, cancel := context.WithCancel(ctx)
	l := &IMAPLink{ctx: lifetime, cancel: cancel, identity: identity, client: client, changed: make(chan struct{}), incoming: make(chan []byte, 512), login: make(chan struct{}, 4)}
	for _, config := range accounts {
		life, stop := context.WithCancel(lifetime)
		a := &mailAccountState{link: l, config: config, ctx: life, cancel: stop, fast: make(chan mailWork, 32), bulk: make(chan mailWork, 4)}
		l.accounts = append(l.accounts, a)
	}
	for _, a := range l.accounts {
		l.wg.Add(1)
		go func() { defer l.wg.Done(); a.run() }()
	}
	return l, nil
}

func (l *IMAPLink) Receive() <-chan []byte { return l.incoming }
func (l *IMAPLink) controlFolder(out bool) string {
	uplink := l.client == out
	direction := "s2c"
	if uplink {
		direction = "c2s"
	}
	return "itom3-" + l.identity + "-control-" + direction
}
func (l *IMAPLink) dataFolder(id SessionID, out bool) string {
	direction := "s2c"
	if l.client == out {
		direction = "c2s"
	}
	return "itom3-" + l.identity + "-" + hex.EncodeToString(id[:]) + "-" + direction
}
func (l *IMAPLink) notify() {
	l.mu.Lock()
	close(l.changed)
	l.changed = make(chan struct{})
	l.mu.Unlock()
}
func (l *IMAPLink) Close() error {
	l.closeOnce.Do(func() {
		l.cancel()
		l.wg.Wait()
		for _, a := range l.accounts {
			a.attachMu.Lock()
			a.closeData()
			a.attachMu.Unlock()
		}
		close(l.incoming)
	})
	return nil
}

func (l *IMAPLink) Send(ctx context.Context, body []byte, priority bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if l.ctx.Err() != nil {
		return ErrClosed
	}
	if _, _, _, err := parseHeader(body); err != nil {
		return err
	}
	l.mu.Lock()
	start := int(l.roundRobin % uint64(len(l.accounts)))
	l.roundRobin++
	l.mu.Unlock()
	work := mailWork{body: bytes.Clone(body), control: priority}
	for n := 0; n < len(l.accounts); n++ {
		a := l.accounts[(start+n)%len(l.accounts)]
		a.mu.Lock()
		available := a.ready && !a.failed && (priority || a.id != (SessionID{}))
		a.mu.Unlock()
		if !available {
			continue
		}
		q := a.bulk
		if priority {
			q = a.fast
		}
		select {
		case q <- work:
			return nil
		default:
		}
	}
	return ErrCapacity
}

func (l *IMAPLink) Attach(ctx context.Context, id SessionID) error {
	if id == (SessionID{}) {
		return ErrProtocol
	}
	l.mu.Lock()
	l.attached = id
	l.mu.Unlock()
	for {
		results := make(chan error, len(l.accounts))
		started := 0
		for _, a := range l.accounts {
			a.mu.Lock()
			ready := a.ready && !a.failed
			done := ready && a.id == id
			a.mu.Unlock()
			if done {
				return nil
			}
			if ready {
				started++
				go func() { results <- a.attach(id) }()
			}
		}
		for range started {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-l.ctx.Done():
				return ErrClosed
			case err := <-results:
				if err == nil {
					return nil
				}
			}
		}
		l.mu.Lock()
		change := l.changed
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-l.ctx.Done():
			return ErrClosed
		case <-change:
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (a *mailAccountState) fail() {
	a.mu.Lock()
	a.failed = true
	a.ready = false
	a.mu.Unlock()
	a.cancel()
	a.link.notify()
}
func (a *mailAccountState) closeData() {
	a.mu.Lock()
	old, done := a.data, a.dataDone
	a.data = nil
	a.dataDone = nil
	a.id = SessionID{}
	a.mu.Unlock()
	if old != nil {
		old.close()
	}
	if done != nil {
		<-done
	}
}
func (a *mailAccountState) attach(id SessionID) error {
	a.attachMu.Lock()
	defer a.attachMu.Unlock()
	a.link.mu.Lock()
	current := a.link.attached
	a.link.mu.Unlock()
	if current != id {
		return ErrClosed
	}
	a.mu.Lock()
	if a.failed || !a.ready {
		a.mu.Unlock()
		return ErrClosed
	}
	if a.id == id {
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()
	a.closeData()
	p, err := openMailPipe(a.ctx, a.config)
	if err != nil {
		a.fail()
		return err
	}
	if _, err = p.selectLog(a.link.dataFolder(id, false), false); err != nil {
		p.close()
		a.fail()
		return err
	}
	a.link.mu.Lock()
	current = a.link.attached
	a.link.mu.Unlock()
	if current != id {
		p.close()
		return ErrClosed
	}
	done := make(chan struct{})
	a.mu.Lock()
	a.data, a.dataDone, a.id = p, done, id
	a.mu.Unlock()
	go func() {
		defer close(done)
		if err := p.receive(a.ctx, a.link.incoming); err != nil && a.ctx.Err() == nil {
			a.mu.Lock()
			current := a.data == p
			a.mu.Unlock()
			if current {
				a.fail()
			}
		}
	}()
	a.link.notify()
	return nil
}

func (a *mailAccountState) run() {
	defer a.fail()
	select {
	case a.link.login <- struct{}{}:
	case <-a.ctx.Done():
		return
	}
	control, err := openMailPipe(a.ctx, a.config)
	if err != nil {
		<-a.link.login
		return
	}
	defer control.close()
	if _, err = control.selectLog(a.link.controlFolder(false), true); err != nil {
		<-a.link.login
		return
	}
	writer, err := openMailPipe(a.ctx, a.config)
	<-a.link.login
	if err != nil {
		return
	}
	defer writer.close()
	a.mu.Lock()
	a.control, a.writer, a.ready = control, writer, true
	a.mu.Unlock()
	a.link.notify()
	a.link.mu.Lock()
	id := a.link.attached
	a.link.mu.Unlock()
	if id != (SessionID{}) {
		go func() { _ = a.attach(id) }()
	}
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		if err := control.receive(a.ctx, a.link.incoming); err != nil {
			a.fail()
		}
	}()
	defer func() { a.cancel(); control.close(); <-readDone }()
	created := map[string]bool{}
	for {
		var work mailWork
		select {
		case work = <-a.fast:
		default:
			select {
			case <-a.ctx.Done():
				return
			case work = <-a.fast:
			case work = <-a.bulk:
			}
		}
		folder := a.link.controlFolder(true)
		if !work.control {
			_, packetID, _, err := parseHeader(work.body)
			if err != nil {
				return
			}
			folder = a.link.dataFolder(packetID, true)
		}
		encoded := encodeMail(work.body)
		if a.spent+len(encoded) > prototypePhysicalBudget {
			return
		}
		if !created[folder] {
			// Both authenticated endpoints may create the same new namespace.
			// Tagged CREATE refusal is tolerated; APPEND's actual completion is
			// still required and failures retire only this account.
			if err := writer.client.Create(folder, nil).Wait(); err != nil {
				var status *imap.Error
				if !errors.As(err, &status) {
					return
				}
			}
			created[folder] = true
		}
		a.spent += len(encoded) // unknown APPEND is charged, never assumed absent
		if err = writer.append(folder, encoded); err != nil {
			return
		}
		a.appended.Add(1)
	}
}

type MailSnapshot struct {
	Accounts int    `json:"accounts"`
	Ready    int    `json:"ready_accounts"`
	Data     int    `json:"data_accounts"`
	Failed   int    `json:"failed_accounts"`
	Appended uint64 `json:"appended_messages"`
}

func (l *IMAPLink) Snapshot() MailSnapshot {
	s := MailSnapshot{Accounts: len(l.accounts)}
	for _, a := range l.accounts {
		a.mu.Lock()
		if a.ready {
			s.Ready++
		}
		if a.id != (SessionID{}) {
			s.Data++
		}
		if a.failed {
			s.Failed++
		}
		a.mu.Unlock()
		s.Appended += a.appended.Load()
	}
	return s
}

type mailPipe struct {
	client    *imapclient.Client
	conn      *tls.Conn
	cancel    func() bool
	wake      chan struct{}
	invalid   atomic.Bool
	after     uint32
	done      chan struct{}
	closeOnce sync.Once
}

func openMailPipe(ctx context.Context, a MailAccount) (*mailPipe, error) {
	dial, stop := context.WithTimeout(ctx, 15*time.Second)
	defer stop()
	raw, err := (&net.Dialer{}).DialContext(dial, "tcp", net.JoinHostPort(a.Host, strconv.Itoa(a.Port)))
	if err != nil {
		return nil, ErrClosed
	}
	conn := tls.Client(raw, &tls.Config{ServerName: a.Host, RootCAs: a.Roots, MinVersion: tls.VersionTLS12})
	if err = conn.HandshakeContext(dial); err != nil {
		conn.Close()
		return nil, ErrAuth
	}
	p := &mailPipe{conn: conn, wake: make(chan struct{}, 1), done: make(chan struct{})}
	p.cancel = context.AfterFunc(ctx, func() { conn.Close() })
	changed := func() {
		select {
		case p.wake <- struct{}{}:
		default:
		}
	}
	p.client = imapclient.New(conn, &imapclient.Options{UnilateralDataHandler: &imapclient.UnilateralDataHandler{Mailbox: func(*imapclient.UnilateralDataMailbox) { changed() }, Expunge: func(uint32) { p.invalid.Store(true); changed() }}})
	if err = p.client.Login(a.Username, a.Password).Wait(); err != nil {
		p.close()
		return nil, ErrAuth
	}
	return p, nil
}
func (p *mailPipe) close() {
	p.closeOnce.Do(func() { close(p.done); p.cancel(); p.conn.Close(); p.client.Close() })
}
func (p *mailPipe) selectLog(folder string, tail bool) (*imap.SelectData, error) {
	if err := p.client.Create(folder, nil).Wait(); err != nil {
		var status *imap.Error
		if !errors.As(err, &status) {
			return nil, ErrClosed
		}
	}
	selected, err := p.client.Select(folder, nil).Wait()
	if err != nil || selected.UIDValidity == 0 {
		return nil, ErrProtocol
	}
	if tail {
		p.after = selected.NumMessages
	}
	return selected, nil
}
func (p *mailPipe) append(folder string, body []byte) error {
	cmd := p.client.Append(folder, int64(len(body)), nil)
	if _, err := cmd.Write(body); err != nil {
		return ErrClosed
	}
	if err := cmd.Close(); err != nil {
		return ErrClosed
	}
	if _, err := cmd.Wait(); err != nil {
		return ErrClosed
	}
	return nil
}
func (p *mailPipe) receive(ctx context.Context, out chan<- []byte) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if p.invalid.Load() {
			return ErrProtocol
		}
		mbox := p.client.Mailbox()
		if mbox == nil || mbox.NumMessages < p.after {
			return ErrProtocol
		}
		if mbox.NumMessages > p.after {
			stop := min(mbox.NumMessages, p.after+8)
			cmd := p.client.Fetch(imap.SeqSet{{Start: p.after + 1, Stop: stop}}, &imap.FetchOptions{UID: true, RFC822Size: true, BodySection: []*imap.FetchItemBodySection{{Peek: true}}})
			for row := cmd.Next(); row != nil; row = cmd.Next() {
				if p.invalid.Load() || row.SeqNum != p.after+1 || row.SeqNum > stop {
					p.conn.Close()
					cmd.Close()
					return ErrProtocol
				}
				var raw []byte
				size := int64(-1)
				for item := row.Next(); item != nil; item = row.Next() {
					switch value := item.(type) {
					case imapclient.FetchItemDataRFC822Size:
						size = value.Size
					case imapclient.FetchItemDataBodySection:
						if raw != nil || value.Literal == nil {
							p.conn.Close()
							cmd.Close()
							return ErrProtocol
						}
						var err error
						raw, err = io.ReadAll(io.LimitReader(value.Literal, 2*maxWire+1))
						if err != nil || len(raw) > 2*maxWire {
							p.conn.Close()
							cmd.Close()
							return ErrCapacity
						}
					}
				}
				if raw == nil || int64(len(raw)) != size {
					p.conn.Close()
					cmd.Close()
					return ErrProtocol
				}
				body, err := decodeMail(raw)
				if err == nil {
					select {
					case out <- body:
					case <-ctx.Done():
						p.conn.Close()
						cmd.Close()
						return ctx.Err()
					}
				}
				p.after = row.SeqNum
			}
			if err := cmd.Close(); err != nil {
				return ErrClosed
			}
			if p.after != stop {
				return ErrProtocol
			}
			continue
		}
		if p.client.Caps().Has(imap.CapIdle) {
			idle, err := p.client.Idle()
			if err != nil {
				return ErrClosed
			}
			// An EXISTS arriving between the check above and IDLE must not
			// sleep until another message. The bounded wake edge is retained.
			select {
			case <-p.done:
				idle.Close()
				return ErrClosed
			case <-ctx.Done():
				p.conn.Close()
				idle.Close()
				return ctx.Err()
			case <-p.wake:
			}
			if err = idle.Close(); err != nil {
				return ErrClosed
			}
		} else {
			begin := time.Now()
			if err := p.client.Noop().Wait(); err != nil {
				return ErrClosed
			}
			delay := min(25*time.Millisecond, max(time.Millisecond, time.Since(begin)/4))
			select {
			case <-p.done:
				return ErrClosed
			case <-ctx.Done():
				return ctx.Err()
			case <-p.wake:
			case <-time.After(delay):
			}
		}
	}
}

func encodeMail(body []byte) []byte {
	var out bytes.Buffer
	out.WriteString("From: transport@itom.invalid\r\nTo: transport@itom.invalid\r\nSubject: ITOM3\r\nMIME-Version: 1.0\r\nContent-Type: application/octet-stream\r\nContent-Transfer-Encoding: base64\r\n\r\n")
	b := base64.StdEncoding.EncodeToString(body)
	for len(b) > 0 {
		n := min(76, len(b))
		out.WriteString(b[:n])
		out.WriteString("\r\n")
		b = b[n:]
	}
	return out.Bytes()
}
func decodeMail(raw []byte) ([]byte, error) {
	if len(raw) > 2*maxWire {
		return nil, ErrCapacity
	}
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, ErrProtocol
	}
	if message.Header.Get("Content-Type") != "application/octet-stream" || !strings.EqualFold(message.Header.Get("Content-Transfer-Encoding"), "base64") {
		return nil, ErrProtocol
	}
	body, err := io.ReadAll(io.LimitReader(base64.NewDecoder(base64.StdEncoding, message.Body), maxWire+1))
	if err != nil || len(body) > maxWire {
		return nil, ErrProtocol
	}
	if _, _, _, err = parseHeader(body); err != nil {
		return nil, err
	}
	return body, nil
}

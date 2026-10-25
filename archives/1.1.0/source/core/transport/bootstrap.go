package transport

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

type NetworkDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}
type Endpoint struct {
	Address, ServerName, Username, Password, Mailbox string
	// AccountID is the trusted profile's quota identity for these credentials.
	// An unbound executor can run protocol checks, not accounted publications.
	AccountID string
	TLS       *tls.Config
}
type Selected struct {
	Executor     *Executor
	UIDValidity  uint32
	LiteralPlus  bool
	LiteralMinus bool
	Idle         bool
	Keywords     bool
	UIDPlus      bool
}

// DialSelected performs a bounded verified TLS/LOGIN/SELECT bootstrap, then
// transfers sole connection ownership to the executor. No old mailbox.Client or
// operation mutex is involved. Error strings never contain provider responses,
// mailbox names, credentials or target hostnames.
func DialSelected(ctx context.Context, d NetworkDialer, p Endpoint, l IMAPLimits) (Selected, error) {
	if d == nil || p.Address == "" || p.ServerName == "" || len(p.AccountID) > 64 {
		return Selected{}, ErrProtocol
	}
	user, err := quoteMailbox(p.Username)
	if err != nil {
		return Selected{}, err
	}
	password, err := quoteMailbox(p.Password)
	if err != nil {
		return Selected{}, err
	}
	folder, err := quoteMailbox(p.Mailbox)
	if err != nil {
		return Selected{}, err
	}
	if l.ProgressTimeout <= 0 {
		return Selected{}, ErrCapacity
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: p.ServerName}
	if p.TLS != nil {
		cfg = p.TLS.Clone()
		cfg.ServerName = p.ServerName
		if cfg.MinVersion < tls.VersionTLS12 {
			cfg.MinVersion = tls.VersionTLS12
		}
	}
	if cfg.InsecureSkipVerify {
		return Selected{}, ErrProtocol
	}
	raw, err := d.DialContext(ctx, "tcp", p.Address)
	if err != nil {
		return Selected{}, ErrUnknown
	}
	conn := tls.Client(raw, cfg)
	owned := false
	defer func() {
		if !owned {
			conn.Close()
		}
	}()
	cancel := context.AfterFunc(ctx, func() { conn.Close() })
	defer cancel()
	if err = conn.SetDeadline(time.Now().Add(l.ProgressTimeout)); err != nil {
		return Selected{}, ErrUnknown
	}
	if err = conn.HandshakeContext(ctx); err != nil {
		return Selected{}, ErrUnknown
	}
	// Reuse only bounded line/writer utilities; the asynchronous parser starts
	// after successful SELECT and buffered bytes are handed over intact.
	x := &Executor{conn: conn, reader: bufio.NewReaderSize(conn, 8192), limits: l}
	line, err := x.line()
	if err != nil || !strings.HasPrefix(strings.ToUpper(line), "* OK") {
		return Selected{}, ErrProtocol
	}
	if err = x.write([]byte("BOOT1 LOGIN " + user + " " + password + "\r\n")); err != nil {
		return Selected{}, ErrUnknown
	}
	if _, err = bootstrapReply(x, "BOOT1"); err != nil {
		return Selected{}, err
	}
	if err = x.write([]byte("BOOT2 CAPABILITY\r\n")); err != nil {
		return Selected{}, ErrUnknown
	}
	lines, err := bootstrapReply(x, "BOOT2")
	if err != nil {
		return Selected{}, err
	}
	plus, minus, idle, uidPlus := false, false, false, false
	for _, line := range lines {
		f := strings.Fields(strings.ToUpper(line))
		if len(f) > 2 && f[0] == "*" && f[1] == "CAPABILITY" {
			for _, capability := range f[2:] {
				if capability == "LITERAL+" {
					plus = true
				}
				if capability == "LITERAL-" {
					minus = true
				}
				if capability == "UIDPLUS" {
					uidPlus = true
				}
				if capability == "IDLE" {
					idle = true
				}
			}
		}
	}
	if err = x.write([]byte("BOOT3 SELECT " + folder + "\r\n")); err != nil {
		return Selected{}, ErrUnknown
	}
	lines, err = bootstrapReply(x, "BOOT3")
	if err != nil {
		return Selected{}, err
	}
	var validity uint64
	keywords := false
	for _, line := range lines {
		upper := strings.ToUpper(line)
		if at := strings.Index(upper, "[PERMANENTFLAGS ("); at >= 0 {
			tail := upper[at+len("[PERMANENTFLAGS ("):]
			end := strings.Index(tail, ")]")
			if end < 0 {
				return Selected{}, ErrProtocol
			}
			for _, flag := range strings.Fields(tail[:end]) {
				if flag == `\*` {
					keywords = true
				}
			}
		}
		const code = "[UIDVALIDITY "
		i := strings.Index(upper, code)
		if i >= 0 {
			tail := upper[i+len(code):]
			end := strings.IndexByte(tail, ']')
			if end < 0 {
				return Selected{}, ErrProtocol
			}
			v, e := strconv.ParseUint(tail[:end], 10, 32)
			if e != nil || v == 0 || validity != 0 && validity != v {
				return Selected{}, ErrProtocol
			}
			validity = v
		}
	}
	if validity == 0 {
		return Selected{}, ErrProtocol
	}
	if !cancel() {
		return Selected{}, ErrUnknown
	}
	if err = ctx.Err(); err != nil {
		return Selected{}, err
	}
	if err = conn.SetDeadline(time.Time{}); err != nil {
		return Selected{}, ErrUnknown
	}
	executor, err := newSelectedExecutor(conn, x.reader, l, p.Mailbox, uint32(validity), plus, minus, idle)
	if err != nil {
		return Selected{}, err
	}
	executor.account = p.AccountID
	executor.keywords = keywords
	executor.uidPlus = uidPlus
	owned = true
	return Selected{Executor: executor, UIDValidity: uint32(validity), LiteralPlus: plus, LiteralMinus: minus, Idle: idle, Keywords: keywords, UIDPlus: uidPlus}, nil
}
func bootstrapReply(x *Executor, tag string) ([]string, error) {
	var out []string
	total := 0
	for {
		line, err := x.line()
		if err != nil {
			return nil, ErrUnknown
		}
		total += len(line)
		if total > x.limits.ResponseBytes {
			return nil, ErrCapacity
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			return nil, ErrProtocol
		}
		if f[0] == tag {
			if f[1] != "OK" {
				return nil, ErrRefused
			}
			return append(out, line), nil
		}
		if f[0] != "*" || strings.EqualFold(f[1], "BYE") || strings.HasSuffix(line, "}") {
			return nil, ErrProtocol
		}
		out = append(out, line)
		if len(out) > 256 {
			return nil, fmt.Errorf("%w: bootstrap response count", ErrCapacity)
		}
	}
}

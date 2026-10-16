// Package mailbox is a bounded, serialized TLS IMAP adapter. It never retries
// APPEND; a lost completion is an unknown physical effect for the journal.
package mailbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"io"
	"net"
	"net/mail"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const MaxBody = 1 << 20
const MaxMIME = 2 << 20

var ErrLimit = errors.New("IMAP_LIMIT")
var ErrScope = errors.New("IMAP_SCOPE")
var ErrChanged = errors.New("IMAP_UIDVALIDITY_CHANGED")
var ErrMailboxStateChanged = errors.New("IMAP_MAILBOX_STATE_CHANGED")
var ErrUnknown = errors.New("IMAP_APPEND_OUTCOME_UNKNOWN")
var ErrNotStarted = errors.New("IMAP_APPEND_NOT_STARTED")

type scopeError struct{ code string }

func (e *scopeError) Error() string { return e.code }
func (e *scopeError) Unwrap() error { return ErrScope }

func scopeFailure(code string) error { return &scopeError{code: code} }

// ScopeDetail returns a bounded application-defined reason for IMAP_SCOPE.
// It never exposes provider text, mailbox names, UIDs or credentials.
func ScopeDetail(err error) string {
	var detail *scopeError
	if errors.As(err, &detail) {
		return detail.code
	}
	if errors.Is(err, ErrScope) {
		return ErrScope.Error()
	}
	return ""
}

type Config struct {
	Host string `json:"host"`
	// DialIP pins only the TCP endpoint. Host remains the TLS identity. Empty uses DNS.
	DialIP   string `json:"dial_ip,omitempty"`
	Port     int    `json:"port"`
	TLS      string `json:"tls"`
	Username string `json:"username"`
	Password string `json:"password"`
	// DisableBinaryAppend opts this account out of RFC BINARY even if the
	// authenticated IMAP server advertises it. Default: automatic.
	DisableBinaryAppend bool `json:"disable_binary_append,omitempty"`
}

func ValidateDialIP(value string) error {
	if value == "" {
		return nil
	}
	ip, err := netip.ParseAddr(value)
	if err != nil || ip.Zone() != "" || ip.Unmap().IsUnspecified() || ip.Unmap().IsMulticast() || ip.Unmap().String() == "255.255.255.255" {
		return scopeFailure("IMAP_DIAL_IP_INVALID")
	}
	return nil
}

func (c Config) Validate() error {
	if err := ValidateDialIP(c.DialIP); err != nil {
		return err
	}
	if c.Host == "" || strings.ContainsAny(c.Host, "\r\n /\\") || c.Port < 1 || c.Port > 65535 || (c.TLS != "tls" && c.TLS != "starttls") || len(c.Username) == 0 || len(c.Username) > 320 || len(c.Password) == 0 || len(c.Password) > 4096 || strings.ContainsAny(c.Username+c.Password, "\r\n\x00") {
		return ErrScope
	}
	return nil
}

type cappedConn struct {
	net.Conn
	closeOnce             sync.Once
	closeMu               sync.Mutex
	closeHooks            []func()
	closed                bool
	closeErr              error
	left                  atomic.Int64
	lastRead              atomic.Int64
	id                    uint64
	lastIO                atomic.Int64
	readBytes, writeBytes atomic.Int64
	firstClose            atomic.Pointer[socketFailure]
}

func (c *cappedConn) Close() error {
	c.closeOnce.Do(func() {
		err := c.Conn.Close()
		c.closeMu.Lock()
		c.closed, c.closeErr = true, err
		hooks := c.closeHooks
		c.closeHooks = nil
		c.closeMu.Unlock()
		for _, fn := range hooks {
			fn()
		}
	})
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	return c.closeErr
}

// OnClose releases a service reservation, never a physical mailbox charge.
// Closing through EOF/error/timeout and explicit Close share the same hook.
func (c *Client) OnClose(fn func()) {
	if fn == nil {
		return
	}
	c.conn.closeMu.Lock()
	closed := c.conn.closed
	if !closed {
		c.conn.closeHooks = append(c.conn.closeHooks, fn)
	}
	c.conn.closeMu.Unlock()
	if closed {
		fn()
	}
}

func (c *cappedConn) Read(b []byte) (int, error) {
	left := c.left.Load()
	if left <= 0 {
		return 0, ErrLimit
	}
	if int64(len(b)) > left {
		b = b[:left]
	}
	n, e := c.Conn.Read(b)
	c.left.Add(-int64(n))
	if n > 0 {
		c.lastRead.Store(time.Now().UnixNano())
		c.lastIO.Store(time.Now().UnixNano())
		c.readBytes.Add(int64(n))
	}
	if e != nil {
		c.noteClose("read", FailureReason(e))
	}
	return n, e
}

func (c *cappedConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	if n > 0 {
		c.lastIO.Store(time.Now().UnixNano())
		c.writeBytes.Add(int64(n))
	}
	if err != nil {
		c.noteClose("write", FailureReason(err))
	}
	return n, err
}

// LastReadProgress observes successful TCP reads without taking the command lock.
func (c *Client) LastReadProgress() time.Time {
	if c == nil || c.conn == nil {
		return time.Time{}
	}
	n := c.conn.lastRead.Load()
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}

type selectionProof struct {
	uidNext  uint32
	validity uint32
	checked  time.Time
	cost     time.Duration
}

type pendingIdleCompletion struct {
	done <-chan error
	stop func() bool
}

type Client struct {
	mu                                       sync.Mutex
	c                                        *imapclient.Client
	conn                                     *cappedConn
	folders                                  map[string]bool
	updates                                  chan struct{}
	idle                                     atomic.Bool
	literalMode                              string
	appendHints                              []AppendHint
	readSelects, readFetches, partialRetries atomic.Uint64
	pendingIdle                              *pendingIdleCompletion
	selectionMu                              sync.Mutex
	selections                               map[string]selectionProof
	closed                                   atomic.Bool
	busy                                     atomic.Bool
	receiveProgressBudget                    atomic.Int64
	binaryFetch                              atomic.Bool
	binaryAppend                             atomic.Bool
	binaryKey                                [32]byte
	tailGaps                                 map[string]*tailGapState
	tailGapMu                                sync.Mutex
	selectedFolder                           string // command mu
}

func (c *Client) SelectionCost(folder string) (time.Duration, bool) {
	if c == nil || c.closed.Load() {
		return 0, false
	}
	c.selectionMu.Lock()
	proof, ok := c.selections[folder]
	c.selectionMu.Unlock()
	return proof.cost, ok
}

type Cursor struct{ Validity, After uint32 }
type Message struct {
	Folder        string
	Validity, UID uint32
	MIME          []byte
}

func validFolder(s string) bool {
	return len(s) > 0 && len(s) <= 255 && !strings.EqualFold(s, "INBOX") && !strings.ContainsAny(s, "\r\n\x00")
}

// DialWithRoots allows a local trusted CA pool, without an insecure TLS mode.
func DialWithRoots(ctx context.Context, cfg Config, folders []string, roots *x509.CertPool) (*Client, error) {
	return dial(ctx, cfg, folders, &tls.Config{RootCAs: roots, ServerName: cfg.Host, MinVersion: tls.VersionTLS12})
}
func Dial(ctx context.Context, cfg Config, folders []string) (*Client, error) {
	return dial(ctx, cfg, folders, nil)
}
func dial(ctx context.Context, cfg Config, folders []string, testTLS *tls.Config) (*Client, error) {
	return dialMode(ctx, cfg, folders, testTLS, false)
}

// DialWithNetwork keeps all provider sockets outside the Android VPN. The
// supplied dialer must also use the underlying network for DNS resolution.
type NetworkDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

func DialWithNetwork(ctx context.Context, cfg Config, folders []string, network NetworkDialer) (*Client, error) {
	return dialNetwork(ctx, cfg, folders, nil, false, network)
}
func dialMode(ctx context.Context, cfg Config, folders []string, testTLS *tls.Config, admin bool) (*Client, error) {
	return dialNetwork(ctx, cfg, folders, testTLS, admin, nil)
}
func dialNetwork(ctx context.Context, cfg Config, folders []string, testTLS *tls.Config, admin bool, network NetworkDialer) (*Client, error) {
	if e := cfg.Validate(); e != nil {
		return nil, e
	}
	allowed := map[string]bool{}
	for _, f := range folders {
		if !validFolder(f) {
			return nil, ErrScope
		}
		allowed[f] = true
	}
	if admin {
		allowed[""] = true
	}
	if len(allowed) == 0 || len(allowed) > 256 {
		return nil, ErrScope
	}
	if network == nil {
		network = &net.Dialer{Timeout: 15 * time.Second}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	dialHost := cfg.Host
	if cfg.DialIP != "" {
		dialHost = cfg.DialIP
	}
	n, e := network.DialContext(ctx, "tcp", net.JoinHostPort(dialHost, fmt.Sprint(cfg.Port)))
	if e != nil {
		return nil, connectionFailed("IMAP_CONNECT_FAILED", "tcp", e)
	}
	conn := &cappedConn{Conn: n, id: socketGeneration.Add(1)}
	conn.left.Store(256 << 10)
	stop := context.AfterFunc(ctx, func() { n.Close() })
	defer stop()
	tc := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}
	if testTLS != nil {
		tc = testTLS.Clone()
	}
	updates := make(chan struct{}, 1)
	handler := &imapclient.UnilateralDataHandler{Mailbox: func(data *imapclient.UnilateralDataMailbox) {
		// IDLE reports new mail as an unsolicited mailbox-size update. Keep the
		// callback non-blocking: go-imap executes it on its decoder goroutine.
		if data == nil || data.NumMessages == nil {
			return
		}
		select {
		case updates <- struct{}{}:
		default:
		}
	}}
	options := &imapclient.Options{UnilateralDataHandler: handler}
	var cli *imapclient.Client
	if cfg.TLS == "tls" {
		secured := tls.Client(conn, tc)
		if e = secured.HandshakeContext(ctx); e == nil {
			cli = imapclient.New(secured, options)
		}
	} else {
		options.TLSConfig = tc
		cli, e = imapclient.NewStartTLS(conn, options)
	}
	if e != nil {
		n.Close()
		return nil, connectionFailed("IMAP_TLS_FAILED", "tls", e)
	}
	stage := "greeting"
	if e = cli.WaitGreeting(); e == nil {
		stage = "login"
		e = cli.Login(cfg.Username, cfg.Password).Wait()
	}
	if e != nil {
		cli.Close()
		return nil, connectionFailed("IMAP_AUTH_OR_GREETING_FAILED", stage, e)
	}
	if ctx.Err() != nil {
		cli.Close()
		return nil, ctx.Err()
	}
	caps := cli.Caps()
	out := &Client{c: cli, conn: conn, folders: allowed, updates: updates}
	out.literalMode = "synchronizing"
	if caps.Has(imap.CapLiteralPlus) {
		out.literalMode = "literal-plus"
	} else if caps.Has(imap.CapLiteralMinus) {
		out.literalMode = "literal-minus"
	}
	out.idle.Store(caps.Has(imap.CapIdle) || caps.Has(imap.CapIMAP4rev2))
	out.binaryFetch.Store(caps.Has(imap.CapBinary) || caps.Has(imap.CapIMAP4rev2))
	// Decide per authenticated account/connection. A definite tagged NO will
	// disable BINARY for future operations; an ambiguous APPEND is never retried.
	out.binaryKey = binaryPolicyKey(cfg)
	out.binaryAppend.Store(!cfg.DisableBinaryAppend && binaryAllowedByRefusals(out.binaryKey) && (caps.Has(imap.CapBinary) || caps.Has(imap.CapIMAP4rev2)))
	go func() {
		<-cli.Closed()
		out.closed.Store(true)
		// Closed synchronizes decoder completion; Close now retrieves its typed
		// original failure without sending another command or waiting on IO.
		conn.noteClose("decoder", FailureReason(cli.Close()))
	}()
	go out.keepIdleConnectionAlive()
	return out, nil
}

// LocalAddr identifies the authenticated socket for scoped diagnostics.
func (c *Client) LocalAddr() net.Addr { return c.conn.LocalAddr() }

// RemoteAddr completes the socket tuple; local ports can be reused for different providers.
func (c *Client) RemoteAddr() net.Addr { return c.conn.RemoteAddr() }

// Advertised at authenticated dial; contains no provider identity or secrets.
func (c *Client) AppendLiteralMode() string { return c.literalMode }

// BinaryFetchEnabled describes receive capability, not binary APPEND support.
func (c *Client) BinaryFetchEnabled() bool  { return c.binaryFetch.Load() }
func (c *Client) BinaryAppendEnabled() bool { return c.binaryAppend.Load() }

func (c *Client) Close() error {
	if c.Ready() {
		c.conn.noteClose("local", "closed")
	}
	c.closed.Store(true)
	err := c.c.Close()
	c.mu.Lock()
	if c.pendingIdle != nil {
		c.pendingIdle.stop()
		c.pendingIdle = nil
	}
	c.mu.Unlock()
	return err
}

// Ping measures an authenticated NOOP round trip. Use a dedicated connection
// for baseline measurements so queued application work cannot inflate baseline.
// It performs no mailbox selection, mutation or retry.
func (c *Client) Ping(ctx context.Context) (time.Duration, error) {
	if c == nil || c.closed.Load() {
		return 0, net.ErrClosed
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pingLocked(ctx)
}
func (c *Client) pingLocked(ctx context.Context) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	c.conn.left.Store(256 << 10)
	stop := context.AfterFunc(ctx, func() { _ = c.conn.Close() })
	defer stop()
	start := time.Now()
	err := c.c.Noop().Wait()
	if ctx.Err() != nil {
		return time.Since(start), ctx.Err()
	}
	return time.Since(start), err
}

// SelectionProof is a successful SELECT observation on this authenticated,
// folder-scoped client. It is only a reader audit hint, never deletion/refund
// proof. Reading it cannot wait behind provider I/O on the client's main mutex.
func (c *Client) SelectionProof(folder string) (uint32, time.Time, bool) {
	if c == nil || c.closed.Load() {
		return 0, time.Time{}, false
	}
	c.selectionMu.Lock()
	proof, ok := c.selections[folder]
	c.selectionMu.Unlock()
	return proof.validity, proof.checked, ok
}

// SelectionAllocationProof also carries the SELECT allocation snapshot. It
// proves old allocated holes, never unallocated UIDs or physical deletion.
func (c *Client) SelectionAllocationProof(folder string) (uint32, uint32, time.Time, bool) {
	if c == nil || c.closed.Load() {
		return 0, 0, time.Time{}, false
	}
	c.selectionMu.Lock()
	proof, ok := c.selections[folder]
	c.selectionMu.Unlock()
	return proof.validity, proof.uidNext, proof.checked, ok && proof.uidNext != 0
}

// IdleCapable reports whether this authenticated connection can wait for
// unsolicited mailbox updates without polling. It is immutable after login.
func (c *Client) IdleCapable() bool { return c != nil && c.idle.Load() }

// IdleWait blocks on IMAP IDLE until the selected mailbox changes or maxWait
// elapses so the caller can perform its periodic UIDVALIDITY audit. The reader
// connection is dedicated, therefore holding c.mu for the IDLE lifetime cannot
// head-of-line block control, DATA or cleanup traffic.
func (c *Client) IdleWait(ctx context.Context, folder string, maxWait time.Duration) (bool, error) {
	notified, _, err := c.IdleWaitWake(ctx, folder, maxWait, nil)
	return notified, err
}

// IdleWaitWake is the latency-sensitive form of IdleWait. A local transport
// request can close the current IDLE command with DONE through wake without
// closing or reconnecting the authenticated reader socket. This is deliberately
// distinct from context cancellation: cancellation still aborts the socket so a
// blocked network operation cannot outlive its lane, while a wake merely asks
// the caller to perform an immediate mailbox tail check.
func (c *Client) IdleWaitWake(ctx context.Context, folder string, maxWait time.Duration, wake <-chan struct{}) (notified, kicked bool, err error) {
	return c.idleWaitWake(ctx, folder, maxWait, wake, false)
}

// IdleWaitWakeAhead writes DONE before returning, allowing the next bounded
// UID FETCH to overlap its tagged completion. One pending completion is kept;
// operation checks it before returning any result. RFC2177/RFC9051 ordering
// is preserved: no command bytes precede the complete DONE continuation.
func (c *Client) IdleWaitWakeAhead(ctx context.Context, folder string, maxWait time.Duration, wake <-chan struct{}) (notified, kicked bool, err error) {
	return c.idleWaitWake(ctx, folder, maxWait, wake, true)
}

func (c *Client) joinIdle() error {
	if c.pendingIdle == nil {
		return nil
	}
	pending := c.pendingIdle
	c.pendingIdle = nil
	defer pending.stop()
	return <-pending.done
}

func (c *Client) idleWaitWake(ctx context.Context, folder string, maxWait time.Duration, wake <-chan struct{}, ahead bool) (notified, kicked bool, err error) {
	if c == nil || !c.idle.Load() {
		return false, false, nil
	}
	if maxWait <= 0 || maxWait > 30*time.Second {
		return false, false, ErrScope
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.folders[folder] {
		return false, false, ErrScope
	}
	if ctx.Err() != nil {
		return false, false, ctx.Err()
	}

	if err := c.joinIdle(); err != nil {
		return false, false, err
	}

	// Both events can race with the empty tail FETCH immediately before IDLE.
	// Keep either one as a bounded recheck request rather than discarding it.
	select {
	case <-c.updates:
		return true, false, nil
	default:
	}
	if wake != nil {
		select {
		case <-wake:
			return false, true, nil
		default:
		}
	}
	c.conn.left.Store(1 << 20)
	stop := context.AfterFunc(ctx, func() { _ = c.conn.Close() })
	defer func() {
		if stop != nil {
			stop()
		}
	}()
	idleCmd, err := c.c.Idle()
	if err != nil {
		if ctx.Err() != nil {
			return false, false, ctx.Err()
		}
		// A provider may advertise IDLE through a front-end that rejects it on a
		// particular backend. If the authenticated connection is still selected,
		// disable push for this socket and let the caller use bounded polling.
		if c.c.State() == imap.ConnStateSelected {
			c.idle.Store(false)
			return false, false, nil
		}
		return false, false, err
	}
	done := make(chan error, 1)
	go func() { done <- idleCmd.Wait() }()
	timer := time.NewTimer(maxWait)
	defer timer.Stop()

	finish := func() error {
		closeErr := idleCmd.Close()
		if ahead && closeErr == nil {
			c.pendingIdle = &pendingIdleCompletion{done: done, stop: stop}
			stop = nil
			return nil
		}
		// Silence after DONE is not an allowed quiet IDLE period. Join or
		// interrupt the old command before another command owns this socket.
		last := time.Now()
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		var waitErr error
		absolute := time.NewTimer(30 * time.Second)
		defer absolute.Stop()
		for {
			select {
			case waitErr = <-done:
				goto joined
			case now := <-tick.C:
				if at := c.LastReadProgress(); at.After(last) {
					last = at
				}
				if now.Sub(last) >= c.ReceiveProgressBudget() {
					c.InterruptReceive()
					<-done
					waitErr = context.DeadlineExceeded
					goto joined
				}
			case <-absolute.C:
				c.InterruptReceive()
				<-done
				waitErr = context.DeadlineExceeded
				goto joined
			}
		}
	joined:
		if closeErr != nil {
			return closeErr
		}
		return waitErr
	}

	select {
	case <-ctx.Done():
		_ = idleCmd.Close()
		<-done
		return false, false, ctx.Err()
	case <-c.updates:
		return true, false, finish()
	case <-wake:
		return false, true, finish()
	case err := <-done:
		// A server-side IDLE termination is not a mailbox notification. Surface
		// the command result so the lane can repair/reconnect if necessary.
		return false, false, err
	case <-timer.C:
		return false, false, finish()
	}
}
func (c *Client) operation(ctx context.Context, folder string, fn func() error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.folders[folder] {
		return ErrScope
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !c.Ready() {
		return connectionFailed("IMAP_SOCKET_NOT_READY", "admission", net.ErrClosed)
	}
	c.busy.Store(true)
	defer c.busy.Store(false)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { c.conn.noteClose("local", FailureReason(ctx.Err())); c.closed.Store(true); c.conn.Close() })
	defer stop()
	c.conn.left.Store(4 << 20)
	e := fn()
	// The following command has already been sent after DONE. Its outcome is
	// not exposed until the older IDLE completion has also succeeded.
	idleErr := c.joinIdle()
	if e == nil {
		e = idleErr
	}
	if e != nil && !errors.Is(e, ErrChanged) {
		c.conn.noteClose("operation", FailureReason(e))
		c.closed.Store(true)
		c.conn.Close()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return e
}
func (c *Client) selectFolder(folder string) (*imap.SelectData, error) {
	c.readSelects.Add(1)
	started := time.Now()
	s, e := c.c.Select(folder, nil).Wait()
	if e != nil {
		if isProviderMailboxStateChange(e) {
			return nil, connectionFailed(ErrMailboxStateChanged.Error(), "select", e)
		}
		return nil, e
	}
	if err := c.recordSelection(folder, s, started); err != nil {
		return nil, err
	}
	return s, nil
}
func (c *Client) recordSelection(folder string, s *imap.SelectData, started time.Time) error {
	if s == nil || s.UIDValidity == 0 || s.UIDNext == 0 {
		return ErrScope
	}
	c.selectedFolder = folder
	if c.folders[folder] && !c.closed.Load() {
		c.selectionMu.Lock()
		if c.selections == nil {
			c.selections = make(map[string]selectionProof)
		}
		c.selections[folder] = selectionProof{validity: s.UIDValidity, uidNext: uint32(s.UIDNext), checked: time.Now(), cost: time.Since(started)}
		c.selectionMu.Unlock()
	}
	return nil
}
func (c *Client) Ensure(ctx context.Context, folder string) error {
	return c.operation(ctx, folder, func() error {
		if _, e := c.selectFolder(folder); e == nil {
			return nil
		} else {
			// A rejected SELECT is not generally evidence of a missing folder.
			// In particular, a provider incarnation/backend error must close this
			// generation and be retried with a fresh selection, never CREATE.
			var response *imap.Error
			if !errors.As(e, &response) || response.Type != imap.StatusResponseTypeNo ||
				(response.Code != "" && response.Code != imap.ResponseCodeNonExistent && response.Code != imap.ResponseCodeTryCreate) {
				return e
			}
		}
		if e := c.c.Create(folder, nil).Wait(); e != nil {
			// Another endpoint may have created the same dedicated folder.
			if _, selected := c.selectFolder(folder); selected == nil {
				return nil
			} else if errors.Is(selected, ErrMailboxStateChanged) {
				return selected
			}
			return errors.New("IMAP_CREATE_FAILED")
		}
		_, e := c.selectFolder(folder)
		return e
	})
}

const mimeHeader = "From: transport@itom.invalid\r\nTo: transport@itom.invalid\r\nSubject: ITOM\r\nMIME-Version: 1.0\r\nContent-Type: application/octet-stream\r\nContent-Transfer-Encoding: base64\r\n\r\n"

// Binary physical MIME keeps a canonical, strict header. The secure ITD3
// envelope and all grant/journal charges remain based on canonical base64
// encoded bytes, never on the smaller physical representation.
const binaryHeader = "From: transport@itom.invalid\r\nTo: transport@itom.invalid\r\nSubject: ITOM\r\nMIME-Version: 1.0\r\nContent-Type: application/octet-stream\r\nContent-Transfer-Encoding: binary\r\n\r\n"

func binaryPhysical(raw []byte) ([]byte, error) {
	if !bytes.HasPrefix(raw, []byte(mimeHeader)) {
		return nil, ErrScope
	}
	body, err := Decode(raw)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(binaryHeader)+len(body))
	copy(out, binaryHeader)
	copy(out[len(binaryHeader):], body)
	clear(body)
	return out, nil
}

// maxProviderMIMEHeaderDelta bounds uncharged provider-added MIME metadata.
// A successfully authenticated DATA body still requires a same-UID physical
// deletion proof before any journal quota can be released. A huge rewrite is
// not an eligible proof and must never be normalized into a quota refund.
const maxProviderMIMEHeaderDelta = 512

// canonicalSizeForReceived only recognizes transport MIME representations that
// decode to one bounded body. For changed provider headers, it regenerates the
// canonical charged length from the decoded body, never from untrusted headers.
// Physical IMAP RFC822.SIZE is verified against BODY literal BEFORE this call.
func canonicalSizeForReceived(raw []byte) (uint64, bool, error) {
	// Even a message with our exact header can be rewritten by an IMAP
	// provider: an appended CRLF or different base64 line wrapping changes
	// physical RFC822.SIZE without changing the authenticated ITQ1 body.
	// Derive the journal charge from the decoded bytes, *not* the physical
	// MIME length; retain raw unchanged for UID/deletion proof.
	body, err := Decode(raw)
	if err != nil {
		return 0, false, err
	}
	defer clear(body)
	n, err := DataProofCharge(raw, body)
	if err != nil {
		return 0, false, err
	}
	return n, uint64(len(raw)) != n, nil
}

// DecodeDataProof returns the one decoded DATA body and its *canonical* durable
// charge, while leaving the source physical MIME unchanged for UID/deletion
// proof. A rewritten header is accepted only within the bounded provider delta.
// The caller owns the returned byte slice and must clear it after verification.
func DecodeDataProof(raw []byte) ([]byte, uint64, error) {
	body, err := Decode(raw)
	if err != nil {
		return nil, 0, err
	}
	charged, err := DataProofCharge(raw, body)
	if err != nil {
		clear(body)
		return nil, 0, err
	}
	return body, charged, nil
}

// DataProofCharge is the metadata-only half of DecodeDataProof. The decoded
// body MUST be obtained from Decode(raw) of the same immutable physical message;
// it is not independently authenticated. The caller must still validate the
// usage descriptor, ITD3 AEAD, grant, and journal/physical UID binding before
// accepting any of its bytes, and retain raw unchanged for physical GC proof.
func DataProofCharge(raw, decodedBody []byte) (uint64, error) {
	if len(decodedBody) == 0 || len(decodedBody) > MaxBody || len(raw) > MaxMIME {
		return 0, ErrLimit
	}
	charged, err := EncodedSize(len(decodedBody))
	if err != nil || uint64(len(raw)) > charged+maxProviderMIMEHeaderDelta {
		return 0, ErrLimit
	}
	// An exact canonical header is not an exact canonical MIME length: a
	// provider can add line endings while preserving every decoded byte.
	// Decode(raw) must have produced decodedBody from THIS physical row.
	// The caller still checks its authenticated digest, physical binding,
	// AEAD and eventually a real UID/deletion proof before any refund.
	return charged, nil
}

// CanonicalEncodedBytes returns the canonical journal charge for recognized
// provider representations, with bounded expansion for rewritten MIME headers.
// It is not proof of ownership: journal.ObserveMany still binds the actual
// authenticated body, physical batch, UID, account and folder before deletion.
func CanonicalEncodedBytes(raw []byte) (uint64, error) {
	n, _, err := canonicalSizeForReceived(raw)
	return n, err
}

// canonicalizeReceived normalizes recognized physical variants before the
// authenticated DATA descriptor length/digest check. No invalid ciphertext is
// ever accepted by this transformation: the downstream ITD3 binding, AEAD and
// replay verification remain mandatory.
// canonicalizeReceived preserves physical provider MIME for recovery/cleanup.
// Only the negotiated strict ITOM BINARY representation is normalized here;
// altered provider headers must remain available as exact deletion evidence.
func canonicalizeReceived(raw []byte) ([]byte, error) {
	if !bytes.HasPrefix(raw, []byte(binaryHeader)) {
		return raw, nil
	}
	return CanonicalDataMIME(raw)
}

// CanonicalDataMIME is a transient cryptographic verification view. An IMAP
// FETCH row retains its *original physical bytes* for WAL/UID proof, while only
// the temporary canonical view is passed to the DATA descriptor/AEAD validator.
// The caller must not use the view as physical deletion evidence.
func CanonicalDataMIME(raw []byte) ([]byte, error) {
	if bytes.HasPrefix(raw, []byte(mimeHeader)) {
		return raw, nil
	}
	var body []byte
	if bytes.HasPrefix(raw, []byte(binaryHeader)) {
		body = raw[len(binaryHeader):]
		if len(body) == 0 || len(body) > MaxBody {
			return nil, ErrLimit
		}
	} else {
		decoded, err := Decode(raw)
		if err != nil {
			return nil, err
		}
		body = decoded
		defer clear(decoded)
	}
	n, err := EncodedSize(len(body))
	if err != nil {
		return nil, err
	}
	if uint64(len(raw)) > n+maxProviderMIMEHeaderDelta {
		return nil, ErrLimit
	}
	return Encode(body)
}

// EncodedSize matches Encode without constructing the MIME body.
func EncodedSize(bodyBytes int) (uint64, error) {
	if bodyBytes < 1 || bodyBytes > MaxBody {
		return 0, ErrLimit
	}
	encoded := base64.StdEncoding.EncodedLen(bodyBytes)
	return uint64(len(mimeHeader) + encoded + 2*((encoded+75)/76)), nil
}

func Encode(body []byte) ([]byte, error) {
	if len(body) == 0 || len(body) > MaxBody {
		return nil, ErrLimit
	}
	size, _ := EncodedSize(len(body))
	b := make([]byte, int(size))
	at := copy(b, mimeHeader)
	for len(body) > 0 {
		n := min(57, len(body))
		encoded := base64.StdEncoding.EncodedLen(n)
		base64.StdEncoding.Encode(b[at:at+encoded], body[:n])
		at += encoded
		b[at], b[at+1] = '\r', '\n'
		at += 2
		body = body[n:]
	}
	return b, nil
}
func Decode(raw []byte) ([]byte, error) {
	if len(raw) > MaxMIME {
		return nil, ErrLimit
	}
	// The canonical header fixes the MIME interpretation. Decode its bounded
	// body directly; noncanonical provider headers retain the general parser.
	if bytes.HasPrefix(raw, []byte(binaryHeader)) {
		body := raw[len(binaryHeader):]
		if len(body) == 0 || len(body) > MaxBody {
			return nil, ErrLimit
		}
		return bytes.Clone(body), nil
	}
	if bytes.HasPrefix(raw, []byte(mimeHeader)) {
		body := raw[len(mimeHeader):]
		decoded := make([]byte, base64.StdEncoding.DecodedLen(len(body)))
		n, err := base64.StdEncoding.Decode(decoded, body)
		if err != nil || n == 0 || n > MaxBody {
			return nil, ErrLimit
		}
		return decoded[:n], nil
	}
	i := bytes.Index(raw, []byte("\r\n\r\n"))
	if i < 0 || i > 8192 {
		return nil, ErrScope
	}
	m, e := mail.ReadMessage(bytes.NewReader(raw))
	if e != nil {
		return nil, ErrScope
	}
	if m.Header.Get("Content-Type") != "application/octet-stream" || m.Header.Get("Content-Transfer-Encoding") != "base64" {
		return nil, ErrScope
	}
	b, e := io.ReadAll(io.LimitReader(base64.NewDecoder(base64.StdEncoding, m.Body), MaxBody+1))
	if e != nil || len(b) == 0 || len(b) > MaxBody {
		return nil, ErrLimit
	}
	return b, nil
}
func (c *Client) Append(ctx context.Context, folder string, raw []byte) error {
	if len(raw) == 0 || len(raw) > MaxMIME {
		return ErrLimit
	}
	// Conversion happens only after all durable quota transactions charge the
	// canonical bytes. Unknown APPEND is still fenced, never transparently retried.
	useBinary := c.binaryAppend.Load() && bytes.HasPrefix(raw, []byte(mimeHeader))
	physical := raw
	if useBinary {
		var err error
		physical, err = binaryPhysical(raw)
		if err != nil {
			return err
		}
		defer clear(physical)
	}
	started := false
	var appended *imap.AppendData
	e := c.operation(ctx, folder, func() error {
		started = true
		var cmd *imapclient.AppendCommand
		if useBinary {
			cmd = c.c.AppendBinary(folder, int64(len(physical)), nil)
		} else {
			cmd = c.c.Append(folder, int64(len(physical)), nil)
		}
		_, e := cmd.Write(physical)
		ce := cmd.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
		appended, e = cmd.Wait()
		return e
	})
	if e == nil && appended != nil && appended.UID != 0 && appended.UIDValidity != 0 {
		// BINARY conversion already decoded this exact BODY before APPEND.
		// Reuse it for the locator digest instead of decoding canonical MIME twice.
		var body []byte
		var decodeErr error
		if useBinary {
			body = physical[len(binaryHeader):]
		} else {
			body, decodeErr = Decode(raw)
			defer clear(body)
		}
		if decodeErr == nil {
			hint := AppendHint{MIMEBytes: uint64(len(raw)), Folder: folder, Validity: appended.UIDValidity, UID: uint32(appended.UID), Digest: sha256.Sum256(body)}
			c.mu.Lock()
			if len(c.appendHints) == 256 {
				copy(c.appendHints, c.appendHints[1:])
				c.appendHints = c.appendHints[:255]
			}
			c.appendHints = append(c.appendHints, hint)
			c.mu.Unlock()
		}
	}

	if e != nil && started {
		if useBinary {
			var status *imap.Error
			if errors.As(e, &status) && status.Type == imap.StatusResponseTypeNo && status.Code == imap.ResponseCodeUnknownCTE {
				rememberBinaryRefusal(c.binaryKey)
				// Tagged NO may mean unsupported literal8 on this folder/account.
				// Never replay the possibly delivered message; only subsequent
				// APPENDs use the canonical MIME representation.
				c.binaryAppend.Store(false)
			}
		}
		return &appendUnknownError{detail: ConnectionFailure{Stage: "append", Reason: FailureReason(e)}}
	}
	if e != nil && ConnectionDetail(e).Stage == "admission" {
		return &appendNotStartedError{detail: ConnectionDetail(e)}
	}
	return e
}

// fetchRange performs one bounded UID FETCH, including holes from expunged
// messages. No SEARCH, sequence-number addressing or per-UID round trips.
func (c *Client) fetchRange(first, last uint32, body bool) ([]Message, error) {
	if first == 0 || last < first || uint64(last)-uint64(first) >= 16 {
		return nil, ErrScope
	}
	return c.fetchSet(imap.UIDSet{{Start: imap.UID(first), Stop: imap.UID(last)}}, int(last-first)+1, body)
}
func (c *Client) fetchSet(set imap.UIDSet, count int, body bool) ([]Message, error) {
	return c.fetchSetStreaming(set, count, body, nil)
}

func (c *Client) fetchSetStreaming(set imap.UIDSet, count int, body bool, emit func(Message) error) ([]Message, error) {
	// UID/FLAGS updates are not BODY evidence. The parser retains the UID for a
	// later full row; if tagged FETCH still lacks it, a bounded UID SEARCH can
	// distinguish a concurrently expunged UID from a present malformed response.
	// A present UID must still produce complete size/BODY on a bounded retry.
	const partialRetries = 3
	// A complete row may have been delivered before a FLAGS-only row forces a
	// retry. Deliver it at most once during this operation; ordinary callers
	// still receive only the final complete, tagged-successful result.
	delivered := make(map[uint32]bool)
	var onRow func(Message) error
	if emit != nil {
		onRow = func(m Message) error {
			if delivered[m.UID] {
				return nil
			}
			if err := emit(m); err != nil {
				return err
			}
			delivered[m.UID] = true
			return nil
		}
	}
	for attempt := 0; attempt < partialRetries; attempt++ {
		rows, partial, err := c.fetchSetOnceStreaming(set, count, body, onRow)
		if err != nil {
			return nil, err
		}
		if !partial {
			return rows, nil
		}
		c.partialRetries.Add(1)
	}
	c.conn.Close()
	return nil, scopeFailure("IMAP_SCOPE_FETCH_PARTIAL_RETRY_EXHAUSTED")
}

func (c *Client) fetchSetOnce(set imap.UIDSet, count int, body bool) ([]Message, bool, error) {
	return c.fetchSetOnceStreaming(set, count, body, nil)
}

func (c *Client) fetchSetOnceStreaming(set imap.UIDSet, count int, body bool, emit func(Message) error) ([]Message, bool, error) {
	// Recovery, exact reads and GC need the original physical MIME. Restrict
	// the receive optimization to the live streaming reader, after recovery.
	useBinary := body && emit != nil && c.binaryFetch.Load()
	rows, partial, err := c.fetchSetFormatStreaming(set, count, body, useBinary, emit)
	if useBinary {
		var status *imap.Error
		unsupported := errors.As(err, &status) && (status.Type == imap.StatusResponseTypeNo || status.Type == imap.StatusResponseTypeBad)
		if unsupported || errors.Is(err, errBinaryRepresentation) {
			// FETCH is read-only and its tagged completion has been drained. This
			// fallback never repeats APPEND or changes durable message accounting.
			c.binaryFetch.Store(false)
			return c.fetchSetFormatStreaming(set, count, body, false, emit)
		}
	}
	return rows, partial, err
}

var errBinaryRepresentation = errors.New("IMAP_BINARY_NONCANONICAL")

func (c *Client) fetchSetFormatStreaming(set imap.UIDSet, count int, body, useBinary bool, emit func(Message) error) ([]Message, bool, error) {
	if count < 1 || count > 128 {
		return nil, false, ErrLimit
	}
	c.conn.left.Store(int64(count) * (MaxMIME + 8192))
	if !body {
		c.conn.left.Store(int64(count) * 8192)
	}
	opts := &imap.FetchOptions{UID: true, RFC822Size: true}
	section := &imap.FetchItemBodySection{Peek: true, Partial: &imap.SectionPartial{Size: MaxMIME + 1}}
	binarySection := &imap.FetchItemBinarySection{Part: []int{1}, Peek: true, Partial: &imap.SectionPartial{Size: MaxBody + 1}}
	if body {
		if useBinary {
			section = &imap.FetchItemBodySection{Specifier: imap.PartSpecifierHeader, Peek: true, Partial: &imap.SectionPartial{Size: 8193}}
			opts.BinarySection = []*imap.FetchItemBinarySection{binarySection}
		}
		opts.BodySection = []*imap.FetchItemBodySection{section}
	}
	c.readFetches.Add(1)
	cmd := c.c.Fetch(set, opts)
	defer cmd.Close()
	// Close before waiting for command completion on a malformed/oversized
	// response, so an attacker cannot trap the deferred command drain.
	fail := func(err error) ([]Message, bool, error) { c.conn.Close(); return nil, false, err }
	result := make([]Message, 0, count)
	seen := map[uint32]bool{}
	partialUIDs := map[uint32]bool{}
	noncanonical := false
	for m := cmd.Next(); m != nil; m = cmd.Next() {
		var got Message
		var size int64
		var hasUID, hasSize, hasBody, hasUpdateOnly bool
		var header, decoded []byte
		var hasHeader bool
		for item := m.Next(); item != nil; item = m.Next() {
			switch d := item.(type) {
			case imapclient.FetchItemDataUID:
				if hasUID {
					return fail(scopeFailure("IMAP_SCOPE_FETCH_DUP_UID_ITEM"))
				}
				hasUID = true
				got.UID = uint32(d.UID)
			case imapclient.FetchItemDataRFC822Size:
				if hasSize {
					return fail(scopeFailure("IMAP_SCOPE_FETCH_DUP_SIZE_ITEM"))
				}
				hasSize = true
				size = d.Size
				if size < 0 || size > MaxMIME {
					return fail(ErrLimit)
				}
			case imapclient.FetchItemDataBodySection:
				limit := int64(MaxMIME)
				duplicate := hasBody
				if useBinary {
					limit = 8192
					duplicate = hasHeader
				}
				if !body || duplicate || d.Literal == nil || d.Literal.Size() > limit || !d.MatchCommand(section) {
					return fail(ErrLimit)
				}
				var err error
				data, err := io.ReadAll(io.LimitReader(d.Literal, limit+1))
				if err != nil {
					return fail(err)
				}
				if int64(len(data)) != d.Literal.Size() {
					return fail(io.ErrUnexpectedEOF)
				}
				if int64(len(data)) > limit {
					return fail(ErrLimit)
				}
				if useBinary {
					hasHeader = true
					header = data
				} else {
					hasBody = true
					got.MIME = data
				}
			case imapclient.FetchItemDataBinarySection:
				if !useBinary || hasBody || !d.MatchCommand(binarySection) {
					return fail(ErrLimit)
				}
				if d.Literal == nil || d.Literal.Size() == 0 {
					// NIL/empty BINARY is not payload evidence. A completed
					// read-only request can still fall back to physical BODY.
					continue
				}
				if d.Literal.Size() > MaxBody {
					return fail(ErrLimit)
				}
				hasBody = true
				var err error
				decoded, err = io.ReadAll(io.LimitReader(d.Literal, MaxBody+1))
				if err != nil {
					return fail(err)
				}
				if int64(len(decoded)) != d.Literal.Size() {
					return fail(io.ErrUnexpectedEOF)
				}
				if len(decoded) < 1 || len(decoded) > MaxBody {
					return fail(ErrLimit)
				}
			case imapclient.FetchItemDataFlags, imapclient.FetchItemDataModSeq:
				// These items can arrive in an unsolicited message-state update.
				// They are never requested by fetchSet and are not sufficient proof
				// for the durable message body/size the caller needs.
				hasUpdateOnly = true
			}
		}
		if !hasUID {
			return fail(scopeFailure("IMAP_SCOPE_FETCH_MISSING_UID"))
		}
		if !set.Contains(imap.UID(got.UID)) {
			return fail(scopeFailure("IMAP_SCOPE_FETCH_UID_OUTSIDE_SET"))
		}
		complete := hasSize && (!body || hasBody)
		if !complete && !hasSize && !hasBody {
			// An update-only row is not body evidence. The parser retains its
			// requested UID until a complete row arrives. Retry only if tagged
			// completion leaves this UID unresolved; missing bodies fail closed.
			_ = hasUpdateOnly
			partialUIDs[got.UID] = true
			continue
		}
		if !hasSize {
			return fail(scopeFailure("IMAP_SCOPE_FETCH_MISSING_SIZE"))
		}
		if useBinary && (!hasBody || !hasHeader) {
			// A provider may omit an unsupported representation while returning
			// tagged OK. Drain the bounded read-only command before one BODY
			// fallback. Never publish the incomplete row or advance its cursor.
			noncanonical = true
			continue
		}
		if body && !hasBody {
			return fail(scopeFailure("IMAP_SCOPE_FETCH_MISSING_BODY"))
		}
		if useBinary {
			// A BINARY[1] read returns decoded octets; the same UID's
			// RFC822.SIZE is the *physical* envelope size. Convert to our
			// canonical MIME only after validating the original representation.
			physicalSize := int64(len(header) + len(decoded))
			if bytes.Equal(header, []byte(binaryHeader)) {
				if physicalSize != size {
					noncanonical = true
					continue
				}
			} else if bytes.Equal(header, []byte(mimeHeader)) {
				canonicalSize, err := EncodedSize(len(decoded))
				if err != nil || int64(canonicalSize) != size {
					noncanonical = true
					continue
				}
			} else {
				noncanonical = true
				continue
			}
			var err error
			got.MIME, err = Encode(decoded)
			if err != nil {
				return fail(err)
			}
			size = int64(len(got.MIME))
		} else if body {
			// BODY[] is the authoritative physical evidence for unknown
			// APPEND. Check its literal size *before* normalization.
			if int64(len(got.MIME)) != size {
				return fail(scopeFailure("IMAP_SCOPE_FETCH_BODY_SIZE_MISMATCH"))
			}
			var err error
			got.MIME, err = canonicalizeReceived(got.MIME)
			if err != nil {
				return fail(err)
			}
			size = int64(len(got.MIME))
		}
		if seen[got.UID] {
			return fail(scopeFailure("IMAP_SCOPE_FETCH_DUP_RESULT_UID"))
		}
		if body && int64(len(got.MIME)) != size {
			return fail(scopeFailure("IMAP_SCOPE_FETCH_BODY_SIZE_MISMATCH"))
		}
		if len(result) >= count {
			return fail(ErrLimit)
		}
		seen[got.UID] = true
		delete(partialUIDs, got.UID)
		result = append(result, got)
		if emit != nil {
			if err := emit(got); err != nil {
				return fail(err)
			}
		}
	}
	if err := cmd.Close(); err != nil {
		return nil, false, err
	}
	if noncanonical {
		return nil, false, errBinaryRepresentation
	}
	if len(partialUIDs) != 0 {
		// A queued update can name a UID expunged before this FETCH snapshot.
		// SEARCH is independent of FETCH update routing and only resolves absence;
		// it never authorizes a partial body, quota refund or cleanup proof.
		stillPresent, err := c.resolvePartialUIDs(partialUIDs)
		if err != nil {
			return fail(err)
		}
		if stillPresent {
			return nil, true, nil
		}
	}
	return result, false, nil
}

// Runs only after an ambiguous, tagged-successful FETCH, on the same serialized
// authenticated mailbox operation. Never query an unbounded range or SEARCH ALL.
func (c *Client) resolvePartialUIDs(unresolved map[uint32]bool) (bool, error) {
	if len(unresolved) == 0 || len(unresolved) > 128 {
		return false, ErrLimit
	}
	ids := make([]imap.UID, 0, len(unresolved))
	for uid := range unresolved {
		if uid == 0 {
			return false, scopeFailure("IMAP_SCOPE_FETCH_PARTIAL_SEARCH_INVALID_UID")
		}
		ids = append(ids, imap.UID(uid))
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	set := imap.UIDSetNum(ids...)
	c.conn.left.Store(128 << 10)
	found, err := c.c.UIDSearch(&imap.SearchCriteria{UID: []imap.UIDSet{set}}, nil).Wait()
	if err != nil {
		return false, err
	}
	result, ok := found.All.(imap.UIDSet)
	if !ok {
		return false, scopeFailure("IMAP_SCOPE_FETCH_PARTIAL_SEARCH_KIND")
	}
	present := false
	for _, part := range result {
		if part.Start == 0 || part.Stop < part.Start || uint64(part.Stop)-uint64(part.Start)+1 > uint64(len(unresolved)) {
			return false, scopeFailure("IMAP_SCOPE_FETCH_PARTIAL_SEARCH_RANGE")
		}
		for uid := part.Start; ; uid++ {
			if !unresolved[uint32(uid)] {
				return false, scopeFailure("IMAP_SCOPE_FETCH_PARTIAL_SEARCH_OUTSIDE_SET")
			}
			present = true
			if uid == part.Stop {
				break
			}
		}
	}
	return present, nil
}

// fetchPresenceSet is deliberately weaker than fetchSet: it needs only UID
// evidence. IMAP allows unsolicited FETCH updates to be emitted before the
// tagged completion of a UID FETCH. go-imap routes an update carrying a
// requested UID into the pending FetchCommand, so a concurrent STORE/EXPUNGE
// may yield UID+FLAGS without RFC822.SIZE. Treating that as malformed can kill
// maintenance even though the only safe conclusion is conservative presence.
// Any returned requested UID therefore means "present or concurrently changed";
// a later cleanup pass can prove absence after the update queue is drained.
func (c *Client) fetchPresenceSet(set imap.UIDSet, count int) (map[uint32]bool, error) {
	if count < 1 || count > 128 {
		return nil, ErrLimit
	}
	c.conn.left.Store(int64(count) * 8192)
	cmd := c.c.Fetch(set, &imap.FetchOptions{UID: true})
	defer cmd.Close()
	fail := func(err error) (map[uint32]bool, error) { c.conn.Close(); return nil, err }
	present := make(map[uint32]bool, count)
	for m := cmd.Next(); m != nil; m = cmd.Next() {
		// A requested UID may legitimately appear more than once here: one row
		// can be an unsolicited UID+FLAGS update and another the solicited UID
		// FETCH result.  Presence is a set, so de-duplicate instead of applying
		// fetchSet's one-row-per-UID invariant.  The connection read cap above
		// still bounds hostile/repeated rows.
		var uid uint32
		hasUID := false
		for item := m.Next(); item != nil; item = m.Next() {
			if d, ok := item.(imapclient.FetchItemDataUID); ok {
				if hasUID {
					return fail(scopeFailure("IMAP_SCOPE_FETCH_DUP_UID_ITEM"))
				}
				hasUID = true
				uid = uint32(d.UID)
			}
		}
		if !hasUID {
			return fail(scopeFailure("IMAP_SCOPE_FETCH_MISSING_UID"))
		}
		if !set.Contains(imap.UID(uid)) {
			return fail(scopeFailure("IMAP_SCOPE_FETCH_UID_OUTSIDE_SET"))
		}
		present[uid] = true
	}
	if err := cmd.Close(); err != nil {
		return nil, err
	}
	return present, nil
}

func (c *Client) fetch(uid uint32, body bool) (Message, bool, error) {
	rows, err := c.fetchRange(uid, uid, body)
	if err != nil {
		return Message{}, false, err
	}
	if len(rows) == 0 {
		return Message{}, false, nil
	}
	return rows[0], true, nil
}

// Tail returns the current mailbox incarnation and highest allocated UID without
// fetching message bodies. It is used to fence pre-session history before a new
// authenticated transport boot, so old mailbox contents cannot be replayed just
// because a process restarted.
func (c *Client) Tail(ctx context.Context, folder string) (Cursor, error) {
	var out Cursor
	err := c.operation(ctx, folder, func() error {
		s, err := c.selectFolder(folder)
		if err != nil {
			return err
		}
		out.Validity = s.UIDValidity
		if uint32(s.UIDNext) > 0 {
			out.After = uint32(s.UIDNext) - 1
		}
		return nil
	})
	return out, err
}

// Recent returns at most the last limit allocated UIDs together with a cursor
// positioned at the current mailbox tail. It is intentionally bounded and is
// used by pre-authentication to discover the server's latest boot challenge
// even when the client starts after that challenge was appended.
func (c *Client) Recent(ctx context.Context, folder string, limit int) ([]Message, Cursor, error) {
	if limit < 1 || limit > 64 {
		return nil, Cursor{}, ErrLimit
	}
	var out []Message
	var tail Cursor
	err := c.operation(ctx, folder, func() error {
		s, err := c.selectFolder(folder)
		if err != nil {
			return err
		}
		tail = Cursor{Validity: s.UIDValidity}
		if uint32(s.UIDNext) == 0 {
			return ErrScope
		}
		tail.After = uint32(s.UIDNext) - 1
		if tail.After == 0 {
			return nil
		}
		start := uint32(1)
		if tail.After >= uint32(limit) {
			start = tail.After - uint32(limit) + 1
		}
		// fetchRange is deliberately capped at 16 allocated UID numbers. Recent
		// supports a wider bounded pre-auth window, so split that window into
		// adjacent <=16-UID FETCH commands instead of feeding a 32/64-UID span
		// into fetchRange (which would fail with IMAP_SCOPE on every reconnect
		// once the mailbox grew beyond one page). UID holes are valid.
		for first := start; first <= tail.After; {
			last := first + 15
			if last < first || last > tail.After {
				last = tail.After
			}
			rows, err := c.fetchRange(first, last, true)
			if err != nil {
				return err
			}
			for _, m := range rows {
				m.Folder = folder
				m.Validity = s.UIDValidity
				out = append(out, m)
			}
			if last == tail.After {
				break
			}
			first = last + 1
		}
		return nil
	})
	if err != nil {
		return nil, Cursor{}, err
	}
	return out, tail, nil
}

// Page scans at most 16 UID numbers, not sequence numbers or an unbounded SEARCH.
func (c *Client) Page(ctx context.Context, folder string, cur Cursor) ([]Message, Cursor, error) {
	var out []Message
	original := cur
	next := cur
	e := c.operation(ctx, folder, func() error {
		s, e := c.selectFolder(folder)
		if e != nil {
			return e
		}
		if cur.Validity != s.UIDValidity {
			cur = Cursor{Validity: s.UIDValidity}
		}
		next = cur
		if cur.After >= uint32(s.UIDNext)-1 {
			return nil
		}
		// The live tail normally contains fewer than 16 UID numbers. Fetch it
		// directly: SEARCH adds a full RTT and cannot reveal any extra UID here.
		if uint32(s.UIDNext)-1-cur.After <= 16 {
			rows, err := c.fetchRange(cur.After+1, uint32(s.UIDNext)-1, true)
			if err != nil {
				return err
			}
			for _, m := range rows {
				m.Folder = folder
				m.Validity = s.UIDValidity
				out = append(out, m)
			}
			next.After = uint32(s.UIDNext) - 1
			return nil
		}
		// SEARCH skips deleted UID holes. Never expand an unbounded server set.
		if s.NumMessages > 4096 {
			return ErrLimit
		}
		c.conn.left.Store(128 << 10)
		found, err := c.c.UIDSearch(&imap.SearchCriteria{UID: []imap.UIDSet{{{Start: imap.UID(cur.After + 1), Stop: s.UIDNext - 1}}}}, nil).Wait()
		if err != nil {
			return err
		}
		set, ok := found.All.(imap.UIDSet)
		if !ok {
			return ErrScope
		}
		ids := make([]imap.UID, 0, 16)
		for _, part := range set {
			if part.Start == 0 || part.Stop < part.Start || part.Start <= imap.UID(cur.After) || part.Stop >= s.UIDNext || uint64(part.Stop)-uint64(part.Start)+1 > uint64(4096-len(ids)) {
				return ErrScope
			}
			for uid := part.Start; uid <= part.Stop; uid++ {
				ids = append(ids, uid)
			}
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		if len(ids) == 0 {
			next.After = uint32(s.UIDNext) - 1
			return nil
		}
		if len(ids) > 16 {
			ids = ids[:16]
		}
		for i := 1; i < len(ids); i++ {
			if ids[i] == ids[i-1] {
				return ErrScope
			}
		}
		rows, err := c.fetchSet(imap.UIDSetNum(ids...), len(ids), true)
		if err != nil {
			return err
		}
		end := uint32(ids[len(ids)-1])

		for _, m := range rows {
			m.Folder = folder
			m.Validity = s.UIDValidity
			out = append(out, m)
		}
		next.After = uint32(end)

		return nil
	})
	if e != nil {
		return nil, original, e
	}
	return out, next, nil
}

// PageTail is the dedicated-reader fast path. After an initial/periodic SELECT
// has established UIDVALIDITY, it polls the next bounded UID range directly.
// Allocation order is not visibility order on replicated providers. Passing an
// allocated but absent UID schedules bounded exact-UID revisits; absence is
// never deletion proof. The caller must periodically verify UIDVALIDITY.
func (c *Client) PageTailLimit(ctx context.Context, folder string, cur Cursor, verify bool, limit uint32) ([]Message, Cursor, error) {
	return c.pageTailLimit(ctx, folder, cur, verify, limit, nil)
}

// PageTailStream delivers individually complete, scope-checked BODY rows before
// later literals and the command's tagged completion. These provisional rows
// are for authenticated logical delivery only, never inventory/absence proof.
// The cursor and returned inventory remain uncommitted on any command failure.
// emit must not call another method on this Client (the operation is serialized).
func (c *Client) PageTailStream(ctx context.Context, folder string, cur Cursor, verify bool, limit uint32, emit func(Message) error) ([]Message, Cursor, error) {
	return c.pageTailLimit(ctx, folder, cur, verify, limit, emit)
}

func (c *Client) pageTailLimit(ctx context.Context, folder string, cur Cursor, verify bool, limit uint32, emit func(Message) error) ([]Message, Cursor, error) {
	if limit < 1 || limit > 16 {
		return nil, cur, ErrLimit
	}
	var out []Message
	original := cur
	next := cur
	e := c.operation(ctx, folder, func() error {
		var allocatedThrough uint32
		var rows []Message
		var set imap.UIDSet
		var count int
		var last uint32
		gaps := c.tailGapRequests(folder, cur.Validity, int(limit), time.Now())
		set, count, last = tailFetchSet(cur, limit, gaps)
		fetched := false
		discoverSparse := cur.Validity == 0 || c.selectedFolder != folder
		knownValidity, uidNext, _, known := c.SelectionAllocationProof(folder)
		if known && knownValidity == cur.Validity {
			allocatedThrough = uidNext - 1
		}
		emitted := false
		var onRow func(Message) error
		if emit != nil {
			onRow = func(m Message) error { m.Folder, m.Validity = folder, cur.Validity; emitted = true; return emit(m) }
		}
		if cur.Validity == 0 || verify || c.selectedFolder != folder {
			var selected *imap.SelectData
			var err error
			if cur.Validity != 0 && known && knownValidity == cur.Validity && c.selectedFolder == folder && count > 0 {
				rows, selected, err = c.selectTailFetch(folder, cur, set, count, onRow)
				fetched = err == nil && selected.UIDValidity == cur.Validity
			} else {
				selected, err = c.selectFolder(folder)
			}
			if err != nil {
				return err
			}
			if cur.Validity != selected.UIDValidity {
				// Speculative old-incarnation rows were parsed but never emitted.
				// Restart discovery using the tagged-successful new UIDVALIDITY.
				cur = Cursor{Validity: selected.UIDValidity}
				discoverSparse = true
				next = cur
				gaps = c.tailGapRequests(folder, cur.Validity, int(limit), time.Now())
				set, count, last = tailFetchSet(cur, limit, gaps)
			}
			allocatedThrough = uint32(selected.UIDNext) - 1
		}
		// Real mailboxes keep UIDNEXT after EXPUNGE. Iterating four UID numbers
		// per audit strands new DATA behind historical holes for minutes. Locate
		// a bounded number of present UIDs, retaining compressed visibility gaps.
		sparseForward := !fetched && discoverSparse && allocatedThrough > cur.After && allocatedThrough-cur.After > 16
		var sparseRequest *tailRange
		var sparseThrough uint32
		var rangeCandidates []imap.UID
		locateForward := func() error {
			ids, through, err := c.searchTailUIDs(cur.After+1, allocatedThrough, int(limit))
			if err != nil {
				return err
			}
			ids = tailSearchCandidates(ids, cur.After+1, through, int(limit))
			set, count, last = imap.UIDSetNum(ids...), len(ids), through
			for _, gap := range gaps {
				set.AddNum(imap.UID(gap.uid))
				count++
			}
			for _, id := range rangeCandidates {
				set.AddNum(id)
				count++
			}
			return nil
		}
		if sparseForward {
			if err := locateForward(); err != nil {
				return err
			}
		}
		if !fetched {
			if span, ok := c.sparseTailRequest(folder, cur.Validity, time.Now()); ok {
				ids, through, err := c.searchTailUIDs(span.first, span.last, int(limit))
				if err != nil {
					return err
				}
				ids = tailSearchCandidates(ids, span.first, through, int(limit))
				rangeCandidates = ids
				for _, id := range ids {
					set.AddNum(id)
					count++
				}
				sparseRequest, sparseThrough = &span, through
			}
		}
		if count == 0 {
			if sparseForward {
				next.After = last
				c.trackSparseTail(folder, cur.Validity, cur.After+1, next.After, nil, time.Now())
			}
			return nil
		}
		if !fetched {
			var err error
			rows, err = c.fetchSetStreaming(set, count, true, onRow)
			if err != nil {
				return err
			}
		}
		// Live dense pages need one FETCH turn. If concurrent cleanup leaves an
		// empty probe, switch to sparse discovery instead of enumerating holes.
		// Provisional rows are never emitted twice by the fallback operation.
		if !sparseForward && len(rows) == 0 && !emitted && allocatedThrough > cur.After && allocatedThrough-cur.After > 16 {
			if err := locateForward(); err != nil {
				return err
			}
			var err error
			rows, err = c.fetchSetStreaming(set, count, true, onRow)
			if err != nil {
				return err
			}
			sparseForward = true
		}
		var newest uint32
		for _, m := range rows {
			m.Folder = folder
			m.Validity = cur.Validity
			out = append(out, m)
			if m.UID > newest {
				newest = m.UID
			}
		}
		// SELECT proves old allocated holes; actual FETCH rows prove newer UIDs.
		// Neither permits advancing into the unallocated remainder of the range.
		if allocatedThrough > next.After {
			next.After = min(allocatedThrough, last)
		}
		if newest > next.After {
			next.After = newest
		}
		c.finishTailGapRequests(folder, cur.Validity, gaps, rows, time.Now())
		if sparseForward {
			c.trackSparseTail(folder, cur.Validity, cur.After+1, next.After, rows, time.Now())
		} else {
			c.trackTailGaps(folder, cur, next, rows, time.Now())
		}
		if sparseRequest != nil {
			c.finishSparseTail(folder, cur.Validity, sparseRequest, sparseThrough, rows, time.Now())
		}
		return nil
	})
	if e != nil {
		return nil, original, e
	}
	return out, next, nil
}
func (c *Client) PageTail(ctx context.Context, folder string, cur Cursor, verify bool) ([]Message, Cursor, error) {
	return c.PageTailLimit(ctx, folder, cur, verify, 16)
}

// Delete verifies the exact full message again in the same mailbox incarnation.
// Fallback EXPUNGE is allowed only in exclusive ITOM folders and after checking
// that no OTHER message has \\Deleted. A single process owns each cleanup folder.
func (c *Client) Delete(ctx context.Context, m Message) error {
	return c.operation(ctx, m.Folder, func() error {
		s, e := c.selectFolder(m.Folder)
		if e != nil {
			return e
		}
		if s.UIDValidity != m.Validity {
			return ErrChanged
		}
		got, exists, e := c.fetch(m.UID, true)
		if e != nil {
			return e
		}
		if !exists {
			return nil
		}
		if sha256.Sum256(got.MIME) != sha256.Sum256(m.MIME) {
			return ErrScope
		}
		plus := c.c.Caps().Has(imap.CapUIDPlus) || c.c.Caps().Has(imap.CapIMAP4rev2)
		if !plus {
			if s.NumMessages > 4096 {
				return ErrLimit
			}
			c.conn.left.Store(1 << 20)
			cmd := c.c.Fetch(imap.UIDSet{{Start: 1, Stop: 0}}, &imap.FetchOptions{UID: true, Flags: true})
			safe := true
			for row := cmd.Next(); row != nil; row = cmd.Next() {
				var uid imap.UID
				deleted := false
				for item := row.Next(); item != nil; item = row.Next() {
					switch x := item.(type) {
					case imapclient.FetchItemDataUID:
						uid = x.UID
					case imapclient.FetchItemDataFlags:
						for _, f := range x.Flags {
							if f == imap.FlagDeleted {
								deleted = true
							}
						}
					}
				}
				if deleted && uint32(uid) != m.UID {
					safe = false
				}
			}
			if e = cmd.Close(); e != nil {
				return e
			}
			if !safe {
				return ErrScope
			}
		}
		if e = c.c.Store(imap.UIDSetNum(imap.UID(m.UID)), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); e != nil {
			return e
		}
		if plus {
			e = c.c.UIDExpunge(imap.UIDSetNum(imap.UID(m.UID))).Close()
		} else {
			e = c.c.Expunge().Close()
		}
		if e != nil {
			return e
		}
		s, e = c.selectFolder(m.Folder)
		if e != nil {
			return e
		}
		if s.UIDValidity != m.Validity {
			return ErrChanged
		}
		_, exists, e = c.fetch(m.UID, false)
		if e != nil {
			return e
		}
		if exists {
			return ErrScope
		}
		return nil
	})
}

// Absent proves disappearance only for a previously observed UID in the SAME
// mailbox incarnation. It is never evidence about an unknown APPEND locator.
func (c *Client) Absent(ctx context.Context, folder string, validity, uid uint32) (bool, error) {
	absent := false
	err := c.operation(ctx, folder, func() error {
		s, e := c.selectFolder(folder)
		if e != nil {
			return e
		}
		if validity == 0 || uid == 0 || s.UIDValidity != validity {
			return ErrChanged
		}
		if uid >= uint32(s.UIDNext) {
			return ErrScope
		}
		_, exists, e := c.fetch(uid, false)
		if e != nil {
			return e
		}
		absent = !exists
		return nil
	})
	return absent, err
}

// Present verifies a bounded set of previously observed locators in one UID
// FETCH. No payload is returned. A concurrent unsolicited UID+FLAGS update is
// conservative presence, and changed UIDVALIDITY is never treated as absence.
func (c *Client) Present(ctx context.Context, folder string, validity uint32, ids []uint32) (map[uint32]bool, error) {
	present := map[uint32]bool{}
	if len(ids) == 0 {
		return present, nil
	}
	if len(ids) > 128 {
		return nil, ErrLimit
	}
	err := c.operation(ctx, folder, func() error {
		selected, e := c.selectFolder(folder)
		if e != nil {
			return e
		}
		if selected.UIDValidity != validity {
			return ErrChanged
		}
		uids := make([]imap.UID, len(ids))
		for i, id := range ids {
			if id == 0 {
				return scopeFailure("IMAP_SCOPE_PRESENT_ZERO_UID")
			}
			uids[i] = imap.UID(id)
		}
		found, e := c.fetchPresenceSet(imap.UIDSetNum(uids...), len(uids))
		if e != nil {
			return e
		}
		for uid := range found {
			present[uid] = true
		}
		return nil
	})
	return present, err
}

// ReadCommandCounts are bounded scalar diagnostics; they contain no body data.
func (c *Client) ReadCommandCounts() (uint64, uint64, uint64) {
	return c.readSelects.Load(), c.readFetches.Load(), c.partialRetries.Load()
}

func tailFetchSet(cur Cursor, limit uint32, gaps []tailGap) (imap.UIDSet, int, uint32) {
	last := cur.After + limit
	if last < cur.After {
		last = ^uint32(0)
	}
	var set imap.UIDSet
	count := int(last - cur.After)
	if cur.After != ^uint32(0) {
		set = imap.UIDSet{{Start: imap.UID(cur.After + 1), Stop: imap.UID(last)}}
	}
	for _, gap := range gaps {
		set.AddNum(imap.UID(gap.uid))
	}
	return set, count + len(gaps), last
}

// SELECT renewal and a bounded UID-only FETCH may share propagation. The
// speculative read is authorized only by a successful SELECT with unchanged
// UIDVALIDITY. No callback receives rows from a failed or changed selection.
// Both commands are joined under operation's cancellation and read bounds.
func (c *Client) selectTailFetch(folder string, cur Cursor, set imap.UIDSet, count int, emit func(Message) error) ([]Message, *imap.SelectData, error) {
	started := time.Now()
	c.readSelects.Add(1)
	selectCmd := c.c.Select(folder, nil)
	ready := make(chan struct{})
	approved := false
	type result struct {
		rows []Message
		err  error
	}
	fetched := make(chan result, 1)
	var onRow func(Message) error
	if emit != nil {
		onRow = func(m Message) error {
			<-ready
			if !approved {
				return nil
			}
			m.Folder, m.Validity = folder, cur.Validity
			return emit(m)
		}
	}
	go func() { rows, err := c.fetchSetStreaming(set, count, true, onRow); fetched <- result{rows, err} }()
	selected, err := selectCmd.Wait()
	if isProviderMailboxStateChange(err) {
		err = connectionFailed(ErrMailboxStateChanged.Error(), "select", err)
	}
	if err == nil {
		err = c.recordSelection(folder, selected, started)
	}
	approved = err == nil && selected.UIDValidity == cur.Validity
	if err != nil {
		c.conn.Close()
	}
	close(ready)
	fetchedResult := <-fetched
	if err != nil {
		return nil, nil, err
	}
	if fetchedResult.err != nil {
		return nil, nil, fetchedResult.err
	}
	if !approved {
		return nil, selected, nil
	}
	return fetchedResult.rows, selected, nil
}

// Package probe runs a REAL encrypted IMAP round trip. It is a connectivity
// qualification service, not a SOCKS exit or a production VPN datapath.
package probe

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/secure"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var dialIMAP = mailbox.Dial

// Keep idle accounts quiet. Faster scans are limited to an authenticated
// handshake's short request window; this is not a production rate governor.
const activePollInterval = 100 * time.Millisecond
const idlePollInterval = time.Second
const activePollWindow = 5 * time.Second

type envelope struct {
	Version int
	Binding secure.Binding
	Stage   int
	Payload []byte
}
type endpoint struct {
	c          *mailbox.Client
	dispatcher *mailbox.Dispatcher
	routes     map[string]*mailbox.Route
	journals   map[string]*journal.Store
	account    string
	users      []servercfg.User
	server     wire.ID
	serverSide bool
	expiry     map[journal.AttemptRef]time.Time
}

func binding(server wire.ID, u servercfg.User, account string) secure.Binding {
	hash := sha256.Sum256([]byte("ITOM/probe/1/" + u.ID + "/" + account + "/" + u.Up + "/" + u.Down))
	var pair wire.ID
	copy(pair[:], hash[:16])
	return secure.Binding{Server: server, User: u.ID, Pair: pair, Epoch: 1, ConfigHash: hash}
}
func parse(raw []byte) (envelope, error) {
	var e envelope
	if len(raw) > 16384 {
		return e, mailbox.ErrLimit
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&e) != nil || d.Decode(new(any)) != io.EOF || e.Version != 1 || e.Stage < 0 || e.Stage > 3 || len(e.Payload) > wire.MaxHandshake+256 {
		return e, errors.New("PROBE_FORMAT")
	}
	return e, nil
}
func checkBinding(b secure.Binding, server wire.ID, u servercfg.User, account string) bool {
	expected := binding(server, u, account)
	return b.Server == expected.Server && b.User == expected.User && b.Pair == expected.Pair && b.ConfigHash == expected.ConfigHash && b.Epoch == 1 && b.Session != (wire.ID{}) && b.Handshake != (wire.ID{})
}
func open(ctx context.Context, server wire.ID, a servercfg.Account, users []servercfg.User, dir string, serverSide bool) (*endpoint, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	folders := []string{}
	for _, u := range users {
		folders = append(folders, u.Up, u.Down)
	}
	if len(users) == 0 {
		return nil, errors.New("NO_USERS")
	}
	c, err := dialIMAP(ctx, a.Mail, folders)
	if err != nil {
		return nil, err
	}
	ep := &endpoint{c: c, journals: map[string]*journal.Store{}, users: users, server: server, account: a.ID, serverSide: serverSide, expiry: map[journal.AttemptRef]time.Time{}}
	routes := make([]mailbox.RouteConfig, 0, len(users))
	for _, u := range users {
		routes = append(routes, mailbox.RouteConfig{User: u.ID, Folders: []string{u.Up, u.Down}})
	}
	dispatcher, err := mailbox.NewDispatcher(ctx, c, mailbox.DispatchConfig{Routes: routes, QueuePerUser: 4})
	if err != nil {
		c.Close()
		return nil, err
	}
	ep.dispatcher = dispatcher
	ep.routes = map[string]*mailbox.Route{}
	for _, u := range users {
		ep.routes[u.ID], _ = dispatcher.Route(u.ID)
	}

	for _, u := range users {
		jc := journal.Config{MaxGrants: 32, MaxAttempts: 32, Accounts: []journal.Account{{ID: a.ID, Provider: a.Mail.Host, Limit: pool.Amount{Bytes: 128 << 10, Messages: 16}, Users: []journal.User{{ID: u.ID, Limit: pool.Amount{Bytes: 128 << 10, Messages: 16}, Folders: []string{u.Up, u.Down}}}}}}
		j, e := journal.Open(filepath.Join(dir, u.ID+".db"), jc, time.Second)
		if e != nil {
			ep.close()
			return nil, e
		}
		ep.journals[u.ID] = j
	}
	for _, u := range users {
		for _, f := range []string{u.Up, u.Down} {
			if err = ep.routes[u.ID].Ensure(ctx, f); err != nil {
				ep.close()
				return nil, err
			}
		}
	}
	if err = ep.cleanup(ctx, true); err != nil {
		ep.close()
		return nil, err
	}
	for _, u := range users {
		j := ep.journals[u.ID]
		if err = j.EnableAfterInventory(a.ID, u.ID, j.Run()); err != nil {
			ep.close()
			return nil, err
		}
	}
	return ep, nil
}
func (e *endpoint) close() {
	if e.dispatcher != nil {
		e.dispatcher.Close()
	}
	e.c.Close()
	for _, j := range e.journals {
		j.Close()
	}
}
func (e *endpoint) send(ctx context.Context, u servercfg.User, msg envelope) error {
	j := e.journals[u.ID]
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	raw, err := mailbox.Encode(body)
	if err != nil {
		return err
	}
	folder := u.Up
	if e.serverSide {
		folder = u.Down
	}
	g, err := j.Issue(journal.Scope{Account: e.account, User: u.ID, Session: msg.Binding.Session}, pool.Amount{Bytes: uint64(len(raw)), Messages: 1})
	if err != nil {
		return err
	}
	var batch wire.ID
	if _, err = rand.Read(batch[:]); err != nil {
		return err
	}
	a, err := j.Prepare(g.Ref, folder, batch, uint64(len(raw)), body)
	if err != nil {
		return err
	}
	if _, err = j.Start(a.Ref); err != nil {
		return err
	}
	e.expiry[a.Ref] = time.Now().Add(90 * time.Second)
	appendErr := e.routes[u.ID].Append(ctx, folder, raw)
	result := journal.Accepted
	if appendErr != nil {
		result = journal.Uncertain
	}
	if err = j.Complete(a.Ref, result); err != nil {
		return err
	}
	if err = j.CloseGrant(g.Ref); err != nil {
		return err
	}
	return appendErr
}

// Reclaim only our exact journaled message bytes. Missing unknown APPENDs stay
// charged; restart and an empty scan are never used as proof of no-write.
func (e *endpoint) cleanup(ctx context.Context, all bool) error {
	for user, j := range e.journals {
		if err := e.cleanupJournal(ctx, user, j, all); err != nil {
			return err
		}
	}
	return nil
}
func (e *endpoint) cleanupJournal(ctx context.Context, user string, j *journal.Store, all bool) error {
	rows, err := j.ScanAttempts(0, 256)
	if err != nil {
		return err
	}
	byFolder := map[string]map[[32]byte]journal.Attempt{}
	for _, a := range rows {
		if a.State == journal.Prepared {
			if err = j.CancelPrepared(a.Ref); err != nil {
				return err
			}
			continue
		}
		if !all && time.Now().Before(e.expiry[a.Ref]) {
			continue
		}
		if byFolder[a.Folder] == nil {
			byFolder[a.Folder] = map[[32]byte]journal.Attempt{}
		}
		byFolder[a.Folder][a.Digest] = a
	}
	for folder, known := range byFolder {
		cur := mailbox.Cursor{}
		for page := 0; page < 256; page++ {
			msgs, next, err := e.routes[user].Page(ctx, folder, cur)
			if err != nil {
				return err
			}
			for _, m := range msgs {
				body, err := mailbox.Decode(m.MIME)
				if err != nil {
					continue
				}
				hash := sha256.Sum256(body)
				a, ok := known[hash]
				if !ok {
					continue
				}
				if err = j.Observe(a.Ref, j.Run(), journal.Observation{Account: e.account, Folder: folder, Location: journal.Location{UIDValidity: m.Validity, UID: m.UID}, Decoded: body}); err != nil {
					return err
				}
				if err = e.routes[user].Delete(ctx, m); err != nil {
					return err
				}
				if err = j.Deleted(a.Ref, j.Run(), journal.Location{UIDValidity: m.Validity, UID: m.UID}); err != nil {
					return err
				}
				delete(known, hash)
				delete(e.expiry, a.Ref)
			}
			if next == cur {
				break
			}
			cur = next
			if page == 255 {
				return errors.New("PROBE_INVENTORY_LIMIT")
			}
		}
	}
	// Crash after successful EXPUNGE but before journal completion: a stored
	// observed locator permits a fresh same-UIDVALIDITY absence check.
	for folder, known := range byFolder {
		for _, a := range known {
			if a.State != journal.Committed || a.Location.UID == 0 {
				continue
			}
			absent, err := e.routes[user].Absent(ctx, folder, a.Location.UIDValidity, a.Location.UID)
			if err != nil {
				return err
			}
			if absent {
				if err = j.Deleted(a.Ref, j.Run(), a.Location); err != nil {
					return err
				}
				delete(e.expiry, a.Ref)
			}
		}
	}
	// Includes lost Issue/Prepare results, without ever freeing physical attempts.
	gs, err := j.ScanGrants(0, 256)
	if err != nil {
		return err
	}
	for _, g := range gs {
		if !g.Closed {
			if err = j.CloseGrant(g.Ref); err != nil {
				return err
			}
		}
	}
	return nil
}
func wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func RunClient(ctx context.Context, p servercfg.Profile, account, dir string) (time.Duration, error) {
	var a servercfg.Account
	for _, x := range p.Accounts {
		if x.ID == account || strings.EqualFold(x.Mail.Username, account) {
			a = x
		}
	}
	if a.ID == "" {
		return 0, errors.New("ACCOUNT_NOT_FOUND")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	ep, err := open(ctx, p.Server, a, []servercfg.User{p.User}, dir, false)
	if err != nil {
		return 0, err
	}
	defer ep.close()
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		_ = ep.cleanup(cleanupCtx, true)
	}()
	b := binding(p.Server, p.User, a.ID)
	if _, err = rand.Read(b.Session[:]); err != nil {
		return 0, err
	}
	if _, err = rand.Read(b.Handshake[:]); err != nil {
		return 0, err
	}
	key, _ := hex.DecodeString(p.User.Key)
	hs, err := secure.NewHandshake(b, key, true)
	clear(key)
	if err != nil {
		return 0, err
	}
	hello, err := hs.Write(nil)
	if err != nil {
		return 0, err
	}
	if err = ep.send(ctx, p.User, envelope{Version: 1, Binding: b, Stage: 0, Payload: hello}); err != nil {
		return 0, err
	}
	var cipher *secure.Epoch
	var tokenBytes [8]byte
	if _, err = rand.Read(tokenBytes[:]); err != nil {
		return 0, err
	}
	token := binary.BigEndian.Uint64(tokenBytes[:])
	if token == 0 {
		token = 1
	}
	cur := mailbox.Cursor{}
	var start time.Time
	hotUntil := time.Now().Add(activePollWindow)
	for {
		sentPing := false
		msgs, next, err := ep.routes[p.User.ID].Page(ctx, p.User.Down, cur)
		if err != nil {
			return 0, err
		}
		cur = next
		for _, m := range msgs {
			raw, err := mailbox.Decode(m.MIME)
			if err != nil {
				continue
			}
			msg, err := parse(raw)
			if err != nil || msg.Binding != b {
				continue
			}
			if msg.Stage == 1 && cipher == nil {
				if err = hs.Read(msg.Payload); err != nil {
					continue
				}
				cipher, err = hs.Epoch()
				if err != nil {
					return 0, err
				}
				frame, _ := wire.EncodeControl(0, wire.Echo{Token: token})
				packet, err := cipher.Seal(frame)
				if err != nil {
					return 0, err
				}
				start = time.Now()
				if err = ep.send(ctx, p.User, envelope{Version: 1, Binding: b, Stage: 2, Payload: packet}); err != nil {
					return 0, err
				}
				sentPing = true
				hotUntil = time.Now().Add(activePollWindow)
			} else if msg.Stage == 3 && cipher != nil {
				f, err := cipher.Open(msg.Payload)
				if err != nil {
					continue
				}
				ctrl, err := wire.DecodeControl(f)
				if err != nil {
					continue
				}
				echo, ok := ctrl.(wire.Echo)
				if ok && echo.Reply && echo.Token == token {
					return time.Since(start), nil
				}
			}
		}
		if !sentPing {
			delay := idlePollInterval
			if time.Now().Before(hotUntil) {
				delay = activePollInterval
			}
			if err = wait(ctx, delay); err != nil {
				return 0, err
			}
		}
	}
}

type live struct {
	binding  secure.Binding
	hs       *secure.Handshake
	cipher   *secure.Epoch
	expires  time.Time
	answered bool
	hotUntil time.Time
}

func serveAccount(ctx context.Context, cfg servercfg.Config, a servercfg.Account, dir string, log func(string)) error {
	users := cfg.Users
	ep, err := open(ctx, cfg.Server, a, users, dir, true)
	if err != nil {
		return err
	}
	defer ep.close()
	cursors := map[string]mailbox.Cursor{}
	sessions := map[wire.ID]*live{}
	nextGC := time.Now().Add(10 * time.Second)
	for {
		for _, u := range users {
			if !u.Enabled {
				continue
			}
			msgs, next, err := ep.routes[u.ID].Page(ctx, u.Up, cursors[u.ID])
			if err != nil {
				return err
			}
			cursors[u.ID] = next
			for _, m := range msgs {
				body, err := mailbox.Decode(m.MIME)
				if err != nil {
					continue
				}
				msg, err := parse(body)
				if err != nil || !checkBinding(msg.Binding, cfg.Server, u, a.ID) {
					continue
				}
				s := sessions[msg.Binding.Session]
				if msg.Stage == 0 && s == nil {
					if len(sessions) >= 32 {
						continue
					}
					key, _ := hex.DecodeString(u.Key)
					hs, err := secure.NewHandshake(msg.Binding, key, false)
					clear(key)
					if err != nil || hs.Read(msg.Payload) != nil {
						continue
					}
					reply, err := hs.Write(nil)
					if err != nil {
						continue
					}
					cipher, err := hs.Epoch()
					if err != nil {
						continue
					}
					sessions[msg.Binding.Session] = &live{binding: msg.Binding, hs: hs, cipher: cipher, expires: time.Now().Add(60 * time.Second)}
					if err = ep.send(ctx, u, envelope{Version: 1, Binding: msg.Binding, Stage: 1, Payload: reply}); err != nil {
						return err
					}
					sessions[msg.Binding.Session].hotUntil = time.Now().Add(activePollWindow)
				} else if msg.Stage == 2 && s != nil && s.binding == msg.Binding && !s.answered {
					f, err := s.cipher.Open(msg.Payload)
					if err != nil {
						continue
					}
					ctrl, err := wire.DecodeControl(f)
					if err != nil {
						continue
					}
					echo, ok := ctrl.(wire.Echo)
					if !ok || echo.Reply {
						continue
					}
					echo.Reply = true
					frame, _ := wire.EncodeControl(0, echo)
					packet, err := s.cipher.Seal(frame)
					if err != nil {
						return err
					}
					s.answered = true
					if err = ep.send(ctx, u, envelope{Version: 1, Binding: msg.Binding, Stage: 3, Payload: packet}); err != nil {
						return err
					}
					log(fmt.Sprintf("account=%s user=%s encrypted IMAP roundtrip answered", a.ID, u.ID))
				}
			}
		}
		for k, s := range sessions {
			if time.Now().After(s.expires) {
				delete(sessions, k)
			}
		}
		if time.Now().After(nextGC) {
			if err = ep.cleanup(ctx, false); err != nil {
				return err
			}
			nextGC = time.Now().Add(10 * time.Second)
		}
		delay := idlePollInterval
		for _, s := range sessions {
			if !s.answered && time.Now().Before(s.hotUntil) {
				delay = activePollInterval
				break
			}
		}
		if err = wait(ctx, delay); err != nil {
			return err
		}
	}
}

// Serve allocates exactly one serialized IMAP connection per configured account.
// Each failed account reconnects independently with bounded backoff.
func Serve(ctx context.Context, cfg servercfg.Config, dir string, log func(string)) error {
	if cfg.Validate() != nil || len(cfg.Accounts) == 0 {
		return errors.New("CONFIG_NOT_READY")
	}
	enabled := 0
	for _, u := range cfg.Users {
		if u.Enabled {
			enabled++
		}
	}
	if enabled == 0 {
		return errors.New("NO_ENABLED_USERS")
	}
	child, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan struct{}, len(cfg.Accounts))
	for _, a := range cfg.Accounts {
		go func(a servercfg.Account) {
			defer func() { done <- struct{}{} }()
			delay := time.Second
			for child.Err() == nil {
				err := serveAccount(child, cfg, a, filepath.Join(dir, a.ID), log)
				if child.Err() != nil {
					return
				}
				log(fmt.Sprintf("account=%s reconnect: %v", a.ID, err))
				if wait(child, delay) != nil {
					return
				}
				if delay < 30*time.Second {
					delay *= 2
				}
				if delay > 30*time.Second {
					delay = 30 * time.Second
				}
			}
		}(a)
	}
	for range cfg.Accounts {
		<-done
	}
	return ctx.Err()
}

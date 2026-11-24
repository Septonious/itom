package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"time"

	"itom.local/core/adminapi"
	"itom.local/core/wire"
)

type adminView struct {
	Authorized bool              `json:"authorized"`
	Status     *adminapi.Status  `json:"status,omitempty"`
	Pending    *adminapi.Pending `json:"pending,omitempty"`
	Error      string            `json:"error,omitempty"`
}
type adminClientState struct {
	mu   sync.Mutex
	view adminView
	key  string
	uid  string
	wake chan struct{}
}

func (c *Client) AdminSnapshot() string {
	c.admin.mu.Lock()
	defer c.admin.mu.Unlock()
	raw, _ := json.Marshal(c.admin.view)
	return string(raw)
}

// AdminCall is a typed, bounded RPC over IMAP, never a local shell or a direct
// server-network API. It is called from Android's IO dispatcher.
func (c *Client) AdminCall(raw string) (string, error) {
	q, e := adminapi.DecodeRequest([]byte(raw))
	if e != nil {
		return "", e
	}
	r, e := c.adminRequest(q)
	if e != nil {
		return "", e
	}
	if r.Pending != nil && (r.Pending.State == "armed" || r.Pending.State == "observing") {
		select {
		case c.admin.wake <- struct{}{}:
		default:
		}
	}
	out, e := json.Marshal(r)
	return string(out), e
}
func (c *Client) adminRequest(q adminapi.Request) (adminapi.Response, error) {
	var r adminapi.Response
	raw, e := json.Marshal(q)
	if e != nil {
		return r, e
	}
	ctx, cancel := context.WithTimeout(c.ctx, 20*time.Second)
	defer cancel()
	stream, _, e := c.pool.Open(ctx, wire.Connect{Host: adminapi.Host, Port: adminapi.Port})
	if e != nil {
		return r, errors.New("ADMIN_CONNECTION_FAILED")
	}
	conn, ok := stream.(net.Conn)
	if !ok {
		stream.Close()
		return r, errors.New("ADMIN_CONNECTION_FAILED")
	}
	r, e = adminapi.Call(ctx, conn, raw, c.admin.key)
	if e != nil {
		return r, e
	}
	c.admin.mu.Lock()
	if r.Status != nil {
		c.admin.view.Status = r.Status
		c.admin.view.Authorized = r.Status.Admin
	}
	if r.Error == "ADMIN_ACCESS_DENIED" {
		c.admin.view.Authorized = false
		c.admin.view.Status = nil
	}
	if r.Pending != nil && (c.admin.view.Pending == nil || r.Pending.ServerTimeMS >= c.admin.view.Pending.ServerTimeMS) {
		c.admin.view.Pending = r.Pending
	}
	c.admin.view.Error = r.Error
	c.admin.mu.Unlock()
	return r, nil
}
func (c *Client) startAdmin(key, uid string) {
	c.admin.key = key
	c.admin.uid = uid
	c.admin.wake = make(chan struct{}, 1)
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		delay := time.Second
		var own *adminapi.Pending
		for {
			timer := time.NewTimer(delay)
			select {
			case <-c.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			case <-c.admin.wake:
				timer.Stop()
			}
			if c.pool.Stats().ActiveLines == 0 {
				delay = time.Second
				continue
			}
			c.admin.mu.Lock()
			v := c.admin.view
			c.admin.mu.Unlock()
			if p := v.Pending; p != nil && p.Owner == uid && (own == nil || p.ID != own.ID && p.ServerTimeMS >= own.ServerTimeMS) {
				copy := *p
				own = &copy
			}
			q := adminapi.Request{Operation: "status"}
			if own != nil && own.State == "observing" {
				q = adminapi.Request{Operation: "heartbeat", ID: own.ID, Challenge: own.Challenge}
			}
			r, e := c.adminRequest(q)
			delay = 30 * time.Second
			if e != nil {
				delay = 5 * time.Second
				if !v.Authorized && v.Pending == nil {
					delay = time.Minute
				}
				continue
			}
			if r.Error == "ADMIN_HEARTBEAT_REJECTED" {
				r, e = c.adminRequest(adminapi.Request{Operation: "status"})
				delay = 5 * time.Second
				if e != nil {
					continue
				}
			}
			if p := r.Pending; p != nil && p.Owner == uid {
				copy := *p
				own = &copy
			} else {
				own = nil
			}
			c.admin.mu.Lock()
			v = c.admin.view
			c.admin.mu.Unlock()
			if !v.Authorized {
				delay = time.Minute
			} else if p := v.Pending; p != nil && (p.State == "armed" || p.State == "observing" || p.State == "rolling_back") {
				delay = 5 * time.Second
			}
		}
	}()
}

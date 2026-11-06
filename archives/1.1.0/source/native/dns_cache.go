package mobile

import (
	"container/list"
	"context"
	"errors"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const dnsCacheSize = 1024

type dnsQueryKey struct {
	name       string
	typ        dnsmessage.Type
	rd, cd, ad bool
}

type dnsQueryPending struct {
	done          chan struct{}
	reply         []byte
	err           error
	canceled      bool
	stored, until time.Time
}

type dnsReplyEntry struct {
	key           dnsQueryKey
	raw           []byte
	stored, until time.Time
}

// Session-local, bounded LRU, owned by dnsResolver.mu. Persistent fake-IP
// mappings are separate: they never authorize reuse of expired DNS answers.
type dnsReplyCache struct {
	entries map[dnsQueryKey]*list.Element
	lru     list.List
}

func cacheQuestion(raw []byte) (dnsmessage.Message, dnsQueryKey, bool) {
	var q dnsmessage.Message
	if len(raw) > maxDNSBytes || q.Unpack(raw) != nil || q.Response || q.OpCode != 0 || len(q.Questions) != 1 || len(q.Answers) != 0 || len(q.Authorities) != 0 || len(q.Additionals) != 0 {
		return q, dnsQueryKey{}, false
	}
	a := q.Questions[0]
	// EDNS/DNSSEC and non-IN questions retain the original complete exchange;
	// their flags, options and advertised datagram size are not conflated.
	return q, dnsQueryKey{strings.ToLower(a.Name.String()), a.Type, q.RecursionDesired, q.CheckingDisabled, q.AuthenticData}, a.Class == dnsmessage.ClassINET
}

func replyForQuestion(raw []byte, q dnsmessage.Message, age time.Duration) ([]byte, error) {
	var answer dnsmessage.Message
	if err := answer.Unpack(raw); err != nil {
		return nil, err
	}
	answer.ID = q.ID
	answer.Questions = q.Questions
	if answer.RCode == dnsmessage.RCodeNameError || len(answer.Answers) == 0 {
		for i := range answer.Authorities {
			a := &answer.Authorities[i]
			if body, ok := a.Body.(*dnsmessage.SOAResource); ok {
				a.Header.TTL = min(a.Header.TTL, body.MinTTL)
			}
		}
	}
	// Round remaining TTL down, never extend a record by a fractional second.
	seconds := uint32(max(0, (age+time.Second-1)/time.Second))
	for _, section := range [][]dnsmessage.Resource{answer.Answers, answer.Authorities, answer.Additionals} {
		for i := range section {
			if section[i].Header.Type != dnsmessage.TypeOPT {
				section[i].Header.TTL -= min(section[i].Header.TTL, seconds)
			}
		}
	}
	packed, err := answer.Pack()
	if err == nil && len(packed) > maxDNSBytes {
		return nil, errors.New("DNS_SIZE")
	}
	return packed, err
}

func (c *dnsReplyCache) get(key dnsQueryKey, q dnsmessage.Message, now time.Time) ([]byte, bool) {
	e := c.entries[key]
	if e == nil {
		return nil, false
	}
	entry := e.Value.(dnsReplyEntry)
	if !now.Before(entry.until) {
		delete(c.entries, key)
		c.lru.Remove(e)
		return nil, false
	}
	raw, err := replyForQuestion(entry.raw, q, now.Sub(entry.stored))
	if err != nil {
		return nil, false
	}
	c.lru.MoveToFront(e)
	return raw, true
}

func (c *dnsReplyCache) put(key dnsQueryKey, raw []byte, now time.Time) {
	var answer dnsmessage.Message
	if answer.Unpack(raw) != nil || answer.Truncated || (answer.RCode != dnsmessage.RCodeSuccess && answer.RCode != dnsmessage.RCodeNameError) {
		return
	}
	ttl := uint32(3600)
	negative := answer.RCode == dnsmessage.RCodeNameError || len(answer.Answers) == 0
	if negative {
		soa := false
		for i := range answer.Authorities {
			a := &answer.Authorities[i]
			if body, ok := a.Body.(*dnsmessage.SOAResource); ok && a.Header.Class == dnsmessage.ClassINET {
				a.Header.TTL = min(a.Header.TTL, body.MinTTL)
				ttl = min(ttl, a.Header.TTL)
				soa = true
			}
		}
		// RFC 2308: no invented negative TTL, and no caching of transient
		// timeout/SERVFAIL/REFUSED responses or unauthoritative empty replies.
		if !soa {
			return
		}
	}
	for _, section := range [][]dnsmessage.Resource{answer.Answers, answer.Authorities, answer.Additionals} {
		for _, a := range section {
			if a.Header.Type == dnsmessage.TypeOPT {
				return
			}
			ttl = min(ttl, a.Header.TTL)
		}
	}
	if ttl == 0 {
		return
	}
	packed, err := answer.Pack()
	if err != nil || len(packed) > maxDNSBytes {
		return
	}
	if c.entries == nil {
		c.entries = make(map[dnsQueryKey]*list.Element)
	}
	entry := dnsReplyEntry{key, packed, now, now.Add(time.Duration(ttl) * time.Second)}
	if e := c.entries[key]; e != nil {
		e.Value = entry
		c.lru.MoveToFront(e)
		return
	}
	if len(c.entries) >= dnsCacheSize {
		e := c.lru.Back()
		delete(c.entries, e.Value.(dnsReplyEntry).key)
		c.lru.Remove(e)
	}
	c.entries[key] = c.lru.PushFront(entry)
}

// Share one upstream race for simultaneous equivalent questions. A canceled
// owner releases its sockets; other live callers retry together rather than
// inheriting that caller's cancellation. No extra background worker is created.
func (r *dnsResolver) resolve(ctx context.Context, raw []byte) ([]byte, error) {
	q, key, cacheable := cacheQuestion(raw)
	if !cacheable {
		return r.exchange(ctx, raw)
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r.mu.Lock()
		if reply, ok := r.answers.get(key, q, time.Now()); ok {
			r.cacheHits.Add(1)
			r.mu.Unlock()
			return reply, nil
		}
		if pending := r.queries[key]; pending != nil {
			r.sharedQueries.Add(1)
			r.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-pending.done:
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if pending.canceled {
					continue
				}
				if pending.err != nil {
					return nil, pending.err
				}
				if !pending.until.IsZero() && !time.Now().Before(pending.until) {
					continue
				}
				return replyForQuestion(pending.reply, q, time.Since(pending.stored))
			}
		}
		pending := &dnsQueryPending{done: make(chan struct{})}
		if r.queries == nil {
			r.queries = make(map[dnsQueryKey]*dnsQueryPending)
		}
		r.queries[key] = pending
		r.cacheMisses.Add(1)
		r.mu.Unlock()
		reply, err := r.exchange(ctx, raw)
		r.mu.Lock()
		pending.reply, pending.err, pending.canceled = reply, err, ctx.Err() != nil
		pending.stored = time.Now()
		if err == nil && !pending.canceled {
			r.answers.put(key, reply, pending.stored)
			if e := r.answers.entries[key]; e != nil {
				pending.until = e.Value.(dnsReplyEntry).until
			}
		}
		delete(r.queries, key)
		close(pending.done)
		r.mu.Unlock()
		if pending.canceled {
			return nil, ctx.Err()
		}
		return reply, err
	}
}

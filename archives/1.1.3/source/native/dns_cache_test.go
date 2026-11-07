package mobile

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func waitDNSCount(t *testing.T, count *atomic.Uint64, target uint64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for count.Load() < target {
		if time.Now().After(deadline) {
			t.Fatal("DNS callers did not share the pending question", count.Load())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestDNSCacheSharesMissesAndPreservesClientBindings(t *testing.T) {
	release := make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	address := udpResolver(t, func(raw []byte) []byte { <-release; return dnsReply(t, raw) })
	var calls atomic.Int32
	r, err := newDNSResolver(defaultDNSSettings(), func(ctx context.Context, network, _ string) (net.Conn, error) {
		calls.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}, forbiddenTunnel(t))
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 32)
	for i := range 32 {
		go func() {
			name := "Shared.test"
			if i%2 == 0 {
				name = "shared.TEST"
			}
			raw, _ := dnsQuestion(name)
			var q dnsmessage.Message
			q.Unpack(raw)
			q.ID = uint16(i + 1)
			raw, _ = q.Pack()
			reply, err := r.resolve(context.Background(), raw)
			if err == nil && !validDNSReply(raw, reply) {
				err = errors.New("shared reply lost caller ID/question")
			}
			results <- err
		}()
	}
	waitDNSCount(t, &r.sharedQueries, 31)
	unblock.Do(func() { close(release) })
	for range 32 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 || r.cacheMisses.Load() != 1 {
		t.Fatal("duplicate questions repeated the upstream resolver race", calls.Load(), r.cacheMisses.Load())
	}
	raw, _ := dnsQuestion("shared.test")
	if reply, err := r.resolve(context.Background(), raw); err != nil || !validDNSReply(raw, reply) || calls.Load() != 2 || r.cacheHits.Load() != 1 {
		t.Fatal("completed question missed TTL cache", err)
	}
	if ips, err := r.lookup(context.Background(), "shared.test"); err != nil || len(ips) != 1 || calls.Load() != 2 {
		t.Fatal("application dial repeated cached A lookup", err)
	}
}

func TestDNSReplyCacheAgesPositiveAndSOANegativeAnswers(t *testing.T) {
	now := time.Unix(1700000000, 0)
	raw, _ := dnsQuestion("alias.test")
	q, key, _ := cacheQuestion(raw)
	q.ID = 99
	positive := dnsmessage.Message{Header: dnsmessage.Header{Response: true}, Questions: q.Questions, Answers: []dnsmessage.Resource{
		{Header: dnsmessage.ResourceHeader{Name: q.Questions[0].Name, Type: dnsmessage.TypeCNAME, Class: dnsmessage.ClassINET, TTL: 8}, Body: &dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName("target.test.")}},
		{Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName("target.test."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 30}, Body: &dnsmessage.AResource{A: [4]byte{203, 0, 113, 9}}},
	}}
	packed, _ := positive.Pack()
	var cache dnsReplyCache
	cache.put(key, packed, now)
	answer, ok := cache.get(key, q, now.Add(3*time.Second))
	var got dnsmessage.Message
	if !ok || got.Unpack(answer) != nil || got.ID != 99 || got.Answers[0].Header.TTL != 5 || got.Answers[1].Header.TTL != 27 {
		t.Fatal("CNAME/A cache did not retain minimum expiry and remaining TTL")
	}
	if _, ok := cache.get(key, q, now.Add(8*time.Second)); ok {
		t.Fatal("expired CNAME reused its target's longer TTL")
	}
	for _, rcode := range []dnsmessage.RCode{dnsmessage.RCodeNameError, dnsmessage.RCodeSuccess} {
		negative := dnsmessage.Message{Header: dnsmessage.Header{Response: true, RCode: rcode}, Questions: q.Questions, Authorities: []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName("test."), Type: dnsmessage.TypeSOA, Class: dnsmessage.ClassINET, TTL: 60}, Body: &dnsmessage.SOAResource{NS: dnsmessage.MustNewName("ns.test."), MBox: dnsmessage.MustNewName("hostmaster.test."), MinTTL: 5}}}}
		packed, _ = negative.Pack()
		cache.put(key, packed, now)
		answer, ok = cache.get(key, q, now.Add(2*time.Second))
		if !ok || got.Unpack(answer) != nil || got.RCode != rcode || got.Authorities[0].Header.TTL != 3 {
			t.Fatal("negative cache extended SOA minimum TTL", rcode)
		}
		if _, ok := cache.get(key, q, now.Add(5*time.Second)); ok {
			t.Fatal("expired NXDOMAIN/NODATA remained cached", rcode)
		}
	}
	for _, rcode := range []dnsmessage.RCode{dnsmessage.RCodeServerFailure, dnsmessage.RCodeRefused, dnsmessage.RCodeNameError, dnsmessage.RCodeSuccess} {
		m := dnsmessage.Message{Header: dnsmessage.Header{Response: true, RCode: rcode}, Questions: q.Questions}
		packed, _ = m.Pack()
		cache.put(key, packed, now)
		if _, ok := cache.get(key, q, now); ok {
			t.Fatal("transient/empty/no-SOA answer acquired an invented cache TTL", rcode)
		}
	}
	positive.Answers[0].Header.TTL = 0
	packed, _ = positive.Pack()
	cache.put(key, packed, now)
	if _, ok := cache.get(key, q, now); ok {
		t.Fatal("zero-TTL answer was cached")
	}
}

func TestDNSReplyCacheEvictsColdEntryWithoutFlushingHotNames(t *testing.T) {
	var cache dnsReplyCache
	now := time.Now()
	var hot dnsQueryKey
	var question dnsmessage.Message
	for i := range dnsCacheSize + 1 {
		raw, _ := dnsQuestion(fmt.Sprintf("name%d.test", i))
		q, key, _ := cacheQuestion(raw)
		if i == 0 {
			hot, question = key, q
		}
		cache.put(key, dnsReply(t, raw), now)
		if _, ok := cache.get(hot, question, now); !ok {
			t.Fatal("hot name evicted while inserting other records")
		}
	}
	if len(cache.entries) != dnsCacheSize {
		t.Fatal("cache capacity is not bounded", len(cache.entries))
	}
	raw, _ := dnsQuestion("name1.test")
	q, cold, _ := cacheQuestion(raw)
	if _, ok := cache.get(cold, q, now); ok {
		t.Fatal("least recently used entry was retained instead of hot name")
	}
	q.Additionals = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName("."), Type: dnsmessage.TypeOPT, Class: 1232}, Body: &dnsmessage.OPTResource{}}}
	raw, _ = q.Pack()
	if _, _, cacheable := cacheQuestion(raw); cacheable {
		t.Fatal("EDNS options/datagram size entered ordinary question cache")
	}
}

func TestDNSSharedQueryOwnerCancellationPreservesLiveWaiter(t *testing.T) {
	address := udpResolver(t, func(raw []byte) []byte { return dnsReply(t, raw) })
	started := make(chan struct{})
	var once sync.Once
	var first atomic.Bool
	first.Store(true)
	r, err := newDNSResolver(defaultDNSSettings(), func(ctx context.Context, network, _ string) (net.Conn, error) {
		if first.Load() {
			once.Do(func() { close(started) })
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}, forbiddenTunnel(t))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := dnsQuestion("canceled.test")
	owner, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, e := r.resolve(owner, raw); done <- e }()
	<-started
	live := make(chan error, 1)
	go func() {
		reply, e := r.resolve(context.Background(), raw)
		if e == nil && !validDNSReply(raw, reply) {
			e = errors.New("invalid replacement reply")
		}
		live <- e
	}()
	waitDNSCount(t, &r.sharedQueries, 1)
	first.Store(false)
	cancel()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal("canceled owner retained its request", e)
	}
	if e := <-live; e != nil {
		t.Fatal("live waiter inherited another caller's cancellation", e)
	}
	if r.cacheMisses.Load() != 2 || len(r.queries) != 0 {
		t.Fatal("replacement did not settle one shared query", r.cacheMisses.Load())
	}
}

func TestDNSAddressCachePressureEvictsOneRecord(t *testing.T) {
	r, err := newDNSResolver(defaultDNSSettings(), func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("unexpected dial") }, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range dnsCacheSize {
		r.cache[fmt.Sprintf("old%d.test", i)] = dnsCacheEntry{ips: []netip.Addr{netip.MustParseAddr("203.0.113.9")}, until: time.Now().Add(time.Hour)}
	}
	r.cache["old0.test"] = dnsCacheEntry{until: time.Now().Add(-time.Second)}
	raw, _ := dnsQuestion("new.test")
	r.remember(dnsReply(t, raw))
	if len(r.cache) != dnsCacheSize {
		t.Fatal("address cache flushed its working set", len(r.cache))
	}
	if _, ok := r.cached("old1.test"); !ok {
		t.Fatal("valid working record removed")
	}
	if _, ok := r.cached("new.test"); !ok {
		t.Fatal("new record was not cached")
	}
}

func TestDNSCanceledWaiterLeavesSharedOwnerRunning(t *testing.T) {
	release := make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	address := udpResolver(t, func(raw []byte) []byte { <-release; return dnsReply(t, raw) })
	r, err := newDNSResolver(defaultDNSSettings(), func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := dnsQuestion("live.test")
	owner := make(chan error, 1)
	go func() { _, e := r.resolve(context.Background(), raw); owner <- e }()
	deadline := time.Now().Add(time.Second)
	for r.cacheMisses.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("owner did not start")
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiter := make(chan error, 1)
	go func() { _, e := r.resolve(ctx, raw); waiter <- e }()
	waitDNSCount(t, &r.sharedQueries, 1)
	cancel()
	if e := <-waiter; !errors.Is(e, context.Canceled) {
		t.Fatal("waiting caller did not release promptly", e)
	}
	unblock.Do(func() { close(release) })
	if e := <-owner; e != nil {
		t.Fatal("waiter canceled another caller's DNS sockets", e)
	}
	if r.cacheMisses.Load() != 1 {
		t.Fatal("waiter cancellation repeated upstream race")
	}
}

func TestDNSCacheSeparatesValidationFlags(t *testing.T) {
	raw, _ := dnsQuestion("flags.test")
	q, key, _ := cacheQuestion(raw)
	q.CheckingDisabled = true
	cdRaw, _ := q.Pack()
	_, cdKey, _ := cacheQuestion(cdRaw)
	if key == cdKey {
		t.Fatal("checking-disabled query shares ordinary cached replies")
	}
	r, _ := newDNSResolver(defaultDNSSettings(), func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("unexpected dial") }, nil)
	r.remember(dnsReply(t, cdRaw))
	if _, ok := r.cached("flags.test"); ok {
		t.Fatal("unvalidated CD answer seeded ordinary address cache")
	}
	q.CheckingDisabled = false
	q.RecursionDesired = false
	rdRaw, _ := q.Pack()
	_, rdKey, _ := cacheQuestion(rdRaw)
	if key == rdKey {
		t.Fatal("non-recursive question shares recursive cached reply")
	}
}

func TestDNSCachePassesUnknownRecordFormatsUnchanged(t *testing.T) {
	const rp = dnsmessage.Type(17)
	var calls atomic.Int32
	address := udpResolver(t, func(query []byte) []byte {
		packet := append([]byte(nil), query...)
		binary.BigEndian.PutUint16(packet[2:4], 0x8180)
		binary.BigEndian.PutUint16(packet[6:8], 2)
		owner := len(packet)
		packet = append(packet, query[12:len(query)-4]...)
		appendHeader := func() {
			packet = binary.BigEndian.AppendUint16(packet, uint16(rp))
			packet = binary.BigEndian.AppendUint16(packet, 1)
			packet = binary.BigEndian.AppendUint32(packet, 60)
			packet = binary.BigEndian.AppendUint16(packet, 4)
		}
		appendHeader()
		packet = append(packet, 0xc0, 12, 0xc0, 12)
		packet = append(packet, 0xc0, 12)
		appendHeader()
		// The second RP points into the first uncompressed owner's suffix.
		// Repacking the message compresses that owner and invalidates this
		// opaque RDATA pointer, even though both original records are valid.
		pointer := uint16(0xc000 | owner + 8)
		packet = binary.BigEndian.AppendUint16(packet, pointer)
		packet = binary.BigEndian.AppendUint16(packet, pointer)
		return packet
	})
	r, err := newDNSResolver(defaultDNSSettings(), func(ctx context.Context, network, _ string) (net.Conn, error) {
		calls.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := dnsQuestion("unknown.test")
	var q dnsmessage.Message
	q.Unpack(raw)
	q.Questions[0].Type = rp
	for i := range 2 {
		q.ID = uint16(i + 1)
		raw, _ = q.Pack()
		reply, err := r.resolve(context.Background(), raw)
		var response dnsmessage.Message
		if err != nil || !validDNSReply(raw, reply) || response.Unpack(reply) != nil || len(response.Answers) != 2 {
			t.Fatal("unknown RR exchange failed", err)
		}
		body := response.Answers[1].Body.(*dnsmessage.UnknownResource)
		offset := int(binary.BigEndian.Uint16(body.Data[:2]) & 0x3fff)
		if offset+5 > len(reply) || reply[offset] != 4 || string(reply[offset+1:offset+5]) != "test" {
			t.Fatal("cached unknown RDATA compression pointer was corrupted")
		}
	}
	if calls.Load() != 4 || r.cacheHits.Load() != 0 {
		t.Fatal("unsupported record layout was cached or rewritten")
	}
}

func TestDNSCacheDoesNotRewriteOpaqueAdditionalData(t *testing.T) {
	raw, _ := dnsQuestion("mixed.test")
	q, key, _ := cacheQuestion(raw)
	var response dnsmessage.Message
	response.Unpack(dnsReply(t, raw))
	response.Additionals = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Questions[0].Name, Type: 17, Class: dnsmessage.ClassINET, TTL: 60}, Body: &dnsmessage.UnknownResource{Type: 17, Data: []byte{0xc0, 12, 0xc0, 12}}}}
	reply, _ := response.Pack()
	var cache dnsReplyCache
	cache.put(key, reply, time.Now())
	if _, ok := cache.get(key, q, time.Now()); ok {
		t.Fatal("opaque additional record entered the rewriting cache")
	}
	if _, err := replyForQuestion(reply, q, 0); !errors.Is(err, errDNSUncachedLayout) {
		t.Fatal("coalesced reply rewrote an opaque record layout", err)
	}
}

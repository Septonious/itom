package mobile

import (
	"golang.org/x/net/dns/dnsmessage"
	"path/filepath"
	"testing"
)

type dnsHost struct{}

func (dnsHost) Protect(int) bool { return true }
func (dnsHost) Route(name string) string {
	if name == "blocked.example" {
		return "BLOCK"
	}
	return "VPN"
}
func (dnsHost) DNS() string          { return `["127.0.0.1"]` }
func (dnsHost) Lookup(string) string { return `["127.0.0.1"]` }
func TestDNSPersistsMappingsAndBlocksBeforeAllocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dns.db")
	db, e := openDNS(path)
	if e != nil {
		t.Fatal(e)
	}
	ip, e := db.address("first.example")
	if e != nil {
		t.Fatal(e)
	}
	db.Close()
	db, e = openDNS(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if name, e := db.name(ip); e != nil || name != "first.example" {
		t.Fatal(name, e)
	}
	other, e := db.address("other.example")
	if e != nil || other == ip {
		t.Fatal(other, e)
	}
	c := &Client{dns: db, host: dnsHost{}}
	name, _ := dnsmessage.NewName("blocked.example.")
	query := dnsmessage.Message{Header: dnsmessage.Header{ID: 1}, Questions: []dnsmessage.Question{{Name: name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}}}
	raw, _ := query.Pack()
	reply, e := c.answer(raw)
	if e != nil {
		t.Fatal(e)
	}
	var parsed dnsmessage.Message
	if parsed.Unpack(reply) != nil || parsed.RCode != dnsmessage.RCodeNameError || len(parsed.Answers) != 0 {
		t.Fatal("blocked DNS returned an address")
	}
}

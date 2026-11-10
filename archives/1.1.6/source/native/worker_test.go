package mobile

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

type dnsAssociation struct{ net.Conn }

func (d dnsAssociation) ID() stack.TransportEndpointID {
	return stack.TransportEndpointID{LocalPort: 53}
}
func (d dnsAssociation) ReadFrom(p []byte) (int, net.Addr, error) {
	n, e := d.Read(p)
	return n, d.RemoteAddr(), e
}
func (d dnsAssociation) WriteTo(p []byte, _ net.Addr) (int, error) { return d.Write(p) }

func TestIdleDNSAssociationsCannotRejectTCPWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{ctx: ctx, cancel: cancel, slots: make(chan struct{}, 128), dnsSlots: make(chan struct{}, 64)}
	defer func() { cancel(); c.wg.Wait() }()
	var peers []net.Conn
	defer func() {
		for _, p := range peers {
			p.Close()
		}
	}()
	for range 128 {
		a, b := net.Pipe()
		peers = append(peers, b)
		c.handleUDP(dnsAssociation{a})
	}
	a, b := net.Pipe()
	defer b.Close()
	accepted := make(chan struct{})
	c.worker(a, func() { close(accepted) })
	select {
	case <-accepted:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("idle DNS associations exhausted the TCP worker budget")
	}
}

func TestAnsweredUDPDNSAssociationReleasesIdleWorker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{ctx: ctx, cancel: cancel, host: pressureDNSHost{}, slots: make(chan struct{}, 128), dnsSlots: make(chan struct{}, 64)}
	defer func() { cancel(); c.wg.Wait() }()
	a, b := net.Pipe()
	defer b.Close()
	c.handleUDP(dnsAssociation{a})
	query := dnsmessage.Message{Header: dnsmessage.Header{ID: 17}, Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName("idle.test."), Type: dnsmessage.TypeAAAA, Class: dnsmessage.ClassINET}}}
	raw, err := query.Pack()
	if err != nil {
		t.Fatal(err)
	}
	b.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err = b.Write(raw); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 4096)
	if _, err = b.Read(buffer); err != nil {
		t.Fatal("DNS answer", err)
	}
	if _, err = b.Read(buffer); !errors.Is(err, io.EOF) {
		t.Fatal("answered DNS association retained its idle worker:", err)
	}
}

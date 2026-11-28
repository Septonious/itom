package mobile

import (
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"testing"
)

func TestTUNReceiveBufferFollowsBearer(t *testing.T) {
	s := stack.New(stack.Options{TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol}})
	defer s.Close()
	c := &Client{stack: s}
	for _, tc := range []struct {
		kind string
		size int
	}{{"wifi", tcp.DefaultReceiveBufferSize}, {"cellular", 128 << 10}, {"wifi", tcp.DefaultReceiveBufferSize}, {"unknown", tcp.DefaultReceiveBufferSize}} {
		c.SetNetworkType(tc.kind)
		var opt tcpip.TCPReceiveBufferSizeRangeOption
		wantMax := tcp.MaxBufferSize
		if tc.kind == "cellular" {
			wantMax = 128 << 10
		}
		if e := s.TransportProtocolOption(tcp.ProtocolNumber, &opt); e != nil || opt.Default != tc.size || opt.Max != wantMax {
			t.Fatalf("%s: %+v %v", tc.kind, opt, e)
		}
	}
}

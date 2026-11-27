package mobile

import (
	tunoption "github.com/xjasonlyu/tun2socks/v2/core/option"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
)

// Bound the local app-to-TUN receive queue on cellular. This is independent
// of mailbox pacing, and applies only to new TCP flows (never resets a flow).
func tunReceiveBuffer(kind string) int {
	if kind == "cellular" {
		return 128 << 10
	}
	return tcp.DefaultReceiveBufferSize
}

func (c *Client) tunOptions() []tunoption.Option {
	if kind := c.networkType.Load(); kind != nil {
		return []tunoption.Option{tunoption.WithTCPReceiveBufferSize(tunReceiveBuffer(*kind))}
	}
	return nil
}

// Android reports the physical network after Attach and on handovers. Update
// the default for future sockets without closing the stack or existing flows.
func (c *Client) refreshTUNReceiveBuffer() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stack == nil || c.closed {
		return
	}
	for _, option := range c.tunOptions() {
		// The sizes are valid constants for the pinned TCP implementation.
		_ = option(c.stack)
	}
}

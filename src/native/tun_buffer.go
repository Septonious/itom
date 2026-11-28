package mobile

import (
	tunoption "github.com/xjasonlyu/tun2socks/v2/core/option"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
)

// Bound local app-to-TUN receive queues on cellular without resetting existing flows.
func tunReceiveBuffer(kind string) int {
	if kind == "cellular" {
		return 128 << 10
	}
	return tcp.DefaultReceiveBufferSize
}
func (c *Client) tunOptions() []tunoption.Option {
	if kind := c.networkType.Load(); kind != nil {
		if *kind == "cellular" {
			// Bound the autotuning ceiling as well as the initial window.
			// Local TCP is not the cellular hop; large read-ahead only
			// delays backpressure to the application during a radio stall.
			return []tunoption.Option{tunoption.WithTCPReceiveBufferSizeRange(tcp.MinBufferSize, 128<<10, 128<<10)}
		}
		return []tunoption.Option{tunoption.WithTCPReceiveBufferSize(tunReceiveBuffer(*kind))}
	}
	return nil
}
func (c *Client) refreshTUNReceiveBuffer() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stack == nil || c.closed {
		return
	}
	for _, option := range c.tunOptions() {
		_ = option(c.stack)
	}
}

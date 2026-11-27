package mobile

import "strings"

// SetNetworkType retains the physical bearer as native diagnostic state.
// Do not alter mailbox pacing or learned RTT using an unverified bearer hint.
func (c *Client) SetNetworkType(kind string) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "wifi", "wi-fi", "wlan":
		kind = "wifi"
	case "cellular", "mobile", "cell":
		kind = "cellular"
	case "ethernet", "wired":
		kind = "ethernet"
	default:
		kind = "unknown"
	}
	previous := c.networkType.Swap(&kind)
	if pool, ok := c.currentPool().(interface{ SetNetworkType(string) }); ok {
		pool.SetNetworkType(kind)
	}
	if previous == nil || *previous != kind {
		c.refreshTUNReceiveBuffer()
		c.RecordNetworkEvent("NETWORK_"+strings.ToUpper(kind), 0)
	}
}

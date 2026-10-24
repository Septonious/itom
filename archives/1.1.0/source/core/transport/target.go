package transport

import (
	"encoding/binary"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// Target is an authenticated TCP destination. Exit authorization must resolve
// and validate it before connecting to a pinned address, never resolve it again.
type Target struct {
	Host string
	Port uint16
}

func ParseTarget(address string) (Target, error) {
	host, service, err := net.SplitHostPort(address)
	if err != nil {
		return Target{}, ErrProtocol
	}
	port, err := strconv.ParseUint(service, 10, 16)
	if err != nil || port == 0 {
		return Target{}, ErrProtocol
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Zone() != "" {
			return Target{}, ErrProtocol
		}
		host = ip.Unmap().String()
	} else {
		host = strings.ToLower(host)
	}
	t := Target{Host: host, Port: uint16(port)}
	if !t.valid() {
		return Target{}, ErrProtocol
	}
	return t, nil
}
func (t Target) valid() bool {
	if t.Port == 0 || len(t.Host) == 0 || len(t.Host) > 253 {
		return false
	}
	if ip, err := netip.ParseAddr(t.Host); err == nil {
		return ip.Zone() == "" && ip.Unmap().String() == t.Host
	}
	for _, label := range strings.Split(t.Host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range []byte(label) {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
func (t Target) encode() ([]byte, error) {
	if !t.valid() {
		return nil, ErrProtocol
	}
	p := binary.BigEndian.AppendUint16(nil, t.Port)
	p = append(p, byte(len(t.Host)))
	return append(p, t.Host...), nil
}
func decodeTarget(p []byte) (Target, error) {
	if len(p) < 4 || int(p[2]) != len(p)-3 {
		return Target{}, ErrProtocol
	}
	t := Target{Host: string(p[3:]), Port: binary.BigEndian.Uint16(p)}
	if !t.valid() {
		return Target{}, ErrProtocol
	}
	return t, nil
}

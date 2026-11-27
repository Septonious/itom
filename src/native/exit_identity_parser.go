package mobile

import (
	"errors"
	"net/netip"
	"strings"
)

type exitIdentity struct {
	IP      string `json:"ip"`
	Country string `json:"country"`
}

func parseExitTrace(raw string) (exitIdentity, error) {
	var result exitIdentity
	for _, line := range strings.Split(raw, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "ip":
			ip, err := netip.ParseAddr(value)
			if err != nil || ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.IsPrivate() {
				return exitIdentity{}, errors.New("EXIT_IP_INVALID")
			}
			result.IP = ip.String()
		case "loc":
			if len(value) == 2 && value[0] >= 'A' && value[0] <= 'Z' && value[1] >= 'A' && value[1] <= 'Z' && value != "XX" {
				result.Country = value
			}
		}
	}
	if result.IP == "" {
		return exitIdentity{}, errors.New("EXIT_IP_MISSING")
	}
	return result, nil
}

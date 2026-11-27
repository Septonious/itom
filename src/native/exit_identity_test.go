package mobile

import "testing"

func TestRC12ExitTraceParser(t *testing.T) {
	for _, v := range []struct{ raw, ip, country string }{{"ip=8.8.8.8\nloc=US\n", "8.8.8.8", "US"}, {"ip=2606:4700::1111\nloc=SE", "2606:4700::1111", "SE"}, {"ip=8.8.8.8\nloc=XX", "8.8.8.8", ""}} {
		got, err := parseExitTrace(v.raw)
		if err != nil || got.IP != v.ip || got.Country != v.country {
			t.Fatal(got, err)
		}
	}
	for _, raw := range []string{"loc=US", "ip=127.0.0.1", "ip=192.168.0.1", "ip=not-an-ip", "ip=fe80::1%eth0"} {
		if _, err := parseExitTrace(raw); err == nil {
			t.Fatal("accepted", raw)
		}
	}
}

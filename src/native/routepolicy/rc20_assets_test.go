package routepolicy

import (
 "net/netip"
 "os"
 "testing"
)

// CI extracts the actual, checksum-verified RC20 datasets, rather than
// validating only synthetic protobuf fixtures.
func TestRC20PackagedAssets(t *testing.T) {
 dir:=os.Getenv("ITOM_RC20_DATA_DIR")
 if dir=="" { t.Skip("RC20 dataset fixture not supplied") }
 p,err:=Load("{}",dir)
 if err!=nil { t.Fatal(err) }
 if len(p.ru)==0 || len(p.ads.suffix)==0 || len(p.malware.suffix)==0 { t.Fatal("empty dataset") }
 if p.Decide("example.ru",netip.Addr{})!=Direct {t.Fatal("RU suffix rule")}
 if p.Decide("github.com",netip.Addr{})!=VPN {t.Fatal("GeoData download rule")}
 t.Logf("validated packaged data: %d RU prefixes, %d ads/tracker suffixes, %d malware suffixes",len(p.ru),len(p.ads.suffix),len(p.malware.suffix))
}

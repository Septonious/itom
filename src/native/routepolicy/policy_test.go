package routepolicy
import (
 "encoding/binary"
 "net/netip"
 "os"
 "path/filepath"
 "testing"
)
func pb(n uint64,b []byte) []byte {o:=binary.AppendUvarint(nil,n<<3|2);o=binary.AppendUvarint(o,uint64(len(b)));return append(o,b...)}
func pv(n,v uint64) []byte {return binary.AppendUvarint(binary.AppendUvarint(nil,n<<3),v)}
func cat(name string,entries ...[]byte) []byte {b:=pb(1,[]byte(name));for _,e:=range entries{b=append(b,pb(2,e)...)};return pb(1,b)}
func dom(k uint64,v string) []byte{return append(pv(1,k),pb(2,[]byte(v))...)}
func data(t *testing.T) string {
 t.Helper();dir:=t.TempDir()
 geo:=append(cat("ru",dom(2,"local.example")),cat("category-ru",dom(3,"exact.example"))...);geo=append(geo,cat("category-ads-all",dom(0,"advertising"))...)
 geo=append(geo,cat("custom",dom(1,"^re[0-9]+[.]example$"))...)
 ip:=cat("RU",append(pb(1,[]byte{203,0,113,0}),pv(2,24)...),append(pb(1,netip.MustParseAddr("2001:db8::").AsSlice()),pv(2,32)...))
 for k,v:=range map[string][]byte{"geosite.dat":geo,"geoip.dat":ip,"ads-trackers.txt":[]byte("# comment\ntracker.example\n"),"malware.txt":[]byte("malware.example\n")}{if e:=os.WriteFile(filepath.Join(dir,k),v,0600);e!=nil{t.Fatal(e)}}
 return dir
}
func TestRulePrecedence(t *testing.T){
 p,e:=Load("{\"block\":[\"domain:blocked.example\",\"ip:198.51.100.0/24\"],\"proxy\":[\"domain:forced.ru\",\"full:exactvpn.example\"],\"direct\":[\"domain:forced.ru\",\"domain:github.com\",\"domain:blocked.example\",\"geosite:custom\",\"ip:192.0.2.1\"]}",data(t));if e!=nil{t.Fatal(e)}
 for _,c:=range []struct{host,ip string;want Action}{
 {"x.blocked.example","",Block},{"forced.ru","",VPN},{"forced.ru","198.51.100.7",Block},{"github.com","",VPN},{"cdn.jsdelivr.net","",VPN},{"x.githubusercontent.com","",VPN},{"sub.tracker.example","",Block},{"malware.example","",Block},{"myadvertising.example","",Block},{"sub.local.example","",Direct},{"exact.example","",Direct},{"x.exact.example","",VPN},{"news.ru","",Direct},{"news.xn--p1ai","",Direct},{"news.ru.evil.example","",VPN},{"ordinary.example","203.0.113.42",Direct},{"ordinary.example","2001:db8::1",Direct},{"ordinary.example","192.0.2.1",Direct},{"re42.example","",Direct},{"unmatched.example","",VPN},{"badblocked.example","",VPN},
 }{var ip netip.Addr;if c.ip!=""{ip=netip.MustParseAddr(c.ip)};if got:=p.Decide(c.host,ip);got!=c.want{t.Errorf("%s %s: got %s want %s",c.host,c.ip,got,c.want)}}
}
func TestDisabledAndLegacyMode(t *testing.T){
 p,e:=Load("{\"enabled\":false,\"mode\":\"legacy\",\"ru_sites_direct\":false,\"ru_ips_direct\":false,\"block_ads_trackers\":false,\"block_malware\":false,\"block\":[\"full:one.example\"]}",data(t));if e!=nil{t.Fatal(e)}
 if p.Decide("one.example",netip.Addr{})!=Block||p.Decide("x.one.example",netip.Addr{})!=VPN||p.Decide("news.ru",netip.Addr{})!=VPN||p.Decide("tracker.example",netip.Addr{})!=VPN||p.Decide("203.0.113.42",netip.Addr{})!=VPN{t.Fatal("policy switch or legacy mode")}
}
func TestInvalidInputs(t *testing.T){
 dir:=data(t);for _,raw:=range []string{"null","{} {}","{\"geoip\":true}","{\"direct\":[\"geoip:us\"]}","{\"direct\":[\"geosite:unknown\"]}","{\"direct\":[\"ip:192.0.2.0/99\"]}","{\"direct\":[\"regexp:.*\"]}","{\"direct\":[\"*.example\"]}"}{if _,e:=Load(raw,dir);e==nil{t.Errorf("accepted %s",raw)}}
 if e:=os.WriteFile(filepath.Join(dir,"geoip.dat"),[]byte{10,255},0600);e!=nil{t.Fatal(e)};if _,e:=Load("{}",dir);e==nil{t.Fatal("accepted corrupt protobuf")}
}

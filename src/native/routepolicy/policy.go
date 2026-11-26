// Package routepolicy implements the CSQTT destination-rule ordering for ITOM.
// A Policy is immutable after loading and safe for concurrent flow decisions.
package routepolicy

import (
 "bufio"
 "encoding/binary"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "net/netip"
 "os"
 "path/filepath"
 "regexp"
 "strings"
 "unicode"
 "golang.org/x/net/idna"
)

type Action string
const (
 Block Action = "BLOCK"
 VPN Action = "VPN"
 Direct Action = "DIRECT"
)
const maxData = 64 << 20
type settings struct {
 Enabled bool `json:"enabled"`
 Mode string `json:"mode"`
 RUSites bool `json:"ru_sites_direct"`
 RUIPs bool `json:"ru_ips_direct"`
 Ads bool `json:"block_ads_trackers"`
 Malware bool `json:"block_malware"`
 Direct []string `json:"direct"`
 Proxy []string `json:"proxy"`
 Block []string `json:"block"`
}
type domains struct {
 full, suffix map[string]struct{}
 keywords []string
 regexps []*regexp.Regexp
}
func newDomains() *domains { return &domains{full:map[string]struct{}{},suffix:map[string]struct{}{}} }
func normalize(s string) string { return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s),".")) }
func canonicalHost(s string) string {
 s=normalize(strings.TrimPrefix(s,"."))
 if ascii,err:=idna.Lookup.ToASCII(s);err==nil{return ascii}
 return ""
}
func validDomain(s string) bool {
 if len(s)==0 || len(s)>253 { return false }
 for _,label:=range strings.Split(s,".") {
  if len(label)==0 || len(label)>63 || label[0]=='-' || label[len(label)-1]=='-' { return false }
  for _,r:=range label { if r!='-' && !unicode.IsLetter(r) && !unicode.IsDigit(r) { return false } }
 }
 return true
}
func suffixMatch(s, domain string) bool { return s==domain || strings.HasSuffix(s,"."+domain) }
func (d *domains) match(host string) bool {
 if d==nil || host=="" { return false }
 if _,ok:=d.full[host];ok{return true}
 for s:=host;; {
  if _,ok:=d.suffix[s];ok{return true}
  i:=strings.IndexByte(s,'.');if i<0{break};s=s[i+1:]
 }
 for _,k:=range d.keywords {if strings.Contains(host,k){return true}}
 for _,r:=range d.regexps {if r.MatchString(host){return true}}
 return false
}
type rule struct { kind, value string; prefix netip.Prefix }
type Policy struct {
 cfg settings
 sites map[string]*domains
 ru map[netip.Prefix]struct{}
 ruBits []int
 ads, malware *domains
 block, vpn, direct []rule
}
func loadSettings(raw string) (settings,error) {
 c:=settings{Enabled:true,Mode:"rules",RUSites:true,RUIPs:true,Ads:true,Malware:true}
 if len(raw)==0 || len(raw)>128000 || strings.TrimSpace(raw)=="null" {return c,errors.New("ROUTING_POLICY_SIZE")}
 d:=json.NewDecoder(strings.NewReader(raw));d.DisallowUnknownFields()
 if err:=d.Decode(&c);err!=nil{return c,fmt.Errorf("ROUTING_POLICY_JSON: %w",err)}
 var trailing any
 if err:=d.Decode(&trailing);err!=io.EOF{return c,errors.New("ROUTING_POLICY_TRAILING")}
 return c,nil
}
func parseRules(lines []string) ([]rule,error) {
 if len(lines)>256{return nil,errors.New("ROUTING_RULE_LIMIT")}
 out:=make([]rule,0,len(lines))
 for _,line:=range lines {
  line=strings.TrimSpace(line)
  if line==""||strings.HasPrefix(line,"#"){continue}
  if len(line)>1024{return nil,errors.New("ROUTING_RULE_SIZE")}
  k,v,ok:=strings.Cut(line,":");if ip,e:=netip.ParseAddr(line);e==nil&&ip.IsValid(){k,v,ok="ip",line,true}else if p,e:=netip.ParsePrefix(line);e==nil&&p.IsValid(){k,v,ok="ip",line,true};if !ok{k,v="domain",line}
  k=strings.ToLower(strings.TrimSpace(k));v=normalize(v)
  r:=rule{kind:k,value:v}
  switch k {
  case "domain","full":
   r.value=canonicalHost(v);if !validDomain(r.value){return nil,fmt.Errorf("ROUTING_DOMAIN: %q",line)}
  case "ip":
   if ip,e:=netip.ParseAddr(v);e==nil{r.prefix=netip.PrefixFrom(ip.Unmap(),ip.Unmap().BitLen())}else{
    p,e:=netip.ParsePrefix(v);if e!=nil{return nil,fmt.Errorf("ROUTING_IP: %q",line)};if p.Addr().Is4In6(){if p.Bits()<96{return nil,fmt.Errorf("ROUTING_IP: %q",line)};p=netip.PrefixFrom(p.Addr().Unmap(),p.Bits()-96)};r.prefix=p.Masked()
   }
  case "geosite":
   if v==""{return nil,errors.New("ROUTING_GEOSITE_EMPTY")}
  case "geoip":
   if v!="ru"{return nil,fmt.Errorf("ROUTING_GEOIP_UNSUPPORTED: %q",v)}
  default:return nil,fmt.Errorf("ROUTING_RULE_UNSUPPORTED: %q",k)
  }
  out=append(out,r)
 }
 return out,nil
}
func (p *Policy) russianIP(ip netip.Addr) bool {
 if !ip.IsValid(){return false};ip=ip.Unmap()
 for _,n:=range p.ruBits {if n<=ip.BitLen(){if _,ok:=p.ru[netip.PrefixFrom(ip,n).Masked()];ok{return true}}}
 return false
}
func (p *Policy) matches(rs []rule,h string,ip netip.Addr) bool {
 for _,r:=range rs {
  switch r.kind {
  case "domain":if suffixMatch(h,r.value){return true}
  case "full":if h==r.value{return true}
  case "ip":if ip.IsValid()&&r.prefix.Contains(ip.Unmap()){return true}
  case "geoip":if p.russianIP(ip){return true}
  case "geosite":if p.sites[r.value].match(h){return true}
  }
 }
 return false
}
// Decision is the stable RC20 routing-check JSON contract. Native dispatch
// uses Action only; matched/reason are human-readable local rule evidence.
type Decision struct {
 Action Action
 Matched string
 Rule string
 Reason string
}
func decision(action Action, matched, reason string) Decision {return Decision{action,matched,matched,reason}}
func (p *Policy) firstMatch(rs []rule,h string,ip netip.Addr) string {
 for _,r:=range rs {if p.matches([]rule{r},h,ip){return r.kind+":"+r.value}}
 return ""
}
func (p *Policy) Decide(host string,ip netip.Addr) Action {return p.DecideDetailed(host,ip).Action}
func (p *Policy) DecideDetailed(host string, ip netip.Addr) Decision {
 h:=normalize(host)
 if literal,e:=netip.ParseAddr(h);e==nil{if !ip.IsValid(){ip=literal.Unmap()};h=""}else{h=canonicalHost(h)}
 if m:=p.firstMatch(p.block,h,ip);m!=""{return decision(Block,m,"custom block")}
 if p.cfg.Ads&&(p.ads.match(h)||p.sites["category-ads-all"].match(h)){return decision(Block,"ads-trackers","advertising or tracking")}
 if p.cfg.Malware&&p.malware.match(h){return decision(Block,"malware","malware list")}
 if h=="github.com"||h=="cdn.jsdelivr.net"||suffixMatch(h,"githubusercontent.com"){return decision(VPN,"geodata-update","dataset update host")}
 if m:=p.firstMatch(p.vpn,h,ip);m!=""{return decision(VPN,m,"custom VPN")}
 if m:=p.firstMatch(p.direct,h,ip);m!=""{return decision(Direct,m,"custom direct")}
 if p.cfg.RUSites&&(p.sites["ru"].match(h)||p.sites["category-ru"].match(h)||strings.HasSuffix(h,".ru")||strings.HasSuffix(h,".xn--p1ai")){return decision(Direct,"geosite:category-ru","Russian domain")}
 if p.cfg.RUIPs&&p.russianIP(ip){return decision(Direct,"geoip:ru","Russian IP")}
 return decision(VPN,"","default rule")
}
func readData(dir,name string) ([]byte,error) {
 f,e:=os.Open(filepath.Join(dir,name));if e!=nil{return nil,e};defer f.Close()
 info,e:=f.Stat();if e!=nil||!info.Mode().IsRegular(){return nil,fmt.Errorf("ROUTING_DATA_FILE: %s",name)}
 b,e:=io.ReadAll(io.LimitReader(f,maxData+1));if e!=nil{return nil,e}
 if len(b)==0||len(b)>maxData{return nil,fmt.Errorf("ROUTING_DATA_SIZE: %s",name)}
 return b,nil
}
// field decodes the V2Ray protobuf wire format without a runtime dependency.
type field struct { number uint64; bytes []byte; numberValue uint64; wire uint64 }
func fields(b []byte, visit func(field) error) error {
 for len(b)>0 {
  tag,n:=binary.Uvarint(b);if n<=0||tag>>3==0||tag>>3>536870911{return errors.New("ROUTING_PROTO_TAG")};b=b[n:]
  f:=field{number:tag>>3,wire:tag&7}
  switch f.wire {
  case 0:v,n:=binary.Uvarint(b);if n<=0{return errors.New("ROUTING_PROTO_VARINT")};f.numberValue=v;b=b[n:]
  case 1:if len(b)<8{return io.ErrUnexpectedEOF};b=b[8:]
  case 2:
   size,n:=binary.Uvarint(b);if n<=0{return errors.New("ROUTING_PROTO_LENGTH")};b=b[n:];if size>uint64(len(b)){return io.ErrUnexpectedEOF};f.bytes=b[:int(size)];b=b[int(size):]
  case 5:if len(b)<4{return io.ErrUnexpectedEOF};b=b[4:]
  default:return errors.New("ROUTING_PROTO_WIRE")
  }
  if err:=visit(f);err!=nil{return err}
 }
 return nil
}
func category(b []byte) (string,[][]byte,error) {
 var name string;var entries [][]byte
 err:=fields(b,func(f field)error{
  if f.number==1&&f.wire==2{name=normalize(string(f.bytes))}
  if f.number==2&&f.wire==2{entries=append(entries,f.bytes)}
  return nil
 })
 if err==nil&&name==""{err=errors.New("ROUTING_DATA_CATEGORY")}
 return name,entries,err
}
func loadSites(raw []byte,wanted,required map[string]bool) (map[string]*domains,error) {
 out:=map[string]*domains{}
 err:=fields(raw,func(f field)error{
  if f.number!=1||f.wire!=2{return nil}
  name,items,e:=category(f.bytes);if e!=nil{return e};if !wanted[name]{return nil}
  d:=out[name];if d==nil{d=newDomains();out[name]=d}
  for _,item:=range items {
   var kind uint64;var value string
   e=fields(item,func(f field)error{if f.number==1&&f.wire==0{kind=f.numberValue};if f.number==2&&f.wire==2{value=string(f.bytes)};return nil})
   if e!=nil{return e};if value==""||len(value)>8192{return errors.New("ROUTING_GEOSITE_ENTRY")}
   switch kind {
   case 0:d.keywords=append(d.keywords,strings.ToLower(value))
   case 1:r,e:=regexp.Compile(value);if e!=nil{return fmt.Errorf("ROUTING_GEOSITE_REGEX: %w",e)};d.regexps=append(d.regexps,r)
   case 2:d.suffix[normalize(value)]=struct{}{}
   case 3:d.full[normalize(value)]=struct{}{}
   default:return errors.New("ROUTING_GEOSITE_KIND")
   }
  }
  return nil
 })
 if err!=nil{return nil,err}
 for name:=range required{if out[name]==nil{return nil,fmt.Errorf("ROUTING_GEOSITE_MISSING: %s",name)}}
 return out,nil
}
func loadRU(raw []byte) (map[netip.Prefix]struct{},[]int,error) {
 out:=map[netip.Prefix]struct{}{};seen:=map[int]bool{}
 err:=fields(raw,func(f field)error{
  if f.number!=1||f.wire!=2{return nil}
  name,items,e:=category(f.bytes);if e!=nil{return e};if name!="ru"{return nil}
  if e=fields(f.bytes,func(x field)error{if x.number==3&&x.numberValue!=0{return errors.New("ROUTING_GEOIP_REVERSE")};return nil});e!=nil{return e}
  for _,item:=range items{
   var addr netip.Addr;var bits uint64
   e=fields(item,func(f field)error{
    if f.number==1&&f.wire==2{var ok bool;addr,ok=netip.AddrFromSlice(f.bytes);if !ok{return errors.New("ROUTING_GEOIP_ADDRESS")}}
    if f.number==2&&f.wire==0{bits=f.numberValue};return nil
   })
   if e!=nil{return e};if !addr.IsValid()||addr.Is4In6()||bits>uint64(addr.BitLen()){return errors.New("ROUTING_GEOIP_PREFIX")}
   out[netip.PrefixFrom(addr,int(bits)).Masked()]=struct{}{};seen[int(bits)]=true
  }
  return nil
 })
 if err!=nil{return nil,nil,err};if len(out)==0{return nil,nil,errors.New("ROUTING_GEOIP_RU_MISSING")}
 bits:=make([]int,0,len(seen));for n:=range seen{bits=append(bits,n)}
 return out,bits,nil
}
func loadList(raw []byte) (*domains,error) {
 d:=newDomains();scan:=bufio.NewScanner(strings.NewReader(string(raw)));scan.Buffer(make([]byte,4096),1<<20)
 for scan.Scan(){s:=normalize(scan.Text());if s==""||strings.HasPrefix(s,"#")||strings.HasPrefix(s,"!"){continue};if !validDomain(s){return nil,fmt.Errorf("ROUTING_BLOCKLIST_ENTRY: %q",s)};d.suffix[s]=struct{}{}}
 if e:=scan.Err();e!=nil{return nil,e};if len(d.suffix)==0{return nil,errors.New("ROUTING_BLOCKLIST_EMPTY")};return d,nil
}
// Load parses the four RC20 assets plus a CSQTT native policy JSON snapshot.
func Load(raw,dir string) (*Policy,error) {
 cfg,e:=loadSettings(raw);if e!=nil{return nil,e}
 p:=&Policy{cfg:cfg}
 if p.block,e=parseRules(cfg.Block);e!=nil{return nil,e}
 if p.vpn,e=parseRules(cfg.Proxy);e!=nil{return nil,e}
 if p.direct,e=parseRules(cfg.Direct);e!=nil{return nil,e}
 wanted:=map[string]bool{"ru":true,"category-ru":true,"category-ads-all":true}
 required:=map[string]bool{"category-ads-all":true}
 for _,rs:=range [][]rule{p.block,p.vpn,p.direct}{for _,r:=range rs{if r.kind=="geosite"{wanted[r.value]=true;required[r.value]=true}}}
 b,e:=readData(dir,"geosite.dat");if e!=nil{return nil,e}
 if p.sites,e=loadSites(b,wanted,required);e!=nil{return nil,e}
 if p.sites["ru"]==nil&&p.sites["category-ru"]==nil{return nil,errors.New("ROUTING_GEOSITE_RU_MISSING")}
 b,e=readData(dir,"geoip.dat");if e!=nil{return nil,e}
 if p.ru,p.ruBits,e=loadRU(b);e!=nil{return nil,e}
 b,e=readData(dir,"ads-trackers.txt");if e!=nil{return nil,e}
 if p.ads,e=loadList(b);e!=nil{return nil,e}
 b,e=readData(dir,"malware.txt");if e!=nil{return nil,e}
 if p.malware,e=loadList(b);e!=nil{return nil,e}
 return p,nil
}

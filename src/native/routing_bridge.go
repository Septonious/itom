package mobile

import (
 "errors"
 "net/netip"
 "strings"
 "itom.local/mobile/routepolicy"
)

// RC20 uses two string parameters for validation and four for CheckRouting.
// The historical caller source is unavailable; accept either order of the
// JSON policy and data directory, while rejecting malformed inputs.
func routingArguments(first,second string) (raw,dir string,err error) {
 a,b:=strings.HasPrefix(strings.TrimSpace(first),"{"),strings.HasPrefix(strings.TrimSpace(second),"{")
 if a==b{return "","",errors.New("ROUTING_POLICY_ARGUMENTS")}
 if a{return first,second,nil}
 return second,first,nil
}
func ValidateRoutingData(dir string) error {
 _,err:=routepolicy.Load("{}",dir)
 return err
}
func ValidateRoutingPolicy(first,second string) error {
 raw,dir,err:=routingArguments(first,second);if err!=nil{return err}
 _,err=routepolicy.Load(raw,dir)
 return err
}
func CheckRouting(first,second,host,address string) (string,error) {
 raw,dir,err:=routingArguments(first,second);if err!=nil{return "",err}
 var ip netip.Addr
 if address!=""{ip,err=netip.ParseAddr(address);if err!=nil{return "",errors.New("ROUTING_TARGET_ADDRESS")}}
 p,err:=routepolicy.Load(raw,dir);if err!=nil{return "",err}
 return string(p.Decide(host,ip)),nil
}
func (c *Client) route(host string,ip netip.Addr) string {
 if c.routing==nil{return c.host.Route(host)}
 return string(c.routing.Decide(host,ip))
}

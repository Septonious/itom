package mobile

import (
 "net/netip"
 "itom.local/mobile/routepolicy"
)

// These Go API signatures are provisional until compared with the RC20 DEX.
// The methods execute real parsing/classification; none is a success stub.
func ValidateRoutingData(dir string) error {
 _,err:=routepolicy.Load("{}",dir)
 return err
}
func ValidateRoutingPolicy(raw,dir string) error {
 _,err:=routepolicy.Load(raw,dir)
 return err
}
func CheckRouting(raw,dir,target string) (string,error) {
 p,err:=routepolicy.Load(raw,dir);if err!=nil{return "",err}
 return string(p.Decide(target,netip.Addr{})),nil
}
func (c *Client) route(host string,ip netip.Addr) string {
 if c.routing==nil{return c.host.Route(host)}
 return string(c.routing.Decide(host,ip))
}

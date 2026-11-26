package mobile

import "testing"

func TestNetworkTypeNormalization(t *testing.T) {
 c:=&Client{}
 for _,v:=range []struct{in,want string}{{" Wi-Fi ","wifi"},{"mobile","cellular"},{"ethernet","ethernet"},{"unexpected","unknown"}}{
  c.SetNetworkType(v.in)
  if p:=c.networkType.Load();p==nil||*p!=v.want{t.Errorf("network type for %q",v.in)}
 }
}
func TestRoutingArgumentOrder(t *testing.T){
 for _,v:=range [][2]string{{"{}","/data/routing"},{"/data/routing","{}"}}{
  raw,dir,e:=routingArguments(v[0],v[1]);if e!=nil||raw!="{}"||dir!="/data/routing"{t.Fatal(raw,dir,e)}
 }
 if _,_,e:=routingArguments("not json","/data/routing");e==nil{t.Fatal("invalid argument accepted")}
}

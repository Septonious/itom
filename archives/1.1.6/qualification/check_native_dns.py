"""Test-only overlay: real native TUN DNS, UDP-to-TCP DNS and named HTTPS.
Production sources and the frozen transport are not modified.
"""
from pathlib import Path
import json,os,subprocess,argparse,shutil
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--out',type=Path,required=True)
parser.add_argument('--go',default=shutil.which('go'))
parser.add_argument('--count',type=int,default=3)
args=parser.parse_args()
root=Path(__file__).resolve().parent;source=root.parent/'source';out=args.out.resolve();out.mkdir(parents=True,exist_ok=True)
if not args.go:parser.error('pinned Go 1.27.1 required')
if not subprocess.check_output([args.go,'version'],text=True).startswith('go version go1.27.1 '):parser.error('unexpected Go version')
p=source/'native/traffic_integration_test.go';s=p.read_text()
s=s.replace('"encoding/json"','"encoding/json"\n"encoding/binary"\n"sync/atomic"')
s=s.replace('"golang.org/x/net/proxy"','"golang.org/x/net/proxy"\n"golang.org/x/net/dns/dnsmessage"\n"gvisor.dev/gvisor/pkg/tcpip/transport/udp"')
a='\traw, err := os.ReadFile(input)';assert s.count(a)==1;s=s.replace(a,'\tdnsCounts := startupDNSFixture(t)\n'+a)
a='\tc, err := New(string(raw), os.Getenv("ITOM_NATIVE_TEST_STATE"), trafficHost{})';assert s.count(a)==1;s=s.replace(a,'''
 var document map[string]any
 if e:=json.Unmarshal(raw,&document);e!=nil {t.Fatal(e)}
 document["DNSResolver"]=map[string]any{"primary":"127.0.0.83","backup":"127.0.0.84","itom_fallback":false}
 raw,err=json.Marshal(document);if err!=nil {t.Fatal(err)}
'''+a)
s=s.replace('TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol}', 'TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol,udp.NewProtocol}')
a='\tfor _, mode := range []string{"socks", "tun"} {';assert s.count(a)==1;s=s.replace(a,'''
 // A real UDP DNS packet crosses the emulated app's TUN before any HTTPS.
 socket,e:=gonet.DialUDP(app,nil,&tcpip.FullAddress{NIC:1,Addr:tcpip.AddrFrom4([4]byte{172,19,0,2}),Port:53},ipv4.ProtocolNumber)
 if e!=nil {t.Fatal(e)}
 _=socket.SetDeadline(time.Now().Add(2*time.Second))
 name,e:=dnsmessage.NewName("example.com.");if e!=nil {t.Fatal(e)}
 query,e:=(&dnsmessage.Message{Header:dnsmessage.Header{ID:1234,RecursionDesired:true},Questions:[]dnsmessage.Question{{Name:name,Type:dnsmessage.TypeA,Class:dnsmessage.ClassINET}}}).Pack();if e!=nil {t.Fatal(e)}
 if _,e=socket.Write(query);e!=nil {t.Fatal(e)}
 buffer:=make([]byte,4096);n,e:=socket.Read(buffer);socket.Close();if e!=nil {t.Fatal("actual TUN DNS",e)}
 var answer dnsmessage.Message;if e=answer.Unpack(buffer[:n]);e!=nil||answer.ID!=1234||answer.RCode!=dnsmessage.RCodeSuccess {t.Fatal("TUN DNS response",e)}
 var appIP [4]byte;found:=false
 for _,record:=range answer.Answers {if a,ok:=record.Body.(*dnsmessage.AResource);ok {appIP=a.A;found=true;break}}
 if !found {t.Fatal("TUN DNS returned no address")}
 if dnsCounts.udp.Load()==0||dnsCounts.tcp.Load()==0 {t.Fatal("protected UDP/53 to TCP/53 fallback was not exercised")}
 t.Logf("TUN DNS and protected UDP/TCP 53 passed; udp=%d tcp=%d",dnsCounts.udp.Load(),dnsCounts.tcp.Load())
'''+a)
s=s.replace('DialContext(ctx, "tcp", "203.0.113.80:443")', 'DialContext(ctx, "tcp", "example.com:443")')
s=s.replace('Addr: tcpip.AddrFrom4([4]byte{203, 0, 113, 80}), Port: 443','Addr: tcpip.AddrFrom4(appIP), Port: 443')
s+='''
// TC responses force the production resolver to use protected TCP/53.
type startupDNSCounts struct {udp,tcp atomic.Int64}
func startupDNSFixture(t *testing.T)*startupDNSCounts {
 t.Helper();counts:=&startupDNSCounts{}
 reply:=func(raw []byte,truncated bool)[]byte {
  var q dnsmessage.Message;if q.Unpack(raw)!=nil||len(q.Questions)!=1{return nil}
  q.Header.Response=true;q.Header.Truncated=truncated;q.Header.RCode=dnsmessage.RCodeSuccess
  if !truncated {q.Answers=[]dnsmessage.Resource{{Header:dnsmessage.ResourceHeader{Name:q.Questions[0].Name,Type:dnsmessage.TypeA,Class:dnsmessage.ClassINET,TTL:60},Body:&dnsmessage.AResource{A:[4]byte{203,0,113,80}}}}}
  b,_:=q.Pack();return b
 }
 for _,host:=range []string{"127.0.0.83","127.0.0.84"} {
  datagram,e:=net.ListenPacket("udp4",host+":53");if e!=nil{t.Fatal(e)};t.Cleanup(func(){datagram.Close()})
  go func(){buf:=make([]byte,4096);for {n,peer,e:=datagram.ReadFrom(buf);if e!=nil{return};counts.udp.Add(1);b:=reply(buf[:n],true);_,_=datagram.WriteTo(b,peer)}}()
  listener,e:=net.Listen("tcp4",host+":53");if e!=nil{t.Fatal(e)};t.Cleanup(func(){listener.Close()})
  go func(){for {c,e:=listener.Accept();if e!=nil{return};go func(){defer c.Close();_ = c.SetDeadline(time.Now().Add(time.Second));var head [2]byte;if _,e:=io.ReadFull(c,head[:]);e!=nil{return};size:=int(binary.BigEndian.Uint16(head[:]));if size<12||size>4096{return};raw:=make([]byte,size);if _,e:=io.ReadFull(c,raw);e!=nil{return};counts.tcp.Add(1);b:=reply(raw,false);binary.BigEndian.PutUint16(head[:],uint16(len(b)));_,_=c.Write(append(head[:],b...))}()}}()
 }
 return counts
}
'''
over=out/'traffic_integration_test.go';over.write_text(s)
subprocess.run([str(Path(args.go).resolve().with_name('gofmt')),'-w',str(over)],check=True)
manifest=out/'overlay.json';manifest.write_text(json.dumps({'Replace':{str(p):str(over)}}))
env=dict(os.environ,GOTOOLCHAIN='local',GOFLAGS='-overlay='+str(manifest));env['PATH']=str(Path(args.go).resolve().parent)+os.pathsep+env.get('PATH','')
# crypto/x509 caches system roots per process. Each repetition generates a
# fresh private fixture CA, so it must have its own process rather than -count=N.
for index in range(args.count):
 with (out/f'events-{index}.jsonl').open('w') as log,(out/f'stderr-{index}.log').open('w') as err:
  result=subprocess.run([args.go,'test','-json','-count=1','-run','^TestNativeVPNProcessUsesReplacement$','-timeout=180s','./tunnel'],cwd=source/'core',env=env,stdout=log,stderr=err)
 print('Native DNS/named HTTPS repetition',index,'exit:',result.returncode,flush=True)
 if result.returncode:raise SystemExit(result.returncode)

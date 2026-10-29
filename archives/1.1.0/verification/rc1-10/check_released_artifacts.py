"""Exercise the published server without changing released production code.

The Go overlay changes test orchestration only. Provider TLS/IMAP, Noise,
wallets, journals, native SOCKS/TUN, the published CLI and admin RPC remain real.
This is a local fixture, not an Android-device or Rambler qualification.
"""
from pathlib import Path
import hashlib,json,os,subprocess,sys,time,urllib.request,zipfile

REPO=Path(__file__).resolve().parents[4]
SOURCE=REPO/'archives/1.1.0/source'
ROOT=Path(os.environ.get('ITOM_RELEASE_CHECK_DIR','/workspace/work/v110/rc1-10-postrelease'))
ROOT.mkdir(parents=True,exist_ok=True)
SHA='462e718e44fbd5c3be328644a3d579d5f8cc1968'
BASE='https://github.com/Septonious/itom/releases/download/v1.1.0-rc1.10/'
SERVER='ITOM-1.1.0-RC1.10-linux-amd64.bin'
SERVER_SHA='8b660495f6885eb0913bb716a6f0cd5dbf58cccfac2f0d1bdf0ba41efb258334'
APK='ITOM-1.1.0-RC1.10.apk'
APK_SHA='303753ae16c394c3262ec201d36942f8c6d2001bd552e26e9f2f8b2107b5d8ae'

def fetched(name,digest):
 p=ROOT/name
 if not p.exists() or hashlib.sha256(p.read_bytes()).hexdigest()!=digest:
  with urllib.request.urlopen(BASE+name,timeout=60)as response,p.open('wb')as output:
   while chunk:=response.read(1<<20):output.write(chunk)
 assert hashlib.sha256(p.read_bytes()).hexdigest()==digest,name
 return p

subprocess.run(['git','diff','--exit-code',SHA,'HEAD','--','archives/1.1.0/source'],cwd=REPO,check=True,stdout=subprocess.DEVNULL)
server=fetched(SERVER,SERVER_SHA);server.chmod(0o700)
apk=fetched(APK,APK_SHA)
report=json.loads((REPO/'archives/1.1.0/publication/rc1-10/RC1_BUILD.json').read_text())
with zipfile.ZipFile(apk)as archive:
 for name,digest in report['apk_native_libraries'].items():
  raw=archive.read(name)
  assert hashlib.sha256(raw).hexdigest()==digest,name
  assert b'itom-1-1-0-rc1-10'in raw,name
print('Published server/APK bytes and embedded native hashes verified',flush=True)

core=SOURCE/'core/transport/endpoint_api_test.go'
s=core.read_text().replace('"itom.local/core/journal"','"itom.local/core/journal"\n"itom.local/core/adminapi"')
s=s.replace('type publicEndpointCase struct {','type publicEndpointCase struct {\n released bool\n androidRuntime bool')
s=s.replace('\troot := t.TempDir()','\troot := t.TempDir()\n serverState := filepath.Join(root,"server-state")\n if scenario.released { serverState=filepath.Join(serverState,"tunnel-server") }')
s=s.replace('filepath.Join(root, "server-state",','filepath.Join(serverState,')
s=s.replace('filepath.Join(root, "server-state")','serverState')
s=s.replace('\tcfg := servercfg.Config{Schema:', '\tif scenario.released {p.User.Name="admin"}\n\tcfg := servercfg.Config{Schema:')
needle='\tcmd := exec.Command(executable, "-test.run=^TestEndpointServerProcessHelper$", "-test.v", "-test.timeout="+serverCohortLifetime.String())'
assert needle in s
s=s.replace(needle,needle+'''
 if scenario.released {
  stateRoot:=filepath.Dir(serverState)
  if err=os.MkdirAll(stateRoot,0700);err!=nil {t.Fatal(err)}
  if err=servercfg.Update(filepath.Join(stateRoot,"server.db"),func(c *servercfg.Config)error{*c=cfg;return nil});err!=nil {t.Fatal(err)}
  cmd=exec.Command(os.Getenv("ITOM_RELEASE_SERVER"),"serve","--state",stateRoot)
 }
''')
# The platform discriminator and DNS settings match the Android runtime document.
s=s.replace('''		if scenario.native {
			raw, e := json.Marshal''','''		if scenario.native {
            if scenario.released || scenario.androidRuntime {p.Kind="itom-android"}
			raw, e := json.Marshal''')
needle='\t\tshutdown.Store(false)\n\t\ttr := &http.Transport'
assert needle in s
s=s.replace(needle,'''
        if scenario.released {
            for sample:=0;sample<3;sample++ {
                begin:=time.Now()
                callCtx,stop:=context.WithTimeout(ctx,1500*time.Millisecond)
                stream,_,e:=client.Open(callCtx,wire.Connect{Host:adminapi.Host,Port:adminapi.Port})
                if e!=nil {stop();client.Close();t.Fatal("published CLI OPEN",round,sample,e)}
                raw,_:=json.Marshal(adminapi.Request{Operation:"status"})
                result,e:=adminapi.Call(callCtx,stream.(net.Conn),raw,p.User.Key)
                stop()
                if e!=nil || !result.OK || result.Status==nil || !result.Status.Admin || result.Status.CurrentUID!=p.User.ID || len(result.Status.Accounts)!=accountCount {
                    client.Close();t.Fatal("published CLI authenticated RPC",round,sample,e,result.Error)
                }
                t.Logf("released_server_session=%d rpc=%d elapsed_ms=%d measured_data_rtt_ms=%d",round,sample,time.Since(begin).Milliseconds(),client.Probe().RTTMillis)
            }
            shutdown.Store(true)
            client.Close()
            continue
        }
'''+needle)
s=s.replace('"DNS": []string{"127.0.0.1"}, "Socks":', '"DNS": []string{"127.0.0.1"}, "DNSResolver": map[string]any{"primary":"127.0.0.83","backup":"127.0.0.84","itom_fallback":false}, "Socks":')
s=s.replace('run.Env = append(os.Environ(), "ITOM_NATIVE_TEST_STOP="', 'run.Env = append(os.Environ(), "ITOM_NATIVE_RELEASED_SERVER="+fmt.Sprint(scenario.released), "ITOM_NATIVE_TEST_STOP="')
s+='''
func TestPublishedServerCleanRestartAndAuthenticatedRPC(t *testing.T){testPublicEndpoints(t,publicEndpointCase{released:true,accounts:3,keywordBytes:32})}
func TestPublishedServerLegacyDebtAndAuthenticatedRPC(t *testing.T){testPublicEndpoints(t,publicEndpointCase{released:true,legacy:true,keywordBytes:32})}
func TestPublishedServerRetainedBacklogAndAuthenticatedRPC(t *testing.T){testPublicEndpoints(t,publicEndpointCase{released:true,accounts:3,retainedBacklog:128,keywordBytes:32})}
func TestPublishedServerWithAndroidRuntimeNativeAdministration(t *testing.T){testPublicEndpoints(t,publicEndpointCase{released:true,native:true,accounts:10,keywordBytes:32})}
'''
s+='''
func TestPublishedServerSourcesNativeDNSAndTLS(t *testing.T){testPublicEndpoints(t,publicEndpointCase{native:true,androidRuntime:true,accounts:10,keywordBytes:32})}
'''
core_overlay=ROOT/'endpoint_api_test.go' ;core_overlay.write_text(s)

native=SOURCE/'native/traffic_integration_test.go';s=native.read_text()
s=s.replace('"golang.org/x/net/proxy"','"golang.org/x/net/proxy"\n"itom.local/core/adminapi"\n"golang.org/x/net/dns/dnsmessage"\n"gvisor.dev/gvisor/pkg/tcpip/transport/udp"')
s=s.replace('raw, err := os.ReadFile(input)', 'releaseDNSFixture(t)\nraw, err := os.ReadFile(input)')
s=s.replace('TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol}', 'TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol,udp.NewProtocol}')
needle='\tfor _, mode := range []string{"socks", "tun"} {'
assert needle in s
s=s.replace(needle,'''
 var appIP [4]byte
 if os.Getenv("ITOM_RELEASE_VALIDATION")=="1" && os.Getenv("ITOM_NATIVE_RELEASED_SERVER")=="true" {
  var cfg config
  if e:=json.Unmarshal(raw,&cfg);e!=nil {t.Fatal(e)}
  for sample:=0;sample<6;sample++ {
   ctx,stop:=context.WithTimeout(context.Background(),1500*time.Millisecond)
   begin:=time.Now()
   stream,_,e:=c.pool.Open(ctx,wire.Connect{Host:adminapi.Host,Port:adminapi.Port})
   if e!=nil {stop();t.Fatal("released native admin OPEN",e)}
   request,_:=json.Marshal(adminapi.Request{Operation:"status"})
   result,e:=adminapi.Call(ctx,stream.(net.Conn),request,cfg.Profile.User.Key)
   stop()
   if e!=nil || !result.OK || result.Status==nil || !result.Status.Admin || len(result.Status.Accounts)!=10 {t.Fatal("released native admin RPC",e,result.Error)}
   t.Logf("released_server_native_admin_rpc=%d elapsed_ms=%d",sample,time.Since(begin).Milliseconds())
  }
  if c.pool.Probe().Samples==0 {t.Fatal("missing real DATA roundtrip")}
  if e:=os.WriteFile(os.Getenv("ITOM_NATIVE_TEST_STOP"),[]byte("intentional fixture shutdown"),0600);e!=nil {t.Fatal(e)}
  return
 }
 // Exercise the actual UDP DNS input path through the emulated TUN.
 dnsConn,e:=gonet.DialUDP(app,nil,&tcpip.FullAddress{NIC:1,Addr:tcpip.AddrFrom4([4]byte{172,19,0,2}),Port:53},ipv4.ProtocolNumber)
 if e!=nil {t.Fatal("native UDP DNS socket",e)}
 dnsConn.SetDeadline(time.Now().Add(1500*time.Millisecond))
 query,e:=dnsQuestion("example.com");if e!=nil {t.Fatal(e)}
 if _,e=dnsConn.Write(query);e!=nil {t.Fatal(e)}
 answer:=make([]byte,4096);n,e:=dnsConn.Read(answer);dnsConn.Close()
 if e!=nil {t.Fatal("native DNS TUN reply",e)}
 var response dnsmessage.Message
 if e=response.Unpack(answer[:n]);e!=nil || len(response.Answers)!=1 {t.Fatal("native DNS answer",e)}
 record,ok:=response.Answers[0].Body.(*dnsmessage.AResource)
 if !ok || !fakePrefix.Contains(netip.AddrFrom4(record.A)) {t.Fatal("native DNS did not retain domain association")}
 appIP=record.A
 t.Log("real UDP resolver and DNS-to-fake-IP TUN path passed")
'''+needle)
s=s.replace('"itom.local/core/adminapi"','"itom.local/core/adminapi"\n"itom.local/core/wire"\n"net/netip"')
s=s.replace('"203.0.113.80:443"','"example.com:443"')
s=s.replace('tcpip.AddrFrom4([4]byte{203, 0, 113, 80})','tcpip.AddrFrom4(appIP)')
s+='''
func releaseDNSFixture(t *testing.T) {
 for _,address:=range []string{"127.0.0.83:53","127.0.0.84:53"} {
  conn,err:=net.ListenPacket("udp4",address);if err!=nil {t.Fatal("local DNS fixture",err)}
  done:=make(chan struct{})
  go func(){defer close(done);for {
   raw:=make([]byte,4096);n,peer,e:=conn.ReadFrom(raw);if e!=nil {return}
   var query dnsmessage.Message
   if query.Unpack(raw[:n])!=nil || len(query.Questions)!=1 {continue}
   query.Response=true;query.RecursionAvailable=true
   query.Answers=[]dnsmessage.Resource{{Header:dnsmessage.ResourceHeader{Name:query.Questions[0].Name,Type:dnsmessage.TypeA,Class:dnsmessage.ClassINET,TTL:60},Body:&dnsmessage.AResource{A:[4]byte{203,0,113,80}}}}
   reply,e:=query.Pack();if e==nil {conn.WriteTo(reply,peer)}
  }}()
  t.Cleanup(func(){conn.Close();<-done})
 }
}
'''
native_overlay=ROOT/'traffic_integration_test.go';native_overlay.write_text(s)
subprocess.run(['gofmt','-w',str(core_overlay),str(native_overlay)],check=True)
overlay=ROOT/'overlay.json';overlay.write_text(json.dumps({'Replace':{str(core):str(core_overlay),str(native):str(native_overlay)}},indent=2)+'\n')
env=dict(os.environ);env['GOFLAGS']='-overlay='+str(overlay);env['GOTOOLCHAIN']='local';env['ITOM_RELEASE_SERVER']=str(server);env['ITOM_RELEASE_VALIDATION']='1'
command=['go','test','-race','-json','./transport','-run','^TestPublishedServer','-count=1','-timeout=300s']
trace=ROOT/('released-artifact-tests-'+str(time.time_ns())+'.jsonl')
with trace.open('w')as log:
 result=subprocess.run(command,cwd=SOURCE/'core',env=env,stdout=log,stderr=subprocess.STDOUT)
events=[json.loads(line)for line in trace.read_text().splitlines()if line.startswith('{')]
summary={'log_file':trace.name,'source_commit':SHA,'server_sha256':SERVER_SHA,'apk_sha256':APK_SHA,'published_server_executed':True,'android_apk_executed_on_device':False,'native_bridge_on_linux':True,'application_tls_uses_source_fixture_exit':True,'internal_admin_names_are_not_normal_dns_targets':True,'provider':'local TLS IMAP fixture','unchanged_request_deadlines':True,'exit_code':result.returncode,'tests':[{k:e[k]for k in ['Action','Test','Elapsed']if k in e}for e in events if e.get('Action')in ['pass','fail']and'Test'in e and'/'not in e['Test']]}
trace.with_suffix('.summary.json').write_text(json.dumps(summary,indent=2)+'\n')
(ROOT/'RESULTS.json').write_text(json.dumps(summary,indent=2)+'\n');print(json.dumps(summary,indent=2),flush=True)
sys.exit(result.returncode)

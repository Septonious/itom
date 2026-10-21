//go:build itomtest

package tunnel

import (
 "encoding/json"
 "os"
 "testing"
 "time"
)

type idlePathCounters struct {
 DataCommits, ControlCommits, DataCommitNS, ControlCommitNS, DataQueueNS, ControlQueueNS uint64
 FastDataWaitMS, FastPumpWaitMS, FastStripeWaitMS, FastGrantWaitMS, ControlWaitMS int64
 FetchPolls, EmptyFetchPolls, IdleWaits, IdleKicks, CleanupRuns int64
}

// Atomic store counters and diagnostic snapshots; never performs journal IO,
// changes flow state, or attributes summed concurrent work as critical-path time.
func idlePathObservation(f *productionSupervisorFixture) map[string]idlePathCounters {
 collect:=func(lanes []*lane) idlePathCounters {
  var total idlePathCounters
  for _,l:=range lanes {
   d,c:=l.data.CommitSnapshot(),l.control.CommitSnapshot()
   total.DataCommits+=d.Count;total.ControlCommits+=c.Count
   total.DataCommitNS+=d.CommitNS;total.ControlCommitNS+=c.CommitNS
   total.DataQueueNS+=d.QueueNS;total.ControlQueueNS+=c.QueueNS
   if l.diag!=nil {
    v:=l.diag.snapshot()
    total.FastDataWaitMS+=v.FastDataWaitMS;total.FastPumpWaitMS+=v.FastPumpQueueWaitMS
    total.FastStripeWaitMS+=v.FastStripeWaitMS;total.FastGrantWaitMS+=v.FastGrantSelectWaitMS
    total.ControlWaitMS+=v.ControlWaitMS;total.FetchPolls+=v.FetchPolls;total.EmptyFetchPolls+=v.EmptyFetchPolls
    total.IdleWaits+=v.IdleWaits;total.IdleKicks+=v.IdleKicks
    total.CleanupRuns+=v.CleanupRemoteRuns+v.CleanupLocalDataRuns+v.CleanupLocalControlRuns+v.CleanupPresenceRuns
   }
  }
  return total
 }
 f.client.mu.Lock();client:=make([]*lane,0,len(f.client.lanes));for _,l:=range f.client.lanes {client=append(client,l)};f.client.mu.Unlock()
 f.mu.Lock();server:=make([]*lane,0,len(f.serverLanes));for _,l:=range f.serverLanes {server=append(server,l)};f.mu.Unlock()
 return map[string]idlePathCounters{"client":collect(client),"server":collect(server)}
}

func TestIdlePathObservation(t *testing.T) {
 t.Setenv("ITOM_HEALTHY_SERVER","1");t.Setenv("ITOM_LOAD_COMMAND_DELAY_MS","0")
 t.Setenv("ITOM_LOAD_DIAGNOSTICS","1");t.Setenv("ITOM_LOAD_CLIENT_MBPS","500");t.Setenv("ITOM_LOAD_SERVER_MBPS","1000")
 t.Setenv("ITOM_LOAD_LITERAL_PLUS","1");t.Setenv("ITOM_LOAD_BINARY_FETCH","1")
 f:=optionalRetainedInteractiveFixture(t);c:=rev4Open(t,f);defer c.Close()
 for i:=0;i<4;i++ {if err:=rev4Exchange(c,i,32,3*time.Second);err!=nil {t.Fatal(err)}}
 for i:=0;i<64;i++ {
  delay:=150*time.Millisecond;if i%8==0 {delay=800*time.Millisecond};time.Sleep(delay)
  before:=idlePathObservation(f);directStart:=f.network.mobile.profile.Load().RTT.Milliseconds()
  at:=time.Now();err:=rev4Exchange(c,i,32,3*time.Second);elapsed:=time.Since(at)
  directEnd:=f.network.mobile.profile.Load().RTT.Milliseconds();after:=idlePathObservation(f)
  b,e:=json.Marshal(map[string]any{"index":i,"variant":os.Getenv("ITOM_EXPERIMENT_LABEL"),"retained":os.Getenv("ITOM_V110_RETAINED_IDLE"),"rtt_ms":float64(elapsed)/float64(time.Millisecond),"client_rtt_start_ms":directStart,"client_rtt_end_ms":directEnd,"server_rtt_ms":loadEnvInt(t,"ITOM_LOAD_SERVER_RTT_MS",20,0,1000),"before":before,"after":after,"counters_are_aggregate_not_critical_path":true})
  if e!=nil {t.Fatal(e)};t.Log("IDLE_PATH_OBSERVATION "+string(b));if err!=nil {t.Fatal(err)}
 }
 if f.targetDials.Load()!=1 || f.carrierExits.unexpected.Load()!=0 {t.Fatal("observation replaced sockets/carriers")}
}

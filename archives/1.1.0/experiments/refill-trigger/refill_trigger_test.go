package tunnel
import (
 "context"
 "path/filepath"
 "testing"
 "time"
 "itom.local/core/journal"
 "itom.local/core/mailbox"
 "itom.local/core/pool"
 "itom.local/core/servercfg"
 "itom.local/core/wire"
)
func TestRefillTriggerCannotCreatePermissionOrRefundDebt(t *testing.T) {
 a:=servercfg.Account{ID:"a01",Mail:mailbox.Config{Host:"mail.example"}};u:=servercfg.User{ID:"12345678",Up:"up",Down:"down"}
 s,err:=journal.Open(filepath.Join(t.TempDir(),"data.db"),ledgerConfig(a,u,dataProfile8),time.Second);if err!=nil {t.Fatal(err)};defer s.Close()
 if err=s.EnableAfterInventory(a.ID,u.ID,s.Run());err!=nil {t.Fatal(err)}
 l:=&lane{ctx:context.Background(),side:true,account:a,user:u,data:s,cipher:cleanupFixtureEpoch(t),stripe:newModelStripeGroup(wire.ID{1},1),cleanupWake:make(chan struct{},1),issued:map[wire.QuotaKey]bool{},issuedFast:map[wire.QuotaKey]bool{}}
 now:=time.Now();later:=now.Add(time.Hour);l.nextRemoteCleanup=later
 l.requestRefillCleanup(now);if l.nextRemoteCleanup!=later {t.Fatal("fresh profile changed periodic scheduling")}
 old,err:=s.IssueRemote(journal.Scope{Account:a.ID,User:u.ID,Session:wire.ID{99}},pool.Amount{Bytes:3<<20,Messages:32});if err!=nil {t.Fatal(err)}
 before,err:=s.Held(a.ID,u.ID);if err!=nil {t.Fatal(err)}
 l.requestRefillCleanup(now);if l.nextRemoteCleanup!=now {t.Fatal("empty retained bulk permission did not request proof service")}
 if !l.refillCleanupRequested {t.Fatal("proof-service event was not retained")}
 select {case <-l.cleanupWake:default:t.Fatal("actor was not signaled")}
 for i:=uint64(1);i<=remoteBulkLeases;i++ {l.issued[wire.QuotaKey{Grant:i}]=true}
 l.nextRemoteCleanup=later;l.requestRefillCleanup(now);if l.nextRemoteCleanup!=later {t.Fatal("usable bulk window accelerated cleanup")}
 clear(l.issued);l.stripe=newStripeGroup(wire.ID{1},1);l.requestRefillCleanup(now);if l.nextRemoteCleanup!=later {t.Fatal("legacy controller changed")}
 after,err:=s.Held(a.ID,u.ID);if err!=nil || before!=after {t.Fatal("scheduling changed physical exposure",before,after,err)}
 grant,err:=s.GetGrant(old.Ref);if err!=nil || grant!=old {t.Fatal("scheduling changed grant authority",err)}
 if err=s.Audit();err!=nil {t.Fatal(err)}
}

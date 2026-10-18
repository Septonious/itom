package tunnel

import (
  "testing"

  "itom.local/core/wire"
)

func TestRC8DescriptorCacheFitsPhysicalInventoryAndRetainedCleanup(t *testing.T) {
  if dataDescriptorCacheLimit < 2*192 {
    t.Fatalf("valid physical descriptor burst exceeds cache: cap=%d physical_per_epoch=192", dataDescriptorCacheLimit)
  }
  // Immutable hashed descriptor identity must remain available until BODY
  // verification / exact GC; no eviction of valid authenticated reports.
  l:=&lane{reports: make(map[[32]byte]wire.UsageReport)}
  for i:=0;i<2*192;i++{
    k:=[32]byte{byte(i),byte(i>>8)}
    l.reports[k]=wire.UsageReport{}
  }
  if len(l.reports)>=dataDescriptorCacheLimit {
    t.Fatal("two inventories already exhausted bounded cache")
  }
  if l.evictOldestBadDataProof(){
    t.Fatal("evicted authenticated descriptor as if invalid proof")
  }
  if len(l.reports)!=2*192{
    t.Fatal("descriptor cache dropped valid messages without proof")
  }
}

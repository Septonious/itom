//go:build itomtest

package tunnel

import (
 "os"
 "testing"
)

func optionalRetainedInteractiveFixture(t *testing.T) *productionSupervisorFixture {
 if os.Getenv("ITOM_V110_RETAINED_IDLE")=="1" {
  return newProductionSupervisorFixtureState(t,10,rev4RetainedState(t))
 }
 return newProductionSupervisorFixtureAccounts(t,10)
}

// Exactly the same persistent sockets,24 idle and24 lightly loaded probes,
// deadlines and verified payloads as the fresh-profile reference workload.
func TestRetainedIdleLightReference(t *testing.T) {
 t.Setenv("ITOM_V110_RETAINED_IDLE","1")
 TestV110IdleLightReference(t)
}

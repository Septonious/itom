package tunnel

import (
	"itom.local/core/datapath"
	"itom.local/core/wire"
	"math"
	"testing"
	"time"
)

type mobileProtocolCeiling struct {
	Wire, Upload, Duplex float64
	Writers, AppendTurns int
}

// Optimistic upper bounds, never an achievable-rate promise. MIME folds every
// 76 base64 characters with CRLF, and the writer waits for APPEND completion.
// Loss, encryption, provider service, receipts and physical GC only reduce this.
func mobileCapacityCeiling(capMbps float64, baseline time.Duration, working, owners int, literalPlus bool) (mobileProtocolCeiling, bool) {
	if capMbps < 0 || math.IsNaN(capMbps) || math.IsInf(capMbps, 0) || baseline <= 0 || baseline > time.Second || working < 1 || working > 10 || owners < 1 || owners > 8 {
		return mobileProtocolCeiling{}, false
	}
	wireUpper := math.Inf(1)
	if capMbps > 0 {
		wireUpper = capMbps * 3 / 4 * 76 / 78
	}
	writers := min(working*accountDataWorkers, owners*8) // fixed stripe Pump worker bound
	turns := 2
	if literalPlus {
		turns = 1
	}
	// A collector crosses its target by at most one full DATA record. This
	// overestimates payload, so framing/Noise/MIME cannot make the bound too low.
	payload := datapath.BulkBatchTargetBytes + wire.MaxData
	uploadUpper := float64(writers*payload*8) / (time.Duration(turns) * baseline).Seconds() / 1e6
	return mobileProtocolCeiling{Wire: wireUpper, Upload: uploadUpper, Duplex: min(wireUpper, uploadUpper), Writers: writers, AppendTurns: turns}, true
}

func TestMobileProtocolCeilingCannotPromiseImpossibleTarget(t *testing.T) {
	minus, ok := mobileCapacityCeiling(600, 300*time.Millisecond, 10, 4, false)
	if !ok || minus.Writers != 20 || minus.AppendTurns != 2 || minus.Duplex < 100 || minus.Duplex >= 101 || minus.Duplex >= 200 {
		t.Fatal("LITERAL- ceiling", minus, ok)
	}
	plus, _ := mobileCapacityCeiling(600, 300*time.Millisecond, 10, 4, true)
	if plus.Duplex != 2*minus.Duplex || plus.AppendTurns != 1 {
		t.Fatal("LITERAL+ ceiling", plus)
	}
	narrow, _ := mobileCapacityCeiling(50, 100*time.Millisecond, 10, 4, true)
	if narrow.Duplex < 36 || narrow.Duplex >= 37 || narrow.Duplex >= 60 {
		t.Fatal("wire ceiling", narrow)
	}
	oneOwner, _ := mobileCapacityCeiling(600, 300*time.Millisecond, 10, 1, false)
	if oneOwner.Writers != 8 || oneOwner.Duplex >= minus.Duplex {
		t.Fatal("owner concurrency bound", oneOwner)
	}
	for _, cap := range []float64{-1, math.NaN(), math.Inf(1)} {
		if _, ok := mobileCapacityCeiling(cap, time.Millisecond, 10, 4, false); ok {
			t.Fatal("invalid capacity accepted")
		}
	}
}

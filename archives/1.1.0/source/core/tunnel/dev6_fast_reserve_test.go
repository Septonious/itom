package tunnel

import (
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/wire"
	"testing"
)

func TestDEV6FastReserveFitsCollectorAndPreservesFreshProfile(t *testing.T) {
	// Include a complete overshooting final record and the DATA envelope.
	body := 24 + 4*(4+2*wire.HeaderSize+wire.FrameHeaderSize+wire.TagSize) + (64 << 10) + wire.MaxData + 512
	encoded, err := mailbox.EncodedSize(body)
	if err != nil || encoded > 160<<10 {
		t.Fatal("reduced fast floor cannot fit collector output", encoded, err)
	}
	if reserve := recoveredFastReserveBytes(dataProfile12, journal.OriginUsage{}); reserve != localFastReserveBytes {
		t.Fatal("fresh account policy changed", reserve)
	}
	// Insufficient space for both maximum bulk BODYs and both fast cushions
	// must not fabricate a smaller legal bulk size or readiness.
	u := journal.OriginUsage{HistoricalRemote: pool.Amount{Bytes: dataProfile12.Bytes - 2*(dataBatchMIMEHardCap+160<<10) + 1, Messages: 165}}
	if reserve := recoveredFastReserveBytes(dataProfile12, u); reserve != localFastReserveBytes {
		t.Fatal("unsafe partial duplex floor admitted", reserve)
	}
}

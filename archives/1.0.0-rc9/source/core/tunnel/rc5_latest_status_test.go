package tunnel

import (
    "context"
    "testing"
    "time"

    "itom.local/core/wire"
)

// Coalescing must preserve original queue age while accurately exposing the
// freshness of the latest still-unsent authenticated cumulative STATUS.
// Diagnostic timestamps cannot authorize sending/receiving or refunding debt.
func TestRC5LatestStatusAgeIndependentFromOriginalQueueAge(t *testing.T) {
    l := &lane{controlQueue: make(chan queuedControl, 8), urgentQueue: make(chan queuedControl, 8)}
    a, err := wire.EncodeControl(7, wire.Progress{
        Receipt: 128 << 10, MaxStream: runtimeStreamWindow + (128 << 10),
        MaxData: runtimeReceiveBytes + (128 << 10)})
    if err != nil {t.Fatal(err)}
    b, err := wire.EncodeControl(7, wire.Progress{
        Receipt: 256 << 10, MaxStream: runtimeStreamWindow + (256 << 10),
        MaxData: runtimeReceiveBytes + (256 << 10)})
    if err != nil {t.Fatal(err)}
    now := time.Now().Add(-3*time.Second)
    if err := l.enqueueFlowControl(context.Background(), queuedControl{Frame:a,added:now,latestAt:now});err!=nil {t.Fatal(err)}
    first := l.flowControlLatest[flowControlKey{a.Type,a.Stream}]
    if err := l.enqueueFlowControl(context.Background(), queuedControl{Frame:b,added:time.Now(),latestAt:time.Now()});err!=nil {t.Fatal(err)}
    latest := l.flowControlLatest[flowControlKey{a.Type,a.Stream}]
    if !latest.added.Equal(first.added) {t.Fatal("original queue token birth was reset")}
    if latest.latestAt.Before(time.Now().Add(-time.Second)) {t.Fatal("new STATUS freshness was not retained")}
    q := <-l.controlQueue
    result := l.resolveFlowControl(q)
    if !result.latestAt.Equal(latest.latestAt) || !result.added.Equal(first.added) {t.Fatal("resolution altered cumulative state timestamps")}
    // Second coalescing stage in the physical writer updates the latest
    // timestamp while retaining its older original token timestamp.
    merged := l.appendControlBatch([]queuedControl{{Frame:a,added:now,latestAt:now}},
        queuedControl{Frame:b,added:first.added,latestAt:latest.latestAt},3)
    if len(merged)!=1 || !merged[0].latestAt.Equal(latest.latestAt) {t.Fatal("physical batch lost newest STATUS timestamp")}
    payload,err := wire.DecodeControl(merged[0].Frame)
    if err!=nil {t.Fatal(err)}
    progress,ok:=payload.(wire.Progress)
    if !ok || progress.Receipt!=256<<10 {t.Fatal("authenticated cumulative progress was not preserved")}
}

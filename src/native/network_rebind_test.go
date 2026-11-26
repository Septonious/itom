package mobile

import (
 "context"
 "errors"
 "sync/atomic"
 "testing"
 "time"
)

type rebindFake struct {
 transportPool
 closes atomic.Int32
}
func (p *rebindFake) Close() error {p.closes.Add(1);return nil}
func (p *rebindFake) RecordNetworkEvent(string,int64){}

func waitUntilRebind(t *testing.T,ready func()bool) {
 t.Helper()
 deadline:=time.Now().Add(6*time.Second)
 for !ready() {
  if time.Now().After(deadline){t.Fatal("rebind condition did not become true")}
  time.Sleep(10*time.Millisecond)
 }
}

func TestRebindCoalescesAndReplacesTransport(t *testing.T){
 ctx,cancel:=context.WithCancel(context.Background())
 old,fresh:=&rebindFake{},&rebindFake{}
 var starts atomic.Int32
 c:=&Client{ctx:ctx,cancel:cancel,pool:old}
 c.startTransport=func(context.Context)(transportPool,error){
  if old.closes.Load()!=1{t.Error("old generation not closed before reopening")}
  starts.Add(1)
  return fresh,nil
 }
 c.startNetworkRebind()
 defer c.Close()
 for i:=0;i<20;i++{c.RebindNetwork()}
 waitUntilRebind(t,func()bool{return c.currentPool()==fresh&&!c.rebinding.Load()})
 if starts.Load()!=1||old.closes.Load()!=1{t.Fatal("duplicate generation")}
 c.Close()
 if fresh.closes.Load()!=1{t.Fatal("replacement not closed exactly once")}
}

func TestRebindRetriesWithoutOldTransport(t *testing.T){
 ctx,cancel:=context.WithCancel(context.Background())
 old,fresh:=&rebindFake{},&rebindFake{}
 var starts atomic.Int32
 c:=&Client{ctx:ctx,cancel:cancel,pool:old}
 c.startTransport=func(context.Context)(transportPool,error){
  if c.currentPool()!=nil{t.Error("transport visible during login")}
  if starts.Add(1)==1{return nil,errors.New("transient")}
  return fresh,nil
 }
 c.startNetworkRebind()
 defer c.Close()
 c.RebindNetwork()
 waitUntilRebind(t,func()bool{return c.currentPool()==fresh&&!c.rebinding.Load()})
 if starts.Load()!=2||c.rebindFailure.Load()!=nil{t.Fatal("retry did not recover")}
}

func TestCloseCancelsInFlightRebind(t *testing.T){
 ctx,cancel:=context.WithCancel(context.Background())
 old,fresh:=&rebindFake{},&rebindFake{}
 started:=make(chan struct{})
 c:=&Client{ctx:ctx,cancel:cancel,pool:old}
 c.startTransport=func(ctx context.Context)(transportPool,error){
  close(started)
  <-ctx.Done()
  return fresh,nil // simulate a late successful dial after cancellation
 }
 c.startNetworkRebind()
 c.RebindNetwork()
 select{case <-started:case <-time.After(3*time.Second):t.Fatal("login not attempted")}
 c.Close()
 if c.currentPool()!=nil||old.closes.Load()!=1||fresh.closes.Load()!=1{t.Fatal("pool leak after close")}
 c.RebindNetwork()
 if c.rebinding.Load(){t.Fatal("closed client started rebind")}
}

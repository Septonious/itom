package mobile

import (
 "context"
 "errors"
 "net/netip"
 "time"

 "itom.local/core/socks"
 "itom.local/core/tunnel"
 "itom.local/core/wire"
)

// The pool slot owns one transport generation. Taking a snapshot of the
// pointer never holds a lock over provider I/O or waits for the old session.
type transportPool interface {
 Close() error
 Stats() tunnel.Stats
 Snapshot() tunnel.ClientSnapshot
 Failure() string
 Diagnostics() string
 Probe() tunnel.ProbeSnapshot
 Open(context.Context, wire.Connect) (socks.Stream, netip.AddrPort, error)
 PingAccount(context.Context,string) (time.Duration,error)
 RecordNetworkEvent(string,int64)
 RecordApplicationCopyEvent(uint64,string,int64,error,string)
}

var _ transportPool = (*tunnel.Client)(nil)

func (c *Client) currentPool() transportPool {
 c.poolMu.RLock()
 defer c.poolMu.RUnlock()
 return c.pool
}
func (c *Client) swapPool(next transportPool) transportPool {
 c.poolMu.Lock()
 defer c.poolMu.Unlock()
 old:=c.pool
 c.pool=next
 return old
}

// RebindNetwork requests a fresh provider generation on Android's newly
// selected underlying network. Host.Protect must protect AND bind every new
// socket to that network. This method returns immediately to the caller.
// Existing TUN, SOCKS listener, routing policy and fake-IP database survive;
// established tunneled TCP flows cannot survive the fresh Noise session.
// Never fall back to direct for a VPN verdict during the outage.
func (c *Client) RebindNetwork() {
 c.mu.Lock()
 defer c.mu.Unlock()
 if c.closed || c.rebindRequests==nil {return}
 c.rebinding.Store(true)
 c.rebindFailure.Store(nil)
 select {
 case c.rebindRequests<-struct{}{}:
 default: // already queued
 }
}

func (c *Client) startNetworkRebind() {
 c.rebindRequests=make(chan struct{},1)
 c.wg.Add(1)
 go func(){
  defer c.wg.Done()
  defer c.rebinding.Store(false)
  for {
   select {
   case <-c.ctx.Done():return
   case <-c.rebindRequests:
   }
   // Collapse the common burst of Android available/capabilities callbacks.
   if !waitRebind(c.ctx,250*time.Millisecond){return}
   select{case <-c.rebindRequests:default:}
   if c.ctx.Err()!=nil{return}
   err:=c.reconnectTransport()
   c.mu.Lock()
   // Another network change received during login must be serviced rather
   // than incorrectly marking the new network as ready.
   if len(c.rebindRequests)==0 {
    c.rebinding.Store(false)
    if err!=nil && c.ctx.Err()==nil {
     reason:="NETWORK_REBIND_FAILED";c.rebindFailure.Store(&reason)
    }
   }
   c.mu.Unlock()
  }
 }()
}

func waitRebind(ctx context.Context,delay time.Duration) bool {
 timer:=time.NewTimer(delay)
 defer timer.Stop()
 select {case <-ctx.Done():return false;case <-timer.C:return true}
}

func (c *Client) reconnectTransport() error {
 // The old transport must fully close before reopening its on-disk retention
 // store or reusing provider capacity. Each pool is closed by exactly one owner.
 old:=c.swapPool(nil)
 if old!=nil {
  old.RecordNetworkEvent("NETWORK_REBIND_BEGIN",0)
  _=old.Close()
 }
 if c.startTransport==nil{return errors.New("NETWORK_REBIND_UNAVAILABLE")}
 var last error
 for attempt:=0;attempt<3;attempt++ {
  if c.ctx.Err()!=nil{return c.ctx.Err()}
  if attempt>0 && !waitRebind(c.ctx,time.Second*time.Duration(attempt)){return c.ctx.Err()}
  fresh,err:=c.startTransport(c.ctx)
  if err!=nil {last=err;continue}
  if fresh==nil {last=errors.New("NETWORK_REBIND_EMPTY");continue}
  c.mu.Lock()
  if c.closed||c.ctx.Err()!=nil {
   c.mu.Unlock()
   _=fresh.Close()
   return context.Canceled
  }
  c.swapPool(fresh)
  c.rebindFailure.Store(nil)
  c.mu.Unlock()
  fresh.RecordNetworkEvent("NETWORK_REBIND_READY",0)
  if kind:=c.networkType.Load();kind!=nil{c.RecordNetworkEvent("NETWORK_BEARER_RESTORED",0)}
  return nil
 }
 return last
}

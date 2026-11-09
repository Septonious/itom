package mailstream

import (
	"context"
	"testing"
	"time"
)

func TestRC117HedgePrefersAuthenticDeliveryRTTWithoutPinningOneAccount(t *testing.T) {
	link:=&IMAPLink{}
	var allowed [20]bool
	now:=time.Duration(time.Now().UnixNano())
	for i,ms:=range []int{180,165,310,190,170,185,390,175,180,210,178,195,190,320,200}{
		a:=&mailAccountState{ready:true,rtt:time.Duration(ms)*time.Millisecond,rttAt:now,fast:make(chan mailWork,16)}
		link.accounts=append(link.accounts,a)
		allowed[i]=true
	}
	// The existing first copy is at index 0. Its slow neighbor (index 2)
	// must not automatically become the second hedge when measured to be slow.
	excluded:=link.accounts[0]
	for start:=0; start<len(link.accounts);start++ {
		order:=link.interactiveHedgeAccountOrder(start,allowed,excluded)
		if len(order)!=15 {t.Fatalf("missing fallback accounts: %v",order)}
		seen:=map[int]bool{}
		for _,idx:=range order {
			if seen[idx] {t.Fatalf("repeated physical account: %v",order)}
			seen[idx]=true
		}
		if order[0]==0 || order[0]==2 || order[0]==6 || order[0]==13 {
			t.Fatalf("slower or excluded account led a hedge: %v",order)
		}
	}
	// Large fast-queue pressure must not lure an interactive hedge.
	for i:=0;i<8;i++ {link.accounts[1].fast<-mailWork{}}
	order:=link.interactiveHedgeAccountOrder(1,allowed,excluded)
	if order[0]==1 {t.Fatal("queued fast account incorrectly preferred")}
}

func TestRC117HedgeFallsBackToRoundRobinWhenRTTUntrained(t *testing.T) {
	link:=&IMAPLink{}
	var allowed [20]bool
	for i:=0;i<6;i++ {
		link.accounts=append(link.accounts,&mailAccountState{ready:true})
		allowed[i]=true
	}
	want:=[]int{3,4,5,0,1,2}
	got:=link.interactiveHedgeAccountOrder(3,allowed,link.accounts[3])
	for i:=range want {
		if got[i]!=want[i] {t.Fatalf("unknown RTT must preserve fair RR: %v",got)}
	}
	for i:=0;i<6;i++ {
		a:=link.accounts[i]
		a.rttAt=time.Duration(time.Now().Add(-time.Minute).UnixNano())
		a.rtt=10*time.Millisecond
	}
	got=link.interactiveHedgeAccountOrder(3,allowed,link.accounts[3])
	for i:=range want {
		if got[i]!=want[i] {t.Fatalf("stale RTT must preserve fair RR: %v",got)}
	}
}

func TestRC117RTTPerAccountEWMAOnlyUsesAuthenticatedFirstReceipt(t *testing.T) {
	id:=SessionID{1}
	a:=&mailAccountState{}
	l:=&IMAPLink{ctx:context.Background(),accounts:[]*mailAccountState{a}}
	a.delivery[1]=deliverySample{id:id,sequence:1,started:time.Now().Add(-120*time.Millisecond)}
	if err:=l.Acknowledge(id,1);err!=nil {t.Fatal(err)}
	previous:=a.rtt
	if previous <100*time.Millisecond || previous>250*time.Millisecond {
		t.Fatalf("initial measured RTT=%v",previous)
	}
	a.delivery[2]=deliverySample{id:id,sequence:2,started:time.Now().Add(-500*time.Millisecond)}
	if err:=l.Acknowledge(id,2);err!=nil {t.Fatal(err)}
	if a.rtt <= previous || a.rtt >= 300*time.Millisecond {
		t.Fatalf("outlier should contribute 1/8, old=%v new=%v",previous,a.rtt)
	}
	before:=a.rtt
	a.delivery[3]=deliverySample{id:id,sequence:3,started:time.Now().Add(-time.Second),retried:true}
	if err:=l.Acknowledge(id,3);err!=nil {t.Fatal(err)}
	if a.rtt!=before {t.Fatal("retransmitted packet must not train hedging RTT")}
}

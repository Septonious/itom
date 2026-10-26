package transport_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"itom.local/core/transport"
)

func expectFetch(t *testing.T, r *bufio.Reader, uid uint32, items string) string {
	t.Helper()
	line := readLine(t, r)
	if !strings.Contains(line, fmt.Sprintf(" UID FETCH %d:%d (%s)\r\n", uid, uid, items)) {
		t.Errorf("wrong FETCH turn: %q", line)
	}
	return tag(line)
}

func TestDisjointBodyAndFlagsCompleteIndependentlyWithoutExtraSocket(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	x := selectedPeer(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		bodyTag := expectFetch(t, r, 2, "UID BODY.PEEK[]")
		fmt.Fprint(c, "* 2 FETCH (UID 2 BODY[] {4}\r\ndata)\r\n")
		flagsTag := expectFetch(t, r, 1, "UID FLAGS")
		fmt.Fprintf(c, "* 1 FETCH (UID 1 FLAGS (receipt))\r\n%s OK flags\r\n", flagsTag)
		line := readLine(t, r)
		if !strings.HasSuffix(line, " NOOP\r\n") {
			t.Error("unrelated command lost progress", line)
		}
		fmt.Fprintf(c, "%s OK alive\r\n", tag(line))
		<-release
		fmt.Fprintf(c, "%s OK body\r\n", bodyTag)
		io.Copy(io.Discard, r)
	})
	body, err := transport.QueuedBodyForTest(context.Background(), x, 2)
	if err != nil {
		t.Fatal(err)
	}
	flags, err := transport.QueuedFlagsForTest(context.Background(), x, 1)
	if err != nil {
		t.Fatal(err)
	}
	got, err := wait(t, flags)
	if err != nil || len(got) != 1 || got[0].UID != 1 || len(got[0].Flags) != 1 || got[0].Flags[0] != "receipt" {
		t.Fatal(got, err)
	}
	detached, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := body.Wait(detached); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := x.Fetch(context.Background(), 7, 7); !errors.Is(err, transport.ErrCapacity) {
		t.Fatal("public FETCH bypassed a live tag", err)
	}
	cmd, err := x.Noop(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wait(t, cmd); err != nil {
		t.Fatal(err)
	}
	if s := x.Snapshot(); s.PendingTags != 1 || !s.FetchActive || s.FlagsFetchActive || s.ConcurrentFetchPairs != 1 || s.FetchResponseBytes <= 0 {
		t.Fatal("wrong independent lifetime", s)
	}
	once.Do(func() { close(release) })
	got, err = wait(t, body)
	if err != nil || len(got) != 1 || got[0].UID != 2 || string(got[0].Bytes) != "data" {
		t.Fatal(got, err)
	}
	if s := x.Snapshot(); s.FetchResponseBytes != 0 || s.FetchActive {
		t.Fatal("response reservation leaked", s)
	}
}

func TestSameUIDBodyAndFlagsSerializeInBothOrders(t *testing.T) {
	for _, flagsFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("flags_first_%t", flagsFirst), func(t *testing.T) {
			x := selectedPeer(t, func(c net.Conn) {
				r := bufio.NewReader(c)
				first := readLine(t, r)
				line := readLine(t, r)
				if !strings.HasSuffix(line, " NOOP\r\n") {
					t.Error("overlapping UID query reached the wire before its tag", line)
				}
				fmt.Fprintf(c, "%s OK independent\r\n%s OK first\r\n", tag(line), tag(first))
				second := readLine(t, r)
				if flagsFirst {
					fmt.Fprintf(c, "* 1 FETCH (UID 2 BODY[] {1}\r\nx)\r\n%s OK body\r\n", tag(second))
				} else {
					fmt.Fprintf(c, "* 1 FETCH (UID 2 FLAGS ())\r\n%s OK flags\r\n", tag(second))
				}
				io.Copy(io.Discard, r)
			})
			first, second := transport.QueuedBodyForTest, transport.QueuedFlagsForTest
			if flagsFirst {
				first, second = second, first
			}
			one, err := first(context.Background(), x, 2)
			if err != nil {
				t.Fatal(err)
			}
			type result struct {
				cmd *transport.Command
				err error
			}
			ready := make(chan result, 1)
			go func() { c, e := second(context.Background(), x, 2); ready <- result{c, e} }()
			awaitReclamation(t, func() bool { return x.Snapshot().FetchWaiters == 1 })
			noop, err := x.Noop(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := wait(t, noop); err != nil {
				t.Fatal(err)
			}
			if _, err := wait(t, one); err != nil {
				t.Fatal(err)
			}
			var two result
			select {
			case two = <-ready:
			case <-time.After(2 * time.Second):
				t.Fatal("same UID successor stranded")
			}
			if two.err != nil {
				t.Fatal(two.err)
			}
			if _, err := wait(t, two.cmd); err != nil {
				t.Fatal(err)
			}
			if x.Snapshot().ConcurrentFetchPairs != 0 {
				t.Fatal("same UID was coalesced across operations")
			}
		})
	}
}

func TestPipelinedFetchesShareOneAggregateResponseBudget(t *testing.T) {
	limits := transport.DefaultIMAPLimits()
	limits.LineBytes, limits.LiteralBytes, limits.ResponseBytes = 128, 128, 4096
	x := selectedAccountPeerWithFeatures(t, "account1", "fast", limits, "", false, func(c net.Conn) {
		r := bufio.NewReader(c)
		expectFetch(t, r, 2, "UID BODY.PEEK[]")
		expectFetch(t, r, 1, "UID FLAGS")
		flagLine := fmt.Sprintf("* 1 FETCH (UID 1 FLAGS (%s))\r\n", strings.Repeat("x", 40))
		bodyCost := len("* 2 FETCH (UID 2 BODY[] {64}") + 64 + 1
		repeats := (limits.ResponseBytes-bodyCost)/len(flagLine) + 1
		// Every individual response remains within its original bound. A
		// provider flooding repeated updates cannot grow their combined cache.
		if repeats*len(flagLine) >= limits.ResponseBytes || repeats*len(flagLine)+bodyCost <= limits.ResponseBytes {
			t.Error("invalid explicit aggregate-overflow fixture")
			return
		}
		for range repeats {
			fmt.Fprint(c, flagLine)
		}
		fmt.Fprintf(c, "* 2 FETCH (UID 2 BODY[] {64}\r\n%s)\r\n", strings.Repeat("b", 64))
		io.Copy(io.Discard, r)
	})
	body, err := transport.QueuedBodyForTest(context.Background(), x, 2)
	if err != nil {
		t.Fatal(err)
	}
	flags, err := transport.QueuedFlagsForTest(context.Background(), x, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []*transport.Command{body, flags} {
		if _, err := wait(t, cmd); !errors.Is(err, transport.ErrCapacity) {
			t.Fatal("parallel responses grew the original memory bound", err)
		}
	}
	if s := x.Snapshot(); !s.Closed || s.FetchResponseBytes != 0 || s.PendingTags != 0 {
		t.Fatal(s)
	}
}

func TestPipelinedMissingTagsExpireAndWakeQueuedInventory(t *testing.T) {
	limits := transport.DefaultIMAPLimits()
	limits.ProgressTimeout = 300 * time.Millisecond
	x := selectedAccountPeerWithFeatures(t, "account1", "fast", limits, "", false, func(c net.Conn) {
		r := bufio.NewReader(c)
		expectFetch(t, r, 2, "UID BODY.PEEK[]")
		expectFetch(t, r, 1, "UID FLAGS")
		io.Copy(io.Discard, r)
	})
	body, err := transport.QueuedBodyForTest(context.Background(), x, 2)
	if err != nil {
		t.Fatal(err)
	}
	flags, err := transport.QueuedFlagsForTest(context.Background(), x, 1)
	if err != nil {
		t.Fatal(err)
	}
	queued := make(chan error, 1)
	go func() { _, err := transport.QueuedAbsenceForTest(context.Background(), x, 9); queued <- err }()
	for _, cmd := range []*transport.Command{body, flags} {
		if _, err := wait(t, cmd); !errors.Is(err, transport.ErrUnknown) {
			t.Fatal("missing completion became safe replay permission", err)
		}
	}
	select {
	case err := <-queued:
		if !errors.Is(err, transport.ErrClosed) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued inventory survived failed carrier")
	}
	if s := x.Snapshot(); s.PendingTags != 0 || s.FetchActive || s.FlagsFetchActive || s.FetchResponseBytes != 0 {
		t.Fatal(s)
	}
}

func TestBodyCompletionDoesNotReleaseAnUnfinishedFlagsTurn(t *testing.T) {
	x := selectedPeer(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		flagTag := expectFetch(t, r, 1, "UID FLAGS")
		bodyTag := expectFetch(t, r, 2, "UID BODY.PEEK[]")
		fmt.Fprintf(c, "* 2 FETCH (UID 2 BODY[] {1}\r\na)\r\n%s OK first body\r\n", bodyTag)
		// A new disjoint BODY may use only the released BODY slot. The flag
		// result/tag can legally arrive after both BODY completions.
		nextTag := expectFetch(t, r, 3, "UID BODY.PEEK[]")
		fmt.Fprintf(c, "* 3 FETCH (UID 3 BODY[] {1}\r\nb)\r\n%s OK next body\r\n* 1 FETCH (UID 1 FLAGS ())\r\n%s OK flags\r\n", nextTag, flagTag)
		io.Copy(io.Discard, r)
	})
	flags, err := transport.QueuedFlagsForTest(context.Background(), x, 1)
	if err != nil {
		t.Fatal(err)
	}
	body, err := transport.QueuedBodyForTest(context.Background(), x, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := wait(t, body); err != nil || len(got) != 1 || string(got[0].Bytes) != "a" {
		t.Fatal(got, err)
	}
	if s := x.Snapshot(); !s.FlagsFetchActive || s.PendingTags != 1 {
		t.Fatal("BODY tag retired FLAGS", s)
	}
	next, err := transport.QueuedBodyForTest(context.Background(), x, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := wait(t, next); err != nil || len(got) != 1 || string(got[0].Bytes) != "b" {
		t.Fatal(got, err)
	}
	if got, err := wait(t, flags); err != nil || len(got) != 1 || got[0].UID != 1 {
		t.Fatal(got, err)
	}
	if s := x.Snapshot(); s.FetchResponseBytes != 0 || s.FetchActive || s.ConcurrentFetchPairs != 2 {
		t.Fatal(s)
	}
}

func TestExclusiveInventoryCannotBeBypassedAndCancellationUnblocksSuccessor(t *testing.T) {
	for _, cancelInventory := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel_inventory_%t", cancelInventory), func(t *testing.T) {
			bodyRelease, flagsRelease := make(chan struct{}), make(chan struct{})
			var bodyOnce, flagsOnce sync.Once
			defer bodyOnce.Do(func() { close(bodyRelease) })
			defer flagsOnce.Do(func() { close(flagsRelease) })
			x := selectedPeer(t, func(c net.Conn) {
				r := bufio.NewReader(c)
				bodyTag := expectFetch(t, r, 2, "UID BODY.PEEK[]")
				flagsTag := expectFetch(t, r, 1, "UID FLAGS")
				fmt.Fprint(c, "* 2 FETCH (UID 2 BODY[] {1}\r\nx)\r\n* 1 FETCH (UID 1 FLAGS ())\r\n")
				<-bodyRelease
				fmt.Fprintf(c, "%s OK body\r\n", bodyTag)
				noop := readLine(t, r)
				if !strings.HasSuffix(noop, " NOOP\r\n") {
					t.Error("inventory or successor bypassed a live flags tag", noop)
				}
				fmt.Fprintf(c, "%s OK marker\r\n", tag(noop))
				if cancelInventory {
					nextTag := expectFetch(t, r, 4, "UID BODY.PEEK[]")
					fmt.Fprintf(c, "* 4 FETCH (UID 4 BODY[] {1}\r\ny)\r\n%s OK successor\r\n", nextTag)
					<-flagsRelease
					fmt.Fprintf(c, "%s OK flags\r\n", flagsTag)
				} else {
					<-flagsRelease
					fmt.Fprintf(c, "%s OK flags\r\n", flagsTag)
					inventory := expectFetch(t, r, 9, "UID")
					fmt.Fprintf(c, "%s OK actual empty UID query\r\n", inventory)
					nextTag := expectFetch(t, r, 4, "UID BODY.PEEK[]")
					fmt.Fprintf(c, "* 4 FETCH (UID 4 BODY[] {1}\r\ny)\r\n%s OK successor\r\n", nextTag)
				}
				io.Copy(io.Discard, r)
			})
			body, err := transport.QueuedBodyForTest(context.Background(), x, 2)
			if err != nil {
				t.Fatal(err)
			}
			flags, err := transport.QueuedFlagsForTest(context.Background(), x, 1)
			if err != nil {
				t.Fatal(err)
			}
			type result struct {
				cmd *transport.Command
				err error
			}
			inventoryDone, nextDone := make(chan result, 1), make(chan result, 1)
			inventoryCtx, stopInventory := context.WithCancel(context.Background())
			defer stopInventory()
			go func() { c, e := transport.QueuedAbsenceForTest(inventoryCtx, x, 9); inventoryDone <- result{c, e} }()
			awaitReclamation(t, func() bool { return x.Snapshot().FetchWaiters == 1 })
			go func() { c, e := transport.QueuedBodyForTest(context.Background(), x, 4); nextDone <- result{c, e} }()
			awaitReclamation(t, func() bool { return x.Snapshot().FetchWaiters == 2 })
			bodyOnce.Do(func() { close(bodyRelease) })
			if _, err := wait(t, body); err != nil {
				t.Fatal(err)
			}
			noop, err := x.Noop(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := wait(t, noop); err != nil {
				t.Fatal(err)
			}
			if cancelInventory {
				stopInventory()
			} else {
				flagsOnce.Do(func() { close(flagsRelease) })
			}
			select {
			case r := <-inventoryDone:
				if cancelInventory {
					if !errors.Is(r.err, context.Canceled) || r.cmd != nil {
						t.Fatal(r.err)
					}
				} else {
					if r.err != nil {
						t.Fatal(r.err)
					}
					if got, err := wait(t, r.cmd); err != nil || len(got) != 0 {
						t.Fatal(got, err)
					}
				}
			case <-time.After(2 * time.Second):
				t.Fatal("exclusive inventory did not complete or detach")
			}
			select {
			case r := <-nextDone:
				if r.err != nil {
					t.Fatal(r.err)
				}
				if got, err := wait(t, r.cmd); err != nil || len(got) != 1 || string(got[0].Bytes) != "y" {
					t.Fatal(got, err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("successor stranded behind inventory")
			}
			flagsOnce.Do(func() { close(flagsRelease) })
			if _, err := wait(t, flags); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPipelinedBodyCannotBeAttributedToAFlagsUID(t *testing.T) {
	x := selectedPeer(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		expectFetch(t, r, 2, "UID BODY.PEEK[]")
		expectFetch(t, r, 1, "UID FLAGS")
		fmt.Fprint(c, "* 1 FETCH (UID 1 BODY[] {1}\r\nx)\r\n")
		io.Copy(io.Discard, r)
	})
	body, err := transport.QueuedBodyForTest(context.Background(), x, 2)
	if err != nil {
		t.Fatal(err)
	}
	flags, err := transport.QueuedFlagsForTest(context.Background(), x, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []*transport.Command{body, flags} {
		if _, err := wait(t, cmd); !errors.Is(err, transport.ErrProtocol) {
			t.Fatal("BODY crossed query scope", err)
		}
	}
}

func TestGroupedBulkAndNarrowBudgetsKeepExclusiveFetchService(t *testing.T) {
	for _, grouped := range []bool{false, true} {
		t.Run(fmt.Sprintf("grouped_bulk_%t", grouped), func(t *testing.T) {
			limits := transport.DefaultIMAPLimits()
			uids, selector := []uint32{2}, "2:2"
			if grouped {
				uids, selector = []uint32{2, 3}, "2,3"
			} else {
				limits.LineBytes, limits.LiteralBytes, limits.ResponseBytes = 128, 128, 128
			}
			x := selectedAccountPeerWithFeatures(t, "account1", "bulk", limits, "", false, func(c net.Conn) {
				r := bufio.NewReader(c)
				first := readLine(t, r)
				if !strings.Contains(first, " UID FETCH "+selector+" (UID BODY.PEEK[])\r\n") {
					t.Error(first)
				}
				noop := readLine(t, r)
				if !strings.HasSuffix(noop, " NOOP\r\n") {
					t.Error("unbacked parallel response reached the wire", noop)
				}
				fmt.Fprintf(c, "%s OK marker\r\n", tag(noop))
				for index, uid := range uids {
					fmt.Fprintf(c, "* %d FETCH (UID %d BODY[] {1}\r\nx)\r\n", index+1, uid)
				}
				fmt.Fprintf(c, "%s OK bodies\r\n", tag(first))
				flags := expectFetch(t, r, 1, "UID FLAGS")
				fmt.Fprintf(c, "* 1 FETCH (UID 1 FLAGS ())\r\n%s OK flags\r\n", flags)
				io.Copy(io.Discard, r)
			})
			body, err := transport.QueuedBodiesForTest(context.Background(), x, uids...)
			if err != nil {
				t.Fatal(err)
			}
			type result struct {
				cmd *transport.Command
				err error
			}
			ready := make(chan result, 1)
			go func() { c, e := transport.QueuedFlagsForTest(context.Background(), x, 1); ready <- result{c, e} }()
			awaitReclamation(t, func() bool { return x.Snapshot().FetchWaiters == 1 })
			noop, err := x.Noop(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := wait(t, noop); err != nil {
				t.Fatal(err)
			}
			if got, err := wait(t, body); err != nil || len(got) != len(uids) {
				t.Fatal(got, err)
			}
			select {
			case r := <-ready:
				if r.err != nil {
					t.Fatal(r.err)
				}
				if _, err := wait(t, r.cmd); err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("exclusive flags successor stranded")
			}
			if s := x.Snapshot(); s.ConcurrentFetchPairs != 0 || s.FetchResponseBytes != 0 {
				t.Fatal(s)
			}
		})
	}
}

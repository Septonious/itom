package transport_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"itom.local/core/transport"
)

func TestCommandDiagnosticsSeparateTaggedRefusalFromCarrierFailureWithoutProviderText(t *testing.T) {
	x := selectedPeer(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		appendLine := readLine(t, r)
		_, body := readAppendLiteral(t, c, r, appendLine)
		if body == nil {
			return
		}
		fmt.Fprintf(c, "%s OK accepted\r\n", tag(appendLine))
		noop := readLine(t, r)
		fmt.Fprintf(c, "%s NO secret-provider-response-private.example\r\n", tag(noop))
		// Close during a different actual tag, rather than ordinary idle close.
		readLine(t, r)
	})
	defer x.Close()
	command, err := x.Append(context.Background(), "fast", []byte("secret-payload-private.example"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = command.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	command, err = x.Noop(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = command.Wait(context.Background()); !errors.Is(err, transport.ErrRefused) {
		t.Fatal(err)
	}
	command, err = x.Noop(context.Background())
	if err == nil {
		_, err = command.Wait(context.Background())
	}
	if err == nil {
		t.Fatal("lost tag reported as completion")
	}
	s := x.Snapshot()
	if s.Append.Started != 1 || s.Append.Completed != 1 || s.Append.Failed != 0 || s.Append.Continuation.Count != 1 || s.Append.LiteralWrite.Count != 1 || s.Append.Elapsed.Count != 1 || s.Append.Elapsed.MaxNS == 0 {
		t.Fatal("APPEND causal timings inconsistent", s)
	}
	if s.Noop.Started != 2 || s.Noop.Completed != 1 || s.Noop.Refused != 1 || s.Noop.Failed != 1 || s.Noop.Elapsed.Count != 2 || s.PendingTags != 0 || !s.Closed {
		t.Fatal("provider refusal and carrier loss were conflated", s)
	}
	encoded, err := json.Marshal(s)
	if err != nil || strings.Contains(string(encoded), "private.example") || strings.Contains(string(encoded), "secret-") {
		t.Fatal("diagnostics leaked body/provider text", err)
	}
}

func TestFlagDiscoveryAcceptsUnsolicitedUpdatesAndKeepsOneBoundedLatestHint(t *testing.T) {
	x := selectedAccountPeerWithFeatures(t, "account1", "fast", transport.DefaultIMAPLimits(), "", true, func(c net.Conn) {
		r := bufio.NewReader(c)
		flags := readLine(t, r)
		fmt.Fprintf(c, "* 2 FETCH (FLAGS ())\r\n* 3 FETCH (UID 4)\r\n* 1 FETCH (FLAGS (\\Seen) UID 1)\r\n* 1 FETCH (UID 1 FLAGS (\\Seen ITOM2_valid-atom))\r\n%s OK flags\r\n", tag(flags))
		noop := readLine(t, r)
		fmt.Fprintf(c, "%s OK healthy\r\n", tag(noop))
	})
	defer x.Close()
	if !x.KeywordsSupported() {
		t.Fatal("verified SELECT permanent flags were lost")
	}
	if _, err := x.StoreKeyword(context.Background(), 1, "bad\r\nAPPEND", true); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("keyword admitted injected command bytes", err)
	}
	cmd, err := x.FetchFlags(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	flags, err := cmd.Wait(context.Background())
	if err != nil || len(flags) != 1 || flags[0].UID != 1 || len(flags[0].Flags) != 2 || flags[0].Flags[1] != "ITOM2_valid-atom" {
		t.Fatal("ordinary metadata updates killed or duplicated the selected carrier", flags, err)
	}
	cmd, err = x.Noop(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cmd.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := x.Snapshot()
	if s.Flags.Completed != 1 || s.Store.Started != 0 || s.Fetch.Started != 0 || s.Noop.Completed != 1 {
		t.Fatal("flag/NOOP service was reported as stored body delivery", s)
	}
}

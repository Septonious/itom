package datapath

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"itom.local/core/flow"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/mailcontrol"
	"itom.local/core/pool"
	"itom.local/core/secure"
	"itom.local/core/wire"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type pumpMailLink struct {
	mail    *mailbox.Route
	inbox   *mailbox.Inbox
	cipher  *secure.Epoch
	control *mailcontrol.Sender
	ledger  *journal.Store
	grant   journal.GrantRef
	folder  string
	ready   <-chan struct{}
}

func (l *pumpMailLink) Receive(ctx context.Context) (Input, error) {
	select {
	case <-ctx.Done():
		return Input{}, ctx.Err()
	case <-l.ready:
	}
	for {
		d, e := l.inbox.Next(ctx)
		if e != nil {
			return Input{}, e
		}
		if d == nil {
			timer := time.NewTimer(time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return Input{}, ctx.Err()
			case <-timer.C:
			}
			continue
		}
		body, e := mailbox.Decode(d.Message.MIME)
		if e != nil {
			return Input{}, e
		}
		var f wire.Frame
		if mailcontrol.IsControlBody(body) {
			f, e = mailcontrol.Open(d.Message.MIME, l.cipher)
		} else {
			f, e = l.cipher.Open(body)
		}
		if e != nil {
			return Input{}, e
		}
		// Receive retains f on its stack until the pump accepts the event. The pump
		// owns a bounded queue, so there is no decode-and-drop path on saturation.
		if e = l.inbox.Accept(d.Token); e != nil {
			return Input{}, e
		}
		return Input{Generation: 1, Frame: &f}, nil
	}
}
func (l *pumpMailLink) SendControl(ctx context.Context, f wire.Frame) error {
	return l.control.Send(ctx, f).Err
}
func (l *pumpMailLink) Size(f wire.Frame) (uint64, error) { return Size(f, nil) }
func (l *pumpMailLink) Write(j *Job) Completion {
	return j.WriteLocal(l.ledger, l.grant, l.folder, l.cipher, l.mail)
}
func localTCPPair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	listener, e := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	dialed, e := net.DialTCP("tcp4", nil, listener.Addr().(*net.TCPAddr))
	if e != nil {
		t.Fatal(e)
	}
	accepted, e := listener.AcceptTCP()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { dialed.Close(); accepted.Close() })
	_ = dialed.SetDeadline(time.Now().Add(10 * time.Second))
	_ = accepted.SetDeadline(time.Now().Add(10 * time.Second))
	return dialed, accepted
}
func TestAutonomousPumpsCarryTCPBothWaysOverTLSIMAP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	mc, ms := mailPair(t, ctx)
	tx, rx := epochs(t, wire.ID{73})
	ready := make(chan struct{})
	makeLink := func(mail *mailbox.Client, cipher *secure.Epoch, folder, incoming string) *pumpMailLink {
		d, e := mailbox.NewDispatcher(ctx, mail, mailbox.DispatchConfig{Routes: []mailbox.RouteConfig{{User: "12345678", Folders: []string{"u-up", "u-down"}}}, QueuePerUser: 8})
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(d.Close)
		route, _ := d.Route("12345678")
		inbox, e := mailbox.NewInbox(route, incoming, mailbox.Cursor{})
		if e != nil {
			t.Fatal(e)
		}
		cfg := journal.Config{MaxGrants: 256, MaxAttempts: 256, Accounts: []journal.Account{{ID: "a01", Provider: "test", Limit: pool.Amount{Bytes: 4 << 20, Messages: 256}, Users: []journal.User{{ID: "12345678", Limit: pool.Amount{Bytes: 4 << 20, Messages: 256}, Folders: []string{"u-up", "u-down"}}}}}}
		open := func() *journal.Store {
			j, e := journal.Open(filepath.Join(t.TempDir(), "ledger.db"), cfg, time.Second)
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { j.Close() })
			if e = j.EnableAfterInventory("a01", "12345678", j.Run()); e != nil {
				t.Fatal(e)
			}
			return j
		}
		ledger, controlLedger := open(), open()
		scope := journal.Scope{Account: "a01", User: "12345678", Session: cipher.Context().Session}
		grant, e := ledger.Issue(scope, pool.Amount{Bytes: 2 << 20, Messages: 128})
		if e != nil {
			t.Fatal(e)
		}
		sender, e := mailcontrol.NewSender(controlLedger, scope, folder, cipher, route)
		if e != nil {
			t.Fatal(e)
		}
		return &pumpMailLink{route, inbox, cipher, sender, ledger, grant.Ref, folder, ready}
	}
	a, b := makeLink(mc, tx, "u-up", "u-down"), makeLink(ms, rx, "u-down", "u-up")
	cfg := DefaultConfig(1, []string{"a01"})
	cfg.StreamWindow = 16 << 10
	left, e := StartPump(ctx, PumpConfig{Engine: cfg, Account: "a01", ControlQueue: 256}, a)
	if e != nil {
		t.Fatal(e)
	}
	defer left.Close()
	right, e := StartPump(ctx, PumpConfig{Engine: cfg, Account: "a01", ControlQueue: 256}, b)
	if e != nil {
		t.Fatal(e)
	}
	defer right.Close()
	type ends struct{ app, destination *net.TCPConn }
	var streams []ends
	for _, id := range []uint64{1, 3} {
		app, clientSocket := localTCPPair(t)
		destination, serverSocket := localTCPPair(t)
		if e = left.Add(ctx, id, clientSocket); e != nil {
			t.Fatal(e)
		}
		if e = right.Add(ctx, id, serverSocket); e != nil {
			t.Fatal(e)
		}
		streams = append(streams, ends{app, destination})
	}
	// Every socket represents authenticated OPEN_OK/adopt. Startup and remote
	// grant negotiation are covered separately, not by this local-writer fixture.
	close(ready)
	results := make(chan error, 4)
	for index, ends := range streams {
		upload := bytes.Repeat([]byte(fmt.Sprintf("upload%d:", index)), 8000)
		download := bytes.Repeat([]byte(fmt.Sprintf("download%d:", index)), 9000)
		go func() {
			got, e := io.ReadAll(ends.destination)
			if e == nil && !bytes.Equal(got, upload) {
				e = errors.New("upload mismatch")
			}
			if e == nil {
				_, e = ends.destination.Write(download)
			}
			if e == nil {
				e = ends.destination.CloseWrite()
			}
			results <- e
		}()
		go func() {
			_, e := ends.app.Write(upload)
			if e == nil {
				e = ends.app.CloseWrite()
			}
			if e == nil {
				var got []byte
				got, e = io.ReadAll(ends.app)
				if e == nil && !bytes.Equal(got, download) {
					e = errors.New("download mismatch")
				}
			}
			results <- e
		}()
	}
	for range 4 {
		if e = <-results; e != nil {
			select {
			case <-left.Done():
				t.Log("left", left.Wait())
			default:
			}
			select {
			case <-right.Done():
				t.Log("right", right.Wait())
			default:
			}
			t.Fatal(e)
		}
	}

	// Cancellation must join workers even while Receive waits on an empty folder.
	if e = left.Close(); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if e = right.Close(); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

type eventLink struct {
	inputs   chan Input
	written  chan wire.Frame
	control  func(context.Context, wire.Frame) error
	outcome  error
	nonFatal bool
}

func (l *eventLink) Receive(ctx context.Context) (Input, error) {
	select {
	case <-ctx.Done():
		return Input{}, ctx.Err()
	case f := <-l.inputs:
		return f, nil
	}
}
func (l *eventLink) SendControl(ctx context.Context, f wire.Frame) error {
	if l.control != nil {
		return l.control(ctx, f)
	}
	return nil
}
func (l *eventLink) Size(wire.Frame) (uint64, error) { return 512, nil }
func (l *eventLink) Write(j *Job) Completion {
	result, ok := j.claim()
	if !ok {
		return result
	}
	result.Outcome = flow.Committed
	if l.outcome != nil {
		result.Outcome = flow.CommitUnknown
		result.Err = l.outcome
		result.NonFatal = l.nonFatal
	}
	f := j.frame
	f.Payload = append([]byte(nil), f.Payload...)
	select {
	case l.written <- f:
	case <-j.ctx.Done():
		result.Err = j.ctx.Err()
	}
	return result
}

type outcomeBatchEventLink struct {
	*eventLink
}

func (l *outcomeBatchEventLink) WriteBatch(jobs []*Job) []Completion {
	results := make([]Completion, len(jobs))
	for i, j := range jobs {
		r, ok := j.claim()
		if !ok {
			results[i] = r
			continue
		}
		r.Outcome = flow.Committed
		if l.outcome != nil {
			r.Outcome = flow.CommitUnknown
			r.Err = l.outcome
			r.NonFatal = l.nonFatal
		}
		f := j.frame
		f.Payload = append([]byte(nil), f.Payload...)
		select {
		case l.written <- f:
		case <-j.ctx.Done():
			r.Err = j.ctx.Err()
		}
		results[i] = r
	}
	return results
}
func (l *outcomeBatchEventLink) BatchParallelism() int { return 1 }

func TestPumpDeferredInitialReturnsWindowWithoutPublishing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	published := make(chan wire.Frame, 2)
	l := &eventLink{inputs: make(chan Input, 2), written: make(chan wire.Frame, 2), control: func(ctx context.Context, f wire.Frame) error {
		select {
		case published <- f:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	cfg := DefaultConfig(1, []string{"a01"})
	p, err := StartPump(ctx, PumpConfig{Engine: cfg, Account: "a01", ControlQueue: 8}, l)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	app, socket := localTCPPair(t)
	defer app.Close()
	initial, err := p.AddDeferredInitial(ctx, 1, socket)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := wire.DecodeControl(initial)
	if err != nil {
		t.Fatal(err)
	}
	window, ok := decoded.(wire.Credit)
	if !ok || initial.Stream != 1 || window.MaxStream != cfg.StreamWindow || window.MaxData != cfg.ReceiveBytes {
		t.Fatalf("initial=%T stream=%d max_stream=%d max_data=%d", decoded, initial.Stream, window.MaxStream, window.MaxData)
	}
	timer := time.NewTimer(25 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case f := <-published:
			decoded, decodeErr := wire.DecodeControl(f)
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if _, isCredit := decoded.(wire.Credit); isCredit && f.Stream == 1 {
				t.Fatalf("deferred WINDOW was published by Pump: type=%d stream=%d", f.Type, f.Stream)
			}
		case <-timer.C:
			return
		}
	}
}

func TestPumpBlockedUploadDoesNotBlockDownload(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	l := &eventLink{inputs: make(chan Input, 8), written: make(chan wire.Frame, 8)}
	cfg := DefaultConfig(1, []string{"a01"})
	p, e := StartPump(ctx, PumpConfig{Engine: cfg, Account: "a01", ControlQueue: 32}, l)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	app, socket := localTCPPair(t)
	if e = p.Add(ctx, 1, socket); e != nil {
		t.Fatal(e)
	}
	if _, e = app.Write([]byte("upload waiting for WINDOW")); e != nil {
		t.Fatal(e)
	}
	down := wire.Frame{Type: wire.Data, Stream: 1, Logical: 1, Payload: []byte("down")}
	l.inputs <- Input{Generation: 1, Frame: &down}
	_ = app.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, 4)
	if _, e = io.ReadFull(app, got); e != nil || string(got) != "down" {
		t.Fatal("blocked upload stalled download", e)
	}
	select {
	case <-l.written:
		t.Fatal("sent without peer WINDOW")
	default:
	}
	credit, _ := wire.EncodeControl(1, wire.Credit{MaxStream: 128, MaxData: 128})
	l.inputs <- Input{Generation: 1, Frame: &credit}
	select {
	case f := <-l.written:
		if string(f.Payload) != "upload waiting for WINDOW" {
			t.Fatal("changed upload")
		}
	case <-ctx.Done():
		t.Fatal("WINDOW did not release pending upload")
	}
}
func TestPumpUnknownWriterOutcomeEndsEpochAndJoins(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	l := &eventLink{inputs: make(chan Input, 2), written: make(chan wire.Frame, 2), outcome: mailbox.ErrUnknown}
	p, e := StartPump(ctx, PumpConfig{Engine: DefaultConfig(1, []string{"a01"}), Account: "a01", ControlQueue: 8}, l)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	app, socket := localTCPPair(t)
	if e = p.Add(ctx, 1, socket); e != nil {
		t.Fatal(e)
	}
	credit, _ := wire.EncodeControl(1, wire.Credit{MaxStream: 128, MaxData: 128})
	l.inputs <- Input{Generation: 1, Frame: &credit}
	_, e = app.Write([]byte("unknown"))
	if e != nil {
		t.Fatal(e)
	}
	if e = p.Wait(); !errors.Is(e, mailbox.ErrUnknown) {
		t.Fatal(e)
	}
	// Closed pump retains no sockets, blocked requests or callback/key references.
	if len(p.sockets) != 0 || p.link != nil {
		t.Fatal("shutdown retained resources")
	}
}
func TestPumpNonFatalUnknownKeepsLogicalOwnerAlive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	l := &eventLink{inputs: make(chan Input, 2), written: make(chan wire.Frame, 2), outcome: mailbox.ErrUnknown, nonFatal: true}
	p, err := StartPump(ctx, PumpConfig{Engine: DefaultConfig(1, []string{"a01"}), Account: "a01", ControlQueue: 8}, l)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	app, socket := localTCPPair(t)
	if err = p.Add(ctx, 1, socket); err != nil {
		t.Fatal(err)
	}
	credit, _ := wire.EncodeControl(1, wire.Credit{MaxStream: 128, MaxData: 128})
	l.inputs <- Input{Generation: 1, Frame: &credit}
	if _, err = app.Write([]byte("unknown-but-repairable")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-l.written:
	case <-ctx.Done():
		t.Fatal("writer was not invoked")
	}
	select {
	case <-p.Done():
		t.Fatalf("non-fatal unknown ended owner pump: %v", p.Wait())
	case <-time.After(100 * time.Millisecond):
	}
}

func TestPumpBatchNonFatalUnknownKeepsLogicalOwnerAlive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	base := &eventLink{inputs: make(chan Input, 2), written: make(chan wire.Frame, 2), outcome: mailbox.ErrUnknown, nonFatal: true}
	l := &outcomeBatchEventLink{eventLink: base}
	p, err := StartPump(ctx, PumpConfig{Engine: DefaultConfig(1, []string{"a01"}), Account: "a01", ControlQueue: 8}, l)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	app, socket := localTCPPair(t)
	if err = p.Add(ctx, 1, socket); err != nil {
		t.Fatal(err)
	}
	credit, _ := wire.EncodeControl(1, wire.Credit{MaxStream: 128, MaxData: 128})
	l.inputs <- Input{Generation: 1, Frame: &credit}
	if _, err = app.Write([]byte("batch-unknown-but-repairable")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-l.written:
	case <-ctx.Done():
		t.Fatal("batch writer was not invoked")
	}
	select {
	case <-p.Done():
		t.Fatalf("non-fatal batch unknown ended owner pump: %v", p.Wait())
	case <-time.After(100 * time.Millisecond):
	}
}

func TestPumpRejectsStaleGeneration(t *testing.T) {
	l := &eventLink{inputs: make(chan Input, 1), written: make(chan wire.Frame, 1)}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	p, e := StartPump(ctx, PumpConfig{Engine: DefaultConfig(1, []string{"a01"}), Account: "a01", ControlQueue: 8}, l)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	f, _ := wire.EncodeControl(1, wire.Received{})
	l.inputs <- Input{Generation: 2, Frame: &f}
	if e = p.Wait(); e != ErrGeneration {
		t.Fatal(e)
	}
}

func checkPumpGrantUsageBypassesBulkControlBacklog(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &Pump{
		ctx:            ctx,
		controls:       make(chan wire.Frame, 8),
		urgentControls: make(chan wire.Frame, 2),
	}
	if err := p.control(wire.Frame{Type: wire.Window, Stream: 1}); err != nil {
		t.Fatal(err)
	}
	if err := p.control(wire.Frame{Type: wire.GrantUsage}); err != nil {
		t.Fatal(err)
	}
	first, ok := p.nextControl()
	if !ok || first.Type != wire.GrantUsage {
		t.Fatalf("first control=%v ok=%t, want GrantUsage", first.Type, ok)
	}
	second, ok := p.nextControl()
	if !ok || second.Type != wire.Window {
		t.Fatalf("second control=%v ok=%t, want Window", second.Type, ok)
	}
}

func checkPumpGrantUsageQueueRemainsBounded(t *testing.T) {
	p := &Pump{
		controls:       make(chan wire.Frame, 2),
		urgentControls: make(chan wire.Frame, 1),
	}
	if err := p.control(wire.Frame{Type: wire.GrantUsage}); err != nil {
		t.Fatal(err)
	}
	if err := p.control(wire.Frame{Type: wire.GrantUsage}); !errors.Is(err, flow.ErrCredit) {
		t.Fatalf("urgent saturation=%v, want flow credit error", err)
	}
}

func TestPumpCumulativeControlSaturationPreservesDataAndOwner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	l := &eventLink{inputs: make(chan Input, 8), written: make(chan wire.Frame, 1), control: func(c context.Context, _ wire.Frame) error { <-c.Done(); return c.Err() }}
	p, e := StartPump(ctx, PumpConfig{Engine: DefaultConfig(1, []string{"a01"}), Account: "a01", ControlQueue: 2}, l)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	app, socket := localTCPPair(t)
	defer app.Close()
	if e = p.Add(ctx, 1, socket); e != nil {
		t.Fatal(e)
	}
	for id := uint64(1); id <= 4; id++ {
		f := wire.Frame{Type: wire.Data, Stream: 1, Logical: id, Offset: id - 1, Payload: []byte("x")}
		l.inputs <- Input{Generation: 1, Frame: &f}
	}
	app.SetReadDeadline(time.Now().Add(time.Second))
	received := make([]byte, 4)
	if _, e = io.ReadFull(app, received); e != nil || string(received) != "xxxx" {
		t.Fatal(received, e)
	}
	select {
	case <-p.Done():
		t.Fatal("cumulative control stalled all streams", p.Wait())
	default:
	}
	cancel()
	if e = p.Wait(); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

type shortFailSocket struct{ *net.TCPConn }

var shortFailure = errors.New("injected partial socket write failure")

func (s shortFailSocket) Write(b []byte) (int, error) {
	n, e := s.TCPConn.Write(b[:min(2, len(b))])
	if e != nil {
		return n, e
	}
	return n, shortFailure
}
func TestPumpPartialSocketWritePreservesPrefixAndResetsOnlyStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	l := &eventLink{inputs: make(chan Input, 1), written: make(chan wire.Frame, 1)}
	failure := make(chan string, 1)
	p, e := StartPump(ctx, PumpConfig{Engine: DefaultConfig(1, []string{"a01"}), Account: "a01", ControlQueue: 8, SocketFailure: func(id uint64, operation string, err error) {
		if id == 1 && err == shortFailure {
			failure <- operation
		}
	}}, l)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	app, socket := localTCPPair(t)
	if e = p.Add(ctx, 1, shortFailSocket{socket}); e != nil {
		t.Fatal(e)
	}
	f := wire.Frame{Type: wire.Data, Stream: 1, Logical: 1, Payload: []byte("abcdef")}
	l.inputs <- Input{Generation: 1, Frame: &f}
	_ = app.SetReadDeadline(time.Now().Add(time.Second))
	got, e := io.ReadAll(app)
	if e != nil || string(got) != "ab" {
		t.Fatal("wrong partial delivery", string(got), e)
	}
	select {
	case operation := <-failure:
		if operation != "socket_write" {
			t.Fatal("wrong failing TCP direction", operation)
		}
	case <-ctx.Done():
		t.Fatal("lost TCP closure cause")
	}
	other, otherSocket := localTCPPair(t)
	defer other.Close()
	if e = p.Add(ctx, 3, otherSocket); e != nil {
		t.Fatal("unrelated stream could not open", e)
	}
	otherFrame := wire.Frame{Type: wire.Data, Stream: 3, Logical: 2, Payload: []byte("alive")}
	l.inputs <- Input{Generation: 1, Frame: &otherFrame}
	other.SetReadDeadline(time.Now().Add(time.Second))
	buffer := make([]byte, 5)
	if _, e = io.ReadFull(other, buffer); e != nil || string(buffer) != "alive" {
		t.Fatal("unrelated stream failed", e)
	}
}

type batchEventLink struct {
	*eventLink
	batches chan []uint64
}

func (l *batchEventLink) WriteBatch(jobs []*Job) []Completion {
	streams := make([]uint64, 0, len(jobs))
	results := make([]Completion, 0, len(jobs))
	for _, j := range jobs {
		r, ok := j.claim()
		if !ok {
			results = append(results, r)
			continue
		}
		streams = append(streams, j.frame.Stream)
		r.Outcome = flow.Committed
		results = append(results, r)
		clear(j.frame.Payload)
	}
	select {
	case l.batches <- streams:
	case <-jobs[0].ctx.Done():
	}
	return results
}

func checkPumpBulkQueueCanFeedEveryPhysicalWorker(t *testing.T) {
	want := dataBatchMaxRecords * 8
	if dataBulkQueueRecords != want {
		t.Fatalf("bulk queue=%d records, want %d for eight max-size physical batches", dataBulkQueueRecords, want)
	}
	if uint64(dataBulkQueueRecords)*uint64(wire.MaxData) != 4<<20 {
		t.Fatalf("bulk queue=%d bytes, want 4 MiB tunnel flow window", uint64(dataBulkQueueRecords)*uint64(wire.MaxData))
	}
}

func TestPumpParallelCollectorsDoNotFragmentOneBulkBurst(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	p := &Pump{
		ctx:      ctx,
		dataFast: make(chan *Job, dataFastQueueRecords),
		dataBulk: make(chan *Job, dataBulkQueueRecords),
	}
	const collectors = 8
	sizes := make(chan int, collectors)
	var wg sync.WaitGroup
	for i := 0; i < collectors; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			batch, ok := p.collectDataBatch()
			if ok {
				sizes <- len(batch)
			}
		}()
	}
	// Give all writer goroutines time to enter the collector path before DATA
	// arrives. Without the serialized collector they would all block directly on
	// dataBulk and the eleven-record burst would be split across eight physical
	// batches instead of filling one near-maximum physical batch.
	time.Sleep(10 * time.Millisecond)
	for i := 0; i < 11; i++ {
		p.dataBulk <- &Job{
			generation: 1,
			attempt:    flow.Attempt{ID: uint64(i + 1), PayloadBytes: 32 << 10},
			frame:      wire.Frame{Type: wire.Data, Stream: 1, Logical: uint64(i + 1), Payload: make([]byte, 32<<10)},
			ctx:        ctx,
			claimed:    new(atomic.Bool),
		}
	}
	select {
	case n := <-sizes:
		if n != 11 {
			t.Fatalf("parallel collectors fragmented bulk burst: first batch=%d records, want 11", n)
		}
	case <-ctx.Done():
		t.Fatal("collector did not produce target bulk batch", ctx.Err())
	}
	cancel()
	wg.Wait()
}

func TestPumpFastDataInterruptsBulkCoalescing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	p := &Pump{
		ctx:          ctx,
		dataFast:     make(chan *Job, dataFastQueueRecords),
		dataFastWake: make(chan struct{}, 1),
		dataBulk:     make(chan *Job, dataBulkQueueRecords),
	}
	makeJob := func(id uint64, size uint64) *Job {
		return &Job{
			generation: 1,
			attempt:    flow.Attempt{ID: id, PayloadBytes: size},
			frame:      wire.Frame{Type: wire.Data, Stream: id, Logical: id, Payload: make([]byte, size)},
			ctx:        ctx,
			claimed:    new(atomic.Bool),
		}
	}

	// Start a sparse bulk batch. With no wakeup it must remain in coalescing
	// until dataBatchDelay expires. A short interactive record arriving behind
	// it must wake the collector instead of inheriting that 20 ms delay.
	p.dataBulk <- makeJob(1, 32<<10)
	done := make(chan []*Job, 1)
	go func() {
		batch, ok := p.collectDataBatch()
		if ok {
			done <- batch
		}
	}()
	started := time.Now().Add(100 * time.Millisecond)
	for len(p.dataBulk) != 0 && time.Now().Before(started) {
		time.Sleep(100 * time.Microsecond)
	}
	if len(p.dataBulk) != 0 {
		t.Fatal("bulk collector did not start")
	}
	p.dataFast <- makeJob(2, 1024)
	p.signalFastData()

	select {
	case batch := <-done:
		if len(batch) != 1 || batch[0].Attempt().ID != 1 {
			t.Fatalf("bulk wake returned attempts=%v, want only bulk attempt 1", func() []uint64 {
				ids := make([]uint64, 0, len(batch))
				for _, j := range batch {
					ids = append(ids, j.Attempt().ID)
				}
				return ids
			}())
		}
		if len(p.dataFast) != 1 {
			t.Fatalf("interactive record was consumed by bulk collector: queued=%d", len(p.dataFast))
		}
	case <-time.After(dataBatchDelay * 3 / 4):
		t.Fatal("interactive DATA did not interrupt bulk coalescing")
	}
}

func TestPumpReservedFastWorkerDoesNotFragmentBulkCoalescing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	p := &Pump{
		ctx:          ctx,
		dataFast:     make(chan *Job, dataFastQueueRecords),
		dataFastWake: make(chan struct{}, 1),
		dataBulk:     make(chan *Job, dataBulkQueueRecords),
		fastReserve:  true,
	}
	makeJob := func(id uint64, size uint64) *Job {
		return &Job{generation: 1, attempt: flow.Attempt{ID: id, PayloadBytes: size}, frame: wire.Frame{Type: wire.Data, Stream: id, Logical: id, Payload: make([]byte, size)}, ctx: ctx, claimed: new(atomic.Bool)}
	}
	p.dataBulk <- makeJob(1, 32<<10)
	done := make(chan []*Job, 1)
	go func() {
		batch, ok := p.collectDataBatch()
		if ok {
			done <- batch
		}
	}()
	deadline := time.Now().Add(100 * time.Millisecond)
	for len(p.dataBulk) != 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Microsecond)
	}
	if len(p.dataBulk) != 0 {
		t.Fatal("bulk collector did not start")
	}
	p.dataFast <- makeJob(2, 1024)
	p.signalFastData()
	p.dataBulk <- makeJob(3, 32<<10)
	select {
	case batch := <-done:
		if len(batch) != 2 || batch[0].Attempt().ID != 1 || batch[1].Attempt().ID != 3 {
			t.Fatalf("reserved fast wake fragmented bulk batch: records=%d", len(batch))
		}
		if len(p.dataFast) != 1 {
			t.Fatalf("bulk collector consumed reserved short DATA: queued=%d", len(p.dataFast))
		}
	case <-ctx.Done():
		t.Fatal("collector did not finish", ctx.Err())
	}
}

func checkPumpSerializedCollectorKeepsFastBurstFairnessGlobal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	p := &Pump{
		ctx:      ctx,
		dataFast: make(chan *Job, dataFastQueueRecords),
		dataBulk: make(chan *Job, dataBulkQueueRecords),
	}
	makeJob := func(id uint64, size uint64) *Job {
		return &Job{
			generation: 1,
			attempt:    flow.Attempt{ID: id, PayloadBytes: size},
			frame:      wire.Frame{Type: wire.Data, Stream: id, Logical: id, Payload: make([]byte, size)},
			ctx:        ctx,
			claimed:    new(atomic.Bool),
		}
	}
	// One full fast batch consumes the global four-record burst allowance. The
	// next collector may be a different physical APPEND worker, but it must take
	// waiting bulk rather than resetting fast priority per worker.
	for i := uint64(1); i <= dataFastBurst+1; i++ {
		p.dataFast <- makeJob(i, 1024)
	}
	p.dataBulk <- makeJob(100, 32<<10)
	first, ok := p.collectDataBatch()
	if !ok || len(first) != dataFastBurst {
		t.Fatalf("first fast batch=%d ok=%t, want %d records", len(first), ok, dataFastBurst)
	}
	second, ok := p.collectDataBatch()
	if !ok || len(second) != 1 || second[0].Attempt().ID != 100 {
		var id uint64
		if len(second) != 0 {
			id = second[0].Attempt().ID
		}
		t.Fatalf("global fast fairness selected attempt=%d records=%d ok=%t, want bulk attempt 100", id, len(second), ok)
	}
}

func TestPumpBatchesMultipleStreamsWithFixedDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	base := &eventLink{inputs: make(chan Input, 16), written: make(chan wire.Frame, 16)}
	link := &batchEventLink{eventLink: base, batches: make(chan []uint64, 32)}
	cfg := DefaultConfig(1, []string{"a01"})
	cfg.Limits.Logical = 1 << 20
	cfg.AccountCaps["a01"] = 1 << 20
	p, err := StartPump(ctx, PumpConfig{Engine: cfg, Account: "a01", ControlQueue: 32, ReadCoalesce: 2 * time.Millisecond}, link)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	app1, sock1 := localTCPPair(t)
	app2, sock2 := localTCPPair(t)
	if err = p.Add(ctx, 1, sock1); err != nil {
		t.Fatal(err)
	}
	if err = p.Add(ctx, 3, sock2); err != nil {
		t.Fatal(err)
	}
	for _, stream := range []uint64{1, 3} {
		credit, e := wire.EncodeControl(stream, wire.Credit{MaxStream: 1 << 20, MaxData: 1 << 20})
		if e != nil {
			t.Fatal(e)
		}
		base.inputs <- Input{Generation: 1, Frame: &credit}
	}
	start := make(chan struct{})
	payload := bytes.Repeat([]byte("batch"), 8<<10)
	for _, c := range []*net.TCPConn{app1, app2} {
		c := c
		go func() {
			<-start
			_, _ = c.Write(payload)
		}()
	}
	close(start)
	for {
		select {
		case streams := <-link.batches:
			seen1, seen3 := false, false
			for _, stream := range streams {
				seen1 = seen1 || stream == 1
				seen3 = seen3 || stream == 3
			}
			if seen1 && seen3 {
				return
			}
		case <-ctx.Done():
			t.Fatal("no multi-stream batch before deadline", ctx.Err())
		}
	}
}

type priorityBatchEvent struct {
	streams []uint64
	allFast bool
}

type priorityBatchLink struct {
	*eventLink
	entered chan priorityBatchEvent
	release chan struct{}
	first   bool
}

func (l *priorityBatchLink) WriteBatch(jobs []*Job) []Completion {
	event := priorityBatchEvent{allFast: true, streams: make([]uint64, 0, len(jobs))}
	for _, j := range jobs {
		event.streams = append(event.streams, j.frame.Stream)
		event.allFast = event.allFast && fastDataJob(j)
	}
	l.entered <- event
	if l.first {
		l.first = false
		select {
		case <-l.release:
		case <-jobs[0].ctx.Done():
		}
	}
	results := make([]Completion, 0, len(jobs))
	for _, j := range jobs {
		r, ok := j.claim()
		if ok {
			r.Outcome = flow.Committed
			clear(j.frame.Payload)
		}
		results = append(results, r)
	}
	return results
}

func TestPumpShortRecordBypassesQueuedBulk(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	base := &eventLink{inputs: make(chan Input, 64), written: make(chan wire.Frame, 64)}
	link := &priorityBatchLink{eventLink: base, entered: make(chan priorityBatchEvent, 64), release: make(chan struct{}), first: true}
	cfg := DefaultConfig(1, []string{"a01"})
	cfg.Limits.Logical = 4 << 20
	cfg.AccountCaps["a01"] = 4 << 20
	p, err := StartPump(ctx, PumpConfig{Engine: cfg, Account: "a01", ControlQueue: 64, ReadCoalesce: 0}, link)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	bulkApp, bulkSocket := localTCPPair(t)
	probeApp, probeSocket := localTCPPair(t)
	defer bulkApp.Close()
	defer probeApp.Close()
	if err = p.Add(ctx, 1, bulkSocket); err != nil {
		t.Fatal(err)
	}
	if err = p.Add(ctx, 3, probeSocket); err != nil {
		t.Fatal(err)
	}
	for _, stream := range []uint64{1, 3} {
		credit, e := wire.EncodeControl(stream, wire.Credit{MaxStream: 4 << 20, MaxData: 4 << 20})
		if e != nil {
			t.Fatal(e)
		}
		base.inputs <- Input{Generation: 1, Frame: &credit}
	}

	bulk := bytes.Repeat([]byte("bulk0123456789ab"), 64<<10)
	go func() { _, _ = bulkApp.Write(bulk) }()
	select {
	case <-link.entered:
	case <-ctx.Done():
		t.Fatal("bulk batch did not enter writer")
	}

	if _, err = probeApp.Write([]byte("latency-probe")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for len(p.dataFast)+len(p.dataTiny) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(p.dataFast)+len(p.dataTiny) == 0 {
		t.Fatal("short record did not reach reserved queue")
	}
	close(link.release)

	for {
		select {
		case event := <-link.entered:
			containsProbe := false
			for _, stream := range event.streams {
				containsProbe = containsProbe || stream == 3
			}
			if !containsProbe {
				// A short tail record from the bulk socket is itself legitimately
				// fast-class; it may win the race before the probe batch.
				continue
			}
			if !event.allFast {
				t.Fatalf("short DATA was mixed back into a bulk-class physical batch: %v", event.streams)
			}
			return
		case <-ctx.Done():
			t.Fatal("no probe batch after releasing bulk writer")
		}
	}
}

type reservedFastBatchEvent struct {
	fast    bool
	streams []uint64
}

type reservedFastBatchLink struct {
	*eventLink
	entered chan reservedFastBatchEvent
	release chan struct{}
}

func (l *reservedFastBatchLink) BatchParallelism() int     { return 2 }
func (l *reservedFastBatchLink) FastBatchParallelism() int { return 1 }
func (l *reservedFastBatchLink) WriteBatch(jobs []*Job) []Completion {
	fast := true
	streams := make([]uint64, 0, len(jobs))
	for _, j := range jobs {
		streams = append(streams, j.frame.Stream)
		fast = fast && fastDataJob(j)
	}
	l.entered <- reservedFastBatchEvent{fast: fast, streams: streams}
	if !fast {
		select {
		case <-l.release:
		case <-jobs[0].ctx.Done():
		}
	}
	results := make([]Completion, 0, len(jobs))
	for _, j := range jobs {
		r, ok := j.claim()
		if ok {
			r.Outcome = flow.Committed
			clear(j.frame.Payload)
		}
		results = append(results, r)
	}
	return results
}

func TestPumpReservedFastWorkerBypassesAllBusyBulkWorkers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	base := &eventLink{inputs: make(chan Input, 1), written: make(chan wire.Frame, 1)}
	link := &reservedFastBatchLink{
		eventLink: base,
		entered:   make(chan reservedFastBatchEvent, 16),
		release:   make(chan struct{}),
	}
	var releaseOnce sync.Once
	releaseBulk := func() { releaseOnce.Do(func() { close(link.release) }) }
	defer releaseBulk()

	cfg := DefaultConfig(1, []string{"a01"})
	cfg.Limits.Logical = 4 << 20
	cfg.AccountCaps["a01"] = 4 << 20
	p, err := StartPump(ctx, PumpConfig{Engine: cfg, Account: "a01", ControlQueue: 32}, link)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	makeJob := func(id, stream, size uint64) *Job {
		return &Job{
			generation: 1,
			attempt:    flow.Attempt{ID: id, PayloadBytes: size},
			frame:      wire.Frame{Type: wire.Data, Stream: stream, Logical: id, Payload: make([]byte, size)},
			ctx:        p.ctx,
			claimed:    new(atomic.Bool),
			queuedAt:   time.Now(),
		}
	}

	// Inject already-admitted jobs so TCP fragmentation cannot accidentally turn
	// a bulk stream tail into a short-record job. Each bulk job reaches the batch
	// target by itself, occupying one ordinary worker in WriteBatch.
	p.dataBulk <- makeJob(1, 1, dataBatchTargetBytes)
	p.dataBulk <- makeJob(2, 1, dataBatchTargetBytes)
	blocked := 0
	for blocked < 2 {
		select {
		case event := <-link.entered:
			if event.fast {
				t.Fatalf("unexpected fast batch while filling bulk workers: %v", event.streams)
			}
			blocked++
		case <-ctx.Done():
			t.Fatal("did not occupy both ordinary bulk workers", ctx.Err())
		}
	}

	p.dataFast <- makeJob(3, 3, 1024)
	select {
	case event := <-link.entered:
		if !event.fast || len(event.streams) != 1 || event.streams[0] != 3 {
			t.Fatalf("reserved fast worker batch=%v fast=%t, want probe stream 3", event.streams, event.fast)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("short DATA waited behind all busy ordinary batch workers")
	}
	releaseBulk()
}

type retryOnceLink struct {
	*eventLink
	retry bool
}

func (l *retryOnceLink) Write(j *Job) Completion {
	r, ok := j.claim()
	if !ok {
		return r
	}
	r.Outcome = flow.Committed
	if !l.retry {
		l.retry = true
		r.Outcome = flow.NotCommitted
		r.NonFatal = true
		r.Err = context.Canceled
	}
	f := j.frame
	f.Payload = append([]byte(nil), f.Payload...)
	select {
	case l.written <- f:
	case <-j.ctx.Done():
		r.Err = j.ctx.Err()
	}
	return r
}
func TestPumpRetriesDefinitiveCarrierCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	l := &retryOnceLink{eventLink: &eventLink{inputs: make(chan Input, 2), written: make(chan wire.Frame, 2)}}
	p, err := StartPump(ctx, PumpConfig{Engine: DefaultConfig(1, []string{"a01"}), Account: "a01", ControlQueue: 8}, l)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	app, socket := localTCPPair(t)
	if err = p.Add(ctx, 1, socket); err != nil {
		t.Fatal(err)
	}
	credit, _ := wire.EncodeControl(1, wire.Credit{MaxStream: 128, MaxData: 128})
	l.inputs <- Input{Generation: 1, Frame: &credit}
	if _, err = app.Write([]byte("same-offset-after-carrier-cancel")); err != nil {
		t.Fatal(err)
	}
	var first wire.Frame
	for i := 0; i < 2; i++ {
		select {
		case f := <-l.written:
			if i == 0 {
				first = f
			} else if f.Stream != first.Stream || f.Offset != first.Offset || string(f.Payload) != string(first.Payload) {
				t.Fatal("definitive retry changed byte identity")
			}
		case <-p.Done():
			t.Fatal("physical failure ended owner", p.Wait())
		case <-ctx.Done():
			t.Fatal("definitive retry was not scheduled")
		}
	}
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioPump(t *testing.T) {
	t.Log("check=checkPumpGrantUsageBypassesBulkControlBacklog")
	checkPumpGrantUsageBypassesBulkControlBacklog(t)
	t.Log("check=checkPumpGrantUsageQueueRemainsBounded")
	checkPumpGrantUsageQueueRemainsBounded(t)
	t.Log("check=checkPumpBulkQueueCanFeedEveryPhysicalWorker")
	checkPumpBulkQueueCanFeedEveryPhysicalWorker(t)
	t.Log("check=checkPumpSerializedCollectorKeepsFastBurstFairnessGlobal")
	checkPumpSerializedCollectorKeepsFastBurstFairnessGlobal(t)
}

func TestPumpSharedCreditLeavesRoomForShortRead(t *testing.T) {
	checkPumpFragmentedBulkKeepsCreditReserve(t)
	for _, reserve := range []uint64{0, 8192} {
		t.Run(fmt.Sprint(reserve), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			link := &eventLink{inputs: make(chan Input, 8), written: make(chan wire.Frame, 16)}
			cfg := DefaultConfig(1, []string{"a01"})
			cfg.Limits.Logical = 64 << 10
			cfg.AccountCaps["a01"] = 64 << 10
			cfg.ReceiveBytes = 64 << 10
			cfg.StreamWindow = 32 << 10
			p, err := StartPump(ctx, PumpConfig{Engine: cfg, Account: "a01", ControlQueue: 16, InteractiveReserve: reserve}, link)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			var apps []*net.TCPConn
			for _, id := range []uint64{1, 3, 5} {
				app, socket := localTCPPair(t)
				defer app.Close()
				if err = p.Add(ctx, id, socket); err != nil {
					t.Fatal(err)
				}
				apps = append(apps, app)
				f, err := wire.EncodeControl(id, wire.Credit{MaxStream: 32 << 10, MaxData: 64 << 10})
				if err != nil {
					t.Fatal(err)
				}
				link.inputs <- Input{Generation: 1, Frame: &f}
			}
			for _, app := range apps[:2] {
				if _, err = app.Write(bytes.Repeat([]byte("b"), 32<<10)); err != nil {
					t.Fatal(err)
				}
			}
			var bulk uint64
			for bulk < (64<<10)-reserve {
				select {
				case f := <-link.written:
					if f.Stream > 3 || !bytes.Equal(f.Payload, bytes.Repeat([]byte("b"), len(f.Payload))) {
						t.Fatal("bulk payload", f.Stream)
					}
					bulk += uint64(len(f.Payload))
				case <-ctx.Done():
					t.Fatal("bulk stalled", bulk)
				}
			}
			if bulk != (64<<10)-reserve {
				t.Fatal("bulk consumed protected credit", bulk)
			}
			probe := bytes.Repeat([]byte("p"), 512)
			if _, err = apps[2].Write(probe); err != nil {
				t.Fatal(err)
			}
			timer := time.NewTimer(150 * time.Millisecond)
			defer timer.Stop()
			select {
			case f := <-link.written:
				if reserve == 0 || f.Stream != 5 || !bytes.Equal(f.Payload, probe) {
					t.Fatal("unexpected admission", f.Stream, len(f.Payload))
				}
			case <-timer.C:
				if reserve != 0 {
					t.Fatal("short read starved despite protected credit")
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		})
	}
}

// Force the exact split that intermittently occurred on real TCP under -race:
// a32741-byte read followed by27bytes. It must not steal the remaining8192
// connection credit after another bulk stream fills the ordinary capacity.
func checkPumpFragmentedBulkKeepsCreditReserve(t *testing.T) {
	cfg := DefaultConfig(1, []string{"a01"})
	cfg.Limits.Logical = 64 << 10
	cfg.AccountCaps["a01"] = 64 << 10
	cfg.ReceiveBytes = 64 << 10
	cfg.StreamWindow = 32 << 10
	e, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	for _, id := range []uint64{1, 3, 5} {
		if _, err = e.Activate(1, id); err != nil {
			t.Fatal(err)
		}
		f, _ := wire.EncodeControl(id, wire.Credit{MaxStream: 32 << 10, MaxData: 64 << 10})
		if _, err = e.Receive(1, f); err != nil {
			t.Fatal(err)
		}
	}
	p := &Pump{engine: e, cfg: PumpConfig{Engine: cfg, InteractiveReserve: 8192}}
	first := &socketState{read: &readResult{bulk: true, data: bytes.Repeat([]byte("b"), 32741), ack: make(chan struct{})}}
	if err = p.acceptRead(1, first); err != nil {
		t.Fatal(err)
	}
	other := &socketState{read: &readResult{bulk: true, data: bytes.Repeat([]byte("b"), 32<<10), ack: make(chan struct{})}}
	if err = p.acceptRead(3, other); err != nil {
		t.Fatal(err)
	}
	tail := &readResult{data: bytes.Repeat([]byte("b"), 27), ack: make(chan struct{})}
	first.read = tail
	if err = p.acceptRead(1, first); err != nil {
		t.Fatal(err)
	}
	tx, _ := e.Snapshot()
	if first.read != tail || len(tail.data) != 27 || tx.Logical != 56<<10 {
		t.Fatalf("fragmented bulk stole interactive credit: remaining=%d logical=%d", len(tail.data), tx.Logical)
	}
	probe := bytes.Repeat([]byte("p"), 512)
	short := &socketState{read: &readResult{data: probe, ack: make(chan struct{})}}
	if err = p.acceptRead(5, short); err != nil {
		t.Fatal(err)
	}
	tx, _ = e.Snapshot()
	if short.read != nil || tx.Logical != (56<<10)+512 {
		t.Fatal("protected short payload was not admitted", tx.Logical)
	}
	// Authenticate receipts for the original block and its tail. Later short
	// exchanges on the same TCP socket must regain interactive admission.
	receipt := func(frontier uint64) {
		t.Helper()
		for _, item := range e.PendingFair(0) {
			f, payloadBytes, err := e.FrameInfo(1, item)
			if err != nil {
				t.Fatal(err)
			}
			if f.Stream == 1 {
				if _, err = e.Begin(1, item, "a01", payloadBytes+100, 0); err != nil {
					t.Fatal(err)
				}
			}
		}
		f, _ := wire.EncodeControl(1, wire.Received{Frontier: frontier})
		if _, err = e.Receive(1, f); err != nil {
			t.Fatal(err)
		}
	}
	receipt(32741)
	f, _ := wire.EncodeControl(1, wire.Credit{MaxStream: 64 << 10, MaxData: 128 << 10})
	if _, err = e.Receive(1, f); err != nil {
		t.Fatal(err)
	}
	if err = p.acceptRead(1, first); err != nil || first.read != nil {
		t.Fatal("bulk tail did not resume after receipt", err)
	}
	receipt(32768)
	shortAgain := &readResult{data: bytes.Repeat([]byte("p"), 512), ack: make(chan struct{})}
	first.read = shortAgain
	if err = p.acceptRead(1, first); err != nil || first.read != nil || first.bulkRead || shortAgain.bulk {
		t.Fatal("drained TCP stream retained bulk class", err)
	}
}

func TestBatchResidenceIncludesExistingQueueAge(t *testing.T) {
	first := &Job{queuedAt: time.Now().Add(-time.Second)}
	if got := batchResidence(first, dataBatchDelay); got != 0 {
		t.Fatal("old batch waited again", got)
	}
	first.queuedAt = time.Now()
	if got := batchResidence(first, dataBatchDelay); got <= 0 || got > dataBatchDelay {
		t.Fatal("fresh batch lost absolute deadline", got)
	}
}

func TestPumpAgedBulkHandoffCoalescesFollowingReadBurst(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	p := &Pump{ctx: ctx, fastReserve: true,
		dataFast: make(chan *Job, dataFastQueueRecords),
		dataBulk: make(chan *Job, dataBulkQueueRecords)}
	job := func(id uint64) *Job {
		return &Job{generation: 1, attempt: flow.Attempt{ID: id, PayloadBytes: 32 << 10},
			frame: wire.Frame{Type: wire.Data, Stream: 1, Logical: id}, ctx: ctx,
			claimed: new(atomic.Bool), bulk: true, queuedAt: time.Now().Add(-time.Second)}
	}
	p.dataBulk <- job(1)
	// The following socket read becomes schedulable just after an old handoff
	// reaches the collector. Its queue age must not force one BODY per record.
	producer := make(chan struct{})
	go func() {
		defer close(producer)
		time.Sleep(5 * time.Millisecond)
		for id := uint64(2); id <= 11; id++ {
			p.dataBulk <- job(id)
		}
	}()
	batch, ok := p.collectDataBatch()
	<-producer
	if !ok || len(batch) != 11 {
		t.Fatalf("aged bulk handoff fragmented the next read burst: got %d records, want 11", len(batch))
	}
}

type orderedAdmissionTestLink struct {
	*eventLink
	admitted     chan uint64
	physical     chan uint64
	releaseFirst chan struct{}
}

func (l *orderedAdmissionTestLink) AdmitBatch(jobs []*Job) func() []Completion {
	id := jobs[0].Attempt().ID
	l.admitted <- id
	return func() []Completion {
		l.physical <- id
		if id == 1 {
			<-l.releaseFirst
		}
		results := make([]Completion, len(jobs))
		for i, job := range jobs {
			results[i] = job.RejectBeforeCommit(context.Canceled)
		}
		return results
	}
}
func (l *orderedAdmissionTestLink) WriteBatch(jobs []*Job) []Completion {
	// Deliberately reverse worker arrival after FIFO collection. Ordered
	// admission must happen before this parallel physical stage is scheduled.
	if jobs[0].Attempt().ID == 1 {
		time.Sleep(10 * time.Millisecond)
	}
	return l.AdmitBatch(jobs)()
}

func TestPumpOrdersBatchAdmissionWhilePhysicalWritesRemainParallel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	l := &orderedAdmissionTestLink{eventLink: &eventLink{}, admitted: make(chan uint64, 2),
		physical: make(chan uint64, 2), releaseFirst: make(chan struct{})}
	p := &Pump{ctx: ctx, link: l, fastReserve: true, events: make(chan pumpEvent, 2),
		dataFast: make(chan *Job, dataFastQueueRecords), dataBulk: make(chan *Job, dataBulkQueueRecords)}
	for id := uint64(1); id <= 22; id++ {
		p.dataBulk <- &Job{ctx: ctx, claimed: new(atomic.Bool), bulk: true,
			attempt: flow.Attempt{ID: id, Stream: (id-1)%11 + 1, Offset: (id - 1) / 11 * wire.MaxData, PayloadBytes: wire.MaxData},
			frame:   wire.Frame{Type: wire.Data, Stream: (id-1)%11 + 1}}
	}
	var workers sync.WaitGroup
	defer func() { close(l.releaseFirst); cancel(); workers.Wait() }()
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() { defer workers.Done(); p.writeDataLoop(l, true) }()
	}
	for _, want := range []uint64{1, 12} {
		select {
		case got := <-l.admitted:
			if got != want {
				t.Fatalf("multiplexed prefixes admitted out of collection order: got first job %d, want %d", got, want)
			}
		case <-ctx.Done():
			t.Fatal("batch admission stalled")
		}
	}
	for i := 0; i < 2; i++ {
		select {
		case <-l.physical:
		case <-ctx.Done():
			t.Fatal("one blocked physical write serialized its sibling")
		}
	}
}

type parkedRetryLink struct {
	*eventLink
	wake   <-chan struct{}
	parked atomic.Bool
}

func (l *parkedRetryLink) Write(j *Job) Completion {
	if j.Attempt().Stream != 1 || !l.parked.CompareAndSwap(false, true) {
		return l.eventLink.Write(j)
	}
	f := j.frame
	f.Payload = append([]byte(nil), f.Payload...)
	r := j.RejectBeforeCommit(errors.New("admission deferred"))
	r.NonFatal, r.RetryWake = true, l.wake
	l.written <- f
	return r
}

func TestPumpDeferredAdmissionParksUntilWakeAndKeepsSiblingLive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	wake := make(chan struct{})
	l := &parkedRetryLink{eventLink: &eventLink{inputs: make(chan Input, 4), written: make(chan wire.Frame, 8)}, wake: wake}
	p, err := StartPump(ctx, PumpConfig{Engine: DefaultConfig(1, []string{"a01"}), Account: "a01", ControlQueue: 8}, l)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	apps := make(map[uint64]*net.TCPConn)
	for _, id := range []uint64{1, 3} {
		app, sock := localTCPPair(t)
		apps[id] = app
		if err = p.Add(ctx, id, sock); err != nil {
			t.Fatal(err)
		}
		credit, _ := wire.EncodeControl(id, wire.Credit{MaxStream: 128, MaxData: 256})
		l.inputs <- Input{Generation: 1, Frame: &credit}
	}
	payload := []byte("parked byte identity")
	if _, err = apps[1].Write(payload); err != nil {
		t.Fatal(err)
	}
	select {
	case f := <-l.written:
		if f.Stream != 1 || !bytes.Equal(f.Payload, payload) {
			t.Fatal("first deferred handoff", f)
		}
	case <-ctx.Done():
		t.Fatal("first handoff missing")
	}
	if _, err = apps[3].Write([]byte("sibling progresses")); err != nil {
		t.Fatal(err)
	}
	select {
	case f := <-l.written:
		if f.Stream != 3 {
			t.Fatal("deferred item spun before sibling", f.Stream)
		}
	case <-ctx.Done():
		t.Fatal("parked item blocked sibling")
	}
	select {
	case f := <-l.written:
		t.Fatal("retry before predicate changed", f.Stream)
	case <-time.After(120 * time.Millisecond):
	}
	close(wake)
	select {
	case f := <-l.written:
		if f.Stream != 1 || f.Offset != 0 || !bytes.Equal(f.Payload, payload) {
			t.Fatal("wake changed byte identity", f)
		}
	case <-ctx.Done():
		t.Fatal("wake lost")
	}
}

func TestPumpDeferredAdmissionCancellationJoinsWithoutWake(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := &parkedRetryLink{eventLink: &eventLink{inputs: make(chan Input, 2), written: make(chan wire.Frame, 2)}, wake: make(chan struct{})}
	p, err := StartPump(ctx, PumpConfig{Engine: DefaultConfig(1, []string{"a01"}), Account: "a01", ControlQueue: 8}, l)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	app, sock := localTCPPair(t)
	if err = p.Add(ctx, 1, sock); err != nil {
		t.Fatal(err)
	}
	credit, _ := wire.EncodeControl(1, wire.Credit{MaxStream: 128, MaxData: 128})
	l.inputs <- Input{Generation: 1, Frame: &credit}
	if _, err = app.Write([]byte("cancel parked item")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-l.written:
	case <-time.After(time.Second):
		t.Fatal("first handoff missing")
	}
	cancel()
	select {
	case <-p.Done():
		if !errors.Is(p.Wait(), context.Canceled) {
			t.Fatal(p.Wait())
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation waited for admission wake")
	}
}

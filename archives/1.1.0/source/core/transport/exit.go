package transport

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
)

// ExitPolicy must resolve and authorize ALL returned address information for
// this authenticated user. The dialer receives only that pinned numeric target,
// preventing a second DNS lookup from changing the authorized destination.
type ExitPolicy interface {
	Authorize(context.Context, string, Target) (netip.AddrPort, error)
}
type ExitPolicyFunc func(context.Context, string, Target) (netip.AddrPort, error)

func (f ExitPolicyFunc) Authorize(ctx context.Context, user string, target Target) (netip.AddrPort, error) {
	return f(ctx, user, target)
}

type ExitServer struct {
	session *Session
	user    string
	policy  ExitPolicy
	dialer  NetworkDialer
	workers int
	mu      sync.Mutex
	running bool
	active  map[*Conn]net.Conn
	bridges sync.WaitGroup
	runtime *Runtime
}

func NewExitServer(s *Session, cipher *Cipher, receiveWindow uint64, workers int, policy ExitPolicy, dialer NetworkDialer) (*ExitServer, error) {
	if s == nil || cipher == nil || workers < 1 || workers > 64 || policy == nil || dialer == nil {
		return nil, ErrProtocol
	}
	if err := s.configureOpen(2, receiveWindow, cipher); err != nil {
		return nil, err
	}
	return &ExitServer{session: s, user: cipher.epoch.Context().User, policy: policy, dialer: dialer, workers: workers, active: make(map[*Conn]net.Conn)}, nil
}

type incomingOpen struct {
	session *Session
	flow    *stream
	ctx     context.Context
	cancel  context.CancelFunc
}

func (s *Session) nextOpen(ctx context.Context) (*incomingOpen, error) {
	for {
		s.mu.Lock()
		if s.closed || ctx.Err() != nil {
			s.mu.Unlock()
			return nil, ErrClosed
		}
		for _, id := range s.order {
			f := s.flows[id]
			if f.opening != openAwaitExit || f.openClaimed || f.stopped() {
				continue
			}
			child, cancel := context.WithCancel(ctx)
			f.openClaimed, f.openCancel = true, cancel
			request := &incomingOpen{session: s, flow: f, ctx: child, cancel: cancel}
			s.mu.Unlock()
			return request, nil
		}
		wake := s.openChanged
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-wake:
		}
	}
}

func (r *incomingOpen) decide(failure OpenFailure) (*Conn, error) {
	s := r.session
	s.mu.Lock()
	f := r.flow
	if s.closed || s.flows[f.id] != f || f.stopped() || f.opening != openAwaitExit || r.ctx.Err() != nil {
		s.mu.Unlock()
		return nil, ErrClosed
	}
	f.openCancel = nil
	f.openPending = true
	f.openFrame = Record{Kind: OpenOK, Generation: s.generation, Stream: f.id, ReceiveLimit: f.receiveLimit()}
	f.opening = openEstablished
	if failure != 0 {
		s.dropLocked(f)
		f.opening, f.applicationClosed = openRejected, true
		f.openFrame = Record{Kind: OpenError, Generation: s.generation, Stream: f.id, Payload: []byte{byte(failure)}}
	}
	f.signal()
	s.signalOutgoing()
	s.mu.Unlock()
	if failure != 0 {
		return nil, failure
	}
	return s.Conn(f.id)
}

// Run owns a bounded policy/dial worker pool and per-stream TCP bridges. It
// never performs DNS, target dialing or copying while holding the session lock.
// Context cancellation closes exits/application streams, never IMAP carriers.
func (e *ExitServer) Run(ctx context.Context) error {
	return e.run(ctx, nil)
}

func (e *ExitServer) run(ctx context.Context, runtime *Runtime) error {
	e.session.mu.Lock()
	if e.session.closed || e.session.runtime != runtime {
		e.session.mu.Unlock()
		return ErrProtocol
	}
	e.mu.Lock()
	if e.running || e.runtime != runtime {
		e.mu.Unlock()
		e.session.mu.Unlock()
		return ErrProtocol
	}
	e.running = true
	e.session.services++
	e.mu.Unlock()
	e.session.mu.Unlock()
	if runtime != nil {
		runtime.serviceStarted(nil, false)
	}
	defer func() {
		e.mu.Lock()
		e.running = false
		e.mu.Unlock()
		e.session.mu.Lock()
		e.session.services--
		e.session.mu.Unlock()
	}()
	var workers sync.WaitGroup
	for range e.workers {
		workers.Go(func() {
			for {
				request, err := e.session.nextOpen(ctx)
				if err != nil {
					return
				}
				e.connect(request)
			}
		})
	}
	workers.Wait()
	e.mu.Lock()
	type activeExit struct {
		stream   *Conn
		upstream net.Conn
	}
	active := make([]activeExit, 0, len(e.active))
	for stream, upstream := range e.active {
		active = append(active, activeExit{stream, upstream})
	}
	e.mu.Unlock()
	for _, connection := range active {
		connection.stream.Close()
		connection.upstream.Close()
	}
	e.bridges.Wait()
	return ctx.Err()
}

func (e *ExitServer) connect(r *incomingOpen) {
	defer r.cancel() // establishment context is independent of the TCP lifetime
	address, err := e.policy.Authorize(r.ctx, e.user, r.flow.openTarget)
	if err != nil || !address.IsValid() || address.Port() != r.flow.openTarget.Port || address.Addr().Zone() != "" {
		r.decide(OpenDenied)
		return
	}
	if r.ctx.Err() != nil {
		return
	}
	upstream, err := e.dialer.DialContext(r.ctx, "tcp", address.String())
	if err != nil {
		r.decide(OpenUnreachable)
		return
	}
	conn, err := r.decide(0)
	if err != nil {
		upstream.Close()
		return
	}
	e.mu.Lock()
	e.active[conn] = upstream
	e.mu.Unlock()
	e.bridges.Go(func() {
		defer func() {
			conn.Close()
			upstream.Close()
			e.mu.Lock()
			delete(e.active, conn)
			e.mu.Unlock()
		}()
		var copyBoth sync.WaitGroup
		copyBoth.Go(func() {
			_, err := io.Copy(upstream, conn)
			if err != nil && !errors.Is(err, net.ErrClosed) {
				upstream.Close()
				conn.Close()
				return
			}
			if half, ok := upstream.(interface{ CloseWrite() error }); ok {
				half.CloseWrite()
			} else {
				upstream.Close()
			}
		})
		_, err := io.Copy(conn, upstream)
		if err != nil {
			conn.Close()
			upstream.Close()
		} else {
			conn.CloseWrite()
		}
		copyBoth.Wait()
	})
}

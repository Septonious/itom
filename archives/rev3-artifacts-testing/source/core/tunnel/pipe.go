package tunnel

import (
	"net"
	"sync"
	"time"
)

// Separate directional pipes preserve TCP half-close without a localhost socket.
type pipe struct {
	stream uint64
	r, w   net.Conn
	once   sync.Once
	closed func()
}

func pipes() (*pipe, *pipe) {
	ar, bw := net.Pipe()
	br, aw := net.Pipe()
	return &pipe{r: ar, w: aw}, &pipe{r: br, w: bw}
}
func (p *pipe) StreamID() uint64 { return p.stream }

func (p *pipe) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *pipe) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipe) CloseWrite() error           { return p.w.Close() }
func (p *pipe) Close() error {
	p.once.Do(func() {
		p.r.Close()
		p.w.Close()
		if p.closed != nil {
			p.closed()
		}
	})
	return nil
}
func (p *pipe) LocalAddr() net.Addr                { return p.r.LocalAddr() }
func (p *pipe) RemoteAddr() net.Addr               { return p.r.RemoteAddr() }
func (p *pipe) SetReadDeadline(t time.Time) error  { return p.r.SetReadDeadline(t) }
func (p *pipe) SetWriteDeadline(t time.Time) error { return p.w.SetWriteDeadline(t) }
func (p *pipe) SetDeadline(t time.Time) error      { p.r.SetReadDeadline(t); return p.w.SetWriteDeadline(t) }

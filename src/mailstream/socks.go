package mailstream

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"time"
)

// ServeSOCKS exposes TCP CONNECT on a caller-owned loopback listener. UDP and
// authentication extensions are deliberately not claimed by this prototype.
// The Android bridge must keep its existing DNS/direct-resolver policy until
// this replacement has the corresponding explicit datagram integration.
func ServeSOCKS(ctx context.Context, listener net.Listener, session *Session) error {
	stop := context.AfterFunc(ctx, func() { listener.Close() })
	defer stop()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		go func() { defer conn.Close(); _ = handleSOCKS(ctx, conn, session) }()
	}
}
func handleSOCKS(ctx context.Context, conn net.Conn, session *Session) error {
	if err := conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		return err
	}
	var greeting [2]byte
	if _, err := io.ReadFull(conn, greeting[:]); err != nil {
		return err
	}
	if greeting[0] != 5 || greeting[1] == 0 {
		return ErrProtocol
	}
	methods := make([]byte, int(greeting[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return err
	}
	allowed := false
	for _, method := range methods {
		allowed = allowed || method == 0
	}
	if !allowed {
		_, _ = conn.Write([]byte{5, 255})
		return ErrProtocol
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil {
		return err
	}
	var request [4]byte
	if _, err := io.ReadFull(conn, request[:]); err != nil {
		return err
	}
	if request[0] != 5 || request[2] != 0 {
		return ErrProtocol
	}
	if request[1] != 1 {
		_, _ = conn.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0})
		return ErrProtocol
	}
	var address string
	switch request[3] {
	case 1:
		b := make([]byte, 4)
		if _, err := io.ReadFull(conn, b); err != nil {
			return err
		}
		address = net.IP(b).String()
	case 4:
		b := make([]byte, 16)
		if _, err := io.ReadFull(conn, b); err != nil {
			return err
		}
		address = net.IP(b).String()
	case 3:
		var n [1]byte
		if _, err := io.ReadFull(conn, n[:]); err != nil {
			return err
		}
		if n[0] == 0 {
			return ErrProtocol
		}
		b := make([]byte, int(n[0]))
		if _, err := io.ReadFull(conn, b); err != nil {
			return err
		}
		address = string(b)
	default:
		return ErrProtocol
	}
	var portBytes [2]byte
	if _, err := io.ReadFull(conn, portBytes[:]); err != nil {
		return err
	}
	port := binary.BigEndian.Uint16(portBytes[:])
	if port == 0 {
		return ErrProtocol
	}
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	peer, err := session.OpenTCP(dialCtx, net.JoinHostPort(address, strconv.Itoa(int(port))))
	if err != nil {
		_, _ = conn.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return err
	}
	defer peer.Close()
	if _, err = conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return err
	}
	if err = conn.SetDeadline(time.Time{}); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close(); peer.Close() })
	defer stop()
	bridge(conn, peer)
	return nil
}

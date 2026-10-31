package transport_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestWireRoutePropagatesHalfCloseAndSurvivesDialContextCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		c, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer c.Close()
		body, err := io.ReadAll(c)
		if err == nil {
			_, err = c.Write(body)
		}
		done <- err
	}()
	route := newWireRoute([]time.Duration{80 * time.Millisecond}, time.Second, 8_000_000)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := route.DialContext(ctx, "tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cancel()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	payload := bytes.Repeat([]byte{17}, 128)
	started := time.Now()
	if _, err = c.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err = c.(*wireConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(c)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatal("wire route lost reverse bytes at half close", len(got), err)
	}
	if elapsed := time.Since(started); elapsed < 80*time.Millisecond {
		t.Fatal("wire propagation delay was bypassed", elapsed)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWireRouteStreamsWithoutChargingOneRTTPerRead(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	payload := bytes.Repeat([]byte{3, 7, 19, 255}, 512<<10)
	done := make(chan error, 1)
	go func() {
		c, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer c.Close()
		_, err = io.Copy(c, c)
		done <- err
	}()
	route := newWireRoute([]time.Duration{80 * time.Millisecond}, time.Second, 8_000_000)
	c, err := route.DialContext(context.Background(), "tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	sent := make(chan error, 1)
	started := time.Now()
	go func() {
		_, err := c.Write(payload)
		if err == nil {
			err = c.(*wireConn).CloseWrite()
		}
		sent <- err
	}()
	got, err := io.ReadAll(c)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatal("wire stream corrupted/stalled", len(got), err)
	}
	if err = <-sent; err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	if elapsed < 80*time.Millisecond+time.Duration(uint64(len(payload))*uint64(time.Second)/8_000_000) {
		t.Fatal("wire serialization budget was bypassed", elapsed)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	t.Logf("wire pipeline payload_bytes=%d elapsed=%s explicit_bytes_per_second=%d", len(payload), elapsed, route.bytesPerSecond)
}

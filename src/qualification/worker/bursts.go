package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

type lightPhase struct {
	Name    string    `json:"name"`
	Streams int       `json:"streams"`
	Rate    int       `json:"offered_bps"`
	Bytes   int       `json:"verified_echo_bytes"`
	Elapsed float64   `json:"elapsed_s"`
	App     []float64 `json:"application_ms"`
}

// Keep the probe connection alive while new application streams open and close
// together. During the quiet interval every workload stream has been closed.
func lightWorkload(ctx context.Context, cycles int, dial func(context.Context) (net.Conn, error), ping func() (float64, error), stage func(string), results *[]lightPhase) error {
	for range cycles {
		for _, step := range []struct{ bps, streams int }{{100000, 1}, {500000, 4}, {2000000, 8}, {5000000, 4}} {
			for _, active := range []bool{true, false} {
				name := fmt.Sprintf("idle_after_%dk", step.bps/1000)
				phase := lightPhase{Name: name}
				if active {
					phase.Name = fmt.Sprintf("burst_%dk", step.bps/1000)
					phase.Rate, phase.Streams = step.bps, step.streams
				}
				stage(phase.Name)
				start := time.Now()
				complete := make(chan error, 1)
				if active {
					go func() { complete <- echoBurst(ctx, step.bps, step.streams, dial) }()
				} else {
					go func() {
						timer := time.NewTimer(3 * time.Second)
						defer timer.Stop()
						select {
						case <-timer.C:
							complete <- nil
						case <-ctx.Done():
							complete <- ctx.Err()
						}
					}()
				}
				for {
					ms, err := ping()
					if err != nil {
						return err
					}
					phase.App = append(phase.App, ms)
					select {
					case <-ctx.Done():
						return ctx.Err()
					case err = <-complete:
						if err != nil {
							return err
						}
						phase.Elapsed = time.Since(start).Seconds()
						if active {
							phase.Bytes = 3 * step.bps / 8
						}
						*results = append(*results, phase)
					case <-time.After(75 * time.Millisecond):
						continue
					}
					break
				}
			}
		}
	}
	return nil
}

func echoBurst(ctx context.Context, bps, streams int, dial func(context.Context) (net.Conn, error)) error {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, streams)
	total := 3 * bps / 8
	for i := range streams {
		n := total*(i+1)/streams - total*i/streams
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := dial(ctx)
			if err == nil {
				stop := context.AfterFunc(ctx, func() { conn.Close() })
				err = echoStream(ctx, conn, n)
				stop()
				conn.Close()
			}
			if err != nil {
				errs <- err
				cancel()
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		return err
	}
	return nil
}

// Verify the returned payload while transmitting to leave real data in flight.
func echoStream(ctx context.Context, conn net.Conn, total int) error {
	const ticks = 150
	reply := make(chan error, 1)
	defer conn.Close()
	go func() {
		buffer := make([]byte, 16<<10)
		offset := 0
		for offset < total {
			n, err := io.ReadFull(conn, buffer[:min(len(buffer), total-offset)])
			if err != nil {
				reply <- err
				return
			}
			if !valid(buffer[:n], uint64(offset)) {
				reply <- fmt.Errorf("burst echo corrupted at byte %d", offset)
				return
			}
			offset += n
		}
		reply <- nil
	}()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	body := make([]byte, (total+ticks-1)/ticks)
	sent := 0
	for i := 1; i <= ticks; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-reply:
			return fmt.Errorf("burst terminated early: %v", err)
		case <-ticker.C:
		}
		part := body[:total*i/ticks-sent]
		fill(part, uint64(sent))
		n, err := conn.Write(part)
		if err != nil {
			return err
		}
		if n != len(part) {
			return io.ErrShortWrite
		}
		sent += n
	}
	select {
	case err := <-reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

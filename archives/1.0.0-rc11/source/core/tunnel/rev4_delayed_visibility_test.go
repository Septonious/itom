package tunnel

import (
	"net"
	"sync"
	"testing"
	"time"
)

func TestRev4DelayedBodiesAndReaderRestartsKeepEveryTCPStream(t *testing.T) {
	t.Setenv("ITOM_LOAD_VISIBILITY_DELAY_MS", "1800")
	// Combine installed UID history with current replica lag and role failure.
	// Historical allocation/deletion completes before the measured session.
	t.Setenv("ITOM_LOAD_UID_HISTORY", "64")
	f := rev4FieldFixture(t, false)
	chat := rev4Open(t, f)
	if err := rev4Exchange(chat, 1, 4096, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	logical, generation := f.client.logicalSession, f.client.generation
	var wg sync.WaitGroup
	var debugOnce sync.Once
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		p := rev4Open(t, f)
		wg.Add(1)
		go func(i int, p net.Conn) {
			defer wg.Done()
			defer p.Close()
			if err := rev4Exchange(p, 10+i, 1<<20, 20*time.Second); err != nil {
				debugOnce.Do(func() { rev4RecoveryFailure(t, f) })
				failures <- err
			}
		}(i, p)
	}
	if err := pause(f.ctx, time.Second); err != nil {
		t.Fatal(err)
	}
	// Close the selected DATA roles only. CONTROL, logical actors and target
	// sockets stay alive while allocated but invisible UIDs must be rediscovered.
	f.client.mu.Lock()
	for _, l := range f.client.lanes {
		if reader := physicalMail(l.mail).dataReaderRef(); reader != nil {
			reader.Close()
		}
	}
	f.client.mu.Unlock()
	for i := 0; i < 6; i++ {
		if err := rev4Exchange(chat, 100+i, 4096, 3*time.Second); err != nil {
			t.Fatal("messages blocked by DATA repair", err)
		}
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error("TCP data did not recover intact", err)
	}
	if f.network.visibility.suppressed.Load() == 0 {
		t.Fatal("visibility fault was not exercised")
	}
	if f.client.logicalSession != logical || f.client.generation != generation {
		t.Fatal("DATA role restart replaced logical session")
	}
	if err := rev4Exchange(chat, 200, 128<<10, 5*time.Second); err != nil {
		t.Fatal("application stayed dead after DATA recovery", err)
	}
}

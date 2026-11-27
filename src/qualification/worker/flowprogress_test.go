package main

import (
	"sync"
	"testing"
	"time"
)

func TestStreamProgressTracksVerifiedTailAndTerminalStages(t *testing.T) {
	c := clock{start: time.Now(), firstByStream: map[string]float64{}}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); c.add(1024, "0") }()
	}
	wg.Wait()
	c.noteStreamStage("0", "writer_done", 8192)
	c.noteStreamStage("0", "origin_eof", 8192)
	c.noteStreamStage("0", "response_verified", 8192)
	c.add(512, "1")
	got := c.result()["stream_progress"].(map[string]flowProgress)
	if got["0"].VerifiedBytes != 8192 || got["0"].WriterBytes == nil || *got["0"].WriterBytes != 8192 || got["0"].OriginBytes == nil || *got["0"].OriginBytes != 8192 || got["0"].ResponseVerifiedMS == nil {
		t.Fatalf("complete flow: %+v", got["0"])
	}
	if got["1"].VerifiedBytes != 512 || got["1"].WriterDoneMS != nil || got["1"].OriginEOFMS != nil || got["1"].ResponseVerifiedMS != nil {
		t.Fatalf("incomplete flow: %+v", got["1"])
	}
	c.add(512, "1")
	if got["1"].VerifiedBytes != 512 {
		t.Fatal("snapshot changed")
	}
}

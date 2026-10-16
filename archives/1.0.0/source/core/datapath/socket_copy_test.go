package datapath

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

type failingCopyReader struct{ err error }

func (r failingCopyReader) Read(b []byte) (int, error) { copy(b, "abc"); return 3, r.err }

type failingCopyWriter struct {
	err   error
	short bool
}

func (w failingCopyWriter) Write(b []byte) (int, error) {
	if w.short {
		return len(b) - 1, nil
	}
	return 0, w.err
}

func TestSocketCopyIdentifiesReadWriteEOFAndPartialBytes(t *testing.T) {
	failure := errors.New("sentinel")
	var out bytes.Buffer
	n, err, stage := CopySocket(&out, failingCopyReader{failure}, make([]byte, 32))
	if n != 3 || !errors.Is(err, failure) || stage != "read" || out.String() != "abc" {
		t.Fatal(n, err, stage, out.String())
	}
	n, err, stage = CopySocket(failingCopyWriter{err: failure}, bytes.NewBufferString("abc"), make([]byte, 32))
	if n != 0 || !errors.Is(err, failure) || stage != "write" {
		t.Fatal(n, err, stage)
	}
	n, err, stage = CopySocket(failingCopyWriter{short: true}, bytes.NewBufferString("abc"), make([]byte, 32))
	if n != 2 || !errors.Is(err, io.ErrShortWrite) || stage != "write" {
		t.Fatal(n, err, stage)
	}
	out.Reset()
	n, err, stage = CopySocket(&out, bytes.NewBufferString("abc"), make([]byte, 32))
	if n != 3 || err != nil || stage != "eof" || out.String() != "abc" {
		t.Fatal(n, err, stage, out.String())
	}
}

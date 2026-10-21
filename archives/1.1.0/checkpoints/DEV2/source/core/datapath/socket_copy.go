package datapath

import "io"

type copyReader struct{ io.Reader }
type copyWriter struct {
	io.Writer
	failed bool
}

func (w *copyWriter) Write(b []byte) (int, error) {
	n, err := w.Writer.Write(b)
	w.failed = w.failed || err != nil || n != len(b)
	return n, err
}

// CopySocket preserves io.CopyBuffer's byte/error/half-close contract while
// observing which side failed. Wrappers deliberately expose only Read/Write;
// optional WriterTo/ReaderFrom bypasses would hide the failing operation.
func CopySocket(dst io.Writer, src io.Reader, buf []byte) (n int64, err error, stage string) {
	w := &copyWriter{Writer: dst}
	n, err = io.CopyBuffer(w, copyReader{src}, buf)
	stage = "eof"
	if w.failed {
		stage = "write"
	} else if err != nil {
		stage = "read"
	}
	return
}

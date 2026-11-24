package mobile

import "io"

// The bridge records the application copy boundary independently of carrier IO.
func copyApplicationSocket(dst io.Writer, src io.Reader, buffer []byte) (int64, error, string) {
	n, err := io.CopyBuffer(dst, src, buffer)
	return n, err, "application_copy"
}

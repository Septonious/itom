package tunnel

import (
	"context"
	"itom.local/core/mailbox"
	"testing"
	"time"
)

type blockedObservedStream struct {
	mailbox.Transport
	release <-chan struct{}
	row     mailbox.Message
}

func (m blockedObservedStream) PageStream(ctx context.Context, folder string, cursor mailbox.Cursor, emit func(mailbox.Message) error) ([]mailbox.Message, mailbox.Cursor, error) {
	if err := emit(m.row); err != nil {
		return nil, cursor, err
	}
	select {
	case <-m.release:
		return []mailbox.Message{m.row}, mailbox.Cursor{Validity: 1, After: 1}, nil
	case <-ctx.Done():
		return nil, cursor, ctx.Err()
	}
}
func TestObservedReaderDoesNotHideStreamingBehindDiagnostics(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release := make(chan struct{})
	raw, _ := mailbox.Encode([]byte("complete-body-before-next-large-literal"))
	transport := &observedMail{Transport: blockedObservedStream{release: release, row: mailbox.Message{Folder: "itom-test", Validity: 1, UID: 1, MIME: raw}}, d: &diagnostic{}}
	var exposed mailbox.Transport = transport
	reader, ok := exposed.(interface {
		PageStream(context.Context, string, mailbox.Cursor, func(mailbox.Message) error) ([]mailbox.Message, mailbox.Cursor, error)
	})
	if !ok {
		t.Fatal("production diagnostics hides streaming interface")
	}
	emitted := make(chan uint32, 1)
	done := make(chan error, 1)
	go func() {
		_, _, err := reader.PageStream(ctx, "itom-test", mailbox.Cursor{}, func(row mailbox.Message) error { emitted <- row.UID; return nil })
		done <- err
	}()
	select {
	case uid := <-emitted:
		if uid != 1 {
			t.Fatal("bad early UID")
		}
	case <-ctx.Done():
		t.Fatal("complete row waits for later literal")
	}
	select {
	case err := <-done:
		t.Fatal("FETCH returned before tagged completion", err)
	default:
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("FETCH completion leaked")
	}
}

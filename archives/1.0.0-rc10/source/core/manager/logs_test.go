package manager

import (
	"context"
	"reflect"
	"testing"
)

func TestLogCommandFollowsWithoutBufferedHistory(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := liveLogCommand(ctx)
	want := []string{"journalctl", "--unit=itom.service", "--follow", "--lines=0", "--no-pager", "--output=short-iso"}
	if !reflect.DeepEqual(c.Args, want) || c.Cancel == nil {
		t.Fatal(c.Args)
	}
}

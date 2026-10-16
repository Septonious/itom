package main

import "testing"

func TestVersionAndUnknownCommand(t *testing.T) {
	if e := run([]string{"version"}); e != nil {
		t.Fatal(e)
	}
	if e := run([]string{"not-a-command"}); e == nil {
		t.Fatal("unknown command accepted")
	}
}

package manager

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestMenuNumericExitAndEOF(t *testing.T) {
	for _, input := range []string{"0\n", "invalid\n0\n", ""} {
		var out bytes.Buffer
		u := &UI{In: bufio.NewReader(strings.NewReader(input)), Out: &out, State: t.TempDir()}
		if e := u.Run(); e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(out.String(), "ITOM — server manager") || !strings.Contains(out.String(), "Clients") {
			t.Fatal(out.String())
		}
	}
}

func TestVisiblePasswordPreservesSpacesAndNewline(t *testing.T) {
	var out bytes.Buffer
	u := &UI{In: bufio.NewReader(strings.NewReader("  visible password  \r\nnext\n")), Out: &out}
	value, err := u.password()
	if err != nil || value != "  visible password  " || strings.Contains(out.String(), "hidden") {
		t.Fatal(value, err)
	}
	next, err := u.ask("next")
	if err != nil || next != "next" {
		t.Fatal("password read consumed next input", err)
	}
}

package wire

import (
	"bytes"
	"testing"
)

func TestRev9EnvelopeSingleDigestRejectsEveryByteMutation(t *testing.T) {
	report, body, _ := envelopeFixture(t)
	for i := range body {
		bad := bytes.Clone(body)
		bad[i] ^= 1
		if _, err := DataEnvelopePayload(bad, report); err == nil {
			t.Fatalf("mutation at %d passed full descriptor proof", i)
		}
	}
	if _, err := DataEnvelopePayload(body, report); err != nil {
		t.Fatal(err)
	}
	changed := report
	d := *report.Attempt
	changed.Attempt = &d
	changed.Total.Bytes++
	if _, err := DataEnvelopePayload(body, changed); err == nil {
		t.Fatal("cumulative quota binding bypassed")
	}
	d.Folder = "other-folder"
	changed = report
	changed.Attempt = &d
	if _, err := DataEnvelopePayload(body, changed); err == nil {
		t.Fatal("folder binding bypassed")
	}
}

package tunnel

import (
	"itom.local/mailstream"
	"testing"
)

func TestRC13PhysicalOverlapDoesNotExceedStableGrant(t *testing.T) {
	physical, serving := 0, 0
	for i := 0; i < 10; i++ {
		line := mailstream.MailAccountSnapshot{Allocated: i < 8, PhysicalSockets: 4}
		if i >= 8 {
			line.PhysicalSockets = 1
			line.Failed = true
		}
		physical += line.PhysicalSockets
		serving += servingSocketCount(line)
	}
	if physical != 34 || serving != 32 {
		t.Fatal(physical, serving)
	}
	if servingSocketCount(mailstream.MailAccountSnapshot{Allocated: true, PhysicalSockets: 4, ScaleBlocked: true}) != 0 {
		t.Fatal("parked actor counted")
	}
}

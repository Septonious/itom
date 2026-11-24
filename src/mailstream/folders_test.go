package mailstream

import (
	"strings"
	"testing"
)

func TestCompactFolderNamesAndOwnership(t *testing.T) {
	client := &IMAPLink{identity: "compact-folders", client: true}
	server := &IMAPLink{identity: client.identity}
	current, old := SessionID{1}, SessionID{2}
	// This token contains '-' in base64url: parsing must use fixed widths.
	old[0], old[1], old[2] = 251, 239, 190
	for _, out := range []bool{false, true} {
		control := client.controlFolder(out)
		if len(control) != 31 || !strings.HasPrefix(control, "itom-") || control != server.controlFolder(!out) {
			t.Fatal("control namespace mismatch", control)
		}
		if client.obsoleteFolder(control, current) {
			t.Fatal("control classified as historical DATA")
		}
		for slot := uint32(0); slot < dataSlots; slot++ {
			name := client.segmentFolder(old, out, slot)
			if len(name) != 55 || name != server.segmentFolder(old, !out, slot) || !client.obsoleteFolder(name, current) {
				t.Fatal("DATA name mismatch", name)
			}
			if client.obsoleteFolder(client.segmentFolder(current, out, slot), current) {
				t.Fatal("current DATA classified as historical")
			}
		}
	}
	base := client.dataFolder(old, true)
	for _, name := range []string{base + "-40", base + "-zz", base + "-00000000", base + "-00-extra", "itom3-" + base, (&IMAPLink{identity: "foreign", client: true}).segmentFolder(old, true, 0), client.segmentFolder(SessionID{}, true, 0)} {
		if client.obsoleteFolder(name, current) {
			t.Fatal("unowned/malformed name accepted", name)
		}
	}
	other := old
	other[15]++
	if client.segmentFolder(old, true, 0) == client.segmentFolder(other, true, 0) {
		t.Fatal("session token was truncated")
	}
}

func TestCompactCapacityStoreSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	account := MailAccount{Host: "imap.example.org", Port: 993, Username: "account"}
	store, err := openRetention(dir, "compact-store", true)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.put(retentionKey(account), 4096); err != nil {
		t.Fatal(err)
	}
	if err = store.close(); err != nil {
		t.Fatal(err)
	}
	store, err = openRetention(dir, "compact-store", true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	if debt, err := store.load(retentionKey(account)); err != nil || debt != 4096 {
		t.Fatal("new layout lost its durable charge", debt, err)
	}
}

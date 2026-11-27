package mailbox

import (
	"context"
	"errors"
	imap "github.com/emersion/go-imap/v2"
	"io"
	"slices"
	"strings"
	"testing"
)

type purgeFixture struct {
	folders                        map[string]Folder
	deleted, emptied, unsubscribed []string
	faults                         map[string]bool
	removedOnFault                 map[string]bool
	broken                         bool
	reopens, lists                 int
	recreateSystem                 bool
	messages                       map[string]int
	emptyFails                     map[string]bool
}

func (f *purgeFixture) Folders(context.Context) ([]Folder, error) {
	if f.broken {
		return nil, io.ErrUnexpectedEOF
	}
	f.lists++
	return f.list(), nil
}
func (f *purgeFixture) check(name string) error {
	if f.broken {
		return errors.New("reused broken socket")
	}
	if protectedPurgeFolders(f.list())[name] {
		return errors.New("protected folder touched: " + name)
	}
	return nil
}
func (f *purgeFixture) list() []Folder {
	var out []Folder
	for _, v := range f.folders {
		out = append(out, v)
	}
	return out
}
func (f *purgeFixture) deleteFolder(_ context.Context, name string) error {
	if e := f.check(name); e != nil {
		return e
	}
	f.deleted = append(f.deleted, name)
	if f.recreateSystem && retainSystemFolder(f.folders[name]) {
		return nil
	}
	if f.faults[name] {
		f.broken = true
		if f.removedOnFault[name] {
			delete(f.folders, name)
		}
		return io.ErrUnexpectedEOF
	}
	if name == "Trash" {
		return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "system folder"}
	}
	delete(f.folders, name)
	if f.recreateSystem && f.messages != nil {
		f.messages["Trash"]++
	}
	return nil
}
func (f *purgeFixture) unsubscribeFolder(_ context.Context, name string) error {
	if e := f.check(name); e != nil {
		return e
	}
	f.unsubscribed = append(f.unsubscribed, name)
	return nil
}
func (f *purgeFixture) empty(_ context.Context, name string) error {
	if e := f.check(name); e != nil {
		return e
	}
	f.emptied = append(f.emptied, name)
	if f.emptyFails[name] {
		return errors.New("IMAP_MAILBOX_NOT_EMPTY")
	}
	if f.messages != nil {
		f.messages[name] = 0
	}
	return nil
}
func (f *purgeFixture) wait(ctx context.Context) error { return ctx.Err() }
func (f *purgeFixture) reopen(context.Context) error   { f.reopens++; f.broken = false; return nil }
func TestPurgeProtectedAndRecover(t *testing.T) {
	const legacy = "itom-1a87c0b9-43332050-6270134fcdf5ee2d-up-session-v2"
	for _, lostReply := range []bool{false, true} {
		f := &purgeFixture{folders: map[string]Folder{}, faults: map[string]bool{"zz-broken": true}, removedOnFault: map[string]bool{"zz-broken": lostReply}}
		for _, name := range []string{"INBOX", "Trash", legacy, "WSIT-old-s2c", "container", "container/" + legacy, "container/" + legacy + "/child", "ordinary", "itom-new-c-u", "zz-broken"} {
			f.folders[name] = Folder{Name: name, Delimiter: '/'}
		}
		r, e := purgeFolders(context.Background(), f)
		if r.Verified != lostReply || (e == nil) != lostReply {
			t.Fatalf("verified=%v err=%v lostReply=%v", r.Verified, e, lostReply)
		}
		if f.reopens != 1 || f.lists != 4 {
			t.Fatalf("recovery: %d lists: %d", f.reopens, f.lists)
		}
		for _, name := range []string{"ordinary", "itom-new-c-u"} {
			if _, ok := f.folders[name]; ok {
				t.Fatal("later folder not cleaned", name)
			}
		}
		protected := map[string]bool{}
		for _, item := range r.Items {
			if item.Outcome == "SKIPPED_PROTECTED" {
				protected[item.Name] = true
			}
		}
		for _, name := range []string{legacy, "WSIT-old-s2c", "container", "container/" + legacy, "container/" + legacy + "/child"} {
			if !protected[name] {
				t.Fatal("missing protection", name)
			}
			for _, list := range [][]string{f.deleted, f.emptied, f.unsubscribed} {
				for _, touched := range list {
					if touched == name {
						t.Fatal("mutated protected folder", name)
					}
				}
			}
		}
		if len(f.emptied) != 2 {
			t.Fatal("system folder emptying", f.emptied)
		}
	}
}
func TestProtectedLegacyNames(t *testing.T) {
	for _, v := range []struct {
		name string
		want bool
	}{
		{"itom-old-up-session-v2", true}, {"ITOM-old-down-SESSION-V2", true}, {"S2C-01", true}, {"S2C-01/child", true}, {"S2C-02", false}, {"WSIT_s2c", true}, {"wsit-legacy-s2c-00", true}, {"wsit-c2s", false}, {"itom-new-c-u", false}, {"itom-new-d-00", false}, {"personal-s2c", false}, {"itom-session-v20", false},
	} {
		if got := protectedLegacyFolder(Folder{Name: v.name, Delimiter: '/'}); got != v.want {
			t.Errorf("%q: %v", v.name, got)
		}
	}
}

// Model successful DELETE followed by automatic recreation, as in the field
// log. No system folder may be deleted or unsubscribed, even on an empty error.
func TestPurgeRC10RecreatedSystemFolders(t *testing.T) {
	legacy := "itom-1a87c0b9-43332050-6270134fcdf5ee2d-up-session-v2"
	systems := []string{"INBOX", "Trash", "Spam", "SentBox", "DraftBox", "Localized-Sent"}
	for _, fail := range []bool{false, true} {
		f := &purgeFixture{folders: map[string]Folder{}, messages: map[string]int{}, emptyFails: map[string]bool{}, recreateSystem: true}
		for _, n := range append(append([]string{}, systems...), legacy, "WSIT-old-s2c", "S2C-01", "S2C-01/child", "itom-new-c-u", "itom-new-d-00") {
			f.folders[n] = Folder{Name: n, Delimiter: '/'}
			f.messages[n] = 3
		}
		f.folders["Localized-Sent"] = Folder{Name: "Localized-Sent", Attrs: []imap.MailboxAttr{imap.MailboxAttrSent}}
		if fail {
			f.emptyFails["Spam"] = true
		}
		r, err := purgeFolders(context.Background(), f)
		if r.Verified == fail || (err != nil) != fail {
			t.Fatalf("fail=%v report=%+v err=%v", fail, r, err)
		}
		if fail && !strings.Contains(err.Error(), "Spam") {
			t.Fatalf("missing failing folder: %v", err)
		}
		if len(f.deleted) != 2 || len(f.unsubscribed) != 2 {
			t.Fatalf("unexpected deletion or unsubscribe: %v %v", f.deleted, f.unsubscribed)
		}
		for _, n := range systems {
			if !slices.Contains(f.emptied, n) || slices.Contains(f.deleted, n) || slices.Contains(f.unsubscribed, n) || !slices.Contains(r.Remaining, n) {
				t.Fatal("system folder not preserved", n)
			}
			if !(fail && n == "Spam") && f.messages[n] != 0 {
				t.Fatal("messages left in", n)
			}
		}
		for _, n := range []string{legacy, "WSIT-old-s2c", "S2C-01", "S2C-01/child"} {
			if f.messages[n] != 3 || slices.Contains(f.emptied, n) || !slices.Contains(r.Remaining, n) {
				t.Fatal("legacy folder touched", n)
			}
		}
	}
}
func TestPurgeRC10SystemFolderNames(t *testing.T) {
	for _, n := range []string{"INBOX", "Trash", "SPAM", "SentBox", "draftbox"} {
		if !retainSystemFolder(Folder{Name: n}) {
			t.Fatal(n)
		}
	}
	for _, n := range []string{"itom-new-c-u", "Spam-archive", "work/SentBox"} {
		if retainSystemFolder(Folder{Name: n}) {
			t.Fatal(n)
		}
	}
}

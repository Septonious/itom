package tunnel

import (
	"errors"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
	"testing"
)

func TestRC12OperationalProfileMinimum(t *testing.T) {
	p := servercfg.Profile{Schema: 1, Kind: "itom-imap-probe", Server: wire.ID{1}, Provider: "fixture", User: servercfg.User{ID: "12345678", Name: "fixture", Key: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Up: "itom-fixture-up", Down: "itom-fixture-down", Enabled: true}}
	for i := 1; i <= 4; i++ {
		p.Accounts = append(p.Accounts, servercfg.Account{ID: string(rune('a' + i)), Mail: mailbox.Config{Host: "localhost", Port: 993, TLS: "tls", Username: string(rune('a' + i)), Password: "test"}})
	}
	for n := 0; n < 4; n++ {
		q := p
		q.Accounts = p.Accounts[:n]
		if _, _, err := profileAccounts(q, nil, nil); !errors.Is(err, servercfg.ErrMinimumAccounts) {
			t.Fatal(n, err)
		}
	}
	if a, _, err := profileAccounts(p, nil, nil); err != nil || len(a) != 4 {
		t.Fatal(len(a), err)
	}
}

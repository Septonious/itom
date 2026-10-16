package adminserver

import (
	"context"
	"itom.local/core/adminapi"
	"itom.local/core/servercfg"
	"strings"
	"testing"
	"time"
)

func TestRev9AdminPinUsesWatchdogAndRollsBackAfterLostConnection(t *testing.T) {
	for _, initial := range []string{"", "81.19.77.168"} {
		t.Run("initial-"+initial, func(t *testing.T) {
			s, u, _ := adminFixture(t)
			if err := servercfg.Update(s.db(), func(c *servercfg.Config) error { return c.SetAccountDialIP("", initial) }); err != nil {
				t.Fatal(err)
			}
			now := time.Unix(10000, 0)
			s.Now = func() time.Time { return now }
			s.Launch = func(string) error { return nil }
			next := "81.19.77.171"
			if initial != "" {
				next = ""
			}
			q := adminapi.Request{Operation: "account_pin", ID: strings.Repeat("9", 32), AccountID: "*", DialIP: next}
			if reply := s.Execute(context.Background(), u, q); !reply.OK {
				t.Fatal(reply)
			}
			applied := false
			runtime := watchdogRuntime{now: func() time.Time { return now }, wait: func() { now = now.Add(time.Second) }, service: func(action string) error {
				if action == "start" && !applied {
					c, err := servercfg.Load(s.db())
					if err != nil || c.Accounts[0].Mail.DialIP != next {
						t.Fatal("pin was not applied", err)
					}
					applied = true
				}
				return nil
			}}
			if err := runGuard(s.State, q.ID, runtime); err != nil {
				t.Fatal(err)
			}
			c, err := servercfg.Load(s.db())
			g, _ := servercfg.GuardStatus(s.db())
			if err != nil || !applied || g.State != "rolled_back" || c.Accounts[0].Mail.DialIP != initial || c.Accounts[0].Mail.Host != "mail.example.invalid" {
				t.Fatal("pin rollback failed", g.State, err)
			}
		})
	}
}

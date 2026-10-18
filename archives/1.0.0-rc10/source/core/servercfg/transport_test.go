package servercfg

import (
	"encoding/json"
	"itom.local/core/mailbox"
	"testing"
)

func TestTransportBudgetsPartitionBothEndpointsAndUsers(t *testing.T) {
	for _, sessions := range []int{2, 5, 10, 16, 32} {
		for _, users := range []int{1, 2} {
			x := Transport{AccountSessions: sessions, Participants: users}.Effective()
			server, client := x.EndpointAccountSessions(true), x.EndpointAccountSessions(false)
			if server+users*client > sessions && sessions >= 2*users {
				t.Fatal("endpoint/user allocation oversubscribed provider sessions")
			}
		}
	}
	for _, x := range []Transport{{Layout: "arbitrary"}, {DataShards: 3}, {AccountSessions: 1}, {EndpointConnections: -1}, {LoginParallelism: 33}, {Participants: 3}} {
		if x.Validate() == nil {
			t.Fatal("invalid transport resources accepted", x)
		}
	}
	c, _ := New("test")
	u, _ := c.AddUser("one")
	_, _ = c.AddUser("two")
	c.Transport = Transport{Layout: "mixed", AccountSessions: 8, EndpointConnections: 64}
	c.Accounts = []Account{{ID: "a01", Mail: mailbox.Config{Host: "imap.example.invalid", Port: 993, TLS: "tls", Username: "one", Password: "synthetic-secret"}}}
	raw, err := c.androidInvitation(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Profile struct {
			Transport Transport `json:"transport_resources"`
		} `json:"profile"`
	}
	if err = json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if v.Profile.Transport.Participants != 2 || v.Profile.Transport.Layout != "mixed" || v.Profile.Transport.EndpointConnections != 64 {
		t.Fatal("invitation lost resource topology")
	}
}

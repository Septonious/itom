package servercfg

import "testing"

// Regression for the RC5 throughput investigation: AccountSessions is a
// provider-wide budget, not an independent per-endpoint allowance.
func TestRC5ProviderSessionBudgetAlreadyPartitionsEndpoints(t *testing.T) {
    cases := []struct {
        total, participants, wantServer, wantClient int
    }{
        {10, 1, 5, 5},
        {10, 2, 5, 2},
        {5, 1, 2, 2},
    }
    for _, tc := range cases {
        p := (Transport{AccountSessions: tc.total, Participants: tc.participants}).Effective()
        server := p.EndpointAccountSessions(true)
        client := p.EndpointAccountSessions(false)
        if server != tc.wantServer || client != tc.wantClient {
            t.Fatalf("sessions=%d participants=%d got server/client %d/%d want %d/%d",
                tc.total, tc.participants, server, client, tc.wantServer, tc.wantClient)
        }
        if server + tc.participants*client > tc.total {
            t.Fatal("physical LOGIN budget overcommitted")
        }
    }
}

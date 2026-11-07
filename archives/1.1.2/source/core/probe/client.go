package probe

import (
	"context"
	"errors"
	"strings"
	"time"

	"itom.local/core/servercfg"
	"itom.local/core/tunnel"
)

// RunClient uses the actual replacement runtime, including its DATA-path
// readiness exchange. It has no separate grant-based probe transport.
func RunClient(ctx context.Context, p servercfg.Profile, account, dir string) (time.Duration, error) {
	for _, a := range p.Accounts {
		if a.ID == account || strings.EqualFold(a.Mail.Username, account) {
			p.Accounts = []servercfg.Account{a}
			client, err := tunnel.StartClient(ctx, p, dir, nil)
			if err != nil {
				return 0, err
			}
			defer client.Close()
			return client.Ping(ctx)
		}
	}
	return 0, errors.New("ACCOUNT_NOT_FOUND")
}

package servercfg

import (
	"fmt"
	"itom.local/core/mailbox"
	"strings"
)

// Tombstones contain no password. Their IDs cannot be assigned to another
// mailbox: control journals on the server and clients are keyed by these IDs.
type AccountIdentity struct {
	ID       string `json:"id"`
	Host     string `json:"host"`
	Username string `json:"username"`
}

func sameMailbox(a AccountIdentity, b mailbox.Config) bool {
	return strings.EqualFold(a.Host, b.Host) && strings.EqualFold(a.Username, b.Username)
}

func (c *Config) RemoveAccount(id string) error {
	if len(c.Retired) >= 1024 {
		return ErrConfig
	}
	for i, a := range c.Accounts {
		if a.ID != id {
			continue
		}
		n := *c
		n.Schema = max(n.Schema, 2)
		n.Retired = append(append([]AccountIdentity(nil), c.Retired...), AccountIdentity{ID: a.ID, Host: a.Mail.Host, Username: a.Mail.Username})
		n.Accounts = append([]Account(nil), c.Accounts[:i]...)
		n.Accounts = append(n.Accounts, c.Accounts[i+1:]...)
		if err := n.Validate(); err != nil {
			return err
		}
		*c = n
		return nil
	}
	return fmt.Errorf("account %q not found", id)
}

func (c *Config) AddAccount(mail mailbox.Config) (string, error) {
	if mail.Validate() != nil {
		return "", ErrConfig
	}
	n := *c
	n.Schema = max(n.Schema, 2)
	n.Accounts = append([]Account(nil), c.Accounts...)
	n.Retired = append([]AccountIdentity(nil), c.Retired...)
	id := ""
	used := map[string]bool{}
	for _, a := range c.Accounts {
		used[a.ID] = true
	}
	for i, a := range c.Retired {
		used[a.ID] = true
		if sameMailbox(a, mail) {
			id = a.ID
			n.Retired = append(n.Retired[:i], n.Retired[i+1:]...)
			break
		}
	}
	if id == "" {
		for i := 1; ; i++ {
			candidate := fmt.Sprintf("a%02d", i)
			if !used[candidate] {
				id = candidate
				break
			}
		}
	}
	n.Accounts = append(n.Accounts, Account{ID: id, Mail: mail})
	if err := n.Validate(); err != nil {
		return "", err
	}
	*c = n
	return id, nil
}

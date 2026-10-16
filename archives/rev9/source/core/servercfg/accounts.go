package servercfg

import (
	"crypto/rand"
	"encoding/hex"
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
			if c.AccountNamespace != "" {
				candidate = fmt.Sprintf("a%s-%02d", c.AccountNamespace, i)
			}
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

// ResetAccounts forgets all configured logins and retired mailbox definitions.
// Rotate the ID namespace so a new login can never inherit an old journal.
// This does not delete provider mail, client identities or physical-debt files.
func (c *Config) ResetAccounts() error {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	n := *c
	n.Schema = max(n.Schema, 6)
	n.AccountNamespace = hex.EncodeToString(nonce[:])
	n.Accounts, n.Retired = nil, nil
	if err := n.Validate(); err != nil {
		return err
	}
	*c = n
	return nil
}

// SetAccountDialIP changes routing only; durable account identities are retained.
// Empty id applies to all accounts; empty ip restores DNS. Validate before mutation.
func (c *Config) SetAccountDialIP(id, ip string) error {
	if err := mailbox.ValidateDialIP(ip); err != nil {
		return err
	}
	found := false
	for _, a := range c.Accounts {
		if id == "" || a.ID == id {
			found = true
		}
	}
	if !found {
		return ErrConfig
	}
	for i := range c.Accounts {
		if id == "" || c.Accounts[i].ID == id {
			c.Accounts[i].Mail.DialIP = ip
		}
	}
	return nil
}

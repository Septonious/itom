package manager

import (
	"errors"
	"fmt"
	"itom.local/core/servercfg"
	"strconv"
	"strings"
)

func (u *UI) selectUser(c servercfg.Config, enabledOnly bool) (servercfg.User, error) {
	var users []servercfg.User
	for _, user := range c.Users {
		if enabledOnly && !user.Enabled {
			continue
		}
		users = append(users, user)
		fmt.Fprintf(u.Out, "%d. %s | UID %s | enabled=%t\n", len(users), user.Name, user.ID, user.Enabled)
	}
	if len(users) == 0 {
		return servercfg.User{}, errors.New("no available clients")
	}
	answer, err := u.ask("Client (number; 0 — back)")
	if err != nil || answer == "0" {
		return servercfg.User{}, err
	}
	if n, err := strconv.Atoi(answer); err == nil && n > 0 && n <= len(users) {
		return users[n-1], nil
	}
	// Exact legacy identifiers remain accepted for scripted manager input.
	for _, user := range users {
		if user.ID == answer {
			return user, nil
		}
	}
	return servercfg.User{}, errors.New("select a client number from the list")
}

func (u *UI) selectAccount(c servercfg.Config) (servercfg.Account, error) {
	for i, a := range c.Accounts {
		fmt.Fprintf(u.Out, "%d. %s\n", i+1, a.Mail.Username)
	}
	if len(c.Accounts) == 0 {
		return servercfg.Account{}, errors.New("no mail accounts")
	}
	answer, err := u.ask("Account (number; 0 — back)")
	if err != nil || answer == "0" {
		return servercfg.Account{}, err
	}
	if n, err := strconv.Atoi(answer); err == nil && n > 0 && n <= len(c.Accounts) {
		return c.Accounts[n-1], nil
	}
	for _, a := range c.Accounts {
		if strings.EqualFold(a.Mail.Username, answer) {
			return a, nil
		}
	}
	return servercfg.Account{}, errors.New("select an account number from the list")
}

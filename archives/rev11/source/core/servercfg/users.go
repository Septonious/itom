package servercfg

import "fmt"

func (c *Config) RemoveUser(id string) error {
	if len(c.RetiredUsers) >= 1024 {
		return ErrConfig
	}
	for i, u := range c.Users {
		if u.ID != id {
			continue
		}
		n := *c
		n.Schema = max(n.Schema, 3)
		n.RetiredUsers = append(append([]UserIdentity(nil), c.RetiredUsers...), UserIdentity{ID: u.ID, Up: u.Up, Down: u.Down})
		n.Users = append([]User(nil), c.Users[:i]...)
		n.Users = append(n.Users, c.Users[i+1:]...)
		n.ClientNotices = make(map[string]string, len(c.ClientNotices))
		for uid, text := range c.ClientNotices {
			if uid != id {
				n.ClientNotices[uid] = text
			}
		}
		if err := n.Validate(); err != nil {
			return err
		}
		*c = n
		return nil
	}
	return fmt.Errorf("client %q not found", id)
}

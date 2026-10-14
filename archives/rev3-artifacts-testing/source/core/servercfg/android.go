package servercfg

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"time"
)

// ExportAndroid writes the exact invitation consumed by the native app. The
// existing qualification profile remains available to probe tooling only.
func (c Config) androidInvitation(uid string) ([]byte, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	if len(c.Accounts) == 0 {
		return nil, ErrConfig
	}
	var u User
	for _, x := range c.Users {
		if x.ID == uid && x.Enabled {
			u = x
		}
	}
	if u.ID == "" {
		return nil, ErrConfig
	}
	namespace := strings.TrimSuffix(u.Up, "-up")
	if namespace == u.Up || u.Down != namespace+"-down" {
		return nil, ErrConfig
	}
	id := hex.EncodeToString(c.Server[:])
	provider := "imap-" + id[:8]
	profileID := "itom-" + id[:16]
	keyref := "keystore:" + profileID + "-key"
	credentials := map[string]any{}
	accounts := []any{}
	for _, a := range c.Accounts {
		ref := "keystore:" + profileID + "-" + a.ID
		mode := "imaps"
		if a.Mail.TLS == "starttls" {
			mode = "starttls"
		}
		accounts = append(accounts, map[string]any{"id": a.ID, "name": a.Mail.Username, "host": a.Mail.Host, "port": a.Mail.Port, "tls": mode, "verify_certificate": true, "credential_ref": ref, "enabled": true, "provider_id": provider})
		credentials[ref] = map[string]string{"username": a.Mail.Username, "password": a.Mail.Password}
	}
	key, e := hex.DecodeString(u.Key)
	if e != nil {
		return nil, e
	}
	defer clear(key)
	pair := sha256.Sum256([]byte(id + "/" + u.ID))
	profile := map[string]any{"schema_version": 2, "profile_id": profileID, "name": u.Name, "server_id": id, "user_id": u.ID, "provider_id": provider, "encryption_key_ref": keyref, "mailbox_namespace": namespace, "accounts": accounts, "transport_profile": "imap-tcp-v1"}
	profile["transport_resources"] = c.TransportForUsers()
	invitation := map[string]any{"schema_version": 2, "wire_version": 2, "profile": profile, "pair_id": hex.EncodeToString(pair[:16]), "psk_base64": base64.StdEncoding.EncodeToString(key), "credentials": credentials, "expires_unix_seconds": time.Now().Add(30 * 24 * time.Hour).Unix()}
	body, e := json.MarshalIndent(invitation, "", "  ")
	if e != nil {
		return nil, e
	}
	if len(body) > MaxConfigBytes {
		return nil, ErrConfigSize
	}
	return body, nil
}

func (c Config) AndroidLink(uid string) (string, error) {
	body, err := c.androidInvitation(uid)
	if err != nil {
		return "", err
	}
	return "itom://import#" + base64.RawURLEncoding.EncodeToString(body), nil
}

// ExportAndroid remains available for tools; the interactive manager uses a link.
func (c Config) ExportAndroid(uid, path string) error {
	body, e := c.androidInvitation(uid)
	if e != nil {
		return e
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(body)
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		os.Remove(path)
		return e
	}
	return closeErr
}

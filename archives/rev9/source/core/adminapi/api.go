// Package adminapi contains the bounded IMAP-tunneled administration protocol.
// It has no server, shell, configuration database or service-management code.
package adminapi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const Host = "itom-admin.internal"
const Port = 1
const MaxRequest = 16384
const MaxResponse = 1 << 20

type Request struct {
	Revision    string     `json:"revision,omitempty"`
	Operation   string     `json:"operation"`
	ID          string     `json:"id,omitempty"`
	UID         string     `json:"uid,omitempty"`
	AccountID   string     `json:"account_id,omitempty"`
	Text        string     `json:"text,omitempty"`
	Name        string     `json:"name,omitempty"`
	Enabled     *bool      `json:"enabled,omitempty"`
	Mail        *Mail      `json:"mail,omitempty"`
	Password    string     `json:"password,omitempty"`
	Diagnostics string     `json:"diagnostics,omitempty"`
	Transport   *Resources `json:"transport,omitempty"`
	Challenge   string     `json:"challenge,omitempty"`
	Offset      int64      `json:"offset,omitempty"`
}
type Mail struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	TLS      string `json:"tls"`
	Username string `json:"username"`
	Password string `json:"password"`
}
type Resources struct {
	Layout              string `json:"layout"`
	DataShards          int    `json:"data_shards"`
	AccountSessions     int    `json:"account_sessions"`
	EndpointConnections int    `json:"endpoint_connections"`
	LoginParallelism    int    `json:"login_parallelism"`
}
type Response struct {
	OK      bool     `json:"ok"`
	Error   string   `json:"error,omitempty"`
	Status  *Status  `json:"status,omitempty"`
	Pending *Pending `json:"pending,omitempty"`
	Text    string   `json:"text,omitempty"`
	Data    []byte   `json:"data,omitempty"`
	ID      string   `json:"id,omitempty"`
	Total   int64    `json:"total,omitempty"`
}
type Pending struct {
	Owner          string `json:"owner"`
	ID             string `json:"id"`
	State          string `json:"state"`
	Challenge      string `json:"challenge,omitempty"`
	LossDeadlineMS int64  `json:"loss_deadline_ms"`
	HealthySinceMS int64  `json:"healthy_since_ms"`
	ServerTimeMS   int64  `json:"server_time_ms"`
}
type User struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Message string `json:"message,omitempty"`
}
type Account struct {
	ID       string `json:"id"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	TLS      string `json:"tls"`
	Username string `json:"username"`
}
type Status struct {
	Revision          string          `json:"revision"`
	CurrentUID        string          `json:"current_uid"`
	Admin             bool            `json:"admin"`
	Provider          string          `json:"provider"`
	Service           string          `json:"service"`
	Diagnostics       string          `json:"diagnostics"`
	GlobalMessage     string          `json:"global_message"`
	Users             []User          `json:"users"`
	Accounts          []Account       `json:"accounts"`
	Transport         Resources       `json:"transport"`
	Traffic           json.RawMessage `json:"traffic,omitempty"`
	WatchdogAvailable bool            `json:"watchdog_available"`
}

func NewID() (string, error) {
	var b [16]byte
	_, e := rand.Read(b[:])
	return hex.EncodeToString(b[:]), e
}
func DecodeRequest(raw []byte) (Request, error) {
	var q Request
	if len(raw) == 0 || len(raw) > MaxRequest {
		return q, errors.New("ADMIN_REQUEST_LIMIT")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&q) != nil || d.Decode(new(any)) != io.EOF {
		return q, errors.New("ADMIN_REQUEST_INVALID")
	}
	return q, nil
}

// Call never uses an HTTP proxy, DNS, or a direct socket: the caller supplies
// the authenticated encrypted logical stream opened to Host:Port.
func Call(ctx context.Context, stream net.Conn, raw []byte, keyHex string) (Response, error) {
	var result Response
	defer stream.Close()
	if _, e := DecodeRequest(raw); e != nil {
		return result, e
	}
	stop := context.AfterFunc(ctx, func() { stream.Close() })
	defer stop()
	deadline := time.Now().Add(20 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = stream.SetDeadline(deadline)
	reader := bufio.NewReaderSize(stream, 1024)
	banner, e := reader.ReadSlice('\n')
	if e != nil {
		return result, errors.New("ADMIN_HANDSHAKE_FAILED")
	}
	if !strings.HasPrefix(string(banner), "ITOM-ADMIN-1 ") {
		return result, errors.New("ADMIN_HANDSHAKE_FAILED")
	}
	nonce := strings.TrimSuffix(strings.TrimPrefix(string(banner), "ITOM-ADMIN-1 "), "\n")
	proof, e := Proof(keyHex, nonce, raw)
	if e != nil {
		return result, e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+Host+"/rpc", bytes.NewReader(raw))
	if e != nil {
		return result, e
	}
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-ITOM-Admin-Proof", proof)
	if e = req.Write(stream); e != nil {
		return result, errors.New("ADMIN_CONNECTION_FAILED")
	}
	res, e := http.ReadResponse(reader, req)
	if e != nil {
		return result, errors.New("ADMIN_CONNECTION_FAILED")
	}
	defer res.Body.Close()
	body, e := io.ReadAll(io.LimitReader(res.Body, MaxResponse+1))
	if e != nil || len(body) > MaxResponse {
		return result, errors.New("ADMIN_RESPONSE_LIMIT")
	}
	if res.StatusCode != http.StatusOK || json.Unmarshal(body, &result) != nil {
		return result, errors.New("ADMIN_RESPONSE_INVALID")
	}
	return result, nil
}

// The per-connection challenge additionally separates trusted app administration
// from ordinary applications routed through an admin user's VPN or SOCKS proxy.
// Those applications can reach the reserved address but cannot access the key.
func Proof(keyHex, nonce string, raw []byte) (string, error) {
	key, e := hex.DecodeString(keyHex)
	if e != nil || len(key) != 32 {
		return "", errors.New("ADMIN_KEY_INVALID")
	}
	n, e := hex.DecodeString(nonce)
	if e != nil || len(n) != 16 {
		return "", errors.New("ADMIN_HANDSHAKE_FAILED")
	}
	h := hmac.New(sha256.New, key)
	h.Write([]byte("ITOM-ADMIN-1\x00"))
	h.Write(n)
	h.Write(raw)
	return hex.EncodeToString(h.Sum(nil)), nil
}

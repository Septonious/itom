package tunnel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestPublicTwoUsersShareAccountServiceWithoutMixingStreams(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cfg, err := servercfg.New("two-user-public")
	if err != nil {
		t.Fatal(err)
	}
	users := make([]servercfg.User, 2)
	for i := range users {
		users[i], err = cfg.AddUser(fmt.Sprintf("user-%d", i))
		if err != nil {
			t.Fatal(err)
		}
	}
	cfg.Transport = servercfg.Transport{AccountSessions: 20, Layout: "split"}
	var folders []string
	for _, u := range users {
		folders = append(folders, u.Up, u.Down)
	}
	providers := map[string]func(context.Context) (*mailbox.Client, error){}
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("a%02d", i+1)
		c, s := tlsMailPair(t, ctx, folders)
		providers[id] = c.(*laneMail).dial
		c.(*laneMail).Close()
		s.(*laneMail).Close()
		cfg.Accounts = append(cfg.Accounts, servercfg.Account{ID: id, Mail: mailbox.Config{Host: "mail.example", Port: 993, TLS: "tls", Username: id, Password: "test-only"}})
	}
	dial := func(ctx context.Context, mail mailbox.Config, folders []string) (*mailbox.Client, error) {
		return providers[mail.Username](ctx)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var echoWG sync.WaitGroup
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			echoWG.Add(1)
			go func() { defer echoWG.Done(); defer c.Close(); io.Copy(c, c) }()
		}
	}()
	done := make(chan error, 1)
	go func() {
		done <- serveWithOptions(ctx, cfg, filepath.Join(t.TempDir(), "server"), nil, serverOptions{dial: dial, allowPrivate: true})
	}()
	var clients []*Client
	defer func() {
		cancel()
		for _, client := range clients {
			client.Close()
		}
		<-done
		echoWG.Wait()
	}()
	for _, u := range users {
		client, err := StartClientWithOptions(ctx, servercfg.Profile{Schema: 1, Server: cfg.Server, Provider: cfg.Provider, User: u, Accounts: cfg.Accounts, Transport: cfg.TransportForUsers()}, filepath.Join(t.TempDir(), u.ID), dial, ClientOptions{})
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, client)
	}
	for _, client := range clients {
		for client.Stats().ActiveLines != 6 {
			select {
			case <-ctx.Done():
				t.Fatal("two-user activation", client.Snapshot())
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	target := listener.Addr().(*net.TCPAddr)
	var wg sync.WaitGroup
	for i, client := range clients {
		wg.Add(1)
		go func(i int, client *Client) {
			defer wg.Done()
			c, _, err := client.Open(ctx, wire.Connect{Host: "127.0.0.1", Port: uint16(target.Port)})
			if err != nil {
				t.Error(err)
				return
			}
			defer c.Close()
			p := c.(net.Conn)
			p.SetDeadline(time.Now().Add(20 * time.Second))
			payload := bytes.Repeat([]byte(fmt.Sprintf("isolated-user-%d/", i)), 20000)
			written := make(chan error, 1)
			go func() { written <- productionWriteFull(p, payload) }()
			got := make([]byte, len(payload))
			if _, err := io.ReadFull(p, got); err != nil {
				t.Error(err)
				return
			}
			if err := <-written; err != nil {
				t.Error(err)
			}
			if !bytes.Equal(payload, got) {
				t.Error("user/session DATA crossed")
			}
		}(i, client)
	}
	wg.Wait()
	if clients[0].logicalSession == clients[1].logicalSession {
		t.Fatal("users shared logical session")
	}
}

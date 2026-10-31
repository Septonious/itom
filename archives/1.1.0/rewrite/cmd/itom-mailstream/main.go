// itom-mailstream is a standalone development prototype. It does not install
// or update the deployed Android app or server, or open old transport state.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	mailstream "itom.local/mailstream"
)

type profile struct {
	Identity string                   `json:"identity"`
	PSK      string                   `json:"psk_base64"`
	Accounts []mailstream.MailAccount `json:"accounts"`
}

func run() error {
	mode := flag.String("mode", "", "server or socks (standalone prototype)")
	path := flag.String("profile", "", "new mailstream JSON profile; legacy profile/state is not modified")
	listen := flag.String("listen", "127.0.0.1:1081", "local SOCKS5 TCP CONNECT listener")
	flag.Parse()
	if *mode != "server" && *mode != "socks" {
		return errors.New("--mode server or socks required")
	}
	if *path == "" || flag.NArg() != 0 {
		return errors.New("--profile required")
	}
	f, err := os.Open(*path)
	if err != nil {
		return errors.New("PROFILE_OPEN_FAILED")
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 128<<10))
	decoder.DisallowUnknownFields()
	var p profile
	if err = decoder.Decode(&p); err != nil {
		return errors.New("PROFILE_INVALID")
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return errors.New("PROFILE_TRAILING_DATA")
	}
	psk, err := base64.StdEncoding.DecodeString(p.PSK)
	if err != nil || len(psk) != 32 {
		return errors.New("PROFILE_KEY_INVALID")
	}
	defer clear(psk)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	link, err := mailstream.NewIMAPLink(ctx, p.Identity, p.Accounts, *mode == "socks")
	if err != nil {
		return err
	}
	defer link.Close()
	fmt.Println("ITOM mailbox-stream rewrite: development prototype; finite storage, no Android/UDP integration, not a release candidate.")
	if *mode == "server" {
		fmt.Println("Waiting for an authenticated new-protocol session.")
		return mailstream.Serve(ctx, link, p.Identity, psk, nil, func(*mailstream.Session) { fmt.Println("Authenticated session ready; no cleanup or grant barrier.") })
	}
	startup, cancel := context.WithTimeout(ctx, 45*time.Second)
	session, err := mailstream.Connect(startup, link, p.Identity, psk)
	cancel()
	if err != nil {
		return err
	}
	defer session.Close()
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return errors.New("LOCAL_LISTENER_FAILED")
	}
	defer listener.Close()
	if !listener.Addr().(*net.TCPAddr).IP.IsLoopback() {
		return errors.New("LOCAL_LISTENER_MUST_BE_LOOPBACK")
	}
	fmt.Println("Authenticated mailbox roundtrip complete; SOCKS5 TCP listener:", listener.Addr())
	return mailstream.ServeSOCKS(ctx, listener, session)
}
func main() {
	if err := run(); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

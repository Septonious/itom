package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"itom.local/core/adminserver"
	"itom.local/core/manager"
	"itom.local/core/probe"
	"itom.local/core/servercfg"
	"itom.local/core/tunnel"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
)

const revision = "itom-1-1-0-dev9"

func run(args []string) error {
	command := "menu"
	if len(args) > 0 {
		command = args[0]
		args = args[1:]
	}
	if command == "version" || command == "--version" {
		fmt.Println("ITOM", revision, runtime.GOOS, runtime.GOARCH)
		return nil
	}
	if command == "install" {
		return manager.Install()
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	state := flags.String("state", "/var/lib/itom", "private state directory")
	profile := flags.String("profile", "", "CLI profile JSON")
	account := flags.String("account", "", "account email (required for imap-check)")
	to := flags.String("to", "", "offline DATA journal profile: data8, data12 or data16")
	mode := flags.String("mode", "", "server diagnostics: on or off; restart service to apply")
	uid := flags.String("uid", "", "client UID for traffic or message (default: all clients)")
	transaction := flags.String("transaction", "", "private admin rollback transaction")
	watch := flags.Bool("watch", false, "refresh traffic every second until Ctrl+C")
	message := flags.String("text", "", "current server message (maximum 2048 UTF-8 bytes)")
	clearMessage := flags.Bool("clear", false, "clear the current server message")
	if e := flags.Parse(args); e != nil {
		return e
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	switch command {
	case "admin-guard":
		return adminserver.RunGuard(*state, *transaction)
	case "message":
		changed := *clearMessage
		flags.Visit(func(f *flag.Flag) {
			if f.Name == "text" {
				changed = true
			}
		})
		if *clearMessage && *message != "" {
			return errors.New("use either --text or --clear")
		}
		if *uid != "" {
			return manager.ClientMessage(*state, *uid, *message, changed, os.Stdout)
		}
		return manager.ServerMessage(*state, *message, changed, os.Stdout)
	case "traffic":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, terminationSignal())
		defer stop()
		return manager.ShowTraffic(ctx, *state, *uid, *watch, os.Stdout)
	case "uninstall":
		return manager.Uninstall(os.Stdout)
	case "diagnostics":
		cfg, e := servercfg.Load(filepath.Join(*state, "server.db"))
		if e != nil {
			return e
		}
		if *mode == "" {
			value := cfg.DiagnosticsMode
			if value == "" {
				value = "on"
			}
			fmt.Println("Server diagnostics:", value)
			return nil
		}
		if *mode != "on" && *mode != "off" {
			return errors.New("--mode on or off required")
		}
		cfg.DiagnosticsMode = *mode
		if e = servercfg.Update(filepath.Join(*state, "server.db"), func(c *servercfg.Config) error { c.DiagnosticsMode = *mode; return nil }); e != nil {
			return e
		}
		fmt.Println("Server diagnostics:", *mode, "; restart itom service to apply")
		return nil
	case "imap-check":
		cfg, e := servercfg.Load(filepath.Join(*state, "server.db"))
		if e != nil {
			return e
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, terminationSignal())
		defer stop()
		return probe.CheckIMAP(ctx, cfg, *account, os.Stdout)
	case "journal-profile":
		if *to != "data8" && *to != "data12" && *to != "data16" {
			return errors.New("--to=data8, --to=data12 or --to=data16 required; stop itom service first")
		}
		cfg, e := servercfg.Load(filepath.Join(*state, "server.db"))
		if e != nil {
			return e
		}
		for _, a := range cfg.Accounts {
			for _, u := range cfg.Users {
				dir := filepath.Join(*state, "tunnel-server", a.ID, u.ID)
				if _, e = os.Stat(filepath.Join(dir, "data.db")); errors.Is(e, os.ErrNotExist) {
					continue
				}
				if e != nil {
					return e
				}
				if e = tunnel.MigrateDataProfile(dir, a, u, *to); e != nil {
					return fmt.Errorf("account=%s user=%s profile=%s: %w", a.Mail.Username, u.ID, *to, e)
				}
				fmt.Printf("account=%s user=%s profile=%s outcome=audited\n", a.Mail.Username, u.ID, *to)
			}
		}
		return nil
	case "menu":
		return manager.New(*state).Run()
	case "serve":
		cfg, e := servercfg.Load(filepath.Join(*state, "server.db"))
		if e != nil {
			return e
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, terminationSignal())
		defer stop()
		var logger func(string)
		if cfg.DiagnosticsMode != "off" {
			log.Printf("event=server_start revision=%s protocol=4 accounts=%d users=%d", revision, len(cfg.Accounts), len(cfg.Users))
			logger = func(s string) { log.Print(s) }
		}
		if e = adminserver.RecoverGuard(*state); e != nil {
			return errors.New("ADMIN_GUARD_RECOVERY_FAILED")
		}
		admin := adminserver.New(ctx, *state)
		defer admin.Close()
		e = tunnel.ServeManaged(ctx, cfg, filepath.Join(*state, "tunnel-server"), logger, admin.Dial)
		if errors.Is(e, context.Canceled) {
			return nil
		}
		return e
	case "probe":
		if *profile == "" {
			return errors.New("--profile required")
		}
		p, e := servercfg.ReadProfile(*profile)
		if e != nil {
			return e
		}
		dir := *state
		if dir == "/var/lib/itom" {
			cache, e := os.UserCacheDir()
			if e != nil {
				return e
			}
			dir = filepath.Join(cache, "itom-probe", fmt.Sprintf("%x", p.Server), p.User.ID, *account)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		rtt, e := probe.RunClient(ctx, p, *account, dir)
		if e != nil {
			return e
		}
		fmt.Printf("Noise + IMAP roundtrip OK; account=%s RTT=%s\n", *account, rtt.Round(1e6))
		return nil
	default:
		return errors.New("commands: menu, serve, probe, imap-check, install, uninstall, traffic, message, version, journal-profile, diagnostics")
	}
}
func main() {
	if e := run(os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, "ITOM:", e)
		os.Exit(1)
	}
}

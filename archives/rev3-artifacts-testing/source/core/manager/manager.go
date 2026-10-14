package manager

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type UI struct {
	In           *bufio.Reader
	Out          io.Writer
	State        string
	Secret       func() (string, error)
	stoppedCheck func() error
	adminDial    func(context.Context, mailbox.Config) (accountAdmin, error)
	pauseAdmin   func(context.Context, time.Duration) error
}

func New(state string) *UI { return &UI{In: bufio.NewReader(os.Stdin), Out: os.Stdout, State: state} }
func (u *UI) ask(label string) (string, error) {
	fmt.Fprint(u.Out, label+": ")
	s, e := u.In.ReadString('\n')
	if e != nil {
		return "", e
	}
	if len(s) > 4096 {
		return "", errors.New("INPUT_LIMIT")
	}
	return strings.TrimSpace(s), nil
}
func (u *UI) password() (string, error) {
	if u.Secret != nil {
		return u.Secret()
	}
	fmt.Fprint(u.Out, "IMAP application password: ")
	value, err := u.In.ReadString('\n')
	if err != nil {
		return "", err
	}
	value = strings.TrimSuffix(strings.TrimSuffix(value, "\n"), "\r")
	if len(value) > 4096 {
		return "", errors.New("INPUT_LIMIT")
	}
	return value, nil
}
func (u *UI) db() string { return filepath.Join(u.State, "server.db") }
func (u *UI) system(action string) error {
	if runtime.GOOS != "linux" {
		return errors.New("service management requires Linux and systemd")
	}
	c := exec.Command("systemctl", action, "itom.service")
	c.Stdout = u.Out
	c.Stderr = u.Out
	return c.Run()
}
func (u *UI) stopped() error {
	if u.stoppedCheck != nil {
		return u.stoppedCheck()
	}
	if runtime.GOOS == "linux" {
		out, err := exec.Command("systemctl", "is-active", "itom.service").Output()
		state := strings.TrimSpace(string(out))
		var exit *exec.ExitError
		if errors.As(err, &exit) && (exit.ExitCode() == 3 || exit.ExitCode() == 4) && (state == "inactive" || state == "failed" || state == "unknown") {
			return nil
		}
		if err != nil && state == "" {
			return errors.New("could not verify systemd state; operation canceled")
		}
		return errors.New("stop the service using menu item 8 first; start it using item 7 after changes")
	}
	return nil
}
func (u *UI) Run() error {
	if e := os.MkdirAll(u.State, 0700); e != nil {
		return e
	}
	for {
		fmt.Fprintln(u.Out, "\nITOM — server manager\nEncrypted IMAP transport for Android VPN.\n1. Status\n2. Initialize server\n3. Mail accounts\n4. Clients\n5. Export Android profile\n6. Check IMAP accounts\n7. Start service\n8. Stop service\n9. Restart service\n10. Live logs\n11. Install/update service\n12. Live packet trace\n13. Export full diagnostics\n14. Server logging and tracing\n15. Live client traffic\n16. Uninstall ITOM from server\n17. Server message\n0. Exit")
		s, e := u.ask("Selection")
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
		switch s {
		case "0":
			return nil
		case "1":
			e = u.status()
		case "2":
			e = u.init()
		case "3":
			e = u.accounts()
		case "4":
			e = u.users()
		case "5":
			e = u.export()
		case "6":
			e = u.check()
		case "7":
			e = u.system("start")
		case "8":
			e = u.system("stop")
		case "9":
			e = u.system("restart")
		case "10":
			e = u.followLogs()
		case "11":
			e = Install()
		case "12":
			e = u.followTrace()
		case "13":
			e = u.exportTrace()
		case "14":
			e = u.diagnostics()
		case "15":
			e = u.followTraffic()
		case "17":
			e = u.serverMessage()
		case "16":
			var removed bool
			removed, e = u.uninstall()
			if removed {
				return nil
			}
		default:
			fmt.Fprintln(u.Out, "Enter a menu number.")
		}
		if e != nil {
			fmt.Fprintln(u.Out, "Error:", e)
		}
	}
}
func (u *UI) status() error {
	c, e := servercfg.Load(u.db())
	if e != nil {
		return e
	}
	fmt.Fprintf(u.Out, "Provider: %s; configured accounts: %d; clients: %d/2\n", c.Provider, len(c.Accounts), len(c.Users))
	for _, x := range c.Users {
		fmt.Fprintf(u.Out, "UID %s | %s | enabled=%t\n", x.ID, x.Name, x.Enabled)
	}
	if runtime.GOOS == "linux" {
		return u.system("status")
	}
	return nil
}
func (u *UI) init() error {
	if e := u.stopped(); e != nil {
		return e
	}
	if _, e := servercfg.Load(u.db()); e == nil {
		return errors.New("server already initialized; existing data was preserved")
	}
	name, e := u.ask("Mail provider name")
	if e != nil {
		return e
	}
	return servercfg.Update(u.db(), func(c *servercfg.Config) error {
		if c.Schema != 0 {
			return errors.New("already initialized")
		}
		n, e := servercfg.New(name)
		*c = n
		return e
	})
}
func (u *UI) accounts() error {
	c, e := servercfg.Load(u.db())
	if e != nil {
		return e
	}
	for _, a := range c.Accounts {
		fmt.Fprintf(u.Out, "%s | %s:%d %s\n", a.Mail.Username, a.Mail.Host, a.Mail.Port, a.Mail.TLS)
	}
	choice, e := u.ask("1 add, 2 change password, 3 remove account from ITOM, 4 delete all folders and messages in this account, 5 purge ALL mail accounts, 0 back")
	if e != nil || choice == "0" {
		return e
	}
	if e = u.stopped(); e != nil {
		return e
	}
	if choice == "3" || choice == "4" {
		return u.accountMaintenance(c, choice)
	}
	if choice == "5" {
		return u.purgeAllAccounts(c)
	}
	if choice == "2" {
		target, e := u.selectAccount(c)
		if e != nil {
			return e
		}
		if target.ID == "" {
			return nil
		}
		password, e := u.password()
		if e != nil {
			return e
		}
		return servercfg.Update(u.db(), func(c *servercfg.Config) error {
			for i := range c.Accounts {
				if c.Accounts[i].ID == target.ID && c.Accounts[i] == target {
					c.Accounts[i].Mail.Password = password
					return nil
				}
			}
			return errors.New("account not found")
		})
	}
	if choice != "1" {
		return errors.New("unknown menu option")
	}
	var cfg mailbox.Config
	if len(c.Accounts) > 0 {
		cfg = c.Accounts[0].Mail
		cfg.Password = ""
		cfg.Username = ""
	} else {
		cfg.Host, e = u.ask("IMAP hostname")
		if e != nil {
			return e
		}
		mode, e := u.ask("1 IMAPS/TLS (993), 2 required STARTTLS (143)")
		if e != nil {
			return e
		}
		cfg.TLS = "tls"
		cfg.Port = 993
		if mode == "2" {
			cfg.TLS = "starttls"
			cfg.Port = 143
		} else if mode != "1" {
			return errors.New("unknown mode")
		}
		port, e := u.ask(fmt.Sprintf("Port (Enter = %d)", cfg.Port))
		if e != nil {
			return e
		}
		if port != "" {
			cfg.Port, e = strconv.Atoi(port)
			if e != nil {
				return e
			}
		}
	}
	cfg.Username, e = u.ask("Username")
	if e != nil {
		return e
	}
	cfg.Password, e = u.password()
	if e != nil {
		return e
	}
	if e = cfg.Validate(); e != nil {
		return e
	}
	return servercfg.Update(u.db(), func(c *servercfg.Config) error {
		_, err := c.AddAccount(cfg)
		return err
	})
}
func (u *UI) users() error {
	c, e := servercfg.Load(u.db())
	if e != nil {
		return e
	}
	for _, x := range c.Users {
		fmt.Fprintf(u.Out, "%s | %s | enabled=%t\n", x.ID, x.Name, x.Enabled)
	}
	choice, e := u.ask("1 add, 2 rename, 3 enable/disable, 4 remove client from server, 0 back")
	if e != nil || choice == "0" {
		return e
	}
	if e = u.stopped(); e != nil {
		return e
	}
	if choice == "1" {
		name, e := u.ask("Client name")
		if e != nil {
			return e
		}
		var uid string
		e = servercfg.Update(u.db(), func(c *servercfg.Config) error { x, err := c.AddUser(name); uid = x.ID; return err })
		if e == nil {
			fmt.Fprintln(u.Out, "Created UID", uid)
		}
		return e
	}
	if choice != "2" && choice != "3" && choice != "4" {
		return errors.New("unknown menu option")
	}
	target, e := u.selectUser(c, false)
	if e != nil {
		return e
	}
	if target.ID == "" {
		return nil
	}
	id := target.ID
	if choice == "4" {
		fmt.Fprintln(u.Out, "This client registration will no longer connect after service startup. Mail folders and quota journals are retained. New clients receive a different identity.")
		answer, err := u.ask("Remove the selected client? (yes/no)")
		if err != nil {
			return err
		}
		if answer != "yes" {
			return nil
		}
	}
	name := ""
	if choice == "2" {
		name, e = u.ask("New name")
		if e != nil {
			return e
		}
	}
	return servercfg.Update(u.db(), func(c *servercfg.Config) error {
		for i := range c.Users {
			if c.Users[i].ID == id {
				if c.Users[i] != target {
					return errors.New("client changed; repeat selection")
				}
				if choice == "4" {
					return c.RemoveUser(id)
				}
				if choice == "2" {
					c.Users[i].Name = name
				} else {
					c.Users[i].Enabled = !c.Users[i].Enabled
				}
				return nil
			}
		}
		return errors.New("UID not found")
	})
}
func (u *UI) export() error {
	c, e := servercfg.Load(u.db())
	if e != nil {
		return e
	}
	user, e := u.selectUser(c, true)
	if e != nil {
		return e
	}
	if user.ID == "" {
		return nil
	}
	link, e := c.AndroidLink(user.ID)
	if e != nil {
		return e
	}
	fmt.Fprintln(u.Out, link)

	return nil
}
func (u *UI) check() error {
	if e := u.stopped(); e != nil {
		return e
	}
	c, e := servercfg.Load(u.db())
	if e != nil {
		return e
	}
	if len(c.Users) == 0 {
		return errors.New("add a client first")
	}
	folders := []string{c.Users[0].Up, c.Users[0].Down}
	for _, a := range c.Accounts {
		ctx, stop := context.WithTimeout(context.Background(), 45*time.Second)
		cli, e := mailbox.Dial(ctx, a.Mail, folders)
		if e == nil {
			for _, f := range folders {
				if e = cli.Ensure(ctx, f); e != nil {
					break
				}
			}
			cli.Close()
		}
		stop()
		if e != nil {
			fmt.Fprintf(u.Out, "%s: %v\n", a.Mail.Username, e)
		} else {
			fmt.Fprintf(u.Out, "%s: TLS / LOGIN / folders OK\n", a.Mail.Username)
		}
	}
	return nil
}

const unit = `[Unit]
Description=ITOM encrypted IMAP TCP transport
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
ExecStart=/usr/local/bin/itom serve --state /var/lib/itom
Restart=on-failure
RestartSec=10
TimeoutStopSec=45
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/itom
[Install]
WantedBy=multi-user.target
`

// Install copies the manually supplied running binary atomically, preserving
// /var/lib/itom. An active service is restarted, with binary rollback on failure.
func Install() error {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return errors.New("installation requires Linux, sudo and systemd")
	}
	if _, e := exec.LookPath("systemctl"); e != nil {
		return e
	}
	if e := os.MkdirAll("/var/lib/itom", 0700); e != nil {
		return e
	}
	src, e := os.Executable()
	if e != nil {
		return e
	}
	dst := "/usr/local/bin/itom"
	wasActive := exec.Command("systemctl", "is-active", "--quiet", "itom.service").Run() == nil
	old, e := os.ReadFile(dst)
	hadOld := e == nil
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	data, e := os.ReadFile(src)
	if e != nil {
		return e
	}
	if len(data) > 100<<20 {
		return errors.New("binary exceeds limit")
	}
	unitPath := "/etc/systemd/system/itom.service"
	oldUnit, ue := os.ReadFile(unitPath)
	if ue != nil && !os.IsNotExist(ue) {
		return ue
	}
	if e = atomicFile(dst, data, 0755); e != nil {
		return e
	}
	rollback := func() {
		if hadOld {
			_ = atomicFile(dst, old, 0755)
		}
		if ue == nil {
			_ = atomicFile(unitPath, oldUnit, 0644)
		}
		_ = exec.Command("systemctl", "daemon-reload").Run()
		if wasActive {
			_ = exec.Command("systemctl", "restart", "itom.service").Run()
		}
	}
	if e = atomicFile(unitPath, []byte(unit), 0644); e != nil {
		rollback()
		return e
	}
	if e = exec.Command("systemctl", "daemon-reload").Run(); e != nil {
		rollback()
		return e
	}
	if e = exec.Command("systemctl", "enable", "itom.service").Run(); e != nil {
		rollback()
		return e
	}
	if wasActive {
		if e = exec.Command("systemctl", "restart", "itom.service").Run(); e == nil {
			time.Sleep(500 * time.Millisecond)
			e = exec.Command("systemctl", "is-active", "--quiet", "itom.service").Run()
		}
		if e != nil {
			rollback()
			return errors.New("restart failed; previous binary restored")
		}
	}
	fmt.Println("ITOM installed. Open the manager with sudo itom. Configuration preserved.")
	return nil
}
func atomicFile(path string, data []byte, mode os.FileMode) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".itom-install-")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(data)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	if e = os.Rename(name, path); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}

func (u *UI) diagnostics() error {
	c, e := servercfg.Load(u.db())
	if e != nil {
		return e
	}
	current := c.DiagnosticsMode
	if current == "" {
		current = "on"
	}
	fmt.Fprintln(u.Out, "Server logging and tracing:", current)
	mode, e := u.ask("Mode (on/off)")
	if e != nil {
		return e
	}
	if mode != "on" && mode != "off" {
		return errors.New("enter on or off")
	}
	c.DiagnosticsMode = mode
	if e = servercfg.Update(u.db(), func(c *servercfg.Config) error { c.DiagnosticsMode = mode; return nil }); e != nil {
		return e
	}
	fmt.Fprintln(u.Out, "Saved. Restart the service using menu item 9. Android tracing is controlled separately.")
	return nil
}

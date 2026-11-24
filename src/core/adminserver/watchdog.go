package adminserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"itom.local/core/adminapi"
	"itom.local/core/manager"
	"itom.local/core/servercfg"
)

func launchGuard(state, id string) error {
	if !watchdogAvailable() || !validID(id) {
		return errors.New("ADMIN_WATCHDOG_UNAVAILABLE")
	}
	// A manually launched server must not stop an unrelated system service.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pid, e := exec.CommandContext(ctx, "systemctl", "show", "--property=MainPID", "--value", "itom.service").Output()
	if e != nil || strings.TrimSpace(string(pid)) != strconv.Itoa(os.Getpid()) {
		return errors.New("ADMIN_SERVICE_NOT_MANAGED")
	}
	exe, e := os.Executable()
	if e != nil || filepath.Clean(exe) != "/usr/local/bin/itom" {
		return errors.New("ADMIN_SERVICE_NOT_INSTALLED")
	}
	abs, e := filepath.Abs(state)
	if e != nil || strings.ContainsAny(abs, "\r\n\t ") {
		return errors.New("ADMIN_STATE_INVALID")
	}
	unit := "itom-admin-" + id
	args := []string{"--quiet", "--collect", "--unit=" + unit, "--property=Type=exec", "--property=Restart=on-failure", "--property=RestartSec=1s", "--property=StartLimitIntervalSec=0", "--property=UMask=0077", "--property=NoNewPrivileges=yes", "--property=PrivateTmp=yes", "--property=ProtectSystem=strict", "--property=ProtectHome=yes", "--property=ReadWritePaths=" + abs + " /usr/local/bin /etc/systemd/system", "--", exe, "admin-guard", "--state", abs, "--transaction", id}
	if e = exec.CommandContext(ctx, "systemd-run", args...).Run(); e != nil {
		return errors.New("ADMIN_WATCHDOG_START_FAILED")
	}
	if e = exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", unit+".service").Run(); e != nil {
		return errors.New("ADMIN_WATCHDOG_START_FAILED")
	}
	return nil
}

func serviceAction(action string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "systemctl", action, "itom.service").Run()
}
func needsRestart(op string) bool {
	return op != "global_message" && op != "client_message" && op != "service_start"
}

// RunGuard is a separate systemd unit, not a child in itom.service's cgroup.
// Its durable phase allows systemd to restart the watchdog itself after a crash.
// It intentionally never opens, deletes or rewinds a physical quota journal.
type watchdogRuntime struct {
	now          func() time.Time
	wait         func()
	service      func(string) error
	install      func() error
	unitSnapshot func() (servercfg.GuardUnit, error)
	unitRestore  func(servercfg.GuardUnit) error
}

func defaultWatchdog() watchdogRuntime {
	return watchdogRuntime{now: time.Now, wait: func() { time.Sleep(time.Second) }, service: serviceAction, install: manager.Install, unitSnapshot: snapshotUnit, unitRestore: restoreUnit}
}
func RunGuard(state, id string) error {
	r := defaultWatchdog()
	r.install = func() error { return manager.InstallGuarded(state, id) }
	return runGuard(state, id, r)
}
func runGuard(state, id string, r watchdogRuntime) error {
	if !validID(id) {
		return errors.New("ADMIN_TRANSACTION_INVALID")
	}
	db := filepath.Join(state, "server.db")
	// Let the accepted RPC response leave before any service interruption. A
	// crash during this delay is harmless: configuration is still unchanged.
	r.wait()
	for {
		g, e := servercfg.GuardStatus(db)
		if e != nil {
			return e
		}
		if g.ID != id {
			return nil
		}
		if g.State == "canceled" || g.State == "committed" {
			return nil
		}
		if g.State == "rolled_back" {
			return r.service("start")
		}
		var q adminapi.Request
		if json.Unmarshal(g.Plan, &q) != nil || !mutation(q.Operation) {
			return errors.New("ADMIN_TRANSACTION_INVALID")
		}
		if g.State == "rolling_back" || g.Expired(r.now()) {
			if e = restoreGuard(db, g, r); e != nil {
				return e
			}
			return nil
		}
		if g.State == "armed" {
			if q.Operation == "service_install" && g.Unit == nil {
				unit, e := r.unitSnapshot()
				if e != nil {
					return e
				}
				if len(unit.Data) > 64<<10 {
					return errors.New("ADMIN_UNIT_LIMIT")
				}
				if e = servercfg.CaptureGuardUnit(db, id, unit); e != nil {
					return e
				}
			}
			if needsRestart(q.Operation) {
				if e = r.service("stop"); e != nil {
					_ = servercfg.MarkGuardRollback(db, id)
					continue
				}
			}
			if e = servercfg.ApplyGuard(db, id, r.now(), func(c *servercfg.Config) error { return apply(c, q) }); e != nil {
				_ = servercfg.MarkGuardRollback(db, id)
				continue
			}
			g, e = servercfg.GuardStatus(db)
			if e != nil {
				return e
			}
		}
		if g.State == "observing" && g.ActionDoneMS == 0 {
			if q.Operation == "service_install" {
				e = r.install()
			}
			if q.Operation == "service_start" {
				e = r.service("start")
			}
			if e == nil && needsRestart(q.Operation) && q.Operation != "service_stop" {
				e = r.service("start")
			}
			if e != nil {
				_ = servercfg.MarkGuardRollback(db, id)
				continue
			}
			if e = servercfg.CompleteGuardAction(db, id, r.now()); e != nil {
				return e
			}
		}
		r.wait()
	}
}

func restoreGuard(db string, g servercfg.Guard, r watchdogRuntime) error {
	if e := servercfg.MarkGuardRollback(db, g.ID); e != nil {
		return e
	}
	if e := r.service("stop"); e != nil {
		return e
	}
	if g.Unit != nil {
		if e := r.unitRestore(*g.Unit); e != nil {
			return e
		}
	}
	if e := servercfg.RollbackGuard(db, g.ID); e != nil {
		return e
	}
	if e := r.service("start"); e != nil {
		return fmt.Errorf("ADMIN_RESTORE_START_FAILED: %w", e)
	}
	return nil
}
func snapshotUnit() (servercfg.GuardUnit, error) { return snapshotUnitFiles("/etc/systemd/system") }
func snapshotUnitFiles(dir string) (servercfg.GuardUnit, error) {
	var unit servercfg.GuardUnit
	path := filepath.Join(dir, "itom.service")
	st, e := os.Lstat(path)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return unit, e
	}
	if e == nil {
		unit.Exists = true
		if st.Mode()&os.ModeSymlink != 0 {
			unit.Symlink = true
			unit.Link, e = os.Readlink(path)
			if e != nil {
				return unit, e
			}
			if len(unit.Link) > 4096 {
				return unit, errors.New("ADMIN_UNIT_LIMIT")
			}
		} else if st.Mode().IsRegular() {
			unit.Data, e = os.ReadFile(path)
			if e != nil {
				return unit, e
			}
		} else {
			return unit, errors.New("ADMIN_UNIT_INVALID")
		}
	}

	target, e := os.Readlink(filepath.Join(dir, "multi-user.target.wants", "itom.service"))
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return unit, e
	}
	if len(target) > 4096 {
		return unit, errors.New("ADMIN_UNIT_LIMIT")
	}
	unit.BootLink = target
	unit.BootLinkExists = e == nil
	return unit, nil
}
func restoreUnit(unit servercfg.GuardUnit) error {
	if e := restoreUnitFiles("/etc/systemd/system", unit); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "systemctl", "daemon-reload").Run()
}
func restoreUnitFiles(dir string, unit servercfg.GuardUnit) error {
	path := filepath.Join(dir, "itom.service")
	if unit.Exists && unit.Symlink {
		if e := atomicSymlink(dir, path, unit.Link); e != nil {
			return e
		}
	} else if unit.Exists {
		f, e := os.CreateTemp(dir, ".itom-admin-unit-")
		if e != nil {
			return e
		}
		name := f.Name()
		defer os.Remove(name)
		if e = f.Chmod(0644); e == nil {
			_, e = f.Write(unit.Data)
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
	} else if e := os.Remove(path); e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	wants := filepath.Join(dir, "multi-user.target.wants")
	link := filepath.Join(wants, "itom.service")
	if unit.BootLinkExists {
		if e := os.MkdirAll(wants, 0755); e != nil {
			return e
		}
		if e := atomicSymlink(wants, link, unit.BootLink); e != nil {
			return e
		}
	} else if e := os.Remove(link); e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}

	for _, folder := range []string{wants, dir} {
		d, e := os.Open(folder)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return e
		}
		e = d.Sync()
		_ = d.Close()
		if e != nil {
			return e
		}
	}
	return nil
}

func atomicSymlink(dir, path, target string) error {
	id, e := adminapi.NewID()
	if e != nil {
		return e
	}
	temp := filepath.Join(dir, ".itom-admin-link-"+id)
	if e = os.Symlink(target, temp); e != nil {
		return e
	}
	defer os.Remove(temp)
	return os.Rename(temp, path)
}

// Resume a durable trial after a host reboot or unexpected service restart.
// It is harmless if the original independent watchdog is still active.
func RecoverGuard(state string) error {
	g, e := servercfg.GuardStatus(filepath.Join(state, "server.db"))
	if e != nil {
		return e
	}
	if !g.Active() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	name := "itom-admin-" + g.ID + ".service"
	if exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", name).Run() == nil {
		return nil
	}
	return launchGuard(state, g.ID)
}

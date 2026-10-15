package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"itom.local/core/servercfg"
	"itom.local/core/tunnel"
	"os"
	"os/signal"
	"path/filepath"
	"time"
)

func (u *UI) followTraffic() error {
	cfg, err := servercfg.Load(u.db())
	if err != nil {
		return err
	}
	choice, err := u.ask("1 all clients, 2 select a client, 0 back")
	if err != nil || choice == "0" {
		return err
	}
	uid := ""
	if choice == "2" {
		user, err := u.selectUser(cfg, false)
		if err != nil {
			return err
		}
		if user.ID == "" {
			return nil
		}
		uid = user.ID
	} else if choice != "1" {
		return errors.New("unknown menu option")
	}
	fmt.Fprintln(u.Out, "Useful TCP payload at the server exit. Counters start with the service. Ctrl+C returns to the menu. Logging is not required.")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return ShowTraffic(ctx, u.State, uid, true, u.Out)
}

func trafficRate(current, previous uint64, elapsedMS int64) string {
	if elapsedMS <= 0 || current < previous {
		return "—"
	}
	return fmt.Sprintf("%.2f", float64(current-previous)*8/float64(elapsedMS)/1000)
}
func ShowTraffic(ctx context.Context, state, uid string, watch bool, out io.Writer) error {
	cfg, err := servercfg.Load(filepath.Join(state, "server.db"))
	if err != nil {
		return err
	}
	if uid != "" {
		found := false
		for _, u := range cfg.Users {
			found = found || u.ID == uid
		}
		if !found {
			return errors.New("client not found")
		}
	}
	var previous tunnel.TrafficSnapshot
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		s, err := tunnel.ReadTraffic(filepath.Join(state, "tunnel-server", tunnel.TrafficFile))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return errors.New("statistics unavailable; start the Rev9 server")
			}
			return err
		}
		status := "running"
		if !s.Running {
			status = "stopped / stale data"
		}
		fmt.Fprintf(out, "\n%s | server %s\n", time.UnixMilli(s.SampledAtMS).Format("15:04:05"), status)
		fmt.Fprintln(out, "Client | TCP | uploaded MB | downloaded MB | upload / download Mbps")
		for _, user := range s.Users {
			if uid != "" && user.ID != uid {
				continue
			}
			name := user.ID
			for _, u := range cfg.Users {
				if u.ID == user.ID {
					name = u.Name + " (" + u.ID + ")"
				}
			}
			up, down := "—", "—"
			if previous.Instance == s.Instance && previous.Running && s.Running {
				for _, p := range previous.Users {
					if p.ID == user.ID {
						up = trafficRate(user.Upload, p.Upload, s.SampledAtMS-previous.SampledAtMS)
						down = trafficRate(user.Download, p.Download, s.SampledAtMS-previous.SampledAtMS)
					}
				}
			}
			fmt.Fprintf(out, "%s | %d | %.2f | %.2f | %s / %s\n", name, user.Sockets, float64(user.Upload)/1e6, float64(user.Download)/1e6, up, down)
		}
		previous = s
		if !watch {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/GSConnect/gsconnect-mount-manager/internal/config"
	"github.com/GSConnect/gsconnect-mount-manager/internal/gsconnect"
	"github.com/GSConnect/gsconnect-mount-manager/internal/manager"
)

const version = "0.1.0"

func main() {
	flag.Usage = usage
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		run()
		return
	}
	switch args[0] {
	case "version":
		fmt.Println(version)
	case "status", "devices", "mount", "unmount", "reconnect", "doctor":
		command(args)
	default:
		usage()
		os.Exit(2)
	}
}
func setup() (*manager.Manager, *gsconnect.Client, error) {
	c, e := config.Load(config.Path())
	if e != nil {
		return nil, nil, e
	}
	g, e := gsconnect.Connect()
	if e != nil {
		return nil, nil, e
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: parseLevel(c.LogLevel)}))
	return manager.New(c, g, manager.NewGVfsBackend(g), log), g, nil
}
func run() {
	m, g, e := setup()
	if e != nil {
		fatal(e)
	}
	defer g.Close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	signals, e := g.Subscribe()
	if e != nil {
		fmt.Fprintln(os.Stderr, "warning: event subscription unavailable:", e)
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-signals:
				if !ok {
					return
				}
				_ = m.Refresh(ctx)
			}
		}
	}()
	m.Run(ctx)
}
func command(args []string) {
	m, g, e := setup()
	if e != nil {
		fatal(e)
	}
	defer g.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if args[0] == "doctor" {
		doctor(ctx, g, m)
		return
	}
	// Commands must not race their requested operation with the daemon's
	// automatic mounting logic. They only need an up-to-date device snapshot.
	if e = m.Discover(ctx); e != nil {
		fatal(e)
	}
	if args[0] == "status" || args[0] == "devices" {
		for _, s := range m.Status() {
			if args[0] == "devices" {
				fmt.Printf("%s\t%s\tpaired=%t connected=%t\n", s.Device.ID, s.Device.Name, s.Device.Paired, s.Device.Connected)
			} else {
				fmt.Printf("%s\t%s\t%s\t%s\t%s\n", s.Device.ID, s.Device.Name, s.State, s.Link, s.LastError)
			}
		}
		return
	}
	if len(args) != 2 {
		fatal(errors.New("device id or unique name is required"))
	}
	id := resolve(args[1], m)
	if id == "" {
		fatal(errors.New("unknown or ambiguous device"))
	}
	switch args[0] {
	case "mount":
		m.Mount(id)
		awaitMount(m, id)
	case "reconnect":
		m.Reconnect(id)
		awaitMount(m, id)
	case "unmount":
		if e := m.Unmount(ctx, id); e != nil {
			fatal(e)
		}
		fmt.Println("unmount requested")
	}
}
func awaitMount(m *manager.Manager, id string) {
	deadline := time.NewTimer(25 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		for _, s := range m.Status() {
			if s.Device.ID != id {
				continue
			}
			if s.State == "MOUNTED" {
				fmt.Println("mounted", s.Link)
				return
			}
			if s.State == "DEGRADED" {
				fatal(errors.New(s.LastError))
			}
		}
		select {
		case <-deadline.C:
			fatal(errors.New("mount request timed out; inspect status or journal"))
		case <-tick.C:
		}
	}
}
func resolve(q string, m *manager.Manager) string {
	var found string
	for _, s := range m.Status() {
		if s.Device.ID == q || strings.EqualFold(s.Device.Name, q) || strings.HasPrefix(s.Device.ID, q) {
			if found != "" && found != s.Device.ID {
				return ""
			}
			found = s.Device.ID
		}
	}
	return found
}
func doctor(ctx context.Context, g *gsconnect.Client, m *manager.Manager) {
	fmt.Println("GSConnect Mount Manager doctor")
	if e := g.Available(); e != nil {
		fmt.Println("D-Bus / GSConnect: FAIL", e)
		return
	}
	fmt.Println("D-Bus / GSConnect: OK")
	if _, e := os.Stat("/usr/bin/gio"); e != nil {
		fmt.Println("GIO: FAIL", e)
	} else {
		fmt.Println("GIO: OK")
	}
	if e := m.Refresh(ctx); e != nil {
		fmt.Println("Device enumeration: FAIL", e)
		return
	}
	for _, s := range m.Status() {
		fmt.Printf("device=%s name=%q paired=%t connected=%t state=%s error=%q\n", s.Device.ID, s.Device.Name, s.Device.Paired, s.Device.Connected, s.State, s.LastError)
	}
}
func parseLevel(s string) slog.Level {
	if s == "debug" {
		return slog.LevelDebug
	}
	if s == "error" {
		return slog.LevelError
	}
	return slog.LevelInfo
}
func usage() {
	fmt.Fprintln(os.Stderr, "usage: gsconnect-mount-manager [status|devices|mount <device>|unmount <device>|reconnect <device>|doctor|version]")
}
func fatal(e error) { fmt.Fprintln(os.Stderr, "gsconnect-mount-manager:", e); os.Exit(1) }

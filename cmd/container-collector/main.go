package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/aartmoon/homeserver-analytics/internal/collector"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"
)

func main() {
	exe, _ := os.Executable()
	root := flag.String("root", filepath.Dir(filepath.Dir(exe)), "monitoring project directory")
	socket := flag.String("socket", "/var/run/docker.sock", "local Docker Unix socket")
	interval := flag.Duration("interval", 0, "repeat interval, zero for one-shot")
	health := flag.Bool("health-check", false, "check most recent successful collection")
	flag.Parse()
	if *health {
		info, err := os.Stat(filepath.Join(*root, "textfile/containers.prom"))
		if err != nil || time.Since(info.ModTime()) > 180*time.Second {
			os.Exit(1)
		}
		return
	}
	debug.SetMemoryLimit(32 << 20)
	state := filepath.Join(*root, "data/container-collector")
	if err := os.MkdirAll(state, 0700); err != nil {
		fail()
	}
	lock, err := os.OpenFile(filepath.Join(state, "collector.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		fail()
	}
	defer lock.Close()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		fmt.Fprintln(os.Stderr, "Collector is already running")
		os.Exit(1)
	}
	parent, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	api := collector.NewAPI(*socket)
	for {
		ctx, cancel := context.WithTimeout(parent, 40*time.Second)
		err := collector.Collect(ctx, api, *root)
		cancel()
		if err != nil {
			if *interval == 0 {
				fail()
			}
			fmt.Fprintln(os.Stderr, "Collection failed; retaining last successful metrics")
		}
		if *interval == 0 {
			return
		}
		select {
		case <-parent.Done():
			return
		case <-time.After(*interval):
		}
	}
}
func fail() {
	fmt.Fprintln(os.Stderr, "Container collector failed; last successful metrics retained")
	os.Exit(1)
}

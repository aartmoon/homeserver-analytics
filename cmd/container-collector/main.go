package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/aartmoon/homeserver-analytics/internal/collector"
	"os"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"
)

func main() {
	exe, _ := os.Executable()
	root := flag.String("root", filepath.Dir(filepath.Dir(exe)), "monitoring project directory")
	socket := flag.String("socket", "/var/run/docker.sock", "local Docker Unix socket")
	flag.Parse()
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
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	if collector.Collect(ctx, collector.NewAPI(*socket), *root) != nil {
		fail()
	}
}
func fail() {
	fmt.Fprintln(os.Stderr, "Container collector failed; last successful metrics retained")
	os.Exit(1)
}

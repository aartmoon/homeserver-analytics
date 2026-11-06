package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/aartmoon/homeserver-analytics/internal/serverbot"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
)

func main() {
	exe, _ := os.Executable()
	root := flag.String("root", filepath.Dir(exe), "bot configuration directory")
	check := flag.Bool("check-config", false, "validate without polling or opening the inbox")
	flag.Parse()
	debug.SetMemoryLimit(64 << 20)
	cfg, err := serverbot.LoadConfig(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *check {
		fmt.Println("Bot configuration valid; no network or polling started")
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	if err = serverbot.Run(ctx, cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

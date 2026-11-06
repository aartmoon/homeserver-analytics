package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/aartmoon/homeserver-analytics/internal/serverbot"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"
)

func main() {
	exe, _ := os.Executable()
	root := flag.String("root", filepath.Dir(exe), "bot configuration directory")
	check := flag.Bool("check-config", false, "validate without polling or opening the inbox")
	health := flag.Bool("health-check", false, "check private webhook readiness")
	flag.Parse()
	if *health {
		port := os.Getenv("MONITORING_WEBHOOK_PORT")
		if port == "" {
			port = "9088"
		}
		client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 3 * time.Second}
		res, err := client.Get("http://127.0.0.1:" + port + "/healthz")
		if err != nil {
			os.Exit(1)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
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

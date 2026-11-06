package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/aartmoon/homeserver-analytics/internal/dockerproxy"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"
)

func main() {
	upstream := flag.String("upstream", "/var/run/docker.sock", "private upstream Docker socket")
	listen := flag.String("listen", "/run/docker-proxy/docker.sock", "restricted Unix socket")
	health := flag.Bool("health-check", false, "check restricted endpoint")
	flag.Parse()
	debug.SetMemoryLimit(24 << 20)
	if *health {
		client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", *listen)
		}}, Timeout: 3 * time.Second}
		response, err := client.Get("http://docker/version")
		if err != nil {
			os.Exit(1)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	if err := os.MkdirAll(filepath.Dir(*listen), 0700); err != nil {
		fail()
	}
	if info, err := os.Lstat(*listen); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			fail()
		}
		if os.Remove(*listen) != nil {
			fail()
		}
	} else if !os.IsNotExist(err) {
		fail()
	}
	listener, err := net.Listen("unix", *listen)
	if err != nil {
		fail()
	}
	defer listener.Close()
	if os.Chmod(*listen, 0660) != nil {
		fail()
	}
	server := &http.Server{Handler: dockerproxy.New(*upstream), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 8 << 10}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	go func() {
		<-ctx.Done()
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(stop)
	}()
	if err = server.Serve(listener); err != nil && err != http.ErrServerClosed {
		fail()
	}
}
func fail() { fmt.Fprintln(os.Stderr, "Restricted Docker metrics proxy failed"); os.Exit(1) }

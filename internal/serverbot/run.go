package serverbot

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

func Lock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("bot is already running")
	}
	return f, nil
}
func Run(ctx context.Context, c Config) error {
	if err := os.MkdirAll(c.Data, 0700); err != nil {
		return errors.New("cannot create bot data directory")
	}
	if err := os.Chmod(c.Data, 0700); err != nil {
		return err
	}
	lock, err := Lock(filepath.Join(c.Data, "bot.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	q, err := OpenQueue(filepath.Join(c.Data, "monitoring-inbox.sqlite3"), 1000)
	if err != nil {
		return errors.New("cannot open durable inbox")
	}
	defer q.Close()
	tg, err := NewTelegram(c.Token, c.Proxy)
	if err != nil {
		return err
	}
	defer tg.Close()
	tg.Status = func() string { return StatusFrom(c.ProcFS, c.RootFS) }
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var workers sync.WaitGroup
	var server *http.Server
	if c.Enabled {
		listener, err := net.Listen("tcp", net.JoinHostPort(c.Host, fmt.Sprint(c.Port)))
		if err != nil {
			return errors.New("cannot bind private webhook listener")
		}
		server = &http.Server{Handler: Webhook(q, c.GrafanaSecret, c.KumaSecret), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 8 << 10}
		go func() {
			if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				cancel()
			}
		}()
		workers.Add(1)
		go func() { defer workers.Done(); q.Worker(runCtx, tg, c.Owner) }()
	}
	commandCtx, commandCancel := context.WithTimeout(runCtx, 10*time.Second)
	_ = tg.Commands(commandCtx)
	commandCancel()
	workers.Add(1)
	go func() { defer workers.Done(); tg.Poll(runCtx, q, c.Owner) }()
	<-runCtx.Done()
	if server != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
		}
		shutdownCancel()
	}
	cancel()
	workers.Wait()
	return nil
}

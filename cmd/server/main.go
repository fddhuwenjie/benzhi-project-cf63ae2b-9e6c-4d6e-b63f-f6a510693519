package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/api"
	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/application"
	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/repository"
	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/selfcheck"
)

type config struct {
	addr, dbPath string
	selfCheck    bool
}

func loadConfig() config {
	var c config
	flag.StringVar(&c.addr, "addr", defaultLoopbackHost+":19081", "HTTP 回环监听地址")
	flag.StringVar(&c.dbPath, "db", "curve-certification.db", "SQLite 数据库路径")
	flag.BoolVar(&c.selfCheck, "self-check", false, "执行真实 HTTP 全流程自检并退出")
	flag.Parse()
	if port := strings.TrimSpace(os.Getenv("PORT")); port != "" && !flagWasSet("addr") {
		if _, err := strconv.ParseUint(port, 10, 16); err == nil {
			c.addr = defaultLoopbackHost + ":" + port
		} else {
			c.addr = "invalid-port"
		}
	}
	return c
}
func flagWasSet(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}
func validateAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("无效 -addr: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("-addr 必须使用明确的回环 IP（127.0.0.1 或 ::1）")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1024 || n > 65535 {
		return errors.New("监听端口必须在 1024 到 65535 之间")
	}
	return nil
}

func run(c config) error {
	if err := validateAddr(c.addr); err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	cleanup := func() {}
	if c.selfCheck {
		dir, err := os.MkdirTemp("", "curve-cert-selfcheck-")
		if err != nil {
			return err
		}
		cleanup = func() { _ = os.RemoveAll(dir) }
		c.dbPath = filepath.Join(dir, "selfcheck.db")
	}
	defer cleanup()
	store, err := repository.Open(c.dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	service := application.New(store)
	handler := api.New(service, logger)
	httpServer := &http.Server{Addr: c.addr, Handler: handler, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	listener, err := net.Listen("tcp", c.addr)
	if err != nil {
		return err
	}
	serveErr := make(chan error, 1)
	go func() {
		err := httpServer.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
		close(serveErr)
	}()
	if c.selfCheck {
		base := "http://" + listener.Addr().String()
		err = selfcheck.New(base).Run(context.Background())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), selfCheckShutdownTimeout)
		defer cancel()
		shutdownErr := httpServer.Shutdown(shutdownCtx)
		if err != nil {
			return err
		}
		if shutdownErr != nil {
			return shutdownErr
		}
		logger.Info("self_check_completed", "service", serviceName, "status", "ok")
		return nil
	}
	logger.Info("server_started", "service", serviceName, "addr", listener.Addr().String(), "db", c.dbPath)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
}
func main() {
	if err := run(loadConfig()); err != nil {
		fmt.Fprintln(os.Stderr, "服务失败:", err)
		os.Exit(1)
	}
}

package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/local/dbbroker/internal/api"
	"github.com/local/dbbroker/internal/broker"
	"github.com/local/dbbroker/internal/config"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		log.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	svc, err := broker.New(ctx, cfg.DatabaseURL, cfg.PublicDatabaseURL, cfg.RedisURL, cfg.PublicRedisURL, cfg.LeaseTTL, cfg.MaxBackupBytes, log)
	if err != nil {
		log.Error("start broker", "error", err)
		os.Exit(1)
	}
	defer svc.Close()
	server := &http.Server{Addr: cfg.Listen, Handler: api.New(svc, cfg.Token, log), ReadHeaderTimeout: 5 * time.Second}
	go collect(ctx, svc, cfg.GCInterval, log)
	go func() {
		<-ctx.Done()
		shutdownCtx, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		_ = server.Shutdown(shutdownCtx)
	}()
	log.Info("dbbroker listening", "address", cfg.Listen, "redis", cfg.RedisURL != "")
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("http server", "error", err)
		os.Exit(1)
	}
}

func collect(ctx context.Context, svc *broker.Service, interval time.Duration, log *slog.Logger) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := svc.Collect(ctx)
			if err != nil {
				log.Error("garbage collection", "error", err)
			} else if n > 0 {
				log.Info("garbage collected", "count", n)
			}
		}
	}
}

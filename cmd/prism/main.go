package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/raviikumar001/prism-gateway/internal/config"
	"github.com/raviikumar001/prism-gateway/internal/httpapi"
	"github.com/raviikumar001/prism-gateway/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := store.ConnectPostgres(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("postgres connect failed", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := store.Migrate(ctx, db); err != nil {
		slog.Error("migrations failed", "err", err)
		os.Exit(1)
	}

	rdb, err := store.ConnectRedis(ctx, cfg.RedisURL)
	if err != nil {
		slog.Error("redis connect failed", "err", err)
		os.Exit(1)
	}
	defer func() { _ = rdb.Close() }()

	if cfg.SeedOnBoot {
		if err := store.SeedFromData(ctx, db, cfg.DataDir); err != nil {
			slog.Error("seed failed", "err", err)
			os.Exit(1)
		}
	}

	srv := httpapi.NewServer(cfg, db, rdb)
	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("prism listening", "addr", httpServer.Addr, "provider_mode", cfg.ProviderMode)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
}

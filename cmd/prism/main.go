package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/raviikumar001/prism-gateway/internal/auth"
	"github.com/raviikumar001/prism-gateway/internal/budget"
	"github.com/raviikumar001/prism-gateway/internal/cache"
	"github.com/raviikumar001/prism-gateway/internal/config"
	"github.com/raviikumar001/prism-gateway/internal/gatewaycfg"
	"github.com/raviikumar001/prism-gateway/internal/httpapi"
	"github.com/raviikumar001/prism-gateway/internal/limit"
	"github.com/raviikumar001/prism-gateway/internal/meter"
	"github.com/raviikumar001/prism-gateway/internal/provider"
	"github.com/raviikumar001/prism-gateway/internal/route"
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

	gwCfg, err := gatewaycfg.Load(cfg.GatewayConfig)
	if err != nil {
		slog.Error("gateway config load failed", "err", err)
		os.Exit(1)
	}
	if err := gwCfg.ApplyEnvSecrets(); err != nil {
		slog.Error("gateway secrets failed", "err", err)
		os.Exit(1)
	}
	if cfg.ProviderMode == "live" {
		if err := gwCfg.RequireAPIKeys(); err != nil {
			slog.Error("live provider keys missing", "err", err)
			os.Exit(1)
		}
	}

	authSvc := auth.NewService(db)
	resolver := route.NewResolver(gwCfg)
	exec := provider.NewExecutor(gwCfg, 30*time.Second, 64)
	meterSvc := meter.NewService(db)
	rpm := limit.NewRPM(rdb)
	_ = rpm.EnsureScript(ctx)
	budgetSvc := budget.NewService(rdb)
	cacheSvc := cache.NewService(db)
	reqLogger := httpapi.NewRequestLogger(db)
	defer reqLogger.Close()

	srv := httpapi.NewServer(cfg, db, rdb, authSvc, resolver, exec, meterSvc, rpm, budgetSvc, cacheSvc, reqLogger)
	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		// WriteTimeout left unset (0) so SSE streams are not cut off.
	}

	go func() {
		slog.Info("prism listening",
			"addr", httpServer.Addr,
			"provider_mode", cfg.ProviderMode,
			"gateway_config", cfg.GatewayConfig,
		)
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

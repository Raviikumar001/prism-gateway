package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Port           string
	DatabaseURL    string
	RedisURL       string
	AdminToken     string
	ProviderMode   string
	DataDir        string
	SeedOnBoot     bool
	LogLevel       slog.Level
	CerebrasKey    string
	OpenRouterKey  string
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		Port:          getenv("PORT", "8080"),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		RedisURL:      os.Getenv("REDIS_URL"),
		AdminToken:    getenv("ADMIN_TOKEN", "dev-admin-change-me"),
		ProviderMode:  strings.ToLower(getenv("PROVIDER_MODE", "mocks")),
		DataDir:       getenv("DATA_DIR", "data"),
		SeedOnBoot:    getenvBool("SEED_ON_BOOT", true),
		CerebrasKey:   os.Getenv("CEREBRAS_API_KEY"),
		OpenRouterKey: os.Getenv("OPENROUTER_API_KEY"),
	}

	level, err := parseLogLevel(getenv("LOG_LEVEL", "info"))
	if err != nil {
		return nil, err
	}
	cfg.LogLevel = level

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.RedisURL == "" {
		return nil, fmt.Errorf("REDIS_URL is required")
	}
	if cfg.ProviderMode != "mocks" && cfg.ProviderMode != "live" {
		return nil, fmt.Errorf("PROVIDER_MODE must be mocks or live")
	}
	return cfg, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func parseLogLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("invalid LOG_LEVEL: %s", s)
	}
}

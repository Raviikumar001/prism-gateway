package config_test

import (
	"testing"
	"time"

	"github.com/raviikumar001/prism-gateway/internal/config"
)

func TestLoadParsesUpstreamTimeout(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("REDIS_URL", "redis://example")
	t.Setenv("PROVIDER_MODE", "mocks")
	t.Setenv("UPSTREAM_TIMEOUT", "3m")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UpstreamTimeout != 3*time.Minute {
		t.Fatalf("timeout = %s", cfg.UpstreamTimeout)
	}
}

func TestLoadRejectsInvalidUpstreamTimeout(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("REDIS_URL", "redis://example")
	t.Setenv("UPSTREAM_TIMEOUT", "0s")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected invalid timeout error")
	}
}

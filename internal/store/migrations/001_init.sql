-- +migrate Up
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tenants (
    virtual_key TEXT PRIMARY KEY,
    team TEXT NOT NULL,
    monthly_budget_usd NUMERIC(18, 8) NOT NULL,
    rpm INTEGER NOT NULL,
    tpm INTEGER NOT NULL DEFAULT 0,
    model_allowlist TEXT[] NOT NULL DEFAULT '{}',
    cache_enabled BOOLEAN NOT NULL DEFAULT false,
    cache_threshold DOUBLE PRECISION NOT NULL DEFAULT 0.92,
    status TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS model_prices (
    model TEXT PRIMARY KEY,
    input_per_1m NUMERIC(18, 8) NOT NULL,
    output_per_1m NUMERIC(18, 8) NOT NULL
);

CREATE TABLE IF NOT EXISTS usage_monthly (
    virtual_key TEXT NOT NULL REFERENCES tenants(virtual_key),
    month TEXT NOT NULL,
    requests BIGINT NOT NULL DEFAULT 0,
    prompt_tokens BIGINT NOT NULL DEFAULT 0,
    completion_tokens BIGINT NOT NULL DEFAULT 0,
    cost_micro_cents BIGINT NOT NULL DEFAULT 0,
    cache_hits BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (virtual_key, month)
);

CREATE TABLE IF NOT EXISTS request_logs (
    request_id TEXT PRIMARY KEY,
    virtual_key TEXT NOT NULL,
    requested_model TEXT NOT NULL,
    resolved_provider TEXT,
    resolved_model TEXT,
    status TEXT NOT NULL,
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    cost_micro_cents BIGINT NOT NULL DEFAULT 0,
    cache TEXT NOT NULL DEFAULT 'miss',
    fallback BOOLEAN NOT NULL DEFAULT false,
    route_reason TEXT,
    retries INTEGER NOT NULL DEFAULT 0,
    latency_ms INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_request_logs_key_created
    ON request_logs (virtual_key, created_at DESC);

-- 384-dim placeholder; live embedder may change later via migration
CREATE TABLE IF NOT EXISTS cache_entries (
    id BIGSERIAL PRIMARY KEY,
    virtual_key TEXT NOT NULL REFERENCES tenants(virtual_key),
    prompt_hash TEXT NOT NULL,
    prompt_text TEXT NOT NULL,
    embedding vector(384),
    response_body JSONB NOT NULL,
    hit_count BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (virtual_key, prompt_hash)
);

CREATE INDEX IF NOT EXISTS idx_cache_entries_key ON cache_entries (virtual_key);

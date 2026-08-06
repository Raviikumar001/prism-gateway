package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

type seedKeysFile struct {
	Tenants []struct {
		Team             string   `json:"team"`
		VirtualKey       string   `json:"virtual_key"`
		MonthlyBudgetUSD float64  `json:"monthly_budget_usd"`
		ModelAllowlist   []string `json:"model_allowlist"`
		RateLimit        struct {
			RequestsPerMinute int `json:"requests_per_minute"`
			TokensPerMinute   int `json:"tokens_per_minute"`
		} `json:"rate_limit"`
		SemanticCache struct {
			Enabled             bool    `json:"enabled"`
			SimilarityThreshold float64 `json:"similarity_threshold"`
		} `json:"semantic_cache"`
	} `json:"tenants"`
}

type priceEntry struct {
	InputPer1M  float64 `json:"input_per_1m"`
	OutputPer1M float64 `json:"output_per_1m"`
}

func SeedFromData(ctx context.Context, db *pgxpool.Pool, dataDir string) error {
	if err := seedTenants(ctx, db, filepath.Join(dataDir, "seed_keys.json")); err != nil {
		return err
	}
	if err := seedPrices(ctx, db, filepath.Join(dataDir, "model_pricing.json")); err != nil {
		return err
	}
	return nil
}

func seedTenants(ctx context.Context, db *pgxpool.Pool, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read seed keys: %w", err)
	}
	var file seedKeysFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return fmt.Errorf("parse seed keys: %w", err)
	}

	for _, t := range file.Tenants {
		threshold := t.SemanticCache.SimilarityThreshold
		if threshold == 0 {
			threshold = 0.92
		}
		_, err := db.Exec(ctx, `
			INSERT INTO tenants (
				virtual_key, team, monthly_budget_usd, rpm, tpm,
				model_allowlist, cache_enabled, cache_threshold, status
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'active')
			ON CONFLICT (virtual_key) DO UPDATE SET
				team = EXCLUDED.team,
				monthly_budget_usd = EXCLUDED.monthly_budget_usd,
				rpm = EXCLUDED.rpm,
				tpm = EXCLUDED.tpm,
				model_allowlist = EXCLUDED.model_allowlist,
				cache_enabled = EXCLUDED.cache_enabled,
				cache_threshold = EXCLUDED.cache_threshold,
				status = 'active'
		`, t.VirtualKey, t.Team, t.MonthlyBudgetUSD, t.RateLimit.RequestsPerMinute,
			t.RateLimit.TokensPerMinute, t.ModelAllowlist, t.SemanticCache.Enabled, threshold)
		if err != nil {
			return fmt.Errorf("upsert tenant %s: %w", t.VirtualKey, err)
		}
	}
	return nil
}

func seedPrices(ctx context.Context, db *pgxpool.Pool, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read pricing: %w", err)
	}
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		return fmt.Errorf("parse pricing: %w", err)
	}

	for model, body := range rawMap {
		if strings.HasPrefix(model, "_") {
			continue
		}
		var p priceEntry
		if err := json.Unmarshal(body, &p); err != nil {
			return fmt.Errorf("parse price for %s: %w", model, err)
		}
		_, err := db.Exec(ctx, `
			INSERT INTO model_prices (model, input_per_1m, output_per_1m)
			VALUES ($1,$2,$3)
			ON CONFLICT (model) DO UPDATE SET
				input_per_1m = EXCLUDED.input_per_1m,
				output_per_1m = EXCLUDED.output_per_1m
		`, model, p.InputPer1M, p.OutputPer1M)
		if err != nil {
			return fmt.Errorf("upsert price %s: %w", model, err)
		}
	}
	return nil
}

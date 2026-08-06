package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrMissingKey = errors.New("missing api key")
	ErrInvalidKey = errors.New("invalid api key")
	ErrDisabled   = errors.New("key disabled")
)

type Tenant struct {
	VirtualKey       string
	Team             string
	MonthlyBudgetUSD float64
	RPM              int
	TPM              int
	Allowlist        []string
	CacheEnabled     bool
	CacheThreshold   float64
	Status           string
}

type Service struct {
	db *pgxpool.Pool
}

func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

func BearerToken(header string) (string, error) {
	header = strings.TrimSpace(header)
	if header == "" {
		return "", ErrMissingKey
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", ErrMissingKey
	}
	key := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if key == "" {
		return "", ErrMissingKey
	}
	return key, nil
}

func (s *Service) Lookup(ctx context.Context, virtualKey string) (*Tenant, error) {
	var t Tenant
	err := s.db.QueryRow(ctx, `
		SELECT virtual_key, team, monthly_budget_usd, rpm, tpm,
		       model_allowlist, cache_enabled, cache_threshold, status
		FROM tenants WHERE virtual_key = $1
	`, virtualKey).Scan(
		&t.VirtualKey, &t.Team, &t.MonthlyBudgetUSD, &t.RPM, &t.TPM,
		&t.Allowlist, &t.CacheEnabled, &t.CacheThreshold, &t.Status,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidKey
	}
	if err != nil {
		return nil, err
	}
	if t.Status != "active" {
		return nil, ErrDisabled
	}
	return &t, nil
}

func (t *Tenant) AllowsModel(model string) bool {
	for _, m := range t.Allowlist {
		if m == "*" || m == model {
			return true
		}
	}
	return false
}

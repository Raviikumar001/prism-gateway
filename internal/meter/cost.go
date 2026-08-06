package meter

import (
	"context"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Price struct {
	InputPer1M  float64
	OutputPer1M float64
}

type Service struct {
	db *pgxpool.Pool
}

func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

func (s *Service) Price(ctx context.Context, model string) (Price, error) {
	var p Price
	err := s.db.QueryRow(ctx, `
		SELECT input_per_1m, output_per_1m FROM model_prices WHERE model = $1
	`, model).Scan(&p.InputPer1M, &p.OutputPer1M)
	if err == pgx.ErrNoRows {
		return Price{}, fmt.Errorf("no price for model %q", model)
	}
	return p, err
}

func CostUSD(promptTokens, completionTokens int, price Price) float64 {
	in := (float64(promptTokens) / 1_000_000.0) * price.InputPer1M
	out := (float64(completionTokens) / 1_000_000.0) * price.OutputPer1M
	return in + out
}

// ToMicroCents converts USD to integer micro-cents (1 USD = 1e8 micro-cents).
func ToMicroCents(costUSD float64) int64 {
	return int64(math.Round(costUSD * 100_000_000.0))
}

func FormatUSD(costUSD float64) string {
	// Trim trailing zeros while keeping enough precision for tiny mock costs.
	return fmt.Sprintf("%.8f", costUSD)
}

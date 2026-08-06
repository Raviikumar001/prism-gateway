package limit_test

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/raviikumar001/prism-gateway/internal/limit"
	"github.com/redis/go-redis/v9"
)

func TestRPMAllowAndReject(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	rpm := limit.NewRPM(rdb)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := rpm.Allow(ctx, "k1", 3); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	if err := rpm.Allow(ctx, "k1", 3); err != limit.ErrRateLimited {
		t.Fatalf("expected rate limited, got %v", err)
	}
	// other key unaffected
	if err := rpm.Allow(ctx, "k2", 3); err != nil {
		t.Fatalf("other key: %v", err)
	}
}

func TestTPMAllowAndRejectWeightedReservations(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	rpm := limit.NewRPM(rdb)
	ctx := context.Background()

	if err := rpm.AllowTokens(ctx, "tokens", 7, 10); err != nil {
		t.Fatal(err)
	}
	if err := rpm.AllowTokens(ctx, "tokens", 4, 10); err != limit.ErrRateLimited {
		t.Fatalf("expected token rate limit, got %v", err)
	}
	if err := rpm.AllowTokens(ctx, "other", 4, 10); err != nil {
		t.Fatalf("other key: %v", err)
	}
}

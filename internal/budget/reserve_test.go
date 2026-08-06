package budget_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/raviikumar001/prism-gateway/internal/budget"
	"github.com/redis/go-redis/v9"
)

func TestReserveSettleAndSecondReject(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	svc := budget.NewService(rdb)
	ctx := context.Background()

	const budgetCents = 1000 // $0.00001
	rsv, err := svc.Reserve(ctx, "demo", budgetCents, 500)
	if err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	if err := svc.Settle(ctx, rsv, 900); err != nil {
		t.Fatalf("settle: %v", err)
	}
	_, err = svc.Reserve(ctx, "demo", budgetCents, 500)
	if err != budget.ErrBudgetExceeded {
		t.Fatalf("expected budget exceeded, got %v", err)
	}
}

func TestReservationTTLReclaim(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	svc := budget.NewService(rdb)
	ctx := context.Background()

	const budgetCents = 1000
	rsv, err := svc.Reserve(ctx, "ttl", budgetCents, 800)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}

	// Simulate TTL expiry: score in the past so next reserve reclaims the hold.
	zkey := "budget:ttl:" + rsv.Month + ":z"
	if err := rdb.ZAdd(ctx, zkey, redis.Z{Score: 1, Member: rsv.ID}).Err(); err != nil {
		t.Fatalf("force expire: %v", err)
	}

	rsv2, err := svc.Reserve(ctx, "ttl", budgetCents, 800)
	if err != nil {
		t.Fatalf("reserve after expiry: %v", err)
	}
	if err := svc.Settle(ctx, rsv2, 100); err != nil {
		t.Fatalf("settle: %v", err)
	}
}

func TestConcurrentReserveNoOverAdmit(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	svc := budget.NewService(rdb)
	ctx := context.Background()

	const (
		budgetCents = 1000
		estimate    = 400
		workers     = 50
	)
	var allowed atomic.Int64
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			rsv, err := svc.Reserve(ctx, "race", budgetCents, estimate)
			if err == nil {
				allowed.Add(1)
				_ = svc.Settle(ctx, rsv, estimate)
			}
		}()
	}
	wg.Wait()
	if got := allowed.Load(); got > 2 {
		t.Fatalf("over-admitted: %d", got)
	}
	if allowed.Load() < 1 {
		t.Fatalf("expected at least one admit")
	}
}

func TestRefreshExtendsReservationAndDetectsSettlement(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	svc := budget.NewService(rdb)
	ctx := context.Background()

	rsv, err := svc.Reserve(ctx, "stream", 10_000, 500)
	if err != nil {
		t.Fatal(err)
	}
	zkey := "budget:stream:" + rsv.Month + ":z"
	if err := rdb.ZAdd(ctx, zkey, redis.Z{Score: 1, Member: rsv.ID}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := svc.Refresh(ctx, rsv); err != nil {
		t.Fatal(err)
	}
	score, err := rdb.ZScore(ctx, zkey, rsv.ID).Result()
	if err != nil {
		t.Fatal(err)
	}
	if score <= 1 {
		t.Fatalf("reservation score was not extended: %f", score)
	}

	if err := svc.Settle(ctx, rsv, 100); err != nil {
		t.Fatal(err)
	}
	if err := svc.Refresh(ctx, rsv); !errors.Is(err, budget.ErrReservationGone) {
		t.Fatalf("expected settled reservation to be gone, got %v", err)
	}
}

package breaker_test

import (
	"testing"
	"time"

	"github.com/raviikumar001/prism-gateway/internal/breaker"
)

func TestOpensOnFailureRate(t *testing.T) {
	b := breaker.New(breaker.Config{
		WindowSize:  10,
		FailureRate: 0.5,
		Cooldown:    time.Minute,
		MinRequests: 4,
	})
	for i := 0; i < 3; i++ {
		b.Failure()
	}
	if b.State() != breaker.Closed {
		t.Fatalf("expected closed before min requests, got %s", b.State())
	}
	b.Failure()
	if b.State() != breaker.Open {
		t.Fatalf("expected open, got %s", b.State())
	}
	if b.Allow() {
		t.Fatal("open breaker should deny")
	}
}

func TestHalfOpenProbe(t *testing.T) {
	b := breaker.New(breaker.Config{
		WindowSize:  10,
		FailureRate: 0.5,
		Cooldown:    10 * time.Millisecond,
		MinRequests: 2,
	})
	b.Failure()
	b.Failure()
	if b.State() != breaker.Open {
		t.Fatalf("expected open")
	}
	time.Sleep(20 * time.Millisecond)
	if !b.Allow() {
		t.Fatal("expected half-open probe allow")
	}
	if b.Allow() {
		t.Fatal("only one probe at a time")
	}
	b.Success()
	if b.State() != breaker.Closed {
		t.Fatalf("expected closed after probe success, got %s", b.State())
	}
}

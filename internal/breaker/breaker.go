package breaker

import (
	"sync"
	"time"
)

type State string

const (
	Closed   State = "closed"
	Open     State = "open"
	HalfOpen State = "half_open"
)

type Config struct {
	WindowSize   int
	FailureRate  float64
	Cooldown     time.Duration
	MinRequests  int
}

func DefaultConfig() Config {
	return Config{
		WindowSize:  20,
		FailureRate: 0.5,
		Cooldown:    30 * time.Second,
		MinRequests: 5,
	}
}

type Breaker struct {
	mu       sync.Mutex
	cfg      Config
	state    State
	openedAt time.Time
	results  []bool // true = success; ring buffer
	idx      int
	filled   int
	probing  bool
}

func New(cfg Config) *Breaker {
	if cfg.WindowSize <= 0 {
		cfg.WindowSize = 20
	}
	if cfg.FailureRate <= 0 {
		cfg.FailureRate = 0.5
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 30 * time.Second
	}
	if cfg.MinRequests <= 0 {
		cfg.MinRequests = 5
	}
	return &Breaker{
		cfg:     cfg,
		state:   Closed,
		results: make([]bool, cfg.WindowSize),
	}
}

func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.maybeHalfOpenLocked(time.Now())
	return b.state
}

// Allow returns false when the breaker is open (skip provider).
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.maybeHalfOpenLocked(time.Now())
	switch b.state {
	case Open:
		return false
	case HalfOpen:
		if b.probing {
			return false
		}
		b.probing = true
		return true
	default:
		return true
	}
}

func (b *Breaker) Success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.recordLocked(true)
	if b.state == HalfOpen {
		b.state = Closed
		b.probing = false
		b.resetWindowLocked()
	}
}

func (b *Breaker) Failure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.recordLocked(false)
	if b.state == HalfOpen {
		b.state = Open
		b.openedAt = time.Now()
		b.probing = false
		return
	}
	if b.shouldOpenLocked() {
		b.state = Open
		b.openedAt = time.Now()
	}
}

// OpenFor forces open for at least d (e.g. Retry-After).
func (b *Breaker) OpenFor(d time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state = Open
	b.probing = false
	until := time.Now().Add(d)
	if until.After(b.openedAt.Add(b.cfg.Cooldown)) {
		b.openedAt = until.Add(-b.cfg.Cooldown)
	} else {
		b.openedAt = time.Now()
	}
}

func (b *Breaker) Snapshot() map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.maybeHalfOpenLocked(time.Now())
	fails, total := b.windowStatsLocked()
	rate := 0.0
	if total > 0 {
		rate = float64(fails) / float64(total)
	}
	return map[string]any{
		"state":        string(b.state),
		"window_size":  b.cfg.WindowSize,
		"samples":      total,
		"failures":     fails,
		"failure_rate": rate,
		"cooldown_ms":  b.cfg.Cooldown.Milliseconds(),
	}
}

func (b *Breaker) maybeHalfOpenLocked(now time.Time) {
	if b.state == Open && now.Sub(b.openedAt) >= b.cfg.Cooldown {
		b.state = HalfOpen
		b.probing = false
	}
}

func (b *Breaker) recordLocked(ok bool) {
	b.results[b.idx] = ok
	b.idx = (b.idx + 1) % len(b.results)
	if b.filled < len(b.results) {
		b.filled++
	}
}

func (b *Breaker) resetWindowLocked() {
	b.idx = 0
	b.filled = 0
	for i := range b.results {
		b.results[i] = false
	}
}

func (b *Breaker) windowStatsLocked() (failures, total int) {
	total = b.filled
	for i := 0; i < b.filled; i++ {
		if !b.results[i] {
			failures++
		}
	}
	return failures, total
}

func (b *Breaker) shouldOpenLocked() bool {
	fails, total := b.windowStatsLocked()
	if total < b.cfg.MinRequests {
		return false
	}
	return float64(fails)/float64(total) >= b.cfg.FailureRate
}

package httpapi

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const logQueueSize = 1024

type RequestLogger struct {
	db   *pgxpool.Pool
	ch   chan logEntry
	wg   sync.WaitGroup
	once sync.Once
}

func NewRequestLogger(db *pgxpool.Pool) *RequestLogger {
	l := &RequestLogger{
		db: db,
		ch: make(chan logEntry, logQueueSize),
	}
	l.wg.Add(1)
	go l.loop()
	return l
}

func (l *RequestLogger) Enqueue(e logEntry) {
	select {
	case l.ch <- e:
	default:
		// Bounded: block briefly rather than drop under load.
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		select {
		case l.ch <- e:
		case <-ctx.Done():
			slog.Error("request log queue full, dropping", "request_id", e.RequestID)
		}
	}
}

func (l *RequestLogger) loop() {
	defer l.wg.Done()
	for e := range l.ch {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		insertRequestLog(ctx, l.db, e)
		cancel()
	}
}

func (l *RequestLogger) Close() {
	l.once.Do(func() {
		close(l.ch)
		l.wg.Wait()
	})
}

func insertRequestLog(ctx context.Context, db *pgxpool.Pool, e logEntry) {
	if e.Cache == "" {
		e.Cache = "miss"
	}
	_, err := db.Exec(ctx, `
		INSERT INTO request_logs (
			request_id, virtual_key, requested_model, resolved_provider, resolved_model,
			status, prompt_tokens, completion_tokens, cost_micro_cents, cache, fallback,
			route_reason, retries, latency_ms
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (request_id) DO NOTHING
	`, e.RequestID, e.VirtualKey, e.RequestedModel, nullStr(e.ResolvedProvider), nullStr(e.ResolvedModel),
		e.Status, e.PromptTokens, e.CompletionTokens, e.CostMicroCents, e.Cache, e.Fallback,
		nullStr(e.RouteReason), e.Retries, e.LatencyMs)
	if err != nil {
		slog.Error("request log insert failed", "err", err, "request_id", e.RequestID)
	}
}

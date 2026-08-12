package httpapi

import (
	"context"
	"log/slog"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const logQueueSize = 1024
const maxLogDetailRunes = 240

type logEntry struct {
	RequestID        string
	VirtualKey       string
	RequestedModel   string
	ResolvedProvider string
	ResolvedModel    string
	Status           string
	PromptTokens     int
	CompletionTokens int
	CostMicroCents   int64
	Cache            string
	Fallback         bool
	RouteReason      string
	Retries          int
	LatencyMs        int
	Stream           bool
	CostEstimated    bool
	Detail           string
}

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

func completeLog(start time.Time, e logEntry) logEntry {
	if e.RequestID == "" {
		e.RequestID = uuid.NewString()
	}
	if e.VirtualKey == "" {
		e.VirtualKey = unauthenticatedKey
	}
	if e.Cache == "" {
		e.Cache = "miss"
	}
	if e.LatencyMs <= 0 {
		e.LatencyMs = int(time.Since(start).Milliseconds())
	}
	e.Detail = truncateDetail(e.Detail)
	return e
}

func truncateDetail(s string) string {
	if s == "" {
		return s
	}
	if utf8.RuneCountInString(s) <= maxLogDetailRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxLogDetailRunes-1]) + "…"
}

func (s *Server) recordLog(start time.Time, e logEntry) {
	s.logger.Enqueue(completeLog(start, e))
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func insertRequestLog(ctx context.Context, db *pgxpool.Pool, e logEntry) {
	if e.Cache == "" {
		e.Cache = "miss"
	}
	_, err := db.Exec(ctx, `
		INSERT INTO request_logs (
			request_id, virtual_key, requested_model, resolved_provider, resolved_model,
			status, prompt_tokens, completion_tokens, cost_micro_cents, cache, fallback,
			route_reason, retries, latency_ms, stream, cost_estimated, detail
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		ON CONFLICT (request_id) DO NOTHING
	`, e.RequestID, e.VirtualKey, e.RequestedModel, nullStr(e.ResolvedProvider), nullStr(e.ResolvedModel),
		e.Status, e.PromptTokens, e.CompletionTokens, e.CostMicroCents, e.Cache, e.Fallback,
		nullStr(e.RouteReason), e.Retries, e.LatencyMs, e.Stream, e.CostEstimated, nullStr(e.Detail))
	if err != nil {
		slog.Error("request log insert failed", "err", err, "request_id", e.RequestID)
	}
}

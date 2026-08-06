package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"unicode"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
	"github.com/raviikumar001/prism-gateway/internal/embed"
)

type Stats struct {
	HitsExact    atomic.Int64
	HitsSemantic atomic.Int64
	Misses       atomic.Int64
	Stores       atomic.Int64
}

type Hit struct {
	Body       json.RawMessage
	Kind       string  // exact | semantic
	Similarity float64
}

type Service struct {
	db       *pgxpool.Pool
	embedder *embed.HashEmbedder
	stats    Stats
}

func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db, embedder: embed.NewHashEmbedder()}
}

func (s *Service) Snapshot() map[string]any {
	hits := s.stats.HitsExact.Load() + s.stats.HitsSemantic.Load()
	miss := s.stats.Misses.Load()
	total := hits + miss
	rate := 0.0
	if total > 0 {
		rate = float64(hits) / float64(total)
	}
	return map[string]any{
		"hits_exact":    s.stats.HitsExact.Load(),
		"hits_semantic": s.stats.HitsSemantic.Load(),
		"misses":        miss,
		"stores":        s.stats.Stores.Load(),
		"hit_rate":      rate,
	}
}

// Normalize uses the last user message (documented policy for single-turn cache keys).
func Normalize(messages []string) string {
	if len(messages) == 0 {
		return ""
	}
	text := messages[len(messages)-1]
	text = strings.TrimSpace(strings.ToLower(text))
	var b strings.Builder
	prevSpace := false
	for _, r := range text {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

func PromptHash(virtualKey, normalized string) string {
	sum := sha256.Sum256([]byte(virtualKey + "\n" + normalized))
	return hex.EncodeToString(sum[:])
}

func (s *Service) Lookup(ctx context.Context, virtualKey, normalized string, threshold float64) (*Hit, error) {
	if normalized == "" {
		s.stats.Misses.Add(1)
		return nil, nil
	}
	hash := PromptHash(virtualKey, normalized)

	var body []byte
	err := s.db.QueryRow(ctx, `
		SELECT response_body FROM cache_entries
		WHERE virtual_key = $1 AND prompt_hash = $2
	`, virtualKey, hash).Scan(&body)
	if err == nil {
		_, _ = s.db.Exec(ctx, `UPDATE cache_entries SET hit_count = hit_count + 1 WHERE virtual_key = $1 AND prompt_hash = $2`, virtualKey, hash)
		s.stats.HitsExact.Add(1)
		return &Hit{Body: body, Kind: "exact", Similarity: 1}, nil
	}

	if threshold <= 0 {
		threshold = 0.8
	}
	vec := s.embedder.Embed(normalized)
	_, _ = s.db.Exec(ctx, `SELECT set_config('hnsw.iterative_scan', 'relaxed_order', true)`)

	var sim float64
	err = s.db.QueryRow(ctx, `
		SELECT response_body, 1 - (embedding <=> $1) AS similarity
		FROM cache_entries
		WHERE virtual_key = $2
		  AND embedding IS NOT NULL
		  AND 1 - (embedding <=> $1) >= $3
		ORDER BY embedding <=> $1
		LIMIT 1
	`, pgvector.NewVector(vec), virtualKey, threshold).Scan(&body, &sim)
	if err != nil {
		s.stats.Misses.Add(1)
		return nil, nil // miss (including no rows); fail open
	}
	s.stats.HitsSemantic.Add(1)
	slog.Info("semantic cache hit", "virtual_key", virtualKey, "similarity", sim)
	return &Hit{Body: body, Kind: "semantic", Similarity: sim}, nil
}

func (s *Service) Store(ctx context.Context, virtualKey, normalized string, response json.RawMessage) error {
	if normalized == "" || len(response) == 0 {
		return nil
	}
	hash := PromptHash(virtualKey, normalized)
	vec := s.embedder.Embed(normalized)
	_, err := s.db.Exec(ctx, `
		INSERT INTO cache_entries (virtual_key, prompt_hash, prompt_text, embedding, response_body)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (virtual_key, prompt_hash) DO UPDATE SET
			response_body = EXCLUDED.response_body,
			embedding = EXCLUDED.embedding,
			prompt_text = EXCLUDED.prompt_text
	`, virtualKey, hash, normalized, pgvector.NewVector(vec), response)
	if err != nil {
		return fmt.Errorf("cache store: %w", err)
	}
	s.stats.Stores.Add(1)
	return nil
}

// LastUserContents returns message contents with preference for last user role index.
func LastUserContents(roles, contents []string) string {
	for i := len(roles) - 1; i >= 0; i-- {
		if strings.EqualFold(roles[i], "user") {
			return contents[i]
		}
	}
	if len(contents) == 0 {
		return ""
	}
	return contents[len(contents)-1]
}

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

	"github.com/jackc/pgx/v5"
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
	Kind       string // exact | semantic
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

// Key keeps routing metadata separate from normalized prompt text. The
// delimiter survives Normalize-style whitespace handling and lets semantic
// lookup scope candidates to the same requested model.
func Key(scope, variant, prompt string) string {
	scope = strings.TrimSpace(scope)
	variant = strings.TrimSpace(variant)
	prompt = Normalize([]string{prompt})
	return scope + " :: " + variant + " :: " + prompt
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
	scope, prompt := cacheScopeAndPrompt(normalized)
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
	if err != pgx.ErrNoRows {
		return nil, fmt.Errorf("cache exact lookup: %w", err)
	}

	if threshold <= 0 {
		threshold = 0.8
	}
	vec := s.embedder.Embed(normalized)

	rows, err := s.db.Query(ctx, `
		SELECT id, response_body, prompt_text, 1 - (embedding <=> $1) AS similarity
		FROM cache_entries
		WHERE virtual_key = $2
		  AND ($4 = '' OR split_part(prompt_text, ' :: ', 1) = $4)
		  AND embedding IS NOT NULL
		  AND 1 - (embedding <=> $1) >= $3
		ORDER BY embedding <=> $1
		LIMIT 8
	`, pgvector.NewVector(vec), virtualKey, threshold, scope)
	if err != nil {
		return nil, fmt.Errorf("cache semantic lookup: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var candidateText string
		var sim float64
		if err := rows.Scan(&id, &body, &candidateText, &sim); err != nil {
			return nil, fmt.Errorf("cache semantic scan: %w", err)
		}
		_, _, candidatePrompt := splitCacheKey(candidateText)
		if !semanticRelated(prompt, candidatePrompt) {
			continue
		}
		_, _ = s.db.Exec(ctx, `UPDATE cache_entries SET hit_count = hit_count + 1 WHERE id = $1`, id)
		s.stats.HitsSemantic.Add(1)
		slog.Info("semantic cache hit", "virtual_key", virtualKey, "similarity", sim)
		return &Hit{Body: body, Kind: "semantic", Similarity: sim}, nil
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cache semantic rows: %w", err)
	}
	s.stats.Misses.Add(1)
	return nil, nil
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

func cacheScopeAndPrompt(normalized string) (string, string) {
	scope, _, prompt := splitCacheKey(normalized)
	return scope, prompt
}

func splitCacheKey(normalized string) (string, string, string) {
	parts := strings.SplitN(normalized, " :: ", 3)
	if len(parts) != 3 {
		return "", "", normalized
	}
	return parts[0], parts[1], parts[2]
}

func semanticRelated(query, candidate string) bool {
	queryVolatile := volatileWords(query)
	candidateVolatile := volatileWords(candidate)
	if len(queryVolatile) != len(candidateVolatile) {
		return false
	}
	for word := range queryVolatile {
		if _, ok := candidateVolatile[word]; !ok {
			return false
		}
	}

	queryWords := semanticWords(query)
	candidateWords := semanticWords(candidate)
	if len(queryWords) == 0 || len(candidateWords) == 0 {
		return false
	}
	shared := 0
	for word := range queryWords {
		if _, ok := candidateWords[word]; ok {
			shared++
		}
	}
	if shared >= 2 {
		return true
	}
	return shared == 1 && len(queryWords) <= 2 && len(candidateWords) <= 2
}

func semanticWords(text string) map[string]struct{} {
	var words []string
	var current strings.Builder
	flush := func() {
		if current.Len() == 0 {
			return
		}
		word := strings.ToLower(current.String())
		current.Reset()
		if isCacheStopWord(word) || isVolatileWord(word) {
			return
		}
		words = append(words, word)
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	out := make(map[string]struct{}, len(words))
	for _, word := range words {
		out[word] = struct{}{}
	}
	return out
}

func isCacheStopWord(word string) bool {
	switch word {
	case "a", "an", "the", "is", "are", "am", "to", "of", "and", "or",
		"on", "in", "for", "my", "i", "do", "how", "what", "steps",
		"with", "that", "this", "it", "me", "please", "can", "you",
		"if", "we", "our", "your", "from", "about":
		return true
	default:
		return false
	}
}

func isVolatileWord(word string) bool {
	if len(word) < 8 {
		return false
	}
	for _, r := range word {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

func volatileWords(text string) map[string]struct{} {
	out := make(map[string]struct{})
	var current strings.Builder
	flush := func() {
		if current.Len() == 0 {
			return
		}
		word := strings.ToLower(current.String())
		current.Reset()
		if isVolatileWord(word) {
			out[word] = struct{}{}
		}
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

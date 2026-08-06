package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/raviikumar001/prism-gateway/internal/meter"
)

func (s *Server) mountAdmin(r chi.Router) {
	r.Route("/admin", func(ar chi.Router) {
		ar.Use(s.adminAuth)
		ar.Get("/usage", s.handleAdminUsage)
		ar.Get("/logs", s.handleAdminLogs)
		ar.Get("/providers/health", s.handleAdminProvidersHealth)
		ar.Get("/cache/stats", s.handleAdminCacheStats)
	})
}

func (s *Server) adminAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("Authorization")
		if token == "Bearer "+s.cfg.AdminToken || r.Header.Get("X-Admin-Token") == s.cfg.AdminToken {
			next.ServeHTTP(w, r)
			return
		}
		writeAPIError(w, http.StatusUnauthorized, "authentication_error", "Invalid admin token")
	})
}

func (s *Server) handleAdminUsage(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "key is required")
		return
	}
	month := r.URL.Query().Get("month")
	if month == "" {
		month = time.Now().UTC().Format("200601")
	}

	var requests, prompt, completion, cost, hits int64
	err := s.db.QueryRow(r.Context(), `
		SELECT requests, prompt_tokens, completion_tokens, cost_micro_cents, cache_hits
		FROM usage_monthly WHERE virtual_key = $1 AND month = $2
	`, key, month).Scan(&requests, &prompt, &completion, &cost, &hits)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, map[string]any{
			"virtual_key":       key,
			"month":             month,
			"requests":          0,
			"prompt_tokens":     0,
			"completion_tokens": 0,
			"cost_usd":          "0.0",
			"cache_hits":        0,
		})
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "server_error", "Could not load usage")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"virtual_key":       key,
		"month":             month,
		"requests":          requests,
		"prompt_tokens":     prompt,
		"completion_tokens": completion,
		"cost_micro_cents":  cost,
		"cost_usd":          trimCost(meter.FormatUSD(float64(cost) / 100_000_000.0)),
		"cache_hits":        hits,
	})
}

func (s *Server) handleAdminLogs(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	q := `
		SELECT request_id, virtual_key, requested_model, COALESCE(resolved_provider,''), COALESCE(resolved_model,''),
		       status, prompt_tokens, completion_tokens, cost_micro_cents, cache, fallback, latency_ms, created_at
		FROM request_logs`
	args := []any{}
	if key != "" {
		q += ` WHERE virtual_key = $1 ORDER BY created_at DESC LIMIT $2`
		args = append(args, key, limit)
	} else {
		q += ` ORDER BY created_at DESC LIMIT $1`
		args = append(args, limit)
	}

	rows, err := s.db.Query(r.Context(), q, args...)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "server_error", "Could not load logs")
		return
	}
	defer rows.Close()

	type row struct {
		RequestID        string    `json:"request_id"`
		VirtualKey       string    `json:"virtual_key"`
		RequestedModel   string    `json:"requested_model"`
		ResolvedProvider string    `json:"resolved_provider"`
		ResolvedModel    string    `json:"resolved_model"`
		Status           string    `json:"status"`
		PromptTokens     int       `json:"prompt_tokens"`
		CompletionTokens int       `json:"completion_tokens"`
		CostMicroCents   int64     `json:"cost_micro_cents"`
		Cache            string    `json:"cache"`
		Fallback         bool      `json:"fallback"`
		LatencyMs        int       `json:"latency_ms"`
		CreatedAt        time.Time `json:"created_at"`
	}
	out := make([]row, 0)
	for rows.Next() {
		var item row
		if err := rows.Scan(
			&item.RequestID, &item.VirtualKey, &item.RequestedModel, &item.ResolvedProvider, &item.ResolvedModel,
			&item.Status, &item.PromptTokens, &item.CompletionTokens, &item.CostMicroCents, &item.Cache, &item.Fallback, &item.LatencyMs, &item.CreatedAt,
		); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "server_error", "Could not scan logs")
			return
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "server_error", "Could not load logs")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": out})
}

func (s *Server) handleAdminProvidersHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"providers": s.exec.HealthSnapshot(),
	})
}

func (s *Server) handleAdminCacheStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cache.Snapshot())
}

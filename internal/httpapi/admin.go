package httpapi

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
		if adminAuthorized(s.cfg.AdminToken, r.Header.Get("Authorization"), r.Header.Get("X-Admin-Token")) {
			next.ServeHTTP(w, r)
			return
		}
		writeAPIError(w, http.StatusUnauthorized, "authentication_error", "Invalid admin token")
	})
}

func adminAuthorized(want, authorization, xAdmin string) bool {
	if want == "" {
		return false
	}
	got := ""
	const prefix = "Bearer "
	if strings.HasPrefix(authorization, prefix) {
		got = authorization[len(prefix):]
	}
	bearerOK := subtle.ConstantTimeCompare([]byte(got), []byte(want))
	headerOK := subtle.ConstantTimeCompare([]byte(xAdmin), []byte(want))
	return (bearerOK | headerOK) == 1
}

func (s *Server) handleAdminUsage(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "key is required")
		return
	}

	window, err := parseUsageWindow(r.URL.Query(), time.Now().UTC())
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	var requests, prompt, completion, cost, hits int64
	var errScan error
	if window.Source == "request_logs" {
		errScan = s.db.QueryRow(r.Context(), `
			SELECT COALESCE(COUNT(*), 0),
			       COALESCE(SUM(prompt_tokens), 0),
			       COALESCE(SUM(completion_tokens), 0),
			       COALESCE(SUM(cost_micro_cents), 0),
			       COALESCE(SUM(CASE WHEN cache = 'hit' THEN 1 ELSE 0 END), 0)
			FROM request_logs
			WHERE virtual_key = $1
			  AND created_at >= $2
			  AND created_at <= $3
			  AND status = 'ok'
		`, key, window.From, window.To).Scan(&requests, &prompt, &completion, &cost, &hits)
	} else {
		errScan = s.db.QueryRow(r.Context(), `
			SELECT COALESCE(SUM(requests), 0),
			       COALESCE(SUM(prompt_tokens), 0),
			       COALESCE(SUM(completion_tokens), 0),
			       COALESCE(SUM(cost_micro_cents), 0),
			       COALESCE(SUM(cache_hits), 0)
			FROM usage_monthly
			WHERE virtual_key = $1 AND month = ANY($2)
		`, key, window.Months).Scan(&requests, &prompt, &completion, &cost, &hits)
	}
	if errScan != nil && !errors.Is(errScan, pgx.ErrNoRows) {
		writeAPIError(w, http.StatusInternalServerError, "server_error", "Could not load usage")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"key":               key,
		"virtual_key":       key,
		"from":              window.From.Format("2006-01-02"),
		"to":                window.To.Format("2006-01-02"),
		"month":             window.PrimaryMonth,
		"months":            window.Months,
		"granularity":       window.Granularity,
		"source":            window.Source,
		"requests":          requests,
		"prompt_tokens":     prompt,
		"completion_tokens": completion,
		"cost_micro_cents":  cost,
		"cost_usd":          trimCost(meter.FormatUSD(float64(cost) / 100_000_000.0)),
		"cache_hits":        hits,
	})
}

type usageWindow struct {
	From         time.Time
	To           time.Time
	Months       []string
	PrimaryMonth string
	Source       string
	Granularity  string
}

func parseUsageWindow(q url.Values, now time.Time) (usageWindow, error) {
	now = now.UTC()
	month := strings.TrimSpace(q.Get("month"))
	fromRaw := strings.TrimSpace(q.Get("from"))
	toRaw := strings.TrimSpace(q.Get("to"))

	if month != "" && (fromRaw != "" || toRaw != "") {
		return usageWindow{}, fmt.Errorf("use either month or from/to, not both")
	}

	if month != "" {
		start, err := parseMonthStart(month)
		if err != nil {
			return usageWindow{}, err
		}
		end := start.AddDate(0, 1, 0).Add(-time.Nanosecond)
		m := start.Format("200601")
		return usageWindow{
			From: start, To: end, Months: []string{m}, PrimaryMonth: m,
			Source: "usage_monthly", Granularity: "month",
		}, nil
	}

	if fromRaw == "" && toRaw == "" {
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 1, 0).Add(-time.Nanosecond)
		m := start.Format("200601")
		return usageWindow{
			From: start, To: end, Months: []string{m}, PrimaryMonth: m,
			Source: "usage_monthly", Granularity: "month",
		}, nil
	}

	if fromRaw == "" || toRaw == "" {
		return usageWindow{}, fmt.Errorf("from and to are both required")
	}

	from, err := parseUsageDate(fromRaw, false)
	if err != nil {
		return usageWindow{}, fmt.Errorf("invalid from: %w", err)
	}
	to, err := parseUsageDate(toRaw, true)
	if err != nil {
		return usageWindow{}, fmt.Errorf("invalid to: %w", err)
	}
	if to.Before(from) {
		return usageWindow{}, fmt.Errorf("to must be on or after from")
	}

	months := monthsInclusive(from, to)
	return usageWindow{
		From:         from,
		To:           to,
		Months:       months,
		PrimaryMonth: months[0],
		Source:       "request_logs",
		Granularity:  "day",
	}, nil
}

func parseMonthStart(month string) (time.Time, error) {
	if len(month) != 6 {
		return time.Time{}, fmt.Errorf("month must be YYYYMM")
	}
	t, err := time.ParseInLocation("200601", month, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("month must be YYYYMM")
	}
	return t, nil
}

func parseUsageDate(raw string, endOfDay bool) (time.Time, error) {
	if len(raw) == 6 && raw[0] >= '0' && raw[0] <= '9' {
		start, err := parseMonthStart(raw)
		if err != nil {
			return time.Time{}, err
		}
		if endOfDay {
			return start.AddDate(0, 1, 0).Add(-time.Nanosecond), nil
		}
		return start, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", raw, time.UTC); err == nil {
		if endOfDay {
			return t.Add(24*time.Hour - time.Nanosecond), nil
		}
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("expected YYYY-MM-DD, YYYYMM, or RFC3339")
}

func monthsInclusive(from, to time.Time) []string {
	cur := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(to.Year(), to.Month(), 1, 0, 0, 0, 0, time.UTC)
	out := make([]string, 0, 4)
	for !cur.After(end) {
		out = append(out, cur.Format("200601"))
		cur = cur.AddDate(0, 1, 0)
	}
	return out
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
		       status, prompt_tokens, completion_tokens, cost_micro_cents, cache, fallback,
		       COALESCE(route_reason,''), retries, latency_ms, created_at,
		       COALESCE(stream, false), COALESCE(cost_estimated, false), COALESCE(detail,'')
		FROM request_logs`
	args := []any{}
	where := []string{}
	if key != "" {
		where = append(where, fmt.Sprintf("virtual_key = $%d", len(args)+1))
		args = append(args, key)
	}
	if status := strings.TrimSpace(r.URL.Query().Get("status")); status != "" && status != "all" {
		switch status {
		case "ok":
			where = append(where, "status = 'ok'")
		case "cache":
			where = append(where, "cache = 'hit'")
		case "rejected":
			where = append(where, "status LIKE 'rejected_%'")
		case "error":
			where = append(where, "status NOT IN ('ok') AND status NOT LIKE 'rejected_%'")
		case "stream":
			where = append(where, "stream = true")
		default:
			where = append(where, fmt.Sprintf("status = $%d", len(args)+1))
			args = append(args, status)
		}
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", len(args)+1)
	args = append(args, limit)

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
		StatusLabel      string    `json:"status_label"`
		PromptTokens     int       `json:"prompt_tokens"`
		CompletionTokens int       `json:"completion_tokens"`
		CostMicroCents   int64     `json:"cost_micro_cents"`
		CostUSD          string    `json:"cost_usd"`
		Cache            string    `json:"cache"`
		Fallback         bool      `json:"fallback"`
		RouteReason      string    `json:"route_reason"`
		Retries          int       `json:"retries"`
		LatencyMs        int       `json:"latency_ms"`
		CreatedAt        time.Time `json:"created_at"`
		Stream           bool      `json:"stream"`
		CostEstimated    bool      `json:"cost_estimated"`
		Detail           string    `json:"detail"`
	}
	out := make([]row, 0)
	for rows.Next() {
		var item row
		if err := rows.Scan(
			&item.RequestID, &item.VirtualKey, &item.RequestedModel, &item.ResolvedProvider, &item.ResolvedModel,
			&item.Status, &item.PromptTokens, &item.CompletionTokens, &item.CostMicroCents, &item.Cache, &item.Fallback,
			&item.RouteReason, &item.Retries, &item.LatencyMs, &item.CreatedAt,
			&item.Stream, &item.CostEstimated, &item.Detail,
		); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "server_error", "Could not scan logs")
			return
		}
		item.StatusLabel = statusLabel(item.Status)
		item.CostUSD = trimCost(meter.FormatUSD(float64(item.CostMicroCents) / 100_000_000.0))
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

func statusLabel(status string) string {
	switch status {
	case "ok":
		return "OK"
	case "rejected_auth":
		return "Auth rejected"
	case "rejected_malformed":
		return "Bad request"
	case "rejected_allowlist":
		return "Model blocked"
	case "rejected_rpm":
		return "RPM limited"
	case "rejected_tpm":
		return "TPM limited"
	case "rejected_budget":
		return "Budget exceeded"
	case "model_not_found":
		return "Unknown model"
	case "pricing_unavailable":
		return "Pricing missing"
	case "budget_unavailable", "rate_limiter_unavailable", "token_rate_limiter_unavailable", "auth_lookup_failed":
		return "Internal"
	case "client_abort":
		return "Client abort"
	case "gateway_overloaded":
		return "Overloaded"
	case "upstream_client_error":
		return "Upstream 4xx"
	case "upstream_error":
		return "Upstream failed"
	case "streaming_unsupported":
		return "Stream unsupported"
	default:
		if status == "" {
			return "Unknown"
		}
		return status
	}
}

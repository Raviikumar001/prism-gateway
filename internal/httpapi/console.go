package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/raviikumar001/prism-gateway/internal/meter"
)

type consoleLogRow struct {
	RequestID        string    `json:"request_id"`
	Team             string    `json:"team"`
	RequestedModel   string    `json:"requested_model"`
	ResolvedProvider string    `json:"resolved_provider"`
	ResolvedModel    string    `json:"resolved_model"`
	Status           string    `json:"status"`
	StatusLabel      string    `json:"status_label"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
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

type consoleAttention struct {
	Tone  string `json:"tone"`
	Title string `json:"title"`
	Body  string `json:"body"`
	Href  string `json:"href,omitempty"`
}

type hourPoint struct {
	Hour time.Time
	Cost float64
}

func (s *Server) handleConsoleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now().UTC()
	month := now.Format("200601")
	since24h := now.Add(-24 * time.Hour)
	since48h := now.Add(-48 * time.Hour)
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))

	health := map[string]string{}
	healthOK := true
	if err := s.db.Ping(ctx); err != nil {
		health["postgres"] = "down"
		healthOK = false
	} else {
		health["postgres"] = "up"
	}
	if err := s.rdb.Ping(ctx).Err(); err != nil {
		health["redis"] = "down"
		healthOK = false
	} else {
		health["redis"] = "up"
	}

	providers := s.exec.HealthSnapshot()
	healthy, open, half := 0, 0, 0
	for _, p := range providers {
		switch strings.ToLower(fmtString(p["state"])) {
		case "open":
			open++
		case "half_open":
			half++
		default:
			healthy++
		}
	}

	cacheStats := s.cache.Snapshot()

	var monthRequests, monthPrompt, monthCompletion, monthCost, monthHits int64
	_ = s.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(requests), 0),
		       COALESCE(SUM(prompt_tokens), 0),
		       COALESCE(SUM(completion_tokens), 0),
		       COALESCE(SUM(cost_micro_cents), 0),
		       COALESCE(SUM(cache_hits), 0)
		FROM usage_monthly
		WHERE month = $1
	`, month).Scan(&monthRequests, &monthPrompt, &monthCompletion, &monthCost, &monthHits)

	var (
		ok24h, total24h, err24h, reject24h, cache24h int64
		cost24h, costPrior                           int64
		okPrior, totalPrior                          int64
		latOK, latErr                                float64
		costOK, costErr, costReject                  int64
	)
	_ = s.db.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE created_at >= $1 AND status = 'ok'),
			COUNT(*) FILTER (WHERE created_at >= $1),
			COUNT(*) FILTER (WHERE created_at >= $1 AND status NOT IN ('ok') AND status NOT LIKE 'rejected_%'),
			COUNT(*) FILTER (WHERE created_at >= $1 AND status LIKE 'rejected_%'),
			COUNT(*) FILTER (WHERE created_at >= $1 AND cache = 'hit'),
			COALESCE(SUM(cost_micro_cents) FILTER (WHERE created_at >= $1), 0),
			COALESCE(SUM(cost_micro_cents) FILTER (WHERE created_at >= $2 AND created_at < $1), 0),
			COUNT(*) FILTER (WHERE created_at >= $2 AND created_at < $1 AND status = 'ok'),
			COUNT(*) FILTER (WHERE created_at >= $2 AND created_at < $1),
			COALESCE(AVG(latency_ms) FILTER (WHERE created_at >= $1 AND status = 'ok'), 0),
			COALESCE(AVG(latency_ms) FILTER (WHERE created_at >= $1 AND status NOT IN ('ok') AND status NOT LIKE 'rejected_%'), 0),
			COALESCE(SUM(cost_micro_cents) FILTER (WHERE created_at >= $1 AND status = 'ok'), 0),
			COALESCE(SUM(cost_micro_cents) FILTER (WHERE created_at >= $1 AND status NOT IN ('ok') AND status NOT LIKE 'rejected_%'), 0),
			COALESCE(SUM(cost_micro_cents) FILTER (WHERE created_at >= $1 AND status LIKE 'rejected_%'), 0)
		FROM request_logs
		WHERE created_at >= $2
	`, since24h, since48h).Scan(
		&ok24h, &total24h, &err24h, &reject24h, &cache24h,
		&cost24h, &costPrior, &okPrior, &totalPrior,
		&latOK, &latErr, &costOK, &costErr, &costReject,
	)

	successRate := 0.0
	if total24h > 0 {
		successRate = float64(ok24h) / float64(total24h)
	}
	priorRate := 0.0
	if totalPrior > 0 {
		priorRate = float64(okPrior) / float64(totalPrior)
	}

	tenants := s.consoleTenants(ctx, month)
	logs := s.consoleLogs(ctx, statusFilter, 50)
	series := fillHourlySeries(since24h, now, s.consoleSpendSeries(ctx, since24h))
	attention := consoleAttentionItems(healthOK, open, half, err24h, reject24h, tenants)

	healthStatus := "degraded"
	if healthOK {
		healthStatus = "ok"
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"generated_at": now,
		"health": map[string]any{
			"status":        healthStatus,
			"checks":        health,
			"provider_mode": s.cfg.ProviderMode,
		},
		"summary": map[string]any{
			"providers_healthy":  healthy,
			"providers_open":     open,
			"providers_half":     half,
			"providers_total":    len(providers),
			"month_requests":     monthRequests,
			"month_prompt":       monthPrompt,
			"month_completion":   monthCompletion,
			"month_cache_hits":   monthHits,
			"month_spend_usd":    trimCost(meter.FormatUSD(float64(monthCost) / 100_000_000.0)),
			"ok_24h":             ok24h,
			"runs_24h":           total24h,
			"errors_24h":         err24h,
			"rejected_24h":       reject24h,
			"cache_hits_24h":     cache24h,
			"spend_24h_usd":      trimCost(meter.FormatUSD(float64(cost24h) / 100_000_000.0)),
			"spend_prior_usd":    trimCost(meter.FormatUSD(float64(costPrior) / 100_000_000.0)),
			"success_rate_24h":   successRate,
			"success_rate_prior": priorRate,
			"cache_hit_rate":     cacheStats["hit_rate"],
			"open_incidents":     open + boolToInt(!healthOK) + boolToInt(err24h > 0),
		},
		"run_mix": []map[string]any{
			{
				"id": "healthy", "label": "Running healthy", "tone": "ok",
				"providers": healthy, "runs": ok24h, "latency_ms": latOK,
				"cost_usd": trimCost(meter.FormatUSD(float64(costOK) / 100_000_000.0)),
			},
			{
				"id": "failing", "label": "Failing", "tone": "bad",
				"providers": open, "runs": err24h, "latency_ms": latErr,
				"cost_usd": trimCost(meter.FormatUSD(float64(costErr) / 100_000_000.0)),
			},
			{
				"id": "probing", "label": "Stuck / probing", "tone": "warn",
				"providers": half, "runs": 0, "latency_ms": 0.0,
				"cost_usd": "0",
			},
			{
				"id": "rejected", "label": "Rejected", "tone": "muted",
				"providers": 0, "runs": reject24h, "latency_ms": 0.0,
				"cost_usd": trimCost(meter.FormatUSD(float64(costReject) / 100_000_000.0)),
			},
		},
		"providers": providers,
		"tenants":   tenants,
		"attention": attention,
		"cache":     cacheStats,
		"logs":      logs,
		"spend_24h": series,
	})
}

func fmtString(v any) string {
	s, _ := v.(string)
	return s
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (s *Server) consoleTenants(ctx context.Context, month string) []map[string]any {
	rows, err := s.db.Query(ctx, `
		SELECT t.team, t.monthly_budget_usd, t.status,
		       COALESCE(u.requests, 0), COALESCE(u.cost_micro_cents, 0)
		FROM tenants t
		LEFT JOIN usage_monthly u
		  ON u.virtual_key = t.virtual_key AND u.month = $1
		ORDER BY t.team
	`, month)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var team, status string
		var budget float64
		var requests, cost int64
		if err := rows.Scan(&team, &budget, &status, &requests, &cost); err != nil {
			continue
		}
		spend := float64(cost) / 100_000_000.0
		used := 0.0
		if budget > 0 {
			used = spend / budget
		}
		out = append(out, map[string]any{
			"team":             team,
			"status":           status,
			"budget_usd":       budget,
			"requests":         requests,
			"spend_usd":        trimCost(meter.FormatUSD(spend)),
			"budget_used_frac": used,
		})
	}
	return out
}

func (s *Server) consoleSpendSeries(ctx context.Context, since time.Time) []hourPoint {
	rows, err := s.db.Query(ctx, `
		SELECT date_trunc('hour', created_at) AS hour,
		       COALESCE(SUM(cost_micro_cents), 0)
		FROM request_logs
		WHERE created_at >= $1
		GROUP BY 1
		ORDER BY 1
	`, since)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make([]hourPoint, 0)
	for rows.Next() {
		var hour time.Time
		var cost int64
		if err := rows.Scan(&hour, &cost); err != nil {
			continue
		}
		out = append(out, hourPoint{
			Hour: hour.UTC(),
			Cost: float64(cost) / 100_000_000.0,
		})
	}
	return out
}

func fillHourlySeries(since, now time.Time, points []hourPoint) []map[string]any {
	byUnix := make(map[int64]float64, len(points))
	for _, p := range points {
		byUnix[p.Hour.UTC().Truncate(time.Hour).Unix()] = p.Cost
	}
	start := since.UTC().Truncate(time.Hour)
	end := now.UTC().Truncate(time.Hour)
	out := make([]map[string]any, 0, 25)
	for t := start; !t.After(end); t = t.Add(time.Hour) {
		out = append(out, map[string]any{
			"hour":     t,
			"cost_usd": byUnix[t.Unix()],
		})
	}
	return out
}

func (s *Server) consoleLogs(ctx context.Context, status string, limit int) []consoleLogRow {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `
		SELECT l.request_id, COALESCE(t.team, 'unknown'), l.requested_model,
		       COALESCE(l.resolved_provider,''), COALESCE(l.resolved_model,''),
		       l.status, l.prompt_tokens, l.completion_tokens, l.cost_micro_cents, l.cache, l.fallback,
		       COALESCE(l.route_reason,''), l.retries, l.latency_ms, l.created_at,
		       COALESCE(l.stream, false), COALESCE(l.cost_estimated, false), COALESCE(l.detail,'')
		FROM request_logs l
		LEFT JOIN tenants t ON t.virtual_key = l.virtual_key`
	args := []any{}
	where := []string{}
	if status != "" && status != "all" {
		switch status {
		case "ok":
			where = append(where, "l.status = 'ok'")
		case "cache":
			where = append(where, "l.cache = 'hit'")
		case "rejected":
			where = append(where, "l.status LIKE 'rejected_%'")
		case "error":
			where = append(where, "l.status NOT IN ('ok') AND l.status NOT LIKE 'rejected_%'")
		case "stream":
			where = append(where, "l.stream = true")
		}
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY l.created_at DESC LIMIT $1"
	args = append(args, limit)

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return []consoleLogRow{}
	}
	defer rows.Close()
	out := make([]consoleLogRow, 0)
	for rows.Next() {
		var item consoleLogRow
		var cost int64
		if err := rows.Scan(
			&item.RequestID, &item.Team, &item.RequestedModel, &item.ResolvedProvider, &item.ResolvedModel,
			&item.Status, &item.PromptTokens, &item.CompletionTokens, &cost, &item.Cache, &item.Fallback,
			&item.RouteReason, &item.Retries, &item.LatencyMs, &item.CreatedAt,
			&item.Stream, &item.CostEstimated, &item.Detail,
		); err != nil {
			continue
		}
		item.StatusLabel = statusLabel(item.Status)
		item.CostUSD = trimCost(meter.FormatUSD(float64(cost) / 100_000_000.0))
		out = append(out, item)
	}
	return out
}

func consoleAttentionItems(healthOK bool, open, half int, errors24h, rejected24h int64, tenants []map[string]any) []consoleAttention {
	out := make([]consoleAttention, 0)
	if !healthOK {
		out = append(out, consoleAttention{
			Tone: "danger", Title: "Platform degraded",
			Body: "Postgres or Redis is down. New requests will fail until both checks are up.",
			Href: "#providers",
		})
	}
	if open > 0 {
		out = append(out, consoleAttention{
			Tone: "danger", Title: "Breaker open",
			Body: "An upstream is skipped until cooldown. Traffic is failing over or stalling.",
			Href: "#providers",
		})
	}
	if half > 0 {
		out = append(out, consoleAttention{
			Tone: "warning", Title: "Breaker probing",
			Body: "A provider is half-open and sending a probe before taking full traffic again.",
			Href: "#providers",
		})
	}
	if errors24h > 0 {
		out = append(out, consoleAttention{
			Tone: "danger", Title: "Upstream errors",
			Body: "There were upstream or gateway errors in the last 24 hours.",
			Href: "#logs",
		})
	}
	if rejected24h > 0 {
		out = append(out, consoleAttention{
			Tone: "warning", Title: "Admits rejected",
			Body: "RPM, TPM, budget, or allowlist rejected some requests in the last 24 hours.",
			Href: "#logs",
		})
	}
	for _, tenant := range tenants {
		used, _ := tenant["budget_used_frac"].(float64)
		team, _ := tenant["team"].(string)
		if used >= 0.8 {
			out = append(out, consoleAttention{
				Tone:  "warning",
				Title: team + " nearing budget",
				Body:  "This tenant has used most of its monthly budget.",
				Href:  "#tenants",
			})
		}
	}
	if len(out) == 0 {
		out = append(out, consoleAttention{
			Tone: "success", Title: "All clear",
			Body: "Providers are closed, dependencies are up, and nothing is over budget.",
		})
	}
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

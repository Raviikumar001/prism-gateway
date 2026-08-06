package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/raviikumar001/prism-gateway/internal/auth"
	"github.com/raviikumar001/prism-gateway/internal/meter"
	"github.com/raviikumar001/prism-gateway/internal/provider"
)

type chatRequestBody struct {
	Model    string                 `json:"model"`
	Messages []provider.ChatMessage `json:"messages"`
	Stream   bool                   `json:"stream"`
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx := r.Context()

	token, err := auth.BearerToken(r.Header.Get("Authorization"))
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "authentication_error", "Missing or invalid Authorization bearer token")
		return
	}

	tenant, err := s.auth.Lookup(ctx, token)
	if errors.Is(err, auth.ErrInvalidKey) || errors.Is(err, auth.ErrDisabled) {
		writeAPIError(w, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	if err != nil {
		slog.Error("tenant lookup failed", "err", err)
		writeAPIError(w, http.StatusInternalServerError, "server_error", "Internal error")
		return
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "Could not read body")
		return
	}
	var body chatRequestBody
	if err := json.Unmarshal(raw, &body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "Request body is not valid JSON")
		return
	}
	if body.Model == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	if len(body.Messages) == 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "messages is required")
		return
	}
	if body.Stream {
		writeAPIError(w, http.StatusNotImplemented, "invalid_request_error", "streaming not enabled yet")
		return
	}

	if !tenant.AllowsModel(body.Model) {
		// Allow concrete models only if the alias they belong to is allowlisted — phase 1:
		// require the requested string itself to be on the allowlist (fast/smart/auto).
		writeAPIError(w, http.StatusForbidden, "model_not_allowed", "Model is not on this key's allowlist")
		s.logRequest(ctx, logEntry{
			RequestID:      uuid.NewString(),
			VirtualKey:     tenant.VirtualKey,
			RequestedModel: body.Model,
			Status:         "rejected_allowlist",
			LatencyMs:      int(time.Since(start).Milliseconds()),
		})
		return
	}

	res, err := s.resolver.Resolve(body.Model)
	if err != nil {
		status := http.StatusNotFound
		typ := "not_found_error"
		if body.Model == "auto" {
			status = http.StatusNotImplemented
			typ = "invalid_request_error"
		}
		writeAPIError(w, status, typ, err.Error())
		return
	}

	model := res.ResolvedModel
	client, providerName, err := s.providers.ClientForModel(model)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "not_found_error", err.Error())
		return
	}

	upCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	upstream, err := client.ChatCompletion(upCtx, provider.ChatRequest{
		Model:    model,
		Messages: body.Messages,
	})
	if err != nil {
		var ue *provider.UpstreamError
		if errors.As(err, &ue) {
			code := ue.StatusCode
			if code < 400 {
				code = http.StatusBadGateway
			}
			writeAPIError(w, http.StatusBadGateway, "upstream_error", ue.Message)
			s.logRequest(ctx, logEntry{
				RequestID:        uuid.NewString(),
				VirtualKey:       tenant.VirtualKey,
				RequestedModel:   body.Model,
				ResolvedProvider: providerName,
				ResolvedModel:    model,
				Status:           "upstream_error",
				LatencyMs:        int(time.Since(start).Milliseconds()),
			})
			return
		}
		slog.Error("upstream call failed", "err", err, "provider", providerName, "model", model)
		writeAPIError(w, http.StatusBadGateway, "upstream_error", "Upstream provider call failed")
		return
	}

	price, err := s.meter.Price(ctx, model)
	if err != nil {
		slog.Error("price lookup failed", "err", err, "model", model)
		writeAPIError(w, http.StatusInternalServerError, "server_error", "Pricing unavailable for model")
		return
	}
	cost := meter.CostUSD(upstream.Usage.PromptTokens, upstream.Usage.CompletionTokens, price)
	costStr := meter.FormatUSD(cost)

	reqID := uuid.NewString()
	if upstream.ID != "" {
		reqID = upstream.ID
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("x-prism-provider", providerName+"/"+model)
	w.Header().Set("x-prism-cache", "miss")
	w.Header().Set("x-prism-fallback", "false")
	w.Header().Set("x-prism-cost-usd", trimCost(costStr))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(upstream.Raw)

	s.logRequest(context.WithoutCancel(ctx), logEntry{
		RequestID:         reqID,
		VirtualKey:        tenant.VirtualKey,
		RequestedModel:    body.Model,
		ResolvedProvider:  providerName,
		ResolvedModel:     model,
		Status:            "ok",
		PromptTokens:      upstream.Usage.PromptTokens,
		CompletionTokens:  upstream.Usage.CompletionTokens,
		CostMicroCents:    meter.ToMicroCents(cost),
		Cache:             "miss",
		Fallback:          false,
		LatencyMs:         int(time.Since(start).Milliseconds()),
	})
}

type logEntry struct {
	RequestID         string
	VirtualKey        string
	RequestedModel    string
	ResolvedProvider  string
	ResolvedModel     string
	Status            string
	PromptTokens      int
	CompletionTokens  int
	CostMicroCents    int64
	Cache             string
	Fallback          bool
	RouteReason       string
	Retries           int
	LatencyMs         int
}

func (s *Server) logRequest(ctx context.Context, e logEntry) {
	if e.Cache == "" {
		e.Cache = "miss"
	}
	_, err := s.db.Exec(ctx, `
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

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func trimCost(s string) string {
	// FormatUSD uses 8 decimals; trim trailing zeros but keep at least one decimal place feel.
	for len(s) > 1 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if len(s) > 0 && s[len(s)-1] == '.' {
		s += "0"
	}
	return s
}

func writeAPIError(w http.ResponseWriter, status int, typ, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"message": message,
			"type":    typ,
			"code":    typ,
		},
	})
}

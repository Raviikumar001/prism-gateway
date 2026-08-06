package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/raviikumar001/prism-gateway/internal/auth"
	"github.com/raviikumar001/prism-gateway/internal/budget"
	"github.com/raviikumar001/prism-gateway/internal/limit"
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

	if !tenant.AllowsModel(body.Model) {
		writeAPIError(w, http.StatusForbidden, "model_not_allowed", "Model is not on this key's allowlist")
		s.logger.Enqueue(logEntry{
			RequestID:      uuid.NewString(),
			VirtualKey:     tenant.VirtualKey,
			RequestedModel: body.Model,
			Status:         "rejected_allowlist",
			LatencyMs:      int(time.Since(start).Milliseconds()),
		})
		return
	}

	if err := s.rpm.Allow(ctx, tenant.VirtualKey, tenant.RPM); err != nil {
		if errors.Is(err, limit.ErrRateLimited) {
			writeAPIError(w, http.StatusTooManyRequests, "rate_limit_exceeded", "Requests per minute exceeded")
			s.logger.Enqueue(logEntry{
				RequestID:      uuid.NewString(),
				VirtualKey:     tenant.VirtualKey,
				RequestedModel: body.Model,
				Status:         "rejected_rpm",
				LatencyMs:      int(time.Since(start).Milliseconds()),
			})
			return
		}
		slog.Error("rpm check failed", "err", err)
		writeAPIError(w, http.StatusServiceUnavailable, "server_error", "Rate limiter unavailable")
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

	// Price estimate uses primary resolved model (conservative for chain).
	primary := res.ResolvedModel
	price, err := s.meter.Price(ctx, primary)
	if err != nil {
		slog.Error("price lookup failed", "err", err, "model", primary)
		writeAPIError(w, http.StatusInternalServerError, "server_error", "Pricing unavailable for model")
		return
	}

	contents := make([]string, len(body.Messages))
	for i, m := range body.Messages {
		contents[i] = m.Content
	}
	estimate := meter.EstimateMicroCents(meter.EstimatePromptTokens(contents...), meter.ReserveCompletionTokens, price)
	budgetLimit := int64(math.Round(tenant.MonthlyBudgetUSD * 100_000_000.0))

	rsv, err := s.budget.Reserve(ctx, tenant.VirtualKey, budgetLimit, estimate)
	if errors.Is(err, budget.ErrBudgetExceeded) {
		writeAPIError(w, http.StatusTooManyRequests, "budget_exceeded", "Monthly budget exceeded")
		s.logger.Enqueue(logEntry{
			RequestID:      uuid.NewString(),
			VirtualKey:     tenant.VirtualKey,
			RequestedModel: body.Model,
			ResolvedModel:  primary,
			Status:         "rejected_budget",
			LatencyMs:      int(time.Since(start).Milliseconds()),
		})
		return
	}
	if err != nil {
		slog.Error("budget reserve failed", "err", err)
		writeAPIError(w, http.StatusServiceUnavailable, "server_error", "Budget service unavailable")
		return
	}

	actualCost := int64(0)
	defer func() {
		settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := s.budget.Settle(settleCtx, rsv, actualCost); err != nil {
			slog.Error("budget settle failed", "err", err, "reservation", rsv.ID)
		}
	}()

	if body.Stream {
		s.handleStream(w, r, tenant, body, res.Chain, start, &actualCost)
		return
	}

	result, err := s.exec.Chat(ctx, res.Chain, body.Messages)
	if err != nil {
		if errors.Is(err, provider.ErrClientAbort) {
			s.logger.Enqueue(logEntry{
				RequestID:      uuid.NewString(),
				VirtualKey:     tenant.VirtualKey,
				RequestedModel: body.Model,
				Status:         "client_abort",
				LatencyMs:      int(time.Since(start).Milliseconds()),
			})
			return
		}
		if errors.Is(err, provider.ErrOverloaded) {
			writeAPIError(w, http.StatusServiceUnavailable, "server_error", "Gateway overloaded")
			return
		}
		writeAPIError(w, http.StatusBadGateway, "upstream_error", "All upstream providers failed")
		s.logger.Enqueue(logEntry{
			RequestID:      uuid.NewString(),
			VirtualKey:     tenant.VirtualKey,
			RequestedModel: body.Model,
			Status:         "upstream_error",
			LatencyMs:      int(time.Since(start).Milliseconds()),
		})
		return
	}

	billPrice, err := s.meter.Price(ctx, result.Model)
	if err != nil {
		billPrice = price
	}
	cost := meter.CostUSD(result.Response.Usage.PromptTokens, result.Response.Usage.CompletionTokens, billPrice)
	actualCost = meter.ToMicroCents(cost)
	costStr := trimCost(meter.FormatUSD(cost))

	reqID := uuid.NewString()
	if result.Response.ID != "" {
		reqID = result.Response.ID
	}

	s.bumpUsage(context.WithoutCancel(ctx), tenant.VirtualKey, result.Response.Usage.PromptTokens, result.Response.Usage.CompletionTokens, actualCost)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("x-prism-provider", result.Provider+"/"+result.Model)
	w.Header().Set("x-prism-cache", "miss")
	w.Header().Set("x-prism-fallback", fmt.Sprintf("%t", result.Fallback))
	w.Header().Set("x-prism-cost-usd", costStr)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result.Response.Raw)

	s.logger.Enqueue(logEntry{
		RequestID:        reqID,
		VirtualKey:       tenant.VirtualKey,
		RequestedModel:   body.Model,
		ResolvedProvider: result.Provider,
		ResolvedModel:    result.Model,
		Status:           "ok",
		PromptTokens:     result.Response.Usage.PromptTokens,
		CompletionTokens: result.Response.Usage.CompletionTokens,
		CostMicroCents:   actualCost,
		Cache:            "miss",
		Fallback:         result.Fallback,
		Retries:          result.Retries,
		LatencyMs:        int(time.Since(start).Milliseconds()),
	})
}

func (s *Server) handleStream(
	w http.ResponseWriter,
	r *http.Request,
	tenant *auth.Tenant,
	body chatRequestBody,
	chain []string,
	start time.Time,
	actualCost *int64,
) {
	ctx := r.Context()
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, http.StatusInternalServerError, "server_error", "Streaming unsupported")
		return
	}

	headersWritten := false
	writeHeaders := func(prov, model string, fallback bool) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("x-prism-provider", prov+"/"+model)
		w.Header().Set("x-prism-cache", "miss")
		w.Header().Set("x-prism-fallback", fmt.Sprintf("%t", fallback))
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		headersWritten = true
	}

	writeEvent := func(data []byte) error {
		_, err := fmt.Fprintf(w, "data: %s\n\n", data)
		if err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	writeDone := func() error {
		_, err := io.WriteString(w, "data: [DONE]\n\n")
		if err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	writeInBandError := func(msg string) error {
		payload, _ := json.Marshal(map[string]any{
			"error": map[string]string{"message": msg, "type": "upstream_error", "code": "upstream_error"},
		})
		_, err := fmt.Fprintf(w, "data: %s\n\n", payload)
		if err != nil {
			return err
		}
		flusher.Flush()
		_ = writeDone()
		return nil
	}

	streamRes, err := s.exec.ChatStream(ctx, chain, body.Messages, writeHeaders, writeEvent, writeDone, writeInBandError)

	status := "ok"
	prov, model := "", ""
	fallback := false
	retries := 0
	var usage provider.Usage
	if streamRes != nil {
		prov, model = streamRes.Provider, streamRes.Model
		fallback = streamRes.Fallback
		retries = streamRes.Retries
		usage = streamRes.Usage
	}

	if err != nil {
		if errors.Is(err, provider.ErrClientAbort) {
			status = "client_abort"
		} else if !headersWritten {
			if errors.Is(err, provider.ErrOverloaded) {
				writeAPIError(w, http.StatusServiceUnavailable, "server_error", "Gateway overloaded")
			} else {
				writeAPIError(w, http.StatusBadGateway, "upstream_error", "All upstream providers failed")
			}
			status = "upstream_error"
		} else {
			status = "upstream_error"
		}
	}

	if usage.PromptTokens > 0 || usage.CompletionTokens > 0 {
		billPrice, perr := s.meter.Price(context.WithoutCancel(ctx), model)
		if perr == nil {
			cost := meter.CostUSD(usage.PromptTokens, usage.CompletionTokens, billPrice)
			*actualCost = meter.ToMicroCents(cost)
			s.bumpUsage(context.WithoutCancel(ctx), tenant.VirtualKey, usage.PromptTokens, usage.CompletionTokens, *actualCost)
		}
	}

	s.logger.Enqueue(logEntry{
		RequestID:        uuid.NewString(),
		VirtualKey:       tenant.VirtualKey,
		RequestedModel:   body.Model,
		ResolvedProvider: prov,
		ResolvedModel:    model,
		Status:           status,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		CostMicroCents:   *actualCost,
		Cache:            "miss",
		Fallback:         fallback,
		Retries:          retries,
		LatencyMs:        int(time.Since(start).Milliseconds()),
	})
}

func (s *Server) bumpUsage(ctx context.Context, virtualKey string, prompt, completion int, costMicroCents int64) {
	month := time.Now().UTC().Format("200601")
	_, err := s.db.Exec(ctx, `
		INSERT INTO usage_monthly (virtual_key, month, requests, prompt_tokens, completion_tokens, cost_micro_cents)
		VALUES ($1, $2, 1, $3, $4, $5)
		ON CONFLICT (virtual_key, month) DO UPDATE SET
			requests = usage_monthly.requests + 1,
			prompt_tokens = usage_monthly.prompt_tokens + EXCLUDED.prompt_tokens,
			completion_tokens = usage_monthly.completion_tokens + EXCLUDED.completion_tokens,
			cost_micro_cents = usage_monthly.cost_micro_cents + EXCLUDED.cost_micro_cents
	`, virtualKey, month, prompt, completion, costMicroCents)
	if err != nil {
		slog.Error("usage_monthly upsert failed", "err", err)
	}
}

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
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func trimCost(s string) string {
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

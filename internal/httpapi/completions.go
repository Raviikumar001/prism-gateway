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
	"github.com/raviikumar001/prism-gateway/internal/cache"
	"github.com/raviikumar001/prism-gateway/internal/limit"
	"github.com/raviikumar001/prism-gateway/internal/meter"
	"github.com/raviikumar001/prism-gateway/internal/provider"
	"github.com/raviikumar001/prism-gateway/internal/route"
)

const maxChatRequestBytes = 8 << 20

const unauthenticatedKey = "_unauthenticated"

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx := r.Context()

	token, err := auth.BearerToken(r.Header.Get("Authorization"))
	if err != nil {
		s.rejectAndLog(w, start, http.StatusUnauthorized, "authentication_error",
			"Missing or invalid Authorization bearer token", logEntry{
				VirtualKey: unauthenticatedKey,
				Status:     "rejected_auth",
			})
		return
	}

	tenant, err := s.auth.Lookup(ctx, token)
	if errors.Is(err, auth.ErrInvalidKey) || errors.Is(err, auth.ErrDisabled) {
		s.rejectAndLog(w, start, http.StatusUnauthorized, "authentication_error", "Invalid API key", logEntry{
			VirtualKey: unauthenticatedKey,
			Status:     "rejected_auth",
		})
		return
	}
	if err != nil {
		slog.Error("tenant lookup failed", "err", err)
		s.rejectAndLog(w, start, http.StatusInternalServerError, "server_error", "Internal error", logEntry{
			VirtualKey: unauthenticatedKey,
			Status:     "auth_lookup_failed",
		})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxChatRequestBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			s.rejectAndLog(w, start, http.StatusRequestEntityTooLarge, "invalid_request_error",
				"Request body exceeds 8 MiB", logEntry{
					VirtualKey: tenant.VirtualKey,
					Status:     "rejected_malformed",
				})
			return
		}
		s.rejectAndLog(w, start, http.StatusBadRequest, "invalid_request_error", "Could not read body", logEntry{
			VirtualKey: tenant.VirtualKey,
			Status:     "rejected_malformed",
		})
		return
	}
	body, err := provider.ParseChatRequest(raw)
	if err != nil {
		s.rejectAndLog(w, start, http.StatusBadRequest, "invalid_request_error",
			"Request body is not valid JSON", logEntry{
				VirtualKey: tenant.VirtualKey,
				Status:     "rejected_malformed",
			})
		return
	}
	if body.Model == "" {
		s.rejectAndLog(w, start, http.StatusBadRequest, "invalid_request_error", "model is required", logEntry{
			VirtualKey: tenant.VirtualKey,
			Status:     "rejected_malformed",
		})
		return
	}
	if len(body.Messages) == 0 {
		s.rejectAndLog(w, start, http.StatusBadRequest, "invalid_request_error", "messages is required", logEntry{
			VirtualKey:     tenant.VirtualKey,
			RequestedModel: body.Model,
			Status:         "rejected_malformed",
		})
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
		s.logger.Enqueue(logEntry{
			RequestID:      uuid.NewString(),
			VirtualKey:     tenant.VirtualKey,
			RequestedModel: body.Model,
			Status:         "rate_limiter_unavailable",
			LatencyMs:      int(time.Since(start).Milliseconds()),
		})
		return
	}
	tokenEstimate := body.PromptTokenUpperBound() + body.CompletionTokenLimit()
	if err := s.rpm.AllowTokens(ctx, tenant.VirtualKey, tokenEstimate, tenant.TPM); err != nil {
		if errors.Is(err, limit.ErrRateLimited) {
			writeAPIError(w, http.StatusTooManyRequests, "token_rate_limit_exceeded", "Tokens per minute exceeded")
			s.logger.Enqueue(logEntry{
				RequestID:      uuid.NewString(),
				VirtualKey:     tenant.VirtualKey,
				RequestedModel: body.Model,
				Status:         "rejected_tpm",
				PromptTokens:   tokenEstimate,
				LatencyMs:      int(time.Since(start).Milliseconds()),
			})
			return
		}
		slog.Error("tpm check failed", "err", err)
		writeAPIError(w, http.StatusServiceUnavailable, "server_error", "Token rate limiter unavailable")
		s.logger.Enqueue(logEntry{
			RequestID:      uuid.NewString(),
			VirtualKey:     tenant.VirtualKey,
			RequestedModel: body.Model,
			Status:         "token_rate_limiter_unavailable",
			PromptTokens:   tokenEstimate,
			LatencyMs:      int(time.Since(start).Milliseconds()),
		})
		return
	}

	promptText := body.LastUserText()
	routingText := body.RoutingText()
	cacheVariant, cacheSafe := body.CacheVariant()
	normalized := cache.Key(body.Model, cacheVariant, promptText)

	res, err := s.resolver.ResolvePrompt(body.Model, routingText)
	if err != nil {
		status := http.StatusNotFound
		typ := "not_found_error"
		writeAPIError(w, status, typ, err.Error())
		s.logger.Enqueue(logEntry{
			RequestID:      uuid.NewString(),
			VirtualKey:     tenant.VirtualKey,
			RequestedModel: body.Model,
			Status:         "model_not_found",
			LatencyMs:      int(time.Since(start).Milliseconds()),
		})
		return
	}

	primary := res.ResolvedModel
	price, err := s.maxChainPrice(ctx, res.Chain)
	if err != nil {
		slog.Error("price lookup failed", "err", err, "models", res.Chain)
		writeAPIError(w, http.StatusInternalServerError, "server_error", "Pricing unavailable for model")
		s.logger.Enqueue(logEntry{
			RequestID:      uuid.NewString(),
			VirtualKey:     tenant.VirtualKey,
			RequestedModel: body.Model,
			ResolvedModel:  primary,
			Status:         "pricing_unavailable",
			RouteReason:    res.RouteReason,
			LatencyMs:      int(time.Since(start).Milliseconds()),
		})
		return
	}

	cacheEnabled := tenant.CacheEnabled && !body.Stream && cacheSafe
	if cacheEnabled {
		hit, cacheErr := s.cache.Lookup(ctx, tenant.VirtualKey, normalized, tenant.CacheThreshold)
		if cacheErr != nil {
			slog.Warn("cache lookup failed; continuing without cache", "err", cacheErr)
		}
		if hit != nil {
			s.bumpCacheHit(context.WithoutCancel(ctx), tenant.VirtualKey)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("x-prism-provider", "cache")
			w.Header().Set("x-prism-cache", "hit")
			w.Header().Set("x-prism-fallback", "false")
			w.Header().Set("x-prism-cost-usd", "0")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(hit.Body)
			s.logger.Enqueue(logEntry{
				RequestID:      uuid.NewString(),
				VirtualKey:     tenant.VirtualKey,
				RequestedModel: body.Model,
				ResolvedModel:  responseModel(hit.Body, primary),
				Status:         "ok",
				Cache:          "hit",
				RouteReason:    res.RouteReason,
				LatencyMs:      int(time.Since(start).Milliseconds()),
			})
			return
		}
	}

	estimate := meter.EstimateMicroCents(
		body.PromptTokenUpperBound(),
		body.CompletionTokenLimit(),
		price,
	)
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
			RouteReason:    res.RouteReason,
			LatencyMs:      int(time.Since(start).Milliseconds()),
		})
		return
	}
	if err != nil {
		slog.Error("budget reserve failed", "err", err)
		writeAPIError(w, http.StatusServiceUnavailable, "server_error", "Budget service unavailable")
		s.logger.Enqueue(logEntry{
			RequestID:      uuid.NewString(),
			VirtualKey:     tenant.VirtualKey,
			RequestedModel: body.Model,
			ResolvedModel:  primary,
			Status:         "budget_unavailable",
			RouteReason:    res.RouteReason,
			LatencyMs:      int(time.Since(start).Milliseconds()),
		})
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
	stopRefresh := s.keepBudgetReservationAlive(rsv)
	defer stopRefresh()

	if body.Stream {
		s.handleStream(w, r, tenant, body, res, start, estimate, price, &actualCost)
		return
	}

	result, err := s.exec.Chat(ctx, res.Chain, body)
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
			s.logger.Enqueue(logEntry{
				RequestID:      uuid.NewString(),
				VirtualKey:     tenant.VirtualKey,
				RequestedModel: body.Model,
				Status:         "gateway_overloaded",
				LatencyMs:      int(time.Since(start).Milliseconds()),
			})
			return
		}
		var upstreamErr *provider.UpstreamError
		if errors.As(err, &upstreamErr) &&
			upstreamErr.StatusCode >= 400 &&
			upstreamErr.StatusCode < 500 &&
			upstreamErr.StatusCode != http.StatusTooManyRequests {
			writeUpstreamClientError(w, upstreamErr)
			s.logger.Enqueue(logEntry{
				RequestID:      uuid.NewString(),
				VirtualKey:     tenant.VirtualKey,
				RequestedModel: body.Model,
				Status:         "upstream_client_error",
				LatencyMs:      int(time.Since(start).Milliseconds()),
			})
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
	if cacheEnabled {
		if err := s.cache.Store(context.WithoutCancel(ctx), tenant.VirtualKey, normalized, result.Response.Raw); err != nil {
			slog.Error("cache store failed", "err", err)
		}
	}

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
		RouteReason:      res.RouteReason,
		Retries:          result.Retries,
		LatencyMs:        int(time.Since(start).Milliseconds()),
	})
}

func (s *Server) handleStream(
	w http.ResponseWriter,
	r *http.Request,
	tenant *auth.Tenant,
	body provider.ChatRequest,
	res *route.Resolution,
	start time.Time,
	estimatedCost int64,
	reservedPrice meter.Price,
	actualCost *int64,
) {
	ctx := r.Context()
	chain := res.Chain
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
		w.Header().Set("x-prism-cost-usd", trimCost(meter.FormatUSD(float64(estimatedCost)/100_000_000.0)))
		w.Header().Set("x-prism-cost-estimated", "true")
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

	streamRes, err := s.exec.ChatStream(ctx, chain, body, writeHeaders, writeEvent, writeDone, writeInBandError)

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
			var upstreamErr *provider.UpstreamError
			if errors.As(err, &upstreamErr) &&
				upstreamErr.StatusCode >= 400 &&
				upstreamErr.StatusCode < 500 &&
				upstreamErr.StatusCode != http.StatusTooManyRequests {
				writeUpstreamClientError(w, upstreamErr)
				status = "upstream_client_error"
			} else if errors.Is(err, provider.ErrOverloaded) {
				writeAPIError(w, http.StatusServiceUnavailable, "server_error", "Gateway overloaded")
				status = "gateway_overloaded"
			} else {
				writeAPIError(w, http.StatusBadGateway, "upstream_error", "All upstream providers failed")
				status = "upstream_error"
			}
		} else {
			status = "upstream_error"
		}
	}

	if usage.PromptTokens > 0 || usage.CompletionTokens > 0 {
		billPrice, perr := s.meter.Price(context.WithoutCancel(ctx), model)
		if perr != nil {
			slog.Error("stream price lookup failed; using reserved price", "err", perr, "model", model)
			billPrice = reservedPrice
		}
		cost := meter.CostUSD(usage.PromptTokens, usage.CompletionTokens, billPrice)
		*actualCost = meter.ToMicroCents(cost)
		s.bumpUsage(context.WithoutCancel(ctx), tenant.VirtualKey, usage.PromptTokens, usage.CompletionTokens, *actualCost)
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
		RouteReason:      res.RouteReason,
		Retries:          retries,
		LatencyMs:        int(time.Since(start).Milliseconds()),
	})
}

func (s *Server) keepBudgetReservationAlive(rsv *budget.Reservation) func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(budget.RefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				refreshCtx, refreshCancel := context.WithTimeout(ctx, 5*time.Second)
				err := s.budget.Refresh(refreshCtx, rsv)
				refreshCancel()
				if err != nil && !errors.Is(err, context.Canceled) {
					slog.Error("budget reservation refresh failed", "err", err, "reservation", rsv.ID)
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func (s *Server) maxChainPrice(ctx context.Context, chain []string) (meter.Price, error) {
	if len(chain) == 0 {
		return meter.Price{}, fmt.Errorf("empty model chain")
	}
	var maxPrice meter.Price
	for _, model := range chain {
		price, err := s.meter.Price(ctx, model)
		if err != nil {
			return meter.Price{}, err
		}
		if price.InputPer1M > maxPrice.InputPer1M {
			maxPrice.InputPer1M = price.InputPer1M
		}
		if price.OutputPer1M > maxPrice.OutputPer1M {
			maxPrice.OutputPer1M = price.OutputPer1M
		}
	}
	return maxPrice, nil
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

func (s *Server) bumpCacheHit(ctx context.Context, virtualKey string) {
	month := time.Now().UTC().Format("200601")
	_, err := s.db.Exec(ctx, `
		INSERT INTO usage_monthly (virtual_key, month, requests, cache_hits)
		VALUES ($1, $2, 1, 1)
		ON CONFLICT (virtual_key, month) DO UPDATE SET
			requests = usage_monthly.requests + 1,
			cache_hits = usage_monthly.cache_hits + 1
	`, virtualKey, month)
	if err != nil {
		slog.Error("cache hit usage upsert failed", "err", err)
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

func (s *Server) rejectAndLog(w http.ResponseWriter, start time.Time, statusCode int, typ, message string, e logEntry) {
	writeAPIError(w, statusCode, typ, message)
	if e.RequestID == "" {
		e.RequestID = uuid.NewString()
	}
	if e.VirtualKey == "" {
		e.VirtualKey = unauthenticatedKey
	}
	if e.Cache == "" {
		e.Cache = "miss"
	}
	e.LatencyMs = int(time.Since(start).Milliseconds())
	s.logger.Enqueue(e)
}

func responseModel(body json.RawMessage, fallback string) string {
	var response struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &response) == nil && response.Model != "" {
		return response.Model
	}
	return fallback
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

func writeUpstreamClientError(w http.ResponseWriter, upstreamErr *provider.UpstreamError) {
	var payload map[string]json.RawMessage
	if json.Unmarshal(upstreamErr.Body, &payload) == nil && len(payload["error"]) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(upstreamErr.StatusCode)
		_, _ = w.Write(upstreamErr.Body)
		return
	}
	message := upstreamErr.Message
	if message == "" {
		message = "Upstream rejected the request"
	}
	writeAPIError(w, upstreamErr.StatusCode, "invalid_request_error", message)
}

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/raviikumar001/prism-gateway/internal/gatewaycfg"
)

func TestExecutorPreservesFallbackAttemptAfterRetry(t *testing.T) {
	var primaryCalls atomic.Int64
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryCalls.Add(1)
		http.Error(w, `{"error":{"message":"temporary"}}`, http.StatusInternalServerError)
	}))
	defer primary.Close()

	middle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"temporary"}}`, http.StatusInternalServerError)
	}))
	defer middle.Close()

	last := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["model"] != "last-model" {
			t.Fatalf("model = %v", request["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"ok","object":"chat.completion","model":"last-model",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`))
	}))
	defer last.Close()

	cfg := &gatewaycfg.Config{
		Providers: []gatewaycfg.Provider{
			{Name: "primary", BaseURL: primary.URL, Models: []string{"primary-model"}},
			{Name: "middle", BaseURL: middle.URL, Models: []string{"middle-model"}},
			{Name: "last", BaseURL: last.URL, Models: []string{"last-model"}},
		},
		Retry: gatewaycfg.Retry{MaxAttempts: 4, InitialBackoffMs: 1, BackoffMultiplier: 2},
	}
	executor := NewExecutor(cfg, time.Second, 4)
	request, err := ParseChatRequest([]byte(`{
		"model":"fast",
		"messages":[{"role":"user","content":"hello"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}

	result, err := executor.Chat(
		context.Background(),
		[]string{"primary-model", "middle-model", "last-model"},
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != "last-model" || !result.Fallback {
		t.Fatalf("result = %+v", result)
	}
	if primaryCalls.Load() != 2 {
		t.Fatalf("primary calls = %d, want one retry", primaryCalls.Load())
	}
}

func TestExecutorRetriesAttemptTimeout(t *testing.T) {
	var calls atomic.Int64
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		time.Sleep(40 * time.Millisecond)
		_, _ = w.Write([]byte(`{"object":"chat.completion"}`))
	}))
	defer slow.Close()

	cfg := &gatewaycfg.Config{
		Providers: []gatewaycfg.Provider{
			{Name: "slow", BaseURL: slow.URL, Models: []string{"slow-model"}},
		},
		Retry: gatewaycfg.Retry{MaxAttempts: 2, InitialBackoffMs: 1, BackoffMultiplier: 2},
	}
	executor := NewExecutor(cfg, 10*time.Millisecond, 2)
	request, err := ParseChatRequest([]byte(`{
		"model":"slow-model",
		"messages":[{"role":"user","content":"hello"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Chat(context.Background(), []string{"slow-model"}, request)
	if err == nil {
		t.Fatal("expected timeout failure")
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, expected timeout retry", calls.Load())
	}
}

func TestExecutorSkipsSameProviderRetryWhenNoSpareAttempts(t *testing.T) {
	var primaryCalls atomic.Int64
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryCalls.Add(1)
		http.Error(w, `{"error":{"message":"temporary"}}`, http.StatusInternalServerError)
	}))
	defer primary.Close()

	var fallbackCalls atomic.Int64
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fallbackCalls.Add(1)
		http.Error(w, `{"error":{"message":"temporary"}}`, http.StatusInternalServerError)
	}))
	defer fallback.Close()

	cfg := &gatewaycfg.Config{
		Providers: []gatewaycfg.Provider{
			{Name: "primary", BaseURL: primary.URL, Models: []string{"primary-model"}},
			{Name: "fallback", BaseURL: fallback.URL, Models: []string{"fallback-model"}},
		},
		Retry: gatewaycfg.Retry{MaxAttempts: 2, InitialBackoffMs: 1, BackoffMultiplier: 2},
	}
	executor := NewExecutor(cfg, time.Second, 4)
	request, err := ParseChatRequest([]byte(`{
		"model":"fast",
		"messages":[{"role":"user","content":"hello"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Chat(context.Background(), []string{"primary-model", "fallback-model"}, request)
	if err == nil {
		t.Fatal("expected all attempts to fail")
	}
	if primaryCalls.Load() != 1 {
		t.Fatalf("primary calls = %d, want 1 so the fallback still fits", primaryCalls.Load())
	}
	if fallbackCalls.Load() != 1 {
		t.Fatalf("fallback calls = %d, want 1", fallbackCalls.Load())
	}
}

func TestStreamClientWriteFailureDoesNotTripBreaker(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	cfg := &gatewaycfg.Config{
		Providers: []gatewaycfg.Provider{
			{Name: "upstream", BaseURL: upstream.URL, Models: []string{"model-a"}},
		},
		Retry: gatewaycfg.Retry{MaxAttempts: 1},
	}
	executor := NewExecutor(cfg, time.Second, 2)
	request, err := ParseChatRequest([]byte(`{
		"model":"model-a",
		"messages":[{"role":"user","content":"hello"}],
		"stream":true
	}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.ChatStream(
		context.Background(),
		[]string{"model-a"},
		request,
		func(string, string, bool) {},
		func([]byte) error { return errors.New("client disconnected") },
		func() error { return nil },
		func(string) error { return nil },
	)
	if !errors.Is(err, ErrClientAbort) {
		t.Fatalf("expected client abort, got %v", err)
	}
	snapshot := executor.HealthSnapshot()
	if got := snapshot[0]["samples"]; got != 0 {
		t.Fatalf("client abort recorded as breaker sample: %v", got)
	}
}

func TestExecutorPreservesTerminalUpstreamClientError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"bad tool schema"}}`))
	}))
	defer upstream.Close()

	cfg := &gatewaycfg.Config{
		Providers: []gatewaycfg.Provider{
			{Name: "upstream", BaseURL: upstream.URL, Models: []string{"model-a"}},
		},
		Retry: gatewaycfg.Retry{MaxAttempts: 1},
	}
	executor := NewExecutor(cfg, time.Second, 2)
	request, err := ParseChatRequest([]byte(`{
		"model":"model-a",
		"messages":[{"role":"user","content":"hello"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Chat(context.Background(), []string{"model-a"}, request)
	var upstreamErr *UpstreamError
	if !errors.As(err, &upstreamErr) {
		t.Fatalf("expected wrapped upstream error, got %v", err)
	}
	if upstreamErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", upstreamErr.StatusCode)
	}
}

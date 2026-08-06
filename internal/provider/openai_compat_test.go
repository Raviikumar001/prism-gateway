package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestChatRequestPayloadPreservesCodingAgentFields(t *testing.T) {
	raw := []byte(`{
		"model":"auto",
		"messages":[
			{"role":"system","content":"You are a coding agent."},
			{"role":"user","content":"Read main.go"},
			{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"main.go\"}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"package main"}
		],
		"tools":[{"type":"function","function":{"name":"read_file","parameters":{"type":"object"}}}],
		"tool_choice":"auto",
		"parallel_tool_calls":true,
		"temperature":0.2,
		"max_tokens":123
	}`)

	req, err := ParseChatRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.LastUserText(); got != "Read main.go" {
		t.Fatalf("last user text = %q", got)
	}
	if got := req.RoutingText(); !strings.Contains(got, "system: You are a coding agent.") ||
		!strings.Contains(got, "tool: package main") {
		t.Fatalf("routing text omitted conversation context: %q", got)
	}
	if req.PromptTokenUpperBound() < len(raw) {
		t.Fatalf("prompt hold %d does not cover request bytes %d", req.PromptTokenUpperBound(), len(raw))
	}

	payload, err := req.Payload("openai/gpt-4.1-mini", false)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	if got["model"] != "openai/gpt-4.1-mini" {
		t.Fatalf("model not overridden: %v", got["model"])
	}
	if got["max_tokens"] != float64(123) || got["temperature"] != 0.2 {
		t.Fatalf("generation controls were not preserved: %v", got)
	}
	if _, ok := got["tools"]; !ok {
		t.Fatal("tools were dropped")
	}
	messages := got["messages"].([]any)
	assistant := messages[2].(map[string]any)
	if _, ok := assistant["tool_calls"]; !ok {
		t.Fatal("assistant tool_calls were dropped")
	}
	tool := messages[3].(map[string]any)
	if tool["tool_call_id"] != "call_1" {
		t.Fatalf("tool_call_id was dropped: %v", tool)
	}
}

func TestChatRequestPayloadAddsBoundedDefaultsAndStreamUsage(t *testing.T) {
	req, err := ParseChatRequest([]byte(`{
		"model":"fast",
		"messages":[{"role":"user","content":"hello"}],
		"stream":true,
		"stream_options":{"include_obfuscation":false}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := req.Payload("mistralai/mistral-nemo", true)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	if got["max_tokens"] != float64(DefaultMaxCompletionTokens) {
		t.Fatalf("default max_tokens = %v", got["max_tokens"])
	}
	options := got["stream_options"].(map[string]any)
	if options["include_usage"] != true || options["include_obfuscation"] != false {
		t.Fatalf("stream_options not merged: %v", options)
	}
}

func TestCacheVariantRejectsAgenticAndMultiTurnRequests(t *testing.T) {
	agentic, err := ParseChatRequest([]byte(`{
		"model":"fast",
		"messages":[{"role":"user","content":"inspect the repo"}],
		"tools":[{"type":"function","function":{"name":"list_files"}}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := agentic.CacheVariant(); ok {
		t.Fatal("tool request must bypass semantic cache")
	}

	multiTurn, err := ParseChatRequest([]byte(`{
		"model":"fast",
		"messages":[
			{"role":"system","content":"Return JSON"},
			{"role":"user","content":"hello"}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := multiTurn.CacheVariant(); ok {
		t.Fatal("multi-turn request must bypass semantic cache")
	}

	fresh, err := ParseChatRequest([]byte(`{
		"model":"fast",
		"messages":[{"role":"user","content":"What is the current status of the payments service?"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fresh.CacheVariant(); ok {
		t.Fatal("time-sensitive prompt must bypass semantic cache")
	}
}

func TestChatRequestValidatesAndBoundsCompletionLimit(t *testing.T) {
	if _, err := ParseChatRequest([]byte(`{
		"model":"fast",
		"messages":[{"role":"user","content":"hello"}],
		"max_tokens":-1
	}`)); err == nil {
		t.Fatal("expected invalid max_tokens error")
	}

	req, err := ParseChatRequest([]byte(`{
		"model":"fast",
		"messages":[{"role":"user","content":"hello"}],
		"max_tokens":null
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if req.CompletionTokenLimit() != DefaultMaxCompletionTokens {
		t.Fatalf("completion limit = %d", req.CompletionTokenLimit())
	}
	payload, err := req.Payload("model-a", false)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["max_tokens"] != float64(DefaultMaxCompletionTokens) {
		t.Fatalf("default max_tokens = %v", fields["max_tokens"])
	}
}

func TestChatCompletionAcceptsStructuredResponseContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"structured","object":"chat.completion","model":"model-a",
			"choices":[{"index":0,"message":{"role":"assistant","content":[{"type":"text","text":"ok"}]},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`))
	}))
	defer server.Close()

	client := NewOpenAICompat("test", server.URL, "key", time.Second, nil)
	request, err := ParseChatRequest([]byte(`{
		"model":"model-a",
		"messages":[{"role":"user","content":"hello"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.ChatCompletion(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(response.Raw), `"type":"text"`) {
		t.Fatalf("structured response was not preserved: %s", response.Raw)
	}
}

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type ChatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type ChatRequest struct {
	Model    string
	Messages []ChatMessage
	Stream   bool
	fields   map[string]json.RawMessage
}

// DefaultMaxCompletionTokens is injected when callers omit an output cap. This
// keeps budget reservations bounded while still leaving enough room for code.
const DefaultMaxCompletionTokens = 4096

func ParseChatRequest(raw []byte) (ChatRequest, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return ChatRequest{}, err
	}
	if fields == nil {
		return ChatRequest{}, fmt.Errorf("request body must be a JSON object")
	}

	var envelope struct {
		Model    string        `json:"model"`
		Messages []ChatMessage `json:"messages"`
		Stream   bool          `json:"stream"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return ChatRequest{}, err
	}
	for _, key := range []string{"max_completion_tokens", "max_tokens"} {
		value, present := positiveInt(fields[key])
		if present && value <= 0 {
			return ChatRequest{}, fmt.Errorf("%s must be a positive integer", key)
		}
	}
	return ChatRequest{
		Model:    envelope.Model,
		Messages: envelope.Messages,
		Stream:   envelope.Stream,
		fields:   fields,
	}, nil
}

func (r ChatRequest) Payload(model string, stream bool) ([]byte, error) {
	fields := make(map[string]json.RawMessage, len(r.fields)+2)
	for k, v := range r.fields {
		fields[k] = v
	}
	if len(fields) == 0 {
		messages, err := json.Marshal(r.Messages)
		if err != nil {
			return nil, err
		}
		fields["messages"] = messages
	}

	modelJSON, _ := json.Marshal(model)
	streamJSON, _ := json.Marshal(stream)
	fields["model"] = modelJSON
	fields["stream"] = streamJSON

	if _, hasCompletion := positiveInt(fields["max_completion_tokens"]); !hasCompletion {
		delete(fields, "max_completion_tokens")
		if _, hasLegacy := positiveInt(fields["max_tokens"]); !hasLegacy {
			limit, _ := json.Marshal(DefaultMaxCompletionTokens)
			fields["max_tokens"] = limit
		}
	}
	if stream {
		var opts map[string]json.RawMessage
		if raw := fields["stream_options"]; len(raw) > 0 {
			_ = json.Unmarshal(raw, &opts)
		}
		if opts == nil {
			opts = make(map[string]json.RawMessage)
		}
		opts["include_usage"] = json.RawMessage("true")
		encoded, err := json.Marshal(opts)
		if err != nil {
			return nil, err
		}
		fields["stream_options"] = encoded
	}
	return json.Marshal(fields)
}

func (r ChatRequest) CompletionTokenLimit() int {
	for _, key := range []string{"max_completion_tokens", "max_tokens"} {
		if n, ok := positiveInt(r.fields[key]); ok && n > 0 {
			return n
		}
	}
	return DefaultMaxCompletionTokens
}

// positiveInt distinguishes an omitted/null field from an invalid integer.
// The boolean reports whether a non-null value was provided.
func positiveInt(raw json.RawMessage) (int, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var n int
	if json.Unmarshal(raw, &n) != nil {
		return 0, true
	}
	return n, true
}

func (r ChatRequest) PromptTokenUpperBound() int {
	encoded, err := json.Marshal(r.fields)
	if err != nil || len(encoded) == 0 {
		return 1
	}
	// A token cannot encode fewer than one byte. Include chat-template
	// overhead per message for a conservative budget hold.
	return len(encoded) + 16*len(r.Messages)
}

func (r ChatRequest) LastUserText() string {
	for i := len(r.Messages) - 1; i >= 0; i-- {
		if strings.EqualFold(r.Messages[i].Role, "user") {
			text, _ := r.Messages[i].TextContent()
			return text
		}
	}
	return ""
}

func (r ChatRequest) RoutingText() string {
	var b strings.Builder
	for _, message := range r.Messages {
		text, ok := message.TextContent()
		if !ok || text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(message.Role)
		b.WriteString(": ")
		b.WriteString(text)
	}
	return b.String()
}

func (m ChatMessage) TextContent() (string, bool) {
	var text string
	if json.Unmarshal(m.Content, &text) == nil {
		return text, true
	}

	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(m.Content, &parts) != nil || len(parts) == 0 {
		return "", false
	}
	var b strings.Builder
	for _, part := range parts {
		if part.Type != "text" && part.Type != "input_text" {
			return "", false
		}
		b.WriteString(part.Text)
	}
	return b.String(), true
}

// CacheVariant allows semantic caching only for a single, text-only user
// message with no tools. It includes generation controls so incompatible
// response shapes or output limits cannot collide. Time-sensitive prompts
// (e.g. "current status…") are excluded so stale answers cannot be served.
func (r ChatRequest) CacheVariant() (string, bool) {
	if len(r.Messages) != 1 || !strings.EqualFold(r.Messages[0].Role, "user") {
		return "", false
	}
	text, ok := r.Messages[0].TextContent()
	if !ok {
		return "", false
	}
	if isTimeSensitivePrompt(text) {
		return "", false
	}
	for _, key := range []string{
		"tools", "tool_choice", "parallel_tool_calls",
		"functions", "function_call",
	} {
		if raw, ok := r.fields[key]; ok && string(raw) != "null" && string(raw) != "[]" {
			return "", false
		}
	}

	variant := map[string]json.RawMessage{}
	for key, raw := range r.fields {
		if key == "model" || key == "messages" || key == "stream" {
			continue
		}
		variant[key] = raw
	}
	encoded, err := json.Marshal(variant)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

var timeSensitiveMarkers = []string{
	"current status",
	"currently",
	"right now",
	"at this moment",
	"as of today",
	"as of now",
	"live status",
	"latest status",
	"what's happening now",
	"what is happening now",
	"up to date",
	"real-time",
	"realtime",
}

func isTimeSensitivePrompt(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range timeSensitiveMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type ChatResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage Usage           `json:"usage"`
	Raw   json.RawMessage `json:"-"`
}

const maxUpstreamResponseBytes = 16 << 20

type UpstreamError struct {
	StatusCode int
	Body       json.RawMessage
	Message    string
	RetryAfter time.Duration
}

func (e *UpstreamError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("upstream status %d", e.StatusCode)
}

type OpenAICompat struct {
	Name         string
	BaseURL      string
	APIKey       string
	ExtraHeaders map[string]string
	HTTPClient   *http.Client
}

func NewOpenAICompat(name, baseURL, apiKey string, timeout time.Duration, extraHeaders map[string]string) *OpenAICompat {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 20
	transport.IdleConnTimeout = 90 * time.Second
	transport.ResponseHeaderTimeout = timeout
	return &OpenAICompat{
		Name:         name,
		BaseURL:      baseURL,
		APIKey:       apiKey,
		ExtraHeaders: extraHeaders,
		HTTPClient: &http.Client{
			Timeout:   timeout,
			Transport: transport,
		},
	}
}

func (c *OpenAICompat) applyHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	for k, v := range c.ExtraHeaders {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
}

func (c *OpenAICompat) ChatCompletion(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	payload, err := req.Payload(req.Model, false)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	c.applyHeaders(httpReq)

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxUpstreamResponseBytes {
		return nil, fmt.Errorf("upstream response exceeds 16 MiB")
	}

	if resp.StatusCode >= 400 {
		var parsed struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &parsed)
		msg := parsed.Error.Message
		if msg == "" {
			msg = string(body)
		}
		return nil, &UpstreamError{
			StatusCode: resp.StatusCode,
			Body:       body,
			Message:    msg,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}
	}

	var out ChatResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode upstream response: %w", err)
	}
	out.Raw = body
	return &out, nil
}

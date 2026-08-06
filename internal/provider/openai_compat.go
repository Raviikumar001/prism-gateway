package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream,omitempty"`
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
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
	Raw   json.RawMessage `json:"-"`
}

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
	return &OpenAICompat{
		Name:         name,
		BaseURL:      baseURL,
		APIKey:       apiKey,
		ExtraHeaders: extraHeaders,
		HTTPClient: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 20,
				IdleConnTimeout:     90 * time.Second,
			},
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
	req.Stream = false
	payload, err := json.Marshal(req)
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
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

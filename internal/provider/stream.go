package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// StreamEvent is one SSE data payload (without the "data: " prefix).
type StreamEvent struct {
	Data []byte
	Done bool
}

// ChatCompletionStream starts an upstream SSE stream. On success the caller must
// drain/close resp.Body. HTTP error responses are returned as UpstreamError.
func (c *OpenAICompat) ChatCompletionStream(ctx context.Context, req ChatRequest) (*http.Response, error) {
	req.Stream = true
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 0, Transport: c.HTTPClient.Transport}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		msg := string(body)
		var parsed struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &parsed) == nil && parsed.Error.Message != "" {
			msg = parsed.Error.Message
		}
		return nil, &UpstreamError{
			StatusCode: resp.StatusCode,
			Body:       body,
			Message:    msg,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}
	}
	return resp, nil
}

func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	var sec int
	if _, err := fmt.Sscanf(v, "%d", &sec); err != nil || sec <= 0 {
		return 0
	}
	d := time.Duration(sec) * time.Second
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	return d
}

// ReadSSE reads OpenAI-style SSE until [DONE], ctx cancel, or read error.
func ReadSSE(ctx context.Context, body io.ReadCloser, onEvent func(StreamEvent) error) (Usage, error) {
	var usage Usage
	defer body.Close()

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = body.Close()
		case <-done:
		}
	}()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return usage, err
		}
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			if err := onEvent(StreamEvent{Done: true}); err != nil {
				return usage, err
			}
			return usage, nil
		}

		var partial struct {
			Usage *Usage `json:"usage"`
		}
		if json.Unmarshal([]byte(data), &partial) == nil && partial.Usage != nil {
			usage = *partial.Usage
		}
		if err := onEvent(StreamEvent{Data: []byte(data)}); err != nil {
			return usage, err
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return usage, ctx.Err()
		}
		if errors.Is(err, io.ErrClosedPipe) {
			return usage, ctx.Err()
		}
		return usage, err
	}
	return usage, nil
}

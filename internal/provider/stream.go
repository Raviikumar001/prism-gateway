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

var (
	ErrStreamIdleTimeout = errors.New("upstream stream idle timeout")
	ErrStreamMissingDone = errors.New("upstream stream ended without [DONE]")
)

// StreamEvent is one SSE data payload (without the "data: " prefix).
type StreamEvent struct {
	Data []byte
	Done bool
}

// ChatCompletionStream starts an upstream SSE stream. On success the caller must
// drain/close resp.Body. HTTP error responses are returned as UpstreamError.
func (c *OpenAICompat) ChatCompletionStream(ctx context.Context, req ChatRequest) (*http.Response, error) {
	payload, err := req.Payload(req.Model, true)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	c.applyHeaders(httpReq)
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

// ReadSSE reads OpenAI-style SSE until [DONE], ctx cancel, read error, or an
// inactivity timeout. The timeout is reset whenever the upstream sends a line.
func ReadSSE(ctx context.Context, body io.ReadCloser, idleTimeout time.Duration, onEvent func(StreamEvent) error) (Usage, error) {
	var usage Usage
	defer body.Close()

	type scanResult struct {
		line string
		err  error
		eof  bool
	}
	results := make(chan scanResult, 1)
	stop := make(chan struct{})
	defer close(stop)

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	go func() {
		for scanner.Scan() {
			select {
			case results <- scanResult{line: scanner.Text()}:
			case <-stop:
				return
			}
		}
		select {
		case results <- scanResult{err: scanner.Err(), eof: true}:
		case <-stop:
		}
	}()

	var timer *time.Timer
	var idle <-chan time.Time
	if idleTimeout > 0 {
		timer = time.NewTimer(idleTimeout)
		idle = timer.C
		defer timer.Stop()
	}
	resetIdle := func() {
		if timer == nil {
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(idleTimeout)
	}

	for {
		var result scanResult
		select {
		case <-ctx.Done():
			_ = body.Close()
			return usage, ctx.Err()
		case <-idle:
			_ = body.Close()
			return usage, ErrStreamIdleTimeout
		case result = <-results:
			resetIdle()
		}

		if result.eof {
			if result.err != nil {
				if ctx.Err() != nil {
					return usage, ctx.Err()
				}
				if errors.Is(result.err, io.ErrClosedPipe) {
					return usage, ctx.Err()
				}
				return usage, result.err
			}
			return usage, ErrStreamMissingDone
		}

		line := result.line
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
}

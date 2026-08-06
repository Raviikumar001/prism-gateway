package provider

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestReadSSEPreservesToolCallDeltasAndUsage(t *testing.T) {
	body := io.NopCloser(strings.NewReader(
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\\\\\"path\\\\\\\":\"}}]}}]}\n\n" +
			"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":4,\"total_tokens\":14}}\n\n" +
			"data: [DONE]\n\n",
	))
	var events []StreamEvent
	usage, err := ReadSSE(context.Background(), body, time.Second, func(event StreamEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || !events[2].Done {
		t.Fatalf("events = %#v", events)
	}
	if !strings.Contains(string(events[0].Data), "tool_calls") {
		t.Fatalf("tool-call delta was not preserved: %s", events[0].Data)
	}
	if usage.PromptTokens != 10 || usage.CompletionTokens != 4 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestReadSSEReturnsErrorWithoutDone(t *testing.T) {
	body := io.NopCloser(strings.NewReader("data: {\"choices\":[]}\n\n"))
	_, err := ReadSSE(context.Background(), body, time.Second, func(StreamEvent) error { return nil })
	if !errors.Is(err, ErrStreamMissingDone) {
		t.Fatalf("expected missing DONE error, got %v", err)
	}
}

func TestReadSSEEnforcesIdleTimeout(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	_, err := ReadSSE(context.Background(), reader, 20*time.Millisecond, func(StreamEvent) error { return nil })
	if !errors.Is(err, ErrStreamIdleTimeout) {
		t.Fatalf("expected idle timeout, got %v", err)
	}
}

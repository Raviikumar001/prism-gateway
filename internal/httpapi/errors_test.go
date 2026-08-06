package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/raviikumar001/prism-gateway/internal/provider"
)

func TestWriteUpstreamClientErrorPreservesOpenAIError(t *testing.T) {
	recorder := httptest.NewRecorder()
	body := `{"error":{"type":"invalid_request_error","message":"bad tool schema"}}`
	writeUpstreamClientError(recorder, &provider.UpstreamError{
		StatusCode: http.StatusBadRequest,
		Body:       []byte(body),
		Message:    "bad tool schema",
	})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", recorder.Code)
	}
	if strings.TrimSpace(recorder.Body.String()) != body {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestResponseModelUsesCachedFallbackModel(t *testing.T) {
	if got := responseModel([]byte(`{"model":"fallback-model"}`), "primary-model"); got != "fallback-model" {
		t.Fatalf("model = %q", got)
	}
}

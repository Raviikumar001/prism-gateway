package consoleui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestConsoleAPIRouteWinsOverStatic(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/console/api/overview", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("overview"))
	})
	Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/console/api/overview", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Body)
	if rec.Code != 200 || string(body) != "overview" {
		t.Fatalf("api route lost to static: status=%d body=%q", rec.Code, body)
	}
}

func TestConsoleIndexServesHTML(t *testing.T) {
	r := chi.NewRouter()
	Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/console/", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	html := string(body)
	if !strings.Contains(html, "Gateway overview") {
		t.Fatalf("index missing title, body starts %q", html[:min(80, len(html))])
	}
	if strings.Contains(html, "admin-token") || strings.Contains(html, "virtual-key") {
		t.Fatal("console still asks for tokens")
	}
}

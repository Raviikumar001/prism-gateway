package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/raviikumar001/prism-gateway/internal/auth"
	"github.com/raviikumar001/prism-gateway/internal/config"
	"github.com/raviikumar001/prism-gateway/internal/meter"
	"github.com/raviikumar001/prism-gateway/internal/provider"
	"github.com/raviikumar001/prism-gateway/internal/route"
)

type Server struct {
	cfg       *config.Config
	db        *pgxpool.Pool
	rdb       *redis.Client
	auth      *auth.Service
	resolver  *route.Resolver
	providers *provider.Registry
	meter     *meter.Service
}

func NewServer(
	cfg *config.Config,
	db *pgxpool.Pool,
	rdb *redis.Client,
	authSvc *auth.Service,
	resolver *route.Resolver,
	providers *provider.Registry,
	meterSvc *meter.Service,
) *Server {
	return &Server{
		cfg:       cfg,
		db:        db,
		rdb:       rdb,
		auth:      authSvc,
		resolver:  resolver,
		providers: providers,
		meter:     meterSvc,
	}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(120 * time.Second))

	r.Get("/health", s.handleHealth)
	r.Post("/v1/chat/completions", s.handleChatCompletions)

	return r
}

type healthResponse struct {
	Status   string            `json:"status"`
	Checks   map[string]string `json:"checks"`
	Provider string            `json:"provider_mode"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	checks := map[string]string{}
	ok := true

	if err := s.db.Ping(ctx); err != nil {
		checks["postgres"] = "down"
		ok = false
	} else {
		checks["postgres"] = "up"
	}

	if err := s.rdb.Ping(ctx).Err(); err != nil {
		checks["redis"] = "down"
		ok = false
	} else {
		checks["redis"] = "up"
	}

	status := "ok"
	code := http.StatusOK
	if !ok {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}

	writeJSON(w, code, healthResponse{
		Status:   status,
		Checks:   checks,
		Provider: s.cfg.ProviderMode,
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/raviikumar001/prism-gateway/internal/auth"
)

type modelObject struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.modelTenant(w, r)
	if !ok {
		return
	}

	models := make([]modelObject, 0)
	for _, model := range s.resolver.Models() {
		if tenant.AllowsModel(model.ID) {
			models = append(models, modelObject{
				ID:      model.ID,
				Object:  "model",
				OwnedBy: model.OwnedBy,
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   models,
	})
}

func (s *Server) handleModel(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.modelTenant(w, r)
	if !ok {
		return
	}

	id := strings.TrimPrefix(chi.URLParam(r, "*"), "/")
	if decoded, err := url.PathUnescape(id); err == nil {
		id = decoded
	}
	model, exists := s.resolver.Model(id)
	if !exists || !tenant.AllowsModel(id) {
		writeAPIError(w, http.StatusNotFound, "not_found_error", "Model not found")
		return
	}
	writeJSON(w, http.StatusOK, modelObject{
		ID:      model.ID,
		Object:  "model",
		OwnedBy: model.OwnedBy,
	})
}

func (s *Server) modelTenant(w http.ResponseWriter, r *http.Request) (*auth.Tenant, bool) {
	token, err := auth.BearerToken(r.Header.Get("Authorization"))
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "authentication_error", "Missing or invalid Authorization bearer token")
		return nil, false
	}
	tenant, err := s.auth.Lookup(r.Context(), token)
	if errors.Is(err, auth.ErrInvalidKey) || errors.Is(err, auth.ErrDisabled) {
		writeAPIError(w, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return nil, false
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "server_error", "Internal error")
		return nil, false
	}
	return tenant, true
}

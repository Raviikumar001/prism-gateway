package consoleui

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
)

//go:embed static/*
var staticFS embed.FS

// Mount registers the ops console under /console/.
func Mount(r chi.Router) {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	r.Get("/console", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/console/", http.StatusFound)
	})
	r.Handle("/console/*", http.StripPrefix("/console/", fileServer))
}

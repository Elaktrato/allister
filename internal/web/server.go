// Package web contains the HTTP server, templates and static assets.
package web

import (
	"embed"
	"html/template"
	"log"
	"net/http"
)

// app.css is built by the Tailwind stage in the Dockerfile and is not committed.
//
//go:embed static
var staticFS embed.FS

//go:embed templates
var templateFS embed.FS

// Deps holds server dependencies; it grows in later tasks.
type Deps struct{}

type server struct {
	deps Deps
	tmpl *template.Template
}

func NewServer(deps Deps) http.Handler {
	s := &server{
		deps: deps,
		tmpl: template.Must(template.ParseFS(templateFS, "templates/*.html")),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.Handle("GET /static/", http.FileServerFS(staticFS))
	mux.HandleFunc("GET /{$}", s.index)
	return mux
}

func (s *server) index(w http.ResponseWriter, r *http.Request) {
	if err := s.tmpl.ExecuteTemplate(w, "base.html", nil); err != nil {
		log.Printf("render: %v", err)
	}
}

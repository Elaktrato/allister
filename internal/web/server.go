// Package web contains the HTTP server, templates and static assets.
package web

import (
	"bytes"
	"embed"
	"html/template"
	"log"
	"net/http"

	"github.com/allister/allister/internal/settings"
)

// app.css is built by the Tailwind stage in the Dockerfile and is not committed.
//
//go:embed static
var staticFS embed.FS

//go:embed templates
var templateFS embed.FS

// Deps holds server dependencies; it grows in later tasks.
type Deps struct {
	Settings *settings.Store
}

type server struct {
	deps  Deps
	pages map[string]*template.Template
}

// pageNames lists every page template (templates/<name>.html). Add a page by
// dropping its file in templates/ and listing it here.
var pageNames = []string{"index", "settings"}

// parsePages builds one template set per page, each cloned from base.html plus
// the page file, so pages can all define "title" and "content" without colliding.
func parsePages() map[string]*template.Template {
	base := template.Must(template.ParseFS(templateFS, "templates/base.html"))
	pages := make(map[string]*template.Template, len(pageNames))
	for _, name := range pageNames {
		t := template.Must(base.Clone())
		pages[name] = template.Must(t.ParseFS(templateFS, "templates/"+name+".html"))
	}
	return pages
}

func NewServer(deps Deps) http.Handler {
	s := &server{deps: deps, pages: parsePages()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.Handle("GET /static/", http.FileServerFS(staticFS))
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /settings", s.getSettings)
	mux.HandleFunc("POST /settings", s.postSettings)
	return mux
}

// render executes the page into a buffer first so a template error yields a
// clean 500 instead of a half-written response.
func (s *server) render(w http.ResponseWriter, r *http.Request, page string, data any) {
	t, ok := s.pages[page]
	if !ok {
		log.Printf("render: unknown page %q", page)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "base.html", data); err != nil {
		log.Printf("render %s: %v", page, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}

func (s *server) index(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "index", nil)
}

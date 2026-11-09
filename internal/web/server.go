// Package web contains the HTTP server, templates and static assets.
package web

import (
	"bytes"
	"context"
	"embed"
	"html/template"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/allister/allister/internal/provider"
	"github.com/allister/allister/internal/settings"
	"github.com/allister/allister/internal/store"
)

// app.css is built by the Tailwind stage in the Dockerfile and is not committed.
//
//go:embed static
var staticFS embed.FS

//go:embed templates
var templateFS embed.FS

// Deps holds server dependencies; it grows in later tasks.
type Deps struct {
	Settings  *settings.Store
	Store     *store.Store
	Providers map[provider.Source]provider.Provider
	// Jobs enqueues background jobs (see package jobs).
	Jobs JobQueue
	// CoversDir is DATA_DIR/covers; when empty /covers/ is not served.
	CoversDir string
}

// JobQueue is the part of jobs.Queue the web layer needs.
type JobQueue interface {
	Enqueue(ctx context.Context, kind string, payload any) error
}

type server struct {
	deps  Deps
	pages map[string]*template.Template
	cache *animeCache
}

// pageNames lists every page template (templates/<name>.html). Add a page by
// dropping its file in templates/ and listing it here.
var pageNames = []string{"index", "settings", "search", "anime", "error"}

// pagePartials lists extra template files a page needs (also rendered alone
// for htmx requests).
var pagePartials = map[string][]string{
	"search": {"templates/search_results.html"},
	"anime":  {"templates/add_dialog.html"},
}

// parsePages builds one template set per page, each cloned from base.html plus
// the page file, so pages can all define "title" and "content" without colliding.
func parsePages() map[string]*template.Template {
	base := template.Must(template.New("base.html").Funcs(templateFuncs).ParseFS(templateFS, "templates/base.html"))
	pages := make(map[string]*template.Template, len(pageNames))
	for _, name := range pageNames {
		t := template.Must(base.Clone())
		files := append([]string{"templates/" + name + ".html"}, pagePartials[name]...)
		pages[name] = template.Must(t.ParseFS(templateFS, files...))
	}
	return pages
}

func NewServer(deps Deps) http.Handler {
	_, h := newServer(deps, time.Now)
	return h
}

// newServer is NewServer with an injectable clock (used by the overview cache).
func newServer(deps Deps, now func() time.Time) (*server, http.Handler) {
	s := &server{deps: deps, pages: parsePages(), cache: newAnimeCache(now, cacheMaxEntries, cacheTTL)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.Handle("GET /static/", http.FileServerFS(staticFS))
	if deps.CoversDir != "" {
		mux.Handle("GET /covers/", coversHandler(deps.CoversDir))
	}
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /settings", s.getSettings)
	mux.HandleFunc("POST /settings", s.postSettings)
	mux.HandleFunc("GET /anime/search", s.search)
	mux.HandleFunc("GET /anime/{source}/{id}", s.getOverview)
	mux.HandleFunc("GET /anime/{source}/{id}/add", s.getAdd)
	mux.HandleFunc("POST /anime/{source}/{id}/add", s.postAdd)
	return s, mux
}

// isPartialRequest reports whether r is an htmx request that wants only a
// fragment. htmx also sends HX-Request on a history cache miss (together with
// HX-History-Restore-Request) and then swaps the response into the body, so
// that case needs the full page.
func isPartialRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true" &&
		r.Header.Get("HX-History-Restore-Request") != "true"
}

// render executes the page into a buffer first so a template error yields a
// clean 500 instead of a half-written response.
func (s *server) render(w http.ResponseWriter, r *http.Request, page string, data any) {
	s.execute(w, http.StatusOK, page, "base.html", data)
}

// renderStatus is render with an explicit success-path status code.
func (s *server) renderStatus(w http.ResponseWriter, status int, page string, data any) {
	s.execute(w, status, page, "base.html", data)
}

// renderPartial executes a named template of a page's set without the base layout.
func (s *server) renderPartial(w http.ResponseWriter, page, name string, data any) {
	s.execute(w, http.StatusOK, page, name, data)
}

// renderPartialStatus is renderPartial with an explicit status code.
func (s *server) renderPartialStatus(w http.ResponseWriter, status int, page, name string, data any) {
	s.execute(w, status, page, name, data)
}

// execute buffers the named template of a page's set. Responses vary by
// HX-Request because the same URL can answer as a partial or a full page.
func (s *server) execute(w http.ResponseWriter, status int, page, name string, data any) {
	w.Header().Add("Vary", "HX-Request")
	t, ok := s.pages[page]
	if !ok {
		log.Printf("render: unknown page %q", page)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("render %s/%s: %v", page, name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

func (s *server) index(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "index", nil)
}

// coversHandler serves files (never directory listings) from dir. Names are
// content hashes, so responses are cacheable forever. http.Dir rejects ".."
// path elements, and the mux cleans paths before routing.
func coversHandler(dir string) http.Handler {
	fs := http.StripPrefix("/covers/", http.FileServer(noDirFS{http.Dir(dir)}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if name := strings.TrimPrefix(r.URL.Path, "/covers/"); name == "" || strings.HasSuffix(name, "/") {
			http.NotFound(w, r)
			return
		}
		fs.ServeHTTP(&coverWriter{ResponseWriter: w}, r)
	})
}

// coverWriter adds the immutable cache header to successful responses only.
type coverWriter struct {
	http.ResponseWriter
	wrote bool
}

func (c *coverWriter) WriteHeader(code int) {
	if !c.wrote {
		c.wrote = true
		if code == http.StatusOK || code == http.StatusNotModified {
			c.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *coverWriter) Write(b []byte) (int, error) {
	if !c.wrote {
		c.WriteHeader(http.StatusOK)
	}
	return c.ResponseWriter.Write(b)
}

// noDirFS hides directories, so neither listings nor redirects to them occur.
type noDirFS struct{ fs http.FileSystem }

func (n noDirFS) Open(name string) (http.File, error) {
	f, err := n.fs.Open(name)
	if err != nil {
		return nil, err
	}
	if fi, err := f.Stat(); err != nil || fi.IsDir() {
		f.Close()
		return nil, os.ErrNotExist
	}
	return f, nil
}

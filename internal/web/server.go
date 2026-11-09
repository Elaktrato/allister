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

	"github.com/allister/allister/internal/importer/yamtrack"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
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
	// Imports runs the Yamtrack import; when nil the import pages answer 404.
	Imports *yamtrack.Importer
	// ParseAnimeURL reduces a pasted AniList URL or number to an anime id
	// (anilist.ParseAnimeURL; injected because web must not import it).
	ParseAnimeURL func(string) (id string, ok bool)
	// Registry holds the installed plugins and their enabled flags. Required
	// in production (cmd/allister builds it); when nil NewServer builds a
	// default one with the anime plugin on top of Settings.
	Registry *plugin.Registry
	// Library is the generic library over the registry's manifests. When nil
	// NewServer builds one over Store's database.
	Library *library.Library
}

// JobQueue is the part of jobs.Queue the web layer needs.
type JobQueue interface {
	Enqueue(ctx context.Context, kind string, payload any) error
}

type server struct {
	deps  Deps
	pages map[string]*template.Template
	cache *animeCache
	// pluginRoutes lists the full patterns mounted per plugin id, and
	// coreGuarded the core routes behind the anime guard (tests walk them).
	pluginRoutes map[string][]string
	coreGuarded  []string
}

// pageNames lists every page template (templates/<name>.html). Add a page by
// dropping its file in templates/ and listing it here.
var pageNames = []string{"settings", "search", "item", "list", "person", "manual_form", "import", "error"}

// pagePartials lists extra template files a page needs (also rendered alone
// for htmx requests).
var pagePartials = map[string][]string{
	"search":   {"templates/search_results.html"},
	"settings": {"templates/settings_plugins.html"},
	"item":     {"templates/sections.html", "templates/add_dialog.html", "templates/match_dialog.html"},
	"list":     {"templates/list_row.html", "templates/add_dialog.html"},
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
	if deps.Registry == nil {
		deps.Registry = defaultRegistry(deps)
	}
	if deps.Library == nil && deps.Store != nil {
		deps.Library = library.New(deps.Store.DB(), deps.Registry)
	}
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
	mux.HandleFunc("GET /settings/import", s.getImport)
	mux.HandleFunc("POST /settings/import", s.postImport)
	mux.HandleFunc("POST /settings/import/cancel", s.postImportCancel)
	mux.HandleFunc("POST /settings/import/resume", s.postImportResume)
	mux.HandleFunc("POST /settings/plugins/{id}", s.postPlugin)
	s.mountPlugins(mux)
	return s, refuseCrossOrigin(mux)
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
	s.executeFor(w, r, http.StatusOK, page, "base.html", data)
}

// renderStatus is render with an explicit success-path status code.
func (s *server) renderStatus(w http.ResponseWriter, r *http.Request, status int, page string, data any) {
	s.executeFor(w, r, status, page, "base.html", data)
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
	s.executeFor(w, nil, status, page, name, data)
}

// executeFor is execute for a request: a full page (base.html) gets the
// navigation prepared for r. A nil r renders without navigation.
func (s *server) executeFor(w http.ResponseWriter, r *http.Request, status int, page, name string, data any) {
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
	out := buf.Bytes()
	if r != nil && name == "base.html" {
		var nav bytes.Buffer
		if err := navTemplate.ExecuteTemplate(&nav, "nav.html", s.navItems(r)); err != nil {
			log.Printf("render nav: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		out = bytes.Replace(out, []byte(navMarker), nav.Bytes(), 1)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write(out)
}

// index redirects the home page to the first enabled plugin's list, or to
// Settings when no plugin is enabled.
func (s *server) index(w http.ResponseWriter, r *http.Request) {
	for _, m := range s.deps.Registry.Manifests() {
		if s.deps.Registry.IsEnabled(m.ID) {
			http.Redirect(w, r, "/"+m.ID, http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
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

// coverWriter adds X-Content-Type-Options: nosniff to every response and the
// immutable cache header to successful ones only.
type coverWriter struct {
	http.ResponseWriter
	wrote bool
}

func (c *coverWriter) WriteHeader(code int) {
	if !c.wrote {
		c.wrote = true
		c.Header().Set("X-Content-Type-Options", "nosniff")
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

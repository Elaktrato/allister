// Package web contains the HTTP server, templates and static assets.
package web

import (
	"bytes"
	"context"
	"embed"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/allister/allister/internal/appearance"
	"github.com/allister/allister/internal/backup"
	"github.com/allister/allister/internal/importer"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
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
	// Jobs enqueues background jobs (see package jobs).
	Jobs JobQueue
	// SyncJobs maps a plugin id to the sync job of a newly saved item of
	// that plugin (kind and payload for Jobs.Enqueue), so the web layer
	// never knows the job kinds of plugins. A plugin without an entry gets no
	// sync job. cmd/allister wires the generic sync job (jobs.SyncJobFor)
	// for every installed plugin.
	SyncJobs map[string]func(itemID int64) (kind string, payload any)
	// CoversDir is DATA_DIR/covers; when empty /covers/ is not served.
	CoversDir string
	// DataDir is DATA_DIR: a backup restore spools its upload to
	// DATA_DIR/tmp. When empty the restore form is not offered.
	DataDir string
	// Imports runs the imports of every plugin's importers (/settings/import);
	// when nil the import pages answer 404.
	Imports *importer.Runner
	// Registry holds the installed plugins and their enabled flags. Required
	// in production (cmd/allister builds it and registers the plugins); when
	// nil NewServer uses an empty registry over Settings (no plugin pages).
	Registry *plugin.Registry
	// Library is the generic library over the registry's manifests. Required
	// for every page that reads or writes items (cmd/allister builds it over
	// the database and Registry).
	Library *library.Library
	// Appearance holds the appearance settings and renders /appearance.css;
	// when nil NewServer uses one over Settings without uploaded fonts.
	Appearance *appearance.Service
}

// JobQueue is the part of jobs.Queue the web layer needs.
type JobQueue interface {
	Enqueue(ctx context.Context, kind string, payload any) error
}

type server struct {
	deps  Deps
	pages map[string]*template.Template
	cache *detailCache
	// recs caches recommendation results; recTimeout bounds one source
	// call (tests shorten it).
	recs       *recCache
	recTimeout time.Duration
	// pluginRoutes lists the full patterns mounted per plugin id, and
	// coreGuarded the entry routes behind the item's plugin guard (tests
	// walk them).
	pluginRoutes map[string][]string
	coreGuarded  []string
	// now is the server's clock (export file names, caches).
	now func() time.Time
	// restoreMu allows one backup restore at a time; maxBackupBody caps its
	// upload; spoolHook (tests) sees the spooled temp file.
	restoreMu     sync.Mutex
	maxBackupBody int64
	spoolHook     func(path string)
	// backupWrite produces the download (backup.Write; tests replace it).
	backupWrite func(ctx context.Context, w io.Writer, lib *library.Library, plugins []backup.PluginVersion, coversDir string, now time.Time) error
	// appearance is Deps.Appearance (or the default one).
	appearance *appearance.Service
}

// pageNames lists every page template (templates/<name>.html). Add a page by
// dropping its file in templates/ and listing it here.
var pageNames = []string{"settings", "settings_appearance", "settings_plugins_page", "settings_plugin", "search", "item", "list", "person", "manual_form", "details_form", "import", "error", "recommendations", "backup"}

// pagePartials lists extra template files a page needs (also rendered alone
// for htmx requests).
var pagePartials = map[string][]string{
	"search":                {"templates/search_results.html"},
	"settings_plugins_page": {"templates/settings_plugins.html"},
	"item":                  {"templates/sections.html", "templates/add_dialog.html", "templates/progress_inputs.html", "templates/match_dialog.html"},
	"list":                  {"templates/list_row.html", "templates/filter_bar.html", "templates/entry_dialog.html", "templates/progress_inputs.html"},
	"manual_form":           {"templates/item_inputs.html", "templates/progress_inputs.html"},
	"details_form":          {"templates/item_inputs.html"},
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
		deps.Registry = emptyRegistry(deps)
	}
	if deps.Appearance == nil {
		var st appearance.Store
		if deps.Settings != nil {
			st = deps.Settings
		}
		deps.Appearance = appearance.NewService(st)
	}
	s := &server{deps: deps, pages: parsePages(), cache: newDetailCache(now, cacheMaxEntries, cacheTTL),
		recs: newRecCache(now, recCacheMaxEntries, cacheTTL), recTimeout: recTimeout, now: now, maxBackupBody: maxBackupBody, backupWrite: backup.Write,
		appearance: deps.Appearance}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.Handle("GET /static/", staticHandler())
	if deps.CoversDir != "" {
		mux.Handle("GET /covers/", coversHandler(deps.CoversDir))
	}
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /settings", s.getSettings)
	mux.HandleFunc("GET /settings/import", s.getImport)
	mux.HandleFunc("POST /settings/import", s.postImport)
	mux.HandleFunc("POST /settings/import/cancel", s.postImportCancel)
	mux.HandleFunc("POST /settings/import/resume", s.postImportResume)
	mux.HandleFunc("GET /settings/plugins", s.getPlugins)
	mux.HandleFunc("POST /settings/plugins/{id}", s.postPlugin)
	mux.HandleFunc("GET /settings/plugins/{id}", s.getPluginSettings)
	mux.HandleFunc("POST /settings/plugins/{id}/title-language", s.postTitleLanguage)
	mux.HandleFunc("POST /settings/plugins/{id}/columns", s.postColumns)
	mux.HandleFunc("GET /settings/backup", s.getBackup)
	mux.HandleFunc("GET /settings/backup/download", s.getBackupDownload)
	mux.HandleFunc("POST /settings/backup/restore", s.postBackupRestore)
	s.mountPlugins(mux)
	s.mountAppearance(mux)
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
	if w.Header().Get("Vary") == "" { // a handler may have set a wider one
		w.Header().Set("Vary", "HX-Request")
	}
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
		var err error
		if out, err = s.fillMarkers(r, out); err != nil {
			log.Printf("render nav: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write(out)
}

// fillMarkers replaces the markers of a full page with what is prepared per
// request: the appearance stylesheet link (appearanceMarker), the sidebar
// (navMarker) and, on a settings page, the settings section tabs
// (settingsTabsMarker).
func (s *server) fillMarkers(r *http.Request, out []byte) ([]byte, error) {
	out = bytes.Replace(out, []byte(appearanceMarker), s.appearanceLink(r.Context()), 1)
	var nav bytes.Buffer
	if err := navTemplate.ExecuteTemplate(&nav, "nav.html", s.navItems(r)); err != nil {
		return nil, err
	}
	out = bytes.Replace(out, []byte(navMarker), nav.Bytes(), 1)
	if bytes.Contains(out, []byte(settingsTabsMarker)) {
		var tabs bytes.Buffer
		if err := settingsTabsTemplate.ExecuteTemplate(&tabs, "settings_tabs.html", s.settingsTabs(r)); err != nil {
			return nil, err
		}
		out = bytes.Replace(out, []byte(settingsTabsMarker), tabs.Bytes(), 1)
	}
	return out, nil
}

// index redirects the home page to the first enabled plugin's list, or to
// Settings > Plugins (where one is switched on) when no plugin is enabled.
func (s *server) index(w http.ResponseWriter, r *http.Request) {
	for _, m := range s.deps.Registry.Manifests() {
		if s.deps.Registry.IsEnabled(m.ID) {
			http.Redirect(w, r, "/"+m.ID, http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, "/settings/plugins", http.StatusSeeOther)
}

// staticHandler serves the embedded static files. A font gets its MIME type
// set explicitly (the runtime image has no mime.types) and, because its file
// name carries its version, a year-long immutable cache and nosniff. Other
// files (app.css, htmx.min.js) keep their names across versions and are
// served without a cache header.
func staticHandler() http.Handler {
	fs := http.FileServerFS(staticFS)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".woff2") {
			w = &fontWriter{ResponseWriter: w}
		}
		fs.ServeHTTP(w, r)
	})
}

// fontWriter sets the font headers on successful responses only (a 404 stays
// uncached).
type fontWriter struct {
	http.ResponseWriter
	wrote bool
}

func (f *fontWriter) WriteHeader(code int) {
	if !f.wrote {
		f.wrote = true
		if code == http.StatusOK || code == http.StatusPartialContent || code == http.StatusNotModified {
			f.Header().Set("Content-Type", "font/woff2")
			f.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			f.Header().Set("X-Content-Type-Options", "nosniff")
		}
	}
	f.ResponseWriter.WriteHeader(code)
}

func (f *fontWriter) Write(b []byte) (int, error) {
	if !f.wrote {
		f.WriteHeader(http.StatusOK)
	}
	return f.ResponseWriter.Write(b)
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

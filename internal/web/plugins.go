package web

import (
	"context"
	"errors"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// routeRegistrar is what a routeSet registers its handlers with. Patterns are
// "METHOD /path" with the path relative to the plugin's prefix: "/" is the
// prefix itself ("/book"), "/search" is "/book/search".
type routeRegistrar interface {
	Handle(pattern string, h http.HandlerFunc)
}

// routeSet registers one plugin's page handlers. The server mounts it under
// the literal prefix "/{plugin-id}" and wraps every handler in the plugin's
// enabled guard, so a route cannot be added without being guarded.
type routeSet func(reg routeRegistrar)

// entryRoutes registers the list entry routes. They are core routes outside
// every plugin prefix; the server mounts them behind itemPluginGuard, so the
// plugin of the item {id} decides whether they answer.
func (s *server) entryRoutes(reg routeRegistrar) {
	reg.Handle("POST /list/{id}/increment", s.postIncrement)
	reg.Handle("GET /list/{id}/edit", s.getEdit)
	reg.Handle("POST /list/{id}/edit", s.postEdit)
	reg.Handle("POST /list/{id}/remove", s.postRemove)
}

// muxRegistrar mounts relative patterns on a mux behind a guard.
type muxRegistrar struct {
	mux    *http.ServeMux
	prefix string // "" for core routes
	guard  func(http.HandlerFunc) http.Handler
	routes *[]string // full patterns, recorded for tests and diagnostics
}

func (m muxRegistrar) Handle(pattern string, h http.HandlerFunc) {
	method, path, ok := strings.Cut(pattern, " ")
	if !ok || !strings.HasPrefix(path, "/") {
		panic("web: bad route pattern " + pattern)
	}
	if m.prefix != "" {
		if path == "/" {
			path = ""
		}
		path = m.prefix + path
	}
	full := method + " " + path
	*m.routes = append(*m.routes, full)
	m.mux.Handle(full, m.guard(h))
}

// pluginGuard wraps handlers so that they answer the 404 page unless plugin id
// is enabled at request time. It runs before the handler for every method, so
// a disabled plugin's POST mutates nothing. The registry is asked once per
// request (one settings read); nothing is cached, which is fine for a
// single-user app.
func (s *server) pluginGuard(id string) func(http.HandlerFunc) http.Handler {
	return func(h http.HandlerFunc) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !s.deps.Registry.IsEnabled(id) {
				s.errorPage(w, r, http.StatusNotFound, "Not found", "This page does not exist or its plugin is disabled.", "")
				return
			}
			h(w, r)
		})
	}
}

// entryCtxKey keys the entryRef that itemPluginGuard stores in the request
// context.
type entryCtxKey struct{}

// entryRef is the item of an entry route as resolved by itemPluginGuard.
type entryRef struct {
	ID       int64
	PluginID string
}

// itemPluginGuard wraps an entry route (/list/{id}/...): it parses {id}
// (ASCII digits only), looks up the plugin of that item and answers the 404
// page unless the item exists and its plugin is installed and enabled. It
// runs before the handler reads the body or mutates anything; the handler
// finds the resolved item with entryOf.
func (s *server) itemPluginGuard(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.PathValue("id")
		var id int64
		var err error
		if isASCIIDigits(raw, maxIDDigits) {
			id, err = strconv.ParseInt(raw, 10, 64)
		}
		if id <= 0 || err != nil {
			s.entryNotFound(w, r)
			return
		}
		pluginID, err := s.deps.Library.ItemPluginID(r.Context(), id)
		if errors.Is(err, library.ErrNoEntry) {
			s.entryNotFound(w, r)
			return
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		if _, ok := s.deps.Registry.Manifest(pluginID); !ok || !s.deps.Registry.IsEnabled(pluginID) {
			s.entryNotFound(w, r)
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), entryCtxKey{}, entryRef{ID: id, PluginID: pluginID})))
	})
}

// entryOf returns the item resolved by itemPluginGuard.
func entryOf(r *http.Request) entryRef {
	ref, _ := r.Context().Value(entryCtxKey{}).(entryRef)
	return ref
}

func (s *server) entryNotFound(w http.ResponseWriter, r *http.Request) {
	s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such entry in your list.", "")
}

// internalError logs err and answers 500.
func (s *server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// mountPlugins registers the routes of every installed plugin (coreRoutes)
// under the literal prefix /{plugin-id} (never a wildcard
// first segment: the mux would panic on overlap with the core routes), the
// entry routes behind the item's plugin guard and the person page. The
// registry has already refused ids that collide with core first segments.
//
// The person page is guarded inside its handler by the PERSON's plugin (the
// plugin is only known after the lookup): see getPerson.
func (s *server) mountPlugins(mux *http.ServeMux) {
	s.pluginRoutes = map[string][]string{}
	for _, m := range s.deps.Registry.Manifests() {
		routes := []string{}
		s.coreRoutes(m.ID)(muxRegistrar{mux: mux, prefix: "/" + m.ID, guard: s.pluginGuard(m.ID), routes: &routes})
		s.pluginRoutes[m.ID] = routes
	}
	s.coreGuarded = []string{}
	s.entryRoutes(muxRegistrar{mux: mux, guard: s.itemPluginGuard, routes: &s.coreGuarded})
	mux.HandleFunc("GET /people/{id}", s.getPerson)
}

// emptyRegistry is the registry used when Deps.Registry is nil: no plugin,
// enabled flags in the settings store (when there is one).
func emptyRegistry(deps Deps) *plugin.Registry {
	var store plugin.EnabledStore = defaultEnabled{}
	if deps.Settings != nil {
		store = deps.Settings
	}
	return plugin.NewRegistry(store)
}

// defaultEnabled keeps nothing and answers with the manifest default.
type defaultEnabled struct{}

func (defaultEnabled) IsEnabled(_ context.Context, _ string, def bool) (bool, error) {
	return def, nil
}
func (defaultEnabled) SetEnabled(context.Context, string, bool) error { return nil }

// navItem is one entry of the header navigation.
type navItem struct {
	Label  string
	Href   string
	Active bool
	Right  bool // pushed to the right edge (Settings)
}

// navItems prepares the navigation for r: one item per enabled plugin (its
// first kind's plural label, linking to /{plugin-id}) followed by Settings.
// The active item has a path under its prefix. Labels come from plugin
// manifests and are escaped by the template.
func (s *server) navItems(r *http.Request) []navItem {
	var items []navItem
	for _, m := range s.deps.Registry.Manifests() {
		if !s.deps.Registry.IsEnabled(m.ID) {
			continue
		}
		label := m.Name
		if len(m.Kinds) > 0 && m.Kinds[0].LabelPlural != "" {
			label = m.Kinds[0].LabelPlural
		}
		href := "/" + m.ID
		items = append(items, navItem{Label: label, Href: href, Active: underPrefix(r.URL.Path, href)})
	}
	return append(items, navItem{Label: "Settings", Href: "/settings", Active: underPrefix(r.URL.Path, "/settings"), Right: true})
}

func underPrefix(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// navMarker is emitted by base.html where the navigation goes. execute swaps
// it for the nav rendered from per-request data. html/template drops comments
// from template text, so the marker is produced by a template function.
const navMarker = "<!--allister-nav-->"

var navTemplate = template.Must(template.New("nav.html").ParseFS(templateFS, "templates/nav.html"))

// pluginRow is one installed plugin on the settings page.
type pluginRow struct {
	ID, Name, Version                    string
	Kinds, Sources, Importers, Exporters []string
	Enabled                              bool
	ListURL                              string // set only while enabled
	// ExportLinks links the plugin's exporters, only while it is enabled.
	ExportLinks []exportLink
}

func (s *server) pluginRows() []pluginRow {
	var rows []pluginRow
	for _, m := range s.deps.Registry.Manifests() {
		row := pluginRow{ID: m.ID, Name: m.Name, Version: m.Version, Enabled: s.deps.Registry.IsEnabled(m.ID)}
		for _, k := range m.Kinds {
			row.Kinds = append(row.Kinds, k.Label)
		}
		for _, x := range m.Sources {
			row.Sources = append(row.Sources, x.Name)
		}
		for _, x := range m.Importers {
			row.Importers = append(row.Importers, x.Name)
		}
		for _, x := range m.Exporters {
			row.Exporters = append(row.Exporters, x.Name)
		}
		if row.Enabled {
			row.ListURL = "/" + m.ID
			row.ExportLinks = s.exportLinks(m)
		}
		rows = append(rows, row)
	}
	return rows
}

// postPlugin switches a plugin on or off (POST /settings/plugins/{id}, field
// enabled=1|0). Unknown ids answer 404; the id is only matched against the
// registry, never echoed.
func (s *server) postPlugin(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.deps.Registry.Manifest(id); !ok {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such plugin.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var on bool
	switch r.PostForm.Get("enabled") {
	case "1":
		on = true
	case "0":
	default:
		http.Error(w, "enabled must be 1 or 0", http.StatusBadRequest)
		return
	}
	if err := s.deps.Registry.SetEnabled(r.Context(), id, on); err != nil {
		log.Printf("plugins: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

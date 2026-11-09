package web

import (
	"context"
	"html/template"
	"log"
	"net/http"
	"strings"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anime"
	"github.com/allister/allister/internal/provider"
)

// animePluginID is the id of the built-in anime plugin.
const animePluginID = "anime"

// routeRegistrar is what a routeSet registers its handlers with. Patterns are
// "METHOD /path" with the path relative to the plugin's prefix: "/" is the
// prefix itself ("/anime"), "/search" is "/anime/search".
type routeRegistrar interface {
	Handle(pattern string, h http.HandlerFunc)
}

// routeSet registers one plugin's page handlers. The server mounts it under
// the literal prefix "/{plugin-id}" and wraps every handler in the plugin's
// enabled guard, so a route cannot be added without being guarded.
type routeSet func(reg routeRegistrar)

// routeSets returns the route sets of plugins that have more than the
// generic core routes. Every other plugin (the test fixtures) gets
// coreRoutes only. Later tasks port the anime-only handlers to generic ones
// and delete this override.
func (s *server) routeSets() map[string]routeSet {
	return map[string]routeSet{animePluginID: s.animeRoutes}
}

// animeRoutes is the core route set plus the legacy anime-shaped handlers
// that are not generic yet.
func (s *server) animeRoutes(reg routeRegistrar) {
	s.coreRoutes(animePluginID)(reg)
	reg.Handle("GET /", s.getList)
	reg.Handle("GET /manual/new", s.getManualNew)
	reg.Handle("POST /manual/new", s.postManualNew)
	reg.Handle("GET /{source}/{id}/add", s.getAdd)
	reg.Handle("POST /{source}/{id}/add", s.postAdd)
	reg.Handle("GET /{source}/{id}/match", s.getMatch)
	reg.Handle("POST /{source}/{id}/match", s.postMatch)
	reg.Handle("GET /{source}/{id}/match/search", s.getMatchSearch)
}

// listRoutes registers the list entry routes. They still act on anime items
// only (through the adapter store), so they are guarded by the anime plugin's
// enabled flag.
//
// TEMPORARY COUPLING: Task 9 (generic list routes) makes these plugin-aware
// (the owning plugin of the item decides) and deletes the anime guard here.
func (s *server) listRoutes(reg routeRegistrar) {
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

// mountPlugins registers the routes of every installed plugin (its own set,
// else coreRoutes) under the literal prefix /{plugin-id} (never a wildcard
// first segment: the mux would panic on overlap with the core routes), the
// guarded list routes and the person page. The registry has already refused
// ids that collide with core first segments.
//
// The person page is guarded inside its handler by the PERSON's plugin (the
// plugin is only known after the lookup): see getPerson.
func (s *server) mountPlugins(mux *http.ServeMux) {
	s.pluginRoutes = map[string][]string{}
	sets := s.routeSets()
	for _, m := range s.deps.Registry.Manifests() {
		set, ok := sets[m.ID]
		if !ok {
			set = s.coreRoutes(m.ID)
		}
		routes := []string{}
		set(muxRegistrar{mux: mux, prefix: "/" + m.ID, guard: s.pluginGuard(m.ID), routes: &routes})
		s.pluginRoutes[m.ID] = routes
	}
	s.coreGuarded = []string{}
	s.listRoutes(muxRegistrar{mux: mux, guard: s.pluginGuard(animePluginID), routes: &s.coreGuarded})
	mux.HandleFunc("GET /people/{id}", s.getPerson)
}

// defaultRegistry is the registry used when Deps.Registry is nil: the anime
// plugin on top of the settings store. Its AniList source is the configured
// AniList provider (never a network client of its own); without one every
// source call is unavailable.
func defaultRegistry(deps Deps) *plugin.Registry {
	var store plugin.EnabledStore = defaultEnabled{}
	if deps.Settings != nil {
		store = deps.Settings
	}
	reg := plugin.NewRegistry(store)
	var p provider.Provider = unavailableProvider{}
	if ap := deps.Providers[provider.AniList]; ap != nil {
		p = ap
	}
	if err := reg.Register(anime.New(anime.Options{Provider: p})); err != nil {
		log.Printf("plugins: %v", err)
	}
	return reg
}

// unavailableProvider answers every call with provider.ErrUnavailable.
type unavailableProvider struct{}

func (unavailableProvider) Name() provider.Source { return provider.AniList }
func (unavailableProvider) Search(context.Context, string) ([]provider.SearchResult, error) {
	return nil, errNoProvider
}
func (unavailableProvider) Anime(context.Context, string) (provider.Anime, error) {
	return provider.Anime{}, errNoProvider
}
func (unavailableProvider) Cast(context.Context, string) ([]provider.CastMember, error) {
	return nil, errNoProvider
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

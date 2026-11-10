package web

// The generic, schema-driven item overview: GET /{plugin}/{source}/{id}
// renders a saved item from the library or an unsaved one live from the
// plugin's source. The kind's Sections list decides which building blocks
// appear and in which order; every block is a fixed template in
// sections.html fed with plain view structs built here (all text is escaped
// by html/template; plugins never supply markup).

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

var (
	// errNoSuchItem: no saved item and nothing that can be fetched live.
	errNoSuchItem = errors.New("no such item")
	// errNoSource: the plugin declares the source but does not provide it.
	errNoSource = fmt.Errorf("%w: source not provided by the plugin", plugin.ErrUnavailable)
	// errAmbiguousKind: several saved kinds hold the id and no ?kind= names one.
	errAmbiguousKind = errors.New("several kinds share this id")
)

// manualSource is the source of items typed in by the user; they exist only
// in the library.
const manualSource = "manual"

// maxPathIDBytes is the longest source or item id accepted from a URL.
const maxPathIDBytes = 200

// coreRoutes returns the generic routes every plugin gets (the whole route
// set of every plugin, anime included), mounted under its literal prefix
// /{plugin-id} and behind its enabled guard. Literal segments ("/manual/new")
// win over the {source}/{id} wildcards in the mux.
func (s *server) coreRoutes(pluginID string) routeSet {
	return func(reg routeRegistrar) {
		reg.Handle("GET /", func(w http.ResponseWriter, r *http.Request) { s.getList(w, r, pluginID) })
		reg.Handle("GET /search", func(w http.ResponseWriter, r *http.Request) { s.search(w, r, pluginID) })
		reg.Handle("GET /{source}/{id}", func(w http.ResponseWriter, r *http.Request) { s.getItem(w, r, pluginID) })
		reg.Handle("GET /{source}/{id}/recommendations", func(w http.ResponseWriter, r *http.Request) { s.getRecommendations(w, r, pluginID) })
		reg.Handle("GET /{source}/{id}/add", func(w http.ResponseWriter, r *http.Request) { s.getAdd(w, r, pluginID) })
		reg.Handle("POST /{source}/{id}/add", func(w http.ResponseWriter, r *http.Request) { s.postAdd(w, r, pluginID) })
		reg.Handle("GET /manual/new", func(w http.ResponseWriter, r *http.Request) { s.getManualNew(w, r, pluginID) })
		reg.Handle("POST /manual/new", func(w http.ResponseWriter, r *http.Request) { s.postManualNew(w, r, pluginID) })
		reg.Handle("GET /{source}/{id}/edit", func(w http.ResponseWriter, r *http.Request) { s.getDetails(w, r, pluginID) })
		reg.Handle("POST /{source}/{id}/edit", func(w http.ResponseWriter, r *http.Request) { s.postDetails(w, r, pluginID) })
		reg.Handle("GET /{source}/{id}/match", func(w http.ResponseWriter, r *http.Request) { s.getMatch(w, r, pluginID) })
		reg.Handle("GET /{source}/{id}/match/search", func(w http.ResponseWriter, r *http.Request) { s.getMatchSearch(w, r, pluginID) })
		reg.Handle("POST /{source}/{id}/match", func(w http.ResponseWriter, r *http.Request) { s.postMatch(w, r, pluginID) })
	}
}

// itemRef names the item of a request. Kind is "" when not given.
type itemRef struct{ Plugin, Source, ID, Kind string }

// validPathID accepts a non-empty valid UTF-8 value of at most
// maxPathIDBytes without control characters.
func validPathID(s string) bool {
	if s == "" || len(s) > maxPathIDBytes || !utf8.ValidString(s) {
		return false
	}
	return strings.IndexFunc(s, unicode.IsControl) < 0
}

func kindDef(m plugin.Manifest, key string) (plugin.KindDef, bool) {
	for _, k := range m.Kinds {
		if k.Key == key {
			return k, true
		}
	}
	return plugin.KindDef{}, false
}

func sourceInfo(m plugin.Manifest, id string) (plugin.SourceInfo, bool) {
	for _, si := range m.Sources {
		if si.ID == id {
			return si, true
		}
	}
	return plugin.SourceInfo{}, false
}

func hasKind(m plugin.Manifest, key string) bool { _, ok := kindDef(m, key); return ok }

func hasSource(m plugin.Manifest, id string) bool { _, ok := sourceInfo(m, id); return ok }

func hasSection(k plugin.KindDef, t plugin.SectionType) bool {
	return slices.ContainsFunc(k.Sections, func(d plugin.SectionDef) bool { return d.Type == t })
}

// itemPath is /{plugin}/{source}/{id} with every part path escaped.
func itemPath(pluginID, source, id string) string {
	return "/" + url.PathEscape(pluginID) + "/" + url.PathEscape(source) + "/" + url.PathEscape(id)
}

// itemURL is itemPath plus ?kind= when the plugin has several kinds.
func itemURL(m plugin.Manifest, source, id, kind string) string {
	u := itemPath(m.ID, source, id)
	if len(m.Kinds) > 1 && kind != "" {
		u += "?kind=" + url.QueryEscape(kind)
	}
	return u
}

// source returns the plugin's source implementation, or nil.
func (s *server) source(pluginID, sourceID string) plugin.Source {
	p, ok := s.deps.Registry.Get(pluginID)
	if !ok {
		return nil
	}
	return p.Source(sourceID)
}

func (s *server) getItem(w http.ResponseWriter, r *http.Request, pluginID string) {
	m, ok := s.deps.Registry.Manifest(pluginID)
	ref := itemRef{Plugin: pluginID, Source: r.PathValue("source"), ID: r.PathValue("id"), Kind: r.URL.Query().Get("kind")}
	if !ok || !validPathID(ref.Source) || !validPathID(ref.ID) || (ref.Kind != "" && !hasKind(m, ref.Kind)) {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such item.", "")
		return
	}
	s.showItem(w, r, ref, itemExtras{}, http.StatusOK)
}

// itemExtras are the dialogs an overview can be rendered with (no
// JavaScript fallback of the add and match dialogs). Dialog carries the add
// form's values and messages; the rest of the form is built from the loaded
// item. UseCache lets a fresh cache entry replace the source calls of an
// unsaved item.
type itemExtras struct {
	Dialog   *entryForm
	Match    *matchView
	UseCache bool
}

// showItem renders the overview of ref (with a dialog open when x says so).
func (s *server) showItem(w http.ResponseWriter, r *http.Request, ref itemRef, x itemExtras, status int) {
	m, _ := s.deps.Registry.Manifest(ref.Plugin)
	li, err := s.loadItem(r.Context(), m, ref, x.UseCache)
	if err != nil {
		s.itemFail(w, r, m, ref.Source, err, itemURL(m, ref.Source, ref.ID, ref.Kind))
		return
	}
	v := s.itemView(r.Context(), li)
	if x.Dialog != nil {
		pc := progressContext{Keys: li.kind.Progress.Defaults, Fields: li.data.Fields}
		if li.saved != nil {
			pc.Keys = li.saved.ProgressKeys
		}
		f := newEntryForm(m, li.kind, li.data.Source, li.data.ID, pc, x.Dialog.entryValues)
		f.Error, f.ExistingURL = x.Dialog.Error, x.Dialog.ExistingURL
		if li.entry != nil && f.Error == "" {
			f.Error, f.ExistingURL = alreadyInList(li.kind), itemURL(m, li.data.Source, li.data.ID, li.kind.Key)
		}
		v.Dialog = &f
	}
	v.Match = x.Match
	s.renderStatus(w, r, status, "item", v)
}

// itemFail writes the error page of a load error.
func (s *server) itemFail(w http.ResponseWriter, r *http.Request, m plugin.Manifest, source string, err error, retry string) {
	switch {
	case errors.Is(err, errAmbiguousKind):
		s.errorPage(w, r, http.StatusNotFound, "Not found", "Several kinds share this id: add ?kind=…", "")
	case errors.Is(err, errNoSuchItem), errors.Is(err, plugin.ErrNotFound):
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such item.", "")
	case errors.Is(err, plugin.ErrRateLimited), errors.Is(err, plugin.ErrUnavailable):
		log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
		s.errorPage(w, r, http.StatusServiceUnavailable, "Temporarily unavailable", sourceMessage(m, source, err), retry)
	default:
		log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// sourceMessage is the readable outage message naming the source.
func sourceMessage(m plugin.Manifest, source string, err error) string {
	name := "The source"
	if si, ok := sourceInfo(m, source); ok && si.Name != "" {
		name = si.Name
	}
	if errors.Is(err, plugin.ErrRateLimited) {
		return name + " is rate limiting requests, please wait a moment and try again."
	}
	return name + " is unavailable right now, please try again."
}

// loadedItem is everything the overview shows, saved or live.
type loadedItem struct {
	man           plugin.Manifest
	kind          plugin.KindDef
	data          plugin.ItemData // identity (Source, ID, Kind) always set
	saved         *library.Item   // nil when live
	entry         *library.Entry  // nil when not in the list
	credits       []creditView
	creditsFailed bool
}

// loadItem finds the saved item of ref, else fetches it live. Unsaved manual
// items, undeclared sources and an unknown kind of a plugin with several
// kinds are errNoSuchItem; several saved kinds without ref.Kind are
// errAmbiguousKind.
func (s *server) loadItem(ctx context.Context, m plugin.Manifest, ref itemRef, useCache bool) (*loadedItem, error) {
	it, k, err := s.findSaved(ctx, m, ref)
	if err != nil {
		return nil, err
	}
	if it != nil {
		return s.loadSaved(ctx, m, k, it)
	}
	return s.loadLive(ctx, m, ref, useCache)
}

// findSaved returns the saved item of ref with its kind, or nil when none is
// saved. Without ref.Kind exactly one saved kind may hold the id: several
// are errAmbiguousKind (like an unsaved id of a plugin with several kinds,
// the URL must name the kind).
func (s *server) findSaved(ctx context.Context, m plugin.Manifest, ref itemRef) (*library.Item, plugin.KindDef, error) {
	items, err := s.deps.Library.ItemByPluginSource(ctx, ref.Plugin, ref.Source, ref.ID)
	if err != nil {
		return nil, plugin.KindDef{}, err
	}
	var found *library.Item
	var fk plugin.KindDef
	for i := range items {
		k, ok := kindDef(m, items[i].Kind)
		if !ok || (ref.Kind != "" && k.Key != ref.Kind) {
			continue
		}
		if found != nil {
			return nil, plugin.KindDef{}, errAmbiguousKind
		}
		found, fk = &items[i], k
	}
	return found, fk, nil
}

func (s *server) loadSaved(ctx context.Context, m plugin.Manifest, k plugin.KindDef, it *library.Item) (*loadedItem, error) {
	li := &loadedItem{man: m, kind: k, saved: it, data: plugin.ItemData{
		ItemSummary: plugin.ItemSummary{Source: it.Source, ID: it.SourceID, Kind: it.Kind, Titles: it.Titles, Fields: it.Fields, CoverURL: it.CoverPath},
		ExternalURL: it.ExternalURL, ExternalIDs: it.ExternalIDs, Relations: it.Relations, Genres: it.Genres, Tags: it.Tags,
	}}
	var err error
	if li.entry, err = s.deps.Library.Entry(ctx, it.ID); err != nil {
		return nil, err
	}
	if hasSection(k, plugin.SectionPeople) {
		cs, err := s.deps.Library.Credits(ctx, it.ID)
		if err != nil {
			return nil, err
		}
		for _, c := range cs {
			cv := creditView{Role: c.Role, Person: savedPersonView(c.Person), Detail: c.Detail, WithRole: c.WithRole}
			if c.With != nil {
				p := savedPersonView(*c.With)
				cv.With = &p
			}
			li.credits = append(li.credits, cv)
		}
	}
	// recommendations are not stored: the overview loads them late
	// (recommendations.go)
	return li, nil
}

func (s *server) loadLive(ctx context.Context, m plugin.Manifest, ref itemRef, useCache bool) (*loadedItem, error) {
	if ref.Source == manualSource || !hasSource(m, ref.Source) {
		return nil, errNoSuchItem
	}
	kindKey := ref.Kind
	if kindKey == "" {
		if len(m.Kinds) != 1 {
			return nil, errNoSuchItem
		}
		kindKey = m.Kinds[0].Key
	}
	k, _ := kindDef(m, kindKey)
	key := liveKey(m.ID, ref.Source, kindKey, ref.ID)
	c, hit := cachedDetail{}, false
	if useCache {
		c, hit = s.cache.get(key)
		hit = hit && !c.DetailsOnly
	}
	if !hit {
		src := s.source(m.ID, ref.Source)
		if src == nil {
			return nil, errNoSource
		}
		d, err := src.Details(ctx, kindKey, ref.ID)
		if err != nil {
			return nil, err
		}
		c = cachedDetail{Data: d}
		if hasSection(k, plugin.SectionPeople) {
			if c.Credits, err = src.People(ctx, kindKey, ref.ID); err != nil {
				log.Printf("people %s: %v", key, err)
				c.Credits, c.CreditsFailed = nil, true
			}
		}
		s.cache.put(key, c)
	}
	li := &loadedItem{man: m, kind: k, data: c.Data, creditsFailed: c.CreditsFailed}
	// the identity comes from the URL (matched against the manifest), not
	// from what the source returned
	li.data.Source, li.data.ID, li.data.Kind = ref.Source, ref.ID, kindKey
	for _, cr := range c.Credits {
		cv := creditView{Role: cr.Role, Person: livePersonView(cr.Person), Detail: cr.Detail, WithRole: cr.WithRole}
		if cr.With != nil {
			p := livePersonView(*cr.With)
			cv.With = &p
		}
		li.credits = append(li.credits, cv)
	}
	return li, nil
}

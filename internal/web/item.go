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
	"strconv"
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
// set of every plugin), mounted under its literal prefix
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
		reg.Handle("GET /{source}/{id}/refresh", func(w http.ResponseWriter, r *http.Request) { s.getRefresh(w, r, pluginID) })
		reg.Handle("POST /{source}/{id}/refresh", func(w http.ResponseWriter, r *http.Request) { s.postRefresh(w, r, pluginID) })
		reg.Handle("GET /{source}/{id}/match", func(w http.ResponseWriter, r *http.Request) { s.getMatch(w, r, pluginID) })
		reg.Handle("GET /{source}/{id}/match/search", func(w http.ResponseWriter, r *http.Request) { s.getMatchSearch(w, r, pluginID) })
		reg.Handle("POST /{source}/{id}/match", func(w http.ResponseWriter, r *http.Request) { s.postMatch(w, r, pluginID) })
		reg.Handle("GET /export/{exporter}", func(w http.ResponseWriter, r *http.Request) { s.getExport(w, r, pluginID) })
		reg.Handle("POST /{source}/{id}/series", func(w http.ResponseWriter, r *http.Request) { s.postMakeSeries(w, r, pluginID) })
		reg.Handle("POST /bulk", func(w http.ResponseWriter, r *http.Request) { s.postBulk(w, r, pluginID) })
		s.seriesRoutes(reg, pluginID)
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

// updatedURL is the overview plus ?updated=N, keeping a ?kind= parameter.
func updatedURL(overview string, n int) string {
	sep := "?"
	if strings.Contains(overview, "?") {
		sep = "&"
	}
	return overview + sep + url.Values{"updated": {strconv.Itoa(n)}}.Encode()
}

// noticeSeriesAttach is the ?notice= value of an overview reached after an
// add whose entry could not be put into its series.
const noticeSeriesAttach = "series"

// noticeURL is the overview plus ?notice=key, keeping a ?kind= parameter.
func noticeURL(overview, key string) string {
	sep := "?"
	if strings.Contains(overview, "?") {
		sep = "&"
	}
	return overview + sep + url.Values{"notice": {key}}.Encode()
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
	s.showItem(w, r, ref, itemExtras{Updated: updatedCount(r), CanonicalRedirect: true}, http.StatusOK)
}

// updatedCount reads ?updated=N of the overview after "Update from source";
// nil when it is absent or not a non-negative integer.
func updatedCount(r *http.Request) *int {
	raw := r.URL.Query().Get("updated")
	if raw == "" {
		return nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return nil
	}
	return &n
}

// itemExtras are the dialogs an overview can be rendered with (no
// JavaScript fallback of the add and match dialogs). Dialog carries the add
// form's values and messages; the rest of the form is built from the loaded
// item. UseCache lets a fresh cache entry replace the source calls of an
// unsaved item. Updated is the ?updated=N count of a finished refresh (nil
// when the page was not reached that way).
type itemExtras struct {
	Dialog    *entryForm
	Match     *matchView
	RunDialog *runDialog
	// NoteDialog is the unit note form (notes.go)
	NoteDialog *noteDialog
	UseCache   bool
	Updated    *int
	// CanonicalRedirect sends an unsaved item opened by an alias to its
	// canonical URL (the plain overview GET only).
	CanonicalRedirect bool
}

// showItem renders the overview of ref (with a dialog open when x says so).
func (s *server) showItem(w http.ResponseWriter, r *http.Request, ref itemRef, x itemExtras, status int) {
	m, _ := s.deps.Registry.Manifest(ref.Plugin)
	li, err := s.loadItem(r.Context(), m, ref, x.UseCache)
	if err != nil {
		s.itemFail(w, r, m, ref.Source, err, itemURL(m, ref.Source, ref.ID, ref.Kind))
		return
	}
	if x.CanonicalRedirect && li.saved == nil && li.canonicalID != "" {
		http.Redirect(w, r, itemURL(m, ref.Source, li.canonicalID, ref.Kind), http.StatusFound)
		return
	}
	v, err := s.itemView(r.Context(), li)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if x.Updated != nil && li.saved != nil {
		v.UpdateNotice = updateNotice(*x.Updated, sourceName(m, li.data.Source))
	}
	if li.saved != nil {
		switch r.URL.Query().Get("notice") {
		case noticeSeriesAttach:
			v.UpdateNotice = "The entry was added to your list, but could not be put into the series."
		case noticeListAttach:
			v.UpdateNotice = "The entry was added to your list, but could not be put into the custom list."
		}
	}
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
	v.Match, v.RunDialog, v.NoteDialog = x.Match, x.RunDialog, x.NoteDialog
	if err := s.carryAdd(r, li, &v); err != nil {
		s.internalError(w, r, err)
		return
	}
	if li.saved != nil && li.entry != nil {
		d := li.data
		if v.ListPicker, err = s.listPicker(r, li.saved.ID, itemURL(li.man, d.Source, d.ID, d.Kind)); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	s.renderStatus(w, r, status, "item", v)
}

// carryAdd keeps a ?series= and a ?list= parameter (a search into a series
// or a custom list) on the add links of the overview, and offers an entry
// that is already listed to join them (the series only when it is in none).
// An unknown series, or one of another kind, and an unknown list are
// ignored.
func (s *server) carryAdd(r *http.Request, li *loadedItem, v *itemView) error {
	ctx := r.Context()
	var carry addCarry
	var ser *library.Series
	if uid := seriesParam(r); uid != "" {
		got, err := s.deps.Library.SeriesByUID(ctx, li.man.ID, uid)
		switch {
		case errors.Is(err, library.ErrNoSeries):
		case err != nil:
			return err
		case got.Kind == li.kind.Key:
			ser, carry.series = got, uid
		}
	}
	var lst library.List
	if id := listParam(r); id > 0 {
		got, err := s.deps.Library.CustomList(ctx, id)
		switch {
		case errors.Is(err, library.ErrNoList):
		case err != nil:
			return err
		default:
			lst, carry.list = got, id
		}
	}
	if v.AddURL != "" {
		v.AddURL = carry.on(v.AddURL)
	}
	if v.Dialog != nil {
		v.Dialog.Action = carry.on(v.Dialog.Action)
	}
	if li.saved == nil || li.entry == nil {
		return nil
	}
	d := li.data
	add := itemAction(li.man, d.Source, d.ID, d.Kind, "add", nil)
	if ser != nil && v.Series == nil {
		v.AddToSeriesURL, v.SeriesTitle = addCarry{series: carry.series}.on(add), ser.Title
	}
	if carry.list > 0 {
		v.AddToListURL, v.ListName = addCarry{list: carry.list}.on(add), lst.Name
	}
	return nil
}

// itemFail writes the error page of a load error.
func (s *server) itemFail(w http.ResponseWriter, r *http.Request, m plugin.Manifest, source string, err error, retry string) {
	switch {
	case errors.Is(err, errAmbiguousKind):
		s.errorPage(w, r, http.StatusNotFound, "Not found", "Several kinds share this id: add ?kind=…", "")
	case errors.Is(err, errNoSuchItem), errors.Is(err, plugin.ErrNotFound):
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such item.", "")
	case errors.Is(err, plugin.ErrNotConfigured):
		log.Printf("%s %q: %s", logSafe(r.Method), r.URL.Path, logSafe(err))
		s.errorPage(w, r, http.StatusServiceUnavailable, "Not configured", sourceMessage(m, source, err), retry)
	case errors.Is(err, plugin.ErrBadResponse):
		log.Printf("%s %q: %s", logSafe(r.Method), r.URL.Path, logSafe(err))
		s.errorPage(w, r, http.StatusBadGateway, "Unusable answer", sourceMessage(m, source, err), retry)
	case errors.Is(err, plugin.ErrRateLimited), errors.Is(err, plugin.ErrUnavailable):
		log.Printf("%s %q: %s", logSafe(r.Method), r.URL.Path, logSafe(err))
		s.errorPage(w, r, http.StatusServiceUnavailable, "Temporarily unavailable", sourceMessage(m, source, err), retry)
	default:
		log.Printf("%s %q: %s", logSafe(r.Method), r.URL.Path, logSafe(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// sourceMessage is the readable message of a source failure naming the
// source: a missing or refused credential (plugin.ErrNotConfigured), a bad
// answer (plugin.ErrBadResponse), rate limiting or an outage.
func sourceMessage(m plugin.Manifest, source string, err error) string {
	name := "The source"
	si, known := sourceInfo(m, source)
	if known && si.Name != "" {
		name = si.Name
	}
	if errors.Is(err, plugin.ErrNotConfigured) {
		switch {
		case si.KeyLabel != "" && len(si.KeyFields) > 0:
			return name + " needs valid " + lowerFirst(si.KeyLabel) + ". Add or replace them in Settings > Plugins."
		case si.KeyLabel != "":
			return name + " needs a valid " + lowerFirst(si.KeyLabel) + ". Add or replace it in Settings > Plugins."
		}
		return name + " needs a valid credential. Add or replace it in Settings > Plugins."
	}
	if errors.Is(err, plugin.ErrBadResponse) {
		return name + " returned an answer we could not use. Please try again later."
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
	// canonicalID is the source's own id for a live item opened by an alias
	// (see canonicalAlias); "" otherwise.
	canonicalID string
}

// canonicalAlias returns the source's canonical id when ref named the item by
// an alias (a slug the source also resolves): the answer's id differs from
// ref.ID, is a valid path id and belongs to the same source. "" otherwise.
func canonicalAlias(ref itemRef, d plugin.ItemData) string {
	if d.ID == "" || d.ID == ref.ID || !validPathID(d.ID) || (d.Source != "" && d.Source != ref.Source) {
		return ""
	}
	return d.ID
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
				log.Printf("people %s: %s", logSafe(key), logSafe(err))
				c.Credits, c.CreditsFailed = nil, true
			}
		}
		s.cache.put(key, c)
	}
	li := &loadedItem{man: m, kind: k, data: c.Data, creditsFailed: c.CreditsFailed}
	// the identity comes from the URL (matched against the manifest), not
	// from what the source returned; an alias is remembered so the overview
	// can redirect to the canonical id (getItem), like a match stores it.
	li.canonicalID = canonicalAlias(itemRef{Source: ref.Source, ID: ref.ID}, c.Data)
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

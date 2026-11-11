package web

// Matching a manual item to a source: GET /{plugin}/manual/{id}/match (the
// dialog), GET .../match/search?q= (results partial, the source's Search)
// and POST .../match (field source_ref, a pasted URL or bare id following the
// source's URL patterns, or pick, a search result's id, any valid path id;
// pick wins). URLs are matched against the source's
// declared URLPatterns by the core (plugin.MatchRef); no plugin code parses
// URLs. The source's Details is fetched FIRST, so a source failure never
// touches the database; then library.ConvertToSource converts the item in
// place (the entry stays) and the plugin's sync job is enqueued.

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// matchView is the data of the match dialog (match_dialog.html).
type matchView struct {
	// Action posts a match; SearchURL serves the results partial; CancelURL
	// is the overview (cancel link without JavaScript).
	Action, SearchURL, CancelURL                string
	Heading, RefLabel, Placeholder, SearchLabel string
	// Title is the item's resolved title; ExternalIDs its importer ids
	// ("<label> #<value>", the label from ImporterInfo.ExternalIDLabel, else
	// the id type).
	Title       string
	ExternalIDs []string
	// Kind is the item's kind for the no-JS search form (only when the
	// plugin has several kinds: browsers drop the action's query on a GET
	// submit).
	Kind string
	// Source is the chosen source id (posted with every match); Sources
	// links the dialog of every source (only when there are several).
	Source  string
	Sources []choice
	// SourceRef echoes the URL/id field.
	SourceRef string
	// Error is a user-facing message; ExistingURL links the item that
	// already holds the chosen source item.
	Error, ExistingURL string
	Search             searchView
}

// matchTarget is the saved manual item of a match request.
type matchTarget struct {
	man  plugin.Manifest
	kind plugin.KindDef
	ref  itemRef // Kind resolved
	item *library.Item
}

func (s *server) matchNotFound(w http.ResponseWriter, r *http.Request) {
	s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such manual item to match.", "")
}

// loadMatch resolves the saved manual item of the request (database only);
// anything else (other sources, unknown or unsaved ids, a plugin without
// sources) gets the 404 page.
func (s *server) loadMatch(w http.ResponseWriter, r *http.Request, pluginID string) (*matchTarget, bool) {
	m, ok := s.deps.Registry.Manifest(pluginID)
	ref := itemRef{Plugin: pluginID, Source: r.PathValue("source"), ID: r.PathValue("id"), Kind: r.URL.Query().Get("kind")}
	if !ok || len(m.Sources) == 0 || ref.Source != manualSource || !validPathID(ref.ID) || (ref.Kind != "" && !hasKind(m, ref.Kind)) {
		s.matchNotFound(w, r)
		return nil, false
	}
	it, k, err := s.findSaved(r.Context(), m, ref)
	switch {
	case errors.Is(err, errAmbiguousKind):
		s.itemFail(w, r, m, ref.Source, err, "")
		return nil, false
	case err != nil:
		s.internalError(w, r, err)
		return nil, false
	case it == nil:
		s.matchNotFound(w, r)
		return nil, false
	}
	ref.Kind = k.Key
	return &matchTarget{man: m, kind: k, ref: ref, item: it}, true
}

// defaultSource is the first source that supports lookups, else the first.
func defaultSource(m plugin.Manifest) plugin.SourceInfo {
	for _, si := range m.Sources {
		if si.SupportsLookup {
			return si
		}
	}
	return m.Sources[0]
}

// chooseSource is the source a request names ("" means the default).
func chooseSource(m plugin.Manifest, id string) (plugin.SourceInfo, bool) {
	if id == "" {
		return defaultSource(m), true
	}
	return sourceInfo(m, id)
}

func (s *server) newMatchView(r *http.Request, t *matchTarget, si plugin.SourceInfo) matchView {
	m := t.man
	var extra url.Values
	validSources := sourcesForKind(m, t.ref.Kind)
	if len(validSources) > 1 {
		extra = url.Values{"source": {si.ID}}
	}
	v := matchView{
		Action:      itemAction(m, t.ref.Source, t.ref.ID, t.ref.Kind, "match", nil),
		SearchURL:   itemAction(m, t.ref.Source, t.ref.ID, t.ref.Kind, "match/search", extra),
		CancelURL:   itemURL(m, t.ref.Source, t.ref.ID, t.ref.Kind),
		Heading:     "Match to " + si.Name,
		RefLabel:    si.Name + " URL or id",
		SearchLabel: "Or search " + si.Name,
		Title:       orID(s.resolveTitle(r.Context(), m, t.kind, t.ref.Kind, t.item.Titles), t.ref.ID),
		Source:      si.ID,
	}
	if len(si.URLPatterns) > 0 {
		v.Placeholder = "https://" + strings.Replace(si.URLPatterns[0], "{id}", "123", 1)
	}
	if len(m.Kinds) > 1 {
		v.Kind = t.ref.Kind
	}
	v.Search.EmptyHint = "Type a title to search " + si.Name + "."
	for typ, val := range t.item.ExternalIDs {
		label := typ
		for _, imp := range m.Importers {
			if imp.ExternalIDType == typ && imp.ExternalIDLabel != "" {
				label = imp.ExternalIDLabel
				break
			}
		}
		v.ExternalIDs = append(v.ExternalIDs, label+" #"+val)
	}
	sort.Strings(v.ExternalIDs)
	if len(validSources) > 1 {
		for _, x := range validSources {
			v.Sources = append(v.Sources, choice{Value: itemAction(m, t.ref.Source, t.ref.ID, t.ref.Kind, "match", url.Values{"source": {x.ID}}),
				Label: s.sourceChoiceLabel(r.Context(), x), Selected: x.ID == si.ID})
		}
	}
	return v
}

// matchResponse writes the dialog (htmx) or the overview page with the dialog
// open (no JavaScript).
func (s *server) matchResponse(w http.ResponseWriter, r *http.Request, t *matchTarget, v matchView, status int) {
	if isPartialRequest(r) {
		s.renderPartialStatus(w, status, "item", "match_dialog", v)
		return
	}
	s.showItem(w, r, t.ref, itemExtras{Match: &v}, status)
}

func (s *server) getMatch(w http.ResponseWriter, r *http.Request, pluginID string) {
	t, ok := s.loadMatch(w, r, pluginID)
	if !ok {
		return
	}
	si, ok := chooseSourceForKind(t.man, t.ref.Kind, r.URL.Query().Get("source"))
	if !ok {
		s.matchNotFound(w, r)
		return
	}
	s.matchResponse(w, r, t, s.newMatchView(r, t, si), http.StatusOK)
}

func (s *server) getMatchSearch(w http.ResponseWriter, r *http.Request, pluginID string) {
	t, ok := s.loadMatch(w, r, pluginID)
	if !ok {
		return
	}
	si, ok := chooseSourceForKind(t.man, t.ref.Kind, r.URL.Query().Get("source"))
	if !ok {
		s.matchNotFound(w, r)
		return
	}
	v := s.newMatchView(r, t, si)
	q := cleanQuery(r.URL.Query().Get("q"))
	v.Search.Query = q
	if q != "" { // an empty query makes no source call
		s.runSearch(r, t.man, si, t.kind, q, &v.Search)
	}
	status := http.StatusOK
	if v.Search.Status != 0 {
		status = v.Search.Status
	}
	if isPartialRequest(r) {
		s.renderPartialStatus(w, status, "item", "match_results", v)
		return
	}
	s.matchResponse(w, r, t, v, status)
}

// postMatch: reduce the input to a source id (no source call for invalid
// input), fetch the details (a failure changes nothing), convert in one
// transaction, enqueue the sync job, redirect to the item's new URL. A
// second (concurrent or repeated) POST finds no manual item any more and
// answers 404, or 422 when the conversion refuses it.
func (s *server) postMatch(w http.ResponseWriter, r *http.Request, pluginID string) {
	t, ok := s.loadMatch(w, r, pluginID)
	if !ok || !parseForm(w, r) {
		return
	}
	ctx := r.Context()
	si, ok := chooseSourceForKind(t.man, t.ref.Kind, strings.TrimSpace(r.PostFormValue("source")))
	pick := strings.TrimSpace(r.PostFormValue("pick"))
	raw := strings.TrimSpace(r.PostFormValue("source_ref"))
	if !ok {
		fallback := defaultSourceForKind(t.man, t.ref.Kind)
		if fallback.ID == "" {
			fallback = defaultSource(t.man)
		}
		v := s.newMatchView(r, t, fallback)
		v.SourceRef, v.Error = raw, "Please choose a source from the list."
		s.matchResponse(w, r, t, v, http.StatusUnprocessableEntity)
		return
	}
	v := s.newMatchView(r, t, si)
	v.SourceRef = raw
	fail := func(status int, msg string) {
		v.Error = msg
		s.matchResponse(w, r, t, v, status)
	}

	// 1. Reduce the input to a source id (pick wins over the reference).
	var id string
	switch {
	case pick != "":
		// any valid path id: the source's Details verifies it
		if !validPathID(pick) {
			fail(http.StatusUnprocessableEntity, "That search result is not a valid "+si.Name+" id.")
			return
		}
		id = pick
	case raw == "":
		fail(http.StatusUnprocessableEntity, "Paste a "+si.Name+" URL or id, or pick a search result.")
		return
	default:
		if id, ok = plugin.MatchRef(si.URLPatterns, raw); !ok {
			msg := "That does not look like a " + si.Name + " URL or id."
			if v.Placeholder != "" {
				msg += " Example: " + v.Placeholder
			}
			fail(http.StatusUnprocessableEntity, msg)
			return
		}
	}

	// 2. Fetch the details first: a source failure never touches the database.
	src := s.source(t.man.ID, si.ID)
	var d plugin.ItemData
	err := errNoSource
	if src != nil {
		d, err = src.Details(ctx, t.kind.Key, id)
	}
	switch {
	case err == nil:
	case errors.Is(err, plugin.ErrNotFound):
		fail(http.StatusUnprocessableEntity, si.Name+" has no "+strings.ToLower(t.kind.Label)+" with that id.")
		return
	case errors.Is(err, plugin.ErrBadResponse):
		log.Printf("match %s/%s: fetch %s/%s: %v", t.man.ID, t.ref.ID, si.ID, id, err)
		fail(http.StatusBadGateway, sourceMessage(t.man, si.ID, err)+" Nothing was changed.")
		return
	case errors.Is(err, plugin.ErrRateLimited), errors.Is(err, plugin.ErrUnavailable), errors.Is(err, plugin.ErrNotConfigured):
		log.Printf("match %s/%s: fetch %s/%s: %v", t.man.ID, t.ref.ID, si.ID, id, err)
		fail(http.StatusServiceUnavailable, sourceMessage(t.man, si.ID, err)+" Nothing was changed.")
		return
	default:
		s.internalError(w, r, err)
		return
	}
	// the identity is the user's choice (matched against the manifest), not
	// what the source returned
	d.Source, d.ID, d.Kind = si.ID, id, t.kind.Key

	// 3. Convert in one transaction (entry untouched, progress clamped).
	err = s.deps.Library.ConvertToSource(ctx, t.item.ID, d)
	var ase *library.AlreadySavedError
	switch {
	case err == nil:
	case errors.As(err, &ase):
		if ex, gerr := s.deps.Library.Item(ctx, ase.ExistingID); gerr == nil && ex != nil {
			v.ExistingURL = itemURL(t.man, ex.Source, ex.SourceID, ex.Kind)
		}
		fail(http.StatusUnprocessableEntity, "That item is already in your library, so nothing was changed.")
		return
	case errors.Is(err, library.ErrInvalid):
		fail(http.StatusUnprocessableEntity, invalidMessage(err))
		return
	case errors.Is(err, library.ErrNoEntry): // deleted meanwhile
		s.matchNotFound(w, r)
		return
	default:
		s.internalError(w, r, err)
		return
	}

	s.enqueueSync(ctx, t.man.ID, t.item.ID)
	s.done(w, r, itemURL(t.man, si.ID, id, t.kind.Key))
}

package web

// Merging two items of the same plugin and kind into one (library.MergeItems):
// GET /{plugin}/merge?keep=&other= shows the comparison page with the other
// item as the incoming version, what moves along without a choice and a
// "Swap" link that changes the survivor; POST /{plugin}/merge (keep, other
// and the ticked keys) merges and redirects to the survivor's overview with
// a notice. GET /{plugin}/merge/pick?keep=&q= ("Merge with…" on the item
// page) searches the library for the other item.

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/allister/allister/internal/covers"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// mergeView is the merge part of the comparison page: the hidden ids, the
// swap link, what moves without a choice, the warning and a refusal.
type mergeView struct {
	KeepID, OtherID string
	SwapURL         string
	Moves           string
	Warning         string
	Error           string
}

// mergeSide is one item of a merge with its display title.
type mergeSide struct {
	item  *library.Item
	title string
}

// maxMergeNoticeRunes bounds the title a ?merged= notice shows.
const maxMergeNoticeRunes = 300

// mergeURL is the merge page of the pair with the default survivor: a
// sourced item over a manual one, else the older (the lower id).
func mergeURL(pluginID string, a, b int64, aManual, bManual bool) string {
	keep, other := a, b
	if (aManual && !bManual) || (aManual == bManual && b < a) {
		keep, other = b, a
	}
	return mergePageURL(pluginID, keep, other)
}

// mergePageURL is the merge page with keep as the survivor.
func mergePageURL(pluginID string, keep, other int64) string {
	return "/" + url.PathEscape(pluginID) + "/merge?" +
		url.Values{"keep": {strconv.FormatInt(keep, 10)}, "other": {strconv.FormatInt(other, 10)}}.Encode()
}

// mergedURL is the survivor's overview with the notice of a finished merge.
func mergedURL(overview, otherTitle string) string {
	sep := "?"
	if strings.Contains(overview, "?") {
		sep = "&"
	}
	return overview + sep + url.Values{"merged": {otherTitle}}.Encode()
}

// mergedNotice is the overview's notice of ?merged=<title> ("" without).
func mergedNotice(r *http.Request) string {
	title := plugin.SingleLine(r.URL.Query().Get("merged"), maxMergeNoticeRunes)
	if title == "" {
		return ""
	}
	return "Merged " + title + " into this item."
}

// mergeSideOf loads an item of the plugin by its id text; nil when the id is
// not a saved item of the plugin with a declared kind.
func (s *server) mergeSideOf(r *http.Request, m plugin.Manifest, raw string) (*mergeSide, plugin.KindDef, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return nil, plugin.KindDef{}, nil
	}
	it, err := s.deps.Library.Item(r.Context(), id)
	if err != nil || it == nil || it.PluginID != m.ID {
		return nil, plugin.KindDef{}, err
	}
	k, ok := kindDef(m, it.Kind)
	if !ok {
		return nil, plugin.KindDef{}, nil
	}
	title := library.ResolveTitle(it.Titles, s.titleLang(r.Context(), m), titleFallback(m, k, it.Kind))
	if title == "" {
		title = "(untitled)"
	}
	return &mergeSide{item: it, title: title}, k, nil
}

// mergeTarget is the pair of a merge request.
type mergeTarget struct {
	man         plugin.Manifest
	kind        plugin.KindDef
	keep, other *mergeSide
}

// mergeTarget resolves keep and other of the request (the query of a GET,
// the form of a POST). Unknown or stale ids, the same item twice and items
// of another plugin or kind are 404; on failure the response is written and
// ok is false.
func (s *server) mergeTarget(w http.ResponseWriter, r *http.Request, pluginID string, vals url.Values) (*mergeTarget, bool) {
	notFound := func() (*mergeTarget, bool) {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such item.", "")
		return nil, false
	}
	m, ok := s.deps.Registry.Manifest(pluginID)
	if !ok {
		return notFound()
	}
	keep, k, err := s.mergeSideOf(r, m, vals.Get("keep"))
	if err != nil {
		s.internalError(w, r, err)
		return nil, false
	}
	if keep == nil {
		return notFound()
	}
	other, _, err := s.mergeSideOf(r, m, vals.Get("other"))
	if err != nil {
		s.internalError(w, r, err)
		return nil, false
	}
	if other == nil || other.item.ID == keep.item.ID || other.item.Kind != keep.item.Kind {
		return notFound()
	}
	return &mergeTarget{man: m, kind: k, keep: keep, other: other}, true
}

func (t *mergeTarget) overview() string {
	return itemURL(t.man, t.keep.item.Source, t.keep.item.SourceID, t.keep.item.Kind)
}

// mergeMoves is the plain list of what the other item hands over without a
// choice: its runs, notes, own tags, custom lists and its series place
// (only when the survivor is in no series).
func (s *server) mergeMoves(r *http.Request, t *mergeTarget) (string, error) {
	ctx := r.Context()
	id := t.other.item.ID
	var parts []string
	plural := func(n int, one, many string) {
		if n == 1 {
			parts = append(parts, "1 "+one)
		} else if n > 1 {
			parts = append(parts, strconv.Itoa(n)+" "+many)
		}
	}
	runs, err := s.deps.Library.Runs(ctx, id)
	if err != nil {
		return "", err
	}
	plural(len(runs), "run", "runs")
	e, err := s.deps.Library.Entry(ctx, id)
	if err != nil {
		return "", err
	}
	if e != nil && e.Notes != "" {
		parts = append(parts, "notes")
	}
	tags, err := s.deps.Library.EntryTags(ctx, []int64{id})
	if err != nil {
		return "", err
	}
	plural(len(tags[id]), "own tag", "own tags")
	lists, err := s.deps.Library.ListsOfItem(ctx, id)
	if err != nil {
		return "", err
	}
	plural(len(lists), "custom list", "custom lists")
	ser, _, err := s.deps.Library.SeriesOfItem(ctx, id)
	if err != nil {
		return "", err
	}
	if ser != nil {
		own, _, err := s.deps.Library.SeriesOfItem(ctx, t.keep.item.ID)
		if err != nil {
			return "", err
		}
		if own == nil {
			parts = append(parts, "series")
		}
	}
	if len(parts) == 0 {
		return "Besides the ticked details nothing else moves.", nil
	}
	return "Also moves from " + t.other.title + ": " + strings.Join(parts, ", ") + ".", nil
}

// mergePage builds the comparison page of the pair; ticked, when not nil,
// replaces the default ticks (a refused POST shows the posted ones).
func (s *server) mergePage(r *http.Request, t *mergeTarget, ticked map[string]bool, msg string) (changesView, error) {
	changes, err := s.deps.Library.MergeChanges(r.Context(), t.keep.item.ID, t.other.item.ID)
	if err != nil {
		return changesView{}, err
	}
	in := plugin.ItemData{ItemSummary: plugin.ItemSummary{Fields: t.other.item.Fields, CoverURL: t.other.item.CoverPath}}
	rows := changeRows(t.kind, t.keep.item, in, changes)
	if ticked != nil {
		for i := range rows {
			rows[i].Checked = ticked[rows[i].Key]
		}
	}
	moves, err := s.mergeMoves(r, t)
	if err != nil {
		return changesView{}, err
	}
	keepID, otherID := strconv.FormatInt(t.keep.item.ID, 10), strconv.FormatInt(t.other.item.ID, 10)
	return changesView{
		Title:  "Merge “" + t.other.title + "” into “" + t.keep.title + "”",
		Action: "/" + url.PathEscape(t.man.ID) + "/merge", Return: t.overview(), Source: t.other.title,
		Rows: rows, Empty: len(rows) == 0,
		Merge: &mergeView{KeepID: keepID, OtherID: otherID, Moves: moves, Error: msg,
			SwapURL: mergePageURL(t.man.ID, t.other.item.ID, t.keep.item.ID),
			Warning: "This removes " + t.other.title + " from your library; its data moves to " + t.keep.title + "."},
	}, nil
}

func (s *server) getMerge(w http.ResponseWriter, r *http.Request, pluginID string) {
	t, ok := s.mergeTarget(w, r, pluginID, r.URL.Query())
	if !ok {
		return
	}
	v, err := s.mergePage(r, t, nil, "")
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.render(w, r, "changes", v)
}

// afterMerge queues the survivor's jobs: the normal sync when a manual
// survivor took over the other's source link (Deps.SyncJobs; it downloads
// a remote cover too), else the cover-only job when the merge gave it a
// remote cover (Deps.CoverJobs). A failed read is logged and ignored.
func (s *server) afterMerge(ctx context.Context, t *mergeTarget) {
	keep := t.keep.item
	if keep.Source == manualSource && t.other.item.Source != manualSource {
		s.enqueueSync(ctx, t.man.ID, keep.ID)
		return
	}
	it, err := s.deps.Library.Item(ctx, keep.ID)
	if err != nil || it == nil {
		log.Printf("merge %s item %d: %v", logSafe(t.man.ID), keep.ID, err)
		return
	}
	if isRemoteCover(it.CoverPath) && it.CoverPath != keep.CoverPath {
		s.enqueueCover(ctx, t.man.ID, keep.ID)
	}
}

// postMerge merges with the posted keys (unknown keys are ErrInvalid in the
// library) and queues the survivor's jobs (afterMerge). A refusal (notes
// too long, ...) shows the page again with the message (422).
func (s *server) postMerge(w http.ResponseWriter, r *http.Request, pluginID string) {
	if !parseForm(w, r) {
		return
	}
	t, ok := s.mergeTarget(w, r, pluginID, r.PostForm)
	if !ok {
		return
	}
	keys := r.PostForm["key"]
	err := s.deps.Library.MergeItems(r.Context(), t.keep.item.ID, t.other.item.ID, keys)
	switch {
	case err == nil:
		s.afterMerge(r.Context(), t)
		//nolint:gosec // G710: a local path built by itemURL from the stored item, the title only in its query
		http.Redirect(w, r, mergedURL(t.overview(), t.other.title), http.StatusSeeOther)
	case errors.Is(err, library.ErrInvalid):
		ticked := map[string]bool{}
		for _, k := range keys {
			ticked[k] = true
		}
		v, perr := s.mergePage(r, t, ticked, invalidMessage(err))
		if perr != nil {
			s.internalError(w, r, perr)
			return
		}
		s.renderStatus(w, r, http.StatusUnprocessableEntity, "changes", v)
	default:
		s.internalError(w, r, err)
	}
}

// mergePickView is the "Merge with…" search page.
type mergePickView struct {
	Title, Action, KeepID, Query, Return string
	Searched                             bool
	Results                              []mergePickResult
}

type mergePickResult struct {
	Title, Cover, Href string
}

// getMergePick searches the library for items of the survivor's plugin and
// kind (library.MergeCandidates), each linking to the merge page.
func (s *server) getMergePick(w http.ResponseWriter, r *http.Request, pluginID string) {
	m, ok := s.deps.Registry.Manifest(pluginID)
	var keep *mergeSide
	var err error
	if ok {
		keep, _, err = s.mergeSideOf(r, m, r.URL.Query().Get("keep"))
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if keep == nil {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such item.", "")
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	found, err := s.deps.Library.MergeCandidates(r.Context(), m.ID, keep.item.Kind, keep.item.ID, q, s.titleLangs(r), maxListResults)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	v := mergePickView{Title: "Merge “" + keep.title + "” with…", Action: "/" + url.PathEscape(m.ID) + "/merge/pick",
		KeepID: strconv.FormatInt(keep.item.ID, 10), Query: q, Searched: q != "",
		Return: itemURL(m, keep.item.Source, keep.item.SourceID, keep.item.Kind)}
	for _, row := range found {
		it := row.Item
		title := row.Title
		if title == "" {
			title = "(untitled)"
		}
		v.Results = append(v.Results, mergePickResult{Title: title, Cover: covers.Src(it.CoverPath),
			Href: mergeURL(m.ID, keep.item.ID, it.ID, keep.item.Source == manualSource, it.Source == manualSource)})
	}
	s.render(w, r, "merge_pick", v)
}

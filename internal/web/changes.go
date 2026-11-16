package web

// The comparison page: one item's stored data next to an incoming version,
// one row per differing part (library.ItemChanges), each with a checkbox;
// "Apply selected" writes only the ticked parts (library.ApplyChanges).
// "Update from source" (GET|POST /{plugin}/{source}/{id}/refresh) shows it
// with the item source's current answer as the incoming version.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// changesView is the comparison page. Title is the heading, Action the URL
// "Apply selected" posts the ticked keys to, Return the page "Cancel" goes
// back to. Source names the incoming side ("From <Source>", "up to date
// with <Source>"). Empty: nothing differs (no form).
type changesView struct {
	Title, Action string
	Rows          []changeRow
	Empty         bool
	Return        string
	Source        string
}

// changeRow is one differing part. Stored and Incoming are display texts;
// Added and Removed the entries of a list part. Checked: the box starts
// ticked (the stored side is empty).
type changeRow struct {
	Key, Label, Stored, Incoming string
	Checked                      bool
	Added, Removed               []string
}

// foldRunes is the length above which a value is shown shortened, with the
// whole text behind "Show all".
const foldRunes = 300

// foldText is the shortened form of a long value ("" when it is short).
func foldText(s string) string {
	if utf8.RuneCountInString(s) <= foldRunes {
		return ""
	}
	n := 0
	for i := range s {
		if n == foldRunes {
			return strings.TrimRight(s[:i], " ") + "…"
		}
		n++
	}
	return ""
}

// changeRows turns the library's changes into rows; field values are
// formatted like the overview formats them (units, dates), from the stored
// item and the incoming data.
func changeRows(k plugin.KindDef, it *library.Item, in plugin.ItemData, changes []library.Change) []changeRow {
	rows := make([]changeRow, 0, len(changes))
	for _, c := range changes {
		row := changeRow{Key: c.Key, Label: c.Label, Stored: c.Stored, Incoming: c.Incoming, Checked: c.StoredEmpty,
			Added: c.Added, Removed: c.Removed}
		if key, ok := strings.CutPrefix(c.Key, "field."); ok {
			if fd, ok := fieldDef(k, key); ok {
				if sv, has := it.Fields[key]; has {
					if t, _ := fieldText(fd, sv); t != "" {
						row.Stored = t
					}
				}
				if t, _ := fieldText(fd, in.Fields[key]); t != "" {
					row.Incoming = t
				}
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// refreshTarget is the saved, sourced item of an "Update from source"
// request.
type refreshTarget struct {
	man      plugin.Manifest
	kind     plugin.KindDef
	item     *library.Item
	src      plugin.Source
	overview string
	action   string
}

// refreshTarget resolves the item of the request. Manual items, unsaved
// items and sources the plugin does not declare or provide are 404; on
// failure the response is written and ok is false.
func (s *server) refreshTarget(w http.ResponseWriter, r *http.Request, pluginID string) (*refreshTarget, bool) {
	m, ref, ok := s.addRef(w, r, pluginID)
	if !ok {
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
	}
	if it == nil || it.Source == manualSource || !hasSource(m, it.Source) {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such item.", "")
		return nil, false
	}
	src := s.source(m.ID, it.Source)
	if src == nil {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such item.", "")
		return nil, false
	}
	return &refreshTarget{man: m, kind: k, item: it, src: src,
		overview: itemURL(m, it.Source, it.SourceID, k.Key),
		action:   itemAction(m, it.Source, it.SourceID, k.Key, "refresh", nil)}, true
}

// incoming asks the source for the item's details and, when the kind shows
// credits, its people. A failing People call is logged and leaves the
// credits out (withCredits false); a Details failure is returned.
func (t *refreshTarget) incoming(ctx context.Context) (d plugin.ItemData, credits []plugin.Credit, withCredits bool, err error) {
	if d, err = t.src.Details(ctx, t.kind.Key, t.item.SourceID); err != nil {
		return plugin.ItemData{}, nil, false, err
	}
	if !hasSection(t.kind, plugin.SectionPeople) {
		return d, nil, false, nil
	}
	credits, perr := t.src.People(ctx, t.kind.Key, t.item.SourceID)
	if perr != nil {
		log.Printf("people %s/%s/%s: %s", logSafe(t.man.ID), logSafe(t.item.Source), logSafe(t.item.SourceID), logSafe(perr))
		return d, nil, false, nil
	}
	return d, credits, true, nil
}

// changesFail writes the page of a library error of the comparison: an
// incoming value the library cannot store is the same bad gateway as an
// unusable answer.
func (s *server) changesFail(w http.ResponseWriter, r *http.Request, t *refreshTarget, err error) {
	switch {
	case errors.Is(err, library.ErrNoEntry):
		s.itemFail(w, r, t.man, t.item.Source, errNoSuchItem, "")
	case errors.Is(err, library.ErrInvalid):
		s.itemFail(w, r, t.man, t.item.Source, fmt.Errorf("%w: %w", plugin.ErrBadResponse, err), t.action)
	default:
		s.internalError(w, r, err)
	}
}

// getRefresh shows the comparison of the item with its source's current
// answer. It changes nothing.
func (s *server) getRefresh(w http.ResponseWriter, r *http.Request, pluginID string) {
	t, ok := s.refreshTarget(w, r, pluginID)
	if !ok {
		return
	}
	d, credits, withCredits, err := t.incoming(r.Context())
	if err != nil {
		s.itemFail(w, r, t.man, t.item.Source, err, t.action)
		return
	}
	changes, err := s.deps.Library.ItemChanges(r.Context(), t.item.ID, d, credits, withCredits)
	if err != nil {
		s.changesFail(w, r, t, err)
		return
	}
	name := sourceName(t.man, t.item.Source)
	title := library.ResolveTitle(t.item.Titles, s.titleLang(r.Context(), t.man), t.kind.TitleFallback)
	if title == "" {
		title = "(untitled)"
	}
	s.render(w, r, "changes", changesView{
		Title: "Update “" + title + "” from " + name, Action: t.action, Return: t.overview, Source: name,
		Rows: changeRows(t.kind, t.item, d, changes), Empty: len(changes) == 0,
	})
}

// postRefresh asks the source again and applies the posted keys (the ticked
// rows) from that answer; values in the form are never used. A key that is
// not a current difference is ignored. A taken remote cover is downloaded by
// the plugin's cover-only job (Deps.CoverJobs). Success redirects to the
// overview with ?updated=N, N the number of parts written.
func (s *server) postRefresh(w http.ResponseWriter, r *http.Request, pluginID string) {
	t, ok := s.refreshTarget(w, r, pluginID)
	if !ok || !parseForm(w, r) {
		return
	}
	ctx := r.Context()
	d, credits, withCredits, err := t.incoming(ctx)
	if err != nil {
		s.itemFail(w, r, t.man, t.item.Source, err, t.action)
		return
	}
	changes, err := s.deps.Library.ItemChanges(ctx, t.item.ID, d, credits, withCredits)
	if err != nil {
		s.changesFail(w, r, t, err)
		return
	}
	posted := map[string]bool{}
	for _, k := range r.PostForm["key"] {
		posted[k] = true
	}
	var keys []string
	for _, c := range changes {
		if posted[c.Key] {
			keys = append(keys, c.Key)
		}
	}
	applied, err := s.deps.Library.ApplyChanges(ctx, t.item.ID, d, credits, withCredits, keys)
	if err != nil {
		s.changesFail(w, r, t, err)
		return
	}
	for _, k := range applied {
		if k == "cover" && isRemoteCover(d.CoverURL) {
			s.enqueueCover(ctx, pluginID, t.item.ID)
		}
	}
	http.Redirect(w, r, updatedURL(t.overview, len(applied)), http.StatusSeeOther)
}

func isRemoteCover(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

// enqueueCover enqueues the plugin's cover-only job (Deps.CoverJobs) of an
// item whose cover became a remote URL. A failure is logged and ignored: the
// overview shows the remote cover until a later sync downloads it.
func (s *server) enqueueCover(ctx context.Context, pluginID string, itemID int64) {
	job := s.deps.CoverJobs[pluginID]
	if job == nil || s.deps.Jobs == nil {
		return
	}
	kind, payload := job(itemID)
	if err := s.deps.Jobs.Enqueue(ctx, kind, payload); err != nil {
		log.Printf("enqueue cover for %s item %d: %v", pluginID, itemID, err)
	}
}

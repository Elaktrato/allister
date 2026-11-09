package web

// The generic add-to-list flow: GET|POST /{plugin}/{source}/{id}/add. The
// dialog is built from the kind's schema (status labels, one progress input
// per active progress track) and the one shared entry form parser
// (parseEntryForm) validates everything, including library.ValidateEntry
// against the item, before anything is written.

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// progressField is the form field prefix of a progress value: the value of
// track "episode" is posted as progress.episode.
const progressField = "progress."

// progressShorthand is the legacy form field "progress": it maps to the
// kind's primary progress key (Progress.PrimaryKey) when the kind has one,
// so a plain progress=3 keeps working. An explicit progress.<primary> wins.
const progressShorthand = "progress"

// entryValues are the raw (trimmed) inputs of the entry form, echoed back
// into the dialog on errors. Progress is keyed by progress key.
type entryValues struct {
	Status, Rating, StartedOn, FinishedOn, Comment string
	Progress                                       map[string]string
	// orphan is a non-empty shorthand value of a kind without a primary key.
	orphan string
}

// progressInput is one progress input of the dialog. Max is the input's max
// attribute ("" for none), Total the "/ total" text ("" for none).
type progressInput struct {
	Name, Label, Value, Max, Total string
	Duration, Percent              bool
	// Inactive marks a track outside the selected combination (edit
	// dialog), shown muted with Hint naming a combination that uses it.
	Inactive bool
	Hint     string
}

// entryForm is the data of add_dialog.html.
type entryForm struct {
	entryValues
	Action, CancelURL, Heading, Submit string
	Statuses                           []statusOpt
	Progress                           []progressInput
	// Error is a user-facing message; ExistingURL links the item when it
	// is already in the list.
	Error, ExistingURL string
}

// progressContext is what the entry form needs of the item: its active
// progress keys and its field values (the totals).
type progressContext struct {
	Keys   []string
	Fields map[string]plugin.FieldValue
}

// readEntryValues reads the raw form values. The comment is trimmed and
// capped at library.MaxCommentRunes. A repeated parameter counts with its
// FIRST value (PostFormValue). When both progress.<primary> and the
// progressShorthand are sent, the explicit progress.<primary> wins.
func readEntryValues(kind plugin.KindDef, r *http.Request) entryValues {
	get := func(k string) string { return strings.TrimSpace(r.PostFormValue(k)) }
	v := entryValues{Status: get("status"), Rating: get("rating"), StartedOn: get("started_on"),
		FinishedOn: get("finished_on"), Comment: get("comment"), Progress: map[string]string{}}
	if rs := []rune(v.Comment); len(rs) > library.MaxCommentRunes {
		v.Comment = strings.TrimSpace(string(rs[:library.MaxCommentRunes]))
	}
	for name := range r.PostForm {
		if key, ok := strings.CutPrefix(name, progressField); ok {
			v.Progress[key] = get(name)
		}
	}
	if sh := get(progressShorthand); sh != "" {
		if pk := kind.Progress.PrimaryKey; pk == "" {
			v.orphan = sh
		} else if v.Progress[pk] == "" {
			v.Progress[pk] = sh
		}
	}
	return v
}

// parseWhole accepts 1 to maxDigits ASCII digits (no sign, no fraction).
func parseWhole(s string, maxDigits int) (int64, bool) {
	if !isASCIIDigits(s, maxDigits) {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}

// parseDuration reads a position as h:mm:ss, m:ss or plain seconds. The
// first part is a whole number, every later part exactly two digits 00..59.
func parseDuration(s string) (int64, bool) {
	parts := strings.Split(s, ":")
	if len(parts) > 3 {
		return 0, false
	}
	total, ok := parseWhole(parts[0], 12)
	if len(parts) > 1 {
		total, ok = parseWhole(parts[0], 9)
	}
	if !ok {
		return 0, false
	}
	for _, p := range parts[1:] {
		n, ok := parseWhole(p, 2)
		if !ok || len(p) != 2 || n > 59 {
			return 0, false
		}
		total = total*60 + n
	}
	return total, true
}

// parseProgress parses one progress value by its type; msg is set on error.
func parseProgress(def plugin.ProgressDef, raw string) (float64, string) {
	switch def.Type {
	case plugin.Duration:
		if n, ok := parseDuration(raw); ok {
			return float64(n), ""
		}
		return 0, def.Label + " must be a time like 1:02:03 (h:mm:ss), 12:34 (m:ss) or a number of seconds."
	case plugin.Percent:
		if n, ok := parseWhole(raw, 3); ok && n <= 100 {
			return float64(n), ""
		}
		return 0, def.Label + " must be a whole number from 0 to 100."
	}
	if n, ok := parseWhole(raw, 12); ok {
		return float64(n), ""
	}
	return 0, def.Label + " must be a whole number, 0 or more."
}

// parseEntryForm is THE entry form parser (add and edit, later manual).
// It is SYNTACTIC: it needs only the kind and the item's active progress
// keys (a saved item's keys, else the kind's defaults), never the item's
// data, so it runs before any source call. It checks the status class, the
// rating, that every progress key is active, the typed progress values, the
// dates and the comment hygiene, ending with library.ValidateEntry against
// an item without totals (percent bounds, finish before start, text rules).
// The total-dependent check is validateAgainstItem, run once the item's data
// is known. Field names: status (a core status class), rating (blank or
// 0..10), progress.<key> per active progress key (plus the
// progressShorthand), started_on and finished_on (YYYY-MM-DD calendar days,
// stored as the UTC midnight of the typed day) and comment. msg is a
// user-facing message, non-empty on error.
func (s *server) parseEntryForm(kind plugin.KindDef, keys []string, r *http.Request) (entryValues, library.Entry, string) {
	v := readEntryValues(kind, r)
	e := library.Entry{Status: library.Status(v.Status), Comment: v.Comment, Progress: map[string]float64{}}
	if !slices.Contains(library.Statuses(), e.Status) {
		return v, e, "Please choose a status."
	}
	if v.Rating != "" {
		n, ok := parseWhole(v.Rating, 2)
		if !ok || n > 10 {
			return v, e, "Rating must be a whole number from 0 to 10, or blank for unrated."
		}
		rating := int(n)
		e.Rating = &rating
	}
	if v.orphan != "" {
		return v, e, "This item has no main progress; use the progress fields."
	}
	sent := make([]string, 0, len(v.Progress))
	for k := range v.Progress {
		sent = append(sent, k)
	}
	sort.Strings(sent)
	for _, key := range sent {
		raw := v.Progress[key]
		if raw == "" {
			continue
		}
		def, ok := progressDefOf(kind, key)
		if !ok || !slices.Contains(keys, key) {
			label := truncateRunes(key, 32)
			if ok {
				label = def.Label
			}
			return v, e, "“" + label + "” is not a progress of this item."
		}
		n, msg := parseProgress(def, raw)
		if msg != "" {
			return v, e, msg
		}
		e.Progress[key] = n
	}
	var msg string
	if e.StartedOn, msg = parseDay(v.StartedOn, "Start date"); msg != "" {
		return v, e, msg
	}
	if e.FinishedOn, msg = parseDay(v.FinishedOn, "Finish date"); msg != "" {
		return v, e, msg
	}
	if err := s.deps.Library.ValidateEntry(kind, &library.Item{ProgressKeys: keys}, e); err != nil {
		return v, e, invalidMessage(err)
	}
	return v, e, ""
}

// validateAgainstItem is the total-dependent second step of the entry form:
// library.ValidateEntry against the item's active keys and field values
// (progress above a total). msg is "" when e can be written.
func (s *server) validateAgainstItem(kind plugin.KindDef, pc progressContext, e library.Entry) string {
	if err := s.deps.Library.ValidateEntry(kind, &library.Item{ProgressKeys: pc.Keys, Fields: pc.Fields}, e); err != nil {
		return invalidMessage(err)
	}
	return ""
}

// parseDay reads a calendar day YYYY-MM-DD as its UTC midnight.
func parseDay(v, label string) (*time.Time, string) {
	if v == "" {
		return nil, ""
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		return nil, label + " must be a date like 2024-01-31."
	}
	return &t, ""
}

func progressDefOf(k plugin.KindDef, key string) (plugin.ProgressDef, bool) {
	for _, d := range k.Progress.Eligible {
		if d.Key == key {
			return d, true
		}
	}
	return plugin.ProgressDef{}, false
}

// addPath is the add URL of an item (with ?kind= when the plugin has several kinds).
func addPath(m plugin.Manifest, source, id, kind string) string {
	u := itemPath(m.ID, source, id) + "/add"
	if len(m.Kinds) > 1 && kind != "" {
		u += "?kind=" + url.QueryEscape(kind)
	}
	return u
}

// newEntryForm builds the add dialog of an item of kind k from the values v.
func newEntryForm(m plugin.Manifest, k plugin.KindDef, source, id string, pc progressContext, v entryValues) entryForm {
	if v.Status == "" {
		v.Status = string(library.Planned)
	}
	f := entryForm{entryValues: v, Action: addPath(m, source, id, k.Key), CancelURL: itemURL(m, source, id, k.Key),
		Heading: "Add to list", Submit: "Add"}
	for _, st := range library.Statuses() {
		f.Statuses = append(f.Statuses, statusOpt{Value: string(st), Label: statusLabel(k, st), Selected: string(st) == v.Status})
	}
	f.Progress = progressInputs(k, pc, v.Progress)
	return f
}

// progressInputs builds one input per progress track of k that is active in
// pc (in the kind's order), prefilled from values (keyed by progress key):
// percent gets max 100, a known total of a count or pages track gets max and
// "/ total", a duration total is shown as h:mm:ss.
func progressInputs(k plugin.KindDef, pc progressContext, values map[string]string) []progressInput {
	var out []progressInput
	for _, def := range k.Progress.Eligible {
		if !slices.Contains(pc.Keys, def.Key) {
			continue
		}
		in := progressInput{Name: progressField + def.Key, Label: def.Label, Value: values[def.Key],
			Duration: def.Type == plugin.Duration, Percent: def.Type == plugin.Percent}
		if in.Percent {
			in.Max = "100"
		} else if total, ok := progressTotal(def, pc.Fields); ok {
			if in.Duration {
				in.Total = fmtDuration(total)
			} else {
				in.Max, in.Total = fmtNum(total), fmtNum(total)
			}
		}
		out = append(out, in)
	}
	return out
}

// progressTotal is the known total (> 0) of a bounded count, pages or
// duration track.
func progressTotal(def plugin.ProgressDef, fields map[string]plugin.FieldValue) (float64, bool) {
	if def.TotalField == "" || def.Type == plugin.Percent {
		return 0, false
	}
	if v := fields[def.TotalField].Num; v != nil && *v > 0 {
		return *v, true
	}
	return 0, false
}

func alreadyInList(k plugin.KindDef) string {
	return "This " + lowerFirst(k.Label) + " is already in your list."
}

// addTarget is the item of an add request: saved (with its entry, if any)
// or an unsaved item of the plugin's source whose live data is fetched on
// demand (fetchLive).
type addTarget struct {
	man      plugin.Manifest
	kind     plugin.KindDef
	ref      itemRef // Kind resolved
	saved    *library.Item
	entry    *library.Entry
	data     plugin.ItemData // live data, unsaved only
	haveData bool            // data is set (from the cache or the source)
}

// keys are the item's active progress keys.
func (t *addTarget) keys() []string {
	if t.saved != nil {
		return t.saved.ProgressKeys
	}
	return t.kind.Progress.Defaults
}

func (t *addTarget) progress() progressContext {
	if t.saved != nil {
		return progressContext{Keys: t.saved.ProgressKeys, Fields: t.saved.Fields}
	}
	return progressContext{Keys: t.kind.Progress.Defaults, Fields: t.data.Fields}
}

func (t *addTarget) form(v entryValues) entryForm {
	return newEntryForm(t.man, t.kind, t.ref.Source, t.ref.ID, t.progress(), v)
}

// addRef validates the request's item reference; it writes the 404 page and
// returns false when it is malformed.
func (s *server) addRef(w http.ResponseWriter, r *http.Request, pluginID string) (plugin.Manifest, itemRef, bool) {
	m, ok := s.deps.Registry.Manifest(pluginID)
	ref := itemRef{Plugin: pluginID, Source: r.PathValue("source"), ID: r.PathValue("id"), Kind: r.URL.Query().Get("kind")}
	if !ok || !validPathID(ref.Source) || !validPathID(ref.ID) || (ref.Kind != "" && !hasKind(m, ref.Kind)) {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such item.", "")
		return m, ref, false
	}
	return m, ref, true
}

// kindFor is the kind a request names, else the plugin's first kind.
func kindFor(m plugin.Manifest, ref itemRef) plugin.KindDef {
	if k, ok := kindDef(m, ref.Kind); ok {
		return k
	}
	return m.Kinds[0]
}

// resolveAdd finds the saved item of ref (database only, no source call).
// For an unsaved item it takes the live data from a fresh overview cache
// entry when there is one; fetchLive asks the source otherwise. Unsaved
// manual items, undeclared sources and an unsaved id of a plugin with
// several kinds without ?kind= are errNoSuchItem.
func (s *server) resolveAdd(ctx context.Context, m plugin.Manifest, ref itemRef) (*addTarget, error) {
	saved, k, err := s.findSaved(ctx, m, ref)
	if err != nil {
		return nil, err
	}
	if saved != nil {
		t := &addTarget{man: m, kind: k, ref: ref, saved: saved}
		t.ref.Kind = k.Key
		if t.entry, err = s.deps.Library.Entry(ctx, saved.ID); err != nil {
			return nil, err
		}
		return t, nil
	}
	if ref.Source == manualSource || !hasSource(m, ref.Source) {
		return nil, errNoSuchItem
	}
	if ref.Kind == "" {
		if len(m.Kinds) != 1 {
			return nil, errNoSuchItem
		}
		ref.Kind = m.Kinds[0].Key
	}
	t := &addTarget{man: m, kind: kindFor(m, ref), ref: ref}
	if c, ok := s.cache.get(liveKey(m.ID, ref.Source, ref.Kind, ref.ID)); ok {
		t.setData(c.Data)
	}
	return t, nil
}

// setData sets the live data; the identity comes from the URL (matched
// against the manifest), not from what the source returned.
func (t *addTarget) setData(d plugin.ItemData) {
	t.data, t.haveData = d, true
	t.data.Source, t.data.ID, t.data.Kind = t.ref.Source, t.ref.ID, t.ref.Kind
}

// fetchLive makes sure an unsaved target has its live data: one Details
// call on a cache miss, kept in the overview cache (marked DetailsOnly) so
// the dialog and its POST cost one source call together.
func (s *server) fetchLive(ctx context.Context, t *addTarget) error {
	if t.saved != nil || t.haveData {
		return nil
	}
	src := s.source(t.man.ID, t.ref.Source)
	if src == nil {
		return errNoSource
	}
	d, err := src.Details(ctx, t.ref.Kind, t.ref.ID)
	if err != nil {
		return err
	}
	s.cache.put(liveKey(t.man.ID, t.ref.Source, t.ref.Kind, t.ref.ID), cachedDetail{Data: d, DetailsOnly: true})
	t.setData(d)
	return nil
}

// addFailure handles a resolveAdd or fetchLive error: source outages
// re-render the dialog (keeping the form) with a readable message, the rest
// use error pages.
func (s *server) addFailure(w http.ResponseWriter, r *http.Request, m plugin.Manifest, ref itemRef, v entryValues, err error) {
	retry := itemURL(m, ref.Source, ref.ID, ref.Kind)
	if !errors.Is(err, plugin.ErrRateLimited) && !errors.Is(err, plugin.ErrUnavailable) {
		s.itemFail(w, r, m, ref.Source, err, retry)
		return
	}
	log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
	msg := sourceMessage(m, ref.Source, err)
	if !isPartialRequest(r) {
		s.errorPage(w, r, http.StatusServiceUnavailable, "Temporarily unavailable", msg, retry)
		return
	}
	k := kindFor(m, ref)
	f := newEntryForm(m, k, ref.Source, ref.ID, progressContext{Keys: k.Progress.Defaults}, v)
	f.Error = msg
	s.renderPartialStatus(w, http.StatusServiceUnavailable, "item", "add_dialog", f)
}

// addDialog writes the dialog (htmx) or the overview with the dialog open
// (no JavaScript).
func (s *server) addDialog(w http.ResponseWriter, r *http.Request, t *addTarget, f entryForm, status int) {
	if isPartialRequest(r) {
		s.renderPartialStatus(w, status, "item", "add_dialog", f)
		return
	}
	s.showItem(w, r, t.ref, itemExtras{Dialog: &f, UseCache: true}, status)
}

func (s *server) getAdd(w http.ResponseWriter, r *http.Request, pluginID string) {
	m, ref, ok := s.addRef(w, r, pluginID)
	if !ok {
		return
	}
	v := entryValues{Status: string(library.Planned)}
	if !isPartialRequest(r) {
		s.showItem(w, r, ref, itemExtras{Dialog: &entryForm{entryValues: v}, UseCache: true}, http.StatusOK)
		return
	}
	ctx := r.Context()
	t, err := s.resolveAdd(ctx, m, ref)
	if err == nil {
		err = s.fetchLive(ctx, t)
	}
	if err != nil {
		s.addFailure(w, r, m, ref, v, err)
		return
	}
	f := t.form(v)
	if t.entry != nil {
		f.Error, f.ExistingURL = alreadyInList(t.kind), itemURL(m, t.ref.Source, t.ref.ID, t.kind.Key)
		s.renderPartialStatus(w, http.StatusConflict, "item", "add_dialog", f)
		return
	}
	s.renderPartial(w, "item", "add_dialog", f)
}

// postAdd: resolve the saved item (database only), parse the form
// syntactically (422 without any source call), fetch the live data of an
// unsaved item (cache, else one Details call), validate against the item's
// totals, then save.
func (s *server) postAdd(w http.ResponseWriter, r *http.Request, pluginID string) {
	m, ref, ok := s.addRef(w, r, pluginID)
	if !ok || !parseForm(w, r) {
		return
	}
	ctx := r.Context()
	t, err := s.resolveAdd(ctx, m, ref)
	if err != nil {
		s.addFailure(w, r, m, ref, readEntryValues(kindFor(m, ref), r), err)
		return
	}
	if t.entry != nil {
		s.addConflict(w, r, t, readEntryValues(t.kind, r))
		return
	}
	v, entry, msg := s.parseEntryForm(t.kind, t.keys(), r)
	if msg == "" {
		if err := s.fetchLive(ctx, t); err != nil {
			s.addFailure(w, r, m, t.ref, v, err)
			return
		}
		msg = s.validateAgainstItem(t.kind, t.progress(), entry)
	}
	if msg != "" {
		f := t.form(v)
		f.Error = msg
		s.addDialog(w, r, t, f, http.StatusUnprocessableEntity)
		return
	}
	if t.saved == nil {
		id, err := s.deps.Library.AddToList(ctx, m.ID, t.kind.Key, t.data, entry)
		switch {
		case err == nil:
			s.enqueueSync(ctx, m.ID, id)
			s.addDone(w, r, m.ID)
			return
		case errors.Is(err, library.ErrInvalid):
			f := t.form(v)
			f.Error = invalidMessage(err)
			s.addDialog(w, r, t, f, http.StatusUnprocessableEntity)
			return
		case errors.Is(err, library.ErrAlreadySaved): // saved concurrently: attach (no second job)
			saved, _, ferr := s.findSaved(ctx, m, t.ref)
			if ferr != nil || saved == nil {
				s.itemFail(w, r, m, ref.Source, errors.Join(errors.New("add: re-read after duplicate"), ferr), "")
				return
			}
			t.saved = saved
		default:
			s.itemFail(w, r, m, ref.Source, err, "")
			return
		}
	}
	s.attachEntry(w, r, t, v, entry)
}

// attachEntry gives an already saved item its list entry (no re-save, no
// job) with library.CreateEntry, which inserts only when the item has no
// entry yet: an entry that appeared meanwhile is the same 409 as before.
func (s *server) attachEntry(w http.ResponseWriter, r *http.Request, t *addTarget, v entryValues, entry library.Entry) {
	entry.ItemID = t.saved.ID
	err := s.deps.Library.CreateEntry(r.Context(), entry)
	switch {
	case err == nil:
		s.addDone(w, r, t.man.ID)
	case errors.Is(err, library.ErrEntryExists):
		s.addConflict(w, r, t, v)
	case errors.Is(err, library.ErrInvalid):
		f := t.form(v)
		f.Error = invalidMessage(err)
		s.addDialog(w, r, t, f, http.StatusUnprocessableEntity)
	default:
		s.itemFail(w, r, t.man, t.ref.Source, err, "")
	}
}

// addConflict answers 409: the item is already in the list (with a link).
func (s *server) addConflict(w http.ResponseWriter, r *http.Request, t *addTarget, v entryValues) {
	f := t.form(v)
	f.Error, f.ExistingURL = alreadyInList(t.kind), itemURL(t.man, t.ref.Source, t.ref.ID, t.kind.Key)
	s.addDialog(w, r, t, f, http.StatusConflict)
}

// enqueueSync enqueues the plugin's sync job of a newly saved item, if the
// plugin has one (Deps.SyncJobs). A failure is logged and ignored (the
// item stays pending, as before).
func (s *server) enqueueSync(ctx context.Context, pluginID string, itemID int64) {
	job := s.deps.SyncJobs[pluginID]
	if job == nil {
		return
	}
	if s.deps.Jobs == nil {
		log.Printf("enqueue sync for %s item %d: no job queue", pluginID, itemID)
		return
	}
	kind, payload := job(itemID)
	if err := s.deps.Jobs.Enqueue(ctx, kind, payload); err != nil {
		log.Printf("enqueue sync for %s item %d: %v", pluginID, itemID, err)
	}
}

// addDone redirects to the plugin's list: htmx would swap a 303 target into
// the dialog, so it gets HX-Redirect with 204.
func (s *server) addDone(w http.ResponseWriter, r *http.Request, pluginID string) {
	target := "/" + url.PathEscape(pluginID)
	if isPartialRequest(r) {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

package web

// The generic entry routes, core routes guarded by the plugin of the item
// {id} (itemPluginGuard): POST /list/{id}/increment (+1 on the primary
// progress), GET|POST /list/{id}/edit (the list entry: status, rating,
// dates, comment, progress values and the active progress combination
// among the kind's allowed ones) and POST /list/{id}/remove (deletes the
// item). They never change the item's own data; the edit may only switch
// its active progress combination.

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// progressKeysField is the edit form field naming the active progress
// combination: the keys of one allowed combination joined by commas.
const progressKeysField = "progress_keys"

// entryDialog is the data of entry_dialog.html.
type entryDialog struct {
	entryValues
	Action, CancelURL, Return, Heading, Submit string
	Statuses                                   []statusOpt
	Progress                                   []progressInput
	// Combos offers the kind's allowed progress combinations (only when
	// there are several).
	Combos []statusOpt
	Error  string
}

// entryTarget is the item of an entry route with its list row.
type entryTarget struct {
	man  plugin.Manifest
	kind plugin.KindDef
	row  *library.Row
}

// loadEntry loads the list row of the guarded item; an item without an
// entry gets the 404 page.
func (s *server) loadEntry(w http.ResponseWriter, r *http.Request) (*entryTarget, bool) {
	ref := entryOf(r)
	ctx := r.Context()
	row, err := s.deps.Library.Row(ctx, ref.ID, s.deps.Settings.TitleLang(ctx))
	if err != nil {
		s.internalError(w, r, err)
		return nil, false
	}
	m, ok := s.deps.Registry.Manifest(ref.PluginID)
	if row == nil || !ok {
		s.entryNotFound(w, r)
		return nil, false
	}
	k, ok := kindDef(m, row.Item.Kind)
	if !ok {
		s.entryNotFound(w, r)
		return nil, false
	}
	return &entryTarget{man: m, kind: k, row: row}, true
}

// done answers a successful change: HX-Redirect with 204 for htmx (a 303
// target would be swapped into the dialog), 303 otherwise.
func (s *server) done(w http.ResponseWriter, r *http.Request, ret string) {
	if isPartialRequest(r) {
		w.Header().Set("HX-Redirect", ret)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, ret, http.StatusSeeOther)
}

// postIncrement adds 1 to the item's primary progress (capped at a known
// total). For htmx it answers the re-rendered row, else 303 to the return
// path. A kind without a primary key, or whose primary key is not active
// for the item, makes it a no-op that answers the unchanged row (the +1
// button is not shown for such rows anyway).
func (s *server) postIncrement(w http.ResponseWriter, r *http.Request) {
	ref := entryOf(r)
	if !parseForm(w, r) {
		return
	}
	ret := safeReturn(r.PostFormValue("return"), ref.PluginID)
	if err := s.deps.Library.Increment(r.Context(), ref.ID); err != nil {
		if errors.Is(err, library.ErrNoEntry) {
			s.entryNotFound(w, r)
			return
		}
		s.internalError(w, r, err)
		return
	}
	if !isPartialRequest(r) {
		http.Redirect(w, r, ret, http.StatusSeeOther)
		return
	}
	t, ok := s.loadEntry(w, r)
	if !ok {
		return
	}
	s.renderPartial(w, "list", "list_row", s.rowView(t.man, *t.row, ret))
}

// comboValue encodes a progress combination for the form.
func comboValue(keys []string) string { return strings.Join(keys, ",") }

// allowedCombo finds the kind's allowed combination posted as raw (the keys
// in any order, each once); ok is false when raw names none.
func allowedCombo(k plugin.KindDef, raw string) ([]string, bool) {
	keys := strings.Split(raw, ",")
	sorted := slices.Clone(keys)
	slices.Sort(sorted)
	if len(slices.Compact(slices.Clone(sorted))) != len(sorted) {
		return nil, false
	}
	for _, c := range k.Progress.Combinations {
		cs := slices.Clone(c)
		slices.Sort(cs)
		if slices.Equal(cs, sorted) {
			return slices.Clone(c), true
		}
	}
	return nil, false
}

// comboLabel names a combination by its tracks' labels: "Season + Episode".
func comboLabel(k plugin.KindDef, keys []string) string {
	labels := make([]string, 0, len(keys))
	for _, key := range keys {
		if def, ok := progressDefOf(k, key); ok {
			labels = append(labels, def.Label)
		} else {
			labels = append(labels, key)
		}
	}
	return strings.Join(labels, " + ")
}

// entryFormValues prefills the edit form from a stored entry: progress as
// typed back by the parser (durations h:mm:ss, the rest and durations above
// 999:59:59 whole numbers), rating blank when unrated.
func entryFormValues(k plugin.KindDef, e library.Entry) entryValues {
	v := entryValues{Status: string(e.Status), StartedOn: fmtDay(e.StartedOn), FinishedOn: fmtDay(e.FinishedOn),
		Comment: e.Comment, Progress: map[string]string{}}
	if e.Rating != nil {
		v.Rating = strconv.Itoa(*e.Rating)
	}
	for key, val := range e.Progress {
		def, ok := progressDefOf(k, key)
		if !ok {
			continue
		}
		if def.Type == plugin.Duration && val <= maxShownSeconds {
			v.Progress[key] = fmtDuration(val)
		} else { // beyond the h:mm:ss display cap: plain seconds round-trip exactly
			v.Progress[key] = fmtNum(max(val, 0))
		}
	}
	return v
}

// dialog builds the edit dialog of t from the values v, showing the inputs
// of the combination keys (the active one, or the one just posted).
func (t *entryTarget) dialog(v entryValues, keys []string, ret string) entryDialog {
	k := t.kind
	d := entryDialog{entryValues: v, Action: "/list/" + strconv.FormatInt(t.row.Item.ID, 10) + "/edit",
		CancelURL: ret, Return: ret, Heading: "Edit list entry", Submit: "Save"}
	for _, st := range library.Statuses() {
		d.Statuses = append(d.Statuses, statusOpt{Value: string(st), Label: statusLabel(k, st), Selected: string(st) == v.Status})
	}
	d.Progress = progressInputs(k, progressContext{Keys: keys, Fields: t.row.Item.Fields}, v.Progress)
	if len(k.Progress.Combinations) > 1 {
		for _, c := range k.Progress.Combinations {
			d.Combos = append(d.Combos, statusOpt{Value: comboValue(c), Label: comboLabel(k, c), Selected: sameSet(c, keys)})
		}
	}
	return d
}

func sameSet(a, b []string) bool {
	as, bs := slices.Clone(a), slices.Clone(b)
	slices.Sort(as)
	slices.Sort(bs)
	return slices.Equal(as, bs)
}

// editResponse writes the edit dialog (htmx) or the full list page of the
// item's plugin with the dialog open (no JavaScript). The list shown is the
// one the return path names (its kind, status and page), else the first
// page of the item's kind.
func (s *server) editResponse(w http.ResponseWriter, r *http.Request, t *entryTarget, d entryDialog, code int) {
	if isPartialRequest(r) {
		s.renderPartialStatus(w, code, "list", "entry_dialog", d)
		return
	}
	lq := listQuery{Kind: t.kind.Key, Page: 1}
	if u, err := url.Parse(d.Return); err == nil && u.Path == "/"+t.man.ID {
		lq = parseListQuery(t.man, u.Query())
	}
	s.showList(w, r, t.man, lq, &d, code)
}

func (s *server) getEdit(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadEntry(w, r)
	if !ok {
		return
	}
	ret := safeReturn(r.URL.Query().Get("return"), t.man.ID)
	d := t.dialog(entryFormValues(t.kind, t.row.Entry), t.row.Item.ProgressKeys, ret)
	s.editResponse(w, r, t, d, http.StatusOK)
}

// postEdit validates the whole form before anything is written: the
// combination must be one of the kind's allowed ones (absent keeps the
// active one); the values are parsed against the active and the new keys
// (so a dialog showing the old inputs can post a switch), only the new
// combination's values are written (values of keys becoming inactive are
// retained as they were), and library.UpdateEntry switches the combination
// and writes the entry in one transaction, validated against the new keys.
func (s *server) postEdit(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	t, ok := s.loadEntry(w, r)
	if !ok {
		return
	}
	ret := safeReturn(r.PostFormValue("return"), t.man.ID)
	active := t.row.Item.ProgressKeys
	keys, change := active, []string(nil)
	var msg string
	if raw := strings.TrimSpace(r.PostFormValue(progressKeysField)); raw != "" {
		if c, ok := allowedCombo(t.kind, raw); !ok {
			msg = "Please choose one of the offered progress combinations."
		} else if !sameSet(c, active) {
			keys, change = c, c
		}
	}
	union := slices.Clone(active)
	for _, k := range keys {
		if !slices.Contains(union, k) {
			union = append(union, k)
		}
	}
	v, entry, perr := s.parseEntryForm(t.kind, union, r)
	if msg == "" {
		msg = perr
	}
	if msg == "" {
		for k := range entry.Progress {
			if !slices.Contains(keys, k) {
				delete(entry.Progress, k)
			}
		}
		msg = s.validateAgainstItem(t.kind, progressContext{Keys: keys, Fields: t.row.Item.Fields}, entry)
	}
	if msg == "" {
		entry.ItemID = t.row.Item.ID
		err := s.deps.Library.UpdateEntry(r.Context(), entry, change)
		switch {
		case err == nil:
			s.done(w, r, ret)
			return
		case errors.Is(err, library.ErrNoEntry):
			s.entryNotFound(w, r)
			return
		case errors.Is(err, library.ErrInvalid):
			msg = invalidMessage(err)
		default:
			s.internalError(w, r, err)
			return
		}
	}
	d := t.dialog(v, keys, ret)
	d.Error = msg
	s.editResponse(w, r, t, d, http.StatusUnprocessableEntity)
}

// postRemove deletes the item with everything attached to it (entry,
// progress, titles, credit links); people and image files stay.
func (s *server) postRemove(w http.ResponseWriter, r *http.Request) {
	ref := entryOf(r)
	if !parseForm(w, r) {
		return
	}
	if err := s.deps.Library.DeleteItem(r.Context(), ref.ID); err != nil {
		if errors.Is(err, library.ErrNoEntry) {
			s.entryNotFound(w, r)
			return
		}
		s.internalError(w, r, err)
		return
	}
	s.done(w, r, safeReturn(r.PostFormValue("return"), ref.PluginID))
}

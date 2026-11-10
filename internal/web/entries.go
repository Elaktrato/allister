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
	m, ok := s.deps.Registry.Manifest(ref.PluginID)
	if !ok {
		s.entryNotFound(w, r)
		return nil, false
	}
	row, err := s.deps.Library.Row(ctx, ref.ID, s.titleLang(ctx, m))
	if err != nil {
		s.internalError(w, r, err)
		return nil, false
	}
	if row == nil {
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
	rv := s.rowView(t.man, *t.row, ret, s.visibleColumns(r.Context(), t.man, t.kind))
	// a +1 from a card answers the card, one from the table the row
	partial := "list_row"
	if r.PostFormValue("view") == "cards" {
		partial = "list_card"
	}
	s.renderPartial(w, "list", partial, rv)
}

// comboValue encodes a progress combination for the form.
func comboValue(keys []string) string { return strings.Join(keys, ",") }

// comboLabel names a combination by its tracks' labels: "Part + Unit".
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

// progressText formats a stored progress value as the parser reads it
// back: durations h:mm:ss (plain seconds above the 999:59:59 display cap,
// so they round-trip exactly), everything else a whole number.
func progressText(def plugin.ProgressDef, val float64) string {
	if def.Type == plugin.Duration && val <= maxShownSeconds {
		return fmtDuration(val)
	}
	return fmtNum(max(val, 0))
}

// entryFormValues prefills the edit form from a stored entry and the item's
// stored progress values (active and retained), rating blank when unrated.
func entryFormValues(k plugin.KindDef, e library.Entry, stored map[string]float64) entryValues {
	v := entryValues{Status: string(e.Status), StartedOn: fmtDay(e.StartedOn), FinishedOn: fmtDay(e.FinishedOn),
		Comment: e.Comment, Progress: map[string]string{}}
	if e.Rating != nil {
		v.Rating = strconv.Itoa(*e.Rating)
	}
	for key, val := range stored {
		if def, ok := progressDefOf(k, key); ok {
			v.Progress[key] = progressText(def, val)
		}
	}
	return v
}

// combinationTracks are the keys of every track used by one of the kind's
// allowed combinations, in manifest order, each once.
func combinationTracks(k plugin.KindDef) []string {
	var out []string
	for _, def := range k.Progress.Eligible {
		for _, c := range k.Progress.Combinations {
			if slices.Contains(c, def.Key) {
				out = append(out, def.Key)
				break
			}
		}
	}
	return out
}

// dialog builds the edit dialog of t from the values v for the combination
// keys (the active one, or the one just posted). A kind with several allowed
// combinations gets an input for EVERY track of its combinations, so a
// switch and the new track's value fit in one save: tracks outside keys are
// muted, name a combination that uses them and show their stored (retained)
// value, never a typed one (those are ignored on save).
func (t *entryTarget) dialog(v entryValues, keys []string, ret string, stored map[string]float64) entryDialog {
	k := t.kind
	d := entryDialog{entryValues: v, Action: "/list/" + strconv.FormatInt(t.row.Item.ID, 10) + "/edit",
		CancelURL: ret, Return: ret, Heading: "Edit list entry", Submit: "Save"}
	for _, st := range library.Statuses() {
		d.Statuses = append(d.Statuses, statusOpt{Value: string(st), Label: statusLabel(k, st), Selected: string(st) == v.Status})
	}
	shown := keys
	if len(k.Progress.Combinations) > 1 {
		shown = combinationTracks(k)
	}
	values := map[string]string{}
	for _, key := range shown {
		if slices.Contains(keys, key) {
			values[key] = v.Progress[key]
		} else if val, ok := stored[key]; ok {
			def, _ := progressDefOf(k, key)
			values[key] = progressText(def, val)
		}
	}
	d.Progress = progressInputs(k, progressContext{Keys: shown, Fields: t.row.Item.Fields}, values)
	for i := range d.Progress {
		key := strings.TrimPrefix(d.Progress[i].Name, progressField)
		if slices.Contains(keys, key) {
			continue
		}
		d.Progress[i].Inactive = true
		for _, c := range k.Progress.Combinations {
			if slices.Contains(c, key) {
				d.Progress[i].Hint = "used when tracking " + comboLabel(k, c)
				break
			}
		}
	}
	if len(k.Progress.Combinations) > 1 {
		cur := library.CombinationIndex(k, keys)
		for i, c := range k.Progress.Combinations {
			d.Combos = append(d.Combos, statusOpt{Value: comboValue(c), Label: comboLabel(k, c), Selected: i == cur})
		}
	}
	return d
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
	stored, err := s.deps.Library.StoredProgress(r.Context(), t.row.Item.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	ret := safeReturn(r.URL.Query().Get("return"), t.man.ID)
	d := t.dialog(entryFormValues(t.kind, t.row.Entry, stored), t.row.Item.ProgressKeys, ret, stored)
	s.editResponse(w, r, t, d, http.StatusOK)
}

// postEdit validates the whole form before anything is written:
//   - progress_keys must name one of the kind's allowed combinations (absent
//     keeps the active one); that is the TARGET combination;
//   - inputs of the kind's tracks outside the target are ignored (not even
//     parsed): their stored values stay as they are;
//   - an input of a target track that is posted BLANK clears it to 0 (a
//     field that is not posted at all keeps its value), in the same write;
//   - the target values are checked against the item's totals and
//     library.UpdateEntry switches the combination and writes the entry in
//     one transaction.
//
// The save is last-write-wins: the dialog posts the values it loaded, so a
// +1 made meanwhile (another tab) is overwritten by the posted value.
func (s *server) postEdit(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	t, ok := s.loadEntry(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	stored, err := s.deps.Library.StoredProgress(ctx, t.row.Item.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	ret := safeReturn(r.PostFormValue("return"), t.man.ID)
	keys, change := t.row.Item.ProgressKeys, []string(nil)
	var msg string
	if raw := strings.TrimSpace(r.PostFormValue(progressKeysField)); raw != "" {
		if i := library.CombinationIndex(t.kind, strings.Split(raw, ",")); i < 0 {
			msg = "Please choose one of the offered progress combinations."
		} else {
			keys = slices.Clone(t.kind.Progress.Combinations[i])
			change = keys // UpdateEntry leaves an unchanged combination alone
		}
	}
	var clear []string
	for _, def := range t.kind.Progress.Eligible {
		name := progressField + def.Key
		vals, posted := r.PostForm[name]
		switch {
		case !slices.Contains(keys, def.Key):
			delete(r.PostForm, name) // ignored: not part of the target combination
		case posted && len(vals) > 0 && strings.TrimSpace(vals[0]) == "":
			clear = append(clear, def.Key)
		}
	}
	v, entry, perr := s.parseEntryForm(t.kind, keys, r)
	if msg == "" {
		msg = perr
	}
	if msg == "" {
		for _, key := range clear {
			if _, typed := entry.Progress[key]; !typed {
				entry.Progress[key] = 0
			}
		}
		msg = s.validateAgainstItem(t.kind, progressContext{Keys: keys, Fields: t.row.Item.Fields}, entry)
	}
	if msg == "" {
		entry.ItemID = t.row.Item.ID
		err := s.deps.Library.UpdateEntry(ctx, entry, change)
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
	d := t.dialog(v, keys, ret, stored)
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

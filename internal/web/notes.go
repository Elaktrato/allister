package web

// The unit notes of a run: GET|POST /list/{id}/runs/{run}/notes/{key}/{unit}
// shows and saves the note on unit {unit} (1-based) of track {key}, a count
// or pages track of the item's kind; a unit above the item's total, another
// track type, or a stale run is the 404 page. An empty body deletes the
// note. The form opens from the run's timeline and from the answer of a +1
// ("Note on <Label> <n>" for the unit just logged).

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

// noteDialog is the data of unit_note_dialog (run_history.html).
type noteDialog struct {
	Action, CancelURL, Return, Heading string
	Body                               string
	MaxBody                            int // the body's maxlength
	Error                              string
}

// noteTarget is one unit of a guarded run that takes a note.
type noteTarget struct {
	*runTarget
	def  plugin.ProgressDef
	unit int
}

// takesNotes reports whether a track of type t takes unit notes.
func takesNotes(t plugin.ProgressType) bool { return t == plugin.Count || t == plugin.Pages }

// unitNoteURL is the note form of one unit of run runID of item itemID.
func unitNoteURL(itemID, runID int64, key string, unit int) string {
	return "/list/" + strconv.FormatInt(itemID, 10) + "/runs/" + strconv.FormatInt(runID, 10) +
		"/notes/" + url.PathEscape(key) + "/" + strconv.Itoa(unit)
}

// unitNoteLink is unitNoteURL with the return path ret.
func unitNoteLink(itemID, runID int64, key string, unit int, ret string) string {
	return unitNoteURL(itemID, runID, key, unit) + "?" + url.Values{"return": {ret}}.Encode()
}

// unitLabel names one unit: "<Label> 5".
func unitLabel(def plugin.ProgressDef, unit int) string { return def.Label + " " + strconv.Itoa(unit) }

// loadNote loads the guarded run and checks {key} and {unit}; anything that
// takes no note gets the 404 page.
func (s *server) loadNote(w http.ResponseWriter, r *http.Request) (*noteTarget, bool) {
	t, ok := s.loadRun(w, r)
	if !ok {
		return nil, false
	}
	def, ok := progressDef(t.kind, r.PathValue("key"))
	raw := r.PathValue("unit")
	var unit int
	if isASCIIDigits(raw, maxIDDigits) {
		unit, _ = strconv.Atoi(raw)
	}
	if !ok || !takesNotes(def.Type) || unit < 1 {
		s.noteNotFound(w, r)
		return nil, false
	}
	if def.TotalField != "" {
		if v, ok := t.row.Item.Fields[def.TotalField]; ok && v.Num != nil && *v.Num > 0 && float64(unit) > *v.Num {
			s.noteNotFound(w, r)
			return nil, false
		}
	}
	return &noteTarget{runTarget: t, def: def, unit: unit}, true
}

func (s *server) noteNotFound(w http.ResponseWriter, r *http.Request) {
	s.errorPage(w, r, http.StatusNotFound, "Not found", "This unit of the run takes no note.", "")
}

// dialog completes the note form d for the target.
func (t *noteTarget) dialog(d noteDialog, ret string) noteDialog {
	d.Action = unitNoteURL(t.row.Item.ID, t.run.ID, t.def.Key, t.unit)
	d.CancelURL, d.Return = ret, ret
	d.Heading = "Note on " + unitLabel(t.def, t.unit)
	d.MaxBody = library.MaxUnitNoteRunes
	return d
}

// noteResponse writes the note form (htmx) or the item page with it open.
func (s *server) noteResponse(w http.ResponseWriter, r *http.Request, t *noteTarget, d noteDialog, code int) {
	if isPartialRequest(r) {
		s.renderPartialStatus(w, code, "item", "unit_note_dialog", d)
		return
	}
	it := t.row.Item
	s.showItem(w, r, itemRef{Plugin: t.man.ID, Source: it.Source, ID: it.SourceID, Kind: it.Kind}, itemExtras{NoteDialog: &d}, code)
}

func (s *server) getUnitNote(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadNote(w, r)
	if !ok {
		return
	}
	notes, err := s.deps.Library.UnitNotes(r.Context(), t.run.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	var d noteDialog
	if i := slices.IndexFunc(notes, func(n library.UnitNote) bool { return n.Key == t.def.Key && n.Unit == t.unit }); i >= 0 {
		d.Body = notes[i].Body
	}
	s.noteResponse(w, r, t, t.dialog(d, t.runReturn(r.URL.Query().Get("return"))), http.StatusOK)
}

// postUnitNote saves the note (an empty body deletes it); a refused body
// (too long, control characters) answers the form with the message (422).
func (s *server) postUnitNote(w http.ResponseWriter, r *http.Request) {
	if !parseTextForm(w, r) {
		return
	}
	t, ok := s.loadNote(w, r)
	if !ok {
		return
	}
	d := noteDialog{Body: lfBreaks(strings.TrimSpace(r.PostFormValue("body")))}
	ret := t.runReturn(r.PostFormValue("return"))
	err := s.deps.Library.SetUnitNote(r.Context(), t.run.ID, t.def.Key, t.unit, d.Body)
	switch {
	case err == nil:
		s.done(w, r, ret)
		return
	case errors.Is(err, library.ErrNoRun):
		s.runNotFound(w, r)
		return
	case errors.Is(err, library.ErrInvalid):
		d.Error = invalidMessage(err)
	default:
		s.internalError(w, r, err)
		return
	}
	s.noteResponse(w, r, t, t.dialog(d, ret), http.StatusUnprocessableEntity)
}

// noteOffer is the "Note on <Label> <n>" link of a +1 answer: the unit the
// primary track of t's display run is at now, when that track is a count or
// pages track and active; "" otherwise.
func (t *entryTarget) noteOffer(ret string) (href, label string) {
	pk := t.kind.Progress.PrimaryKey
	def, ok := progressDef(t.kind, pk)
	if !ok || !takesNotes(def.Type) || !slices.Contains(t.row.Item.ProgressKeys, pk) {
		return "", ""
	}
	n := t.row.Entry.Progress[pk]
	if n < 1 || !isWhole(n) || n > float64(maxUnit) {
		return "", ""
	}
	unit := int(n)
	return unitNoteLink(t.row.Item.ID, t.row.Entry.RunID, pk, unit, ret), "Note on " + unitLabel(def, unit)
}

// maxUnit is the largest unit a note link names (the library's progress cap).
const maxUnit = 1e12

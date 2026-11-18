package web

// The read-only timeline of one run: GET /list/{id}/runs/{run}/timeline
// answers the fragment the item page's run history loads when a run's
// timeline is opened. The wording comes from the plugin's labels: a count or
// pages step reads "<Label> 5", a jump "<LabelPlural> 5-7"; other changes
// show their values. A unit note reads "Note on <Label> 5" with its
// formatted text and an edit link; a forward count or pages step without a
// note on the unit it reached offers one. Times are in the configured time
// zone.

import (
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// timelineStamp is the layout of a timeline row's time.
const timelineStamp = "2006-01-02 15:04"

// timelineRow is one line of the timeline.
type timelineRow struct {
	When, WhenISO string // local time shown; UTC RFC 3339 for the time element
	Text          string
	Origin        string // "" for a manual change, else the marker ("Imported", "Restored")
	Note          string // a unit note's text (rendered with richtext)
	// NoteURL is the note form of the row's unit: "Edit" on a note, "Add
	// note" (NoteLabel names the unit for screen readers) on a step.
	NoteURL, NoteLabel string
}

// timelineData is the data of the timeline template.
type timelineData struct {
	Rows []timelineRow
}

func (s *server) getTimeline(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadRun(w, r)
	if !ok {
		return
	}
	entries, err := s.deps.Library.Timeline(r.Context(), t.run.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	ret := t.overview()
	link := func(key string, unit int) string { return unitNoteLink(t.row.Item.ID, t.run.ID, key, unit, ret) }
	s.renderPartial(w, "item", "timeline", timelineData{Rows: timelineRows(t.kind, entries, s.location(r.Context()), link)})
}

// timelineRows words the entries (already oldest first) for kind k; link
// is the note form of one unit.
func timelineRows(k plugin.KindDef, entries []library.TimelineEntry, loc *time.Location, link func(key string, unit int) string) []timelineRow {
	noted := map[string]bool{} // "key/unit" of the run's notes
	for _, e := range entries {
		if e.Note != nil {
			noted[e.Key+"/"+strconv.Itoa(e.Note.Unit)] = true
		}
	}
	rows := make([]timelineRow, 0, len(entries))
	for _, e := range entries {
		row := timelineRow{When: e.At.In(loc).Format(timelineStamp), WhenISO: e.At.UTC().Format(time.RFC3339), Text: timelineText(k, e)}
		if e.Note != nil {
			row.Note, row.NoteURL = e.Note.Body, link(e.Key, e.Note.Unit)
		} else if unit, def, ok := steppedUnit(k, e); ok && !noted[e.Key+"/"+strconv.Itoa(unit)] {
			row.NoteURL, row.NoteLabel = link(e.Key, unit), "Add a note on "+unitLabel(def, unit)
		}
		switch e.Origin {
		case library.OriginImport:
			row.Origin = "Imported"
		case library.OriginRestore:
			row.Origin = "Restored"
		}
		rows = append(rows, row)
	}
	return rows
}

// timelineText words one change.
func timelineText(k plugin.KindDef, e library.TimelineEntry) string {
	switch e.Type {
	case "progress":
		return timelineProgress(k, e)
	case "status":
		return "Status: " + statusText(k, e.OldText) + " → " + statusText(k, e.NewText)
	case "rating":
		return "Rating: " + ratingText(e.From) + " → " + ratingText(e.To)
	case "note":
		if e.Note == nil {
			break
		}
		def, ok := progressDef(k, e.Key)
		if !ok {
			def = plugin.ProgressDef{Key: e.Key, Label: e.Key}
		}
		return "Note on " + unitLabel(def, e.Note.Unit)
	}
	return e.Type
}

// steppedUnit is the unit a forward progress step of a count or pages track
// reached (the last one of a jump), the one a note can be added to.
func steppedUnit(k plugin.KindDef, e library.TimelineEntry) (int, plugin.ProgressDef, bool) {
	if e.Type != "progress" || e.To == nil {
		return 0, plugin.ProgressDef{}, false
	}
	def, ok := progressDef(k, e.Key)
	from := 0.0
	if e.From != nil {
		from = *e.From
	}
	if !ok || !takesNotes(def.Type) || !isWhole(*e.To) || *e.To <= from || *e.To < 1 || *e.To > maxUnit {
		return 0, def, false
	}
	return int(*e.To), def, true
}

func statusText(k plugin.KindDef, v string) string {
	if v == "" {
		return "–"
	}
	return statusLabel(k, library.Status(v))
}

func ratingText(v *float64) string {
	if v == nil {
		return "–"
	}
	return fmtNum(*v)
}

// timelineProgress words a progress change of track e.Key.
func timelineProgress(k plugin.KindDef, e library.TimelineEntry) string {
	def, ok := progressDef(k, e.Key)
	if !ok { // a track the kind no longer declares
		def = plugin.ProgressDef{Key: e.Key, Type: plugin.Count, Label: e.Key}
	}
	var from, to float64
	if e.From != nil {
		from = *e.From
	}
	if e.To != nil {
		to = *e.To
	}
	if def.Type == plugin.Count || def.Type == plugin.Pages {
		if to > from && isWhole(from) && isWhole(to) {
			if to-from == 1 {
				return def.Label + " " + fmtNum(to)
			}
			return def.PluralLabel() + " " + fmtNum(from+1) + "-" + fmtNum(to)
		}
		return def.Label + " " + fmtNum(from) + " → " + fmtNum(to)
	}
	def.TotalField = ""
	return def.Label + ": " + fmtProgress(def, from, nil) + " → " + fmtProgress(def, to, nil)
}

func progressDef(k plugin.KindDef, key string) (plugin.ProgressDef, bool) {
	for _, d := range k.Progress.Eligible {
		if d.Key == key {
			return d, true
		}
	}
	return plugin.ProgressDef{}, false
}

func isWhole(v float64) bool {
	return v == math.Trunc(v) && math.Abs(v) < 1e15
}

// timelineURL is the fragment's path for one run of item itemID.
func timelineURL(itemID, runID int64) string {
	return "/list/" + strconv.FormatInt(itemID, 10) + "/runs/" + strconv.FormatInt(runID, 10) + "/timeline"
}

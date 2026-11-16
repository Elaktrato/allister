package web

// The read-only timeline of one run: GET /list/{id}/runs/{run}/timeline
// answers the fragment the item page's run history loads when a run's
// timeline is opened. The wording comes from the plugin's labels: a count or
// pages step reads "<Label> 5", a jump "<LabelPlural> 5-7"; other changes
// show their values. Times are in the configured time zone.

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
	s.renderPartial(w, "item", "timeline", timelineData{Rows: timelineRows(t.kind, entries, s.location(r.Context()))})
}

// timelineRows words the entries (already oldest first) for kind k.
func timelineRows(k plugin.KindDef, entries []library.TimelineEntry, loc *time.Location) []timelineRow {
	rows := make([]timelineRow, 0, len(entries))
	for _, e := range entries {
		row := timelineRow{When: e.At.In(loc).Format(timelineStamp), WhenISO: e.At.UTC().Format(time.RFC3339), Text: timelineText(k, e)}
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
	}
	return e.Type
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

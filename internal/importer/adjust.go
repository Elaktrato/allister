package importer

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// buildEntry maps a parsed row to a list entry that passes library
// validation for item (its active progress keys and, through the kind's total
// fields, its totals), noting every adjustment: progress of keys that are not
// active is dropped, invalid values are dropped, fractions are rounded down,
// values above a known total (or above 100 percent) are clamped, an
// out-of-range rating is dropped, a finish date before the start date drops
// both dates, and an over-long comment is cut. Status and comment text are
// left to the library to judge (a rejection fails the row).
func buildEntry(kind plugin.KindDef, item *library.Item, e plugin.ParsedEntry) (library.Entry, []string) {
	var notes []string
	for _, n := range e.Notes {
		if n = plugin.SingleLine(n, maxNoteRunes); n != "" {
			notes = append(notes, n)
		}
	}
	en := library.Entry{Status: library.Status(e.Status), Comment: e.Comment, Progress: map[string]float64{}}
	if utf8.RuneCountInString(en.Comment) > library.MaxCommentRunes {
		en.Comment = string([]rune(en.Comment)[:library.MaxCommentRunes])
		notes = append(notes, fmt.Sprintf("comment cut to %d characters", library.MaxCommentRunes))
	}
	if e.Rating != nil {
		if *e.Rating < 0 || *e.Rating > 10 {
			notes = append(notes, fmt.Sprintf("rating %d is not between 0 and 10, left unrated", *e.Rating))
		} else {
			v := *e.Rating
			en.Rating = &v
		}
	}
	keys := make([]string, 0, len(e.Progress))
	for k := range e.Progress {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := e.Progress[k]
		def, eligible := progressDef(kind, k)
		if !eligible || !slices.Contains(item.ProgressKeys, k) {
			notes = append(notes, fmt.Sprintf("progress %q dropped (not an active progress)", plugin.SingleLine(k, 40)))
			continue
		}
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1e12 {
			notes = append(notes, fmt.Sprintf("invalid progress %q, using 0", k))
			continue
		}
		if v != math.Trunc(v) {
			v = math.Floor(v)
			notes = append(notes, fmt.Sprintf("progress %q rounded down to %d", k, int64(v)))
		}
		if total, bounded := progressBound(def, item); bounded && v > total {
			v = total
			prefix := "progress"
			if len(item.ProgressKeys) > 1 {
				prefix = k + " progress"
			}
			notes = append(notes, fmt.Sprintf("%s clamped to %d", prefix, int64(total)))
		}
		en.Progress[k] = v
	}
	if e.StartedOn != nil {
		en.StartedOn = validDay(*e.StartedOn, "start", &notes)
	}
	if e.FinishedOn != nil {
		en.FinishedOn = validDay(*e.FinishedOn, "finish", &notes)
	}
	if en.StartedOn != nil && en.FinishedOn != nil && en.FinishedOn.Before(*en.StartedOn) {
		en.StartedOn, en.FinishedOn = nil, nil
		notes = append(notes, "dates dropped (finish date was before start date)")
	}
	return en, notes
}

// validDay returns the calendar day of t, or nil with a note when its year
// is outside 1..9999.
func validDay(t time.Time, what string, notes *[]string) *time.Time {
	d := plugin.DateOnly(t)
	if d.Year() < 1 || d.Year() > 9999 {
		*notes = append(*notes, what+" date out of range, left empty")
		return nil
	}
	return &d
}

func progressDef(kind plugin.KindDef, key string) (plugin.ProgressDef, bool) {
	for _, d := range kind.Progress.Eligible {
		if d.Key == key {
			return d, true
		}
	}
	return plugin.ProgressDef{}, false
}

// progressBound mirrors the library: percent is bounded at 100; a count,
// pages or duration track only by a positive number in its total field.
func progressBound(def plugin.ProgressDef, item *library.Item) (float64, bool) {
	if def.Type == plugin.Percent {
		return 100, true
	}
	if def.TotalField == "" {
		return 0, false
	}
	if v, ok := item.Fields[def.TotalField]; ok && v.Num != nil && *v.Num > 0 {
		return math.Floor(*v.Num), true
	}
	return 0, false
}

func truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}

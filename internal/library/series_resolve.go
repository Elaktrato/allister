package library

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/allister/allister/internal/plugin"
)

// ResolvedSeries is a series with each value resolved: the series' own value,
// else the linked entry's mapped value, else the derived value, else
// nothing. MainParts and Completed count the main parts only; PartsTotal is
// the expected number of parts (the number of main parts when neither the
// series nor its linked entry sets one); Status is derived from the main
// parts and the set expected number.
type ResolvedSeries struct {
	Series                             Series
	Cover, Description                 string
	StartedOn, EndedOn                 *time.Time
	Finished                           bool
	PartsTotal                         int
	Fields                             map[string]plugin.FieldValue
	MainParts, SpinOffParts, Completed int
	Status                             Status
}

// ResolveSeries resolves s (of kind) from its own data, the linked item and
// its entry (nil when not linked) and the main parts' rows in order. It
// reads no database: SpinOffParts is left to the caller. The linked item
// counts only when kind declares a SeriesLink of the item's kind; it fills
// the description, dates and expected parts through the link's fields, each
// series field from its same-named field, and "finished" when the owner's
// marker is not set but it has an end date. Derived values come from the
// main parts: the start is the earliest date of the kind's release field,
// the end the latest one when the series is finished, the expected parts
// their number. The cover is the own cover, else the linked cover, else the
// first main part's cover. linkedEntry is accepted for callers that load it;
// the resolution reads the linked item's fields only.
func ResolveSeries(s Series, kind plugin.KindDef, linked *Item, linkedEntry *Entry, main []Row) ResolvedSeries {
	r := ResolvedSeries{Series: s, MainParts: len(main), Fields: map[string]plugin.FieldValue{}}
	link := kind.SeriesLink
	if link == nil || linked == nil || linked.Kind != link.Kind {
		link, linked = nil, nil
	}
	linkedValue := func(key string) plugin.FieldValue {
		if link == nil || key == "" {
			return plugin.FieldValue{}
		}
		return linked.Fields[key]
	}

	r.Cover = s.CoverPath
	if r.Cover == "" && linked != nil {
		r.Cover = linked.CoverPath
	}
	if r.Cover == "" && len(main) > 0 {
		r.Cover = main[0].Item.CoverPath
	}

	r.Description = s.Description
	if r.Description == "" && link != nil {
		if v := linkedValue(link.DescriptionField).Text; strings.TrimSpace(v) != "" {
			r.Description = v
		}
	}

	var linkedEnd *time.Time
	if link != nil {
		linkedEnd = linkedValue(link.EndField).Date
	}
	r.Finished = s.Finished || linkedEnd != nil

	first, last := releaseRange(kind, main)
	r.StartedOn = s.StartedOn
	if r.StartedOn == nil && link != nil {
		r.StartedOn = linkedValue(link.StartField).Date
	}
	if r.StartedOn == nil {
		r.StartedOn = first
	}
	r.EndedOn = s.EndedOn
	if r.EndedOn == nil {
		r.EndedOn = linkedEnd
	}
	if r.EndedOn == nil && r.Finished {
		r.EndedOn = last
	}

	expected := expectedParts(s, kind, linked)
	r.PartsTotal = expected
	if r.PartsTotal == 0 {
		r.PartsTotal = len(main)
	}

	for k, v := range s.Fields {
		r.Fields[k] = v
	}
	if link != nil {
		for _, f := range kind.SeriesFields {
			if _, own := r.Fields[f.Key]; own {
				continue
			}
			if v, ok := linked.Fields[f.Key]; ok && fieldValueSet(f.Type, v) {
				r.Fields[f.Key] = v
			}
		}
	}

	statuses := make([]Status, len(main))
	for i, row := range main {
		statuses[i] = row.Entry.Status
		if row.Entry.Status == Completed {
			r.Completed++
		}
	}
	r.Status = DeriveSeriesStatus(statuses, expected)
	return r
}

// expectedParts is the set expected number of parts of s (0 = not set): its
// own, else the linked item's parts field rounded down (negative is not
// set, capped like the own value). The SQL of ActiveSeries mirrors it.
func expectedParts(s Series, kind plugin.KindDef, linked *Item) int {
	if s.PartsTotal > 0 {
		return s.PartsTotal
	}
	link := kind.SeriesLink
	if link == nil || link.PartsField == "" || linked == nil || linked.Kind != link.Kind {
		return 0
	}
	n := linked.Fields[link.PartsField].Num
	if n == nil || math.IsNaN(*n) || *n < 1 {
		return 0
	}
	return int(min(math.Floor(*n), maxSeriesPartsTotal))
}

// releaseRange is the earliest and latest date of the kind's release field
// over the rows (nil without a release field or dates).
func releaseRange(kind plugin.KindDef, rows []Row) (first, last *time.Time) {
	if kind.SeriesHint == nil || kind.SeriesHint.ReleaseField == "" {
		return nil, nil
	}
	for _, row := range rows {
		d := row.Item.Fields[kind.SeriesHint.ReleaseField].Date
		if d == nil {
			continue
		}
		if first == nil || d.Before(*first) {
			first = d
		}
		if last == nil || d.After(*last) {
			last = d
		}
	}
	return first, last
}

// fieldValueSet reports whether v holds a value of type t.
func fieldValueSet(t plugin.FieldType, v plugin.FieldValue) bool {
	switch t {
	case plugin.FieldNumber:
		return v.Num != nil
	case plugin.FieldDate:
		return v.Date != nil
	case plugin.FieldList:
		return len(v.List) > 0
	}
	return strings.TrimSpace(v.Text) != ""
}

// LoadResolvedSeries loads what s needs for ResolveSeries (its main parts'
// rows with titles for lang, the linked item and its entry) and resolves it,
// with the number of spin-off parts.
func (l *Library) LoadResolvedSeries(ctx context.Context, s *Series, lang string) (ResolvedSeries, error) {
	kind, err := l.kindDef(s.PluginID, s.Kind)
	if err != nil {
		return ResolvedSeries{}, err
	}
	order, err := l.SeriesPartsOrder(ctx, s.ID)
	if err != nil {
		return ResolvedSeries{}, err
	}
	rows, err := l.SeriesParts(ctx, s.ID, lang)
	if err != nil {
		return ResolvedSeries{}, err
	}
	spinOff := map[int64]bool{}
	spinOffs := 0
	for _, p := range order {
		if p.SpinOff {
			spinOff[p.ItemID] = true
			spinOffs++
		}
	}
	main := make([]Row, 0, len(rows))
	for _, row := range rows {
		if !spinOff[row.Item.ID] {
			main = append(main, row)
		}
	}
	var linked *Item
	var linkedEntry *Entry
	if s.LinkedItemID != 0 {
		if linked, err = l.Item(ctx, s.LinkedItemID); err != nil {
			return ResolvedSeries{}, fmt.Errorf("series linked entry: %w", err)
		}
		if linked != nil {
			if linkedEntry, err = l.Entry(ctx, s.LinkedItemID); err != nil {
				return ResolvedSeries{}, fmt.Errorf("series linked entry: %w", err)
			}
		}
	}
	r := ResolveSeries(*s, kind, linked, linkedEntry, main)
	r.SpinOffParts = spinOffs
	return r, nil
}

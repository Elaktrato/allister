// Package stats aggregates library rows into the numbers of the Statistics
// page. It is pure: it takes rows and their kind declarations, applies the
// period, and returns totals, buckets and dimensions. All media semantics
// come from plugin.StatsDef as data; the package names no media kind.
package stats

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// BucketMode selects the width of the time buckets of a report.
type BucketMode string

const (
	// BucketAuto uses months for a resolved span of up to 24 calendar months
	// and years above that.
	BucketAuto BucketMode = "auto"
	// BucketMonth uses one bucket per calendar month.
	BucketMonth BucketMode = "month"
	// BucketYear uses one bucket per calendar year.
	BucketYear BucketMode = "year"
)

// Input is one media kind of the report: its plugin, its declaration and the
// rows already narrowed by every filter except the period.
type Input struct {
	PluginID string
	Kind     plugin.KindDef
	Rows     []library.Row
}

// Options narrows the report. From and To are calendar days of the entry's
// finished date and both ends are inclusive; nil means open. With both nil the
// report covers all time. Bucket is the requested bucket width; an empty or
// unknown value means BucketAuto, and all time always uses years.
type Options struct {
	From, To *time.Time
	Bucket   BucketMode
}

// Totals are the summed numbers of one kind or of every kind. Statuses holds
// the five fixed classes (absent ones as zero). Rated counts entries with a
// stored rating, so Entries-Rated is the unrated count. Rating sum and average
// exclude unrated entries; an explicit 0 is a real rating. TimeEntries and
// UnitEntries count the entries that contributed a value above zero and are
// the denominators of the averages. MissingTime counts entries whose declared
// time metric could not be computed. UndatedCompleted counts completed entries
// without a finished date. Units are never summed across kinds in Report.All.
type Totals struct {
	Entries, Completed, Rated, RatingSum, TimeEntries, MissingTime, UnitEntries, UndatedCompleted int
	Statuses                                                                                      map[plugin.StatusClass]int
	RatingAverage                                                                                 float64
	RatingDistribution                                                                            [11]int
	TimeSeconds, Units                                                                            float64
	TimeLabel, UnitLabel                                                                          string
}

// Bucket is one month ("2006-01") or year ("2006") of the time chart with the
// seconds that were consumed by entries finished in it. Buckets are
// continuous; empty ones carry zero.
type Bucket struct {
	Label   string
	Seconds float64
}

// Dimension is one genre, tag or field value with the entries that carry it.
type Dimension struct {
	Value                     string
	Entries, Rated, RatingSum int
	TimeSeconds, Units        float64
}

// DimensionGroup is the breakdown by one term or field. Label is the display
// label of the kind.
type DimensionGroup struct {
	Label  string
	Values []Dimension
}

// KindReport is the report of one media kind.
type KindReport struct {
	PluginID, Kind, Label string
	Totals
	Buckets    []Bucket
	Dimensions []DimensionGroup
}

// Report is the whole statistics page for the selected kinds.
type Report struct {
	Kinds        []KindReport
	All          Totals
	Buckets      []Bucket
	Genres, Tags []Dimension
}

// rowFacts is one selected row with its resolved time and units.
type rowFacts struct {
	row        library.Row
	seconds    float64
	hasTime    bool
	timeMetric bool
	units      float64
	hasUnits   bool
}

// Build aggregates inputs into a report. Rows are narrowed to the period by
// their finished date; a nil finished date survives only in an all-time
// report. The kinds keep the input order.
func Build(inputs []Input, opts Options) Report {
	rep := Report{All: newTotals()}
	facts := make([][]rowFacts, len(inputs))
	var all []rowFacts
	for i, in := range inputs {
		for _, row := range in.Rows {
			if !inPeriod(row.Entry.FinishedOn, opts) {
				continue
			}
			f := factsFor(in.Kind, row)
			facts[i] = append(facts[i], f)
			all = append(all, f)
		}
	}

	for i, in := range inputs {
		kr := KindReport{
			PluginID: in.PluginID,
			Kind:     in.Kind.Key,
			Label:    in.Kind.Label,
			Totals:   newTotals(),
		}
		if in.Kind.Stats != nil {
			if in.Kind.Stats.Time != nil {
				kr.TimeLabel = in.Kind.Stats.Time.Label
			}
			if in.Kind.Stats.Units != nil {
				kr.UnitLabel = in.Kind.Stats.Units.LabelPlural
			}
		}
		for _, f := range facts[i] {
			addTotals(&kr.Totals, f, true)
		}
		finishTotals(&kr.Totals)
		kr.Buckets = buckets(facts[i], opts)
		kr.Dimensions = dimensions(in.Kind, facts[i])
		rep.Kinds = append(rep.Kinds, kr)
	}

	for _, f := range all {
		addTotals(&rep.All, f, false)
	}
	finishTotals(&rep.All)
	rep.Buckets = buckets(all, opts)
	rep.Genres, rep.Tags = mergedTerms(inputs, facts)
	return rep
}

func newTotals() Totals {
	t := Totals{Statuses: map[plugin.StatusClass]int{}}
	for _, s := range plugin.StatusClasses() {
		t.Statuses[s] = 0
	}
	return t
}

func finishTotals(t *Totals) {
	if t.Rated > 0 {
		t.RatingAverage = float64(t.RatingSum) / float64(t.Rated)
	}
}

// addTotals adds one row to t. sumUnits is false for the all-media totals,
// where units of different kinds are not comparable. Entries without a
// declared time metric never count as missing time; a resolved zero is known
// and only stays out of the average denominators.
func addTotals(t *Totals, f rowFacts, sumUnits bool) {
	t.Entries++
	if s := plugin.StatusClass(f.row.Entry.Status); isStatus(s) {
		t.Statuses[s]++
	}
	if f.row.Entry.Status == library.Completed {
		t.Completed++
		if f.row.Entry.FinishedOn == nil {
			t.UndatedCompleted++
		}
	}
	if r := f.row.Entry.Rating; r != nil {
		t.Rated++
		t.RatingSum += *r
		if *r >= 0 && *r <= 10 {
			t.RatingDistribution[*r]++
		}
	}
	if f.timeMetric {
		if f.hasTime {
			t.TimeSeconds += f.seconds
			if f.seconds > 0 {
				t.TimeEntries++
			}
		} else {
			t.MissingTime++
		}
	}
	if f.hasUnits && sumUnits {
		t.Units += f.units
		if f.units > 0 {
			t.UnitEntries++
		}
	}
}

func isStatus(s plugin.StatusClass) bool {
	for _, v := range plugin.StatusClasses() {
		if v == s {
			return true
		}
	}
	return false
}

// factsFor resolves the time and units of one row from its kind's declaration.
func factsFor(kind plugin.KindDef, row library.Row) rowFacts {
	f := rowFacts{row: row}
	if kind.Stats == nil {
		return f
	}
	if td := kind.Stats.Time; td != nil {
		f.timeMetric = true
		f.seconds, f.hasTime = resolveTime(kind, row)
	}
	if ud := kind.Stats.Units; ud != nil {
		f.units, f.hasUnits = trackValue(kind, row, ud.Key)
	}
	return f
}

// resolveTime applies the documented order: a completed entry's completed
// field in minutes, then the active duration track in seconds (capped by the
// completed field), then the active units track times the per-unit field in
// minutes.
func resolveTime(kind plugin.KindDef, row library.Row) (float64, bool) {
	td := kind.Stats.Time
	if row.Entry.Status == library.Completed && td.CompletedField != "" {
		if v, ok := fieldNum(row, td.CompletedField); ok && v > 0 {
			return v * 60, true
		}
	}
	if active(row, td.DurationKey) {
		seconds := row.Entry.Progress[td.DurationKey]
		if total, ok := fieldNum(row, td.CompletedField); ok && td.CompletedField != "" && total > 0 {
			if limit := total * 60; seconds > limit {
				seconds = limit
			}
		}
		return seconds, true
	}
	if active(row, td.UnitsKey) && td.PerUnitField != "" {
		if per, ok := fieldNum(row, td.PerUnitField); ok && per > 0 {
			units, _ := trackValue(kind, row, td.UnitsKey)
			return units * per * 60, true
		}
	}
	return 0, false
}

// trackValue returns the value of an active progress track. A completed entry
// at zero falls back to the track's bounded total field, because a completed
// entry may carry no progress at all.
func trackValue(kind plugin.KindDef, row library.Row, key string) (float64, bool) {
	if !active(row, key) {
		return 0, false
	}
	v := row.Entry.Progress[key]
	if row.Entry.Status == library.Completed && v == 0 {
		if def, ok := progressDef(kind, key); ok {
			if total, ok := fieldNum(row, def.TotalField); ok && total > 0 {
				v = total
			}
		}
	}
	return v, true
}

// active reports whether key is one of the item's active progress keys. Only
// those values are statistics; retired values stay stored but do not count.
func active(row library.Row, key string) bool {
	if key == "" {
		return false
	}
	for _, k := range row.Item.ProgressKeys {
		if k == key {
			return true
		}
	}
	return false
}

func progressDef(kind plugin.KindDef, key string) (plugin.ProgressDef, bool) {
	for _, def := range kind.Progress.Eligible {
		if def.Key == key {
			return def, true
		}
	}
	return plugin.ProgressDef{}, false
}

func fieldNum(row library.Row, key string) (float64, bool) {
	if key == "" {
		return 0, false
	}
	v, ok := row.Item.Fields[key]
	if !ok || v.Num == nil {
		return 0, false
	}
	return *v.Num, true
}

// day returns the calendar day of t in time.UTC, keeping the written day.
func day(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// inPeriod reports whether finished is inside the inclusive range. Without a
// bound every entry qualifies; with any bound an entry without a finished date
// does not.
func inPeriod(finished *time.Time, opts Options) bool {
	if opts.From == nil && opts.To == nil {
		return true
	}
	if finished == nil {
		return false
	}
	d := day(*finished)
	if opts.From != nil && d.Before(day(*opts.From)) {
		return false
	}
	if opts.To != nil && d.After(day(*opts.To)) {
		return false
	}
	return true
}

// buckets builds the continuous time axis of the facts. Explicit ends fix the
// range; an open end takes the earliest or latest finished date of the facts.
// All time resolves both ends from the facts and always uses years.
func buckets(facts []rowFacts, opts Options) []Bucket {
	var (
		start, end time.Time
		okStart    bool
		okEnd      bool
	)
	if opts.From != nil {
		start, okStart = day(*opts.From), true
	}
	if opts.To != nil {
		end, okEnd = day(*opts.To), true
	}
	for _, f := range facts {
		if f.row.Entry.FinishedOn == nil {
			continue
		}
		d := day(*f.row.Entry.FinishedOn)
		if !okStart || d.Before(start) {
			start, okStart = d, true
		}
		if !okEnd || d.After(end) {
			end, okEnd = d, true
		}
	}
	if !okStart || !okEnd {
		return nil
	}
	if start.After(end) {
		return nil
	}
	mode := bucketMode(opts, start, end)
	sums := map[string]float64{}
	for _, f := range facts {
		if f.row.Entry.FinishedOn == nil || !f.hasTime {
			continue
		}
		sums[bucketLabel(*f.row.Entry.FinishedOn, mode)] += f.seconds
	}
	labels := bucketLabels(start, end, mode)
	out := make([]Bucket, len(labels))
	for i, label := range labels {
		out[i] = Bucket{Label: label, Seconds: sums[label]}
	}
	return out
}

// bucketMode resolves the requested mode. With both ends nil (all time) it is
// always years; otherwise auto picks months for at most 24 calendar months.
func bucketMode(opts Options, start, end time.Time) BucketMode {
	if opts.From == nil && opts.To == nil {
		return BucketYear
	}
	if opts.Bucket == BucketMonth || opts.Bucket == BucketYear {
		return opts.Bucket
	}
	if monthIndex(end)-monthIndex(start)+1 <= 24 {
		return BucketMonth
	}
	return BucketYear
}

func monthIndex(t time.Time) int {
	y, m, _ := t.Date()
	return y*12 + int(m) - 1
}

func bucketLabel(t time.Time, mode BucketMode) string {
	if mode == BucketYear {
		return t.Format("2006")
	}
	return t.Format("2006-01")
}

// bucketLabels returns every month or year of the range, in order.
func bucketLabels(start, end time.Time, mode BucketMode) []string {
	var out []string
	if mode == BucketYear {
		for y := start.Year(); y <= end.Year(); y++ {
			out = append(out, time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC).Format("2006"))
		}
		return out
	}
	cur := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC)
	last := time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, time.UTC)
	for !cur.After(last) {
		out = append(out, cur.Format("2006-01"))
		cur = cur.AddDate(0, 1, 0)
	}
	return out
}

// dimensions builds the kind's breakdowns: genres, tags, then every filterable
// non-date field in declaration order. Date fields are filters, not
// dimensions; duplicate values of one entry count once.
func dimensions(kind plugin.KindDef, facts []rowFacts) []DimensionGroup {
	var groups []DimensionGroup
	if kind.Genres.Enabled {
		groups = append(groups, termGroup(termLabel(kind.Genres, "Genres"), facts, func(f rowFacts) []string {
			return f.row.Item.Genres
		}))
	}
	if kind.Tags.Enabled {
		groups = append(groups, termGroup(termLabel(kind.Tags, "Tags"), facts, func(f rowFacts) []string {
			names := make([]string, len(f.row.Item.Tags))
			for i, t := range f.row.Item.Tags {
				names[i] = t.Name
			}
			return names
		}))
	}
	for _, def := range kind.Fields {
		if !def.Filterable || def.Type == plugin.FieldDate || def.Key == "" {
			continue
		}
		label := def.Label
		if label == "" {
			label = def.Key
		}
		groups = append(groups, termGroup(label, facts, func(f rowFacts) []string {
			return fieldValues(f.row, def)
		}))
	}
	return groups
}

func termLabel(def plugin.TermDef, fallback string) string {
	if def.Label != "" {
		return def.Label
	}
	return fallback
}

// termGroup accumulates one value set per row. Values are compared exactly
// here and units stay comparable because the group belongs to one kind;
// mergedTerms compares across kinds case-insensitively and omits units.
func termGroup(label string, facts []rowFacts, values func(rowFacts) []string) DimensionGroup {
	acc := newAccumulator(false, true)
	for _, f := range facts {
		acc.add(f, values(f))
	}
	return DimensionGroup{Label: label, Values: acc.values()}
}

// fieldValues returns the display values of one field of one row. List fields
// contribute each element separately; text, enum and URL values stay verbatim;
// numbers are printed without a trailing decimal point. Dates are skipped.
func fieldValues(row library.Row, def plugin.FieldDef) []string {
	v, ok := row.Item.Fields[def.Key]
	if !ok {
		return nil
	}
	switch def.Type {
	case plugin.FieldList:
		return v.List
	case plugin.FieldNumber:
		if v.Num == nil {
			return nil
		}
		return []string{strconv.FormatFloat(*v.Num, 'f', -1, 64)}
	case plugin.FieldDate:
		return nil
	default:
		if strings.TrimSpace(v.Text) == "" {
			return nil
		}
		return []string{v.Text}
	}
}

// accumulator collects dimensions. When fold is set, values merge
// case-insensitively and the first spelling wins. sumUnits is false for the
// all-media terms, where units of different kinds are not comparable.
type accumulator struct {
	fold     bool
	sumUnits bool
	seen     map[string]*Dimension
}

func newAccumulator(fold, sumUnits bool) *accumulator {
	return &accumulator{fold: fold, sumUnits: sumUnits, seen: map[string]*Dimension{}}
}

func (a *accumulator) key(value string) string {
	if a.fold {
		return strings.ToLower(value)
	}
	return value
}

// add records one row's values once each.
func (a *accumulator) add(f rowFacts, values []string) {
	seen := map[string]bool{}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		key := a.key(value)
		if seen[key] {
			continue
		}
		seen[key] = true
		d, ok := a.seen[key]
		if !ok {
			d = &Dimension{Value: value}
			a.seen[key] = d
		}
		d.Entries++
		if r := f.row.Entry.Rating; r != nil {
			d.Rated++
			d.RatingSum += *r
		}
		if f.hasTime {
			d.TimeSeconds += f.seconds
		}
		if f.hasUnits && a.sumUnits {
			d.Units += f.units
		}
	}
}

// values returns the accumulated dimensions, most entries first and then by
// value, so the output is deterministic.
func (a *accumulator) values() []Dimension {
	out := make([]Dimension, 0, len(a.seen))
	for _, d := range a.seen {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Entries != out[j].Entries {
			return out[i].Entries > out[j].Entries
		}
		return out[i].Value < out[j].Value
	})
	return out
}

// mergedTerms builds the all-media genre and tag breakdowns. A term merges
// case-insensitively across kinds; the first display spelling wins. The groups
// only exist when at least one selected kind enables them.
func mergedTerms(inputs []Input, facts [][]rowFacts) (genres, tags []Dimension) {
	gacc, tacc := newAccumulator(true, false), newAccumulator(true, false)
	anyGenres, anyTags := false, false
	for i, in := range inputs {
		anyGenres = anyGenres || in.Kind.Genres.Enabled
		anyTags = anyTags || in.Kind.Tags.Enabled
		for _, f := range facts[i] {
			if in.Kind.Genres.Enabled {
				gacc.add(f, f.row.Item.Genres)
			}
			if in.Kind.Tags.Enabled {
				names := make([]string, len(f.row.Item.Tags))
				for j, t := range f.row.Item.Tags {
					names[j] = t.Name
				}
				tacc.add(f, names)
			}
		}
	}
	if anyGenres {
		genres = gacc.values()
	}
	if anyTags {
		tags = tacc.values()
	}
	return genres, tags
}

package web

// The statistics page GET /stats: one or every enabled media kind of the
// library, narrowed by a completion-based period, a status, genres, tags and
// (for one kind) its filterable fields. The page reads the manifests' StatsDef
// as data and aggregates the rows with internal/stats; no media kind is named
// here.
//
// URL parameters: media (empty or "all", or "<plugin>/<kind>"; a bare
// "<plugin>" for a plugin with one kind), period (all, year, 12m or custom),
// from and to (YYYY-MM-DD, used with custom; present dates imply a custom
// range when no period is named), status (a core status class), genre with
// genre_mode any|all, tag with tag_mode, and f.<field> with the list's filter
// syntax for the selected kind only. Unknown or invalid values are ignored and
// named in a notice, like the list. The page is a plain GET form; no htmx.

import (
	"context"
	"errors"
	"log"
	"math"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/stats"
)

// statsAll is the media and period value meaning every enabled kind and all
// time.
const statsAll = "all"

// statsDimensionTop is the number of values a dimension table shows before
// the "Show all" disclosure.
const statsDimensionTop = 10

// statsKind is one enabled media kind of the statistics page.
type statsKind struct {
	PluginID string
	Manifest plugin.Manifest
	Kind     plugin.KindDef
}

// statsQuery is a parsed /stats request. Media is "all" or "<plugin>/<kind>";
// From and To are the inclusive calendar days of the selected period (nil for
// an open end or all time). Fields are the valid filters of the selected kind
// (never set for All media).
type statsQuery struct {
	Media, Period      string
	From, To           *time.Time
	Status             *library.Status
	Genres, Tags       []string
	GenresAll, TagsAll bool
	Fields             []fieldFilter
	Notices            []string
}

// statCard is one summary card: Key names it for tests (data-stat).
type statCard struct {
	Key, Label, Value, Detail string
}

// statsKindRow is one row of the per-kind comparison table.
type statsKindRow struct {
	PluginID, Kind, Label, Href string
	Entries, Completed          string
	Time, AvgTime, Units        string
	Rating                      string
}

// dimensionRow is one value of a dimension table.
type dimensionRow struct {
	Value, Entries, Time, Units, Rating string
}

// dimensionTable holds the columns and rows of one dimension table.
type dimensionTable struct {
	ShowTime, ShowUnits bool
	Rows                []dimensionRow
}

// dimensionView is one breakdown by a term or field: the top values and the
// rest behind a "Show all" disclosure.
type dimensionView struct {
	Key, Label string
	Top, More  dimensionTable
	MoreCount  int
}

// statsPage is the data of stats.html.
type statsPage struct {
	// NoKinds: no plugin is enabled. Empty: no row to show. AnyEntries: the
	// scope holds entries at all (it distinguishes an empty list from
	// filters that match nothing). TooMany: the row cap was exceeded.
	NoKinds, Empty, AnyEntries, TooMany bool
	// AllMedia: the comparison view. HasTime, HasUnits: the scope can compute
	// a time total or (one kind only) a unit total.
	AllMedia, HasTime, HasUnits bool
	// the filter form
	MediaOptions, PeriodOptions, StatusOptions []option
	Period, From, To                           string // Period selects the custom range inputs
	Genres, Tags                               *termInput
	Fields                                     []filterField
	ClearURL                                   string
	// notices
	Notice           []string
	NoticeMore       int
	MissingTime      int
	MissingTimeURL   string
	UndatedCompleted int
	// the numbers
	Cards        []statCard
	KindRows     []statsKindRow
	CompareTime  bool
	CompareUnits bool
	NoTime       bool
	Dimensions   []dimensionView
	// the figures: Chart is nil when the scope has no time metric or no
	// bucket carries time; Ratings always holds the 12 distribution rows;
	// Heatmap covers the last 12 months for the media scope only.
	Chart   *timeChartView
	Ratings []ratingRow
	Heatmap heatmapView
}

// statsScope lists the enabled media kinds in registry order.
func (s *server) statsScope(ctx context.Context) []statsKind {
	var out []statsKind
	for _, m := range s.deps.Registry.Manifests() {
		if !s.deps.Registry.IsEnabled(m.ID) {
			continue
		}
		for _, k := range m.Kinds {
			out = append(out, statsKind{PluginID: m.ID, Manifest: m, Kind: k})
		}
	}
	return out
}

// getStats renders the statistics page. A row cap overflow is a notice, not
// an error page: the filter form stays usable.
func (s *server) getStats(w http.ResponseWriter, r *http.Request) {
	scope := s.statsScope(r.Context())
	q := s.parseStatsQuery(r, s.now(), scope)
	v, err := s.buildStatsPage(r.Context(), q)
	switch {
	case errors.Is(err, library.ErrTooManyRows):
		v = s.statsOverflowPage(r.Context(), scope, q)
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	s.render(w, r, "stats", v)
}

// statsMedia resolves a media parameter to its canonical "<plugin>/<kind>"
// ("all" for every kind, and ok is true then). A bare plugin id is accepted
// when the plugin has exactly one enabled kind.
func statsMedia(scope []statsKind, raw string) (string, bool) {
	if raw == "" || raw == statsAll {
		return statsAll, true
	}
	if p, k, ok := strings.Cut(raw, "/"); ok {
		for _, sk := range scope {
			if sk.PluginID == p && sk.Kind.Key == k {
				return p + "/" + k, true
			}
		}
		return "", false
	}
	var found statsKind
	n := 0
	for _, sk := range scope {
		if sk.PluginID == raw {
			found, n = sk, n+1
		}
	}
	if n == 1 {
		return found.PluginID + "/" + found.Kind.Key, true
	}
	return "", false
}

// statsSelect returns the kinds a canonical media value selects.
func statsSelect(scope []statsKind, media string) []statsKind {
	if media == "" || media == statsAll {
		return scope
	}
	for _, sk := range scope {
		if sk.PluginID+"/"+sk.Kind.Key == media {
			return []statsKind{sk}
		}
	}
	return nil
}

// parseStatsDay reads one YYYY-MM-DD parameter; absent is (nil, true).
func parseStatsDay(raw string) (*time.Time, bool) {
	if raw == "" {
		return nil, true
	}
	d, err := time.Parse(time.DateOnly, raw)
	if err != nil {
		return nil, false
	}
	return &d, true
}

// dayOf returns the UTC calendar day of the instant t. Entry dates are read
// back as UTC midnight and the activity log stamps UTC, so every comparison and
// the heatmap window use UTC days whatever the host's zone.
func dayOf(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// parseStatsQuery reads and validates every parameter of a /stats request.
func (s *server) parseStatsQuery(r *http.Request, now time.Time, scope []statsKind) statsQuery {
	q := statsQuery{Media: statsAll, Period: statsAll}
	vals, err := url.ParseQuery(r.URL.RawQuery) // keeps the readable pairs
	if err != nil {
		q.Notices = append(q.Notices, "(unreadable parameter)")
	}
	params := filterParams(vals)
	rejected := map[string]bool{}
	reject := func(key string) { rejected[key] = true }
	// first returns the one value of a parameter; repeated values are refused.
	first := func(key string) (string, bool) {
		v := params[key]
		switch len(v) {
		case 0:
			return "", false
		case 1:
			return v[0], true
		}
		reject(key)
		return v[0], true
	}

	if raw, ok := first("media"); ok {
		if media, valid := statsMedia(scope, raw); valid {
			q.Media = media
		} else {
			reject("media")
		}
	}
	periodRaw, hasPeriod := first("period")
	switch {
	case !hasPeriod || periodRaw == "":
		q.Period = statsAll
	case periodRaw == statsAll || periodRaw == "year" || periodRaw == "12m" || periodRaw == "custom":
		q.Period = periodRaw
	default:
		reject("period")
	}
	fromRaw, hasFrom := first("from")
	toRaw, hasTo := first("to")
	from, fromOK := parseStatsDay(fromRaw)
	to, toOK := parseStatsDay(toRaw)
	if hasFrom && !fromOK {
		reject("from")
	}
	if hasTo && !toOK {
		reject("to")
	}
	// a present date implies a custom range when no period was named
	if !hasPeriod || periodRaw == "" {
		if hasFrom || hasTo {
			q.Period = "custom"
		}
	}
	switch q.Period {
	case "custom":
		q.From, q.To = from, to
		if q.From != nil && q.To != nil && q.From.After(*q.To) {
			reject("from")
			reject("to")
			q.From, q.To = nil, nil
		}
	case "year":
		q.From = timePtr(time.Date(now.Year(), time.January, 1, 0, 0, 0, 0, time.UTC))
		q.To = timePtr(time.Date(now.Year(), time.December, 31, 0, 0, 0, 0, time.UTC))
	case "12m":
		firstOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		q.From = timePtr(firstOfMonth.AddDate(0, -11, 0))
		q.To = timePtr(firstOfMonth.AddDate(0, 1, 0).AddDate(0, 0, -1))
	}

	if raw, ok := first("status"); ok {
		if slices.Contains(library.Statuses(), library.Status(raw)) {
			st := library.Status(raw)
			q.Status = &st
		} else {
			reject("status")
		}
	}

	kinds := statsSelect(scope, q.Media)
	q.Genres, q.GenresAll = parseStatsTerms(params, "genre", kinds, func(k plugin.KindDef) bool {
		return k.Genres.Enabled && k.Genres.Filterable
	}, reject)
	q.Tags, q.TagsAll = parseStatsTerms(params, "tag", kinds, func(k plugin.KindDef) bool {
		return k.Tags.Enabled && k.Tags.Filterable
	}, reject)

	if q.Media != statsAll && len(kinds) == 1 {
		fields, bad := parseStatsFields(kinds[0].Kind, params)
		q.Fields = fields
		for _, key := range bad {
			reject(key)
		}
	}

	// everything else is unknown: f.* belongs to one kind only
	known := map[string]bool{"media": true, "period": true, "from": true, "to": true, "status": true,
		"genre": true, "tag": true, "genre_mode": true, "tag_mode": true}
	if q.Media != statsAll && len(kinds) == 1 {
		for _, fd := range kinds[0].Kind.Fields {
			if !fd.Filterable {
				continue
			}
			base := "f." + fd.Key
			known[base] = true
			if fd.Type == plugin.FieldNumber || fd.Type == plugin.FieldDate {
				known[base+".from"] = true
				known[base+".to"] = true
			}
		}
	}
	for key := range params {
		if !known[key] {
			reject(key)
		}
	}
	keys := make([]string, 0, len(rejected))
	for key := range rejected {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	q.Notices = append(q.Notices, keys...)
	return q
}

// timePtr returns a pointer to t.
func timePtr(t time.Time) *time.Time { return &t }

// parseStatsTerms reads one repeatable term filter with its mode. It refuses
// the filter when no selected kind enables that term type as filterable.
func parseStatsTerms(params map[string][]string, key string, kinds []statsKind, supported func(plugin.KindDef) bool, reject func(string)) ([]string, bool) {
	all := false
	switch mode := params[key+"_mode"]; {
	case len(mode) == 0, len(mode) == 1 && mode[0] == "any":
	case len(mode) == 1 && mode[0] == "all":
		all = true
	default:
		reject(key + "_mode")
	}
	vals := params[key]
	if len(vals) == 0 {
		return nil, false
	}
	if !slices.ContainsFunc(kinds, func(k statsKind) bool { return supported(k.Kind) }) {
		reject(key)
		return nil, false
	}
	// distinct values (case-insensitive), at most maxTermValues
	var keep []string
	for _, v := range vals {
		if slices.ContainsFunc(keep, func(x string) bool { return strings.EqualFold(x, v) }) {
			continue
		}
		if len(keep) == maxTermValues {
			reject(key)
			break
		}
		keep = append(keep, v)
	}
	return keep, all
}

// statsFieldParam reports whether key is a filter parameter of a filterable
// field of the kind (the value itself or, for ranged types, its form aliases).
func statsFieldParam(k plugin.KindDef, key string) bool {
	for _, fd := range k.Fields {
		if !fd.Filterable {
			continue
		}
		base := "f." + fd.Key
		switch key {
		case base:
			return true
		case base + ".from", base + ".to":
			if fd.Type == plugin.FieldNumber || fd.Type == plugin.FieldDate {
				return true
			}
		}
	}
	return false
}

// parseStatsFields validates the f.* parameters of one kind with the list's
// parser, so syntax, types and bounds behave the same.
func parseStatsFields(k plugin.KindDef, params map[string][]string) ([]fieldFilter, []string) {
	subset := url.Values{}
	for key, vals := range params {
		if statsFieldParam(k, key) {
			subset[key] = vals
		}
	}
	f, rejected := parseFilters(k, subset)
	return f.Fields, rejected
}

// statsNarrows reports whether a filter other than All media/All time is set.
func statsNarrows(q statsQuery) bool {
	return q.Media != statsAll || q.Period != statsAll || q.Status != nil ||
		len(q.Genres) > 0 || len(q.Tags) > 0 || len(q.Fields) > 0
}

// newStatsPage builds the page's filter view and empty state; the numbers are
// filled by buildStatsPage.
func (s *server) newStatsPage(scope []statsKind, q statsQuery, genres, tags []library.TermCount) *statsPage {
	kinds := statsSelect(scope, q.Media)
	allMedia := q.Media == statsAll
	v := &statsPage{
		NoKinds:       len(scope) == 0,
		Empty:         true,
		AllMedia:      allMedia,
		Period:        q.Period,
		MediaOptions:  statsMediaOptions(scope, q.Media),
		PeriodOptions: statsPeriodOptions(q.Period),
		StatusOptions: statsStatusOptions(kinds, allMedia, q.Status),
	}
	if q.Period == "custom" {
		v.From, v.To = fmtDay(q.From), fmtDay(q.To)
	}
	if statsNarrows(q) {
		v.ClearURL = "/stats"
	}
	if slices.ContainsFunc(kinds, func(k statsKind) bool { return k.Kind.Genres.Enabled && k.Kind.Genres.Filterable }) {
		def := plugin.TermDef{Enabled: true, Label: "Genres"}
		if !allMedia && len(kinds) == 1 && kinds[0].Kind.Genres.Label != "" {
			def.Label = kinds[0].Kind.Genres.Label
		}
		v.Genres = termBar(def, "genre", q.Genres, q.GenresAll, genres)
	}
	if slices.ContainsFunc(kinds, func(k statsKind) bool { return k.Kind.Tags.Enabled && k.Kind.Tags.Filterable }) {
		def := plugin.TermDef{Enabled: true, Label: "Tags"}
		if !allMedia && len(kinds) == 1 && kinds[0].Kind.Tags.Label != "" {
			def.Label = kinds[0].Kind.Tags.Label
		}
		v.Tags = termBar(def, "tag", q.Tags, q.TagsAll, tags)
	}
	// the kind's filterable fields belong to a specific kind only
	if !allMedia && len(kinds) == 1 {
		v.Fields = statsFieldInputs(kinds[0].Kind, q.Fields)
	}
	v.HasTime = slices.ContainsFunc(kinds, func(k statsKind) bool {
		return k.Kind.Stats != nil && k.Kind.Stats.Time != nil
	})
	if !allMedia && len(kinds) == 1 && kinds[0].Kind.Stats != nil && kinds[0].Kind.Stats.Units != nil {
		v.HasUnits = true
	}
	v.Notice, v.NoticeMore = noticeKeys(q.Notices)
	return v
}

// statsMediaOptions are the choices of the media filter.
func statsMediaOptions(scope []statsKind, cur string) []option {
	out := []option{{Value: "", Label: "All media", Selected: cur == statsAll || cur == ""}}
	for _, sk := range scope {
		label := sk.Kind.LabelPlural
		if label == "" {
			label = sk.Kind.Label
		}
		value := sk.PluginID + "/" + sk.Kind.Key
		out = append(out, option{Value: value, Label: label, Selected: cur == value})
	}
	return out
}

// statsPeriodOptions are the choices of the period filter.
func statsPeriodOptions(cur string) []option {
	out := make([]option, 0, 4)
	for _, p := range []struct{ value, label string }{
		{statsAll, "All time"}, {"year", "This year"}, {"12m", "Last 12 months"}, {"custom", "Custom range"},
	} {
		out = append(out, option{Value: p.value, Label: p.label, Selected: cur == p.value || (cur == "" && p.value == statsAll)})
	}
	return out
}

// statsStatusOptions are the choices of the status filter: the kind's own
// labels for a specific kind, neutral core labels for All media (also when
// only one kind is enabled).
func statsStatusOptions(kinds []statsKind, allMedia bool, cur *library.Status) []option {
	out := []option{{Value: "", Label: "Any status", Selected: cur == nil}}
	for _, st := range library.Statuses() {
		label := statsStatusLabel(st)
		if !allMedia && len(kinds) == 1 {
			label = statusLabel(kinds[0].Kind, st)
		}
		out = append(out, option{Value: string(st), Label: label, Selected: cur != nil && *cur == st})
	}
	return out
}

// statsStatusLabel is the neutral core label of a status class.
func statsStatusLabel(st library.Status) string {
	switch st {
	case library.Planned:
		return "Planned"
	case library.Active:
		return "Active"
	case library.Paused:
		return "Paused"
	case library.Completed:
		return "Completed"
	case library.Dropped:
		return "Dropped"
	}
	return string(st)
}

// statsField finds the parsed filter of a field key.
func statsField(fields []fieldFilter, key string) (fieldFilter, bool) {
	for _, ff := range fields {
		if ff.Key == key {
			return ff, true
		}
	}
	return fieldFilter{}, false
}

// statsFieldInputs builds the filter inputs of one kind's filterable fields,
// the same shapes as the list's filter bar.
func statsFieldInputs(k plugin.KindDef, fields []fieldFilter) []filterField {
	var out []filterField
	for _, fd := range k.Fields {
		if !fd.Filterable {
			continue
		}
		cur, _ := statsField(fields, fd.Key)
		in := filterField{Label: fd.Label, Name: "f." + fd.Key}
		switch fd.Type {
		case plugin.FieldEnum:
			in.Type = "enum"
			for _, value := range fd.EnumValues {
				in.Options = append(in.Options, option{Value: value, Label: value, Selected: slices.Contains(cur.Values, value)})
			}
		case plugin.FieldNumber, plugin.FieldDate:
			in.Type = string(fd.Type)
			in.FromName, in.ToName = in.Name+".from", in.Name+".to"
			if len(cur.Values) == 1 {
				lo, hi, ok := strings.Cut(cur.Values[0], "..")
				if !ok {
					hi = lo
				}
				in.From, in.To = lo, hi
			}
		default:
			in.Type = "text"
			in.Value = strings.Join(cur.Values, " ")
		}
		out = append(out, in)
	}
	return out
}

// statsKindRows is the loaded rows of one kind.
type statsKindRows struct {
	kind statsKind
	rows []library.Row
}

// statsKindTermsOK reports whether the kind can satisfy the selected term
// filters: a kind that does not enable the term type cannot match it.
func statsKindTermsOK(k plugin.KindDef, q statsQuery) bool {
	if len(q.Genres) > 0 && !(k.Genres.Enabled && k.Genres.Filterable) {
		return false
	}
	if len(q.Tags) > 0 && !(k.Tags.Enabled && k.Tags.Filterable) {
		return false
	}
	return true
}

// statsTagNames returns the names of an item's tags.
func statsTagNames(row library.Row) []string {
	names := make([]string, len(row.Item.Tags))
	for i, t := range row.Item.Tags {
		names[i] = t.Name
	}
	return names
}

// statsMatchTerms reports whether have satisfies the term filter want: one of
// the values (any) or every value (all), compared case-insensitively.
func statsMatchTerms(have, want []string, all bool) bool {
	if len(want) == 0 {
		return true
	}
	n := 0
	for _, w := range want {
		if slices.ContainsFunc(have, func(h string) bool { return strings.EqualFold(h, w) }) {
			n++
		}
	}
	if all {
		return n == len(want)
	}
	return n > 0
}

// statsNarrow keeps the rows matching the selected terms.
func statsNarrow(rows []library.Row, q statsQuery) []library.Row {
	if len(q.Genres) == 0 && len(q.Tags) == 0 {
		return rows
	}
	out := make([]library.Row, 0, len(rows))
	for _, row := range rows {
		if !statsMatchTerms(row.Item.Genres, q.Genres, q.GenresAll) {
			continue
		}
		if !statsMatchTerms(statsTagNames(row), q.Tags, q.TagsAll) {
			continue
		}
		out = append(out, row)
	}
	return out
}

// statsDayInPeriod reports whether a finished day is inside the inclusive
// range; an entry without a finished date is outside a closed range.
func statsDayInPeriod(finished *time.Time, from, to *time.Time) bool {
	if from == nil && to == nil {
		return true
	}
	if finished == nil {
		return false
	}
	d := dayOf(*finished)
	if from != nil && d.Before(dayOf(*from)) {
		return false
	}
	if to != nil && d.After(dayOf(*to)) {
		return false
	}
	return true
}

// statsTermCounts counts the genres or tags of the loaded rows for the filter
// options: each term once per entry, merged case-insensitively across kinds,
// inside the period, with the other term filter applied but not its own.
func statsTermCounts(loaded []statsKindRows, q statsQuery, which string) []library.TermCount {
	counts := map[string]*library.TermCount{}
	for _, l := range loaded {
		if which == "genre" && !l.kind.Kind.Genres.Enabled {
			continue
		}
		if which == "tag" && !l.kind.Kind.Tags.Enabled {
			continue
		}
		for _, row := range l.rows {
			if !statsDayInPeriod(row.Entry.FinishedOn, q.From, q.To) {
				continue
			}
			switch which {
			case "genre":
				if !statsMatchTerms(statsTagNames(row), q.Tags, q.TagsAll) {
					continue
				}
			case "tag":
				if !statsMatchTerms(row.Item.Genres, q.Genres, q.GenresAll) {
					continue
				}
			}
			values := row.Item.Genres
			if which == "tag" {
				values = statsTagNames(row)
			}
			seen := map[string]bool{}
			for _, value := range values {
				key := strings.ToLower(value)
				if seen[key] {
					continue
				}
				seen[key] = true
				c, ok := counts[key]
				if !ok {
					c = &library.TermCount{Term: value}
					counts[key] = c
				}
				c.Count++
			}
		}
	}
	out := make([]library.TermCount, 0, len(counts))
	for _, c := range counts {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return strings.ToLower(out[i].Term) < strings.ToLower(out[j].Term)
	})
	return out
}

// statsBucket is the requested bucket width of the time chart.
func statsBucket(period string) stats.BucketMode {
	switch period {
	case "year", "12m":
		return stats.BucketMonth
	case "custom":
		return stats.BucketAuto
	}
	return stats.BucketYear
}

// statsQueryFor is the library query of one kind: the status and the selected
// kind's field filters (the term filters are applied after loading, see
// buildStatsPage).
func statsQueryFor(sk statsKind, q statsQuery) library.Query {
	query := library.Query{PluginID: sk.PluginID, Kind: sk.Kind.Key, Status: q.Status}
	for _, ff := range q.Fields {
		fd, _ := fieldDef(sk.Kind, ff.Key)
		query.Filters = append(query.Filters, library.Filter{Key: ff.Key, Op: filterOp(fd, ff.Values), Values: ff.Values})
	}
	return query
}

// buildStatsPage loads the rows of the selected kinds and aggregates them.
func (s *server) buildStatsPage(ctx context.Context, q statsQuery) (*statsPage, error) {
	scope := s.statsScope(ctx)
	kinds := statsSelect(scope, q.Media)
	v := s.newStatsPage(scope, q, nil, nil)
	if len(kinds) == 0 {
		return v, nil
	}
	loaded := make([]statsKindRows, 0, len(kinds))
	for _, sk := range kinds {
		var rows []library.Row
		// The term filters are applied after loading (statsNarrow): a kind
		// that does not enable the term type drops out, and the option counts
		// need the rows from before their own filter. The status and field
		// filters are in the query.
		if statsKindTermsOK(sk.Kind, q) {
			var err error
			rows, err = s.deps.Library.StatsRows(ctx, statsQueryFor(sk, q), s.titleLang(ctx, sk.Manifest))
			if err != nil {
				return nil, err
			}
		}
		loaded = append(loaded, statsKindRows{kind: sk, rows: rows})
	}
	genres := statsTermCounts(loaded, q, "genre")
	tags := statsTermCounts(loaded, q, "tag")
	v = s.newStatsPage(scope, q, genres, tags)

	inputs := make([]stats.Input, len(loaded))
	undated := 0
	for i, l := range loaded {
		rows := statsNarrow(l.rows, q)
		inputs[i] = stats.Input{PluginID: l.kind.PluginID, Kind: l.kind.Kind, Rows: rows}
		for _, row := range rows {
			if row.Entry.Status == library.Completed && row.Entry.FinishedOn == nil {
				undated++
			}
		}
	}
	rep := stats.Build(inputs, stats.Options{From: q.From, To: q.To, Bucket: statsBucket(q.Period)})
	if err := s.fillStatsPage(ctx, v, q, scope, rep); err != nil {
		return nil, err
	}
	// the heatmap stands beside the period: it always covers the last 12
	// months, narrowed by the media filter only, so it is filled even when
	// the filters leave no entry
	if err := s.fillStatsActivity(ctx, v, q, scope); err != nil {
		return nil, err
	}
	// a period excludes undated entries before stats.Build counts them, so
	// the note counts them on the rows the period was applied to
	if q.Period != statsAll {
		v.UndatedCompleted = undated
	}
	return v, nil
}

// statsOverflowPage builds the page shown when the row cap is exceeded: the
// filter form with its term choices and the notice, but no numbers and no
// empty-list copy.
func (s *server) statsOverflowPage(ctx context.Context, scope []statsKind, q statsQuery) *statsPage {
	genres, tags := s.statsTermOptions(ctx, scope, q)
	v := s.newStatsPage(scope, q, genres, tags)
	v.TooMany = true
	return v
}

// statsTermOptions loads the genre and tag choices from the term index for a
// page that cannot build a report. It merges across kinds case-insensitively
// and sums counts. A failed option query leaves that list empty (the notice
// path stays usable) and is logged.
func (s *server) statsTermOptions(ctx context.Context, scope []statsKind, q statsQuery) (genres, tags []library.TermCount) {
	for _, sk := range statsSelect(scope, q.Media) {
		if sk.Kind.Genres.Enabled && sk.Kind.Genres.Filterable {
			counts, err := s.deps.Library.Terms(ctx, statsQueryFor(sk, q), "genre")
			if err != nil {
				log.Printf("stats: genre options: %v", err)
			} else {
				genres = mergeTermCounts(genres, counts)
			}
		}
		if sk.Kind.Tags.Enabled && sk.Kind.Tags.Filterable {
			counts, err := s.deps.Library.Terms(ctx, statsQueryFor(sk, q), "tag")
			if err != nil {
				log.Printf("stats: tag options: %v", err)
			} else {
				tags = mergeTermCounts(tags, counts)
			}
		}
	}
	return genres, tags
}

// mergeTermCounts adds the term counts of one kind to the merged list: the
// counts of a term sum case-insensitively and the first spelling wins.
func mergeTermCounts(into, add []library.TermCount) []library.TermCount {
	index := make(map[string]int, len(into))
	for i, c := range into {
		index[strings.ToLower(c.Term)] = i
	}
	for _, c := range add {
		if i, ok := index[strings.ToLower(c.Term)]; ok {
			into[i].Count += c.Count
			continue
		}
		index[strings.ToLower(c.Term)] = len(into)
		into = append(into, c)
	}
	sort.Slice(into, func(i, j int) bool {
		if into[i].Count != into[j].Count {
			return into[i].Count > into[j].Count
		}
		return strings.ToLower(into[i].Term) < strings.ToLower(into[j].Term)
	})
	return into
}

// statsAnyEntries reports whether any enabled kind of the scope has an entry
// at all, so an empty result can tell an empty list from filters that match
// nothing.
func (s *server) statsAnyEntries(ctx context.Context, scope []statsKind) (bool, error) {
	for _, sk := range scope {
		counts, err := s.deps.Library.Counts(ctx, library.Query{PluginID: sk.PluginID, Kind: sk.Kind.Key})
		if err != nil {
			return false, err
		}
		for _, n := range counts {
			if n > 0 {
				return true, nil
			}
		}
	}
	return false, nil
}

// fillStatsPage fills the empty state, notices, cards, comparison and
// dimension tables from the report.
func (s *server) fillStatsPage(ctx context.Context, v *statsPage, q statsQuery, scope []statsKind, rep stats.Report) error {
	// a specific kind's own totals carry the unit sum and the kind's labels;
	// the All media totals are the comparable cross-kind numbers (units are
	// not summed)
	total := rep.All
	if !v.AllMedia && len(rep.Kinds) == 1 {
		total = rep.Kinds[0].Totals
	}
	v.Empty = total.Entries == 0
	switch {
	case total.Entries > 0:
		v.AnyEntries = true
	case statsNarrows(q):
		any, err := s.statsAnyEntries(ctx, scope)
		if err != nil {
			return err
		}
		v.AnyEntries = any
	}
	if v.Empty {
		return nil
	}
	v.MissingTime = total.MissingTime
	if v.MissingTime > 0 && !v.AllMedia {
		v.MissingTimeURL = "/" + url.PathEscape(rep.Kinds[0].PluginID)
	}
	v.NoTime = v.HasTime && total.TimeEntries == 0
	v.Cards = statsCards(total, v.HasTime, v.HasUnits)
	if v.AllMedia {
		for _, kr := range rep.Kinds {
			v.KindRows = append(v.KindRows, statsCompareRow(q, kr, statsKindLabel(scope, kr.PluginID, kr.Kind)))
			if kr.TimeLabel != "" {
				v.CompareTime = true
			}
			if kr.UnitLabel != "" {
				v.CompareUnits = true
			}
		}
	}
	v.Dimensions = statsDimensions(statsSelect(scope, q.Media), rep, v.AllMedia, v.HasTime, v.HasUnits)
	v.Chart = buildTimeChart(rep.Buckets)
	v.Ratings = buildRatingRows(total.RatingDistribution, total.Entries-total.Rated)
	return nil
}

// fillStatsActivity fills the activity heatmap of the last 12 months. Only the
// media filter narrows it; the window is independent of the period and of the
// status, genre, tag and field filters. The activity log is global, so the
// earliest recorded day ("tracked since") is too.
func (s *server) fillStatsActivity(ctx context.Context, v *statsPage, q statsQuery, scope []statsKind) error {
	kinds := statsSelect(scope, q.Media)
	if len(kinds) == 0 {
		return nil
	}
	refs := make([]library.KindRef, len(kinds))
	for i, sk := range kinds {
		refs[i] = library.KindRef{PluginID: sk.PluginID, Kind: sk.Kind.Key}
	}
	// 365 days including today: the 53 Monday-first week columns of the
	// grid always cover the window
	today := dayOf(s.now())
	from := today.AddDate(0, 0, -364)
	counts, err := s.deps.Library.ActivityDays(ctx, from, today.AddDate(0, 0, 1), refs)
	if err != nil {
		return err
	}
	since, _, err := s.deps.Library.FirstActivityDay(ctx)
	if err != nil {
		return err
	}
	v.Heatmap = buildHeatmap(counts, from, today, since)
	return nil
}

// statsKindLabel is the plural display label of a kind.
func statsKindLabel(scope []statsKind, pluginID, kind string) string {
	for _, sk := range scope {
		if sk.PluginID == pluginID && sk.Kind.Key == kind {
			if sk.Kind.LabelPlural != "" {
				return sk.Kind.LabelPlural
			}
			return sk.Kind.Label
		}
	}
	return kind
}

// statsCompareRow builds one row of the per-kind comparison.
func statsCompareRow(q statsQuery, kr stats.KindReport, label string) statsKindRow {
	row := statsKindRow{
		PluginID: kr.PluginID, Kind: kr.Kind, Label: label,
		Href:    statsMediaURL(q, kr.PluginID+"/"+kr.Kind),
		Entries: strconv.Itoa(kr.Entries), Completed: strconv.Itoa(kr.Completed),
		Rating: "–",
	}
	if kr.TimeLabel != "" {
		row.Time = fmtSpan(kr.TimeSeconds)
		if kr.TimeEntries > 0 {
			row.AvgTime = fmtSpan(kr.TimeSeconds / float64(kr.TimeEntries))
		}
	}
	if kr.UnitLabel != "" {
		row.Units = fmtNum(kr.Units)
	}
	if kr.Rated > 0 {
		row.Rating = strconv.FormatFloat(kr.RatingAverage, 'f', 1, 64)
	}
	return row
}

// statsMediaURL is the /stats URL for a media value, keeping the period,
// status and term filters (field filters are kind-specific and dropped).
func statsMediaURL(q statsQuery, media string) string {
	v := url.Values{}
	if media != "" && media != statsAll {
		v.Set("media", media)
	}
	if q.Period != "" && q.Period != statsAll {
		v.Set("period", q.Period)
	}
	if q.Period == "custom" {
		if q.From != nil {
			v.Set("from", fmtDay(q.From))
		}
		if q.To != nil {
			v.Set("to", fmtDay(q.To))
		}
	}
	if q.Status != nil {
		v.Set("status", string(*q.Status))
	}
	for _, g := range q.Genres {
		v.Add("genre", g)
	}
	if q.GenresAll && len(q.Genres) > 0 {
		v.Set("genre_mode", "all")
	}
	for _, t := range q.Tags {
		v.Add("tag", t)
	}
	if q.TagsAll && len(q.Tags) > 0 {
		v.Set("tag_mode", "all")
	}
	if len(v) == 0 {
		return "/stats"
	}
	// a slash is legal in a query value and keeps media=<plugin>/<kind>
	// readable; Encode escapes it for no reason
	return "/stats?" + strings.ReplaceAll(v.Encode(), "%2F", "/")
}

// statsCards builds the summary cards. The time and unit cards only exist
// when the scope can compute them.
func statsCards(t stats.Totals, hasTime, hasUnits bool) []statCard {
	cards := []statCard{{Key: "entries", Label: "Entries", Value: strconv.Itoa(t.Entries)}}
	rate := 0
	if t.Entries > 0 {
		rate = int(math.Round(float64(t.Completed) * 100 / float64(t.Entries)))
	}
	cards = append(cards, statCard{Key: "completed", Label: "Completed", Value: strconv.Itoa(t.Completed),
		Detail: strconv.Itoa(rate) + "% of entries"})
	if hasTime {
		label := t.TimeLabel
		if label == "" {
			label = "Time"
		}
		cards = append(cards, statCard{Key: "time", Label: label, Value: fmtSpan(t.TimeSeconds),
			Detail: "across " + statsCount(t.TimeEntries, "entry", "entries") + " with time"})
		if t.TimeEntries > 0 {
			cards = append(cards, statCard{Key: "avg-time", Label: "Average time",
				Value:  fmtSpan(t.TimeSeconds / float64(t.TimeEntries)),
				Detail: "over " + statsCount(t.TimeEntries, "entry", "entries") + " with time"})
		}
	}
	if hasUnits {
		label := t.UnitLabel
		if label == "" {
			label = "Units"
		}
		cards = append(cards, statCard{Key: "units", Label: label, Value: fmtNum(t.Units),
			Detail: "across " + statsCount(t.UnitEntries, "entry", "entries") + " with units"})
		if t.UnitEntries > 0 {
			cards = append(cards, statCard{Key: "avg-units", Label: "Average " + strings.ToLower(label),
				Value:  fmtNum(t.Units / float64(t.UnitEntries)),
				Detail: "over " + statsCount(t.UnitEntries, "entry", "entries") + " with units"})
		}
	}
	rating := "–"
	if t.Rated > 0 {
		rating = strconv.FormatFloat(t.RatingAverage, 'f', 1, 64)
	}
	cards = append(cards,
		statCard{Key: "avg-rating", Label: "Average rating", Value: rating,
			Detail: "from " + statsCount(t.Rated, "rated entry", "rated entries")},
		statCard{Key: "rated", Label: "Rated", Value: strconv.Itoa(t.Rated),
			Detail: strconv.Itoa(t.Entries-t.Rated) + " unrated"})
	return cards
}

// statsDimensions builds the dimension tables of the selected kinds: the
// kind's own groups for a specific kind, the merged genres and tags for All
// media.
func statsDimensions(kinds []statsKind, rep stats.Report, allMedia, hasTime, hasUnits bool) []dimensionView {
	if !allMedia && len(kinds) == 1 {
		keys := statsDimensionKeys(kinds[0].Kind)
		var out []dimensionView
		for i, g := range rep.Kinds[0].Dimensions {
			key := "dimension-" + strconv.Itoa(i)
			if i < len(keys) {
				key = keys[i]
			}
			out = append(out, statsDimensionView(key, g.Label, g.Values, hasTime, hasUnits))
		}
		return out
	}
	var out []dimensionView
	if rep.Genres != nil {
		out = append(out, statsDimensionView("genres", "Genres", rep.Genres, hasTime, false))
	}
	if rep.Tags != nil {
		out = append(out, statsDimensionView("tags", "Tags", rep.Tags, hasTime, false))
	}
	return out
}

// statsDimensionKeys mirrors the group order of internal/stats: genres, tags,
// then every filterable non-date field in declaration order.
func statsDimensionKeys(k plugin.KindDef) []string {
	var keys []string
	if k.Genres.Enabled {
		keys = append(keys, "genres")
	}
	if k.Tags.Enabled {
		keys = append(keys, "tags")
	}
	for _, fd := range k.Fields {
		if fd.Filterable && fd.Type != plugin.FieldDate && fd.Key != "" {
			keys = append(keys, fd.Key)
		}
	}
	return keys
}

// statsDimensionView splits one dimension into the top values and the rest.
func statsDimensionView(key, label string, values []stats.Dimension, hasTime, hasUnits bool) dimensionView {
	v := dimensionView{Key: key, Label: label,
		Top:  dimensionTable{ShowTime: hasTime, ShowUnits: hasUnits},
		More: dimensionTable{ShowTime: hasTime, ShowUnits: hasUnits}}
	for i, d := range values {
		row := dimensionRow{Value: d.Value, Entries: strconv.Itoa(d.Entries), Rating: "–"}
		if hasTime {
			row.Time = fmtSpan(d.TimeSeconds)
		}
		if hasUnits {
			row.Units = fmtNum(d.Units)
		}
		if d.Rated > 0 {
			row.Rating = strconv.FormatFloat(float64(d.RatingSum)/float64(d.Rated), 'f', 1, 64)
		}
		if i < statsDimensionTop {
			v.Top.Rows = append(v.Top.Rows, row)
		} else {
			v.More.Rows = append(v.More.Rows, row)
		}
	}
	v.MoreCount = len(v.More.Rows)
	return v
}

// fmtSpan formats seconds as "12h 30m" (minutes rounded down; a positive
// remainder under a minute as "<1m").
func fmtSpan(seconds float64) string {
	if !(seconds > 0) {
		return "0m"
	}
	mins := int64(seconds) / 60
	if mins == 0 {
		return "<1m"
	}
	h, m := mins/60, mins%60
	switch {
	case h == 0:
		return strconv.FormatInt(m, 10) + "m"
	case m == 0:
		return strconv.FormatInt(h, 10) + "h"
	}
	return strconv.FormatInt(h, 10) + "h " + strconv.FormatInt(m, 10) + "m"
}

// statsCount returns "1 entry" or "3 entries": n with its plural noun.
func statsCount(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

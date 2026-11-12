package stats_test

import (
	"math"
	"testing"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/stats"
)

// The concrete media shapes below live only in tests: the core package stays
// media-agnostic and learns every rule from a kind's StatsDef.

func num(f float64) *float64 { return &f }

func day(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}

func rating(v int) *int { return &v }

func mkRow(id int64, status library.Status, finished *time.Time) library.Row {
	return library.Row{Item: library.Item{ID: id}, Entry: library.Entry{ItemID: id, Status: status, FinishedOn: finished}}
}

func withKeys(r library.Row, keys ...string) library.Row {
	r.Item.ProgressKeys = keys
	return r
}

func withProgress(r library.Row, values map[string]float64) library.Row {
	r.Entry.Progress = values
	return r
}

func withFields(r library.Row, fields map[string]plugin.FieldValue) library.Row {
	r.Item.Fields = fields
	return r
}

func withRating(r library.Row, v int) library.Row {
	r.Entry.Rating = rating(v)
	return r
}

func withGenres(r library.Row, genres ...string) library.Row {
	r.Item.Genres = genres
	return r
}

func withTags(r library.Row, tags ...string) library.Row {
	for _, t := range tags {
		r.Item.Tags = append(r.Item.Tags, plugin.Tag{Name: t})
	}
	return r
}

func movieKind() plugin.KindDef {
	return plugin.KindDef{
		Key: "movie", Label: "Movie", LabelPlural: "Movies",
		Fields: []plugin.FieldDef{
			{Key: "runtime", Label: "Runtime", Type: plugin.FieldNumber, Min: num(1), Max: num(10000)},
		},
		Genres:   plugin.TermDef{Enabled: true, Label: "Genres", Filterable: true},
		Tags:     plugin.TermDef{Enabled: true, Label: "Tags", Filterable: true},
		Statuses: plugin.StatusLabels{Planned: "Planned", Active: "Watching", Paused: "Paused", Completed: "Watched", Dropped: "Dropped"},
		Progress: plugin.ProgressSpec{
			Eligible:     []plugin.ProgressDef{{Key: "position", Type: plugin.Duration, Label: "Position"}},
			Defaults:     []string{"position"},
			Combinations: [][]string{{"position"}},
		},
		Stats: &plugin.StatsDef{
			Time: &plugin.StatsTimeDef{Label: "Watchtime", CompletedField: "runtime", DurationKey: "position"},
		},
	}
}

func showKind() plugin.KindDef {
	return plugin.KindDef{
		Key: "show", Label: "Show", LabelPlural: "Shows",
		Fields: []plugin.FieldDef{
			{Key: "runtime", Label: "Runtime", Type: plugin.FieldNumber, Unit: "min"},
			{Key: "episodes", Label: "Episodes", Type: plugin.FieldNumber},
		},
		Genres:   plugin.TermDef{Enabled: true, Label: "Genres", Filterable: true},
		Tags:     plugin.TermDef{Enabled: true, Label: "Tags", Filterable: true},
		Statuses: plugin.StatusLabels{Planned: "Planned", Active: "Watching", Paused: "Paused", Completed: "Watched", Dropped: "Dropped"},
		Progress: plugin.ProgressSpec{
			Eligible: []plugin.ProgressDef{
				{Key: "episode", Type: plugin.Count, Label: "Episode", TotalField: "episodes"},
				{Key: "position", Type: plugin.Duration, Label: "Position"},
			},
			Defaults:     []string{"episode"},
			Combinations: [][]string{{"episode"}, {"episode", "position"}},
			PrimaryKey:   "episode",
		},
		Stats: &plugin.StatsDef{
			Time:  &plugin.StatsTimeDef{Label: "Watchtime", UnitsKey: "episode", PerUnitField: "runtime"},
			Units: &plugin.StatsUnitsDef{Key: "episode", LabelPlural: "Episodes"},
		},
	}
}

func animeKind() plugin.KindDef {
	return plugin.KindDef{
		Key: "anime", Label: "Anime", LabelPlural: "Anime",
		Fields: []plugin.FieldDef{
			{Key: "duration", Label: "Episode duration", Type: plugin.FieldNumber, Unit: "min"},
			{Key: "episodes", Label: "Episodes", Type: plugin.FieldNumber},
		},
		Genres:   plugin.TermDef{Enabled: true, Label: "Genres", Filterable: true},
		Tags:     plugin.TermDef{Enabled: true, Label: "Tags", Filterable: true},
		Statuses: plugin.StatusLabels{Planned: "Planned", Active: "Watching", Paused: "Paused", Completed: "Watched", Dropped: "Dropped"},
		Progress: plugin.ProgressSpec{
			Eligible:     []plugin.ProgressDef{{Key: "episode", Type: plugin.Count, Label: "Episode", TotalField: "episodes"}},
			Defaults:     []string{"episode"},
			Combinations: [][]string{{"episode"}},
			PrimaryKey:   "episode",
		},
		Stats: &plugin.StatsDef{
			Time:  &plugin.StatsTimeDef{Label: "Watchtime", UnitsKey: "episode", PerUnitField: "duration"},
			Units: &plugin.StatsUnitsDef{Key: "episode", LabelPlural: "Episodes"},
		},
	}
}

func gameKind() plugin.KindDef {
	return plugin.KindDef{
		Key: "game", Label: "Game", LabelPlural: "Games",
		Genres:   plugin.TermDef{Enabled: true, Label: "Genres", Filterable: true},
		Tags:     plugin.TermDef{Enabled: true, Label: "Tags", Filterable: true},
		Statuses: plugin.StatusLabels{Planned: "Planned", Active: "Playing", Paused: "Paused", Completed: "Played", Dropped: "Dropped"},
		Progress: plugin.ProgressSpec{
			Eligible:     []plugin.ProgressDef{{Key: "playtime", Type: plugin.Duration, Label: "Playtime"}},
			Defaults:     []string{"playtime"},
			Combinations: [][]string{{"playtime"}},
		},
		Stats: &plugin.StatsDef{
			Time: &plugin.StatsTimeDef{Label: "Playtime", DurationKey: "playtime"},
		},
	}
}

func bookKind() plugin.KindDef {
	return plugin.KindDef{
		Key: "book", Label: "Book", LabelPlural: "Books",
		Fields: []plugin.FieldDef{
			{Key: "pages", Label: "Pages", Type: plugin.FieldNumber},
		},
		Genres:   plugin.TermDef{Enabled: true, Label: "Genres", Filterable: true},
		Statuses: plugin.StatusLabels{Planned: "Want to read", Active: "Reading", Paused: "Paused", Completed: "Read", Dropped: "Dropped"},
		Progress: plugin.ProgressSpec{
			Eligible: []plugin.ProgressDef{
				{Key: "pages", Type: plugin.Pages, Label: "Page", TotalField: "pages"},
				{Key: "percent", Type: plugin.Percent, Label: "Percent"},
			},
			Defaults:     []string{"pages"},
			Combinations: [][]string{{"pages"}, {"percent"}},
			PrimaryKey:   "pages",
		},
		Stats: &plugin.StatsDef{
			Units: &plugin.StatsUnitsDef{Key: "pages", LabelPlural: "Pages"},
		},
	}
}

func mangaKind() plugin.KindDef {
	return plugin.KindDef{
		Key: "manga", Label: "Manga", LabelPlural: "Manga",
		Fields: []plugin.FieldDef{
			{Key: "chapters", Label: "Chapters", Type: plugin.FieldNumber},
		},
		Genres:   plugin.TermDef{Enabled: true, Label: "Genres", Filterable: true},
		Statuses: plugin.StatusLabels{Planned: "Planned", Active: "Reading", Paused: "Paused", Completed: "Read", Dropped: "Dropped"},
		Progress: plugin.ProgressSpec{
			Eligible:     []plugin.ProgressDef{{Key: "chapter", Type: plugin.Count, Label: "Chapter"}},
			Defaults:     []string{"chapter"},
			Combinations: [][]string{{"chapter"}},
			PrimaryKey:   "chapter",
		},
		Stats: &plugin.StatsDef{
			Units: &plugin.StatsUnitsDef{Key: "chapter", LabelPlural: "Chapters"},
		},
	}
}

func seriesKind() plugin.KindDef {
	return plugin.KindDef{
		Key: "series", Label: "Series", LabelPlural: "Series",
		Fields: []plugin.FieldDef{
			{Key: "author", Label: "Author", Type: plugin.FieldText, Filterable: true},
		},
		Genres:   plugin.TermDef{Enabled: true, Label: "Genres", Filterable: true},
		Statuses: plugin.StatusLabels{Planned: "Planned", Active: "Reading", Paused: "Paused", Completed: "Read", Dropped: "Dropped"},
	}
}

func approx(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

func TestTimeRules(t *testing.T) {
	tests := []struct {
		name     string
		kind     plugin.KindDef
		row      library.Row
		seconds  float64
		entries  int
		missing  int
		units    float64
		unitRows int
		timeLbl  string
		unitLbl  string
	}{
		{
			name:    "completed movie uses runtime minutes",
			kind:    movieKind(),
			row:     withFields(withKeys(mkRow(1, library.Completed, day(2026, 1, 10)), "position"), map[string]plugin.FieldValue{"runtime": {Num: num(120)}}),
			seconds: 7200, entries: 1, timeLbl: "Watchtime",
		},
		{
			name:    "active movie uses position seconds",
			kind:    movieKind(),
			row:     withProgress(withKeys(mkRow(1, library.Active, nil), "position"), map[string]float64{"position": 1800}),
			seconds: 1800, entries: 1, timeLbl: "Watchtime",
		},
		{
			name:    "completed movie without runtime falls back to position",
			kind:    movieKind(),
			row:     withProgress(withKeys(mkRow(1, library.Completed, day(2026, 1, 10)), "position"), map[string]float64{"position": 3000}),
			seconds: 3000, entries: 1, timeLbl: "Watchtime",
		},
		{
			name:    "movie position is capped by runtime",
			kind:    movieKind(),
			row:     withFields(withProgress(withKeys(mkRow(1, library.Active, nil), "position"), map[string]float64{"position": 9000}), map[string]plugin.FieldValue{"runtime": {Num: num(120)}}),
			seconds: 7200, entries: 1, timeLbl: "Watchtime",
		},
		{
			name:    "show episodes times runtime",
			kind:    showKind(),
			row:     withFields(withProgress(withKeys(mkRow(1, library.Active, nil), "episode"), map[string]float64{"episode": 10}), map[string]plugin.FieldValue{"runtime": {Num: num(30)}}),
			seconds: 18000, entries: 1, units: 10, unitRows: 1, timeLbl: "Watchtime", unitLbl: "Episodes",
		},
		{
			name:    "anime episodes times duration",
			kind:    animeKind(),
			row:     withFields(withProgress(withKeys(mkRow(1, library.Active, nil), "episode"), map[string]float64{"episode": 12}), map[string]plugin.FieldValue{"duration": {Num: num(24)}}),
			seconds: 17280, entries: 1, units: 12, unitRows: 1, timeLbl: "Watchtime", unitLbl: "Episodes",
		},
		{
			name:    "game playtime is seconds",
			kind:    gameKind(),
			row:     withProgress(withKeys(mkRow(1, library.Active, nil), "playtime"), map[string]float64{"playtime": 3600}),
			seconds: 3600, entries: 1, timeLbl: "Playtime",
		},
		{
			name:    "completed show without episode progress falls back to the total",
			kind:    showKind(),
			row:     withFields(withProgress(withKeys(mkRow(1, library.Completed, day(2026, 2, 1)), "episode"), map[string]float64{"episode": 0}), map[string]plugin.FieldValue{"episodes": {Num: num(12)}, "runtime": {Num: num(30)}}),
			seconds: 21600, entries: 1, units: 12, unitRows: 1, timeLbl: "Watchtime", unitLbl: "Episodes",
		},
		{
			name:    "episodes without a per-unit runtime are missing time",
			kind:    showKind(),
			row:     withProgress(withKeys(mkRow(1, library.Active, nil), "episode"), map[string]float64{"episode": 3}),
			missing: 1, units: 3, unitRows: 1, timeLbl: "Watchtime", unitLbl: "Episodes",
		},
		{
			name:    "inactive progress keys are ignored",
			kind:    showKind(),
			row:     withFields(withProgress(withKeys(mkRow(1, library.Active, nil), "position"), map[string]float64{"episode": 3, "position": 0}), map[string]plugin.FieldValue{"runtime": {Num: num(30)}}),
			missing: 1, timeLbl: "Watchtime", unitLbl: "Episodes",
		},
		{
			// no stored duration value and no completed field: nothing to use
			name:    "completed entry without a duration value or completed field is missing",
			kind:    movieKind(),
			row:     mkRow(1, library.Completed, day(2026, 1, 10)),
			missing: 1, timeLbl: "Watchtime",
		},
		{
			name:    "completed entry at zero without a completed field is missing",
			kind:    gameKind(),
			row:     withProgress(withKeys(mkRow(1, library.Completed, day(2026, 1, 10)), "playtime"), map[string]float64{"playtime": 0}),
			missing: 1, timeLbl: "Playtime",
		},
		{
			name:    "in-progress entry at zero is a known zero",
			kind:    gameKind(),
			row:     withProgress(withKeys(mkRow(1, library.Active, nil), "playtime"), map[string]float64{"playtime": 0}),
			timeLbl: "Playtime",
		},
		{
			name:    "in-progress position zero without a runtime is a known zero",
			kind:    movieKind(),
			row:     withProgress(withKeys(mkRow(1, library.Active, nil), "position"), map[string]float64{"position": 0}),
			timeLbl: "Watchtime",
		},
		{
			name:    "completed position zero without a runtime is missing",
			kind:    movieKind(),
			row:     withProgress(withKeys(mkRow(1, library.Completed, day(2026, 1, 10)), "position"), map[string]float64{"position": 0}),
			missing: 1, timeLbl: "Watchtime",
		},
		{
			// zero units in a non-completed entry is a known zero, not missing
			name:    "planned zero units without a per-unit field is a known zero",
			kind:    animeKind(),
			row:     withFields(withProgress(withKeys(mkRow(1, library.Planned, nil), "episode"), map[string]float64{"episode": 0}), map[string]plugin.FieldValue{"episodes": {Num: num(24)}}),
			timeLbl: "Watchtime", unitLbl: "Episodes",
		},
		{
			// completed units fall back to the bounded total, which is absent
			name:    "completed zero units without a total is missing",
			kind:    showKind(),
			row:     withProgress(withKeys(mkRow(1, library.Completed, day(2026, 2, 1)), "episode"), map[string]float64{"episode": 0}),
			missing: 1, timeLbl: "Watchtime", unitLbl: "Episodes",
		},
		{
			// the fallback finds units but no per-unit field to turn into time
			name:    "completed zero units with a total but no per-unit field is missing",
			kind:    showKind(),
			row:     withFields(withProgress(withKeys(mkRow(1, library.Completed, day(2026, 2, 1)), "episode"), map[string]float64{"episode": 0}), map[string]plugin.FieldValue{"episodes": {Num: num(12)}}),
			missing: 1, units: 12, unitRows: 1, timeLbl: "Watchtime", unitLbl: "Episodes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := stats.Build([]stats.Input{{PluginID: "p", Kind: tt.kind, Rows: []library.Row{tt.row}}}, stats.Options{})
			if len(rep.Kinds) != 1 {
				t.Fatalf("kinds = %d, want 1", len(rep.Kinds))
			}
			kr := rep.Kinds[0]
			approx(t, "TimeSeconds", kr.TimeSeconds, tt.seconds)
			if kr.TimeEntries != tt.entries {
				t.Fatalf("TimeEntries = %d, want %d", kr.TimeEntries, tt.entries)
			}
			if kr.MissingTime != tt.missing {
				t.Fatalf("MissingTime = %d, want %d", kr.MissingTime, tt.missing)
			}
			approx(t, "Units", kr.Units, tt.units)
			if kr.UnitEntries != tt.unitRows {
				t.Fatalf("UnitEntries = %d, want %d", kr.UnitEntries, tt.unitRows)
			}
			if kr.TimeLabel != tt.timeLbl {
				t.Fatalf("TimeLabel = %q, want %q", kr.TimeLabel, tt.timeLbl)
			}
			if kr.UnitLabel != tt.unitLbl {
				t.Fatalf("UnitLabel = %q, want %q", kr.UnitLabel, tt.unitLbl)
			}
			if rep.All.MissingTime != tt.missing {
				t.Fatalf("All.MissingTime = %d, want %d", rep.All.MissingTime, tt.missing)
			}
		})
	}
}

func TestUnitRules(t *testing.T) {
	tests := []struct {
		name     string
		kind     plugin.KindDef
		row      library.Row
		units    float64
		unitRows int
	}{
		{
			name:     "book pages",
			kind:     bookKind(),
			row:      withFields(withProgress(withKeys(mkRow(1, library.Active, nil), "pages"), map[string]float64{"pages": 120}), map[string]plugin.FieldValue{"pages": {Num: num(300)}}),
			units:    120,
			unitRows: 1,
		},
		{
			name:     "manga chapters",
			kind:     mangaKind(),
			row:      withProgress(withKeys(mkRow(1, library.Active, nil), "chapter"), map[string]float64{"chapter": 40}),
			units:    40,
			unitRows: 1,
		},
		{
			name:     "completed book at zero falls back to the bounded total",
			kind:     bookKind(),
			row:      withFields(withProgress(withKeys(mkRow(1, library.Completed, day(2026, 3, 1)), "pages"), map[string]float64{"pages": 0}), map[string]plugin.FieldValue{"pages": {Num: num(300)}}),
			units:    300,
			unitRows: 1,
		},
		{
			name:     "completed book at zero without a total stays zero",
			kind:     bookKind(),
			row:      withProgress(withKeys(mkRow(1, library.Completed, day(2026, 3, 1)), "pages"), map[string]float64{"pages": 0}),
			units:    0,
			unitRows: 0,
		},
		{
			name:     "active book at zero does not fall back",
			kind:     bookKind(),
			row:      withFields(withProgress(withKeys(mkRow(1, library.Active, nil), "pages"), map[string]float64{"pages": 0}), map[string]plugin.FieldValue{"pages": {Num: num(300)}}),
			units:    0,
			unitRows: 0,
		},
		{
			name:     "inactive units key is ignored",
			kind:     bookKind(),
			row:      withFields(withProgress(withKeys(mkRow(1, library.Active, nil), "percent"), map[string]float64{"pages": 120, "percent": 40}), map[string]plugin.FieldValue{"pages": {Num: num(300)}}),
			units:    0,
			unitRows: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := stats.Build([]stats.Input{{PluginID: "p", Kind: tt.kind, Rows: []library.Row{tt.row}}}, stats.Options{})
			kr := rep.Kinds[0]
			approx(t, "Units", kr.Units, tt.units)
			if kr.UnitEntries != tt.unitRows {
				t.Fatalf("UnitEntries = %d, want %d", kr.UnitEntries, tt.unitRows)
			}
			approx(t, "TimeSeconds", kr.TimeSeconds, 0)
			if kr.MissingTime != 0 {
				t.Fatalf("MissingTime = %d, want 0 (the kind declares no time)", kr.MissingTime)
			}
			if rep.All.Units != 0 || rep.All.UnitEntries != 0 {
				t.Fatalf("All units = %v/%d, want 0/0", rep.All.Units, rep.All.UnitEntries)
			}
		})
	}
}

func TestPeriodInclusivity(t *testing.T) {
	rows := []library.Row{
		mkRow(1, library.Completed, day(2025, 12, 31)),
		mkRow(2, library.Completed, day(2026, 1, 1)),
		withProgress(withKeys(mkRow(3, library.Completed, day(2026, 6, 15)), "playtime"), map[string]float64{"playtime": 3600}),
		mkRow(4, library.Completed, day(2026, 12, 31)),
		mkRow(5, library.Completed, day(2027, 1, 1)),
		mkRow(6, library.Completed, nil),
	}
	from, to := day(2026, 1, 1), day(2026, 12, 31)
	rep := stats.Build([]stats.Input{{PluginID: "p", Kind: gameKind(), Rows: rows}}, stats.Options{From: from, To: to})
	kr := rep.Kinds[0]
	if kr.Entries != 3 {
		t.Fatalf("Entries = %d, want 3 (the range includes both bounds)", kr.Entries)
	}
	if kr.Completed != 3 {
		t.Fatalf("Completed = %d, want 3", kr.Completed)
	}
	if kr.UndatedCompleted != 0 {
		t.Fatalf("UndatedCompleted = %d, want 0 inside a period", kr.UndatedCompleted)
	}
	approx(t, "TimeSeconds", kr.TimeSeconds, 3600)

	// All time keeps every entry and reports completed entries without a date.
	rep = stats.Build([]stats.Input{{PluginID: "p", Kind: gameKind(), Rows: rows}}, stats.Options{})
	if rep.Kinds[0].Entries != 6 {
		t.Fatalf("all-time Entries = %d, want 6", rep.Kinds[0].Entries)
	}
	if rep.Kinds[0].UndatedCompleted != 1 {
		t.Fatalf("all-time UndatedCompleted = %d, want 1", rep.Kinds[0].UndatedCompleted)
	}

	// One open end still excludes undated entries: the range is a period.
	rep = stats.Build([]stats.Input{{PluginID: "p", Kind: gameKind(), Rows: rows}}, stats.Options{From: from})
	if rep.Kinds[0].Entries != 4 {
		t.Fatalf("open-start Entries = %d, want 4", rep.Kinds[0].Entries)
	}
}

func TestRatings(t *testing.T) {
	rows := []library.Row{
		mkRow(1, library.Completed, nil),
		withRating(mkRow(2, library.Completed, nil), 0),
		withRating(mkRow(3, library.Completed, nil), 7),
		withRating(mkRow(4, library.Dropped, nil), 10),
	}
	rep := stats.Build([]stats.Input{{PluginID: "p", Kind: seriesKind(), Rows: rows}}, stats.Options{})
	kr := rep.Kinds[0]
	if kr.Rated != 3 {
		t.Fatalf("Rated = %d, want 3 (an explicit 0 is a rating, NULL is not)", kr.Rated)
	}
	if kr.RatingSum != 17 {
		t.Fatalf("RatingSum = %d, want 17", kr.RatingSum)
	}
	approx(t, "RatingAverage", kr.RatingAverage, 17.0/3.0)
	want := [11]int{1, 0, 0, 0, 0, 0, 0, 1, 0, 0, 1}
	if kr.RatingDistribution != want {
		t.Fatalf("RatingDistribution = %v, want %v", kr.RatingDistribution, want)
	}
	if kr.Entries-kr.Rated != 1 {
		t.Fatalf("unrated = %d, want 1", kr.Entries-kr.Rated)
	}
	approx(t, "All.RatingAverage", rep.All.RatingAverage, 17.0/3.0)

	// Without any rating the average is zero, not a division by zero.
	rep = stats.Build([]stats.Input{{PluginID: "p", Kind: seriesKind(), Rows: []library.Row{mkRow(1, library.Completed, nil)}}}, stats.Options{})
	if rep.Kinds[0].RatingAverage != 0 {
		t.Fatalf("RatingAverage = %v, want 0", rep.Kinds[0].RatingAverage)
	}
}

func TestStatusCounts(t *testing.T) {
	rows := []library.Row{
		mkRow(1, library.Planned, nil),
		mkRow(2, library.Active, nil),
		mkRow(3, library.Completed, nil),
		mkRow(4, library.Completed, nil),
		mkRow(5, library.Dropped, nil),
	}
	rep := stats.Build([]stats.Input{{PluginID: "p", Kind: seriesKind(), Rows: rows}}, stats.Options{})
	for _, tc := range []struct {
		status library.Status
		want   int
	}{
		{library.Planned, 1}, {library.Active, 1}, {library.Paused, 0},
		{library.Completed, 2}, {library.Dropped, 1},
	} {
		if got := rep.Kinds[0].Statuses[plugin.StatusClass(tc.status)]; got != tc.want {
			t.Fatalf("kind status %s = %d, want %d", tc.status, got, tc.want)
		}
		if got := rep.All.Statuses[plugin.StatusClass(tc.status)]; got != tc.want {
			t.Fatalf("all status %s = %d, want %d", tc.status, got, tc.want)
		}
	}
	if rep.Kinds[0].Completed != 2 {
		t.Fatalf("Completed = %d, want 2", rep.Kinds[0].Completed)
	}
}

func bucketMiss(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("bucket labels = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("bucket labels = %v, want %v", got, want)
		}
	}
}

func labels(bs []stats.Bucket) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.Label
	}
	return out
}

func TestBuckets(t *testing.T) {
	game := gameKind()
	play := func(id int64, finished *time.Time, seconds float64) library.Row {
		r := mkRow(id, library.Completed, finished)
		if seconds > 0 {
			r = withProgress(withKeys(r, "playtime"), map[string]float64{"playtime": seconds})
		}
		return r
	}
	tests := []struct {
		name   string
		rows   []library.Row
		opts   stats.Options
		labels []string
		values []float64
		kind   plugin.KindDef
	}{
		{
			name:   "monthly range is continuous",
			rows:   []library.Row{play(1, day(2024, 2, 10), 3600), play(2, day(2024, 2, 20), 1800)},
			opts:   stats.Options{From: day(2024, 1, 15), To: day(2024, 3, 2), Bucket: stats.BucketMonth},
			labels: []string{"2024-01", "2024-02", "2024-03"},
			values: []float64{0, 5400, 0},
		},
		{
			name:   "yearly range is continuous",
			rows:   []library.Row{play(1, day(2023, 5, 1), 3600), play(2, day(2025, 2, 1), 1800)},
			opts:   stats.Options{From: day(2023, 5, 1), To: day(2025, 2, 1), Bucket: stats.BucketYear},
			labels: []string{"2023", "2024", "2025"},
			values: []float64{3600, 0, 1800},
		},
		{
			name:   "auto keeps months up to 24",
			rows:   []library.Row{play(1, day(2024, 6, 1), 60)},
			opts:   stats.Options{From: day(2024, 1, 1), To: day(2025, 12, 31), Bucket: stats.BucketAuto},
			labels: months("2024-01", 24),
			values: appendZeros(5, 60, 24),
		},
		{
			name:   "auto switches to years above 24 months",
			rows:   []library.Row{play(1, day(2024, 6, 1), 60)},
			opts:   stats.Options{From: day(2024, 1, 1), To: day(2026, 1, 1), Bucket: stats.BucketAuto},
			labels: []string{"2024", "2025", "2026"},
			values: []float64{60, 0, 0},
		},
		{
			name:   "empty bucket string behaves like auto",
			rows:   []library.Row{play(1, day(2024, 6, 1), 60)},
			opts:   stats.Options{From: day(2024, 1, 1), To: day(2026, 1, 1)},
			labels: []string{"2024", "2025", "2026"},
			values: []float64{60, 0, 0},
		},
		{
			name:   "an open start uses the earliest selected finished date",
			rows:   []library.Row{play(1, day(2024, 3, 10), 60), play(2, day(2024, 5, 20), 120)},
			opts:   stats.Options{From: day(2024, 1, 1), Bucket: stats.BucketMonth},
			labels: []string{"2024-01", "2024-02", "2024-03", "2024-04", "2024-05"},
			values: []float64{0, 0, 60, 0, 120},
		},
		{
			name:   "an open end uses the latest selected finished date",
			rows:   []library.Row{play(1, day(2024, 1, 5), 60)},
			opts:   stats.Options{To: day(2024, 3, 31), Bucket: stats.BucketMonth},
			labels: []string{"2024-01", "2024-02", "2024-03"},
			values: []float64{60, 0, 0},
		},
		{
			name:   "all time uses the rows and yearly buckets",
			rows:   []library.Row{play(1, day(2022, 6, 1), 3600), play(2, day(2024, 2, 1), 1800), play(3, nil, 600)},
			opts:   stats.Options{Bucket: stats.BucketYear},
			labels: []string{"2022", "2023", "2024"},
			values: []float64{3600, 0, 1800},
		},
		{
			name:   "all time stays yearly even when month is requested",
			rows:   []library.Row{play(1, day(2024, 2, 1), 1800)},
			opts:   stats.Options{Bucket: stats.BucketMonth},
			labels: []string{"2024"},
			values: []float64{1800},
		},
		{
			name:   "undated entries never enter a bucket",
			rows:   []library.Row{play(1, day(2024, 2, 1), 1800), play(2, nil, 600)},
			opts:   stats.Options{From: day(2024, 1, 1), To: day(2024, 3, 31), Bucket: stats.BucketMonth},
			labels: []string{"2024-01", "2024-02", "2024-03"},
			values: []float64{0, 1800, 0},
		},
		{
			name:   "an entry without time still populates the axis",
			rows:   []library.Row{play(1, day(2024, 2, 1), 0)},
			opts:   stats.Options{From: day(2024, 1, 1), To: day(2024, 2, 29), Bucket: stats.BucketMonth},
			labels: []string{"2024-01", "2024-02"},
			values: []float64{0, 0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind := tt.kind
			if kind.Key == "" {
				kind = game
			}
			rep := stats.Build([]stats.Input{{PluginID: "p", Kind: kind, Rows: tt.rows}}, tt.opts)
			buckets := rep.Kinds[0].Buckets
			bucketMiss(t, labels(buckets), tt.labels)
			for i, want := range tt.values {
				approx(t, "bucket "+tt.labels[i], buckets[i].Seconds, want)
			}
			bucketMiss(t, labels(rep.Buckets), tt.labels)
			for i, want := range tt.values {
				approx(t, "all bucket "+tt.labels[i], rep.Buckets[i].Seconds, want)
			}
		})
	}
}

func months(start string, n int) []string {
	base, err := time.Parse("2006-01", start)
	if err != nil {
		panic(err)
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = base.AddDate(0, i, 0).Format("2006-01")
	}
	return out
}

func appendZeros(before int, value float64, n int) []float64 {
	out := make([]float64, n)
	out[before] = value
	return out
}

func TestDimensions(t *testing.T) {
	kind := plugin.KindDef{
		Key: "show", Label: "Show", LabelPlural: "Shows",
		Fields: []plugin.FieldDef{
			{Key: "network", Label: "Network", Type: plugin.FieldText, Filterable: true},
			{Key: "kind", Label: "Kind", Type: plugin.FieldEnum, EnumValues: []string{"series", "miniseries"}, Filterable: true},
			{Key: "year", Label: "Year", Type: plugin.FieldNumber, Filterable: true},
			{Key: "platforms", Label: "Platforms", Type: plugin.FieldList, Filterable: true},
			{Key: "studio", Label: "Studio", Type: plugin.FieldText, Filterable: true},
			{Key: "released", Label: "Released", Type: plugin.FieldDate, Filterable: true},
			{Key: "note", Label: "Note", Type: plugin.FieldText},
		},
		Genres:   plugin.TermDef{Enabled: true, Label: "Genres", Filterable: true},
		Tags:     plugin.TermDef{Enabled: true, Label: "Tags", Filterable: true},
		Statuses: plugin.StatusLabels{Planned: "Planned", Active: "Watching", Paused: "Paused", Completed: "Watched", Dropped: "Dropped"},
		Progress: plugin.ProgressSpec{
			Eligible:     []plugin.ProgressDef{{Key: "episode", Type: plugin.Count, Label: "Episode", TotalField: "episodes"}},
			Defaults:     []string{"episode"},
			Combinations: [][]string{{"episode"}},
			PrimaryKey:   "episode",
		},
		Stats: &plugin.StatsDef{
			Time:  &plugin.StatsTimeDef{Label: "Watchtime", UnitsKey: "episode", PerUnitField: "runtime"},
			Units: &plugin.StatsUnitsDef{Key: "episode", LabelPlural: "Episodes"},
		},
	}
	released := day(2020, 1, 1)
	r1 := withTags(withGenres(
		withFields(withRating(withProgress(withKeys(mkRow(1, library.Completed, day(2024, 1, 1)), "episode"), map[string]float64{"episode": 10}), 8),
			map[string]plugin.FieldValue{
				"network":   {Text: "Netflix"},
				"kind":      {Text: "series"},
				"year":      {Num: num(2020)},
				"platforms": {List: []string{"PC", "PC", "Console"}},
				"studio":    {Text: "A, B"},
				"released":  {Date: released},
				"note":      {Text: "n1"},
				"runtime":   {Num: num(30)},
			}), "Fantasy", "Fantasy"), "A", "A")
	r2 := withTags(withGenres(
		withFields(withProgress(withKeys(mkRow(2, library.Active, day(2024, 2, 1)), "episode"), map[string]float64{"episode": 2}),
			map[string]plugin.FieldValue{
				"network":   {Text: "Netflix"},
				"kind":      {Text: "miniseries"},
				"year":      {Num: num(2020.5)},
				"platforms": {List: []string{"PC"}},
				"studio":    {Text: "A, B"},
				"note":      {Text: "n2"},
				"runtime":   {Num: num(30)},
			}), "fantasy"), "A", "B")

	rep := stats.Build([]stats.Input{{PluginID: "p", Kind: kind, Rows: []library.Row{r1, r2}}}, stats.Options{})
	groups := rep.Kinds[0].Dimensions
	wantLabels := []string{"Genres", "Tags", "Network", "Kind", "Year", "Platforms", "Studio"}
	if len(groups) != len(wantLabels) {
		t.Fatalf("groups = %d, want %d: %+v", len(groups), len(wantLabels), groups)
	}
	for i, want := range wantLabels {
		if groups[i].Label != want {
			t.Fatalf("group %d label = %q, want %q", i, groups[i].Label, want)
		}
	}

	find := func(group int, value string) stats.Dimension {
		t.Helper()
		for _, d := range groups[group].Values {
			if d.Value == value {
				return d
			}
		}
		t.Fatalf("group %q has no value %q: %+v", groups[group].Label, value, groups[group].Values)
		return stats.Dimension{}
	}

	// Genres: exact duplicates on one entry count once, differing spellings stay separate.
	genres := groups[0].Values
	if len(genres) != 2 {
		t.Fatalf("genres = %+v, want two values", genres)
	}
	g := find(0, "Fantasy")
	if g.Entries != 1 || g.Rated != 1 || g.RatingSum != 8 {
		t.Fatalf("Fantasy = %+v, want 1 entry rated 8", g)
	}
	approx(t, "Fantasy time", g.TimeSeconds, 18000)
	approx(t, "Fantasy units", g.Units, 10)

	// Tags: "A" appears on both rows (twice on row 1) and counts each row once.
	a := find(1, "A")
	if a.Entries != 2 || a.Rated != 1 || a.RatingSum != 8 {
		t.Fatalf("tag A = %+v, want 2 entries rated once", a)
	}
	approx(t, "tag A time", a.TimeSeconds, 21600)
	approx(t, "tag A units", a.Units, 12)

	// Text is verbatim: a comma-joined value is one value.
	studio := find(6, "A, B")
	if studio.Entries != 2 {
		t.Fatalf("studio = %+v, want 2 entries", studio)
	}

	// List fields contribute each element separately, duplicates once.
	pc := find(5, "PC")
	if pc.Entries != 2 {
		t.Fatalf("platform PC = %+v, want 2 entries", pc)
	}
	console := find(5, "Console")
	if console.Entries != 1 {
		t.Fatalf("platform Console = %+v, want 1 entry", console)
	}
	approx(t, "Console time", console.TimeSeconds, 18000)

	// Numbers render without a decimal point when whole.
	if v := find(4, "2020"); v.Entries != 1 {
		t.Fatalf("year 2020 = %+v", v)
	}
	if v := find(4, "2020.5"); v.Entries != 1 {
		t.Fatalf("year 2020.5 = %+v", v)
	}

	// Date and non-filterable fields are not dimensions.
	for _, group := range groups {
		if group.Label == "Released" || group.Label == "Note" {
			t.Fatalf("unexpected dimension group %q", group.Label)
		}
	}
}

func TestAllMediaMergeAndTotals(t *testing.T) {
	movie := withTags(withGenres(
		withRating(withFields(mkRow(1, library.Completed, day(2024, 1, 1)), map[string]plugin.FieldValue{"runtime": {Num: num(120)}}), 8),
		"Fantasy", "Sci-Fi"), "TagOne")
	anime := withTags(withGenres(
		withProgress(withKeys(mkRow(2, library.Completed, day(2024, 1, 5)), "episode"), map[string]float64{"episode": 4}),
		"fantasy"), "tagone")
	anime = withFields(anime, map[string]plugin.FieldValue{"duration": {Num: num(24)}})

	rep := stats.Build([]stats.Input{
		{PluginID: "p1", Kind: movieKind(), Rows: []library.Row{movie}},
		{PluginID: "p2", Kind: animeKind(), Rows: []library.Row{anime}},
	}, stats.Options{})

	if len(rep.Kinds) != 2 {
		t.Fatalf("kinds = %d, want 2", len(rep.Kinds))
	}
	if rep.Kinds[0].PluginID != "p1" || rep.Kinds[0].Kind != "movie" || rep.Kinds[0].Label != "Movie" {
		t.Fatalf("kind 0 = %+v", rep.Kinds[0])
	}
	if rep.Kinds[1].PluginID != "p2" || rep.Kinds[1].Kind != "anime" {
		t.Fatalf("kind 1 = %+v", rep.Kinds[1])
	}

	// Genres and tags merge case-insensitively across kinds; the first spelling wins.
	if len(rep.Genres) != 2 {
		t.Fatalf("genres = %+v, want Fantasy and Sci-Fi", rep.Genres)
	}
	if rep.Genres[0].Value != "Fantasy" || rep.Genres[0].Entries != 2 {
		t.Fatalf("merged genre = %+v, want Fantasy with 2 entries", rep.Genres[0])
	}
	if rep.Genres[0].Rated != 1 || rep.Genres[0].RatingSum != 8 {
		t.Fatalf("merged genre ratings = %+v", rep.Genres[0])
	}
	approx(t, "merged genre time", rep.Genres[0].TimeSeconds, 7200+5760)
	if len(rep.Tags) != 1 || rep.Tags[0].Value != "TagOne" || rep.Tags[0].Entries != 2 {
		t.Fatalf("merged tag = %+v, want TagOne with 2 entries", rep.Tags)
	}

	// All totals sum counts, ratings, statuses and time, but never units.
	if rep.All.Entries != 2 || rep.All.Completed != 2 {
		t.Fatalf("All entries/completed = %d/%d, want 2/2", rep.All.Entries, rep.All.Completed)
	}
	if rep.All.Rated != 1 || rep.All.RatingSum != 8 {
		t.Fatalf("All ratings = %d/%d, want 1/8", rep.All.Rated, rep.All.RatingSum)
	}
	approx(t, "All rating average", rep.All.RatingAverage, 8)
	approx(t, "All time", rep.All.TimeSeconds, 12960)
	if rep.All.TimeEntries != 2 {
		t.Fatalf("All TimeEntries = %d, want 2", rep.All.TimeEntries)
	}
	if rep.All.Units != 0 || rep.All.UnitEntries != 0 {
		t.Fatalf("All units = %v/%d, want 0/0", rep.All.Units, rep.All.UnitEntries)
	}
	if rep.All.Statuses[plugin.Completed] != 2 {
		t.Fatalf("All completed status = %d, want 2", rep.All.Statuses[plugin.Completed])
	}
	if rep.All.TimeLabel != "" || rep.All.UnitLabel != "" {
		t.Fatalf("All labels = %q/%q, want empty", rep.All.TimeLabel, rep.All.UnitLabel)
	}
	// Per-kind units stay separate.
	approx(t, "anime units", rep.Kinds[1].Units, 4)
	if rep.Kinds[0].Units != 0 {
		t.Fatalf("movie units = %v, want 0", rep.Kinds[0].Units)
	}
}

// unitTimeKind is a kind with one units track and one duration track, so a
// merged-term test can check that time adds up while units do not.
func unitTimeKind(key, label, unitKey, totalField string) plugin.KindDef {
	return plugin.KindDef{
		Key: key, Label: label, LabelPlural: label + "s",
		Fields:   []plugin.FieldDef{{Key: totalField, Label: totalField, Type: plugin.FieldNumber}},
		Genres:   plugin.TermDef{Enabled: true, Label: "Genres", Filterable: true},
		Tags:     plugin.TermDef{Enabled: true, Label: "Tags", Filterable: true},
		Statuses: plugin.StatusLabels{Planned: "Planned", Active: "Active", Paused: "Paused", Completed: "Completed", Dropped: "Dropped"},
		Progress: plugin.ProgressSpec{
			Eligible: []plugin.ProgressDef{
				{Key: unitKey, Type: plugin.Count, Label: label, TotalField: totalField},
				{Key: "time", Type: plugin.Duration, Label: "Time"},
			},
			Defaults:     []string{unitKey, "time"},
			Combinations: [][]string{{unitKey, "time"}},
			PrimaryKey:   unitKey,
		},
		Stats: &plugin.StatsDef{
			Time:  &plugin.StatsTimeDef{Label: "Time", DurationKey: "time"},
			Units: &plugin.StatsUnitsDef{Key: unitKey, LabelPlural: label + "s"},
		},
	}
}

func TestMergedTermsOmitUnits(t *testing.T) {
	book := unitTimeKind("book", "book", "pages", "pages")
	manga := unitTimeKind("manga", "manga", "chapter", "chapters")
	bookRow := withTags(withGenres(
		withRating(withProgress(withKeys(mkRow(1, library.Completed, day(2024, 1, 1)), "pages", "time"), map[string]float64{"pages": 120, "time": 3600}), 8),
		"Fantasy"), "Shared")
	mangaRow := withTags(withGenres(
		withProgress(withKeys(mkRow(2, library.Completed, day(2024, 2, 1)), "chapter", "time"), map[string]float64{"chapter": 40, "time": 1800}),
		"fantasy"), "shared")

	rep := stats.Build([]stats.Input{
		{PluginID: "p1", Kind: book, Rows: []library.Row{bookRow}},
		{PluginID: "p2", Kind: manga, Rows: []library.Row{mangaRow}},
	}, stats.Options{})

	// A merged term adds entries and time across kinds but never units.
	if len(rep.Genres) != 1 {
		t.Fatalf("genres = %+v, want one merged value", rep.Genres)
	}
	g := rep.Genres[0]
	if g.Value != "Fantasy" || g.Entries != 2 {
		t.Fatalf("merged genre = %+v, want Fantasy with 2 entries", g)
	}
	approx(t, "merged genre time", g.TimeSeconds, 5400)
	if g.Units != 0 {
		t.Fatalf("merged genre units = %v, want 0 (pages and chapters are not comparable)", g.Units)
	}
	if len(rep.Tags) != 1 {
		t.Fatalf("tags = %+v, want one merged value", rep.Tags)
	}
	tag := rep.Tags[0]
	if tag.Value != "Shared" || tag.Entries != 2 {
		t.Fatalf("merged tag = %+v, want Shared with 2 entries", tag)
	}
	approx(t, "merged tag time", tag.TimeSeconds, 5400)
	if tag.Units != 0 {
		t.Fatalf("merged tag units = %v, want 0", tag.Units)
	}

	// The per-kind dimensions keep their own units.
	approx(t, "book genre units", rep.Kinds[0].Dimensions[0].Values[0].Units, 120)
	approx(t, "manga genre units", rep.Kinds[1].Dimensions[0].Values[0].Units, 40)
}

func TestKindWithoutStats(t *testing.T) {
	row := withGenres(withRating(withFields(mkRow(1, library.Completed, day(2024, 4, 1)), map[string]plugin.FieldValue{"author": {Text: "Someone"}}), 9), "Drama")
	rep := stats.Build([]stats.Input{{PluginID: "p", Kind: seriesKind(), Rows: []library.Row{row}}}, stats.Options{})
	kr := rep.Kinds[0]
	if kr.Entries != 1 || kr.Completed != 1 || kr.UndatedCompleted != 0 {
		t.Fatalf("counts = %+v", kr.Totals)
	}
	if kr.Rated != 1 || kr.RatingSum != 9 {
		t.Fatalf("ratings = %d/%d, want 1/9", kr.Rated, kr.RatingSum)
	}
	if kr.TimeSeconds != 0 || kr.TimeEntries != 0 || kr.MissingTime != 0 || kr.Units != 0 || kr.UnitEntries != 0 {
		t.Fatalf("stats without a declaration = %+v", kr.Totals)
	}
	if kr.TimeLabel != "" || kr.UnitLabel != "" {
		t.Fatalf("labels = %q/%q, want empty", kr.TimeLabel, kr.UnitLabel)
	}
	if len(kr.Dimensions) != 2 {
		t.Fatalf("dimensions = %+v, want genres and author", kr.Dimensions)
	}
	if kr.Dimensions[0].Label != "Genres" || kr.Dimensions[0].Values[0].Value != "Drama" {
		t.Fatalf("genre dimension = %+v", kr.Dimensions[0])
	}
	if kr.Dimensions[1].Label != "Author" || kr.Dimensions[1].Values[0].Value != "Someone" {
		t.Fatalf("author dimension = %+v", kr.Dimensions[1])
	}
}

func TestEmptyBuild(t *testing.T) {
	rep := stats.Build(nil, stats.Options{})
	if len(rep.Kinds) != 0 {
		t.Fatalf("kinds = %d, want 0", len(rep.Kinds))
	}
	if rep.All.Entries != 0 || rep.All.RatingAverage != 0 || len(rep.Buckets) != 0 {
		t.Fatalf("empty report = %+v", rep.All)
	}
	if len(rep.Genres) != 0 || len(rep.Tags) != 0 {
		t.Fatalf("empty terms = %+v/%+v", rep.Genres, rep.Tags)
	}
	for _, s := range plugin.StatusClasses() {
		if rep.All.Statuses[s] != 0 {
			t.Fatalf("status %s = %d, want 0", s, rep.All.Statuses[s])
		}
	}
}

package stats_test

import (
	"testing"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/stats"
)

func withRuns(r library.Row, runs ...library.Run) library.Row {
	r.PastRuns = runs
	return r
}

func movieRuntime(r library.Row, minutes float64) library.Row {
	return withFields(r, map[string]plugin.FieldValue{"runtime": {Num: num(minutes)}})
}

func movieInput(rows ...library.Row) []stats.Input {
	return []stats.Input{{PluginID: "p1", Kind: movieKind(), Rows: rows}}
}

// rewatched is a movie completed in 2025 and watched again in 2026.
func rewatched() library.Row {
	r := movieRuntime(withRating(mkRow(1, library.Completed, day(2026, 5, 1)), 9), 120)
	return withRuns(r, library.Run{Number: 1, Status: library.Completed, FinishedOn: day(2025, 4, 1)})
}

func TestRewatchAddsTimeToBothYears(t *testing.T) {
	y := func(year int) stats.Options {
		return stats.Options{From: day(year, 1, 1), To: day(year, 12, 31)}
	}
	r25 := stats.Build(movieInput(rewatched()), y(2025))
	approx(t, "2025 time", r25.All.TimeSeconds, 7200)
	if r25.All.Entries != 0 || r25.All.Completed != 0 {
		t.Fatalf("2025 entries/completed = %d/%d, want 0/0 (the entry finished in 2026)", r25.All.Entries, r25.All.Completed)
	}
	if got := r25.Buckets; len(got) != 12 || got[3].Seconds != 7200 {
		t.Fatalf("2025 buckets = %+v, want the past run in 2025-04", got)
	}
	r26 := stats.Build(movieInput(rewatched()), y(2026))
	approx(t, "2026 time", r26.All.TimeSeconds, 7200)
	if r26.All.Entries != 1 || r26.All.Completed != 1 || r26.All.Rated != 1 {
		t.Fatalf("2026 entries/completed/rated = %d/%d/%d, want 1/1/1", r26.All.Entries, r26.All.Completed, r26.All.Rated)
	}
	all := stats.Build(movieInput(rewatched()), stats.Options{})
	approx(t, "all-time time", all.All.TimeSeconds, 14400)
	approx(t, "kind time", all.Kinds[0].TimeSeconds, 14400)
	if all.All.Entries != 1 || all.All.Completed != 1 || all.All.Rated != 1 || all.All.RatingSum != 9 || all.All.Statuses[plugin.Completed] != 1 {
		t.Fatalf("all-time counts = %+v, want one entry, one rating", all.All)
	}
	if all.All.RatingDistribution[9] != 1 {
		t.Fatalf("rating distribution = %v, want a single 9", all.All.RatingDistribution)
	}
}

func TestPastRunDimensionTimeCountsEntryOnce(t *testing.T) {
	row := withGenres(rewatched(), "Drama")
	rep := stats.Build(movieInput(row), stats.Options{})
	d := rep.Genres[0]
	if d.Value != "Drama" || d.Entries != 1 || d.Rated != 1 {
		t.Fatalf("genre = %+v, want one entry", d)
	}
	approx(t, "genre time", d.TimeSeconds, 14400)
}

func TestUndatedPastRunOnlyInAllTime(t *testing.T) {
	r := movieRuntime(mkRow(1, library.Completed, day(2026, 5, 1)), 100)
	r = withRuns(r, library.Run{Number: 1, Status: library.Completed})
	all := stats.Build(movieInput(r), stats.Options{})
	approx(t, "all-time", all.All.TimeSeconds, 12000)
	y := stats.Build(movieInput(r), stats.Options{From: day(2026, 1, 1), To: day(2026, 12, 31)})
	approx(t, "2026", y.All.TimeSeconds, 6000)
	if all.All.UndatedCompleted != 0 {
		t.Fatalf("UndatedCompleted = %d: a past run is no entry", all.All.UndatedCompleted)
	}
}

func TestPastRunProgressCountsWhateverItsStatus(t *testing.T) {
	r := withKeys(movieRuntime(mkRow(1, library.Completed, day(2026, 5, 1)), 100), "position")
	r = withRuns(r, library.Run{Number: 1, Status: library.Dropped, FinishedOn: day(2025, 2, 2),
		Progress: map[string]float64{"position": 600}})
	rep := stats.Build(movieInput(r), stats.Options{From: day(2025, 1, 1), To: day(2025, 12, 31)})
	approx(t, "dropped run time", rep.All.TimeSeconds, 600)
	if rep.All.Statuses[plugin.Dropped] != 0 {
		t.Fatalf("a past run is no status count: %v", rep.All.Statuses)
	}
}

func TestPastRunUnits(t *testing.T) {
	r := withKeys(withFields(mkRow(1, library.Completed, day(2026, 5, 1)),
		map[string]plugin.FieldValue{"runtime": {Num: num(24)}}), "episode")
	r = withProgress(r, map[string]float64{"episode": 12})
	r = withRuns(r, library.Run{Number: 1, Status: library.Completed, FinishedOn: day(2025, 5, 1),
		Progress: map[string]float64{"episode": 10}})
	rep := stats.Build([]stats.Input{{PluginID: "p", Kind: showKind(), Rows: []library.Row{r}}}, stats.Options{})
	approx(t, "units", rep.Kinds[0].Units, 22)
	approx(t, "time", rep.Kinds[0].TimeSeconds, 22*24*60)
	if rep.Kinds[0].Entries != 1 {
		t.Fatalf("entries = %d, want 1", rep.Kinds[0].Entries)
	}
}

func TestStatusFilterNarrowsPastRunsByTheirOwnStatus(t *testing.T) {
	completed := library.Completed
	// the display run is an active repeat: its time and entry stay out of a
	// "completed" report, the completed past run counts
	r := withKeys(movieRuntime(mkRow(1, library.Active, nil), 120), "position")
	r = withProgress(r, map[string]float64{"position": 1800})
	r = withRuns(r,
		library.Run{Number: 2, Status: library.Dropped, FinishedOn: day(2025, 8, 1), Progress: map[string]float64{"position": 300}},
		library.Run{Number: 1, Status: library.Completed, FinishedOn: day(2025, 4, 1)})
	rep := stats.Build(movieInput(r), stats.Options{Status: &completed})
	approx(t, "completed time", rep.All.TimeSeconds, 7200)
	if rep.All.Entries != 0 {
		t.Fatalf("entries = %d, want 0: the display run is active", rep.All.Entries)
	}
	// without a filter every run counts, whatever its status
	all := stats.Build(movieInput(r), stats.Options{})
	approx(t, "unfiltered time", all.All.TimeSeconds, 1800+300+7200)
	if all.All.Entries != 1 {
		t.Fatalf("entries = %d, want 1", all.All.Entries)
	}
	// a completed display run is narrowed the same way as a past run
	r2 := movieRuntime(mkRow(2, library.Completed, day(2026, 1, 1)), 60)
	r2 = withRuns(r2, library.Run{Number: 1, Status: library.Dropped, FinishedOn: day(2025, 1, 1), Progress: map[string]float64{"position": 99}})
	rep2 := stats.Build(movieInput(r2), stats.Options{Status: &completed})
	approx(t, "dropped past run excluded", rep2.All.TimeSeconds, 3600)
	if rep2.All.Entries != 1 {
		t.Fatalf("entries = %d, want 1", rep2.All.Entries)
	}
}

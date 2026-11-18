package stats_test

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/stats"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden statistics report")

// goldenInputs is a seeded library without repeats: several kinds, every
// status, rated, unrated and undated entries, terms and fields.
func goldenInputs() []stats.Input {
	rt := func(v float64) map[string]plugin.FieldValue {
		return map[string]plugin.FieldValue{"runtime": {Num: num(v)}}
	}
	movies := []library.Row{
		withTags(withGenres(withRating(withFields(mkRow(1, library.Completed, day(2024, 1, 1)), rt(120)), 8), "Fantasy", "Sci-Fi"), "A"),
		withGenres(withRating(withFields(mkRow(2, library.Completed, day(2025, 3, 9)), rt(90)), 0), "Drama"),
		withGenres(withFields(mkRow(3, library.Completed, nil), rt(100)), "Drama"),
		withProgress(withKeys(withFields(mkRow(4, library.Active, nil), rt(110)), "position"), map[string]float64{"position": 1800}),
		withFields(mkRow(5, library.Planned, nil), rt(80)),
		withRating(withFields(mkRow(6, library.Dropped, day(2025, 6, 30)), rt(95)), 3),
	}
	shows := []library.Row{
		withTags(withGenres(withRating(withProgress(withKeys(withFields(mkRow(10, library.Completed, day(2025, 2, 1)),
			map[string]plugin.FieldValue{"runtime": {Num: num(24)}, "episodes": {Num: num(12)}}), "episode"), map[string]float64{"episode": 12}), 9), "Fantasy"), "a"),
		withProgress(withKeys(withFields(mkRow(11, library.Active, nil),
			map[string]plugin.FieldValue{"runtime": {Num: num(45)}, "episodes": {Num: num(10)}}), "episode"), map[string]float64{"episode": 3}),
		withProgress(withKeys(withFields(mkRow(12, library.Paused, day(2026, 1, 15)),
			map[string]plugin.FieldValue{"runtime": {Num: num(30)}}), "episode"), map[string]float64{"episode": 5}),
	}
	return []stats.Input{
		{PluginID: "p1", Kind: movieKind(), Rows: movies},
		{PluginID: "p2", Kind: showKind(), Rows: shows},
	}
}

func goldenReport(opts stats.Options) string {
	return fmt.Sprintf("%+v\n", stats.Build(goldenInputs(), opts))
}

// TestGoldenReportWithoutRepeats pins the numbers of a library without
// repeats: runs must not change a single figure.
func TestGoldenReportWithoutRepeats(t *testing.T) {
	cases := map[string]stats.Options{
		"all":    {},
		"year":   {From: day(2025, 1, 1), To: day(2025, 12, 31)},
		"months": {From: day(2024, 1, 1), To: day(2026, 3, 1), Bucket: stats.BucketMonth},
	}
	var got strings.Builder
	for _, name := range []string{"all", "year", "months"} {
		fmt.Fprintf(&got, "== %s\n%s", name, goldenReport(cases[name]))
	}
	const path = "testdata/golden_report.txt"
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != string(want) {
		t.Fatalf("report changed for a library without repeats\n got: %s\nwant: %s", got.String(), want)
	}
}

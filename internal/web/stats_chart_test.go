package web

import (
	"testing"
	"time"

	"github.com/allister/allister/internal/stats"
)

// heatDay returns the cell of a heatmap view for a date.
func heatDay(t *testing.T, v heatmapView, date string) heatmapDay {
	t.Helper()
	for _, week := range v.Weeks {
		for _, d := range week {
			if d.Date == date {
				return d
			}
		}
	}
	t.Fatalf("no heatmap cell for %q", date)
	return heatmapDay{}
}

// heatMonth returns the month label of a heatmap view.
func heatMonth(t *testing.T, v heatmapView, label string) heatmapMonth {
	t.Helper()
	for _, m := range v.Months {
		if m.Label == label {
			return m
		}
	}
	t.Fatalf("no heatmap month %q", label)
	return heatmapMonth{}
}

func TestTimeChartBars(t *testing.T) {
	v := buildTimeChart([]stats.Bucket{
		{Label: "2025-11"},
		{Label: "2025-12", Seconds: 3600},
		{Label: "2026-01", Seconds: 1800},
	})
	if v == nil {
		t.Fatal("no chart for three buckets")
	}
	if len(v.Bars) != 3 {
		t.Fatalf("bars = %d, want 3", len(v.Bars))
	}
	if v.Total != "1h 30m" {
		t.Errorf("total = %q, want 1h 30m", v.Total)
	}
	if v.MaxLabel != "2025-12 (1h)" {
		t.Errorf("max label = %q, want 2025-12 (1h)", v.MaxLabel)
	}
	if v.Bars[0].Height != 0 {
		t.Errorf("empty bucket height = %d, want 0", v.Bars[0].Height)
	}
	// the largest bucket fills the plot; a half-size bucket is half as tall
	if v.Bars[1].Height <= v.Bars[2].Height {
		t.Errorf("heights not ordered: %d, %d", v.Bars[1].Height, v.Bars[2].Height)
	}
	if v.Bars[2].Height != v.Bars[1].Height/2 {
		t.Errorf("half bucket height = %d, want %d", v.Bars[2].Height, v.Bars[1].Height/2)
	}
	// every bar sits on the same baseline
	if base := v.Bars[1].Y + v.Bars[1].Height; v.Bars[2].Y+v.Bars[2].Height != base || v.Bars[0].Y != base {
		t.Errorf("bars do not share a baseline: %+v", v.Bars)
	}
	if v.Bars[0].X >= v.Bars[1].X || v.Bars[1].X >= v.Bars[2].X {
		t.Errorf("bars overlap horizontally: %+v", v.Bars)
	}
	if v.Bars[1].Title != "2025-12: 1h" {
		t.Errorf("bar title = %q", v.Bars[1].Title)
	}
	if v.Width <= v.Bars[2].X+v.Bars[2].Width || v.Height <= v.Bars[1].Y {
		t.Errorf("chart %dx%d does not enclose its bars", v.Width, v.Height)
	}
	want := []bucketRow{{Label: "2025-11", Value: "0m"}, {Label: "2025-12", Value: "1h"}, {Label: "2026-01", Value: "30m"}}
	if len(v.Table) != len(want) {
		t.Fatalf("table rows = %d, want %d", len(v.Table), len(want))
	}
	for i, row := range want {
		if v.Table[i] != row {
			t.Errorf("table row %d = %+v, want %+v", i, v.Table[i], row)
		}
	}

	// no buckets and all-zero buckets have no chart
	if buildTimeChart(nil) != nil {
		t.Error("a chart without buckets")
	}
	if buildTimeChart([]stats.Bucket{{Label: "2025"}, {Label: "2026", Seconds: 0}}) != nil {
		t.Error("a chart for only empty buckets")
	}
}

func TestRatingRows(t *testing.T) {
	var dist [11]int
	dist[0] = 1
	dist[8] = 2
	rows := buildRatingRows(dist, 1)
	if len(rows) != 12 {
		t.Fatalf("rows = %d, want 12 (0-10 plus unrated)", len(rows))
	}
	for i := 0; i < 11; i++ {
		if rows[i].Score != i || rows[i].Unrated {
			t.Errorf("row %d = %+v, want score %d and rated", i, rows[i], i)
		}
	}
	if rows[0].Count != 1 || rows[0].BarWidth != 50 {
		t.Errorf("score 0 = %+v, want count 1 and bar 50", rows[0])
	}
	if rows[8].Count != 2 || rows[8].BarWidth != 100 {
		t.Errorf("score 8 = %+v, want count 2 and bar 100", rows[8])
	}
	if rows[9].Count != 0 || rows[9].BarWidth != 0 {
		t.Errorf("score 9 = %+v, want empty", rows[9])
	}
	unrated := rows[11]
	if !unrated.Unrated || unrated.Count != 1 || unrated.BarWidth != 50 {
		t.Errorf("unrated = %+v, want count 1 and bar 50", unrated)
	}

	// without any rating the rows still exist and no bar has a width
	empty := buildRatingRows([11]int{}, 0)
	if len(empty) != 12 {
		t.Fatalf("empty rows = %d, want 12", len(empty))
	}
	for _, r := range empty {
		if r.BarWidth != 0 {
			t.Errorf("empty distribution bar = %+v", r)
		}
	}
}

func TestHeatmapGrid(t *testing.T) {
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) // a Wednesday
	to := time.Date(2025, 3, 31, 0, 0, 0, 0, time.UTC)
	v := buildHeatmap(map[string]int{"2025-01-06": 3, "2025-01-07": 1}, from, to, "2025-01-02")

	if len(v.Weeks) != 53 {
		t.Fatalf("weeks = %d, want 53", len(v.Weeks))
	}
	for i, week := range v.Weeks {
		if len(week) != 7 {
			t.Fatalf("week %d has %d days, want 7", i, len(week))
		}
	}
	// weeks are Monday-first: the grid starts on the Monday before the window
	if v.Weeks[0][0].Date != "" {
		t.Errorf("first cell = %+v, want the empty Monday before the window", v.Weeks[0][0])
	}
	if v.Weeks[0][1].Date != "" || v.Weeks[0][2].Date != "2025-01-01" {
		t.Errorf("first week = %+v, want an empty Monday/Tuesday and Wednesday 2025-01-01", v.Weeks[0])
	}
	if v.Weeks[1][0].Date != "2025-01-06" {
		t.Errorf("second week starts on %q, want the Monday 2025-01-06", v.Weeks[1][0].Date)
	}

	busy := heatDay(t, v, "2025-01-06")
	if busy.Count != 3 || busy.Level != 3 || busy.X <= 0 || busy.Y < 0 {
		t.Errorf("busy cell = %+v, want count 3 at level 3 with geometry", busy)
	}
	if busy.Title != "2025-01-06: 3 activity events" {
		t.Errorf("busy title = %q", busy.Title)
	}
	one := heatDay(t, v, "2025-01-07")
	if one.Count != 1 || one.Level != 1 || one.Title != "2025-01-07: 1 activity event" {
		t.Errorf("single cell = %+v", one)
	}
	quiet := heatDay(t, v, "2025-01-02")
	if quiet.Count != 0 || quiet.Level != 0 || quiet.Title != "2025-01-02: no activity" {
		t.Errorf("quiet cell = %+v", quiet)
	}
	// cells of one week differ in y only, and cells of one day differ in x only
	if busy.X != one.X || busy.Y == one.Y {
		t.Errorf("same-week cells = %+v, %+v", busy, one)
	}
	if busy.Y != heatDay(t, v, "2025-01-13").Y {
		t.Error("the next Monday is not in the same row")
	}

	jan := heatMonth(t, v, "Jan")
	if jan.X != heatDay(t, v, "2025-01-01").X {
		t.Errorf("January label x = %d, want the x of 2025-01-01", jan.X)
	}
	if feb := heatMonth(t, v, "Feb"); feb.X <= jan.X {
		t.Errorf("month labels are not in order: %+v", v.Months)
	}
	if v.Since != "2 January 2025" {
		t.Errorf("since = %q, want 2 January 2025", v.Since)
	}
	if v.Empty {
		t.Error("a heatmap with activity is empty")
	}
}

func TestHeatmapEmpty(t *testing.T) {
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)
	v := buildHeatmap(nil, from, to, "")
	if len(v.Weeks) != 53 {
		t.Fatalf("weeks = %d, want 53", len(v.Weeks))
	}
	if !v.Empty {
		t.Error("a heatmap without counts is not empty")
	}
	if v.Since != "" {
		t.Errorf("since = %q, want empty", v.Since)
	}
	for _, week := range v.Weeks {
		for _, d := range week {
			if d.Count != 0 || d.Level != 0 {
				t.Fatalf("empty heatmap cell = %+v", d)
			}
		}
	}
}

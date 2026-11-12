package web

// The figures of the statistics page: the SVG time chart, the rating
// distribution and the 12-month activity heatmap. The builders are pure and
// supply every coordinate, because html/template cannot do arithmetic and the
// SVG geometry must be attributes (no inline styles). Colours stay classes of
// the theme: a mark is fill="currentColor" under a text-* class.

import (
	"math"
	"strconv"
	"time"

	"github.com/allister/allister/internal/stats"
)

// Chart geometry in SVG user units: a fixed slot per bucket, the bar centred
// in its slot, and the baseline shared by every bar. The viewBox scales to the
// available width when the page renders it; a chart with few buckets keeps at
// least chartMinSlots so its bars do not fill the page. Labels sit in the band
// below the baseline, rotated so neighbouring ones do not collide, and use the
// same slot geometry as the bars.
const (
	chartHeight   = 196
	chartBaseline = 150
	chartTopPad   = 8
	chartSlot     = 34
	chartBarWidth = 22
	chartLeftPad  = 4
	chartMinSlots = 8
	// chartLabelY is the anchor line of the bucket labels and
	// chartMaxLabels the bucket count above which every second label is
	// dropped (the bars keep their tooltips and the value table).
	chartLabelY    = 164
	chartMaxLabels = 24
)

// timeChartView is the time chart of the selected scope. MaxLabel names the
// busiest bucket with its value, Total is the sum of every bucket, Table
// carries the same values for the details table below the chart and Labels
// carries one x-axis label per drawn bar.
type timeChartView struct {
	Bars     []chartBar
	MaxLabel string
	Total    string
	Table    []bucketRow
	Labels   []chartLabel
	Width    int
	Height   int
}

// chartBar is one bucket of the time chart with its rectangle.
type chartBar struct {
	Label         string
	Seconds       float64
	X, Y          int
	Width, Height int
	Title         string
}

// chartLabel is one bucket label on the x-axis, at the centre of its bar's
// slot and on the rotated label line.
type chartLabel struct {
	X, Y int
	Text string
}

// bucketRow is one row of the chart's value table.
type bucketRow struct {
	Label, Value string
}

// buildTimeChart turns the buckets of a report into the chart. It returns nil
// when there is no bucket or no bucket carries time, because the page shows no
// empty chart.
func buildTimeChart(buckets []stats.Bucket) *timeChartView {
	var total, max float64
	maxAt := 0
	for i, b := range buckets {
		total += b.Seconds
		if b.Seconds > max {
			max, maxAt = b.Seconds, i
		}
	}
	if len(buckets) == 0 || !(total > 0) {
		return nil
	}
	slots := len(buckets)
	if slots < chartMinSlots {
		slots = chartMinSlots
	}
	offset := chartLeftPad + (slots-len(buckets))*chartSlot/2
	v := &timeChartView{
		MaxLabel: buckets[maxAt].Label + " (" + fmtSpan(max) + ")",
		Total:    fmtSpan(total),
		Width:    chartLeftPad*2 + slots*chartSlot,
		Height:   chartHeight,
	}
	plot := float64(chartBaseline - chartTopPad)
	// above chartMaxLabels every second label is enough to name the axis
	labelStep := 1
	if len(buckets) > chartMaxLabels {
		labelStep = 2
	}
	for i, b := range buckets {
		height := 0
		if b.Seconds > 0 {
			height = int(math.Round(b.Seconds / max * plot))
			if height == 0 {
				height = 1 // a bucket with time keeps a visible bar
			}
		}
		bar := chartBar{
			Label: b.Label, Seconds: b.Seconds,
			X:     offset + i*chartSlot + (chartSlot-chartBarWidth)/2,
			Y:     chartBaseline - height,
			Width: chartBarWidth, Height: height,
			Title: b.Label + ": " + fmtSpan(b.Seconds),
		}
		v.Bars = append(v.Bars, bar)
		if i%labelStep == 0 {
			v.Labels = append(v.Labels, chartLabel{X: bar.X + bar.Width/2, Y: chartLabelY, Text: b.Label})
		}
		v.Table = append(v.Table, bucketRow{Label: b.Label, Value: fmtSpan(b.Seconds)})
	}
	return v
}

// ratingRow is one row of the rating distribution: scores 0-10 and the
// unrated entries. BarWidth is the percentage of the largest row.
type ratingRow struct {
	Score, Count int
	BarWidth     int
	Unrated      bool
}

// buildRatingRows lists every rating value with its count, then the unrated
// entries. The widest row (never zero) fills the bar.
func buildRatingRows(dist [11]int, unrated int) []ratingRow {
	max := unrated
	for _, n := range dist {
		if n > max {
			max = n
		}
	}
	width := func(n int) int {
		if max <= 0 {
			return 0
		}
		return int(math.Round(float64(n) * 100 / float64(max)))
	}
	rows := make([]ratingRow, 0, 12)
	for score := 0; score <= 10; score++ {
		rows = append(rows, ratingRow{Score: score, Count: dist[score], BarWidth: width(dist[score])})
	}
	return append(rows, ratingRow{Count: unrated, BarWidth: width(unrated), Unrated: true})
}

// Heatmap geometry in SVG user units: 53 Monday-first week columns of seven
// days, a gutter for the weekday labels and a band for the month labels.
const (
	heatCell  = 12
	heatGap   = 3
	heatStep  = heatCell + heatGap
	heatCols  = 53
	heatRows  = 7
	heatTop   = 18
	heatLeft  = 30
	heatRight = 24 // room for the label of the last month

	heatWidth  = heatLeft + heatCols*heatStep - heatGap + heatRight
	heatHeight = heatTop + heatRows*heatStep - heatGap

	// heatLevels is the number of intensity steps above an empty cell.
	heatLevels = 4
)

// heatmapView is the activity heatmap of the last 12 months: 53 week columns of
// seven Monday-first days. Since is the display form of the earliest recorded
// activity, Empty says that no day in the window carries activity.
type heatmapView struct {
	Weeks    [][]heatmapDay
	Months   []heatmapMonth
	Weekdays []heatmapWeekday
	Since    string
	Empty    bool
	Width    int
	Height   int
}

// heatmapDay is one cell of the heatmap. The cells before from and after to
// keep an empty Date and are not drawn.
type heatmapDay struct {
	Date  string
	Count int
	Level int
	Title string
	X, Y  int
}

// heatmapMonth is one month label above its first week.
type heatmapMonth struct {
	Label string
	X     int
}

// heatmapWeekday is one weekday label left of its row.
type heatmapWeekday struct {
	Label string
	Y     int
}

// buildHeatmap lays the counts out on a 53x7 Monday-first grid covering
// from..to (inclusive calendar days); from..to must span at most 53 weeks.
// Days outside that window stay empty. since is the earliest activity day
// (YYYY-MM-DD, "" for none).
func buildHeatmap(counts map[string]int, from, to time.Time, since string) heatmapView {
	from, to = dayOf(from), dayOf(to)
	// Monday on or before from: week columns start on a Monday
	start := from.AddDate(0, 0, -((int(from.Weekday()) + 6) % 7))
	v := heatmapView{Width: heatWidth, Height: heatHeight, Since: activitySince(since), Empty: true}
	v.Weeks = make([][]heatmapDay, heatCols)
	for col := 0; col < heatCols; col++ {
		week := make([]heatmapDay, heatRows)
		for row := 0; row < heatRows; row++ {
			d := heatmapDay{X: heatLeft + col*heatStep, Y: heatTop + row*heatStep}
			day := start.AddDate(0, 0, col*heatRows+row)
			if !day.Before(from) && !day.After(to) {
				d.Date = day.Format(time.DateOnly)
				d.Count = counts[d.Date]
				d.Level = activityLevel(d.Count)
				d.Title = activityTitle(d.Date, d.Count)
				if d.Count > 0 {
					v.Empty = false
				}
			}
			week[row] = d
		}
		v.Weeks[col] = week
	}
	v.Months = heatmapMonths(start, from, to)
	// Monday, Wednesday and Friday name the rows, like a calendar's axis
	for _, row := range []int{0, 2, 4} {
		v.Weekdays = append(v.Weekdays, heatmapWeekday{
			Label: [7]string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}[row],
			Y:     heatTop + row*heatStep + 10,
		})
	}
	return v
}

// heatmapMonths places one label per month in the window at the column of the
// month's first day. The leading partial month is not labelled: its label would
// sit at the first column and push the first full month (only two or three
// columns away) out under the four-column rule. A later label closer than four
// columns to the previous one is dropped the same way.
func heatmapMonths(start, from, to time.Time) []heatmapMonth {
	var out []heatmapMonth
	last := -4
	month := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC)
	if month.Before(from) {
		month = month.AddDate(0, 1, 0)
	}
	for ; !month.After(to); month = month.AddDate(0, 1, 0) {
		col := int(month.Sub(start).Hours()/24) / heatRows
		if col < last+4 {
			continue
		}
		out = append(out, heatmapMonth{Label: month.Format("Jan"), X: heatLeft + col*heatStep})
		last = col
	}
	return out
}

// activityLevel maps a day's event count to one of five intensities: empty and
// four steps above it.
func activityLevel(count int) int {
	switch {
	case count <= 0:
		return 0
	case count >= heatLevels:
		return heatLevels
	}
	return count
}

// activityTitle is the native tooltip of one cell.
func activityTitle(date string, count int) string {
	switch count {
	case 0:
		return date + ": no activity"
	case 1:
		return date + ": 1 activity event"
	}
	return date + ": " + strconv.Itoa(count) + " activity events"
}

// activitySince formats the earliest activity day for the page.
func activitySince(since string) string {
	if since == "" {
		return ""
	}
	d, err := time.Parse(time.DateOnly, since)
	if err != nil {
		return ""
	}
	return d.Format("2 January 2006")
}

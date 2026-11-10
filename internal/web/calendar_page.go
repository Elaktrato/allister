package web

// The calendar page, GET /calendar?month=YYYY-MM: a month grid of the stored
// releases of watching entries (the same data as the feed), one chip per
// release on its air date.

import (
	"context"
	"net/http"
	"time"

	"github.com/allister/allister/internal/library"
)

// calendarView is the data of calendar.html.
type calendarView struct {
	MonthLabel                 string
	Weekdays                   []string
	Weeks                      [][]calendarDay
	PrevURL, NextURL, TodayURL string
}

// calendarDay is one cell of the grid.
type calendarDay struct {
	Day      int
	InMonth  bool
	Today    bool
	Releases []calendarRelease
}

// calendarRelease is one airing chip.
type calendarRelease struct {
	Title, Label, Href string
	Number             int
	At                 time.Time
}

// calendarWeekdays are the column labels (Monday first).
var calendarWeekdays = []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}

// parseCalendarMonth reads a ?month=YYYY-MM value; anything else means the
// current month.
func parseCalendarMonth(raw string, now time.Time) time.Time {
	if t, err := time.Parse("2006-01", raw); err == nil {
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, now.Location())
	}
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
}

// getCalendarPage renders the month grid; htmx month links get the grid
// alone.
func (s *server) getCalendarPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := s.now()
	month := parseCalendarMonth(r.URL.Query().Get("month"), now)
	rows, err := s.deps.Library.ScheduleEntries(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	v := calendarView{
		MonthLabel: month.Format("January 2006"),
		Weekdays:   calendarWeekdays,
		PrevURL:    "/calendar?month=" + month.AddDate(0, -1, 0).Format("2006-01"),
		NextURL:    "/calendar?month=" + month.AddDate(0, 1, 0).Format("2006-01"),
		TodayURL:   "/calendar",
	}
	s.fillCalendar(ctx, &v, month, now, rows)
	// viewing the grid also refreshes stale schedules, like the feed does
	s.enqueueStaleSchedules(ctx, now)
	// the answer depends on the partial headers, like the list
	w.Header().Set("Vary", "HX-Request, HX-Target")
	if isPartialRequest(r) && r.Header.Get("HX-Target") == "calendar-grid" {
		s.renderPartial(w, "calendar", "calendar_grid", v)
		return
	}
	s.render(w, r, "calendar", v)
}

// fillCalendar lays the month out Monday first, padded with the neighbouring
// days, and puts every release of the month on its day.
func (s *server) fillCalendar(ctx context.Context, v *calendarView, month, now time.Time, rows []library.ScheduledEntry) {
	next := month.AddDate(0, 1, 0)
	lead := (int(month.Weekday()) + 6) % 7 // Monday first
	start := month.AddDate(0, 0, -lead)
	days := int(next.Sub(month).Hours() / 24)
	cells := lead + days
	weeks := (cells + 6) / 7
	// releases keyed by the month's local day
	byDay := map[int][]calendarRelease{}
	for _, row := range rows {
		at := row.AirsAt.In(month.Location())
		if at.Before(month) || !at.Before(next) {
			continue
		}
		m, ok := s.deps.Registry.Manifest(row.PluginID)
		if !ok || !sourceSchedules(m, row.Source) {
			continue
		}
		k, ok := kindDef(m, row.Kind)
		if !ok {
			continue
		}
		title := s.resolveTitle(ctx, m, k, row.Kind, row.Titles)
		if title == "" {
			title = "(untitled)"
		}
		byDay[at.Day()] = append(byDay[at.Day()], calendarRelease{
			Title: title, Label: progressLabel(k), Number: row.Number,
			Href: itemURL(m, row.Source, row.SourceID, row.Kind), At: at,
		})
	}
	for w := 0; w < weeks; w++ {
		week := make([]calendarDay, 0, 7)
		for i := 0; i < 7; i++ {
			d := start.AddDate(0, 0, w*7+i)
			day := calendarDay{Day: d.Day(), InMonth: d.Month() == month.Month(),
				Today: d.Year() == now.Year() && d.YearDay() == now.YearDay()}
			if day.InMonth {
				day.Releases = byDay[d.Day()]
			}
			week = append(week, day)
		}
		v.Weeks = append(v.Weeks, week)
	}
}

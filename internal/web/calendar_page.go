package web

// The calendar page, GET /calendar?month=YYYY-MM: a month grid of the stored
// releases of watching entries (the same data as the feed), one chip per
// release on its air date.

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/allister/allister/internal/library"
)

// calendarView is the data of calendar.html. View is "month" or "week"; the
// month view fills Weeks, the week view Week.
type calendarView struct {
	View                       string
	Label                      string
	Weekdays                   []string
	Weeks                      [][]calendarDay
	Week                       []calendarDay
	Plugin                     string
	Plugins                    []calendarPluginOption
	PrevURL, NextURL, TodayURL string
	MonthURL, WeekURL          string
	// Position is the period as hidden form values of the plugin select
	PositionName, PositionValue string
	// Return is this view's own URL, where Mark watched comes back to
	Return string
	// Notice is a message above the grid (a refused Mark watched)
	Notice string
}

// setReturn stamps the view's own URL on every release (the button's form).
func (v *calendarView) setReturn() {
	days := v.Week
	for _, w := range v.Weeks {
		days = append(days, w...)
	}
	for _, d := range days {
		for i := range d.Releases {
			d.Releases[i].Return = v.Return
		}
	}
}

// calendarPluginOption is one entry of the plugin select.
type calendarPluginOption struct {
	ID, Name string
	Selected bool
}

// calendarDay is one cell of the grid.
type calendarDay struct {
	Day      int
	Label    string // "Nov 9", shown in the week view
	InMonth  bool
	Today    bool
	Releases []calendarRelease
}

// calendarRelease is one airing chip.
type calendarRelease struct {
	Title, Label, Href string
	Season, Number     int
	At                 time.Time
	ItemID             int64
	// Markable: the release has aired, the entry is active on its first run and
	// library.ReleaseMarkable agrees (beyond progress, every track it sets is
	// active, and the clamp to the total still moves something); only then the
	// Mark watched button is offered.
	Markable bool
	Return   string
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

// parseCalendarWeek reads a ?week=YYYY-MM-DD value, any day of the week, and
// returns that week's Monday at local midnight; anything else means the
// current week.
func parseCalendarWeek(raw string, now time.Time) time.Time {
	y, m, d := now.Date()
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		y, m, d = t.Date()
	}
	// civil-date arithmetic: time.Date renormalises and keeps local midnight
	// across a DST change (a Sub of 24h steps would not)
	wd := int(time.Date(y, m, d, 12, 0, 0, 0, now.Location()).Weekday()+6) % 7
	return time.Date(y, m, d-wd, 0, 0, 0, 0, now.Location())
}

// calendarQuery builds /calendar from name/value pairs in the order given;
// pairs with an empty value are left out.
func calendarQuery(kv ...string) string {
	var parts []string
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			parts = append(parts, url.QueryEscape(kv[i])+"="+url.QueryEscape(kv[i+1]))
		}
	}
	if len(parts) == 0 {
		return "/calendar"
	}
	return "/calendar?" + strings.Join(parts, "&")
}

// calendarPlugins lists the enabled plugins with a schedule-capable source
// and marks the selected one; an id that is not listed selects nothing.
func (s *server) calendarPlugins(selected string) ([]calendarPluginOption, string) {
	var out []calendarPluginOption
	picked := ""
	for _, m := range s.enabledManifests() {
		capable := false
		for _, si := range m.Sources {
			capable = capable || si.SupportsSchedule
		}
		if !capable {
			continue
		}
		o := calendarPluginOption{ID: m.ID, Name: m.Name, Selected: m.ID == selected}
		if o.Selected {
			picked = m.ID
		}
		out = append(out, o)
	}
	return out, picked
}

// buildCalendar assembles the month or week view the query asks for.
func (s *server) buildCalendar(ctx context.Context, q url.Values) (calendarView, error) {
	now := s.now().In(s.location(ctx))
	rows, err := s.deps.Library.ScheduleEntries(ctx)
	if err != nil {
		return calendarView{}, err
	}
	v := calendarView{View: "month", Weekdays: calendarWeekdays}
	v.Plugins, v.Plugin = s.calendarPlugins(q.Get("plugin"))
	if q.Get("view") == "week" {
		v.View = "week"
		week := parseCalendarWeek(q.Get("week"), now)
		s.fillCalendarWeek(ctx, &v, week, now, rows)
		v.Return = v.WeekURL
	} else {
		month := parseCalendarMonth(q.Get("month"), now)
		s.fillCalendar(ctx, &v, month, now, rows)
		v.Return = v.MonthURL
	}
	v.setReturn()
	return v, nil
}

// getCalendarPage renders the month or week grid; htmx links get the grid
// alone.
func (s *server) getCalendarPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := s.now().In(s.location(ctx))
	v, err := s.buildCalendar(ctx, r.URL.Query())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	// viewing the grid also refreshes stale schedules, like the feed does
	s.enqueueStaleSchedules(ctx, now)
	// the answer depends on the partial headers, like the list
	w.Header().Set("Vary", "HX-Request, HX-Target, HX-History-Restore-Request")
	if isPartialRequest(r) && r.Header.Get("HX-Target") == "calendar-grid" {
		// the select submits plugin= for "All plugins" (and an unknown id is
		// ignored): push the canonical URL instead of the request's
		if q := r.URL.Query(); q.Has("plugin") && v.Plugin == "" {
			w.Header().Set("HX-Push-Url", v.Return)
		}
		s.renderPartial(w, "calendar", "calendar_grid", v)
		return
	}
	s.render(w, r, "calendar", v)
}

// collectReleases returns the releases airing in [from, to) (local time of
// from), keyed by local calendar day "2006-01-02". It keeps only releases of
// schedule-capable sources and, when plugin is set, of that plugin.
func (s *server) collectReleases(ctx context.Context, rows []library.ScheduledEntry, from, to, now time.Time, plugin string) map[string][]calendarRelease {
	byDay := map[string][]calendarRelease{}
	for _, row := range rows {
		at := row.AirsAt.In(from.Location())
		if at.Before(from) || !at.Before(to) || (plugin != "" && row.PluginID != plugin) {
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
		key := at.Format("2006-01-02")
		byDay[key] = append(byDay[key], calendarRelease{
			Title: title, Label: progressLabel(k), Season: row.Season, Number: row.Number,
			Href: itemURL(m, row.Source, row.SourceID, row.Kind), At: at, ItemID: row.ItemID,
			Markable: s.deps.Registry.IsEnabled(row.PluginID) && !row.AirsAt.After(now) && row.RunNumber == 1 &&
				library.ReleaseMarkable(k, row.ProgressKeys, row.Totals, row.Progress, row.Season, row.Number),
		})
	}
	return byDay
}

// isToday reports whether d is the local calendar day of now.
func isToday(d, now time.Time) bool {
	return d.Year() == now.Year() && d.YearDay() == now.YearDay()
}

// fillCalendarWeek lays out the seven days from Monday week.
func (s *server) fillCalendarWeek(ctx context.Context, v *calendarView, week, now time.Time, rows []library.ScheduledEntry) {
	end := week.AddDate(0, 0, 7)
	byDay := s.collectReleases(ctx, rows, week, end, now, v.Plugin)
	for i := 0; i < 7; i++ {
		d := week.AddDate(0, 0, i)
		v.Week = append(v.Week, calendarDay{Day: d.Day(), Label: d.Format("Jan 2"), InMonth: true,
			Today: isToday(d, now), Releases: byDay[d.Format("2006-01-02")]})
	}
	last := end.AddDate(0, 0, -1)
	switch {
	case week.Year() != last.Year():
		v.Label = week.Format("2 Jan 2006") + " – " + last.Format("2 Jan 2006")
	case week.Month() != last.Month():
		v.Label = week.Format("2 Jan") + " – " + last.Format("2 Jan 2006")
	default:
		v.Label = week.Format("2") + " – " + last.Format("2 Jan 2006")
	}
	key := func(t time.Time) string { return t.Format("2006-01-02") }
	v.PrevURL = calendarQuery("view", "week", "week", key(week.AddDate(0, 0, -7)), "plugin", v.Plugin)
	v.NextURL = calendarQuery("view", "week", "week", key(end), "plugin", v.Plugin)
	v.TodayURL = calendarQuery("view", "week", "plugin", v.Plugin)
	v.WeekURL = calendarQuery("view", "week", "week", key(week), "plugin", v.Plugin)
	v.MonthURL = calendarQuery("month", week.Format("2006-01"), "plugin", v.Plugin)
	v.PositionName, v.PositionValue = "week", key(week)
}

// fillCalendar lays the month out Monday first, padded with the neighbouring
// days, and puts every release of the month on its day.
func (s *server) fillCalendar(ctx context.Context, v *calendarView, month, now time.Time, rows []library.ScheduledEntry) {
	next := month.AddDate(0, 1, 0)
	lead := (int(month.Weekday()) + 6) % 7 // Monday first
	start := month.AddDate(0, 0, -lead)
	days := time.Date(month.Year(), month.Month()+1, 0, 12, 0, 0, 0, month.Location()).Day() // not Sub: a DST month is not whole days
	cells := lead + days
	weeks := (cells + 6) / 7
	byDay := s.collectReleases(ctx, rows, month, next, now, v.Plugin)
	for w := 0; w < weeks; w++ {
		week := make([]calendarDay, 0, 7)
		for i := 0; i < 7; i++ {
			d := start.AddDate(0, 0, w*7+i)
			day := calendarDay{Day: d.Day(), InMonth: d.Month() == month.Month(), Today: isToday(d, now)}
			if day.InMonth {
				day.Releases = byDay[d.Format("2006-01-02")]
			}
			week = append(week, day)
		}
		v.Weeks = append(v.Weeks, week)
	}
	v.Label = month.Format("January 2006")
	key := func(t time.Time) string { return t.Format("2006-01") }
	v.PrevURL = calendarQuery("month", key(month.AddDate(0, -1, 0)), "plugin", v.Plugin)
	v.NextURL = calendarQuery("month", key(next), "plugin", v.Plugin)
	v.TodayURL = calendarQuery("plugin", v.Plugin)
	v.MonthURL = calendarQuery("month", key(month), "plugin", v.Plugin)
	// the week toggle opens the current week when it lies in this month, else
	// the month's first week
	anchor := month
	if now.Year() == month.Year() && now.Month() == month.Month() {
		anchor = now
	}
	v.WeekURL = calendarQuery("view", "week", "week", parseCalendarWeek(anchor.Format("2006-01-02"), anchor).Format("2006-01-02"), "plugin", v.Plugin)
	v.PositionName, v.PositionValue = "month", key(month)
}

package web

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// The fixture TV source declares SupportsSchedule; the book source does not.
func TestCalendarFeed(t *testing.T) {
	e := newFixtureEnv(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	e.now = &now
	active := e.addFixture(t, "tv", "show", tvShow("1", "Dark"), library.Entry{Status: library.Active})
	paused := e.addFixture(t, "tv", "show", tvShow("2", "Light"), library.Entry{Status: library.Paused})
	book := e.addFixture(t, "book", "book", bookItem("3", "Earthsea"), library.Entry{Status: library.Active})
	for _, id := range []int64{active, paused, book} {
		err := e.lib.SaveSchedule(context.Background(), id, []library.Airing{
			{Number: 3, AirsAt: now.Add(2 * time.Hour)},
			{Number: 1, AirsAt: now.Add(-48 * time.Hour)}, // too old for the feed
		}, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	rec := e.get("/calendar.ics")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "text/calendar; charset=utf-8" {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	b := rec.Body.String()
	mustContain(t, b, "BEGIN:VCALENDAR\r\n", "VERSION:2.0\r\n", "PRODID:-//Allister//Calendar//EN\r\n",
		"X-WR-CALNAME:Allister\r\n", "BEGIN:VEVENT\r\n", "END:VEVENT\r\n", "END:VCALENDAR\r\n")
	// one event: the active tv entry's future airing; the old one, the paused
	// entry and the schedule-less book are absent
	if n := strings.Count(b, "BEGIN:VEVENT"); n != 1 {
		t.Fatalf("%d events:\n%s", n, b)
	}
	mustContain(t, b, "UID:"+idStr(active)+"-3@allister\r\n",
		"DTSTART:20261110T140000Z\r\n", "DTEND:20261110T143000Z\r\n",
		"SUMMARY:Dark — Episode 3\r\n", "DESCRIPTION:View on Fake TV: http://example.com/tv/tvdata/1\r\n")
	mustNotContain(t, b, "Light", "Earthsea", "-1@allister")
}

// TestCalendarEscapesAndFolds: text values are escaped, and long lines fold
// at 75 octets without splitting a character.
func TestCalendarEscapesAndFolds(t *testing.T) {
	e := newFixtureEnv(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	e.now = &now
	long := "Comma, semicolon; backslash \\ and " + strings.Repeat("ü", 60)
	id := e.addFixture(t, "tv", "show", tvShow("1", long), library.Entry{Status: library.Active})
	if err := e.lib.SaveSchedule(context.Background(), id, []library.Airing{{Number: 7, AirsAt: now.Add(time.Hour)}}, now); err != nil {
		t.Fatal(err)
	}
	b := e.get("/calendar.ics").Body.String()
	mustContain(t, b, `SUMMARY:Comma\, semicolon\; backslash \\ and `)
	for _, line := range strings.Split(b, "\r\n") {
		if len(line) > 75 {
			t.Errorf("line longer than 75 octets: %q", line)
		}
	}
	// the fold continues with a leading space and the text survives whole
	escaped := strings.NewReplacer(`\`, `\\`, `;`, `\;`, `,`, `\,`).Replace(long)
	if !strings.Contains(strings.ReplaceAll(b, "\r\n ", ""), escaped) {
		t.Error("folded summary does not reassemble to the title")
	}
}

// TestCalendarEnqueuesStaleSchedules: the feed asks for at most 20 refreshes
// of schedule-capable active entries, ignores fresh and non-capable ones.
func TestCalendarEnqueuesStaleSchedules(t *testing.T) {
	e := newFixtureEnv(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	e.now = &now
	var ids []int64
	for i := 0; i < 25; i++ {
		id := e.addFixture(t, "tv", "show", tvShow(fmt.Sprint(i+1), "Show "+fmt.Sprint(i+1)), library.Entry{Status: library.Active})
		ids = append(ids, id)
	}
	// one entry with a fresh schedule is not enqueued
	fresh := ids[0]
	if err := e.lib.SaveSchedule(context.Background(), fresh, nil, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	// the book has no schedule capability: never enqueued
	book := e.addFixture(t, "book", "book", bookItem("9", "Earthsea"), library.Entry{Status: library.Active})
	_ = book
	e.get("/calendar.ics")
	if n := len(e.jobs.got); n != 20 {
		t.Fatalf("%d enqueued, want 20", n)
	}
	for _, j := range e.jobs.got {
		if j.Kind != "sync_schedule" {
			t.Fatalf("kind %q", j.Kind)
		}
	}
	// asking again enqueues nothing new for the same page? (the jobs are not
	// run in this test, so the same stale set is offered again — bounded)
	e.jobs.got = nil
	e.get("/calendar.ics")
	if n := len(e.jobs.got); n != 20 {
		t.Fatalf("second poll: %d enqueued", n)
	}
}

// TestCalendarRouteAbsentWithoutScheduleJobs: a server without ScheduleJobs
// still serves the feed (it just cannot refresh).
func TestCalendarWithoutScheduleJobs(t *testing.T) {
	h, _, _ := newTestServer(t)
	rec := serve(h, "/calendar.ics")
	if rec.Code != 200 || !strings.HasPrefix(rec.Body.String(), "BEGIN:VCALENDAR\r\n") {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
}

// TestCalendarPage: the month grid lists the stored releases of watching
// entries on their days, navigates months, and answers htmx with just the
// grid.
func TestCalendarPage(t *testing.T) {
	e := newFixtureEnv(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	e.now = &now
	id := e.addFixture(t, "tv", "show", tvShow("1", "Dark"), library.Entry{Status: library.Active})
	err := e.lib.SaveSchedule(context.Background(), id, []library.Airing{
		{Number: 3, AirsAt: time.Date(2026, 11, 12, 18, 30, 0, 0, time.UTC)},
		{Number: 4, AirsAt: time.Date(2026, 12, 2, 18, 30, 0, 0, time.UTC)},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	b := e.get("/calendar").Body.String()
	mustContain(t, b, ">Calendar</h1>", "November 2026", ">Mon</th>", ">Sun</th>",
		`href="/calendar?month=2026-10"`, `href="/calendar?month=2026-12"`,
		`href="/tv/tvdata/1"`, "Dark", "Episode 3", "12")
	mustNotContain(t, b, "Episode 4") // December, not this month
	// another month, and an invalid one falls back to the current
	mustContain(t, e.get("/calendar?month=2026-12").Body.String(), "December 2026", "Episode 4")
	mustContain(t, e.get("/calendar?month=nope").Body.String(), "November 2026")
	// htmx asks for the grid only
	rec := e.getWith("/calendar?month=2026-12", map[string]string{"HX-Request": "true", "HX-Target": "calendar-grid"})
	body := rec.Body.String()
	mustContain(t, body, `id="calendar-grid"`, "Episode 4")
	mustNotContain(t, body, "<html", "<header")
	if v := rec.Header().Get("Vary"); !strings.Contains(v, "HX-Request") {
		t.Fatalf("Vary = %q", v)
	}
	// the sidebar links the calendar and marks it current
	mustContain(t, b, `href="/calendar"`, `aria-current="page"`)
	// viewing the page also asks for stale schedules (its own entries were
	// fetched above, so only entries without one are enqueued — none here)
	if len(e.jobs.got) != 0 {
		t.Fatalf("unexpected enqueues: %d", len(e.jobs.got))
	}
	// a watching entry without a stored schedule is enqueued from the page
	e.addFixture(t, "tv", "show", tvShow("2", "Fresh"), library.Entry{Status: library.Active})
	e.get("/calendar")
	found := false
	for _, j := range e.jobs.got {
		if j.Kind == "sync_schedule" {
			found = true
		}
	}
	if !found {
		t.Fatalf("stale schedule not enqueued from the page: %+v", e.jobs.got)
	}
}

var _ = plugin.Airing{}

func TestCalendarSeasonLabels(t *testing.T) {
	e := newFixtureEnv(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	e.now = &now
	id := e.addFixture(t, "tv", "show", tvShow("1", "Dark"), library.Entry{Status: library.Active})
	if err := e.lib.SaveSchedule(context.Background(), id, []library.Airing{{Season: 2, Number: 5, AirsAt: now.Add(time.Hour)}}, now); err != nil {
		t.Fatal(err)
	}
	b := e.get("/calendar.ics").Body.String()
	mustContain(t, b, "UID:"+idStr(id)+"-s2-5@allister\r\n", "SUMMARY:Dark — Season 2\\, Episode 5\r\n")
	page := e.get("/calendar").Body.String()
	mustContain(t, page, "S2E5", ">S2E5</span>")
}

func TestParseCalendarWeek(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, berlin) // a Wednesday
	cases := []struct {
		name, raw string
		now       time.Time
		want      time.Time
	}{
		{"mid-week day gives its Monday", "2026-11-12", now, time.Date(2026, 11, 9, 0, 0, 0, 0, berlin)},
		{"a Monday is itself", "2026-11-09", now, time.Date(2026, 11, 9, 0, 0, 0, 0, berlin)},
		{"a Sunday belongs to the week before", "2026-11-15", now, time.Date(2026, 11, 9, 0, 0, 0, 0, berlin)},
		{"garbage is the current week", "nope", now, time.Date(2026, 11, 9, 0, 0, 0, 0, berlin)},
		{"empty is the current week", "", now, time.Date(2026, 11, 9, 0, 0, 0, 0, berlin)},
		{"impossible date is the current week", "2026-02-31", now, time.Date(2026, 11, 9, 0, 0, 0, 0, berlin)},
		{"DST end week keeps local midnight", "2026-10-28", now, time.Date(2026, 10, 26, 0, 0, 0, 0, berlin)},
		{"DST start week keeps local midnight", "2026-03-29", now, time.Date(2026, 3, 23, 0, 0, 0, 0, berlin)},
		{"year boundary goes back a year", "2027-01-01", now, time.Date(2026, 12, 28, 0, 0, 0, 0, berlin)},
		{"current week across the new year", "", time.Date(2027, 1, 2, 9, 0, 0, 0, berlin), time.Date(2026, 12, 28, 0, 0, 0, 0, berlin)},
	}
	for _, c := range cases {
		got := parseCalendarWeek(c.raw, c.now)
		if !got.Equal(c.want) || got.Location() != berlin || got.Hour() != 0 {
			t.Errorf("%s: parseCalendarWeek(%q) = %v, want %v", c.name, c.raw, got, c.want)
		}
	}
}

// scheduleEnv has two schedule-capable plugins (tv and books) and one show
// and one book airing in the week of Monday 2026-11-09.
func scheduleEnv(t *testing.T) *fixtureEnv {
	t.Helper()
	e := newFixtureEnv(t, func(book, tv *plugintest.Plugin) {
		book.M.Sources[0].SupportsSchedule = true
	})
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	e.now = &now
	show := e.addFixture(t, "tv", "show", tvShow("1", "Dark"), library.Entry{Status: library.Active})
	book := e.addFixture(t, "book", "book", bookItem("2", "Earthsea"), library.Entry{Status: library.Active})
	save := func(id int64, n int, at time.Time) {
		if err := e.lib.SaveSchedule(context.Background(), id, []library.Airing{{Number: n, AirsAt: at}}, now); err != nil {
			t.Fatal(err)
		}
	}
	save(show, 3, time.Date(2026, 11, 12, 18, 30, 0, 0, time.UTC))
	// a second show release on the Sunday and the book on the Monday
	if err := e.lib.SaveSchedule(context.Background(), show, []library.Airing{
		{Number: 3, AirsAt: time.Date(2026, 11, 12, 18, 30, 0, 0, time.UTC)},
		{Number: 4, AirsAt: time.Date(2026, 11, 15, 23, 30, 0, 0, time.UTC)},
	}, now); err != nil {
		t.Fatal(err)
	}
	save(book, 9, time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC))
	return e
}

func TestCalendarWeekView(t *testing.T) {
	e := scheduleEnv(t)
	b := e.get("/calendar?view=week&week=2026-11-12").Body.String()
	mustContain(t, b, "9 – 15 Nov 2026", ">Mon</th>", ">Sun</th>",
		`href="/calendar?view=week&amp;week=2026-11-02"`, `href="/calendar?view=week&amp;week=2026-11-16"`,
		"Dark", "Episode 3", "Episode 4", "Earthsea")
	if n := strings.Count(b, `class="calendar-day`); n != 7 {
		t.Fatalf("%d day cells, want 7", n)
	}
	// the Monday cell comes first and holds the book
	if strings.Index(b, "Earthsea") > strings.Index(b, "Dark") {
		t.Error("Monday's release is not before Thursday's")
	}
	// Sunday 23:30 UTC lands on Monday in a zone ahead of UTC
	if rec := e.do("POST", "/settings/time-zone", url.Values{"time_zone": {"Europe/Berlin"}}, false); rec.Code != 303 {
		t.Fatalf("%d", rec.Code)
	}
	b = e.get("/calendar?view=week&week=2026-11-09").Body.String()
	mustNotContain(t, b, "Episode 4") // now Monday 16 Nov: next week
	mustContain(t, e.get("/calendar?view=week&week=2026-11-16").Body.String(), "Episode 4")
	// view toggle links stay on the same period
	mustContain(t, b, `href="/calendar?month=2026-11"`, `href="/calendar?view=week&amp;week=2026-11-09"`)
	// a week across the new year labels both years
	mustContain(t, e.get("/calendar?view=week&week=2026-12-31").Body.String(), "28 Dec 2026 – 3 Jan 2027")
	// garbage means the current week; an unknown view means the month
	mustContain(t, e.get("/calendar?view=week&week=zzz").Body.String(), "9 – 15 Nov 2026")
	mustContain(t, e.get("/calendar?view=year").Body.String(), "November 2026")
}

func TestCalendarPluginFilter(t *testing.T) {
	e := scheduleEnv(t)
	for _, u := range []string{"/calendar?plugin=tv&month=2026-11", "/calendar?view=week&week=2026-11-09&plugin=tv"} {
		b := e.get(u).Body.String()
		mustContain(t, b, "Dark")
		mustNotContain(t, b, "Earthsea")
		// navigation keeps the filter
		mustContain(t, b, "plugin=tv")
	}
	b := e.get("/calendar?plugin=book&month=2026-11").Body.String()
	mustContain(t, b, "Earthsea")
	mustNotContain(t, b, "Dark")
	// both without a filter
	b = e.get("/calendar?view=week&week=2026-11-09").Body.String()
	mustContain(t, b, "Dark", "Earthsea")
	mustNotContain(t, b, "plugin=")
	// the htmx partial keeps both views and the filter
	for _, u := range []string{"/calendar?plugin=tv", "/calendar?view=week&plugin=tv"} {
		rec := e.getWith(u, map[string]string{"HX-Request": "true", "HX-Target": "calendar-grid"})
		body := rec.Body.String()
		mustContain(t, body, `id="calendar-grid"`, `hx-target="#calendar-grid"`)
		mustNotContain(t, body, "<html", "<header")
		if v := rec.Header().Get("Vary"); v != "HX-Request, HX-Target, HX-History-Restore-Request" {
			t.Fatalf("Vary = %q", v)
		}
	}
}

func TestCalendarUnknownPluginIgnored(t *testing.T) {
	e := scheduleEnv(t)
	b := e.get("/calendar?plugin=nope&month=2026-11").Body.String()
	mustContain(t, b, "Dark", "Earthsea")
	mustNotContain(t, b, "plugin=nope")
	b = e.get("/calendar?view=week&week=2026-11-09&plugin=%00%3Cx%3E").Body.String()
	mustContain(t, b, "Dark", "Earthsea")
	mustNotContain(t, b, "plugin=")
}

func TestCalendarPluginSelectListsScheduleCapable(t *testing.T) {
	// only tv declares schedules in the plain fixture
	e := newFixtureEnv(t)
	b := e.get("/calendar").Body.String()
	mustContain(t, b, `<option value="">All plugins</option>`, `<option value="tv"`)
	mustNotContain(t, b, `<option value="book"`)
	e = scheduleEnv(t)
	mustContain(t, e.get("/calendar?plugin=book").Body.String(), `<option value="tv">`, `<option value="book" selected>`)
}

// watchEnv has one active show with episodes 1 and 2 aired (Nov 9 and 10) and
// episode 3 still to air (Nov 12), now being Wednesday 2026-11-11.
func watchEnv(t *testing.T, progress float64) (*fixtureEnv, int64) {
	t.Helper()
	e := newFixtureEnv(t)
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	e.now = &now
	en := library.Entry{Status: library.Active}
	if progress > 0 {
		en.Progress = map[string]float64{"episode": progress}
	}
	id := e.addFixture(t, "tv", "show", tvShow("1", "Dark"), en)
	if err := e.lib.SaveSchedule(context.Background(), id, []library.Airing{
		{Number: 1, AirsAt: time.Date(2026, 11, 9, 18, 0, 0, 0, time.UTC)},
		{Number: 2, AirsAt: time.Date(2026, 11, 10, 18, 0, 0, 0, time.UTC)},
		{Number: 3, AirsAt: time.Date(2026, 11, 12, 18, 0, 0, 0, time.UTC)},
	}, now); err != nil {
		t.Fatal(err)
	}
	return e, id
}

func watchForm(season, number int, ret string) url.Values {
	return url.Values{"season": {fmt.Sprint(season)}, "number": {fmt.Sprint(number)}, "return": {ret}}
}

func episodeOf(t *testing.T, e *fixtureEnv, id int64) float64 {
	t.Helper()
	en, err := e.lib.Entry(context.Background(), id)
	if err != nil || en == nil {
		t.Fatalf("entry: %v %v", en, err)
	}
	return en.Progress["episode"]
}

// TestCalendarMarkWatchedButton: the button is offered only for aired
// releases beyond the progress of an active entry on its first run.
func TestCalendarMarkWatchedButton(t *testing.T) {
	for _, u := range []string{"/calendar?month=2026-11", "/calendar?view=week&week=2026-11-09"} {
		e, id := watchEnv(t, 0)
		b := e.get(u).Body.String()
		action := fmt.Sprintf(`action="/list/%d/watched"`, id)
		if n := strings.Count(b, action); n != 2 { // episodes 1 and 2, not the unaired 3
			t.Fatalf("%s: %d buttons, want 2:\n%s", u, n, b)
		}
		mustContain(t, b, `name="number" value="1"`, `name="number" value="2"`, `aria-label="Mark Dark Episode 2 watched"`)
		mustNotContain(t, b, `name="number" value="3"`)
		// the form returns to the very view it was on
		mustContain(t, b, `name="return" value="`+strings.ReplaceAll(strings.ReplaceAll(u, "&", "&amp;"), "?", "?")+`"`)
	}
	// progress at or beyond a release hides its button
	e, id := watchEnv(t, 1)
	b := e.get("/calendar?month=2026-11").Body.String()
	if n := strings.Count(b, fmt.Sprintf(`action="/list/%d/watched"`, id)); n != 1 {
		t.Fatalf("%d buttons, want 1 (episode 2 only)", n)
	}
	mustNotContain(t, b, `name="number" value="1"`)
	e, _ = watchEnv(t, 2)
	mustNotContain(t, e.get("/calendar?month=2026-11").Body.String(), "/watched")
	// a repeat run offers none
	e, _ = watchEnv(t, 0)
	rep := e.addFixture(t, "tv", "show", tvShow("9", "Rerun"), library.Entry{Status: library.Completed})
	if _, err := e.lib.StartRun(context.Background(), rep, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.SaveSchedule(context.Background(), rep, []library.Airing{{Number: 1, AirsAt: time.Date(2026, 11, 9, 18, 0, 0, 0, time.UTC)}}, *e.now); err != nil {
		t.Fatal(err)
	}
	b = e.get("/calendar?month=2026-11").Body.String()
	mustContain(t, b, "Rerun")
	mustNotContain(t, b, fmt.Sprintf(`action="/list/%d/watched"`, rep))
}

func TestPostListWatched(t *testing.T) {
	e, id := watchEnv(t, 0)
	path := fmt.Sprintf("/list/%d/watched", id)
	back := "/calendar?month=2026-11"
	rec := e.do("POST", path, watchForm(0, 2, back), false)
	if rec.Code != 303 || rec.Header().Get("Location") != back {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Location"))
	}
	if got := episodeOf(t, e, id); got != 2 {
		t.Fatalf("episode = %v, want 2", got)
	}
	countEvents := func() int {
		var n int
		if err := e.d.QueryRow(`SELECT COUNT(*) FROM entry_events WHERE item_id = ?`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if countEvents() != 1 {
		t.Fatalf("%d events, want 1", countEvents())
	}
	// a double submit and an older release change nothing and still succeed
	for _, n := range []int{2, 1} {
		if rec := e.do("POST", path, watchForm(0, n, back), false); rec.Code != 303 {
			t.Fatalf("repeat %d: %d", n, rec.Code)
		}
	}
	if episodeOf(t, e, id) != 2 || countEvents() != 1 {
		t.Fatalf("a repeat changed the entry: episode %v, %d events", episodeOf(t, e, id), countEvents())
	}
	// forged: an unaired release, one that was never stored, a season that does not exist
	for _, f := range []url.Values{watchForm(0, 3, back), watchForm(0, 99, back), watchForm(2, 1, back)} {
		if rec := e.do("POST", path, f, false); rec.Code != 422 {
			t.Fatalf("%v: %d, want 422", f, rec.Code)
		}
	}
	if episodeOf(t, e, id) != 2 {
		t.Fatal("a forged release moved the progress")
	}
	// malformed numbers
	for _, f := range []url.Values{{"season": {"0"}}, {"season": {"x"}, "number": {"1"}}, {"season": {"0"}, "number": {"1.5"}}} {
		if rec := e.do("POST", path, f, false); rec.Code != 400 {
			t.Fatalf("%v: %d, want 400", f, rec.Code)
		}
	}
	// unknown item and an item without entry
	if rec := e.do("POST", "/list/9999/watched", watchForm(0, 1, back), false); rec.Code != 404 {
		t.Fatalf("unknown item: %d", rec.Code)
	}
	// a paused entry is refused
	if err := e.lib.UpsertEntry(context.Background(), library.Entry{ItemID: id, Status: library.Paused, Progress: map[string]float64{"episode": 2}}); err != nil {
		t.Fatal(err)
	}
	if rec := e.do("POST", path, watchForm(0, 2, back), false); rec.Code != 422 {
		t.Fatalf("paused: %d", rec.Code)
	}
}

func TestPostListWatchedReturnIsSameSite(t *testing.T) {
	for raw, want := range map[string]string{
		"/calendar?view=week&week=2026-11-09": "/calendar?view=week&week=2026-11-09",
		"/calendar":                           "/calendar",
		"":                                    "/calendar",
		"//evil.example/x":                    "/calendar",
		"https://evil.example/":               "/calendar",
		"/\\evil.example":                     "/calendar",
		"evil":                                "/calendar",
		"/a\r\nSet-Cookie: x=1":               "/calendar",
	} {
		e, id := watchEnv(t, 0)
		rec := e.do("POST", fmt.Sprintf("/list/%d/watched", id), watchForm(0, 1, raw), false)
		if rec.Code != 303 || rec.Header().Get("Location") != want {
			t.Errorf("return %q: %d %q, want %q", raw, rec.Code, rec.Header().Get("Location"), want)
		}
	}
}

func TestPostListWatchedHTMXGrid(t *testing.T) {
	e, id := watchEnv(t, 0)
	// without the grid target the answer is the redirect
	if rec := e.do("POST", fmt.Sprintf("/list/%d/watched", id), watchForm(0, 1, "/calendar"), true); rec.Code != 303 {
		t.Fatalf("htmx without target: %d", rec.Code)
	}
	form := watchForm(0, 2, "/calendar?view=week&week=2026-11-09")
	r := httptest.NewRequest("POST", fmt.Sprintf("/list/%d/watched", id), strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("HX-Request", "true")
	r.Header.Set("HX-Target", "calendar-grid")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	mustContain(t, body, `id="calendar-grid"`, "9 – 15 Nov 2026")
	mustNotContain(t, body, "<html", "<header")
	// both aired episodes are now at or below the progress: no button left
	mustNotContain(t, body, "/watched")
	if episodeOf(t, e, id) != 2 {
		t.Fatalf("episode = %v", episodeOf(t, e, id))
	}
}

// TestPostListWatchedDisabledPlugin: the guard answers 404 before anything is
// written when the item's plugin is disabled.
func TestPostListWatchedDisabledPlugin(t *testing.T) {
	e, id := watchEnv(t, 0)
	if err := e.reg.SetEnabled(context.Background(), "tv", false); err != nil {
		t.Fatal(err)
	}
	if rec := e.do("POST", fmt.Sprintf("/list/%d/watched", id), watchForm(0, 2, "/calendar"), false); rec.Code != 404 {
		t.Fatalf("disabled: %d", rec.Code)
	}
	if err := e.reg.SetEnabled(context.Background(), "tv", true); err != nil {
		t.Fatal(err)
	}
	if episodeOf(t, e, id) != 0 {
		t.Fatal("a disabled plugin's POST changed progress")
	}
}

// TestMarkWatchedTotalBelowRelease: a release numbered above the item's total
// sets the total, never more.
func TestMarkWatchedTotalBelowRelease(t *testing.T) {
	e := newFixtureEnv(t)
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	e.now = &now
	d := tvShow("7", "Short")
	d.Fields["episodes"] = plugin.FieldValue{Num: num(2)}
	id := e.addFixture(t, "tv", "show", d, library.Entry{Status: library.Active, Progress: map[string]float64{"episode": 1}})
	if err := e.lib.SaveSchedule(context.Background(), id, []library.Airing{{Number: 3, AirsAt: now.Add(-time.Hour)}}, now); err != nil {
		t.Fatal(err)
	}
	if rec := e.do("POST", fmt.Sprintf("/list/%d/watched", id), watchForm(0, 3, "/calendar"), false); rec.Code != 303 {
		t.Fatalf("%d", rec.Code)
	}
	if got := episodeOf(t, e, id); got != 2 {
		t.Fatalf("episode = %v, want the total 2", got)
	}
	if rec := e.do("POST", fmt.Sprintf("/list/%d/watched", id), watchForm(0, 3, "/calendar"), false); rec.Code != 303 || episodeOf(t, e, id) != 2 {
		t.Fatal("the second post changed something")
	}
}

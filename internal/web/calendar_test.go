package web

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// The fixture TV source declares SupportsSchedule; the book source does not.
func TestCalendarFeed(t *testing.T) {
	e := newFixtureEnv(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	e.env.now = &now
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
	e.env.now = &now
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
	e.env.now = &now
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

var _ = plugin.Airing{}

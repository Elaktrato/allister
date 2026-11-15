package web

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/library"
)

// lateRelease stores a release at 23:30 UTC on Monday 2026-11-09 for a show.
func lateRelease(t *testing.T, e *fixtureEnv, now time.Time) {
	t.Helper()
	id := e.addFixture(t, "tv", "show", tvShow("1", "Dark"), library.Entry{Status: library.Active})
	at := time.Date(2026, 11, 9, 23, 30, 0, 0, time.UTC)
	if err := e.lib.SaveSchedule(context.Background(), id, []library.Airing{{Number: 4, AirsAt: at}}, now); err != nil {
		t.Fatal(err)
	}
}

func TestTimeZoneSettingValidates(t *testing.T) {
	e := newFixtureEnv(t)
	rec := e.do("POST", "/settings/time-zone", url.Values{"time_zone": {"Mars/Olympus"}}, false)
	if rec.Code != 422 {
		t.Fatalf("invalid zone: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Mars/Olympus", "not a time zone")
	if e.setting(t, "time_zone") != "" {
		t.Error("invalid zone stored")
	}
	if rec := e.do("POST", "/settings/time-zone", url.Values{"time_zone": {"Europe/Berlin"}}, false); rec.Code != 303 {
		t.Fatalf("valid zone: %d", rec.Code)
	}
	if e.setting(t, "time_zone") != "Europe/Berlin" {
		t.Error("zone not stored")
	}
	mustContain(t, e.get("/settings").Body.String(), `value="Europe/Berlin"`)
	if rec := e.do("POST", "/settings/time-zone", url.Values{"time_zone": {"  "}}, false); rec.Code != 303 {
		t.Fatalf("empty zone: %d", rec.Code)
	}
	if e.setting(t, "time_zone") != "" {
		t.Error("empty value should reset to the default")
	}
}

func TestTimeZoneDefaultShownFollowsTZ(t *testing.T) {
	e := newFixtureEnv(t)
	t.Setenv("TZ", "Asia/Tokyo")
	mustContain(t, e.get("/settings").Body.String(), "Asia/Tokyo")
	os.Unsetenv("TZ")
	mustContain(t, e.get("/settings").Body.String(), "UTC")
}

func TestTimeZoneGroupsHomeAndCalendar(t *testing.T) {
	e := newFixtureEnv(t)
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	e.now = &now
	lateRelease(t, e, now)

	// default (UTC): Monday, 23:30
	sec := section(t, e.get("/").Body.String(), "Coming up")
	mustContain(t, sec, "Monday, 9 November", "23:30")

	e.do("POST", "/settings/time-zone", url.Values{"time_zone": {"Europe/Berlin"}}, false)
	sec = section(t, e.get("/").Body.String(), "Coming up")
	mustContain(t, sec, "Tuesday, 10 November", "00:30")
	mustNotContain(t, sec, "Monday, 9 November", "23:30", "data-local")

	cal := e.get("/calendar?month=2026-11").Body.String()
	mustContain(t, cal, "00:30")
	mustNotContain(t, cal, "23:30", "data-local")
	// the chip sits in the cell of the 10th
	i := strings.Index(cal, "00:30")
	before := cal[:i]
	if d10, d9 := strings.LastIndex(before, ">10</div>"), strings.LastIndex(before, ">9</div>"); d10 < 0 || d10 < d9 {
		t.Errorf("chip not under the 10th (10 at %d, 9 at %d)", d10, d9)
	}

	// the feed stays UTC
	ics := e.get("/calendar.ics").Body.String()
	mustContain(t, ics, "DTSTART:20261109T233000Z\r\n")
}

func TestTimeZoneBackupRoundTrip(t *testing.T) {
	a := backupEnv(t)
	a.do("POST", "/settings/time-zone", url.Values{"time_zone": {"Europe/Berlin"}}, false)
	data := a.download(t)

	b := backupEnv(t)
	if rec := b.restoreUpload(t, data); rec.Code != 200 {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body.String())
	}
	if b.setting(t, "time_zone") != "Europe/Berlin" {
		t.Errorf("zone after restore: %q", b.setting(t, "time_zone"))
	}

	// an installation with its own zone keeps it
	c := backupEnv(t)
	c.do("POST", "/settings/time-zone", url.Values{"time_zone": {"Asia/Tokyo"}}, false)
	c.restoreUpload(t, data)
	if c.setting(t, "time_zone") != "Asia/Tokyo" {
		t.Errorf("own zone overwritten: %q", c.setting(t, "time_zone"))
	}
}

func TestTimeZoneBrowserButton(t *testing.T) {
	e := newFixtureEnv(t)
	mustContain(t, e.get("/settings").Body.String(), `data-use-browser-zone`, "Use this browser")
	js, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"data-use-browser-zone", "resolvedOptions().timeZone"} {
		if !strings.Contains(string(js), want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
}

// A month with a DST change is not a whole number of 24-hour days: the grid
// must still show every day (March 2026 has 31 in Europe/Berlin).
func TestCalendarGridWholeMonthAcrossDST(t *testing.T) {
	e := newFixtureEnv(t)
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	e.now = &now
	e.do("POST", "/settings/time-zone", url.Values{"time_zone": {"Europe/Berlin"}}, false)
	b := e.get("/calendar?month=2026-03").Body.String()
	mustContain(t, b, ">31</div>")
}

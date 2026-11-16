package web

import (
	"context"
	"testing"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// seedRunEvent appends an event of run runID with an explicit UTC stamp.
func seedRunEvent(t *testing.T, e *env, itemID, runID int64, at time.Time, typ, key string, from, to *float64, oldText, newText, origin string) {
	t.Helper()
	if _, err := e.d.Exec(`INSERT INTO entry_events(item_id, run_id, occurred_at, event_type, progress_key, old_num, new_num, old_text, new_text, origin)
		VALUES (?,?,?,?,?,?,?,?,?,?)`, itemID, runID, at.UTC().Format("2006-01-02T15:04:05Z"), typ, key, from, to, oldText, newText, origin); err != nil {
		t.Fatal(err)
	}
}

func runTimelineURL(item int64, run library.Run) string {
	return "/list/" + idStr(item) + "/runs/" + runIDStr(run) + "/timeline"
}

func TestTimelineWording(t *testing.T) {
	e := newFixtureEnv(t, func(_, tv *plugintest.Plugin) {
		tv.M.Kinds[0].Progress.Eligible[1].LabelPlural = "Eps"
	})
	id := e.addFixture(t, "tv", "show", tvWithEpisodes("1", "Wording", 20), library.Entry{Status: library.Active, Progress: map[string]float64{"episode": 7}})
	run := e.runsOf(t, id)[0]
	base := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	seedRunEvent(t, e.env, id, run.ID, base, "progress", "episode", num(4), num(5), "", "", "manual")
	seedRunEvent(t, e.env, id, run.ID, base.Add(time.Hour), "progress", "episode", num(4), num(7), "", "", "manual")
	seedRunEvent(t, e.env, id, run.ID, base.Add(2*time.Hour), "progress", "position", num(60), num(3725), "", "", "manual")
	seedRunEvent(t, e.env, id, run.ID, base.Add(3*time.Hour), "status", "", nil, nil, "active", "completed", "manual")
	seedRunEvent(t, e.env, id, run.ID, base.Add(4*time.Hour), "rating", "", nil, num(8), "", "", "manual")
	seedRunEvent(t, e.env, id, run.ID, base.Add(5*time.Hour), "rating", "", num(8), nil, "", "", "manual")
	seedRunEvent(t, e.env, id, run.ID, base.Add(6*time.Hour), "progress", "episode", num(7), num(3), "", "", "manual")

	rec := e.do("GET", runTimelineURL(id, run), nil, true)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	b := rec.Body.String()
	mustContain(t, b, "Episode 5", "Eps 5-7", "Position", "0:01:00", "1:02:05", "Status", "Watching", "Watched", "Rating", "8", "Episode 7", "3")
	mustNotContain(t, b, "<html", "Nothing was logged", "Imported", "Restored")
}

func TestTimelineMarksImportAndRestore(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.addFixture(t, "tv", "show", tvWithEpisodes("2", "Origins", 20), library.Entry{Status: library.Active, Progress: map[string]float64{"episode": 2}})
	run := e.runsOf(t, id)[0]
	base := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	seedRunEvent(t, e.env, id, run.ID, base, "progress", "episode", num(0), num(1), "", "", "import")
	seedRunEvent(t, e.env, id, run.ID, base.Add(time.Hour), "progress", "episode", num(1), num(2), "", "", "restore")
	b := e.do("GET", runTimelineURL(id, run), nil, true).Body.String()
	mustContain(t, b, "Imported", "Restored")
}

func TestTimelineEmptyRun(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.addFixture(t, "tv", "show", tvWithEpisodes("3", "Quiet", 20), library.Entry{Status: library.Planned})
	run := e.runsOf(t, id)[0]
	rec := e.do("GET", runTimelineURL(id, run), nil, true)
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	mustContain(t, rec.Body.String(), "Nothing was logged for this run")
}

func TestTimelineUsesTheConfiguredTimeZone(t *testing.T) {
	e := newFixtureEnv(t)
	if err := e.set.SetTimeZone(context.Background(), "Europe/Berlin"); err != nil {
		t.Fatal(err)
	}
	id := e.addFixture(t, "tv", "show", tvWithEpisodes("4", "Zoned", 20), library.Entry{Status: library.Active, Progress: map[string]float64{"episode": 1}})
	run := e.runsOf(t, id)[0]
	seedRunEvent(t, e.env, id, run.ID, time.Date(2026, 1, 1, 23, 30, 0, 0, time.UTC), "progress", "episode", num(0), num(1), "", "", "manual")
	b := e.do("GET", runTimelineURL(id, run), nil, true).Body.String()
	mustContain(t, b, "2026-01-02 00:30", `datetime="2026-01-01T23:30:00Z"`)
}

func TestTimelineRouteGuards(t *testing.T) {
	e := newFixtureEnv(t)
	a := e.addFixture(t, "tv", "show", tvWithEpisodes("5", "A", 20), library.Entry{Status: library.Active})
	b := e.addFixture(t, "tv", "show", tvWithEpisodes("6", "B", 20), library.Entry{Status: library.Active})
	runB := e.runsOf(t, b)[0]
	for _, target := range []string{
		runTimelineURL(a, runB), // a run of another item
		"/list/" + idStr(a) + "/runs/abc/timeline",
		"/list/" + idStr(a) + "/runs/0/timeline",
	} {
		if rec := e.do("GET", target, nil, true); rec.Code != 404 {
			t.Fatalf("%s: %d", target, rec.Code)
		}
	}
}

func TestItemPageLoadsTheTimelineLazily(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.addFixture(t, "tv", "show", tvWithEpisodes("7", "Lazy", 20), library.Entry{Status: library.Active})
	run := e.runsOf(t, id)[0]
	rec := e.do("GET", "/tv/tvdata/7", nil, false)
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	body := rec.Body.String()
	mustContain(t, body, `hx-get="`+runTimelineURL(id, run)+`"`, `hx-trigger="toggle once"`, "Timeline")
	mustNotContain(t, body, "Nothing was logged")
}

// Without JavaScript the collapsed timeline offers a link to its fragment
// instead of staying "Loading…".
func TestTimelineNoscriptLink(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.addFixture(t, "tv", "show", tvShow("1", "Linked"), library.Entry{Status: library.Active})
	u := runTimelineURL(id, e.runsOf(t, id)[0])
	b := e.get("/tv/tvdata/1").Body.String()
	mustContain(t, b, `<noscript><a href="`+u+`"`)
	if rec := e.do("GET", u, nil, false); rec.Code != 200 {
		t.Fatalf("timeline without htmx: %d", rec.Code)
	}
}

package web

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// --- runs: start again, the dialog question, markers, run history ---

// runsOf returns the stored runs of item id, newest first.
func (e *env) runsOf(t *testing.T, id int64) []library.Run {
	t.Helper()
	runs, err := e.lib.Runs(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return runs
}

// completedShow lists a finished tv show (10 of 10 episodes, rated 8).
func completedShow(t *testing.T, e *env, id, title string) int64 {
	t.Helper()
	start, end := day(2025, 3, 1), day(2025, 4, 1)
	return e.addFixture(t, "tv", "show", tvWithEpisodes(id, title, 10), library.Entry{Status: library.Completed,
		Rating: ip(8), StartedOn: start, FinishedOn: end, Progress: map[string]float64{"episode": 10}})
}

func runIDStr(r library.Run) string { return strconv.FormatInt(r.ID, 10) }

// repeatLabels gives the tv fixture's show kind its own repeat wording.
func repeatLabels(_, tv *plugintest.Plugin) {
	tv.M.Kinds[0].Repeat = &plugin.RepeatDef{Action: "Watch again", Marker: "Watching again"}
}

func TestRunStartAgainAnswersLikeIncrement(t *testing.T) {
	e := newFixtureEnv(t, repeatLabels)
	if err := e.set.SetTimeZone(context.Background(), "Europe/Berlin"); err != nil {
		t.Fatal(err)
	}
	late := time.Date(2026, 1, 1, 23, 30, 0, 0, time.UTC) // already Jan 2 in Berlin
	e.now = &late
	id := completedShow(t, e.env, "1", "Again Show")
	rec := e.do("POST", "/list/"+idStr(id)+"/runs", url.Values{"return": {"/tv?view=table"}}, true)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	b := rec.Body.String()
	mustContain(t, b, "<tr data-row", "Again Show", "Watching again")
	mustNotContain(t, b, "<html")
	runs := e.runsOf(t, id)
	if len(runs) != 2 || runs[0].Number != 2 || runs[0].Status != library.Active || len(runs[0].Progress) != 0 {
		t.Fatalf("runs: %+v", runs)
	}
	if got := fmtDay(runs[0].StartedOn); got != "2026-01-02" {
		t.Fatalf("started on %q, want today in the configured zone", got)
	}
	if runs[0].Rating == nil || *runs[0].Rating != 8 {
		t.Fatalf("rating not copied: %v", runs[0].Rating)
	}
	// the display run is active now: a second start is refused
	if rec := e.do("POST", "/list/"+idStr(id)+"/runs", url.Values{}, false); rec.Code != 400 {
		t.Fatal(rec.Code)
	}
	if n := len(e.runsOf(t, id)); n != 2 {
		t.Fatalf("refused start wrote: %d runs", n)
	}
	// a card answers the card, a plain post redirects to the return path
	card := completedShow(t, e.env, "2", "Card Show")
	rec = e.do("POST", "/list/"+idStr(card)+"/runs", url.Values{"return": {"/"}, "view": {"cards"}}, true)
	mustContain(t, rec.Body.String(), "data-card", "Card Show", "Watching again")
	plain := completedShow(t, e.env, "3", "Plain Show")
	rec = e.do("POST", "/list/"+idStr(plain)+"/runs", url.Values{"return": {"/tv/tvdata/3"}}, false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/tv/tvdata/3" {
		t.Fatal(rec.Code, rec.Header())
	}
}

func TestRepeatMarkerAndCountOnRowsAndCards(t *testing.T) {
	e := newFixtureEnv(t)
	ctx := context.Background()
	id := completedShow(t, e.env, "1", "Twice Show")
	completedShow(t, e.env, "2", "Once Show")
	run, err := e.lib.StartRun(ctx, id, *day(2026, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	// run 2 active: the neutral marker, no count yet (one completed run)
	row := rowOf(t, e.get("/tv?view=table").Body.String(), "Twice Show")
	mustContain(t, row, "Again")
	mustNotContain(t, row, "x2", "x1")
	mustNotContain(t, rowOf(t, e.get("/tv?view=table").Body.String(), "Once Show"), "Again", "x1")
	if err := e.lib.UpdateRun(ctx, run.ID, library.RunUpdate{Status: library.Completed}); err != nil {
		t.Fatal(err)
	}
	// two completed runs: the count, no marker (the display run is completed)
	row = rowOf(t, e.get("/tv?view=table").Body.String(), "Twice Show")
	mustContain(t, row, "x2")
	mustNotContain(t, row, ">Again<")
	cards := e.get("/tv?view=cards").Body.String()
	mustContain(t, cardOf(t, cards, "Twice Show"), "x2")
	mustNotContain(t, cardOf(t, cards, "Once Show"), "x1", "x2")
	// a third, active run shows both on the card and on home
	if _, err := e.lib.StartRun(ctx, id, *day(2026, 1, 1)); err != nil {
		t.Fatal(err)
	}
	card := cardOf(t, e.get("/tv?view=cards").Body.String(), "Twice Show")
	mustContain(t, card, ">Again<", "x2")
	home := e.get("/").Body.String()
	mustContain(t, cardOf(t, home, "Twice Show"), ">Again<", "x2")
}

// cardOf returns the <div data-card ...> card whose html contains marker.
func cardOf(t *testing.T, body, marker string) string {
	t.Helper()
	const open = "<div data-card class"
	for _, part := range strings.Split(body, open)[1:] {
		if strings.Contains(part, marker) {
			return open + part
		}
	}
	t.Fatalf("no card containing %q", marker)
	return ""
}

func TestRunDialogAsksBeforeMovingCompletedBack(t *testing.T) {
	e := newFixtureEnv(t, repeatLabels)
	id := completedShow(t, e.env, "1", "Asked Show")
	edit := "/list/" + idStr(id) + "/edit"
	form := url.Values{"status": {"active"}, "rating": {"8"}, "progress.episode": {"10"},
		"started_on": {"2025-03-01"}, "finished_on": {"2025-04-01"}, "comment": {"note"}, "return": {"/tv"}}
	before := dump(t, e.env)
	for _, htmx := range []bool{true, false} {
		rec := e.do("POST", edit, form, htmx)
		if rec.Code != 200 {
			t.Fatal(htmx, rec.Code, rec.Body.String())
		}
		b := rec.Body.String()
		mustContain(t, b, `name="run_choice" value="new"`, "Watch again instead", `name="run_choice" value="correct"`, "No, correct this one",
			`name="comment"`, ">note</textarea>")
		if after := dump(t, e.env); after != before {
			t.Fatalf("the question changed the database (htmx %v)", htmx)
		}
	}
	// no question when nothing moves a completed run back
	for _, st := range []string{"completed", "dropped"} {
		f := url.Values{"status": {st}, "return": {"/tv"}}
		other := completedShow(t, e.env, "q"+st, "No Question "+st)
		if rec := e.do("POST", "/list/"+idStr(other)+"/edit", f, true); rec.Code != 204 {
			t.Fatal(st, rec.Code, rec.Body.String())
		}
	}
	active := e.addFixture(t, "tv", "show", tvShow("a", "Active Show"), library.Entry{Status: library.Active})
	if rec := e.do("POST", "/list/"+idStr(active)+"/edit", url.Values{"status": {"planned"}}, true); rec.Code != 204 {
		t.Fatal(rec.Code)
	}

	// "correct" changes the completed run in place
	form.Set("run_choice", "correct")
	if rec := e.do("POST", edit, form, true); rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	runs := e.runsOf(t, id)
	if len(runs) != 1 || runs[0].Status != library.Active {
		t.Fatalf("correct: %+v", runs)
	}
}

func TestRunDialogNewStartsAgainWithTheDialogsChanges(t *testing.T) {
	e := newFixtureEnv(t)
	id := completedShow(t, e.env, "1", "New Show")
	// only the status and the comment change: the new run keeps its own
	// start (today), no finish, no progress and the copied rating
	form := url.Values{"status": {"active"}, "rating": {"8"}, "progress.episode": {"10"},
		"started_on": {"2025-03-01"}, "finished_on": {"2025-04-01"}, "comment": {"second time"}, "run_choice": {"new"}, "return": {"/tv"}}
	rec := e.do("POST", "/list/"+idStr(id)+"/edit", form, true)
	if rec.Code != 204 || rec.Header().Get("HX-Redirect") != "/tv" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	runs := e.runsOf(t, id)
	if len(runs) != 2 {
		t.Fatalf("runs: %+v", runs)
	}
	r2, r1 := runs[0], runs[1]
	if r2.Status != library.Active || fmtDay(r2.StartedOn) != "2026-01-01" || r2.FinishedOn != nil || r2.Progress["episode"] != 0 ||
		r2.Rating == nil || *r2.Rating != 8 {
		t.Fatalf("new run: %+v", r2)
	}
	if r1.Status != library.Completed || r1.Progress["episode"] != 10 || fmtDay(r1.FinishedOn) != "2025-04-01" {
		t.Fatalf("the completed run changed: %+v", r1)
	}
	if en := e.entryOf(t, id); en.Notes != "second time" || en.RunNumber != 2 {
		t.Fatalf("entry: %+v", en)
	}
	// changed values go to the new run
	other := completedShow(t, e.env, "2", "Changed Show")
	form = url.Values{"status": {"paused"}, "rating": {"6"}, "progress.episode": {"3"},
		"started_on": {"2025-12-20"}, "finished_on": {""}, "run_choice": {"new"}}
	if rec := e.do("POST", "/list/"+idStr(other)+"/edit", form, false); rec.Code != 303 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	runs = e.runsOf(t, other)
	if len(runs) != 2 || runs[0].Status != library.Paused || *runs[0].Rating != 6 || runs[0].Progress["episode"] != 3 ||
		fmtDay(runs[0].StartedOn) != "2025-12-20" || runs[0].FinishedOn != nil {
		t.Fatalf("changed new run: %+v", runs[0])
	}
	// an invalid change starts nothing
	bad := completedShow(t, e.env, "3", "Bad Show")
	form = url.Values{"status": {"active"}, "progress.episode": {"11"}, "run_choice": {"new"}}
	if rec := e.do("POST", "/list/"+idStr(bad)+"/edit", form, true); rec.Code != 422 {
		t.Fatal(rec.Code)
	}
	// the start stays (the new run's is today), the finish moves before it
	form = url.Values{"status": {"active"}, "started_on": {"2025-03-01"}, "finished_on": {"2025-12-31"}, "run_choice": {"new"}}
	if rec := e.do("POST", "/list/"+idStr(bad)+"/edit", form, true); rec.Code != 422 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if n := len(e.runsOf(t, bad)); n != 1 {
		t.Fatalf("invalid new: %d runs", n)
	}
}

func TestRunHistoryOnTheItemPage(t *testing.T) {
	e := newFixtureEnv(t, repeatLabels)
	id := completedShow(t, e.env, "1", "History Show")
	b := e.get("/tv/tvdata/1").Body.String()
	// one run: the start again button, history without delete
	mustContain(t, b, `action="/list/`+idStr(id)+`/runs"`, "Watch again", "Run 1")
	mustNotContain(t, b, "/delete")
	if rec := e.do("POST", "/list/"+idStr(id)+"/runs", url.Values{"return": {"/tv/tvdata/1"}}, false); rec.Code != 303 {
		t.Fatal(rec.Code)
	}
	runs := e.runsOf(t, id)
	b = e.get("/tv/tvdata/1").Body.String()
	mustNotContain(t, b, `action="/list/`+idStr(id)+`/runs"`) // the display run is active
	i2, i1 := strings.Index(b, "Run 2"), strings.Index(b, "Run 1")
	if i2 < 0 || i1 < 0 || i2 > i1 {
		t.Fatalf("history not newest first: %d %d", i2, i1)
	}
	past := "/list/" + idStr(id) + "/runs/" + runIDStr(runs[1])
	mustContain(t, b, "Watched", "Watching", "2025-03-01", "2025-04-01",
		`href="`+past+`/edit?return=%2Ftv%2Ftvdata%2F1"`, `action="`+past+`/delete"`, "hx-confirm=",
		`action="/list/`+idStr(id)+`/runs/`+runIDStr(runs[0])+`/delete"`)
	// the display run is edited in the entry dialog
	mustContain(t, b, `href="/list/`+idStr(id)+`/edit?return=%2Ftv%2Ftvdata%2F1"`)

	// the past run's form offers only completed and dropped
	d := e.do("GET", past+"/edit?return=%2Ftv%2Ftvdata%2F1", nil, true).Body.String()
	mustContain(t, d, `value="completed"`, `value="dropped"`, `name="review"`, `value="2025-03-01"`)
	mustNotContain(t, d, `value="active"`, `value="planned"`, `value="paused"`, "<html")
	full := e.get(past + "/edit")
	if full.Code != 200 {
		t.Fatal(full.Code)
	}
	mustContain(t, full.Body.String(), "<html", "History Show", `name="review"`)
	rec := e.do("POST", past+"/edit", url.Values{"status": {"active"}, "return": {"/tv/tvdata/1"}}, true)
	if rec.Code != 422 {
		t.Fatal(rec.Code)
	}
	rec = e.do("POST", past+"/edit", url.Values{"status": {"dropped"}, "rating": {"5"}, "review": {"fine"},
		"started_on": {"2025-03-02"}, "return": {"/tv/tvdata/1"}}, true)
	if rec.Code != 204 || rec.Header().Get("HX-Redirect") != "/tv/tvdata/1" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	runs = e.runsOf(t, id)
	if r := runs[1]; r.Status != library.Dropped || *r.Rating != 5 || r.Review != "fine" || fmtDay(r.StartedOn) != "2025-03-02" || r.FinishedOn != nil {
		t.Fatalf("edited run: %+v", r)
	}
	mustContain(t, e.get("/tv/tvdata/1").Body.String(), "fine")

	// delete the latest run (the undo of a start again)
	rec = e.do("POST", "/list/"+idStr(id)+"/runs/"+runIDStr(runs[0])+"/delete", url.Values{"return": {"/tv/tvdata/1"}}, false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/tv/tvdata/1" {
		t.Fatal(rec.Code, rec.Header())
	}
	runs = e.runsOf(t, id)
	if len(runs) != 1 {
		t.Fatalf("after delete: %+v", runs)
	}
	// the only run: not offered, and refused
	mustNotContain(t, e.get("/tv/tvdata/1").Body.String(), "/delete")
	before := dump(t, e.env)
	if rec := e.do("POST", "/list/"+idStr(id)+"/runs/"+runIDStr(runs[0])+"/delete", url.Values{}, false); rec.Code != 400 {
		t.Fatal(rec.Code)
	}
	if after := dump(t, e.env); after != before {
		t.Fatal("refused delete changed the database")
	}
}

func TestRunRoutesRefuseStaleAndForeignRuns(t *testing.T) {
	e := newFixtureEnv(t)
	id := completedShow(t, e.env, "1", "Mine")
	other := completedShow(t, e.env, "2", "Other")
	if _, err := e.lib.StartRun(context.Background(), other, *day(2026, 1, 1)); err != nil {
		t.Fatal(err)
	}
	foreign := runIDStr(e.runsOf(t, other)[1])
	before := dump(t, e.env)
	for _, run := range []string{"999999", foreign, "0", "abc", "-1"} {
		base := "/list/" + idStr(id) + "/runs/" + run
		if rec := e.get(base + "/edit"); rec.Code != 404 {
			t.Errorf("GET %s: %d", run, rec.Code)
		}
		if rec := e.do("POST", base+"/edit", url.Values{"status": {"dropped"}}, true); rec.Code != 404 {
			t.Errorf("POST edit %s: %d", run, rec.Code)
		}
		if rec := e.do("POST", base+"/delete", url.Values{}, true); rec.Code != 404 {
			t.Errorf("delete %s: %d", run, rec.Code)
		}
	}
	if rec := e.do("POST", "/list/999999/runs", url.Values{}, false); rec.Code != 404 {
		t.Errorf("start on a missing item: %d", rec.Code)
	}
	if after := dump(t, e.env); after != before {
		t.Fatal("refused run routes changed the database")
	}
}

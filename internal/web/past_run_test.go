package web

import (
	"context"
	"net/url"
	"testing"

	"github.com/allister/allister/internal/library"
)

// When the shown run is a past run (the completed run behind a dropped
// repeat), moving it back only offers starting again; a forged "correct"
// answer is refused and writes nothing.
func TestRunDialogOnAPastDisplayRunOnlyOffersStartingAgain(t *testing.T) {
	e := newFixtureEnv(t, repeatLabels)
	id := completedShow(t, e.env, "1", "Dropped Repeat")
	ctx := context.Background()
	run2, err := e.lib.StartRun(ctx, id, *day(2026, 1, 2))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.lib.UpdateRun(ctx, run2.ID, library.RunUpdate{Status: library.Dropped}); err != nil {
		t.Fatal(err)
	}
	edit := "/list/" + idStr(id) + "/edit"
	form := url.Values{"status": {"active"}, "rating": {"8"}, "progress.episode": {"10"}, "user_tag": {"forged"}, "return": {"/tv"}}
	before := dump(t, e.env)
	rec := e.do("POST", edit, form, true)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	mustContain(t, rec.Body.String(), `name="run_choice" value="new"`, "Watch again instead")
	mustNotContain(t, rec.Body.String(), `value="correct"`, "No, correct this one")
	form.Set("run_choice", "correct")
	rec = e.do("POST", edit, form, true)
	if rec.Code != 422 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	mustContain(t, rec.Body.String(), "can only be completed or dropped")
	if after := dump(t, e.env); after != before {
		t.Fatal("the refused answer changed the database")
	}
}

// A past display run of an item that is still airing offers neither answer:
// the question says so instead of asking to correct a run it cannot correct.
func TestRunDialogPastRunStillAiringHasNothingToAnswer(t *testing.T) {
	e := newFixtureEnv(t, repeatLabels)
	id := completedShow(t, e.env, "1", "Airing Past")
	ctx := context.Background()
	run2, err := e.lib.StartRun(ctx, id, *day(2026, 1, 2))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.lib.UpdateRun(ctx, run2.ID, library.RunUpdate{Status: library.Dropped}); err != nil {
		t.Fatal(err)
	}
	e.airing(t, id, *day(2999, 1, 1))
	form := url.Values{"status": {"active"}, "rating": {"8"}, "progress.episode": {"10"}, "return": {"/tv"}}
	rec := e.do("POST", "/list/"+idStr(id)+"/edit", form, true)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	b := rec.Body.String()
	mustContain(t, b, "Still airing: it can be started again once it has finished airing, and this earlier run stays finished behind the dropped repeat, so there is nothing to change here.")
	mustNotContain(t, b, "Correct the current run?", `name="run_choice"`, "No, correct this one", "Watch again instead")
	mustContain(t, b, "data-dialog-close") // Cancel stays
}

package web

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugins/anilist"
)

// --- entry notes, run reviews and unit notes ---

// noteShow lists an active tv show at episode 5 of 12.
func noteShow(t *testing.T, e *env, id string) (int64, library.Run) {
	t.Helper()
	item := e.addFixture(t, "tv", "show", tvWithEpisodes(id, "Notes Show "+id, 12), library.Entry{Status: library.Active,
		Progress: map[string]float64{"episode": 5}})
	return item, e.runsOf(t, item)[0]
}

func noteURLOf(item int64, run library.Run, key string, unit int) string {
	return "/list/" + idStr(item) + "/runs/" + runIDStr(run) + "/notes/" + key + "/" + strconv.Itoa(unit)
}

func editForm(notes, review string) url.Values {
	return url.Values{"status": {"active"}, "progress.episode": {"5"}, "comment": {notes}, "review": {review}, "return": {"/tv"}}
}

func TestEntryDialogSavesLongMultibyteNotesAndReview(t *testing.T) {
	e := newFixtureEnv(t)
	id, _ := noteShow(t, e.env, "1")
	long := strings.Repeat("😀", library.MaxNotesRunes)
	// 10,000 four-byte runes each, url-encoded about 120 KB per field
	rec := e.do("POST", "/list/"+idStr(id)+"/edit", editForm(long, long), true)
	if rec.Code != 204 {
		t.Fatalf("10,000 runes: %d %s", rec.Code, rec.Body.String())
	}
	if en := e.entryOf(t, id); en.Notes != long || en.Review != long {
		t.Fatalf("stored: %d / %d runes", len([]rune(en.Notes)), len([]rune(en.Review)))
	}
	for name, f := range map[string]url.Values{
		"notes":  editForm(long+"😀", ""),
		"review": editForm("", long+"😀"),
	} {
		rec := e.do("POST", "/list/"+idStr(id)+"/edit", f, true)
		if rec.Code != 422 {
			t.Fatalf("%s 10,001 runes: %d", name, rec.Code)
		}
		mustContain(t, rec.Body.String(), "longer than 10000 characters")
	}
	if en := e.entryOf(t, id); en.Notes != long || en.Review != long {
		t.Fatal("a refused save changed the entry")
	}
}

func TestAddAndRunFormsAcceptLongMultibyteText(t *testing.T) {
	e := newEnv(t)
	long := strings.Repeat("漢", library.MaxNotesRunes)
	f := okAddForm()
	f.Set("comment", strings.Repeat("😀", library.MaxNotesRunes))
	if rec := e.post(f, true); rec.Code != 204 {
		t.Fatalf("add: %d %s", rec.Code, rec.Body.String())
	}
	sv, err := findBySource(context.Background(), e.lib, anilist.AniList, "1535")
	if err != nil || sv == nil {
		t.Fatal(sv, err)
	}
	if en := e.entryOf(t, sv.ID); len([]rune(en.Notes)) != library.MaxNotesRunes {
		t.Fatalf("notes: %d runes", len([]rune(en.Notes)))
	}
	id := sv.ID
	run := e.runsOf(t, id)[0]
	rf := url.Values{"status": {"active"}, "review": {long}, "return": {"/anime"}}
	if rec := e.do("POST", "/list/"+idStr(id)+"/runs/"+runIDStr(run)+"/edit", rf, true); rec.Code != 204 {
		t.Fatalf("run edit: %d %s", rec.Code, rec.Body.String())
	}
	if got := e.runsOf(t, id)[0].Review; got != long {
		t.Fatalf("review: %d runes", len([]rune(got)))
	}
}

func TestEntryDialogCarriesTheReview(t *testing.T) {
	e := newFixtureEnv(t)
	id, run := noteShow(t, e.env, "2")
	if err := e.lib.UpdateRun(context.Background(), run.ID, library.RunUpdate{Status: library.Active, Review: "Kept **review**"}); err != nil {
		t.Fatal(err)
	}
	b := e.do("GET", "/list/"+idStr(id)+"/edit?return=/tv", nil, true).Body.String()
	mustContain(t, b, `name="review"`, "Kept **review**")
	// a save that does not touch the review (it posts the prefilled text) keeps it
	if rec := e.do("POST", "/list/"+idStr(id)+"/edit", editForm("new notes", "Kept **review**"), true); rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	// and a post without the field at all (an older form) keeps it too
	f := editForm("newer notes", "")
	f.Del("review")
	if rec := e.do("POST", "/list/"+idStr(id)+"/edit", f, true); rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if en := e.entryOf(t, id); en.Review != "Kept **review**" || en.Notes != "newer notes" {
		t.Fatalf("entry: %+v", en)
	}
	// the dialog edits it
	if rec := e.do("POST", "/list/"+idStr(id)+"/edit", editForm("newer notes", "changed"), true); rec.Code != 204 {
		t.Fatal(rec.Code)
	}
	if en := e.entryOf(t, id); en.Review != "changed" {
		t.Fatalf("review: %q", en.Review)
	}
}

func TestItemPageRendersFormattedNotesAndReview(t *testing.T) {
	e := newFixtureEnv(t)
	id, _ := noteShow(t, e.env, "3")
	ctx := context.Background()
	if err := e.lib.UpdateEntry(ctx, library.Entry{ItemID: id, Status: library.Active, Notes: "**bold** and ||secret||", Review: "*fine* <b>x</b>",
		Progress: map[string]float64{"episode": 5}}, nil); err != nil {
		t.Fatal(err)
	}
	b := e.get("/tv/tvdata/3").Body.String()
	mustContain(t, b, "<strong>bold</strong>", `<span class="spoiler" tabindex="0">secret</span>`, "<em>fine</em>", "&lt;b&gt;x&lt;/b&gt;")
	mustNotContain(t, b, "**bold**", "||secret||", "<b>x</b>")
}

func TestIncrementAnswerOffersAUnitNote(t *testing.T) {
	e := newFixtureEnv(t)
	id, run := noteShow(t, e.env, "4")
	rec := e.do("POST", "/list/"+idStr(id)+"/increment", url.Values{"return": {"/tv?view=table"}}, true)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	link := noteURLOf(id, run, "episode", 6)
	mustContain(t, rec.Body.String(), "Note on Episode 6", `href="`+link+`?return=`)
	// the card answer too
	rec = e.do("POST", "/list/"+idStr(id)+"/increment", url.Values{"return": {"/tv"}, "view": {"cards"}}, true)
	mustContain(t, rec.Body.String(), "data-card", "Note on Episode 7", noteURLOf(id, run, "episode", 7))
	// the list itself does not offer it
	mustNotContain(t, e.get("/tv?view=table").Body.String(), "Note on")
	// a book's primary pages track reads its own label
	book := e.addFixture(t, "book", "book", bookItem("4", "Paged"), library.Entry{Status: library.Active, Progress: map[string]float64{"pages": 1}})
	brun := e.runsOf(t, book)[0]
	rec = e.do("POST", "/list/"+idStr(book)+"/increment", url.Values{"return": {"/book"}}, true)
	mustContain(t, rec.Body.String(), "Note on Page 2", noteURLOf(book, brun, "pages", 2))
}

func TestUnitNoteForm(t *testing.T) {
	e := newFixtureEnv(t)
	id, run := noteShow(t, e.env, "5")
	u := noteURLOf(id, run, "episode", 6)
	rec := e.do("GET", u+"?return=/tv", nil, true)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	mustContain(t, rec.Body.String(), "Note on Episode 6", `name="body"`, `maxlength="2000"`, `action="`+u+`"`)
	mustNotContain(t, rec.Body.String(), "<html")
	// without JavaScript the item page opens with the form
	mustContain(t, e.get(u).Body.String(), "<html", "Note on Episode 6", `name="body"`)

	if rec := e.do("POST", u, url.Values{"body": {"  a **great** one  "}, "return": {"/tv"}}, true); rec.Code != 204 || rec.Header().Get("HX-Redirect") != "/tv" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	notes, err := e.lib.UnitNotes(context.Background(), run.ID)
	if err != nil || len(notes) != 1 || notes[0].Body != "a **great** one" || notes[0].Unit != 6 {
		t.Fatalf("notes = %+v, %v", notes, err)
	}
	// the form prefills the stored note
	mustContain(t, e.do("GET", u, nil, true).Body.String(), "a **great** one")

	long := strings.Repeat("😀", library.MaxUnitNoteRunes)
	if rec := e.do("POST", u, url.Values{"body": {long}}, true); rec.Code != 204 {
		t.Fatalf("2000 runes: %d", rec.Code)
	}
	for name, body := range map[string]string{"2001 runes": long + "x", "control": "a\x07b"} {
		rec := e.do("POST", u, url.Values{"body": {body}}, true)
		if rec.Code != 422 {
			t.Fatalf("%s: %d", name, rec.Code)
		}
		mustContain(t, rec.Body.String(), `role="alert"`, `name="body"`)
	}
	// an empty body deletes the note
	if rec := e.do("POST", u, url.Values{"body": {" "}}, true); rec.Code != 204 {
		t.Fatal(rec.Code)
	}
	if notes, _ := e.lib.UnitNotes(context.Background(), run.ID); len(notes) != 0 {
		t.Fatalf("not deleted: %+v", notes)
	}
}

func TestUnitNoteRefusals(t *testing.T) {
	e := newFixtureEnv(t)
	id, run := noteShow(t, e.env, "6")
	for _, u := range []string{
		noteURLOf(id, run, "position", 1),                     // a duration track
		noteURLOf(id, run, "nope", 1),                         // no such track
		noteURLOf(id, run, "episode", 0),                      // units start at 1
		noteURLOf(id, run, "episode", 13),                     // above the total of 12
		"/list/" + idStr(id) + "/runs/999999/notes/episode/1", // a foreign run
		"/list/" + idStr(id) + "/runs/" + runIDStr(run) + "/notes/episode/x",
	} {
		for _, method := range []string{"GET", "POST"} {
			var f url.Values
			if method == "POST" {
				f = url.Values{"body": {"x"}}
			}
			if rec := e.do(method, u, f, true); rec.Code != 404 {
				t.Errorf("%s %s: %d", method, u, rec.Code)
			}
		}
	}
	// a book's percent track takes no notes
	book := e.addFixture(t, "book", "book", bookItem("6", "Percent"), library.Entry{Status: library.Active})
	if rec := e.do("GET", noteURLOf(book, e.runsOf(t, book)[0], "percent", 1), nil, true); rec.Code != 404 {
		t.Errorf("percent: %d", rec.Code)
	}
	if notes, _ := e.lib.UnitNotes(context.Background(), run.ID); len(notes) != 0 {
		t.Fatalf("stored: %+v", notes)
	}
}

func TestTimelineShowsUnitNotes(t *testing.T) {
	e := newFixtureEnv(t)
	id, run := noteShow(t, e.env, "7")
	base := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	seedRunEvent(t, e.env, id, run.ID, base, "progress", "episode", num(4), num(5), "", "", "manual")
	seedRunEvent(t, e.env, id, run.ID, base.Add(time.Hour), "progress", "episode", num(5), num(6), "", "", "manual")
	if err := e.lib.SetUnitNote(context.Background(), run.ID, "episode", 5, "**loved** it"); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.SetUnitNote(context.Background(), run.ID, "episode", 9, "ahead"); err != nil {
		t.Fatal(err)
	}
	b := e.do("GET", runTimelineURL(id, run), nil, true).Body.String()
	mustContain(t, b, "Note on Episode 5", "<strong>loved</strong> it", "Note on Episode 9", "ahead",
		`href="`+noteURLOf(id, run, "episode", 5)+`?return=`, // edit the note
		`href="`+noteURLOf(id, run, "episode", 6)+`?return=`) // add one on a logged unit
	mustNotContain(t, b, "**loved**")
	if i, j := strings.Index(b, "Note on Episode 5"), strings.Index(b, "Episode 6"); i < 0 || j < i {
		t.Fatalf("the note is not at its unit:\n%s", b)
	}
}

// crlfText is a text of n runes once its line breaks are LF, written with
// CRLF breaks as browsers submit a textarea (so it has more runes as sent),
// and that LF form.
func crlfText(n int) (sent, stored string) {
	line := strings.Repeat("a", 100)
	breaks := (n - 1) / 101
	stored = strings.Repeat(line+"\n", breaks) + strings.Repeat("a", n-breaks*101)
	return strings.ReplaceAll(stored, "\n", "\r\n"), stored
}

// Line breaks count once against the rune limits: a CRLF (or lone CR) is
// stored as LF, through the entry dialog, the run edit, the unit-note form
// and the manual form.
func TestTextFormsStoreLFLineBreaks(t *testing.T) {
	e := newFixtureEnv(t)
	sent, stored := crlfText(library.MaxNotesRunes)
	if len([]rune(stored)) != library.MaxNotesRunes || len([]rune(sent)) <= library.MaxNotesRunes {
		t.Fatalf("fixture: %d / %d runes", len([]rune(stored)), len([]rune(sent)))
	}
	id, run := noteShow(t, e.env, "8")
	if rec := e.do("POST", "/list/"+idStr(id)+"/edit", editForm(sent, strings.ReplaceAll(sent, "\r\n", "\r")), true); rec.Code != 204 {
		t.Fatalf("entry dialog: %d %s", rec.Code, rec.Body.String())
	}
	if en := e.entryOf(t, id); en.Notes != stored || en.Review != stored {
		t.Fatalf("entry dialog stored CRs: %t / %t", strings.Contains(en.Notes, "\r"), strings.Contains(en.Review, "\r"))
	}
	rf := url.Values{"status": {"active"}, "review": {sent + "\r\n"}, "return": {"/tv"}}
	if rec := e.do("POST", "/list/"+idStr(id)+"/runs/"+runIDStr(run)+"/edit", rf, true); rec.Code != 204 {
		t.Fatalf("run edit: %d %s", rec.Code, rec.Body.String())
	}
	if got := e.runsOf(t, id)[0].Review; got != stored {
		t.Fatalf("run edit stored CRs: %t", strings.Contains(got, "\r"))
	}
	usent, ustored := crlfText(library.MaxUnitNoteRunes)
	if rec := e.do("POST", noteURLOf(id, run, "episode", 6), url.Values{"body": {usent}}, true); rec.Code != 204 {
		t.Fatalf("unit note: %d %s", rec.Code, rec.Body.String())
	}
	if notes, err := e.lib.UnitNotes(context.Background(), run.ID); err != nil || len(notes) != 1 || notes[0].Body != ustored {
		t.Fatalf("unit note: %d notes, %v", len(notes), err)
	}
	if rec := e.postMultipart(t, "/tv/manual/new", url.Values{"title": {"Hand Made"}, "status": {"planned"}, "comment": {sent}}, nil); rec.Code != 303 {
		t.Fatalf("manual: %d %s", rec.Code, rec.Body.String())
	}
	var manual int64
	if err := e.d.QueryRow(`SELECT max(id) FROM items`).Scan(&manual); err != nil {
		t.Fatal(err)
	}
	if en := e.entryOf(t, manual); en.Notes != stored {
		t.Fatalf("manual stored CRs: %t (%d runes)", strings.Contains(en.Notes, "\r"), len([]rune(en.Notes)))
	}
}

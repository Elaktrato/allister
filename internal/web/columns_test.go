package web

import (
	"context"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
	"github.com/allister/allister/internal/plugins/anilist"
)

var thLabelRe = regexp.MustCompile(`(?s)<th scope="col"[^>]*>(.*?)</th>`)
var tagRe = regexp.MustCompile(`<[^>]+>`)

// headLabels returns the visible labels of the table head, in order
// (the cover and actions columns are screen-reader only and skipped).
func headLabels(t *testing.T, body string) []string {
	t.Helper()
	var out []string
	for _, m := range thLabelRe.FindAllStringSubmatch(thead(t, body), -1) {
		if strings.Contains(m[1], `class="sr-only"`) {
			continue
		}
		label := strings.TrimSpace(tagRe.ReplaceAllString(m[1], ""))
		// drop the sort icon ligature names
		for _, icon := range []string{"unfold_more", "arrow_upward", "arrow_downward"} {
			label = strings.TrimSpace(strings.TrimSuffix(label, icon))
		}
		out = append(out, label)
	}
	return out
}

// rowCells returns the number of <td> cells of a row.
func rowCells(row string) int { return strings.Count(row, "<td") }

// TestDefaultColumnsReproduceTheAnimeTable: the anime plugin declares its
// columns; the defaults are the table it always had, with the same
// screen-width hints (Type from sm, Year from md, Updated from lg).
func TestDefaultColumnsReproduceTheAnimeTable(t *testing.T) {
	e := newEnv(t)
	e.addEntry(t, anilist.AniList, "1", ip(12), library.Active, 3)
	b := e.get("/anime?view=table").Body.String()
	if got, want := strings.Join(headLabels(t, b), "|"), "Title|Type|Year|Progress|Score|Status|Updated"; got != want {
		t.Fatalf("head = %s, want %s", got, want)
	}
	head := thead(t, b)
	mustContain(t, headCell(t, head, "Type"), `hidden py-2 pr-3 font-medium sm:table-cell`)
	mustContain(t, headCell(t, head, "Year"), `class="hidden py-2 pr-3 font-medium md:table-cell"`, `id="sort-year"`)
	mustContain(t, headCell(t, head, "Updated"), `class="hidden py-2 pr-3 font-medium lg:table-cell"`, `id="sort-updated"`)
	mustNotContain(t, head, ">Episodes<", ">Studio<", ">Source<", ">Started<", ">Ended<")
	row := rowOf(t, b, "Title 1")
	if n := rowCells(row); n != 9 { // cover, title, the six columns, actions
		t.Errorf("row has %d cells: %s", n, row)
	}
	mustContain(t, row, `<td class="hidden py-2 pr-3 text-slate-300 sm:table-cell">TV</td>`, "3 / 12", `data-score="none"`,
		`<td class="py-2 pr-3 whitespace-nowrap">Watching</td>`, `<td class="hidden py-2 pr-3 whitespace-nowrap text-slate-400 lg:table-cell">`)
}

// bookColumns makes the book fixture declare its own table: author and the
// page count (as "Length", from md) and the status by default; score and
// updated offered but hidden; no progress column at all.
func bookColumns(book, _ *plugintest.Plugin) {
	k := &book.M.Kinds[0]
	k.Columns = []plugin.ColumnDef{
		{Key: "author", Default: true},
		{Key: "pages", Label: "Length", Default: true, Hide: "md"},
		{Key: "status", Default: true},
		{Key: "rating"},
		{Key: "updated", Hide: "lg"},
	}
}

// TestDeclaredColumnsDriveTheTable: a book-like plugin shows the columns it
// declares, in its order, next to an unchanged anime table; sort links only
// for visible sortable columns, while the compact sort control keeps every
// sort key; the rows partial (head and rows) follows too.
func TestDeclaredColumnsDriveTheTable(t *testing.T) {
	e := newFixtureEnv(t, bookColumns)
	d := bookItem("1", "Earthsea")
	d.Fields["pages"] = plugin.FieldValue{Num: num(183)}
	e.addFixture(t, "book", "book", d, library.Entry{Status: library.Active, Rating: ip(8)})
	e.addEntry(t, anilist.AniList, "1", ip(12), library.Active, 3)

	b := e.listGet(t, "/book?view=table&view=table")
	if got, want := strings.Join(headLabels(t, b), "|"), "Title|Author|Length|Status"; got != want {
		t.Fatalf("book head = %s, want %s", got, want)
	}
	row := rowOf(t, b, "Earthsea")
	mustContain(t, row, ">Ursula K. Le Guin</td>", ">183</td>", ">Reading</td>")
	mustNotContain(t, row, "data-score", "<time", "/increment")
	if n := rowCells(row); n != 6 { // cover, title, author, length, status, actions
		t.Errorf("book row has %d cells: %s", n, row)
	}
	mustContain(t, headCell(t, thead(t, b), "Length"), `class="hidden py-2 pr-3 font-medium md:table-cell"`)
	// sort links: title only (pages is not sortable; rating, progress and
	// updated sort but are not shown)
	head := thead(t, b)
	mustContain(t, head, `id="sort-title"`)
	mustNotContain(t, head, `id="sort-rating"`, `id="sort-progress"`, `id="sort-updated"`, `id="sort-pages"`)
	mustNotContain(t, headCell(t, head, "Length"), "<a ")
	// the compact sort control still offers every sort key
	ctl := filterForm(t, b)
	mustContain(t, ctl, `<option value="updated" selected>Recently updated</option>`, `<option value="title">Title</option>`,
		`<option value="rating">Score</option>`, `<option value="progress">Progress</option>`)
	// sorting by a hidden column still works (and marks no head cell)
	sorted := e.listGet(t, "/book?sort=rating&view=table")
	mustNotContain(t, thead(t, sorted), "aria-sort")
	if n := noticeOf(sorted); n != "" {
		t.Errorf("notice %q", n)
	}
	// the rows partial brings the same head and cells
	p := e.getWith("/book?q=earth&view=table", map[string]string{"HX-Request": "true", "HX-Target": "list-rows"}).Body.String()
	if got := strings.Join(headLabels(t, p), "|"); got != "Title|Author|Length|Status" {
		t.Fatalf("partial head = %s", got)
	}
	mustContain(t, rowOf(t, p, "Earthsea"), ">183</td>")

	// the anime table next to it is unchanged
	if got := strings.Join(headLabels(t, e.listGet(t, "/anime?view=table&view=table")), "|"); got != "Title|Type|Year|Progress|Score|Status|Updated" {
		t.Fatalf("anime head = %s", got)
	}
}

// TestUndeclaredColumnsKeepTheDerivedTable: a kind without Columns keeps
// the table derived from its schema (the type column, the sortable fields,
// progress, score, status, updated), as before columns existed.
func TestUndeclaredColumnsKeepTheDerivedTable(t *testing.T) {
	e := newFixtureEnv(t)
	e.addFixture(t, "tv", "show", tvShow("1", "Dark"), library.Entry{Status: library.Active})
	e.addFixture(t, "book", "book", bookItem("1", "Earthsea"), library.Entry{Status: library.Active})
	if got := strings.Join(headLabels(t, e.listGet(t, "/tv?view=table&view=table")), "|"); got != "Title|Type|Year|Progress|Score|Status|Updated" {
		t.Fatalf("tv head = %s", got)
	}
	if got := strings.Join(headLabels(t, e.listGet(t, "/book?view=table&view=table")), "|"); got != "Title|Author|Progress|Score|Status|Updated" {
		t.Fatalf("book head = %s", got)
	}
}

// postColumns posts the column form of plugin id.
func (e *env) postColumns(id string, v url.Values, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/settings/plugins/"+id+"/columns", strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, val := range headers {
		req.Header.Set(k, val)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// animeOrder is every column the anime plugin offers, in its order.
var animeOrder = []string{"type", "year", "progress", "rating", "status", "updated", "episodes", "studio", "source_material", "started_on", "ended_on"}

// columnForm is the columns form of kind kind on a plugin settings page.
func columnForm(t *testing.T, body, kind string) string {
	t.Helper()
	i := strings.Index(body, `<form method="post" action="/settings/plugins/`)
	for i >= 0 {
		end := strings.Index(body[i:], "</form>")
		f := body[i : i+end]
		if strings.Contains(f, `name="kind" value="`+kind+`"`) {
			return f
		}
		next := strings.Index(body[i+end:], `<form method="post" action="/settings/plugins/`)
		if next < 0 {
			break
		}
		i += end + next
	}
	t.Fatalf("no columns form for %s in %s", kind, body)
	return ""
}

// TestColumnSettingsForm: the plugin's settings page lists every offered
// column with a checkbox (the defaults checked, in order, the others after
// them), the title as always shown, move buttons for the shown columns and
// Reset to defaults; it is a plain form.
func TestColumnSettingsForm(t *testing.T) {
	e := newEnv(t)
	b := e.get("/settings/plugins/anime").Body.String()
	f := columnForm(t, b, "anime")
	mustContain(t, f, `action="/settings/plugins/anime/columns"`, `type="checkbox" checked disabled`, ">Title<",
		`name="reset" value="1"`, "Reset to defaults", `>Save columns<`)
	last := -1
	for _, key := range animeOrder {
		i := strings.Index(f, `name="order" value="`+key+`"`)
		if i < 0 || i < last {
			t.Errorf("column %s missing or out of order", key)
		}
		last = i
		box := `name="show" value="` + key + `"`
		mustContain(t, f, box)
		shown := slices.Contains(animeOrder[:6], key)
		if got := strings.Contains(f, box+" checked"); got != shown {
			t.Errorf("%s checked = %v", key, got)
		}
		if got := strings.Contains(f, `value="up:`+key+`"`); got != (shown && key != "type") {
			t.Errorf("%s up button = %v", key, got)
		}
		if got := strings.Contains(f, `value="down:`+key+`"`); got != (shown && key != "updated") {
			t.Errorf("%s down button = %v", key, got)
		}
	}
	mustContain(t, f, ">Episodes<", ">Studio<", ">Started<", `aria-label="Move Year up"`)
	mustIcon(t, f, "arrow_upward")
	mustIcon(t, f, "arrow_downward")
	mustNotContain(t, f, "hx-post", "<script")
}

// TestPostColumnsChangesTheList: a saved choice (a plain POST, the page's
// Save button) is what the list shows: head, sort links (only visible
// sortable columns), rows, the rows partial and the +1 row; the compact
// sort control still offers every sort key.
func TestPostColumnsChangesTheList(t *testing.T) {
	e := newEnv(t)
	a := anilist.Media{SearchResult: anilist.SearchResult{Source: anilist.AniList, ID: "1", Episodes: ip(12), Type: "TV",
		Titles: titleEn("Title 1"), Studio: "Madhouse"}}
	id, err := addToList(context.Background(), e.lib, a, listEntry{Status: library.Active, Progress: 3})
	if err != nil {
		t.Fatal(err)
	}
	rec := e.postColumns("anime", url.Values{"kind": {"anime"}, "order": {"studio", "rating", "type", "year", "progress", "status", "updated"},
		"show": {"studio", "type", "rating"}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/settings/plugins/anime#columns-anime" {
		t.Fatalf("post: %d %q %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if got, ok := e.set.Columns(context.Background(), "anime", "anime"); !ok || strings.Join(got, ",") != "studio,rating,type" {
		t.Fatalf("stored %v %v", got, ok)
	}
	b := e.listGet(t, "/anime?view=table&view=table")
	if got := strings.Join(headLabels(t, b), "|"); got != "Title|Studio|Score|Type" {
		t.Fatalf("head = %s", got)
	}
	head := thead(t, b)
	mustContain(t, head, `id="sort-title"`, `id="sort-rating"`)
	mustNotContain(t, head, `id="sort-year"`, `id="sort-progress"`, `id="sort-updated"`)
	row := rowOf(t, b, "Title 1")
	mustContain(t, row, ">Madhouse</td>", `data-score="none"`, ">TV</td>")
	mustNotContain(t, row, "3 / 12", "Watching", "<time")
	if n := rowCells(row); n != 6 {
		t.Errorf("row cells %d", n)
	}
	ctl := filterForm(t, b)
	mustContain(t, ctl, `<option value="year">Year</option>`, `<option value="progress">Progress</option>`, `<option value="updated" selected>Recently updated</option>`)
	p := e.getWith("/anime?sort=year&view=table", map[string]string{"HX-Request": "true", "HX-Target": "list-rows"}).Body.String()
	if got := strings.Join(headLabels(t, p), "|"); got != "Title|Studio|Score|Type" {
		t.Fatalf("partial head = %s", got)
	}
	mustNotContain(t, thead(t, p), "aria-sort") // sorted by a hidden column
	// the +1 row has the same cells (no progress column: no +1 button)
	inc := e.do("POST", "/list/"+idStr(id)+"/increment", url.Values{"return": {"/anime"}}, true)
	if inc.Code != 200 {
		t.Fatalf("+1: %d", inc.Code)
	}
	if n := rowCells(inc.Body.String()); n != 6 {
		t.Errorf("+1 row cells %d: %s", n, inc.Body.String())
	}
	mustContain(t, inc.Body.String(), ">Madhouse</td>")

	// nothing shown but the title
	if rec := e.postColumns("anime", url.Values{"kind": {"anime"}, "order": animeOrder}, nil); rec.Code != 303 {
		t.Fatalf("empty: %d", rec.Code)
	}
	if got := strings.Join(headLabels(t, e.listGet(t, "/anime?view=table&view=table")), "|"); got != "Title" {
		t.Fatalf("title only: %s", got)
	}
}

// TestPostColumnsMoveAndReset: the move buttons reorder the shown columns
// (saving the checkboxes as they are), Reset to defaults removes the choice.
func TestPostColumnsMoveAndReset(t *testing.T) {
	e := newEnv(t)
	e.addEntry(t, anilist.AniList, "1", ip(12), library.Active, 3)
	form := url.Values{"kind": {"anime"}, "order": animeOrder, "show": {"type", "year", "rating", "studio"}}
	form.Set("move", "up:rating")
	if rec := e.postColumns("anime", form, nil); rec.Code != 303 {
		t.Fatalf("move: %d", rec.Code)
	}
	if got, _ := e.set.Columns(context.Background(), "anime", "anime"); strings.Join(got, ",") != "type,rating,year,studio" {
		t.Fatalf("after up: %v", got)
	}
	form = url.Values{"kind": {"anime"}, "order": {"type", "rating", "year", "studio"}, "show": {"type", "rating", "year", "studio"}, "move": {"down:type"}}
	e.postColumns("anime", form, nil)
	if got, _ := e.set.Columns(context.Background(), "anime", "anime"); strings.Join(got, ",") != "rating,type,year,studio" {
		t.Fatalf("after down: %v", got)
	}
	// the first cannot go up, the last cannot go down: unchanged
	form.Set("order", "rating")
	form["order"] = []string{"rating", "type", "year", "studio"}
	form.Set("move", "up:rating")
	e.postColumns("anime", form, nil)
	form.Set("move", "down:studio")
	e.postColumns("anime", form, nil)
	if got, _ := e.set.Columns(context.Background(), "anime", "anime"); strings.Join(got, ",") != "rating,type,year,studio" {
		t.Fatalf("edges: %v", got)
	}
	mustContain(t, e.get("/settings/plugins/anime").Body.String(), `name="show" value="studio" checked`)
	if rec := e.postColumns("anime", url.Values{"kind": {"anime"}, "reset": {"1"}}, nil); rec.Code != 303 {
		t.Fatalf("reset: %d", rec.Code)
	}
	if _, ok := e.set.Columns(context.Background(), "anime", "anime"); ok {
		t.Fatal("reset kept the choice")
	}
	if got := strings.Join(headLabels(t, e.listGet(t, "/anime?view=table&view=table")), "|"); got != "Title|Type|Year|Progress|Score|Status|Updated" {
		t.Fatalf("after reset: %s", got)
	}
}

// TestPostColumnsRefusesUnknownValues: every value is checked against the
// columns the kind offers; the title cannot be switched; a refused or
// cross-site request stores nothing.
func TestPostColumnsRefusesUnknownValues(t *testing.T) {
	e := newEnv(t)
	for name, v := range map[string]url.Values{
		"title":          {"kind": {"anime"}, "order": animeOrder, "show": {"title"}},
		"unknown show":   {"kind": {"anime"}, "order": animeOrder, "show": {"bogus"}},
		"unknown order":  {"kind": {"anime"}, "order": append([]string{"bogus"}, animeOrder...)},
		"duplicate":      {"kind": {"anime"}, "order": append([]string{"year"}, animeOrder...)},
		"unknown kind":   {"kind": {"book"}, "order": animeOrder},
		"no kind":        {"order": animeOrder},
		"unknown move":   {"kind": {"anime"}, "order": animeOrder, "show": {"year"}, "move": {"up:bogus"}},
		"bad move":       {"kind": {"anime"}, "order": animeOrder, "show": {"year"}, "move": {"sideways:year"}},
		"hostile":        {"kind": {"anime"}, "order": animeOrder, "show": {"<script>"}},
		"show not order": {"kind": {"anime"}, "order": {"type"}, "show": {"year"}},
	} {
		rec := e.postColumns("anime", v, nil)
		if rec.Code != 400 {
			t.Errorf("%s: %d", name, rec.Code)
		}
		mustNotContain(t, rec.Body.String(), "<script>")
	}
	if rec := e.postColumns("nope", url.Values{"kind": {"anime"}}, nil); rec.Code != 404 {
		t.Errorf("unknown plugin: %d", rec.Code)
	}
	for _, h := range []map[string]string{{"Sec-Fetch-Site": "cross-site"}, {"Origin": "http://evil.example"}} {
		if rec := e.postColumns("anime", url.Values{"kind": {"anime"}, "order": animeOrder, "show": {"year"}}, h); rec.Code != 403 {
			t.Errorf("%v: %d", h, rec.Code)
		}
	}
	if got, ok := e.set.Columns(context.Background(), "anime", "anime"); ok {
		t.Fatalf("stored %v", got)
	}
	// same origin is fine
	if rec := e.postColumns("anime", url.Values{"kind": {"anime"}, "order": animeOrder, "show": {"year"}}, map[string]string{"Sec-Fetch-Site": "same-origin"}); rec.Code != 303 {
		t.Fatalf("same-origin: %d", rec.Code)
	}
}

// TestStoredColumnsAreCheckedOnRead: a stored list keeps only the columns
// the kind offers (unknown keys and repeats dropped), in its order.
func TestStoredColumnsAreCheckedOnRead(t *testing.T) {
	e := newEnv(t)
	e.addEntry(t, anilist.AniList, "1", ip(12), library.Active, 3)
	if err := e.set.Set(context.Background(), "plugin.anime.columns.anime", `["bogus","year","title","year","type"]`); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(headLabels(t, e.listGet(t, "/anime?view=table&view=table")), "|"); got != "Title|Year|Type" {
		t.Fatalf("head = %s", got)
	}
	// unreadable JSON: the defaults
	e.set.Set(context.Background(), "plugin.anime.columns.anime", `{`)
	if got := strings.Join(headLabels(t, e.listGet(t, "/anime?view=table&view=table")), "|"); got != "Title|Type|Year|Progress|Score|Status|Updated" {
		t.Fatalf("garbage: %s", got)
	}
}

// TestColumnChoiceIsPerPlugin: changing anime's columns leaves the book
// plugin's table (and its setting) alone, and the other way round.
func TestColumnChoiceIsPerPlugin(t *testing.T) {
	e := newFixtureEnv(t, bookColumns)
	e.addFixture(t, "book", "book", bookItem("1", "Earthsea"), library.Entry{Status: library.Active})
	e.addEntry(t, anilist.AniList, "1", ip(12), library.Active, 3)
	if rec := e.postColumns("anime", url.Values{"kind": {"anime"}, "order": animeOrder, "show": {"studio"}}, nil); rec.Code != 303 {
		t.Fatalf("anime: %d", rec.Code)
	}
	if got := strings.Join(headLabels(t, e.listGet(t, "/book?view=table&view=table")), "|"); got != "Title|Author|Length|Status" {
		t.Fatalf("book head = %s", got)
	}
	if _, ok := e.set.Columns(context.Background(), "book", "book"); ok {
		t.Fatal("book setting written")
	}
	// the book form offers book's columns only
	f := columnForm(t, e.get("/settings/plugins/book").Body.String(), "book")
	mustContain(t, f, `name="show" value="author" checked`, `name="show" value="pages" checked`, `name="show" value="rating"`, ">Length<")
	mustNotContain(t, f, `value="studio"`, `value="year"`, `value="progress"`)
	if rec := e.postColumns("book", url.Values{"kind": {"book"}, "order": {"rating", "author", "pages", "status", "updated"}, "show": {"rating", "author"}}, nil); rec.Code != 303 {
		t.Fatalf("book: %d", rec.Code)
	}
	if rec := e.postColumns("book", url.Values{"kind": {"book"}, "order": {"studio"}, "show": {"studio"}}, nil); rec.Code != 400 {
		t.Fatalf("anime column for book: %d", rec.Code)
	}
	if got := strings.Join(headLabels(t, e.listGet(t, "/book?view=table&view=table")), "|"); got != "Title|Score|Author" {
		t.Fatalf("book head = %s", got)
	}
	if got := strings.Join(headLabels(t, e.listGet(t, "/anime?view=table&view=table")), "|"); got != "Title|Studio" {
		t.Fatalf("anime head = %s", got)
	}
}

var submitRe = regexp.MustCompile(`<button[^>]*type="submit"[^>]*>`)

// TestColumnFormEnterSavesWithoutMoving: implicit submission (Enter on a
// checkbox) uses a form's first submit button, so in every form of the
// settings page that button has no name (a plain save, never a move or
// reset); and a submit without a move field keeps the order as shown.
func TestColumnFormEnterSavesWithoutMoving(t *testing.T) {
	e := newFixtureEnv(t, bookColumns)
	for _, id := range []string{"anime", "book", "tv"} {
		b := e.get("/settings/plugins/" + id).Body.String()
		forms := strings.Split(b, "<form")[1:]
		if len(forms) < 2 {
			t.Fatalf("%s: %d forms", id, len(forms))
		}
		for i, f := range forms {
			f = f[:strings.Index(f, "</form>")]
			first := submitRe.FindString(f)
			if first == "" || strings.Contains(first, "name=") {
				t.Errorf("%s form %d: first submit is %q", id, i, first)
			}
		}
	}
	// the rendered anime form, submitted as a browser does with Enter
	if rec := e.postColumns("anime", url.Values{"kind": {"anime"}, "order": animeOrder, "show": {"year", "rating", "type"}}, nil); rec.Code != 303 {
		t.Fatalf("save: %d", rec.Code)
	}
	f := columnForm(t, e.get("/settings/plugins/anime").Body.String(), "anime")
	v := url.Values{"kind": {"anime"}}
	for _, m := range regexp.MustCompile(`name="order" value="([^"]+)"`).FindAllStringSubmatch(f, -1) {
		v.Add("order", m[1])
	}
	for _, m := range regexp.MustCompile(`name="show" value="([^"]+)" checked`).FindAllStringSubmatch(f, -1) {
		v.Add("show", m[1])
	}
	if rec := e.postColumns("anime", v, nil); rec.Code != 303 {
		t.Fatalf("resubmit: %d", rec.Code)
	}
	if got, _ := e.set.Columns(context.Background(), "anime", "anime"); strings.Join(got, ",") != "type,year,rating" {
		t.Fatalf("order changed: %v", got)
	}
}

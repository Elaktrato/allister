package web

import (
	"regexp"
	"strings"
	"testing"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
	"github.com/allister/allister/internal/plugins/anime/anilist"
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
	b := e.get("/anime").Body.String()
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

	b := e.listGet(t, "/book")
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
	sorted := e.listGet(t, "/book?sort=rating")
	mustNotContain(t, thead(t, sorted), "aria-sort")
	if n := noticeOf(sorted); n != "" {
		t.Errorf("notice %q", n)
	}
	// the rows partial brings the same head and cells
	p := e.getWith("/book?q=earth", map[string]string{"HX-Request": "true", "HX-Target": "list-rows"}).Body.String()
	if got := strings.Join(headLabels(t, p), "|"); got != "Title|Author|Length|Status" {
		t.Fatalf("partial head = %s", got)
	}
	mustContain(t, rowOf(t, p, "Earthsea"), ">183</td>")

	// the anime table next to it is unchanged
	if got := strings.Join(headLabels(t, e.listGet(t, "/anime")), "|"); got != "Title|Type|Year|Progress|Score|Status|Updated" {
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
	if got := strings.Join(headLabels(t, e.listGet(t, "/tv")), "|"); got != "Title|Type|Year|Progress|Score|Status|Updated" {
		t.Fatalf("tv head = %s", got)
	}
	if got := strings.Join(headLabels(t, e.listGet(t, "/book")), "|"); got != "Title|Author|Progress|Score|Status|Updated" {
		t.Fatalf("book head = %s", got)
	}
}

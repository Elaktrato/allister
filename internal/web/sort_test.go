package web

import (
	"html"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// thead returns the table head of a list body.
func thead(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, "<thead")
	if i < 0 {
		t.Fatalf("no table head in %s", body)
	}
	return body[i : i+strings.Index(body[i:], "</thead>")]
}

// headCell returns the <th>...</th> of the head whose visible label is label.
func headCell(t *testing.T, head, label string) string {
	t.Helper()
	for _, part := range strings.Split(head, "<th")[1:] {
		cell := "<th" + part[:strings.Index(part, "</th>")+len("</th>")]
		if strings.Contains(cell, ">"+label+"<") {
			return cell
		}
	}
	t.Fatalf("no head cell %q in %s", label, head)
	return ""
}

// listHref is the escaped href of /tv with vals (status=active kept).
func listHref(path string, vals url.Values) string {
	if len(vals) == 0 {
		return html.EscapeString(path)
	}
	return html.EscapeString(path + "?" + vals.Encode())
}

func sortEnv(t *testing.T) *fixtureEnv {
	e := newFixtureEnv(t)
	active := library.Entry{Status: library.Active}
	for _, s := range []struct {
		id, title string
		year      float64
	}{{"1", "Dark", 2017}, {"2", "Dune Days", 2001}, {"3", "Deep", 2010}} {
		d := tvShow(s.id, s.title)
		d.Fields["year"] = plugin.FieldValue{Num: num(s.year)}
		e.addFixture(t, "tv", "show", d, active)
	}
	return e
}

func TestSortHeaderLinks(t *testing.T) {
	e := sortEnv(t)
	base := url.Values{"q": {"d"}, "f.year": {"2000..2030"}, "status": {"active"}}
	with := func(kv ...string) url.Values {
		v := url.Values{}
		for k, vals := range base {
			v[k] = vals
		}
		for i := 0; i < len(kv); i += 2 {
			v.Set(kv[i], kv[i+1])
		}
		return v
	}
	for _, c := range []struct {
		query  string      // sort state of the request
		active string      // label of the sorted column
		dir    string      // its aria-sort
		links  [][2]string // label -> expected link query (sort and dir pairs)
	}{
		{"", "Updated", "descending", [][2]string{{"Title", "sort=title"}, {"Year", "sort=year"}, {"Progress", "sort=progress"}, {"Score", "sort=rating"}, {"Updated", "sort=updated&dir=asc"}}},
		{"sort=title", "Title", "ascending", [][2]string{{"Title", "sort=title&dir=desc"}, {"Year", "sort=year"}, {"Updated", ""}}},
		{"sort=title&dir=desc", "Title", "descending", [][2]string{{"Title", "sort=title"}}},
		{"sort=year&dir=asc", "Year", "ascending", [][2]string{{"Year", "sort=year"}, {"Score", "sort=rating"}}},
		{"sort=year", "Year", "descending", [][2]string{{"Year", "sort=year&dir=asc"}}},
		{"sort=rating", "Score", "descending", [][2]string{{"Score", "sort=rating&dir=asc"}}},
		{"sort=progress&dir=asc", "Progress", "ascending", [][2]string{{"Progress", "sort=progress"}}},
		{"sort=updated&dir=asc", "Updated", "ascending", [][2]string{{"Updated", ""}, {"Title", "sort=title"}}},
	} {
		q := with()
		extra, _ := url.ParseQuery(c.query)
		for k, v := range extra {
			q[k] = v
		}
		q.Set("page", "3") // clamped to page 1; the links never carry a page
		b := e.listGet(t, "/tv?"+q.Encode())
		head := thead(t, b)
		if n := strings.Count(head, "aria-sort="); n != 1 {
			t.Errorf("%q: %d aria-sort in %s", c.query, n, head)
		}
		cell := headCell(t, head, c.active)
		mustContain(t, cell, `aria-sort="`+c.dir+`"`)
		arrow := map[string]string{"ascending": "arrow_upward", "descending": "arrow_downward"}[c.dir]
		mustIcon(t, cell, arrow)
		for _, l := range c.links {
			want := with()
			sv, _ := url.ParseQuery(l[1])
			for k, v := range sv {
				want[k] = v
			}
			want.Set("view", "cards") // the links keep the resolved view
			href := listHref("/tv", want)
			cell := headCell(t, head, l[0])
			mustContain(t, cell, `href="`+href+`"`, `hx-get="`+href+`"`, `hx-target="#list-rows"`, `hx-push-url="true"`,
				`hx-sync="#list-filter:replace"`, `hx-include="#list-filter"`)
			mustNotContain(t, cell, "page=")
		}
		// non-sortable columns are plain text
		for _, label := range []string{"Type", "Status"} {
			mustNotContain(t, headCell(t, head, label), "<a ", "aria-sort")
		}
	}
	// stable ids keep keyboard focus on the clicked header across the swap
	head := thead(t, e.listGet(t, "/tv"))
	mustContain(t, head, `id="sort-title"`, `id="sort-year"`, `id="sort-progress"`, `id="sort-rating"`, `id="sort-updated"`)
}

// TestSortHeaderWorksWithoutJavaScript: a header link is a plain GET of the
// list sorted by that column.
func TestSortHeaderWorksWithoutJavaScript(t *testing.T) {
	e := sortEnv(t)
	b := e.listGet(t, "/tv?q=d")
	link := regexp.MustCompile(`<a id="sort-title" href="([^"]+)"`).FindStringSubmatch(b)
	if link == nil {
		t.Fatalf("no title link in %s", thead(t, b))
	}
	asc := e.listGet(t, html.UnescapeString(link[1]))
	wantOrder(t, "title asc", asc, "Dark", "Deep", "Dune Days")
	link = regexp.MustCompile(`<a id="sort-title" href="([^"]+)"`).FindStringSubmatch(asc)
	wantOrder(t, "title desc", e.listGet(t, html.UnescapeString(link[1])), "Dune Days", "Deep", "Dark")
	year := regexp.MustCompile(`<a id="sort-year" href="([^"]+)"`).FindStringSubmatch(b)
	wantOrder(t, "year desc", e.listGet(t, html.UnescapeString(year[1])), "Dark", "Deep", "Dune Days")
	// the cells of the extra columns
	mustContain(t, b, `>2017</td>`, `>2001</td>`)
	if !regexp.MustCompile(`<time datetime="\d{4}-\d{2}-\d{2}">\d{4}-\d{2}-\d{2}</time></td>`).MatchString(b) {
		t.Error("no updated date cell")
	}
}

// TestCompactSortControlIsTheOnlySortField: the filter panel holds a
// compact sort control ("Sort by" and a direction), shown below lg only
// (the table hides Year below md and Updated below lg, so their head links
// are not reachable there). It is the ONE pair of sort/dir fields of the
// form (also on lg+, where CSS hides it and the head links set the sort):
// its options are every key the table head can sort by, the hidden
// columns included; a rows partial swaps its options out of band (the
// selects keep focus), and a plain GET submit sorts.
func TestCompactSortControlIsTheOnlySortField(t *testing.T) {
	e := sortEnv(t)
	b := e.listGet(t, "/tv?sort=year&dir=asc&q=d")
	form := filterForm(t, b)
	for _, name := range []string{`name="sort"`, `name="dir"`} {
		if n := strings.Count(b, name); n != 1 {
			t.Errorf("%s appears %d times", name, n)
		}
	}
	mustNotContain(t, b, `type="hidden" name="sort"`, `type="hidden" name="dir"`, `id="filter-sort"`)
	panel := form[strings.Index(form, `<details id="filter-panel"`):]
	i := strings.Index(panel, `id="filter-sort-control"`)
	if i < 0 {
		t.Fatalf("no sort control in the panel: %s", panel)
	}
	ctl := panel[strings.LastIndex(panel[:i], "<"):]
	ctl = ctl[:strings.Index(ctl, "</fieldset>")]
	mustContain(t, ctl, "lg:hidden", "<legend", "Sort by",
		`<select id="filter-sort-select" name="sort"`, `<select id="filter-dir-select" name="dir"`,
		`<option value="updated">Recently updated</option>`, `<option value="title">Title</option>`,
		`<option value="rating">Score</option>`, `<option value="progress">Progress</option>`,
		`<option value="year" selected>Year</option>`, `<option value="asc" selected>Ascending</option>`)
	// the default sort selects "Recently updated" and the default order
	plain := filterForm(t, e.listGet(t, "/tv"))
	mustContain(t, plain, `<option value="updated" selected>Recently updated</option>`, `<option value="" selected>Default order</option>`)

	// a no-JS submit of the form (every field as a browser sends it) sorts
	vals := submitted(t, form)
	vals.Set("sort", "title")
	vals.Set("dir", "desc")
	sorted := e.listGet(t, "/tv?"+vals.Encode())
	wantOrder(t, "submitted", sorted, "Dune Days", "Deep", "Dark")
	if n := noticeOf(sorted); n != "" {
		t.Errorf("notice %q", n)
	}
	vals.Set("sort", "updated")
	vals.Set("dir", "")
	if n := noticeOf(e.listGet(t, "/tv?"+vals.Encode())); n != "" {
		t.Errorf("default submit notice %q", n)
	}

	// a head link's partial keeps the control in sync, out of band (options
	// only: never a select element, which would lose focus)
	hx := map[string]string{"HX-Request": "true", "HX-Target": "list-rows"}
	p := e.getWith("/tv?sort=title&q=d", hx).Body.String()
	mustNotContain(t, p, `<form method="get"`, "<html", `name="q"`, "<select", `name="sort"`)
	mustContain(t, headCell(t, thead(t, p), "Title"), `aria-sort="ascending"`)
	mustContain(t, oobBlock(t, p, "innerHTML:#filter-sort-select"), `<option value="title" selected>Title</option>`, `<option value="year">Year</option>`)
	mustContain(t, oobBlock(t, p, "innerHTML:#filter-dir-select"), `<option value="" selected>Default order</option>`)
	wantOrder(t, "partial", p, "Dark", "Deep", "Dune Days")
}

// TestSortHeaderColumnsFollowTheSchema: the book fixture has no sortable
// field (no extra column) but a +1 track (progress sorts); its type column
// (the first Show text field) is plain text.
func TestSortHeaderColumnsFollowTheSchema(t *testing.T) {
	e := newFixtureEnv(t)
	e.addFixture(t, "book", "book", bookItem("1", "Earthsea"), library.Entry{Status: library.Active})
	head := thead(t, e.listGet(t, "/book"))
	mustContain(t, head, `id="sort-title"`, `id="sort-progress"`, `id="sort-rating"`, `id="sort-updated"`)
	mustNotContain(t, head, `id="sort-year"`, `id="sort-author"`, ">Year<")
	mustNotContain(t, headCell(t, head, "Author"), "<a ")
	// a plugin without a +1 track: Progress is plain text
	e2 := newFixtureEnv(t, func(book, _ *plugintest.Plugin) { book.M.Kinds[0].Progress.PrimaryKey = "" })
	e2.addFixture(t, "book", "book", bookItem("1", "Earthsea"), library.Entry{Status: library.Active})
	head = thead(t, e2.listGet(t, "/book"))
	mustNotContain(t, headCell(t, head, "Progress"), "<a ", "aria-sort")
}

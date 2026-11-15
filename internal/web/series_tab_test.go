package web

import (
	"html"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// An unknown tab value is an ignored parameter like any other; tab=series is
// not.
func TestSeriesTabParameterNotice(t *testing.T) {
	fe := newFixtureEnv(t)
	fe.tvSeries(t, "Saga")
	if got := noticeKeysOf(fe.get("/tv?tab=bogus").Body.String()); strings.Join(got, ",") != "tab" {
		t.Errorf("tab=bogus notice keys = %v, want [tab]", got)
	}
	for _, u := range []string{"/tv?tab=series", "/tv?tab=series&view=table"} {
		if n := noticeOf(fe.get(u).Body.String()); n != "" {
			t.Errorf("%s: unexpected notice %q", u, n)
		}
	}
}

// kindNavLabels returns the link texts of the kind tab bar, in order.
func kindNavLabels(body string) []string {
	i := strings.Index(body, `aria-label="Kinds"`)
	if i < 0 {
		return nil
	}
	nav := body[i : i+strings.Index(body[i:], "</nav>")]
	var out []string
	for _, m := range regexp.MustCompile(`<a [^>]*>([^<]*)</a>`).FindAllStringSubmatch(nav, -1) {
		out = append(out, strings.TrimSpace(m[1]))
	}
	return out
}

func TestSeriesTabKindBar(t *testing.T) {
	fe := newFixtureEnv(t)
	fe.tvSeries(t, "Saga")
	for _, u := range []string{"/tv", "/tv?tab=series"} {
		if got := kindNavLabels(fe.get(u).Body.String()); strings.Join(got, ",") != "All,Series" {
			t.Errorf("one-kind %s: tabs = %v, want All and Series", u, got)
		}
	}

	multi := newFixtureEnv(t, func(_, tv *plugintest.Plugin) {
		k := tv.M.Kinds[0]
		k.Key, k.Label, k.LabelPlural = "film", "Film", "Films"
		k.Sections = append([]plugin.SectionDef(nil), k.Sections...)
		tv.M.Kinds = append(tv.M.Kinds, k)
	})
	multi.tvSeries(t, "Saga")
	for _, u := range []string{"/tv", "/tv?kind=film", "/tv?tab=series"} {
		if got := kindNavLabels(multi.get(u).Body.String()); strings.Join(got, ",") != "Shows,Films,Series" {
			t.Errorf("multi-kind %s: tabs = %v, want the kinds then Series", u, got)
		}
	}
}

func TestSeriesTabSortByTitle(t *testing.T) {
	fe := newFixtureEnv(t)
	// created Mid, Zeta, Alpha: the newest update first is Alpha
	for _, title := range []string{"Mid Set", "Zeta Set", "Alpha Set"} {
		id := fe.addFixture(t, "tv", "show", tvShow(strings.ReplaceAll(title, " ", ""), "Show "+title), library.Entry{Status: library.Planned})
		if _, err := fe.lib.CreateSeries(bg, id, title); err != nil {
			t.Fatal(err)
		}
	}
	titlesIn := func(u string) string {
		body := fe.get(u).Body.String()
		type at struct {
			title string
			pos   int
		}
		var found []at
		for _, title := range []string{"Alpha Set", "Mid Set", "Zeta Set"} {
			i := strings.Index(body, ">"+title+"<")
			if i < 0 {
				t.Fatalf("%s: %q missing", u, title)
			}
			found = append(found, at{title, i})
		}
		for i := range found {
			for j := i + 1; j < len(found); j++ {
				if found[j].pos < found[i].pos {
					found[i], found[j] = found[j], found[i]
				}
			}
		}
		var out []string
		for _, f := range found {
			out = append(out, f.title)
		}
		return strings.Join(out, ",")
	}
	if got := titlesIn("/tv?tab=series&view=table&sort=title"); got != "Alpha Set,Mid Set,Zeta Set" {
		t.Errorf("sort=title order = %s", got)
	}
	if got := titlesIn("/tv?tab=series&view=table"); got == "Alpha Set,Mid Set,Zeta Set" {
		t.Errorf("default order = %s, want the newest update first", got)
	}
}

// The pager links of the Series tab stay on the tab and keep its parameters.
func TestSeriesTabPaginationKeepsTab(t *testing.T) {
	fe := newFixtureEnv(t)
	for i := 0; i < listPageSize+1; i++ {
		n := strconv.Itoa(i)
		id := fe.addFixture(t, "tv", "show", tvShow("p"+n, "Show "+n), library.Entry{Status: library.Planned})
		if _, err := fe.lib.CreateSeries(bg, id, "Set "+n); err != nil {
			t.Fatal(err)
		}
	}
	linkTo := func(body, label string) string {
		m := regexp.MustCompile(`href="([^"]*)"[^>]*>` + label + `</a>`).FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("no %s link", label)
		}
		return html.UnescapeString(m[1])
	}
	next := linkTo(fe.get("/tv?tab=series&view=table&sort=title").Body.String(), "Next")
	for _, want := range []string{"tab=series", "page=2", "sort=title", "view=table"} {
		if !strings.Contains(next, want) {
			t.Errorf("next link %q misses %s", next, want)
		}
	}
	if prev := linkTo(fe.get(next).Body.String(), "Previous"); !strings.Contains(prev, "tab=series") {
		t.Errorf("previous link %q leaves the tab", prev)
	}
}

// Titles are user data: tags in a series title, a part title or a candidate
// title are escaped on the series page, the Series tab and the candidates
// fragment.
func TestSeriesViewsEscapeTitles(t *testing.T) {
	const raw = "<b>x</b>"
	fe := newFixtureEnv(t)
	first := fe.addFixture(t, "tv", "show", tvShow("e1", "Part "+raw), library.Entry{Status: library.Planned})
	ser, err := fe.lib.CreateSeries(bg, first, "Series "+raw)
	if err != nil {
		t.Fatal(err)
	}
	fe.addFixture(t, "tv", "show", tvShow("e2", "Candidate "+raw), library.Entry{Status: library.Planned})
	for name, u := range map[string]string{
		"series page":    seriesURL("tv", ser.UID),
		"series tab":     "/tv?tab=series&view=table",
		"series cards":   "/tv?tab=series&view=cards",
		"expanded parts": seriesURL("tv", ser.UID) + "/parts",
		"candidates":     seriesURL("tv", ser.UID) + "/candidates",
	} {
		rec := fe.get(u)
		if rec.Code != 200 {
			t.Fatalf("%s: code %d", name, rec.Code)
		}
		body := rec.Body.String()
		if strings.Contains(body, raw) {
			t.Errorf("%s: unescaped %s in the page", name, raw)
		}
		if !strings.Contains(body, "&lt;b&gt;x&lt;/b&gt;") {
			t.Errorf("%s: no escaped title", name)
		}
	}
}

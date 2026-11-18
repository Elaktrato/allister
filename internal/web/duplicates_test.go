package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/allister/allister/internal/library"
)

// twoBooksSameISBN adds two books sharing the ISBN 9780000000001.
func (fe *fixtureEnv) twoBooksSameISBN(t *testing.T) (int64, int64) {
	t.Helper()
	a := bookItem("d1", "Earthsea")
	a.ExternalIDs = map[string]string{"isbn": "9780000000001"}
	b := bookItem("d2", "A Wizard of Earthsea")
	b.ExternalIDs = map[string]string{"isbn": "9780000000001"}
	return fe.addFixture(t, "book", "book", a, library.Entry{Status: library.Active}),
		fe.saveFixture(t, "book", "book", b)
}

func TestDuplicatesPageListsPairsWithReason(t *testing.T) {
	fe := newFixtureEnv(t)
	a, b := fe.twoBooksSameISBN(t)
	fe.addFixture(t, "tv", "show", tvShow("t1", "Twin Peaks"), library.Entry{Status: library.Completed})
	fe.addFixture(t, "tv", "show", tvShow("t2", "twin  peaks"), library.Entry{Status: library.Planned})

	rec := fe.get("/book/duplicates")
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d", rec.Code)
	}
	body := rec.Body.String()
	mustContain(t, body, "Possible duplicates", "Earthsea", "A Wizard of Earthsea", "Fake Books", "same ISBN",
		"not in your library", `/book/merge?keep=`+idStr(a)+`&amp;other=`+idStr(b),
		`action="/book/duplicates/dismiss"`, `name="a" value="`+idStr(a)+`"`, `name="b" value="`+idStr(b)+`"`, "Not a duplicate", "Compare &amp; merge")
	mustNotContain(t, body, "same title")

	tv := fe.get("/tv/duplicates").Body.String()
	mustContain(t, tv, "Twin Peaks", "same title and year", "/tv/merge?keep=")
	mustNotContain(t, tv, "Earthsea")
}

func TestDuplicatesDismissHidesPair(t *testing.T) {
	fe := newFixtureEnv(t)
	a, b := fe.twoBooksSameISBN(t)
	rec := fe.do("POST", "/book/duplicates/dismiss", url.Values{"a": {idStr(b)}, "b": {idStr(a)}}, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/book/duplicates" {
		t.Fatalf("dismiss: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	body := fe.get("/book/duplicates").Body.String()
	mustNotContain(t, body, "same ISBN", "A Wizard of Earthsea")
	mustContain(t, body, "No possible duplicates")
	for _, v := range []url.Values{{"a": {"x"}, "b": {"1"}}, {"a": {idStr(a)}, "b": {idStr(a)}}, {"a": {idStr(a)}}} {
		if c := fe.do("POST", "/book/duplicates/dismiss", v, false).Code; c < 400 {
			t.Fatalf("bad dismiss %v answered %d", v, c)
		}
	}
}

func TestListLinksDuplicatesOnlyWhenPairsExist(t *testing.T) {
	fe := newFixtureEnv(t)
	fe.addFixture(t, "book", "book", bookItem("s1", "Solo"), library.Entry{Status: library.Active})
	mustNotContain(t, fe.get("/book").Body.String(), "/book/duplicates")
	a, b := fe.twoBooksSameISBN(t)
	_ = b
	body := fe.get("/book").Body.String()
	mustContain(t, body, `href="/book/duplicates"`, "Possible duplicates (1)")
	// a rows partial skips the count
	req := httptest.NewRequest("GET", "/book?status=all", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Target", "list-rows")
	rec := httptest.NewRecorder()
	fe.h.ServeHTTP(rec, req)
	mustNotContain(t, rec.Body.String(), "Possible duplicates")
	if err := fe.lib.DismissDuplicate(bg, a, b); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fe.get("/book").Body.String(), "/book/duplicates") {
		t.Fatal("link stays after the pair is dismissed")
	}
}

// TestDuplicateRowsSkipAPairWhoseItemIsGone: an item deleted between
// Duplicates and the side lookup drops its pair instead of failing.
func TestDuplicateRowsSkipAPairWhoseItemIsGone(t *testing.T) {
	fe := newFixtureEnv(t)
	a, b := fe.twoBooksSameISBN(t)
	m, _ := fe.reg.Manifest("book")
	r := httptest.NewRequest("GET", "/book/duplicates", nil)
	pairs := []library.DuplicatePair{{A: a, B: 999999, Reason: "id:isbn"}, {A: 999998, B: b, Reason: "id:isbn"}, {A: a, B: b, Reason: "id:isbn"}}
	rows, err := fe.srv.duplicateRows(r, m, pairs)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].AID != idStr(a) || rows[0].BID != idStr(b) {
		t.Fatalf("rows = %+v, want only the pair of existing items", rows)
	}
}

// TestListPageSurvivesAFailingDuplicateCheck: the duplicate check is an
// extra; when it fails the list still shows, without the link.
func TestListPageSurvivesAFailingDuplicateCheck(t *testing.T) {
	fe := newFixtureEnv(t)
	fe.twoBooksSameISBN(t)
	if _, err := fe.d.Exec(`DROP TABLE dismissed_duplicates`); err != nil {
		t.Fatal(err)
	}
	rec := fe.get("/book")
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Earthsea")
	mustNotContain(t, rec.Body.String(), "Possible duplicates")
}

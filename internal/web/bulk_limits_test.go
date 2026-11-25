package web

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"
)

// manyIDs is a bulk form selecting n ids (1..n) for a status change.
func manyIDs(n int) url.Values {
	v := url.Values{"list": {"status=all&view=table"}, "action": {"status"}, "to_status": {"paused"}}
	for i := 1; i <= n; i++ {
		v.Add("ids", strconv.Itoa(i))
	}
	return v
}

// A "Select all loaded" after scrolling far posts thousands of ids: beyond
// the ordinary form limit (64 KiB) the bulk form still asks, and beyond the
// text-form limit it answers the form with a message, never a bare error.
func TestBulkLargeSelections(t *testing.T) {
	fe := newFixtureEnv(t)
	v := manyIDs(8000)
	if n := len(v.Encode()); n <= maxFormBytes {
		t.Fatalf("form %d bytes is not above the ordinary limit", n)
	}
	rec := fe.do("POST", "/book/bulk", v, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("8000 ids: %d %.200s", rec.Code, rec.Body.String())
	}
	mustContain(t, rec.Body.String(), "Change 8000 entries?", `name="expect" value="8000"`)

	big := manyIDs(60000)
	if n := len(big.Encode()); n <= maxTextFormBytes {
		t.Fatalf("form %d bytes is not above the text-form limit", n)
	}
	rec = fe.do("POST", "/book/bulk", big, false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("60000 ids: %d %.200s", rec.Code, rec.Body.String())
	}
	b := rec.Body.String()
	mustContain(t, b, "<html", "Too many entries selected", "Select all", `action="/book/bulk"`, `href="/book"`)
	mustNotContain(t, b, "request body too large", `name="ids"`)
}

// The bulk bar's idle text names no page (scroll mode loads several).
func TestBulkBarIdleText(t *testing.T) {
	fe := newFixtureEnv(t)
	manyShows(t, fe, 2)
	for _, mode := range []string{"scroll", "pages"} {
		if err := fe.set.Set(bg, "list_paging", mode); err != nil {
			t.Fatal(err)
		}
		b := fe.listGet(t, "/tv?view=table")
		mustContain(t, b, "Tick entries, then choose what to change.")
		mustNotContain(t, b, "Tick entries on this page")
	}
}

// The list paging setting rides along in the backup and is restored only
// into an installation without its own.
func TestListPagingBackupRoundTrip(t *testing.T) {
	a := backupEnv(t)
	a.do("POST", "/settings/list-paging", url.Values{"list_paging": {"pages"}}, false)
	data := a.download(t)

	b := backupEnv(t)
	if rec := b.restoreUpload(t, data); rec.Code != 200 {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body.String())
	}
	if b.setting(t, "list_paging") != "pages" {
		t.Errorf("paging after restore: %q", b.setting(t, "list_paging"))
	}
	c := backupEnv(t)
	c.do("POST", "/settings/list-paging", url.Values{"list_paging": {"scroll"}}, false)
	c.restoreUpload(t, data)
	if c.setting(t, "list_paging") != "scroll" {
		t.Errorf("own paging overwritten: %q", c.setting(t, "list_paging"))
	}
}

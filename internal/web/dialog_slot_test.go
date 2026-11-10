package web

import (
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anime/anilist"
)

var (
	slotRe       = regexp.MustCompile(`id="dialog-slot" data-swap-statuses="([^"]*)"`)
	slotStatusRe = regexp.MustCompile(`\b(\d{3})\b`)
)

// slotStatuses returns the error statuses the page's dialog slot swaps in
// (htmx 2 swaps no 4xx/5xx answer unless the before-swap handler allows it).
func slotStatuses(t *testing.T, page string) map[int]bool {
	t.Helper()
	m := slotRe.FindStringSubmatch(page)
	if m == nil {
		t.Fatal("no dialog slot with a before-swap handler in page")
	}
	out := map[int]bool{}
	for _, s := range slotStatusRe.FindAllStringSubmatch(m[1], -1) {
		n, _ := strconv.Atoi(s[1])
		out[n] = true
	}
	return out
}

// TestDialogSlotSwapsEveryDialogStatus: every error status with which a
// dialog handler answers an htmx request with a dialog (add, match, entry
// edit) is one the page's dialog slot swaps in; otherwise the user sees
// nothing happen.
func TestDialogSlotSwapsEveryDialogStatus(t *testing.T) {
	type answer struct {
		what string
		rec  *httptest.ResponseRecorder
	}
	var overview, list []answer

	ae := newAddEnv(t)
	for _, err := range []error{plugin.ErrUnavailable, plugin.ErrBadResponse} {
		ae.book.Err = err
		overview = append(overview, answer{"add: " + err.Error(), ae.do("POST", "/book/books/42/add", url.Values{"status": {"active"}}, true)})
	}
	ae.book.Err = nil
	overview = append(overview, answer{"add: invalid", ae.do("POST", "/book/books/42/add", url.Values{"status": {"nope"}}, true)})
	ae.do("POST", "/book/books/42/add", url.Values{"status": {"active"}}, true)
	overview = append(overview, answer{"add: duplicate", ae.do("POST", "/book/books/42/add", url.Values{"status": {"active"}}, true)})

	me := newMatchEnv(t)
	me.unmatched(t)
	for _, err := range []error{anilist.ErrUnavailable, anilist.ErrBadResponse} {
		me.fp.animeErr = err
		overview = append(overview, answer{"match: " + err.Error(), me.do("POST", "/anime/manual/man1/match", url.Values{"pick": {"1535"}}, true)})
	}
	me.fp.animeErr = nil

	le := newEnv(t)
	id := le.addEntry(t, anilist.AniList, "1535", nil, library.Active, 1)
	list = append(list, answer{"entry edit: invalid", le.do("POST", "/list/"+idStr(id)+"/edit", url.Values{"status": {"nope"}}, true)})

	for _, c := range []struct {
		page    string
		answers []answer
	}{{newEnv(t).get("/anime/anilist/1535").Body.String(), overview}, {le.get("/anime?view=table").Body.String(), list}} {
		swaps := slotStatuses(t, c.page)
		for _, code := range dialogSwapStatuses {
			if !swaps[code] {
				t.Errorf("dialog slot does not swap %d (dialogSwapStatuses %v)", code, dialogSwapStatuses)
			}
		}
		for _, a := range c.answers {
			if a.rec.Code < 400 || !strings.Contains(a.rec.Body.String(), "<form") {
				t.Fatalf("%s: %d is not a dialog answer: %s", a.what, a.rec.Code, a.rec.Body.String())
			}
			if !swaps[a.rec.Code] {
				t.Errorf("%s answers %d with a dialog, but the dialog slot only swaps %v", a.what, a.rec.Code, swaps)
			}
		}
	}
}

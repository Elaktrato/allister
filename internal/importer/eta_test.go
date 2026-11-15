package importer

import (
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
)

// A row resolved by title costs two requests (Search, then Details); a row
// with an id the source looks up costs one. The fake books source answers 60
// requests a minute: one second each.
func TestETACountsTitleResolvedRowsAsTwoRequests(t *testing.T) {
	t.Run("ids the source cannot look up", func(t *testing.T) {
		e, _, imp := newTitleEnv(t)
		imp.Result = plugin.ParseResult{Entries: []plugin.ParsedEntry{
			shelfEntry("/a/", "Alpha", 2001), shelfEntry("/b/", "Beta", 2002), shelfEntry("/c/", "Gamma", 2003),
		}}
		e.submit("book", "shelf-csv")
		if r := e.report(); r.Remaining != 3 || r.ETA != 6*time.Second {
			t.Fatalf("remaining %d, eta %v, want 3 and 6s", r.Remaining, r.ETA)
		}
	})
	t.Run("mixed lookup and title rows", func(t *testing.T) {
		e, _, imp := newTitleEnv(t)
		byTitle := plugin.ParsedEntry{Kind: "book", Title: "Delta", Status: plugin.Completed,
			Fields: map[string]plugin.FieldValue{"year": {Num: pf(2004)}}}
		byISBN := func(isbn string) plugin.ParsedEntry {
			return plugin.ParsedEntry{Kind: "book", Title: "Isbn " + isbn, Status: plugin.Completed,
				ExternalIDs: map[string]string{"isbn": isbn}}
		}
		imp.Result = plugin.ParseResult{Entries: []plugin.ParsedEntry{byISBN("1"), byISBN("2"), byTitle}}
		e.submit("book", "isbn-titles")
		// two lookups (one request each) and one title row (two requests)
		if r := e.report(); r.Remaining != 3 || r.ETA != 4*time.Second {
			t.Fatalf("remaining %d, eta %v, want 3 and 4s", r.Remaining, r.ETA)
		}
	})
}

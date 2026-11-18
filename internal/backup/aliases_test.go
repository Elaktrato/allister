package backup

import (
	"strings"
	"testing"

	"github.com/allister/allister/internal/library"
)

// tableRows returns the rows of a one-column query as strings.
func (e *env) tableRows(query string) []string {
	e.t.Helper()
	rows, err := e.db.Query(query)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

const (
	aliasRowsQuery = `SELECT i.source_id || '<-' || a.source || ':' || a.source_id FROM item_aliases a
		JOIN items i ON i.id = a.item_id ORDER BY a.source, a.source_id`
	dismissedRowsQuery = `SELECT x.source_id || '~' || y.source_id || '@' || d.dismissed_at FROM dismissed_duplicates d
		JOIN items x ON x.id = d.item_a JOIN items y ON y.id = d.item_b ORDER BY x.source_id, y.source_id`
)

func TestBackupRoundTripKeepsAliasesAndDismissedPairs(t *testing.T) {
	a := newEnv(t)
	i1 := a.book("1", "", &library.Entry{Status: library.Planned})
	i2 := a.book("2", "", &library.Entry{Status: library.Planned})
	i3 := a.book("3", "", &library.Entry{Status: library.Planned})
	i4 := a.book("4", "", &library.Entry{Status: library.Planned})
	if err := a.lib.MergeItems(bg, i1, i2, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.lib.DismissDuplicate(bg, i4, i3); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`UPDATE dismissed_duplicates SET dismissed_at = '2026-03-04T05:06:07Z'`); err != nil {
		t.Fatal(err)
	}
	data := a.archive()
	_, files := open(t, data)
	body := string(files["backup.json"])
	lists, aliases, dismissed := strings.Index(body, `"lists":[`), strings.Index(body, `"aliases":[`), strings.Index(body, `"dismissed_duplicates":[`)
	if lists < 0 || aliases < lists || dismissed < aliases || !strings.Contains(body, `"format_version":3`) {
		t.Fatalf("aliases or dismissed_duplicates missing or out of order: %s", body)
	}

	b := newEnv(t)
	rep := mustRestore(t, b, data)
	if rep.Created != 3 || rep.Aliases != 1 || rep.AliasesSkipped != 0 || rep.AliasesLeftOut != 0 ||
		rep.Dismissed != 1 || rep.DismissedSkipped != 0 || rep.DismissedLeftOut != 0 {
		t.Fatalf("report = %+v", rep)
	}
	if got := b.tableRows(aliasRowsQuery); len(got) != 1 || got[0] != "1<-books:2" {
		t.Fatalf("aliases = %v", got)
	}
	if it, err := b.lib.ItemBySource(bg, "book", "book", "books", "2"); err != nil || it == nil || it.SourceID != "1" {
		t.Fatalf("alias lookup = %+v, %v", it, err)
	}
	if got := b.tableRows(dismissedRowsQuery); len(got) != 1 || got[0] != "3~4@2026-03-04T05:06:07Z" {
		t.Fatalf("dismissed = %v", got)
	}

	// restoring twice adds nothing
	rep = mustRestore(t, b, data)
	if rep.Created != 0 || rep.Aliases != 0 || rep.AliasesSkipped != 1 || rep.Dismissed != 0 || rep.DismissedSkipped != 1 {
		t.Fatalf("second report = %+v", rep)
	}
	if got := b.tableRows(aliasRowsQuery); len(got) != 1 {
		t.Fatalf("aliases after second restore = %v", got)
	}
	if got := b.tableRows(dismissedRowsQuery); len(got) != 1 {
		t.Fatalf("dismissed after second restore = %v", got)
	}
}

func ref(id string) string {
	return `{"plugin_id":"book","kind":"book","source":"books","source_id":"` + id + `"}`
}

func TestRestoreAliasesAndDismissedPairsMerge(t *testing.T) {
	b := newEnv(t)
	b.book("1", "", &library.Entry{Status: library.Planned})
	b.book("2", "", &library.Entry{Status: library.Planned})
	b.book("3", "", &library.Entry{Status: library.Planned})
	body := header("2") + `],"lists":[],"aliases":[` +
		`{"item":` + ref("1") + `,"alias":` + ref("9") + `},` + // added
		`{"item":` + ref("1") + `,"alias":` + ref("2") + `},` + // the alias identity is an item: skipped
		`{"item":` + ref("3") + `,"alias":` + ref("9") + `},` + // the alias exists already: skipped
		`{"item":` + ref("gone") + `,"alias":` + ref("8") + `},` + // no survivor: left out
		`{"item":` + ref("1") + `,"alias":{"plugin_id":"book","kind":"book","source":"","source_id":"7"}},` + // not valid
		`42],"dismissed_duplicates":[` +
		`{"a":` + ref("2") + `,"b":` + ref("1") + `,"dismissed_at":"2026-01-02T03:04:05Z"},` + // added (ordered)
		`{"a":` + ref("1") + `,"b":` + ref("2") + `,"dismissed_at":"2026-01-02T03:04:05Z"},` + // present: skipped
		`{"a":` + ref("9") + `,"b":` + ref("3") + `,"dismissed_at":"2026-01-02T03:04:05Z"},` + // 9 is an alias of 1: added
		`{"a":` + ref("3") + `,"b":` + ref("gone") + `,"dismissed_at":"2026-01-02T03:04:05Z"},` + // left out
		`{"a":` + ref("3") + `,"b":` + ref("3") + `,"dismissed_at":"2026-01-02T03:04:05Z"},` + // the same item: left out
		`{"a":` + ref("2") + `,"b":` + ref("3") + `,"dismissed_at":"yesterday"}]}` // not valid
	rep := mustRestore(t, b, zipOf(t, entry{name: "backup.json", body: []byte(body)}))
	if rep.Aliases != 1 || rep.AliasesSkipped != 2 || rep.AliasesLeftOut != 1 ||
		rep.Dismissed != 2 || rep.DismissedSkipped != 1 || rep.DismissedLeftOut != 2 {
		t.Fatalf("report = %+v", rep)
	}
	var reasons []string
	for _, n := range rep.Notes {
		reasons = append(reasons, n.Reason)
	}
	joined := strings.Join(reasons, "|")
	if len(rep.Notes) != 3 || strings.Count(joined, "not valid") != 2 || !strings.Contains(joined, "cannot be read") {
		t.Fatalf("notes = %q", reasons)
	}
	if got := b.tableRows(aliasRowsQuery); len(got) != 1 || got[0] != "1<-books:9" {
		t.Fatalf("aliases = %v", got)
	}
	if got := b.tableRows(dismissedRowsQuery); len(got) != 2 || got[0] != "1~2@2026-01-02T03:04:05Z" || got[1] != "1~3@2026-01-02T03:04:05Z" {
		t.Fatalf("dismissed = %v", got)
	}
}

func TestRestoreWithoutAliasesOrDismissedPairs(t *testing.T) {
	b := newEnv(t)
	data := zipOf(t, entry{name: "backup.json", body: jsonOf(t, bookItem("9"))})
	rep := mustRestore(t, b, data)
	if rep.Created != 1 || rep.Aliases != 0 || rep.AliasesSkipped != 0 || rep.Dismissed != 0 || len(rep.Notes) != 0 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestRestoreRefusesAliasesBeforeItems(t *testing.T) {
	for _, member := range []string{"aliases", "dismissed_duplicates"} {
		e := newEnv(t)
		body := `{"format":"allister.backup","format_version":2,"` + member + `":[],"items":[]}`
		_, err := e.restore(zipOf(t, entry{name: "backup.json", body: []byte(body)}), Limits{}, nil)
		if err == nil || !strings.Contains(err.Error(), `"`+member+`" must come after "items"`) {
			t.Fatalf("%s: err = %v", member, err)
		}
		e = newEnv(t)
		body = header("2") + `],"` + member + `":[],"` + member + `":[]}`
		rep, err := e.restore(zipOf(t, entry{name: "backup.json", body: []byte(body)}), Limits{}, nil)
		if err != nil || !strings.Contains(rep.Aborted, `more than one "`+member+`"`) {
			t.Fatalf("%s: report = %+v, %v", member, rep, err)
		}
	}
}

func TestBackupRoundTripKeepsCoverSource(t *testing.T) {
	a := newEnv(t)
	cover := a.saveCover(7)
	id := a.book("1", cover, &library.Entry{Status: library.Planned})
	if _, err := a.db.Exec(`UPDATE items SET cover_source = 'https://img.example/1.jpg' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	a.book("2", "", &library.Entry{Status: library.Planned})
	data := a.archive()
	d, _ := open(t, data)
	if len(d.Items) != 2 || d.Items[0].CoverSource != "https://img.example/1.jpg" || d.Items[1].CoverSource != "" {
		t.Fatalf("items = %+v", d.Items)
	}
	b := newEnv(t)
	mustRestore(t, b, data)
	if got := b.tableRows(`SELECT source_id || '=' || cover_source FROM items ORDER BY source_id`); len(got) != 2 ||
		got[0] != "1=https://img.example/1.jpg" || got[1] != "2=" {
		t.Fatalf("cover sources = %v", got)
	}
}

func TestRestoreCoverSourceNeedsALocalCover(t *testing.T) {
	b := newEnv(t)
	it := bookItem("1")
	it.CoverURL = "https://img.example/1.jpg"
	it.CoverSource = "https://img.example/old.jpg" // only a downloaded cover has an origin
	bad := bookItem("2")
	bad.Cover, bad.CoverSource = "missing.png", "javascript:alert(1)"
	rep := mustRestore(t, b, zipOf(t, entry{name: "backup.json", body: jsonOf(t, it, bad)}))
	if rep.Created != 1 || rep.Failed != 1 || !strings.Contains(rep.FailedRows[0].Reason, "cover source") {
		t.Fatalf("report = %+v", rep)
	}
	if got := b.tableRows(`SELECT cover_source FROM items`); len(got) != 1 || got[0] != "" {
		t.Fatalf("cover sources = %v", got)
	}
}

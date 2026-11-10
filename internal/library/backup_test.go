package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

// collect returns every BackupItem of the library (batch size n).
func collect(t *testing.T, l *Library, n int) []BackupItem {
	t.Helper()
	var out []BackupItem
	if err := l.BackupItems(bg, n, func(bi BackupItem) error {
		out = append(out, bi)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// richShow saves a TV item with every part: titles, fields, genres, tags,
// external ids, relations, credits with counterparts and Detail, a local
// cover and an entry with an inactive progress value.
func richShow(t *testing.T, e *env, id string) int64 {
	t.Helper()
	d := tvData(id, fp(12))
	d.Titles = []plugin.Title{{Lang: "en", Text: "Show " + id}, {Lang: "original", Text: "原題 " + id}}
	d.Fields["type"] = plugin.FieldValue{Text: "series"}
	d.Fields["year"] = plugin.FieldValue{Num: fp(2020)}
	d.Genres = []string{"Drama", "Comedy"}
	d.Tags = []plugin.Tag{{Name: "Found Family", Rank: ip(87)}, {Name: "Twist", Rank: ip(0), Spoiler: true}, {Name: "Unranked"}}
	d.ExternalIDs = map[string]string{"tvdata": "tv-" + id, "imdb": "tt" + id}
	d.ExternalURL = "https://tv.example/show/" + id
	d.Relations = []plugin.Relation{{Kind: "sequel", Item: plugin.ItemSummary{Source: "tvdata", ID: "s2", Kind: "show",
		Titles: []plugin.Title{{Lang: "en", Text: "Season 2"}}, CoverURL: "https://img.example/s2.jpg", URL: "https://tv.example/show/s2"}}}
	d.CoverURL = "abc123.jpg"
	itemID, err := e.lib.AddToList(bg, "tv", "show", d, Entry{Status: Active, Rating: ip(8), Comment: "line one\nline two",
		StartedOn: tp(day(2024, 3, 5)), Progress: map[string]float64{"episode": 3}}, WithProgressKeys([]string{"episode", "position"}))
	if err != nil {
		t.Fatal(err)
	}
	// position 90 stays stored after switching to season+episode (inactive)
	if err := e.lib.UpsertEntry(bg, Entry{ItemID: itemID, Status: Active, Rating: ip(8), Comment: "line one\nline two",
		StartedOn: tp(day(2024, 3, 5)), Progress: map[string]float64{"episode": 3, "position": 90}}); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.SetActiveProgress(bg, itemID, []string{"season", "episode"}); err != nil {
		t.Fatal(err)
	}
	credits := []plugin.Credit{
		{Role: "actor", Detail: "MAIN", Person: plugin.PersonRef{ID: "c1", Name: "Hero", ImageURL: "https://img.example/c1.jpg", URL: "https://tv.example/c/c1"},
			With: &plugin.PersonRef{ID: "p1", Name: "Actor One", ImageURL: "https://img.example/p1.jpg"}, WithRole: "voice"},
		{Role: "director", Person: plugin.PersonRef{ID: "p2", Name: "Director", ImageURL: "https://img.example/p2.jpg"}},
	}
	if err := e.lib.SaveCredits(bg, itemID, credits); err != nil {
		t.Fatal(err)
	}
	// a downloaded person photo is a local name: not part of the backup
	if _, err := e.db.Exec(`UPDATE people SET image_path = 'local-photo.jpg' WHERE source_id = 'p2'`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE items SET created_at = '2023-01-02T03:04:05Z', updated_at = '2023-02-03T04:05:06Z' WHERE id = ?`, itemID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE entries SET updated_at = '2024-04-05T06:07:08Z' WHERE item_id = ?`, itemID); err != nil {
		t.Fatal(err)
	}
	return itemID
}

func TestBackupItemsCarriesEveryPart(t *testing.T) {
	e := newEnv(t)
	richShow(t, e, "1")
	items := collect(t, e.lib, 200)
	if len(items) != 1 {
		t.Fatalf("items = %d", len(items))
	}
	bi := items[0]
	if bi.PluginID != "tv" || bi.Kind != "show" || bi.Source != "tvdata" || bi.SourceID != "1" {
		t.Fatalf("identity = %+v", bi)
	}
	if bi.Cover != "abc123.jpg" || bi.CoverURL != "" {
		t.Fatalf("cover = %q url %q", bi.Cover, bi.CoverURL)
	}
	if !reflect.DeepEqual(bi.ProgressKeys, []string{"season", "episode"}) {
		t.Fatalf("progress keys = %v", bi.ProgressKeys)
	}
	if bi.CreatedAt != "2023-01-02T03:04:05Z" || bi.UpdatedAt != "2023-02-03T04:05:06Z" {
		t.Fatalf("stamps = %q %q", bi.CreatedAt, bi.UpdatedAt)
	}
	if len(bi.Tags) != 3 || *bi.Tags[0].Rank != 87 || *bi.Tags[1].Rank != 0 || !bi.Tags[1].Spoiler || bi.Tags[2].Rank != nil {
		t.Fatalf("tags = %+v", bi.Tags)
	}
	if !reflect.DeepEqual(bi.Genres, []string{"Drama", "Comedy"}) {
		t.Fatalf("genres = %v", bi.Genres)
	}
	if len(bi.Credits) != 2 || bi.Credits[0].Detail != "MAIN" || bi.Credits[0].With == nil || bi.Credits[0].With.SourceID != "p1" ||
		bi.Credits[0].WithRole != "voice" || bi.Credits[0].Person.ImageURL != "https://img.example/c1.jpg" {
		t.Fatalf("credits = %+v", bi.Credits)
	}
	if bi.Credits[1].Person.ImageURL != "" {
		t.Fatalf("local person photo leaked: %+v", bi.Credits[1].Person)
	}
	if bi.Entry == nil || bi.Entry.Status != Active || bi.Entry.Rating == nil || *bi.Entry.Rating != 8 ||
		bi.Entry.StartedOn == nil || *bi.Entry.StartedOn != "2024-03-05" || bi.Entry.FinishedOn != nil ||
		bi.Entry.UpdatedAt != "2024-04-05T06:07:08Z" {
		t.Fatalf("entry = %+v", bi.Entry)
	}
	if !reflect.DeepEqual(bi.Entry.Progress, map[string]float64{"episode": 3, "position": 90}) {
		t.Fatalf("progress (all keys) = %v", bi.Entry.Progress)
	}
	if len(bi.Relations) != 1 || bi.Relations[0].Item.URL != "https://tv.example/show/s2" {
		t.Fatalf("relations = %+v", bi.Relations)
	}
	// JSON: snake_case, unrated is null, sync_state is not stored
	raw, err := json.Marshal(bi)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"plugin_id":"tv"`, `"source_id":"1"`, `"progress_keys":`, `"external_ids":`, `"created_at":`, `"started_on":"2024-03-05"`, `"finished_on":null`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("json lacks %s: %s", want, raw)
		}
	}
	if strings.Contains(string(raw), "sync_state") {
		t.Fatalf("sync_state stored: %s", raw)
	}
}

func TestBackupItemsIteratesInBatchesByID(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 450; i++ {
		mustSave(t, e.lib, "book", "book", bookData(fmt.Sprint(i)))
	}
	// an item without an entry and of a plugin that is not installed any more
	if _, err := e.db.Exec(`INSERT INTO items(plugin_id, kind, source, source_id, progress_keys) VALUES ('gone','x','s','1','[]')`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`INSERT INTO item_titles(item_id, lang, text) VALUES (last_insert_rowid(), 'en', 'Gone')`); err != nil {
		t.Fatal(err)
	}
	items := collect(t, e.lib, 200)
	if len(items) != 451 {
		t.Fatalf("items = %d", len(items))
	}
	seen := map[string]bool{}
	for i, bi := range items[:450] {
		if bi.SourceID != fmt.Sprint(i) || seen[bi.SourceID] {
			t.Fatalf("order/dup at %d: %s", i, bi.SourceID)
		}
		seen[bi.SourceID] = true
		if len(bi.Titles) != 3 || bi.Fields["author"].Text != "Frank Herbert" || bi.Entry != nil {
			t.Fatalf("parts of %d: %+v", i, bi)
		}
		if bi.CoverURL != "https://img.example/"+bi.SourceID+".jpg" || bi.Cover != "" {
			t.Fatalf("remote cover = %q / %q", bi.Cover, bi.CoverURL)
		}
	}
	if items[450].PluginID != "gone" || items[450].Titles[0].Text != "Gone" {
		t.Fatalf("uninstalled plugin data missing: %+v", items[450])
	}
	// the callback error stops the iteration and is returned
	stop := errors.New("stop")
	n := 0
	err := e.lib.BackupItems(bg, 200, func(BackupItem) error {
		n++
		if n == 3 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) || n != 3 {
		t.Fatalf("err = %v after %d", err, n)
	}
}

func restoreAll(t *testing.T, l *Library, items []BackupItem, opts RestoreOptions) []RestoreResult {
	t.Helper()
	var out []RestoreResult
	for _, bi := range items {
		res, err := l.RestoreItem(bg, bi, opts)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, res)
	}
	return out
}

// keepCovers resolves every cover name to itself (as if present in the archive).
var keepCovers = RestoreOptions{ResolveCover: func(_ context.Context, name string) (string, string, error) { return name, "", nil }}

func TestRestoreItemRoundTripsIntoEmptyLibrary(t *testing.T) {
	a := newEnv(t)
	richShow(t, a, "1")
	richShow(t, a, "2")
	// an unmatched manual book with an external id, finished, unrated
	manual := bookData("m1")
	manual.Source = "manual"
	manual.CoverURL = ""
	manual.ExternalIDs = map[string]string{"isbn": "978-1"}
	manual.Fields["released"] = plugin.FieldValue{Date: tp(day(1965, 8, 1))}
	manual.Fields["translators"] = plugin.FieldValue{List: []string{"A", "B"}}
	manual.Fields["score"] = plugin.FieldValue{Num: fp(7.5)}
	if _, err := a.lib.AddToList(bg, "book", "book", manual, Entry{Status: Completed, StartedOn: tp(day(2024, 1, 1)), FinishedOn: tp(day(2024, 2, 29)),
		Progress: map[string]float64{"pages": 0}}, WithSyncState("done")); err != nil {
		t.Fatal(err)
	}
	// a disabled plugin's item is backed up and restored as data
	mustSave(t, a.lib, "book2", "book", bookData("x"))
	if err := a.reg.SetEnabled(bg, "book2", false); err != nil {
		t.Fatal(err)
	}
	before := collect(t, a.lib, 2)

	b := newEnv(t)
	if err := b.reg.SetEnabled(bg, "book2", false); err != nil {
		t.Fatal(err)
	}
	res := restoreAll(t, b.lib, before, keepCovers)
	for i, r := range res {
		if r.Outcome != RestoreCreated || r.ItemID == 0 || r.PluginID != before[i].PluginID {
			t.Fatalf("result %d = %+v", i, r)
		}
	}
	if res[2].SyncPending || !res[0].SyncPending || !res[3].SyncPending {
		t.Fatalf("sync pending: manual %v, sourced %v %v", res[2].SyncPending, res[0].SyncPending, res[3].SyncPending)
	}
	if st := mustItem(t, b.lib, res[2].ItemID).SyncState; st != "done" {
		t.Fatalf("manual sync state = %q", st)
	}
	if st := mustItem(t, b.lib, res[0].ItemID).SyncState; st != "pending" {
		t.Fatalf("sourced sync state = %q", st)
	}
	after := collect(t, b.lib, 200)
	if !reflect.DeepEqual(before, after) {
		ja, _ := json.MarshalIndent(before, "", " ")
		jb, _ := json.MarshalIndent(after, "", " ")
		t.Fatalf("round trip differs:\nA: %s\nB: %s", ja, jb)
	}
	// people are restored with their remote photos; the counterpart is linked
	if n := count(t, b.db, `SELECT count(*) FROM people WHERE plugin_id = 'tv'`); n != 3 {
		t.Fatalf("people = %d", n)
	}
}

func TestRestoreItemSkipsExistingAndNeverOverwrites(t *testing.T) {
	a := newEnv(t)
	richShow(t, a, "1")
	items := collect(t, a.lib, 200)

	b := newEnv(t)
	d := tvData("1", fp(12))
	d.Titles = []plugin.Title{{Lang: "en", Text: "My edited title"}}
	id := mustSave(t, b.lib, "tv", "show", d)
	res := restoreAll(t, b.lib, items, keepCovers)
	if res[0].Outcome != RestoreSkipped || res[0].ItemID != 0 {
		t.Fatalf("result = %+v", res[0])
	}
	it := mustItem(t, b.lib, id)
	if len(it.Titles) != 1 || it.Titles[0].Text != "My edited title" {
		t.Fatalf("existing item touched: %+v", it.Titles)
	}
	if en, _ := b.lib.Entry(bg, id); en != nil {
		t.Fatalf("entry created for a skipped item: %+v", en)
	}
	if n := count(t, b.db, `SELECT count(*) FROM credits`); n != 0 {
		t.Fatalf("credits written: %d", n)
	}
	// idempotent: a second restore of the same data skips everything
	c := newEnv(t)
	first := restoreAll(t, c.lib, items, keepCovers)
	second := restoreAll(t, c.lib, items, keepCovers)
	if first[0].Outcome != RestoreCreated || second[0].Outcome != RestoreSkipped {
		t.Fatalf("outcomes = %v, %v", first[0].Outcome, second[0].Outcome)
	}
	if n := count(t, c.db, `SELECT count(*) FROM items`); n != 1 {
		t.Fatalf("items = %d", n)
	}
}

func TestRestoreItemReportsInvalidRowsWithoutWriting(t *testing.T) {
	e := newEnv(t)
	richShow(t, e, "1")
	good := collect(t, e.lib, 200)[0]
	good.SourceID = "fresh"
	cases := []struct {
		name, want string
		mod        func(*BackupItem)
	}{
		{"unknown kind", "has no kind", func(b *BackupItem) { b.Kind = "movie" }},
		{"unknown field", "unknown field", func(b *BackupItem) { b.Fields["mood"] = plugin.FieldValue{Text: "x"} }},
		{"bad enum", "not one of", func(b *BackupItem) { b.Fields["type"] = plugin.FieldValue{Text: "film"} }},
		{"control char title", "control character", func(b *BackupItem) { b.Titles[0].Text = "bad\x07title" }},
		{"rank 500", "between 0 and 100", func(b *BackupItem) { b.Tags[0].Rank = ip(500) }},
		{"javascript url", "http(s) URL", func(b *BackupItem) { b.ExternalURL = "javascript:alert(1)" }},
		{"javascript person url", "http(s) URL", func(b *BackupItem) { b.Credits[0].Person.URL = "javascript:alert(1)" }},
		{"local person photo", "http(s) URL", func(b *BackupItem) { b.Credits[0].Person.ImageURL = "x.jpg" }},
		{"long detail", "longer than", func(b *BackupItem) { b.Credits[0].Detail = strings.Repeat("d", 101) }},
		{"progress keys not allowed", "not an allowed combination", func(b *BackupItem) { b.ProgressKeys = []string{"position"} }},
		{"progress key not eligible", "not a progress", func(b *BackupItem) { b.Entry.Progress["chapter"] = 1 }},
		{"progress above total", "above the total", func(b *BackupItem) { b.Entry.Progress["episode"] = 99 }},
		{"inactive progress negative", "negative", func(b *BackupItem) { b.Entry.Progress["position"] = -1 }},
		{"bad status", "status", func(b *BackupItem) { b.Entry.Status = "watching" }},
		{"rating 11", "rating", func(b *BackupItem) { b.Entry.Rating = ip(11) }},
		{"bad date", "date", func(b *BackupItem) { s := "2024-02-30"; b.Entry.StartedOn = &s }},
		{"finish before start", "before the start", func(b *BackupItem) { s := "2020-01-01"; b.Entry.FinishedOn = &s }},
		{"bad stamp", "time", func(b *BackupItem) { b.CreatedAt = "yesterday" }},
		{"bad entry stamp", "time", func(b *BackupItem) { b.Entry.UpdatedAt = "soon" }},
		{"cover path", "file name", func(b *BackupItem) { b.Cover = "../etc/passwd" }},
		{"cover url not remote", "http(s) URL", func(b *BackupItem) { b.Cover = ""; b.CoverURL = "x.jpg" }},
		{"cover and url", "both", func(b *BackupItem) { b.CoverURL = "https://img.example/a.jpg" }},
		{"no titles", "title", func(b *BackupItem) { b.Titles = nil }},
		{"too many titles", "titles", func(b *BackupItem) {
			for i := 0; i < 201; i++ {
				b.Titles = append(b.Titles, plugin.Title{Lang: "en", Text: fmt.Sprint("t", i)})
			}
		}},
		{"comment NUL", "NUL", func(b *BackupItem) { b.Entry.Comment = "a\x00b" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newEnv(t)
			raw, _ := json.Marshal(good)
			var bi BackupItem
			if err := json.Unmarshal(raw, &bi); err != nil {
				t.Fatal(err)
			}
			tc.mod(&bi)
			res, err := b.lib.RestoreItem(bg, bi, keepCovers)
			if err != nil {
				t.Fatal(err)
			}
			if res.Outcome != RestoreFailed || !strings.Contains(res.Reason, tc.want) {
				t.Fatalf("result = %+v, want failed mentioning %q", res, tc.want)
			}
			if n := count(t, b.db, `SELECT count(*) FROM items`) + count(t, b.db, `SELECT count(*) FROM people`); n != 0 {
				t.Fatalf("rows written: %d", n)
			}
		})
	}
}

func TestRestoreItemUnknownPluginAndCovers(t *testing.T) {
	e := newEnv(t)
	res, err := e.lib.RestoreItem(bg, BackupItem{PluginID: "nope", Kind: "x", Source: "s", SourceID: "1",
		Titles: []plugin.Title{{Lang: "en", Text: "T"}}}, RestoreOptions{})
	if err != nil || res.Outcome != RestoreUnknownPlugin {
		t.Fatalf("unknown plugin = %+v, %v", res, err)
	}
	bi := BackupItem{PluginID: "book", Kind: "book", Source: "books", SourceID: "1", Cover: "gone.jpg",
		Titles: []plugin.Title{{Lang: "en", Text: "T"}}}
	// no resolver: the cover is left empty with a note
	res, err = e.lib.RestoreItem(bg, bi, RestoreOptions{})
	if err != nil || res.Outcome != RestoreCreated || len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "cover") {
		t.Fatalf("result = %+v, %v", res, err)
	}
	if it := mustItem(t, e.lib, res.ItemID); it.CoverPath != "" {
		t.Fatalf("cover = %q", it.CoverPath)
	}
	// a resolver maps the archive name to the stored name; a note is kept
	bi.SourceID = "2"
	calls := 0
	res, err = e.lib.RestoreItem(bg, bi, RestoreOptions{ResolveCover: func(_ context.Context, name string) (string, string, error) {
		calls++
		return "stored-" + name, "", nil
	}})
	if err != nil || res.Outcome != RestoreCreated || len(res.Notes) != 0 {
		t.Fatalf("result = %+v, %v", res, err)
	}
	if it := mustItem(t, e.lib, res.ItemID); it.CoverPath != "stored-gone.jpg" {
		t.Fatalf("cover = %q", it.CoverPath)
	}
	// a skipped item never resolves (stores) its cover
	_, _ = e.lib.RestoreItem(bg, bi, RestoreOptions{ResolveCover: func(_ context.Context, name string) (string, string, error) {
		calls++
		return name, "", nil
	}})
	if calls != 1 {
		t.Fatalf("resolver calls = %d", calls)
	}
	// a resolver error is fatal and nothing is written
	bi.SourceID = "3"
	boom := errors.New("disk full")
	if _, err := e.lib.RestoreItem(bg, bi, RestoreOptions{ResolveCover: func(context.Context, string) (string, string, error) {
		return "", "", boom
	}}); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if it, _ := e.lib.ItemBySource(bg, "book", "book", "books", "3"); it != nil {
		t.Fatal("item written despite resolver error")
	}
}

func TestValidFileName(t *testing.T) {
	for _, ok := range []string{"a", "abc.jpg", "A_b-c.d", "0" + strings.Repeat("x", 127)} {
		if !ValidFileName(ok) {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"", ".hidden", "../x", "a/b", `a\b`, "a..b", "-x", "0" + strings.Repeat("x", 128), "a b", "é.jpg", "a:b"} {
		if ValidFileName(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestRestoreItemLinksExistingPeopleUnchanged(t *testing.T) {
	a := newEnv(t)
	richShow(t, a, "1")
	bi := collect(t, a.lib, 200)[0]

	b := newEnv(t)
	// B already knows the character (c1) and the voice actor (p1) from another item
	other := mustSave(t, b.lib, "tv", "show", tvData("other", nil))
	if err := b.lib.SaveCredits(bg, other, []plugin.Credit{{Role: "actor",
		Person: plugin.PersonRef{ID: "c1", Name: "Hero (edited in B)", URL: "https://tv.example/c/b-c1"},
		With:   &plugin.PersonRef{ID: "p1", Name: "Actor (B)", URL: "https://tv.example/p/b-p1"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.db.Exec(`UPDATE people SET image_path = 'b-local.jpg' WHERE source_id = 'c1'`); err != nil {
		t.Fatal(err)
	}
	snapshot := func(sid string) [3]string {
		var r [3]string
		if err := b.db.QueryRow(`SELECT name, external_url, image_path FROM people WHERE plugin_id = 'tv' AND source_id = ?`, sid).Scan(&r[0], &r[1], &r[2]); err != nil {
			t.Fatal(err)
		}
		return r
	}
	c1, p1 := snapshot("c1"), snapshot("p1")
	res, err := b.lib.RestoreItem(bg, bi, keepCovers)
	if err != nil || res.Outcome != RestoreCreated {
		t.Fatalf("restore = %+v, %v", res, err)
	}
	if got := snapshot("c1"); got != c1 {
		t.Fatalf("existing person changed: %v -> %v", c1, got)
	}
	if got := snapshot("p1"); got != p1 {
		t.Fatalf("existing counterpart changed: %v -> %v", p1, got)
	}
	// the missing director is created with the backup's data; credits link to the existing people
	if got := snapshot("p2"); got != [3]string{"Director", "", ""} {
		t.Fatalf("new person = %v", got)
	}
	cs, err := b.lib.Credits(bg, res.ItemID)
	if err != nil || len(cs) != 2 || cs[0].Person.Name != "Hero (edited in B)" || cs[0].With == nil || cs[0].With.Name != "Actor (B)" || cs[0].Detail != "MAIN" {
		t.Fatalf("credits = %+v, %v", cs, err)
	}
	// a new counterpart is created from the backup's data
	c := newEnv(t)
	res, err = c.lib.RestoreItem(bg, bi, keepCovers)
	if err != nil || res.Outcome != RestoreCreated {
		t.Fatal(err)
	}
	var name, img string
	if err := c.db.QueryRow(`SELECT name, image_path FROM people WHERE source_id = 'p1'`).Scan(&name, &img); err != nil || name != "Actor One" || img != "https://img.example/p1.jpg" {
		t.Fatalf("new counterpart = %q %q %v", name, img, err)
	}
}

func TestRestoreItemPersonWithoutNameFailsAtPlanTime(t *testing.T) {
	e := newEnv(t)
	bi := BackupItem{PluginID: "tv", Kind: "show", Source: "tvdata", SourceID: "1", Titles: []plugin.Title{{Lang: "en", Text: "T"}}, Cover: "c.png",
		Credits: []BackupCredit{{Role: "actor", Person: BackupPerson{SourceID: "x", Name: " "}}}}
	resolved := 0
	res, err := e.lib.RestoreItem(bg, bi, RestoreOptions{ResolveCover: func(context.Context, string) (string, string, error) {
		resolved++
		return "", "", nil
	}})
	if err != nil || res.Outcome != RestoreFailed || !strings.Contains(res.Reason, "name") {
		t.Fatalf("result = %+v, %v", res, err)
	}
	if resolved != 0 {
		t.Fatal("the cover was stored before the names were checked")
	}
	bi.Credits[0].Person.Name = "Named"
	w := "  "
	bi.Credits[0].With = &BackupPerson{SourceID: "y", Name: w}
	if res, _ := e.lib.RestoreItem(bg, bi, RestoreOptions{}); res.Outcome != RestoreFailed {
		t.Fatalf("counterpart without name = %+v", res)
	}
}

func TestRestoreItemReleasesCoverWhenTheItemIsNotWritten(t *testing.T) {
	e := newEnv(t)
	// a trigger makes the item transaction fail for source id "boom"
	if _, err := e.db.Exec(`CREATE TRIGGER fail_boom BEFORE INSERT ON item_titles WHEN NEW.text = 'Boom'
		BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	var released []string
	opts := RestoreOptions{
		ResolveCover: func(_ context.Context, name string) (string, string, error) { return "stored-" + name, "", nil },
		ReleaseCover: func(_ context.Context, stored string) { released = append(released, stored) },
	}
	ok := BackupItem{PluginID: "book", Kind: "book", Source: "books", SourceID: "ok", Cover: "shared.png", Titles: []plugin.Title{{Lang: "en", Text: "Fine"}}}
	if res, err := e.lib.RestoreItem(bg, ok, opts); err != nil || res.Outcome != RestoreCreated {
		t.Fatalf("ok = %+v, %v", res, err)
	}
	boom := ok
	boom.SourceID, boom.Titles = "boom", []plugin.Title{{Lang: "en", Text: "Boom"}}
	// the shared cover is referenced by the first item: kept
	if _, err := e.lib.RestoreItem(bg, boom, opts); err == nil {
		t.Fatal("trigger did not fail the transaction")
	}
	if len(released) != 0 {
		t.Fatalf("referenced cover released: %v", released)
	}
	// an unreferenced cover of a failed item is released
	boom.Cover = "own.png"
	if _, err := e.lib.RestoreItem(bg, boom, opts); err == nil {
		t.Fatal("trigger did not fail the transaction")
	}
	if !reflect.DeepEqual(released, []string{"stored-own.png"}) {
		t.Fatalf("released = %v", released)
	}
}

func TestRestoreItemManualRulesAndRemoteCoverSync(t *testing.T) {
	e := newEnv(t)
	// richBook makes author required: a manual item without it fails
	manual := BackupItem{PluginID: "book", Kind: "book", Source: "manual", SourceID: "m1", Titles: []plugin.Title{{Lang: "en", Text: "T"}}}
	res, err := e.lib.RestoreItem(bg, manual, RestoreOptions{})
	if err != nil || res.Outcome != RestoreFailed || !strings.Contains(res.Reason, "required") {
		t.Fatalf("manual without required = %+v, %v", res, err)
	}
	// a sourced item may lack it (as in a normal save)
	sourced := manual
	sourced.Source = "books"
	if res, err := e.lib.RestoreItem(bg, sourced, RestoreOptions{}); err != nil || res.Outcome != RestoreCreated || !res.SyncPending {
		t.Fatalf("sourced = %+v, %v", res, err)
	}
	// a manual item is done, unless it has a remote cover to download
	manual.Fields = map[string]plugin.FieldValue{"author": {Text: "A"}}
	res, err = e.lib.RestoreItem(bg, manual, RestoreOptions{})
	if err != nil || res.Outcome != RestoreCreated || res.SyncPending || mustItem(t, e.lib, res.ItemID).SyncState != "done" {
		t.Fatalf("manual = %+v, %v", res, err)
	}
	manual.SourceID, manual.CoverURL = "m2", "https://img.example/m2.jpg"
	res, err = e.lib.RestoreItem(bg, manual, RestoreOptions{})
	if err != nil || res.Outcome != RestoreCreated || !res.SyncPending || mustItem(t, e.lib, res.ItemID).SyncState != "pending" {
		t.Fatalf("manual with remote cover = %+v, %v", res, err)
	}
}

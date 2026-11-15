package backup

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

func (e *env) bookKind() plugin.KindDef {
	e.t.Helper()
	m, _ := e.reg.Manifest("book")
	return m.Kinds[0]
}

func (e *env) itemID(source, id string) int64 {
	e.t.Helper()
	var n int64
	if err := e.db.QueryRow(`SELECT id FROM items WHERE plugin_id = 'book' AND source = ? AND source_id = ?`, source, id).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) seriesByUID(uid string) *library.Series {
	e.t.Helper()
	s, err := e.lib.SeriesByUID(bg, "book", uid)
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func day(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}

// buildSeriesData makes a library with a spin-off series that comes BEFORE
// its parent (lower id), a parent with every value, a spin-off part and a
// cover shared with an item.
func buildSeriesData(t *testing.T) (a *env, cover string, child, parent library.Series) {
	a = newEnv(t)
	cover = a.saveCover(7)
	i1 := a.book("1", cover, &library.Entry{Status: library.Planned})
	i2 := a.book("2", "", &library.Entry{Status: library.Planned})
	i3 := a.book("3", "", &library.Entry{Status: library.Planned})
	i4 := a.book("4", "", &library.Entry{Status: library.Planned})
	i5 := a.book("5", "", &library.Entry{Status: library.Planned})
	child = a.seriesOf("Side", i4)
	parent = a.seriesOf("Saga", i1, i2, i3)
	if err := a.lib.SetPartSpinOff(bg, parent.ID, i2, true); err != nil {
		t.Fatal(err)
	}
	k := a.bookKind()
	if err := a.lib.UpdateSeriesDetails(bg, parent.ID, k, library.SeriesDetails{
		Title: "Saga", CoverPath: cover, Description: "Line one\nline two",
		StartedOn: day(2001, 2, 3), EndedOn: day(2009, 10, 11), Finished: true, PartsTotal: 12, LinkedItemID: i5,
		Fields: map[string]plugin.FieldValue{"volumes": {Num: fp(3)}, "publisher": {Text: "Pub"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.lib.UpdateSeriesDetails(bg, child.ID, k, library.SeriesDetails{Title: "Side", ParentID: parent.ID}); err != nil {
		t.Fatal(err)
	}
	return a, cover, *a.seriesByUID(child.UID), *a.seriesByUID(parent.UID)
}

func TestBackupRoundTripKeepsSeriesData(t *testing.T) {
	a, cover, child, parent := buildSeriesData(t)
	data := a.archive()
	_, files := open(t, data)
	var coverEntries []string
	for name := range files {
		if strings.HasPrefix(name, "covers/") {
			coverEntries = append(coverEntries, name)
		}
	}
	if len(coverEntries) != 1 || coverEntries[0] != "covers/"+cover {
		t.Fatalf("cover entries = %v, want the shared cover once", coverEntries)
	}
	js := string(files["backup.json"])
	if !strings.Contains(js, `"spin_off":true`) || !strings.Contains(js, `"parent":"`+parent.UID+`"`) {
		t.Fatalf("series data missing: %s", js)
	}

	b := newEnv(t)
	rep := mustRestore(t, b, data)
	if rep.SeriesCreated != 2 || rep.SeriesSkipped != 0 || len(rep.Notes) != 0 {
		t.Fatalf("report = %+v", rep)
	}
	got := b.seriesByUID(parent.UID)
	if got.CoverPath != cover || got.Description != "Line one\nline two" || !got.Finished || got.PartsTotal != 12 ||
		got.StartedOn == nil || !got.StartedOn.Equal(*day(2001, 2, 3)) || got.EndedOn == nil || !got.EndedOn.Equal(*day(2009, 10, 11)) {
		t.Fatalf("values = %+v", got)
	}
	if !got.CreatedAt.Equal(parent.CreatedAt) || !got.UpdatedAt.Equal(parent.UpdatedAt) {
		t.Fatalf("stamps %v %v, want %v %v", got.CreatedAt, got.UpdatedAt, parent.CreatedAt, parent.UpdatedAt)
	}
	if got.LinkedItemID != b.itemID("books", "5") {
		t.Fatalf("linked = %d, want book 5", got.LinkedItemID)
	}
	if v := got.Fields["volumes"]; v.Num == nil || *v.Num != 3 || got.Fields["publisher"].Text != "Pub" || len(got.Fields) != 2 {
		t.Fatalf("fields = %+v", got.Fields)
	}
	if ref, err := b.lib.ImageReferenced(bg, cover); err != nil || !ref {
		t.Fatalf("cover not referenced: %v %v", ref, err)
	}
	parts, err := b.lib.SeriesPartsOrder(bg, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []library.SeriesPart{
		{ItemID: b.itemID("books", "1"), Position: 1},
		{ItemID: b.itemID("books", "3"), Position: 2},
		{ItemID: b.itemID("books", "2"), Position: 1, SpinOff: true},
	}
	if !reflect.DeepEqual(parts, want) {
		t.Fatalf("parts = %+v, want %+v", parts, want)
	}
	gotChild := b.seriesByUID(child.UID)
	if gotChild.ParentID != got.ID {
		t.Fatalf("child parent = %d, want %d", gotChild.ParentID, got.ID)
	}
	if !gotChild.UpdatedAt.Equal(child.UpdatedAt) {
		t.Fatalf("child stamp %v, want %v", gotChild.UpdatedAt, child.UpdatedAt)
	}

	// re-restoring the same archive changes nothing
	rep = mustRestore(t, b, data)
	if rep.SeriesCreated != 0 || rep.SeriesSkipped != 2 || len(rep.Notes) != 0 {
		t.Fatalf("second report = %+v", rep)
	}
	if again := b.seriesByUID(child.UID); again.ParentID != got.ID {
		t.Fatalf("child parent after re-restore = %d", again.ParentID)
	}
}

// oldSeries is the series object as a reader from before series data
// decodes it.
type oldSeries struct {
	UID      string `json:"uid"`
	PluginID string `json:"plugin_id"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Parts    []struct {
		PluginID string `json:"plugin_id"`
		Kind     string `json:"kind"`
		Source   string `json:"source"`
		SourceID string `json:"source_id"`
	} `json:"parts"`
}

func TestNewSeriesMemberReadsInOldShape(t *testing.T) {
	a, _, _, parent := buildSeriesData(t)
	_, files := open(t, a.archive())
	var d struct {
		Series []oldSeries `json:"series"`
	}
	if err := json.Unmarshal(files["backup.json"], &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Series) != 2 || d.Series[1].UID != parent.UID || d.Series[1].Title != "Saga" || len(d.Series[1].Parts) != 3 ||
		d.Series[1].Parts[0].SourceID != "1" || d.Series[1].Parts[1].SourceID != "3" || d.Series[1].Parts[2].SourceID != "2" {
		t.Fatalf("old reader got %+v", d.Series)
	}
}

func TestRestoreOldSeriesMember(t *testing.T) {
	b := newEnv(t)
	b.book("1", "", &library.Entry{Status: library.Planned})
	body := `{"format":"allister.backup","format_version":1,"items":[],"series":[` + seriesJSON("u-old", "Old", "1") + `]}`
	rep := mustRestore(t, b, zipOf(t, entry{name: "backup.json", body: []byte(body)}))
	if rep.SeriesCreated != 1 || len(rep.Notes) != 0 {
		t.Fatalf("report = %+v", rep)
	}
	s := b.seriesByUID("u-old")
	if s.CoverPath != "" || s.Description != "" || s.StartedOn != nil || s.Finished || s.PartsTotal != 0 ||
		s.LinkedItemID != 0 || s.ParentID != 0 || len(s.Fields) != 0 {
		t.Fatalf("values = %+v", s)
	}
}

// seriesWith is seriesJSON with extra members spliced in.
func seriesWith(uid, title, extra string, parts ...string) string {
	s := seriesJSON(uid, title, parts...)
	return s[:1] + extra + "," + s[1:]
}

func TestRestoreSeriesMissingLinkAndParent(t *testing.T) {
	b := newEnv(t)
	b.book("1", "", &library.Entry{Status: library.Planned})
	b.book("2", "", &library.Entry{Status: library.Planned})
	b.book("3", "", nil) // not in the list
	body := `{"format":"allister.backup","format_version":1,"items":[],"series":[` +
		seriesWith("u-a", "Lost", `"linked":{"plugin_id":"book","kind":"book","source":"books","source_id":"gone"},"parent":"u-nowhere","finished":true`, "1") + `,` +
		seriesWith("u-b", "Unlisted", `"linked":{"plugin_id":"book","kind":"book","source":"books","source_id":"3"}`, "2") + `]}`
	rep := mustRestore(t, b, zipOf(t, entry{name: "backup.json", body: []byte(body)}))
	if rep.SeriesCreated != 2 || rep.SeriesSkipped != 0 {
		t.Fatalf("report = %+v", rep)
	}
	notes := map[string][]string{}
	for _, n := range rep.Notes {
		notes[n.Title] = append(notes[n.Title], n.Reason)
	}
	lost := strings.Join(notes["Lost"], " | ")
	if len(notes["Lost"]) != 2 || !strings.Contains(lost, "linked entry books:gone") || !strings.Contains(lost, "parent series u-nowhere") {
		t.Fatalf("notes of Lost = %q", notes["Lost"])
	}
	if len(notes["Unlisted"]) != 1 || !strings.Contains(notes["Unlisted"][0], "not in your list") {
		t.Fatalf("notes of Unlisted = %q", notes["Unlisted"])
	}
	a := b.seriesByUID("u-a")
	if a.LinkedItemID != 0 || a.ParentID != 0 || !a.Finished {
		t.Fatalf("u-a = %+v", a)
	}
	if u := b.seriesByUID("u-b"); u.LinkedItemID != 0 {
		t.Fatalf("u-b = %+v", u)
	}
}

func TestRestoreSeriesDropsBadValuesOnly(t *testing.T) {
	b := newEnv(t)
	b.book("1", "", &library.Entry{Status: library.Planned})
	b.book("2", "", &library.Entry{Status: library.Planned})
	b.book("3", "", &library.Entry{Status: library.Planned})
	b.book("4", "", &library.Entry{Status: library.Planned})
	bad := `"cover":"../evil.png","description":"a\u0001b","started_on":"2024-13-45","ended_on":"2020-05-06",` +
		`"parts_total":-3,"finished":true,"fields":{"volumes":{"num":-1},"publisher":{"text":"Kept"},"nope":{"text":"x"}}`
	body := `{"format":"allister.backup","format_version":1,"items":[],"series":[` +
		seriesWith("u-bad", "Bad", bad, "1") + `,` +
		seriesWith("u-type", "Typed", `"parts_total":"many","description":"Fine"`, "2") + `,` +
		seriesWith("u-self", "Self", `"parent":"u-self"`, "3") + `,` +
		seriesWith("u-title", "", `"description":"x"`, "4") + `]}`
	rep := mustRestore(t, b, zipOf(t, entry{name: "backup.json", body: []byte(body)}))
	if rep.SeriesCreated != 3 || rep.SeriesSkipped != 1 {
		t.Fatalf("report = %+v", rep)
	}
	notes := map[string][]string{}
	for _, n := range rep.Notes {
		notes[n.Title] = append(notes[n.Title], n.Reason)
	}
	all := strings.Join(notes["Bad"], " | ")
	for _, want := range []string{"cover", "description", "started_on", "parts_total", "volumes", "nope"} {
		if !strings.Contains(all, want) {
			t.Errorf("no note names %q: %q", want, notes["Bad"])
		}
	}
	s := b.seriesByUID("u-bad")
	if s.CoverPath != "" || s.Description != "" || s.StartedOn != nil || s.EndedOn == nil || !s.EndedOn.Equal(*day(2020, 5, 6)) ||
		s.PartsTotal != 0 || !s.Finished || len(s.Fields) != 1 || s.Fields["publisher"].Text != "Kept" {
		t.Fatalf("u-bad = %+v", s)
	}
	typed := b.seriesByUID("u-type")
	if typed.Description != "Fine" || typed.PartsTotal != 0 || len(notes["Typed"]) != 1 || !strings.Contains(notes["Typed"][0], "parts_total") {
		t.Fatalf("u-type = %+v notes %q", typed, notes["Typed"])
	}
	if self := b.seriesByUID("u-self"); self.ParentID != 0 || len(notes["Self"]) != 1 || !strings.Contains(notes["Self"][0], "own parent") {
		t.Fatalf("u-self = %+v notes %q", self, notes["Self"])
	}
	if len(notes[""]) != 1 || !strings.Contains(notes[""][0], "not valid") {
		t.Fatalf("invalid title notes = %q", notes[""])
	}
}

// TestBackupRoundTripPartlessSeries (R6): a series kept without parts (for
// its own data, or as the parent of a spin-off series) is archived with an
// empty parts list and restored; one archived without parts or own values
// that nothing names as parent is not created.
func TestBackupRoundTripPartlessSeries(t *testing.T) {
	a := newEnv(t)
	k := a.bookKind()
	i1 := a.book("1", "", &library.Entry{Status: library.Planned})
	i2 := a.book("2", "", &library.Entry{Status: library.Planned})
	i3 := a.book("3", "", &library.Entry{Status: library.Planned})
	own := a.seriesOf("Own", i1)
	if err := a.lib.UpdateSeriesDetails(bg, own.ID, k, library.SeriesDetails{Title: "Own", Description: "Kept", PartsTotal: 3}); err != nil {
		t.Fatal(err)
	}
	parent := a.seriesOf("Parent", i2)
	kid := a.seriesOf("Kid", i3)
	if err := a.lib.UpdateSeriesDetails(bg, kid.ID, k, library.SeriesDetails{Title: "Kid", ParentID: parent.ID}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct{ s, i int64 }{{own.ID, i1}, {parent.ID, i2}} {
		if err := a.lib.RemovePart(bg, p.s, p.i); err != nil {
			t.Fatal(err)
		}
	}
	data := a.archive()
	_, files := open(t, data)
	var d struct {
		Series []library.BackupSeries `json:"series"`
	}
	if err := json.Unmarshal(files["backup.json"], &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Series) != 3 || len(d.Series[0].Parts) != 0 || len(d.Series[1].Parts) != 0 || d.Series[0].Description != "Kept" {
		t.Fatalf("archived %+v", d.Series)
	}
	if !strings.Contains(string(files["backup.json"]), `"parts":[]`) {
		t.Fatal("a part-less series has no parts list")
	}
	b := newEnv(t)
	rep := mustRestore(t, b, data)
	if rep.SeriesCreated != 3 || rep.SeriesSkipped != 0 || len(rep.Notes) != 0 {
		t.Fatalf("report = %+v", rep)
	}
	if got := b.seriesByUID(own.UID); got.Description != "Kept" || got.PartsTotal != 3 {
		t.Fatalf("own = %+v", got)
	}
	gotParent := b.seriesByUID(parent.UID)
	if gotKid := b.seriesByUID(kid.UID); gotKid.ParentID != gotParent.ID {
		t.Fatalf("kid parent = %d, want %d", gotKid.ParentID, gotParent.ID)
	}
	body := `{"format":"allister.backup","format_version":1,"items":[],"series":[` + seriesJSON("u-none", "Nothing") + `]}`
	rep = mustRestore(t, b, zipOf(t, entry{name: "backup.json", body: []byte(body)}))
	if rep.SeriesCreated != 0 || rep.SeriesSkipped != 1 || len(rep.Notes) != 1 || !strings.Contains(rep.Notes[0].Reason, "no parts") {
		t.Fatalf("empty series report = %+v", rep)
	}
	if _, err := b.lib.SeriesByUID(bg, "book", "u-none"); err == nil {
		t.Fatal("an empty series nothing names as parent was restored")
	}
}

// TestRestoreSeriesParentChainDepthTwo: an archive with a chain A -> B -> C
// (A a spin-off of B, B of C) restores at most one level of it; the refused
// link is left empty with a note.
func TestRestoreSeriesParentChainDepthTwo(t *testing.T) {
	b := newEnv(t)
	for _, id := range []string{"1", "2", "3"} {
		b.book(id, "", &library.Entry{Status: library.Planned})
	}
	body := `{"format":"allister.backup","format_version":1,"items":[],"series":[` +
		seriesWith("u-a", "A", `"parent":"u-b"`, "1") + `,` +
		seriesWith("u-b", "B", `"parent":"u-c"`, "2") + `,` +
		seriesJSON("u-c", "C", "3") + `]}`
	rep := mustRestore(t, b, zipOf(t, entry{name: "backup.json", body: []byte(body)}))
	if rep.SeriesCreated != 3 {
		t.Fatalf("report = %+v", rep)
	}
	sa, sb, sc := b.seriesByUID("u-a"), b.seriesByUID("u-b"), b.seriesByUID("u-c")
	set := 0
	if sa.ParentID == sb.ID {
		set++
	}
	if sb.ParentID == sc.ID {
		set++
	}
	if set != 1 || sc.ParentID != 0 || (sa.ParentID != 0 && sa.ParentID != sb.ID) {
		t.Fatalf("parents a=%d b=%d c=%d (ids %d %d %d), want exactly one level", sa.ParentID, sb.ParentID, sc.ParentID, sa.ID, sb.ID, sc.ID)
	}
	if sa.ParentID != 0 && sb.ParentID != 0 {
		t.Fatal("a two-level chain was restored")
	}
	if len(rep.Notes) != 1 || !strings.Contains(rep.Notes[0].Reason, "parent series") || strings.Contains(rep.Notes[0].Reason, "invalid: ") {
		t.Fatalf("notes = %+v", rep.Notes)
	}
}

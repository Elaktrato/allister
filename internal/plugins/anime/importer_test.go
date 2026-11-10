package anime

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
)

func importer(t *testing.T) plugin.Importer {
	t.Helper()
	i := New(Options{}).Importer("yamtrack")
	if i == nil {
		t.Fatal("no yamtrack importer")
	}
	return i
}

func parseCSV(t *testing.T, csv string) plugin.ParseResult {
	t.Helper()
	res, err := importer(t).Parse(context.Background(), strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func day(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}

func TestImporterMapsRows(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("yamtrack", "testdata", "sample.csv"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := importer(t).Parse(context.Background(), bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 6 {
		t.Fatalf("entries = %d: %+v", len(res.Entries), res.Entries)
	}
	by := map[string]plugin.ParsedEntry{}
	for _, e := range res.Entries {
		by[e.ExternalIDs["mal"]] = e
		if e.Kind != "anime" {
			t.Errorf("kind = %q", e.Kind)
		}
	}
	rating := func(e plugin.ParsedEntry) int {
		if e.Rating == nil {
			return -1
		}
		return *e.Rating
	}
	type want struct {
		title    string
		status   plugin.StatusClass
		rating   int
		progress float64
		cover    string
	}
	for id, w := range map[string]want{
		"1001": {"Completed Sample", plugin.Completed, 8, 12, "https://img.example/1001.jpg"},
		"1002": {"Planned Sample", plugin.Planned, -1, 0, "https://img.example/1002.jpg"},
		"1003": {"Dropped Sample", plugin.Dropped, -1, 3, "https://img.example/1003.jpg"},
		"1004": {"Paused Sample", plugin.Paused, 6, 5, ""},
		"1005": {"Watching Sample", plugin.Active, 7, 2, "https://img.example/1005.jpg"},
		"1006": {`Title, with "quotes" and comma`, plugin.Planned, -1, 0, "https://img.example/1006.jpg"},
	} {
		e, ok := by[id]
		if !ok {
			t.Errorf("%s missing", id)
			continue
		}
		if e.Title != w.title || e.Status != w.status || rating(e) != w.rating || e.CoverURL != w.cover {
			t.Errorf("%s = %+v, want %+v", id, e, w)
		}
		if p, ok := e.Progress["episode"]; !ok || p != w.progress || len(e.Progress) != 1 {
			t.Errorf("%s progress = %v, want episode=%v", id, e.Progress, w.progress)
		}
	}
	c := by["1001"]
	if c.Comment != "Great ending" || !c.StartedOn.Equal(*day(2024, 1, 5)) || !c.FinishedOn.Equal(*day(2024, 2, 10)) {
		t.Errorf("1001 comment/dates = %q %v %v", c.Comment, c.StartedOn, c.FinishedOn)
	}
	if by["1002"].StartedOn != nil || by["1002"].FinishedOn != nil {
		t.Errorf("1002 dates must be nil")
	}
	if by["1006"].Comment != "line one\nline two" {
		t.Errorf("1006 comment = %q", by["1006"].Comment)
	}
	// skipped: the three other-media rows are ignored and not reported
	reasons := map[string]plugin.SkippedRow{}
	for _, s := range res.Skipped {
		reasons[s.Title] = s
		if s.Line <= 0 || s.Reason == "" {
			t.Errorf("skipped row = %+v", s)
		}
	}
	if len(res.Skipped) != 4 {
		t.Errorf("skipped = %+v, want 4 (weird status, other source, duplicate, bad id)", res.Skipped)
	}
	for _, title := range []string{"Weird Status Sample", "Other Source Sample", "Duplicate Of First", "Bad Id Sample"} {
		if reasons[title].Reason == "" {
			t.Errorf("%q not reported: %+v", title, res.Skipped)
		}
	}
	for _, title := range []string{"Game Sample", "Manga Sample", "Episode Sample"} {
		if _, ok := reasons[title]; ok {
			t.Errorf("%q is another media type and must not be reported", title)
		}
	}
	if res.Ignored != 3 {
		t.Errorf("ignored = %d, want the 3 other-media rows counted", res.Ignored)
	}
	if !strings.Contains(reasons["Weird Status Sample"].Reason, "unknown status") {
		t.Errorf("reason = %q", reasons["Weird Status Sample"].Reason)
	}
}

func TestImporterEveryStatus(t *testing.T) {
	for in, want := range map[string]plugin.StatusClass{
		"Completed": plugin.Completed, "Planning": plugin.Planned, "Dropped": plugin.Dropped,
		"Paused": plugin.Paused, "In progress": plugin.Active,
	} {
		res := parseCSV(t, "media_id,media_type,status,title\n5,anime,"+in+",T\n")
		if len(res.Entries) != 1 || res.Entries[0].Status != want {
			t.Errorf("status %q -> %+v, want %s", in, res.Entries, want)
		}
	}
}

func TestImporterScoreDateAndNotes(t *testing.T) {
	csv := "media_id,media_type,status,title,score,start_date,end_date,progress\n" +
		`1,anime,Completed,,7.5,2024-01-05 23:30:00-05:00,nonsense,abc` + "\n" +
		`2,anime,Completed,T,0.0,,,3` + "\n" +
		`3,anime,Completed,T,-2,,,` + "\n"
	res := parseCSV(t, csv)
	if len(res.Entries) != 3 {
		t.Fatalf("entries = %+v", res.Entries)
	}
	a := res.Entries[0]
	if a.Title != "MAL #1" || a.Rating == nil || *a.Rating != 8 {
		t.Errorf("a = %+v", a)
	}
	if !a.StartedOn.Equal(*day(2024, 1, 5)) {
		t.Errorf("start = %v, want the written calendar day 2024-01-05", a.StartedOn)
	}
	if a.FinishedOn != nil || a.Progress["episode"] != 0 {
		t.Errorf("finish/progress = %v %v", a.FinishedOn, a.Progress)
	}
	joined := strings.Join(a.Notes, "|")
	for _, s := range []string{"no title in the export, using MAL #1", "score 7.5 rounded to 8", `invalid end date "nonsense"`, `invalid progress "abc"`} {
		if !strings.Contains(joined, s) {
			t.Errorf("notes %q miss %q", joined, s)
		}
	}
	if res.Entries[1].Rating != nil {
		t.Errorf("score 0 must be unrated")
	}
	if res.Entries[2].Rating != nil || !strings.Contains(strings.Join(res.Entries[2].Notes, "|"), "left unrated") {
		t.Errorf("negative score = %+v", res.Entries[2])
	}
}

func TestImporterSanitisesFileText(t *testing.T) {
	csv := "media_id,media_type,status,title,image,notes\n" +
		"1,anime,Completed,\"Two\nLines\tTitle\",//cdn.example/x.jpg,\"a\x00b\r\nc\"\n" +
		"2,anime,Completed,T,javascript:alert(1),\n"
	res := parseCSV(t, csv)
	e := res.Entries[0]
	if e.Title != "Two Lines Title" || e.CoverURL != "https://cdn.example/x.jpg" || e.Comment != "ab\nc" {
		t.Errorf("entry = %+v", e)
	}
	if res.Entries[1].CoverURL != "" {
		t.Errorf("cover = %q", res.Entries[1].CoverURL)
	}
}

func TestImporterBadFormat(t *testing.T) {
	for name, in := range map[string]string{"empty": "", "missing columns": "a,b\n1,2\n"} {
		_, err := importer(t).Parse(context.Background(), strings.NewReader(in))
		if !errors.Is(err, plugin.ErrBadInput) {
			t.Errorf("%s: err = %v, want ErrBadInput", name, err)
		}
	}
}

func TestImporterReadErrorKeepsItsCause(t *testing.T) {
	_, err := importer(t).Parse(context.Background(), errReader{})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want the read error", err)
	}
	if errors.Is(err, plugin.ErrBadInput) {
		t.Errorf("an I/O error is not bad input: %v", err)
	}
	if !errors.Is(err, errBoom) {
		t.Errorf("cause not preserved: %v", err)
	}
}

func TestImporterTruncatedCommentIsNoted(t *testing.T) {
	long := strings.Repeat("é", 2500)
	res := parseCSV(t, "media_id,media_type,status,title,notes\n1,anime,Completed,T,"+long+"\n2,anime,Completed,T,short\n")
	if n := len([]rune(res.Entries[0].Comment)); n != 2000 {
		t.Errorf("comment runes = %d", n)
	}
	if !strings.Contains(strings.Join(res.Entries[0].Notes, "|"), "comment truncated to 2000 characters") {
		t.Errorf("notes = %v", res.Entries[0].Notes)
	}
	if len(res.Entries[1].Notes) != 0 {
		t.Errorf("notes = %v", res.Entries[1].Notes)
	}
}

type errReader struct{}

var errBoom = errors.New("boom")

func (errReader) Read([]byte) (int, error) { return 0, errBoom }

func TestImporterCancelledContext(t *testing.T) {
	c, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := importer(t).Parse(c, strings.NewReader("media_id,media_type,status,title\n"))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestPluginUnknownIDsReturnNil(t *testing.T) {
	p := New(Options{})
	if p.Source("nope") != nil || p.Source("") != nil || p.Importer("nope") != nil || p.Exporter("nope") != nil {
		t.Error("unknown ids must return nil")
	}
	if p.Source("yamtrack") != nil || p.Importer("anilist") != nil || p.Exporter("anilist") != nil {
		t.Error("ids are per kind of thing")
	}
	if p.Source("anilist") == nil || p.Importer("yamtrack") == nil || p.Exporter("json") == nil {
		t.Error("declared ids must resolve")
	}
}

// AniList reports MyAnimeList ids in canonical decimal form, so the export's
// id loses its leading zeros: "0123" must match a saved item with mal 123
// (and is a duplicate of "123").
func TestImporterCanonicalMALID(t *testing.T) {
	res := parseCSV(t, "media_id,media_type,status,title\n0123,anime,Completed,\n123,anime,Planning,Dup\n")
	if len(res.Entries) != 1 {
		t.Fatalf("entries = %+v", res.Entries)
	}
	if e := res.Entries[0]; e.ExternalIDs["mal"] != "123" || e.Title != "MAL #123" {
		t.Errorf("entry = %+v", e)
	}
	if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Reason, "duplicate media_id 123") {
		t.Errorf("skipped = %+v", res.Skipped)
	}
}

package yamtrack

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/store"
)

func sample(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/sample.csv")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func parseSample(t *testing.T) ([]Row, []SkippedRow) {
	t.Helper()
	rows, skipped, err := Parse(bytes.NewReader(sample(t)))
	if err != nil {
		t.Fatal(err)
	}
	return rows, skipped
}

func day(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}

func TestParseTakesAnimeRowsOnly(t *testing.T) {
	rows, skipped := parseSample(t)
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.MALID)
	}
	if got := strings.Join(ids, ","); got != "1001,1002,1003,1004,1005,1006" {
		t.Fatalf("rows = %s", got)
	}
	reasons := map[string]string{}
	ignored := 0
	for _, s := range skipped {
		reasons[s.Title] = s.Reason
		if s.Ignored {
			ignored++
		}
	}
	if ignored != 3 {
		t.Errorf("ignored = %d, want 3 (game, manga, episode): %+v", ignored, skipped)
	}
	for _, title := range []string{"Game Sample", "Manga Sample", "Episode Sample"} {
		if reasons[title] == "" {
			t.Errorf("%q not reported as skipped", title)
		}
	}
	if len(skipped) != 7 {
		t.Errorf("skipped = %d, want 7: %+v", len(skipped), skipped)
	}
}

func TestParseMapsFields(t *testing.T) {
	rows, _ := parseSample(t)
	byID := map[string]Row{}
	for _, r := range rows {
		byID[r.MALID] = r
	}
	c := byID["1001"]
	if c.Title != "Completed Sample" || c.ImageURL != "https://img.example/1001.jpg" || c.Status != store.Completed ||
		c.Rating == nil || *c.Rating != 8 || c.Progress != 12 || c.Comment != "Great ending" {
		t.Errorf("completed row: %+v", c)
	}
	if c.StartedOn == nil || !c.StartedOn.Equal(*day(2024, 1, 5)) || c.FinishedOn == nil || !c.FinishedOn.Equal(*day(2024, 2, 10)) {
		t.Errorf("dates: %v %v", c.StartedOn, c.FinishedOn)
	}
	if r := byID["1002"]; r.Status != store.PlanToWatch || r.Rating != nil || r.StartedOn != nil || r.FinishedOn != nil || r.Progress != 0 {
		t.Errorf("planning row: %+v", r)
	}
	if r := byID["1003"]; r.Status != store.Dropped || r.Rating != nil || r.StartedOn == nil || r.FinishedOn != nil {
		t.Errorf("dropped row (0.0 score is unrated): %+v", r)
	}
	if r := byID["1004"]; r.Status != store.Paused || r.Rating == nil || *r.Rating != 6 {
		t.Errorf("paused row: %+v", r)
	}
	if r := byID["1005"]; r.Status != store.Watching || r.Progress != 2 {
		t.Errorf("watching row: %+v", r)
	}
	if r := byID["1006"]; r.Title != `Title, with "quotes" and comma` || r.Comment != "line one\nline two" {
		t.Errorf("quoting row: %q %q", r.Title, r.Comment)
	}
}

func TestParseReportsUnknownStatus(t *testing.T) {
	_, skipped := parseSample(t)
	for _, s := range skipped {
		if s.Title == "Weird Status Sample" {
			if !strings.Contains(s.Reason, "unknown status") || !strings.Contains(s.Reason, "On hold forever") || s.Ignored || s.Line == 0 {
				t.Fatalf("skipped = %+v", s)
			}
			return
		}
	}
	t.Fatal("unknown status row not reported")
}

func TestParseSkipReasons(t *testing.T) {
	_, skipped := parseSample(t)
	want := map[string]string{
		"Other Source Sample": "source",
		"Duplicate Of First":  "duplicate media_id",
		"Bad Id Sample":       "media_id",
	}
	for _, s := range skipped {
		if w, ok := want[s.Title]; ok {
			if !strings.Contains(s.Reason, w) || s.Ignored {
				t.Errorf("%s: %+v", s.Title, s)
			}
			delete(want, s.Title)
		}
	}
	if len(want) != 0 {
		t.Errorf("not reported: %v", want)
	}
}

func TestParseToleratesBOM(t *testing.T) {
	bom := append([]byte{0xEF, 0xBB, 0xBF}, sample(t)...)
	rows, _, err := Parse(bytes.NewReader(bom))
	if err != nil || len(rows) != 6 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	crlf := bytes.ReplaceAll(sample(t), []byte("\n"), []byte("\r\n"))
	rows, _, err = Parse(bytes.NewReader(crlf))
	if err != nil || len(rows) != 6 {
		t.Fatalf("crlf: rows=%d err=%v", len(rows), err)
	}
}

func TestParseMissingRequiredColumn(t *testing.T) {
	all := []string{"media_id", "media_type", "status", "title"}
	for _, col := range all {
		var hdr []string
		for _, c := range all {
			if c != col {
				hdr = append(hdr, `"`+c+`"`)
			}
		}
		_, _, err := Parse(strings.NewReader(strings.Join(hdr, ",") + "\n"))
		if !errors.Is(err, ErrBadFormat) || !strings.Contains(err.Error(), col) {
			t.Errorf("missing %s: err = %v", col, err)
		}
	}
	for _, in := range []string{"", "\n", "\xff\xfe binary \x00\x01"} {
		if _, _, err := Parse(strings.NewReader(in)); !errors.Is(err, ErrBadFormat) {
			t.Errorf("%q: err = %v", in, err)
		}
	}
}

func parseOne(t *testing.T, fields string) (Row, []SkippedRow) {
	t.Helper()
	in := `media_id,source,media_type,title,image,score,status,notes,start_date,end_date,progress` + "\n" + fields + "\n"
	rows, skipped, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		return Row{}, skipped
	}
	return rows[0], skipped
}

func TestParseScoreMapping(t *testing.T) {
	cases := []struct {
		score string
		want  int // -1 = nil
	}{
		{"", -1}, {"0.0", -1}, {"0", -1}, {"8.0", 8}, {"7.5", 8}, {"7.4", 7}, {"10.0", 10},
		{"11", 10}, {"1", 1}, {" 9.0 ", 9},
	}
	for _, c := range cases {
		r, sk := parseOne(t, `1,mal,anime,T,,"`+c.score+`",Completed,,,,1`)
		if len(sk) != 0 {
			t.Errorf("score %q skipped: %+v", c.score, sk)
			continue
		}
		switch {
		case c.want < 0 && r.Rating != nil:
			t.Errorf("score %q = %d, want nil", c.score, *r.Rating)
		case c.want >= 0 && (r.Rating == nil || *r.Rating != c.want):
			t.Errorf("score %q = %v, want %d", c.score, r.Rating, c.want)
		}
	}
	if _, sk := parseOne(t, `1,mal,anime,T,,abc,Completed,,,,1`); len(sk) != 1 {
		t.Errorf("non-numeric score must be reported: %+v", sk)
	}
}

func TestParseStatusMapping(t *testing.T) {
	cases := map[string]store.Status{
		"Completed": store.Completed, "Planning": store.PlanToWatch, "Dropped": store.Dropped,
		"Paused": store.Paused, "In progress": store.Watching, " in progress ": store.Watching,
	}
	for in, want := range cases {
		r, sk := parseOne(t, `1,mal,anime,T,,,"`+in+`",,,,0`)
		if len(sk) != 0 || r.Status != want {
			t.Errorf("%q = %q (%v)", in, r.Status, sk)
		}
	}
}

func TestParseDateKeepsWrittenCalendarDay(t *testing.T) {
	cases := []struct {
		in string
		y  int
		m  time.Month
		d  int
	}{
		{"2024-03-05 00:30:00+09:00", 2024, 3, 5}, // 2024-03-04 in UTC
		{"2024-03-05 23:30:00-08:00", 2024, 3, 5}, // 2024-03-06 in UTC
		{"2024-03-05 12:00:00+00:00", 2024, 3, 5},
		{"2024-12-31 23:59:59.999999+01:00", 2024, 12, 31},
	}
	for _, c := range cases {
		r, _ := parseOne(t, `1,mal,anime,T,,,Completed,,"`+c.in+`","`+c.in+`",1`)
		want := day(c.y, c.m, c.d)
		if r.StartedOn == nil || !r.StartedOn.Equal(*want) || r.StartedOn.Location() != time.UTC {
			t.Errorf("%s: start = %v, want %v", c.in, r.StartedOn, want)
		}
		if r.FinishedOn == nil || !r.FinishedOn.Equal(*want) {
			t.Errorf("%s: finish = %v", c.in, r.FinishedOn)
		}
	}
	if r, _ := parseOne(t, `1,mal,anime,T,,,Completed,,garbage,,1`); r.StartedOn != nil {
		t.Errorf("garbage date = %v, want nil", r.StartedOn)
	}
}

func TestParseCapsTitleAndComment(t *testing.T) {
	long := strings.Repeat("あ", 400)
	r, _ := parseOne(t, `1,mal,anime,"`+long+`",,,Completed,"  `+strings.Repeat("n", 2500)+`  ",,,1`)
	if n := len([]rune(r.Title)); n != 300 {
		t.Errorf("title runes = %d", n)
	}
	if n := len([]rune(r.Comment)); n != 2000 {
		t.Errorf("comment runes = %d", n)
	}
	if r, _ := parseOne(t, `7,mal,anime,"",,,Completed,,,,1`); r.Title != "MAL #7" {
		t.Errorf("empty title = %q", r.Title)
	}
}

func TestParseBadProgress(t *testing.T) {
	for _, p := range []string{"x", "-1", "1.5"} {
		if _, sk := parseOne(t, `1,mal,anime,T,,,Completed,,,,`+p); len(sk) != 1 || !strings.Contains(sk[0].Reason, "progress") {
			t.Errorf("progress %q: %+v", p, sk)
		}
	}
}

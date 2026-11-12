package book

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anilist"
)

// yamtrackNovelCSV is a synthetic Yamtrack general export: two novels, a manga
// and a one-shot row, a mangaupdates row, an id AniList does not know, a book
// row, an unknown status and a duplicate of the first novel.
const yamtrackNovelCSV = `media_id,source,media_type,title,image,season_number,episode_number,score,status,notes,start_date,end_date,progress
1,mal,manga,Novel One,https://img.example/one.jpg,,,8.0,Completed,a comment,2026-01-01 00:00:00+00:00,2026-02-01 00:00:00+00:00,19
2,mal,manga,Manga Two,,,,7.0,Completed,,,,5
3,mal,manga,One Shot Three,,,,6.0,Completed,,,,1
40,mangaupdates,manga,Novel Forty,,,,6.0,Completed,,,,1
999,mal,manga,Missing Novel,,,,6.0,Completed,,,,1
50,hardcover,book,Book Fifty,,,,6.0,Completed,,,,1
4,mal,manga,Novel Four,,,,0.0,Completed,,,,0
5,mal,manga,Unknown Status,,,,7.0,Sometimes,,,,1
1,mal,manga,Novel One,,,,8.0,Completed,,,,19
`

// novelFormatFake answers the batch lookup like AniList would for the synthetic
// export: 1 and 4 are novels, 2 is a manga and 3 a one-shot. 999 is unknown.
func novelFormatFake() *fakeNovelClient {
	return &fakeNovelClient{formats: map[int]string{1: "NOVEL", 2: "MANGA", 3: "ONE_SHOT", 4: "NOVEL"}}
}

func TestYamtrackNovelImporterRows(t *testing.T) {
	c := novelFormatFake()
	res, err := yamtrackNovelImporter{c: c}.Parse(context.Background(), strings.NewReader(yamtrackNovelCSV))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 2 || res.Ignored != 5 || len(res.Skipped) != 2 {
		t.Fatalf("entries=%+v ignored=%d skipped=%+v", res.Entries, res.Ignored, res.Skipped)
	}
	started := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	finished := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	first := res.Entries[0]
	if first.Kind != KindSeries || first.Title != "Novel One" || len(first.ExternalIDs) != 1 ||
		first.ExternalIDs["mal"] != "1" || first.Status != plugin.Completed ||
		first.Rating == nil || *first.Rating != 8 ||
		len(first.Progress) != 1 || first.Progress["chapter"] != 19 ||
		first.StartedOn == nil || !first.StartedOn.Equal(started) ||
		first.FinishedOn == nil || !first.FinishedOn.Equal(finished) ||
		first.Comment != "a comment" || first.CoverURL != "https://img.example/one.jpg" || len(first.Notes) != 0 {
		t.Fatalf("first = %+v", first)
	}
	second := res.Entries[1]
	if second.Kind != KindSeries || second.Title != "Novel Four" || second.ExternalIDs["mal"] != "4" ||
		second.Status != plugin.Completed || second.Rating != nil ||
		len(second.Progress) != 1 || second.Progress["chapter"] != 0 ||
		second.StartedOn != nil || second.FinishedOn != nil ||
		second.Comment != "" || second.CoverURL != "" {
		t.Fatalf("second = %+v", second)
	}
	// The unknown status and the duplicate are reported while scanning the
	// rows; compare by line, not by order.
	wantSkipped := map[int]string{9: "status", 10: "duplicate media_id 1"}
	for _, s := range res.Skipped {
		reason, ok := wantSkipped[s.Line]
		if !ok || !strings.Contains(s.Reason, reason) {
			t.Fatalf("unexpected skipped row %+v", s)
		}
		delete(wantSkipped, s.Line)
	}
	if len(wantSkipped) != 0 {
		t.Fatalf("missing skipped rows %v (got %+v)", wantSkipped, res.Skipped)
	}
	// One batch over every distinct manga/mal id that can still become an
	// entry; the book and mangaupdates rows never reach AniList.
	if c.formatCalls != 1 || c.formatType != anilist.TypeManga {
		t.Fatalf("batch calls=%d type=%q", c.formatCalls, c.formatType)
	}
	ids := slices.Clone(c.formatIDs)
	slices.Sort(ids)
	if !slices.Equal(ids, []int{1, 2, 3, 4, 999}) {
		t.Fatalf("batch ids = %v", ids)
	}
}

func TestYamtrackNovelImporterMissingTitle(t *testing.T) {
	c := &fakeNovelClient{formats: map[int]string{7: "NOVEL"}}
	csv := "media_id,source,media_type,title,status,progress\n7,mal,manga,,Completed,3\n"
	res, err := yamtrackNovelImporter{c: c}.Parse(context.Background(), strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 || res.Entries[0].Title != "MyAnimeList #7" {
		t.Fatalf("entries = %+v", res.Entries)
	}
}

func TestYamtrackNovelImporterCommentCap(t *testing.T) {
	c := &fakeNovelClient{formats: map[int]string{1: "NOVEL"}}
	csv := "media_id,source,media_type,title,status,notes,progress\n1,mal,manga,Novel,Completed," +
		strings.Repeat("x", 2100) + ",1\n"
	res, err := yamtrackNovelImporter{c: c}.Parse(context.Background(), strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 {
		t.Fatalf("entries = %+v", res.Entries)
	}
	e := res.Entries[0]
	if n := len([]rune(e.Comment)); n != 2000 {
		t.Fatalf("comment length = %d", n)
	}
	if !hasNote(e.Notes, "comment truncated to 2000") {
		t.Fatalf("notes = %v", e.Notes)
	}
}

func TestYamtrackNovelImporterBadCSV(t *testing.T) {
	_, err := yamtrackNovelImporter{c: &fakeNovelClient{}}.
		Parse(context.Background(), strings.NewReader("media_type,title\nmanga,Novel\n"))
	if !errors.Is(err, plugin.ErrBadInput) {
		t.Fatalf("err = %v", err)
	}
}

func TestYamtrackNovelImporterBatchError(t *testing.T) {
	csv := "media_id,source,media_type,title,status\n1,mal,manga,Novel,Completed\n"
	for _, c := range []struct {
		name string
		err  error
		want error
	}{
		{"rate limited", anilist.ErrRateLimited, plugin.ErrRateLimited},
		{"unavailable", anilist.ErrUnavailable, plugin.ErrUnavailable},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeNovelClient{formatErr: c.err}
			_, err := yamtrackNovelImporter{c: f}.Parse(context.Background(), strings.NewReader(csv))
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}

func TestYamtrackNovelImporterNoRows(t *testing.T) {
	c := &fakeNovelClient{}
	csv := "media_id,source,media_type,title,status\n50,hardcover,book,Book,Completed\n"
	res, err := yamtrackNovelImporter{c: c}.Parse(context.Background(), strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 0 || res.Ignored != 1 || c.formatCalls != 0 {
		t.Fatalf("res = %+v, calls = %d", res, c.formatCalls)
	}
}

func hasNote(notes []string, want string) bool {
	for _, note := range notes {
		if strings.Contains(note, want) {
			return true
		}
	}
	return false
}

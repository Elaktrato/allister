package manga

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

// fakeMangaFormatClient is an AniList provider that also answers the batch
// format lookup the Yamtrack importer uses.
type fakeMangaFormatClient struct {
	formats     map[int]string
	formatErr   error
	formatType  string
	formatIDs   []int
	formatCalls int
}

var _ malFormatClient = (*fakeMangaFormatClient)(nil)

func (f *fakeMangaFormatClient) Name() anilist.Source { return anilist.AniList }
func (f *fakeMangaFormatClient) Search(context.Context, string, string) ([]anilist.SearchResult, error) {
	return nil, nil
}
func (f *fakeMangaFormatClient) Media(context.Context, string, string) (anilist.Media, error) {
	return anilist.Media{}, nil
}
func (f *fakeMangaFormatClient) Cast(context.Context, string) ([]anilist.CastMember, error) {
	return nil, nil
}
func (f *fakeMangaFormatClient) MediaFormatsByMALIDs(_ context.Context, typ string, malIDs []int) (map[int]string, error) {
	f.formatCalls++
	f.formatType = typ
	f.formatIDs = append(f.formatIDs, malIDs...)
	if f.formatErr != nil {
		return nil, f.formatErr
	}
	return f.formats, nil
}

// plainMangaProvider is a provider without the batch lookup: the importer must
// answer ErrUnavailable instead of silently importing every row.
type plainMangaProvider struct{}

func (plainMangaProvider) Name() anilist.Source { return anilist.Manual }
func (plainMangaProvider) Search(context.Context, string, string) ([]anilist.SearchResult, error) {
	return nil, nil
}
func (plainMangaProvider) Media(context.Context, string, string) (anilist.Media, error) {
	return anilist.Media{}, nil
}
func (plainMangaProvider) Cast(context.Context, string) ([]anilist.CastMember, error) {
	return nil, nil
}

// yamtrackMangaCSV is a synthetic Yamtrack general export: a novel, a manga and
// a one-shot, an id AniList does not know, a mangaupdates row, a book row, an
// unknown status and a duplicate of the manga row.
const yamtrackMangaCSV = `media_id,source,media_type,title,image,season_number,episode_number,score,status,notes,start_date,end_date,progress
1,mal,manga,Novel One,https://img.example/novel.jpg,,,8.0,Completed,,2026-01-01 00:00:00+00:00,2026-02-01 00:00:00+00:00,3
2,mal,manga,Manga Two,https://img.example/two.jpg,,,8.0,Completed,a comment,2026-01-01 00:00:00+00:00,2026-02-01 00:00:00+00:00,12
3,mal,manga,One Shot Three,,,,0.0,Completed,,,,1
999,mal,manga,Missing Manga,,,,0.0,Planning,,,,0
40,mangaupdates,manga,Manga Updates Forty,,,,6.0,Completed,,,,1
50,hardcover,book,Book Fifty,,,,6.0,Completed,,,,1
4,mal,manga,Unknown Status,,,,7.0,Sometimes,,,,1
2,mal,manga,Manga Two,,,,8.0,Completed,,,,12
`

// mangaFormatFake answers the batch lookup like AniList would for the synthetic
// export: 1 is a novel, 2 a manga and 3 a one-shot. 999 is unknown.
func mangaFormatFake() *fakeMangaFormatClient {
	return &fakeMangaFormatClient{formats: map[int]string{1: "NOVEL", 2: "MANGA", 3: "ONE_SHOT", 4: "MANGA"}}
}

func TestYamtrackMangaImporterRows(t *testing.T) {
	c := mangaFormatFake()
	res, err := yamtrackMangaImporter{c: c}.Parse(context.Background(), strings.NewReader(yamtrackMangaCSV))
	if err != nil {
		t.Fatal(err)
	}
	// The novel and the book row are not manga entries; the novel is counted
	// after the batch lookup.
	if len(res.Entries) != 4 || res.Ignored != 2 || len(res.Skipped) != 2 {
		t.Fatalf("entries=%+v ignored=%d skipped=%+v", res.Entries, res.Ignored, res.Skipped)
	}
	started := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	finished := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	manga := res.Entries[0]
	if manga.Kind != KindManga || manga.Title != "Manga Two" || len(manga.ExternalIDs) != 1 ||
		manga.ExternalIDs["mal"] != "2" || manga.Status != plugin.Completed ||
		manga.Rating == nil || *manga.Rating != 8 ||
		len(manga.Progress) != 1 || manga.Progress["chapter"] != 12 ||
		manga.StartedOn == nil || !manga.StartedOn.Equal(started) ||
		manga.FinishedOn == nil || !manga.FinishedOn.Equal(finished) ||
		manga.Comment != "a comment" || manga.CoverURL != "https://img.example/two.jpg" || len(manga.Notes) != 0 {
		t.Fatalf("manga = %+v", manga)
	}
	oneShot := res.Entries[1]
	if oneShot.Kind != KindManga || oneShot.Title != "One Shot Three" || oneShot.ExternalIDs["mal"] != "3" ||
		oneShot.Status != plugin.Completed || oneShot.Rating != nil ||
		len(oneShot.Progress) != 1 || oneShot.Progress["chapter"] != 1 ||
		oneShot.StartedOn != nil || oneShot.FinishedOn != nil ||
		oneShot.Comment != "" || oneShot.CoverURL != "" {
		t.Fatalf("one-shot = %+v", oneShot)
	}
	// An id the format map does not know is still submitted: the runner marks
	// the entry unmatched and keeps it as a manual manga.
	missing := res.Entries[2]
	if missing.Kind != KindManga || missing.Title != "Missing Manga" || missing.ExternalIDs["mal"] != "999" ||
		missing.Status != plugin.Planned || missing.Rating != nil ||
		len(missing.Progress) != 1 || missing.Progress["chapter"] != 0 {
		t.Fatalf("missing = %+v", missing)
	}
	// The non-MAL row becomes a manual entry without any external id.
	manual := res.Entries[3]
	if manual.Kind != KindManga || manual.Title != "Manga Updates Forty" || len(manual.ExternalIDs) != 0 ||
		manual.Status != plugin.Completed || manual.Rating == nil || *manual.Rating != 6 ||
		len(manual.Progress) != 1 || manual.Progress["chapter"] != 1 {
		t.Fatalf("manual = %+v", manual)
	}
	// The unknown status and the duplicate are reported while scanning the
	// rows; compare by line, not by order.
	wantSkipped := map[int]string{8: "status", 9: "duplicate media_id 2"}
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
	// One batch over every distinct manga/mal id; the book and mangaupdates
	// rows never reach AniList.
	if c.formatCalls != 1 || c.formatType != anilist.TypeManga {
		t.Fatalf("batch calls=%d type=%q", c.formatCalls, c.formatType)
	}
	ids := slices.Clone(c.formatIDs)
	slices.Sort(ids)
	if !slices.Equal(ids, []int{1, 2, 3, 999}) {
		t.Fatalf("batch ids = %v", ids)
	}
}

func TestYamtrackMangaImporterBadCSV(t *testing.T) {
	_, err := yamtrackMangaImporter{c: &fakeMangaFormatClient{}}.
		Parse(context.Background(), strings.NewReader("media_type,title\nmanga,Manga\n"))
	if !errors.Is(err, plugin.ErrBadInput) {
		t.Fatalf("err = %v", err)
	}
}

func TestYamtrackMangaImporterBatchError(t *testing.T) {
	csv := "media_id,source,media_type,title,status\n1,mal,manga,Manga,Completed\n"
	for _, c := range []struct {
		name string
		err  error
		want error
	}{
		{"rate limited", anilist.ErrRateLimited, plugin.ErrRateLimited},
		{"unavailable", anilist.ErrUnavailable, plugin.ErrUnavailable},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeMangaFormatClient{formatErr: c.err}
			_, err := yamtrackMangaImporter{c: f}.Parse(context.Background(), strings.NewReader(csv))
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}

func TestYamtrackMangaImporterNoCandidates(t *testing.T) {
	// A book row and a manual manga row: nothing needs the batch lookup, so a
	// provider without the method must not fail the import.
	csv := "media_id,source,media_type,title,status\n" +
		"50,hardcover,book,Book Fifty,Completed\n" +
		"40,mangaupdates,manga,Manual Forty,Completed\n"
	res, err := yamtrackMangaImporter{c: plainMangaProvider{}}.Parse(context.Background(), strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 || res.Entries[0].Title != "Manual Forty" ||
		len(res.Entries[0].ExternalIDs) != 0 || res.Ignored != 1 || len(res.Skipped) != 0 {
		t.Fatalf("res = %+v", res)
	}
}

func TestYamtrackMangaImporterUnsupportedProvider(t *testing.T) {
	csv := "media_id,source,media_type,title,status\n1,mal,manga,Manga,Completed\n"
	_, err := yamtrackMangaImporter{c: plainMangaProvider{}}.Parse(context.Background(), strings.NewReader(csv))
	if !errors.Is(err, plugin.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestYamtrackMangaImporterCommentCap(t *testing.T) {
	c := &fakeMangaFormatClient{formats: map[int]string{1: "MANGA"}}
	csv := "media_id,source,media_type,title,status,notes,progress\n1,mal,manga,Manga,Completed," +
		strings.Repeat("x", 2100) + ",1\n"
	res, err := yamtrackMangaImporter{c: c}.Parse(context.Background(), strings.NewReader(csv))
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

func hasNote(notes []string, want string) bool {
	for _, note := range notes {
		if strings.Contains(note, want) {
			return true
		}
	}
	return false
}

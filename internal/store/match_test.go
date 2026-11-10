package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugins/anime/anilist"
)

// manualEntry saves an unmatched import-like manual entry with list data.
func manualEntry(t *testing.T, s *Store) (int64, ListEntry) {
	t.Helper()
	m := sample(anilist.Manual, "man1", intp(12))
	m.Titles = []anilist.Title{{Lang: "en", Text: "Lost Show"}}
	m.MALID, m.CoverURL, m.Studio = "5114", "covers-hash.jpg", "Old Studio"
	m.Relations = []anilist.Relation{{Kind: "other", Source: anilist.AniList, ID: "99", Type: "TV"}}
	start := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	en := ListEntry{Status: Completed, Rating: intp(0), Progress: 3, StartedOn: &start, Comment: "hello"}
	id, err := s.AddToList(context.Background(), m, en, WithSyncState("pending"))
	if err != nil {
		t.Fatal(err)
	}
	en.AnimeID = id
	return id, en
}

func matched(id string) anilist.Anime {
	start := time.Date(2023, 9, 29, 0, 0, 0, 0, time.UTC)
	return anilist.Anime{
		SearchResult: anilist.SearchResult{
			Source: anilist.AniList, ID: id, Type: "TV", Episodes: intp(28), Year: intp(2023), Studio: "Madhouse",
			Synopsis: "New synopsis", CoverURL: "https://img.example/new.jpg",
			Titles: []anilist.Title{{Lang: "ja", Text: "新"}, {Lang: "en", Text: "New Show"}},
		},
		StartDate: &start, SourceMaterial: "MANGA", ExternalURL: "https://anilist.co/anime/" + id,
		Relations: []anilist.Relation{{Kind: "sequel", Source: anilist.AniList, ID: "200", Type: "TV",
			Titles: []anilist.Title{{Lang: "en", Text: "Seq"}}}},
		Recommendations: []anilist.Recommendation{{ID: "9"}},
	}
}

func TestConvertKeepsListEntry(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id, want := manualEntry(t, s)
	var before string
	if err := s.db.QueryRow(`SELECT updated_at FROM entries WHERE item_id = ?`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := s.ConvertToSource(ctx, id, matched("700")); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetListEntry(ctx, id)
	if err != nil || got == nil || !reflect.DeepEqual(*got, want) {
		t.Fatalf("entry %+v err=%v, want %+v", got, err, want)
	}
	if *got.Rating != 0 {
		t.Fatal("rating 0 lost")
	}
	var after string
	s.db.QueryRow(`SELECT updated_at FROM entries WHERE item_id = ?`, id).Scan(&after)
	if after != before {
		t.Fatalf("updated_at changed %q -> %q", before, after)
	}
	sv, _ := s.FindBySource(ctx, anilist.AniList, "700")
	if sv == nil || sv.ID != id {
		t.Fatalf("anime id changed: %+v", sv)
	}
}

func TestConvertReplacesTitlesAndSource(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id, _ := manualEntry(t, s)
	a := matched("700")
	if err := s.ConvertToSource(ctx, id, a); err != nil {
		t.Fatal(err)
	}
	if old, _ := s.FindBySource(ctx, anilist.Manual, "man1"); old != nil {
		t.Fatal("manual row still addressable")
	}
	got, _ := s.GetByID(ctx, id)
	if got.Source != anilist.AniList || got.Anime.ID != "700" || got.SyncState != "pending" {
		t.Fatalf("%+v", got)
	}
	if !reflect.DeepEqual(got.Titles, a.Titles) {
		t.Fatalf("titles %v", got.Titles)
	}
	if len(got.Relations) != 1 || got.Relations[0].ID != "200" || got.Relations[0].Kind != "sequel" {
		t.Fatalf("relations %v", got.Relations)
	}
	if got.Studio != "Madhouse" || got.Synopsis != "New synopsis" || *got.Episodes != 28 || *got.Year != 2023 ||
		got.SourceMaterial != "MANGA" || got.ExternalURL != "https://anilist.co/anime/700" || got.Type != "TV" ||
		got.StartDate == nil || got.EndDate != nil {
		t.Fatalf("fields %+v", got)
	}
	if got.CoverPath != "https://img.example/new.jpg" {
		t.Fatalf("cover %q", got.CoverPath)
	}
	// MAL id kept when AniList has none, replaced when it has
	if got.MALID != "5114" {
		t.Fatalf("mal id %q", got.MALID)
	}
	id2, _ := func() (int64, ListEntry) {
		m := sample(anilist.Manual, "man2", nil)
		m.MALID = "1"
		i, err := s.AddToList(ctx, m, ListEntry{Status: Watching})
		if err != nil {
			t.Fatal(err)
		}
		return i, ListEntry{}
	}()
	b := matched("701")
	b.MALID = "777"
	if err := s.ConvertToSource(ctx, id2, b); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.GetByID(ctx, id2); g.MALID != "777" {
		t.Fatalf("mal id %q", g.MALID)
	}
}

func TestConvertWorksWithoutListEntryOrMAL(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id := mustSave(t, s, sample(anilist.Manual, "x", nil), WithSyncState("done"))
	if err := s.ConvertToSource(ctx, id, matched("5")); err != nil {
		t.Fatal(err)
	}
	if en, _ := s.GetListEntry(ctx, id); en != nil {
		t.Fatal("conversion must not create a list entry")
	}
	if g, _ := s.GetByID(ctx, id); g.MALID != "" || g.Source != anilist.AniList {
		t.Fatalf("%+v", g)
	}
}

func TestConvertRefusesDuplicate(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id, _ := manualEntry(t, s)
	existing := mustSave(t, s, sample(anilist.AniList, "700", nil))
	before, _ := s.GetByID(ctx, id)
	err := s.ConvertToSource(ctx, id, matched("700"))
	if !errors.Is(err, ErrAlreadySaved) {
		t.Fatalf("err=%v", err)
	}
	var ase *AlreadySavedError
	if !errors.As(err, &ase) || ase.ExistingID != existing {
		t.Fatalf("as: %v %+v", err, ase)
	}
	after, _ := s.GetByID(ctx, id)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("changed:\n%+v\n%+v", before, after)
	}
}

func TestConvertRefusesNonManual(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id := mustSave(t, s, sample(anilist.AniList, "1", nil))
	if err := s.ConvertToSource(ctx, id, matched("700")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err=%v", err)
	}
	if err := s.ConvertToSource(ctx, 9999, matched("700")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: err=%v", err)
	}
	m := mustSave(t, s, sample(anilist.Manual, "m", nil))
	if err := s.ConvertToSource(ctx, m, sample(anilist.Manual, "other", nil)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("manual target: err=%v", err)
	}
}

func TestConvertIsAtomicOnFailure(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id, _ := manualEntry(t, s)
	before, _ := s.GetByID(ctx, id)
	if _, err := s.db.Exec(`CREATE TRIGGER boom BEFORE INSERT ON relations
		BEGIN SELECT RAISE(ABORT, 'forced failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.ConvertToSource(ctx, id, matched("700")); err == nil {
		t.Fatal("want error")
	}
	after, _ := s.GetByID(ctx, id)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("not rolled back:\n%+v\n%+v", before, after)
	}
	if after.Source != anilist.Manual || after.Anime.ID != "man1" || after.MALID != "5114" ||
		after.CoverPath != "covers-hash.jpg" || after.SyncState != "pending" || len(after.Relations) != 1 {
		t.Fatalf("%+v", after)
	}
}

func TestConvertClampsProgressToNewTotal(t *testing.T) {
	for _, c := range []struct {
		name     string
		episodes *int
		clamped  bool
		progress int
	}{
		{"above total", intp(2), true, 2},
		{"unknown total", nil, false, 3},
		{"within total", intp(3), false, 3},
		{"far within total", intp(28), false, 3},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newStore(t)
			ctx := context.Background()
			id, want := manualEntry(t, s)
			const old = "2001-02-03T04:05:06Z"
			if _, err := s.db.Exec(`UPDATE entries SET updated_at = ? WHERE item_id = ?`, old, id); err != nil {
				t.Fatal(err)
			}
			a := matched("700")
			a.Episodes = c.episodes
			if err := s.ConvertToSource(ctx, id, a); err != nil {
				t.Fatal(err)
			}
			got, err := s.GetListEntry(ctx, id)
			if err != nil || got == nil {
				t.Fatal(got, err)
			}
			if got.Progress != c.progress {
				t.Fatalf("progress %d, want %d", got.Progress, c.progress)
			}
			want.Progress = c.progress // every other field unchanged
			if !reflect.DeepEqual(*got, want) {
				t.Fatalf("entry %+v, want %+v", *got, want)
			}
			var after string
			s.db.QueryRow(`SELECT updated_at FROM entries WHERE item_id = ?`, id).Scan(&after)
			if c.clamped == (after == old) {
				t.Fatalf("updated_at %q (clamped=%v)", after, c.clamped)
			}
		})
	}
}

// The trigger aborts the last write (the entry's updated_at bump after the
// progress clamp): the clamp and the conversion roll back together.
func TestConvertRollsBackClampOnLateFailure(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id, want := manualEntry(t, s)
	before, _ := s.GetByID(ctx, id)
	if _, err := s.db.Exec(`CREATE TRIGGER boom BEFORE UPDATE ON entries BEGIN SELECT RAISE(ABORT, 'forced failure'); END`); err != nil {
		t.Fatal(err)
	}
	a := matched("700")
	a.Episodes = intp(2) // below the entry's progress of 3: clamps
	if err := s.ConvertToSource(ctx, id, a); err == nil {
		t.Fatal("want error")
	}
	if after, _ := s.GetByID(ctx, id); !reflect.DeepEqual(before, after) {
		t.Fatalf("anime not rolled back:\n%+v\n%+v", before, after)
	}
	if got, _ := s.GetListEntry(ctx, id); got == nil || !reflect.DeepEqual(*got, want) {
		t.Fatalf("entry not rolled back: %+v, want %+v", got, want)
	}
}

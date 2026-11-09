package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/allister/allister/internal/provider"
)

func day(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}

func TestUpsertRejectsRatingOutOfRange(t *testing.T) {
	s := newStore(t)
	id := mustSave(t, s, sample(provider.AniList, "1", nil))
	for _, r := range []int{-1, 11} {
		err := s.UpsertListEntry(context.Background(), ListEntry{AnimeID: id, Status: Watching, Rating: intp(r)})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("rating %d: %v", r, err)
		}
		if err.Error() == ErrInvalid.Error() {
			t.Fatal("no reason in message")
		}
	}
}

func TestUpsertAcceptsZeroRating(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id := mustSave(t, s, sample(provider.AniList, "1", nil))
	if err := s.UpsertListEntry(ctx, ListEntry{AnimeID: id, Status: Completed, Rating: intp(0)}); err != nil {
		t.Fatal(err)
	}
	e, _ := s.GetListEntry(ctx, id)
	if e == nil || e.Rating == nil || *e.Rating != 0 {
		t.Fatalf("%+v", e)
	}
}

func TestUpsertAcceptsNilRating(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id := mustSave(t, s, sample(provider.AniList, "1", nil))
	if err := s.UpsertListEntry(ctx, ListEntry{AnimeID: id, Status: Completed}); err != nil {
		t.Fatal(err)
	}
	e, _ := s.GetListEntry(ctx, id)
	if e == nil || e.Rating != nil {
		t.Fatalf("%+v", e)
	}
}

func TestUpsertRejectsProgressAboveTotal(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id := mustSave(t, s, sample(provider.AniList, "1", intp(12)))
	if err := s.UpsertListEntry(ctx, ListEntry{AnimeID: id, Status: Watching, Progress: 13}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := s.UpsertListEntry(ctx, ListEntry{AnimeID: id, Status: Watching, Progress: -1}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := s.UpsertListEntry(ctx, ListEntry{AnimeID: id, Status: Watching, Progress: 12}); err != nil {
		t.Fatal(err)
	}
	unk := mustSave(t, s, sample(provider.AniList, "2", nil))
	if err := s.UpsertListEntry(ctx, ListEntry{AnimeID: unk, Status: Watching, Progress: 500}); err != nil {
		t.Fatal(err)
	}
}

func TestUpsertRejectsFinishBeforeStart(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id := mustSave(t, s, sample(provider.AniList, "1", nil))
	err := s.UpsertListEntry(ctx, ListEntry{AnimeID: id, Status: Completed, StartedOn: day(2024, 5, 2), FinishedOn: day(2024, 5, 1)})
	if !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := s.UpsertListEntry(ctx, ListEntry{AnimeID: id, Status: Completed, StartedOn: day(2024, 5, 1), FinishedOn: day(2024, 5, 1)}); err != nil {
		t.Fatal(err)
	}
	e, _ := s.GetListEntry(ctx, id)
	if !e.StartedOn.Equal(*day(2024, 5, 1)) {
		t.Fatalf("%v", e.StartedOn)
	}
}

func TestUpsertRejectsUnknownStatusAndMissingAnime(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id := mustSave(t, s, sample(provider.AniList, "1", nil))
	if err := s.UpsertListEntry(ctx, ListEntry{AnimeID: id, Status: "bogus"}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := s.UpsertListEntry(ctx, ListEntry{AnimeID: 999, Status: Watching}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestUpsertUpdatesExisting(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id := mustSave(t, s, sample(provider.AniList, "1", nil))
	s.UpsertListEntry(ctx, ListEntry{AnimeID: id, Status: Watching, Comment: "a"})
	s.UpsertListEntry(ctx, ListEntry{AnimeID: id, Status: Dropped, Comment: "b", Rating: intp(3)})
	e, _ := s.GetListEntry(ctx, id)
	if e.Status != Dropped || e.Comment != "b" || *e.Rating != 3 {
		t.Fatalf("%+v", e)
	}
	if e, _ := s.GetListEntry(ctx, 12345); e != nil {
		t.Fatal("expected nil")
	}
}

func TestIncrementStopsAtTotal(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id := mustSave(t, s, sample(provider.AniList, "1", intp(2)))
	s.UpsertListEntry(ctx, ListEntry{AnimeID: id, Status: Watching, Progress: 1})
	for i := 0; i < 3; i++ {
		if err := s.IncrementProgress(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	e, _ := s.GetListEntry(ctx, id)
	if e.Progress != 2 {
		t.Fatal(e.Progress)
	}
}

func TestIncrementUnknownTotalKeepsCounting(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id := mustSave(t, s, sample(provider.AniList, "1", nil))
	s.UpsertListEntry(ctx, ListEntry{AnimeID: id, Status: Watching})
	for i := 0; i < 5; i++ {
		s.IncrementProgress(ctx, id)
	}
	e, _ := s.GetListEntry(ctx, id)
	if e.Progress != 5 {
		t.Fatal(e.Progress)
	}
}

func TestIncrementWithoutEntry(t *testing.T) {
	s := newStore(t)
	id := mustSave(t, s, sample(provider.AniList, "1", nil))
	if err := s.IncrementProgress(context.Background(), id); !errors.Is(err, ErrNoEntry) {
		t.Fatal(err)
	}
}

func TestRemoveListEntryKeepsAnime(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id := mustSave(t, s, sample(provider.AniList, "1", nil))
	s.UpsertListEntry(ctx, ListEntry{AnimeID: id, Status: Watching})
	if err := s.RemoveListEntry(ctx, id); err != nil {
		t.Fatal(err)
	}
	if e, _ := s.GetListEntry(ctx, id); e != nil {
		t.Fatal("entry remains")
	}
	if a, _ := s.FindBySource(ctx, provider.AniList, "1"); a == nil {
		t.Fatal("anime removed")
	}
}

func TestListFiltersAndOrders(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	a := mustSave(t, s, sample(provider.AniList, "1", intp(12)))
	b := mustSave(t, s, sample(provider.AniList, "2", nil))
	c := mustSave(t, s, sample(provider.AniList, "3", nil))
	for _, id := range []int64{a, b, c} {
		st := Watching
		if id == c {
			st = Completed
		}
		s.UpsertListEntry(ctx, ListEntry{AnimeID: id, Status: st})
	}
	// force distinct timestamps: a oldest, b newest
	s.db.Exec(`UPDATE entries SET updated_at = '2024-01-01T00:00:00Z' WHERE item_id = ?`, a)
	s.db.Exec(`UPDATE entries SET updated_at = '2024-03-01T00:00:00Z' WHERE item_id = ?`, b)
	s.db.Exec(`UPDATE entries SET updated_at = '2024-02-01T00:00:00Z' WHERE item_id = ?`, c)

	all, err := s.List(ctx, nil)
	if err != nil || len(all) != 3 || all[0].AnimeID != b || all[1].AnimeID != c || all[2].AnimeID != a {
		t.Fatalf("%+v err=%v", all, err)
	}
	if len(all[2].Titles) != 2 || all[2].Type != "TV" || *all[2].Episodes != 12 || all[2].Entry.Status != Watching || all[2].SyncState != "pending" {
		t.Fatalf("%+v", all[2])
	}
	w := Watching
	got, _ := s.List(ctx, &w)
	if len(got) != 2 || got[0].AnimeID != b || got[1].AnimeID != a {
		t.Fatalf("%+v", got)
	}
}

func TestCounts(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for i, st := range []Status{Watching, Watching, Dropped} {
		id := mustSave(t, s, sample(provider.AniList, string(rune('a'+i)), nil))
		s.UpsertListEntry(ctx, ListEntry{AnimeID: id, Status: st})
	}
	m, err := s.Counts(ctx)
	if err != nil || m[Watching] != 2 || m[Dropped] != 1 || m[Completed] != 0 {
		t.Fatalf("%v %v", m, err)
	}
}

func zoned(zone *time.Location, y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, zone)
	return &t
}

func TestDatesStoredByCalendarDayAnyZone(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	jst, pst := time.FixedZone("JST", 9*3600), time.FixedZone("PST", -8*3600)
	id := mustSave(t, s, sample(provider.AniList, "1", nil))
	if err := s.UpsertListEntry(ctx, ListEntry{AnimeID: id, Status: Completed,
		StartedOn: zoned(jst, 2024, 5, 1), FinishedOn: zoned(pst, 2024, 5, 2)}); err != nil {
		t.Fatal(err)
	}
	e, _ := s.GetListEntry(ctx, id)
	if e.StartedOn.Format(dateLayout) != "2024-05-01" || e.FinishedOn.Format(dateLayout) != "2024-05-02" ||
		!e.StartedOn.Equal(*day(2024, 5, 1)) || e.StartedOn.Location() != time.UTC {
		t.Fatalf("%v %v", e.StartedOn, e.FinishedOn)
	}
}

func TestFinishSameCalendarDayMixedZonesAccepted(t *testing.T) {
	s := newStore(t)
	id := mustSave(t, s, sample(provider.AniList, "1", nil))
	jst, pst := time.FixedZone("JST", 9*3600), time.FixedZone("PST", -8*3600)
	err := s.UpsertListEntry(context.Background(), ListEntry{AnimeID: id, Status: Completed,
		StartedOn: day(2024, 5, 1), FinishedOn: zoned(jst, 2024, 5, 1)})
	if err != nil {
		t.Fatal(err)
	}
	err = s.UpsertListEntry(context.Background(), ListEntry{AnimeID: id, Status: Completed,
		StartedOn: zoned(jst, 2024, 5, 1), FinishedOn: zoned(pst, 2024, 5, 1)})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFinishOneDayBeforeStartMixedZonesRejected(t *testing.T) {
	s := newStore(t)
	id := mustSave(t, s, sample(provider.AniList, "1", nil))
	jst, pst := time.FixedZone("JST", 9*3600), time.FixedZone("PST", -8*3600)
	err := s.UpsertListEntry(context.Background(), ListEntry{AnimeID: id, Status: Completed,
		StartedOn: zoned(pst, 2024, 5, 2), FinishedOn: zoned(jst, 2024, 5, 1)})
	if !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestAnimeDatesStoredByCalendarDayAnyZone(t *testing.T) {
	s := newStore(t)
	a := sample(provider.AniList, "1", nil)
	a.StartDate = zoned(time.FixedZone("JST", 9*3600), 2020, 4, 5)
	mustSave(t, s, a)
	got, _ := s.FindBySource(context.Background(), provider.AniList, "1")
	if got.StartDate.Format(dateLayout) != "2020-04-05" {
		t.Fatal(got.StartDate)
	}
}

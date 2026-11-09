package store

import (
	"context"
	"errors"
	"testing"

	"github.com/allister/allister/internal/provider"
)

func TestAdapterKeepsLegacyStatusStrings(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	core := map[Status]string{Watching: "active", PlanToWatch: "planned", Paused: "paused", Completed: "completed", Dropped: "dropped"}
	ids := map[Status]int64{}
	for i, st := range []Status{Watching, PlanToWatch, Paused, Completed, Dropped} {
		id, err := s.AddToList(ctx, sample(provider.AniList, string(rune('a'+i)), nil), ListEntry{Status: st})
		if err != nil {
			t.Fatal(err)
		}
		ids[st] = id
	}
	for st, id := range ids {
		var stored string
		if err := s.db.QueryRow(`SELECT status FROM entries WHERE item_id = ?`, id).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if stored != core[st] {
			t.Errorf("%s stored as %q, want %q", st, stored, core[st])
		}
		if e, _ := s.GetListEntry(ctx, id); e == nil || e.Status != st {
			t.Errorf("GetListEntry status %+v, want %s", e, st)
		}
		if r, _ := s.GetListRow(ctx, id); r == nil || r.Entry.Status != st {
			t.Errorf("GetListRow status %+v, want %s", r, st)
		}
		if rows, err := s.List(ctx, &st); err != nil || len(rows) != 1 || rows[0].AnimeID != id || rows[0].Entry.Status != st {
			t.Errorf("List(%s) = %+v, %v", st, rows, err)
		}
	}
	m, err := s.Counts(ctx)
	if err != nil || len(m) != 5 || m[Watching] != 1 || m[PlanToWatch] != 1 {
		t.Fatalf("counts %v %v", m, err)
	}
	// the core class names are not legacy statuses
	for _, bad := range []Status{"active", "planned"} {
		if err := s.UpsertListEntry(ctx, ListEntry{AnimeID: ids[Watching], Status: bad}); !errors.Is(err, ErrInvalid) {
			t.Errorf("status %q accepted: %v", bad, err)
		}
		if rows, err := s.List(ctx, &bad); err != nil || len(rows) != 0 {
			t.Errorf("List(%q) = %+v, %v", bad, rows, err)
		}
	}
}

func TestAdapterStoresGenericRows(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	a := sample(provider.AniList, "1", intp(12))
	a.Year, a.MALID = intp(2020), "5"
	a.Relations = []provider.Relation{{Kind: "sequel", Source: provider.AniList, ID: "2", Type: "TV"}}
	id, err := s.AddToList(ctx, a, ListEntry{Status: Watching, Progress: 3})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveCast(ctx, id, []provider.CastMember{cast("1", "1")}); err != nil {
		t.Fatal(err)
	}
	for tbl, want := range map[string]int{"items": 1, "item_titles": 2, "item_fields": 3, "external_ids": 1, "relations": 1,
		"entries": 1, "entry_progress": 1, "credits": 1, "people": 2} {
		if n := countRows(t, s, tbl); n != want {
			t.Errorf("%s has %d rows, want %d", tbl, n, want)
		}
	}
	var plugin, kind string
	if err := s.db.QueryRow(`SELECT plugin_id, kind FROM items WHERE id = ?`, id).Scan(&plugin, &kind); err != nil || plugin != "anime" || kind != "anime" {
		t.Fatalf("item %q/%q %v", plugin, kind, err)
	}
	var epType string
	var eps float64
	if err := s.db.QueryRow(`SELECT vtype, num FROM item_fields WHERE item_id = ? AND key = 'episodes'`, id).Scan(&epType, &eps); err != nil || epType != "number" || eps != 12 {
		t.Fatalf("episodes field %q %v %v", epType, eps, err)
	}
	if n := countLegacy(t, s); n != 0 {
		t.Fatalf("%d legacy tables exist", n)
	}
}

func countLegacy(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table'
		AND name IN ('anime','anime_titles','anime_relations','anime_cast','list_entries','people_g','relations_g')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestLegacyTablesDropped(t *testing.T) {
	s := newStore(t)
	if n := countLegacy(t, s); n != 0 {
		t.Fatalf("%d legacy tables exist", n)
	}
	// "people" is the generic people table
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM pragma_table_info('people') WHERE name = 'plugin_id'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("people is not the generic table: %d %v", n, err)
	}
}

func TestAdapterMalIDIsExternalID(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	a := sample(provider.AniList, "1", nil)
	a.MALID = "0123" // legacy stored an integer: leading zeros vanish
	id := mustSave(t, s, a)
	var typ, val string
	if err := s.db.QueryRow(`SELECT type, value FROM external_ids WHERE item_id = ?`, id).Scan(&typ, &val); err != nil || typ != "mal" || val != "123" {
		t.Fatalf("external id %q=%q %v", typ, val, err)
	}
	for _, q := range []string{"123", "0123"} {
		if got, err := s.FindByMALID(ctx, q); err != nil || got == nil || got.ID != id || got.MALID != "123" {
			t.Fatalf("FindByMALID(%q) = %+v, %v", q, got, err)
		}
	}
	if got, err := s.FindByMALID(ctx, "abc"); got != nil || err != nil {
		t.Fatalf("non-numeric: %+v %v", got, err)
	}
	b := sample(provider.AniList, "2", nil)
	b.MALID = "12x"
	if _, err := s.SaveAnime(ctx, b); !errors.Is(err, ErrInvalid) {
		t.Fatalf("non-numeric MAL id saved: %v", err)
	}
	if got, _ := s.FindBySource(ctx, provider.AniList, "2"); got != nil {
		t.Fatal("item saved despite the bad MAL id")
	}
}

func TestAdapterUnmatchedSemantics(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	m := sample(provider.Manual, "m1", nil)
	m.MALID = "42"
	unmatched, err := s.AddToList(ctx, m, ListEntry{Status: PlanToWatch}, WithSyncState("done"))
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := s.AddToList(ctx, sample(provider.Manual, "m2", nil), ListEntry{Status: PlanToWatch}, WithSyncState("done"))
	sourced := sample(provider.AniList, "9", nil)
	sourced.MALID = "43"
	matched, _ := s.AddToList(ctx, sourced, ListEntry{Status: PlanToWatch})
	for id, want := range map[int64]bool{unmatched: true, plain: false, matched: false} {
		r, err := s.GetListRow(ctx, id)
		if err != nil || r == nil {
			t.Fatal(r, err)
		}
		// the legacy meaning: a manual entry carrying a MAL id
		if got := r.Source == provider.Manual && r.MALID != ""; got != want {
			t.Errorf("row %d unmatched=%v, want %v (%+v)", id, got, want, r)
		}
		// and the library flags the same rows
		lr, err := s.lib.Row(ctx, id, "")
		if err != nil || lr == nil || lr.Flags.Unmatched != want {
			t.Errorf("library row %d flags %+v, want unmatched=%v (%v)", id, lr, want, err)
		}
	}
}

func TestAdapterCastBecomesCredits(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id := mustSave(t, s, sample(provider.AniList, "1", nil))
	noActor := provider.CastMember{Character: provider.Person{Source: provider.AniList, ID: "cz", Name: "Z"}, Role: "SUPPORTING"}
	if err := s.SaveCast(ctx, id, []provider.CastMember{cast("1", "1"), noActor}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.db.Query(`SELECT c.role, c.detail, p.source_id, COALESCE(w.source_id, ''), c.counterpart_role
		FROM credits c JOIN people p ON p.id = c.person_id LEFT JOIN people w ON w.id = c.counterpart_person_id
		WHERE c.item_id = ? ORDER BY c.seq`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got [][5]string
	for rows.Next() {
		var r [5]string
		if err := rows.Scan(&r[0], &r[1], &r[2], &r[3], &r[4]); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	want := [][5]string{{"character", "MAIN", "c1", "v1", "Voice actor"}, {"character", "SUPPORTING", "cz", "", ""}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("credits %v, want %v", got, want)
	}
	var pluginID, src string
	if err := s.db.QueryRow(`SELECT plugin_id, source FROM people WHERE source_id = 'v1'`).Scan(&pluginID, &src); err != nil || pluginID != "anime" || src != "anilist" {
		t.Fatalf("person scope %q/%q %v", pluginID, src, err)
	}
}

func TestAdapterProgressIsEpisodeKey(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id, err := s.AddToList(ctx, sample(provider.AniList, "1", intp(12)), ListEntry{Status: Watching, Progress: 3})
	if err != nil {
		t.Fatal(err)
	}
	value := func() float64 {
		t.Helper()
		var v float64
		if err := s.db.QueryRow(`SELECT value FROM entry_progress WHERE item_id = ? AND key = 'episode'`, id).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	if v := value(); v != 3 {
		t.Fatalf("episode = %v", v)
	}
	if err := s.IncrementProgress(ctx, id); err != nil {
		t.Fatal(err)
	}
	if v := value(); v != 4 {
		t.Fatalf("episode after +1 = %v", v)
	}
	if n := countRows(t, s, "entry_progress"); n != 1 {
		t.Fatalf("progress rows %d", n)
	}
	if err := s.RemoveListEntry(ctx, id); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, "entry_progress"); n != 0 {
		t.Fatalf("progress rows after remove %d", n)
	}
}

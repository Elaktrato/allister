package library

import (
	"testing"
	"time"
)

// TestApplyImport: only status, rating and the primary progress change; the
// comment, dates and other tracks stay. A source modification time newer
// than the entry's own blocks the update, nil fields keep the current values.
func TestApplyImport(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	id := addShow(t, l, "a1", fp(12), Entry{Status: Planned, Rating: ip(4), Comment: "mine",
		Progress: map[string]float64{"episode": 1}}, WithProgressKeys([]string{"season", "episode"}))
	if err := l.UpsertEntry(bg, Entry{ItemID: id, Status: Planned, Rating: ip(4), Comment: "mine",
		Progress: map[string]float64{"episode": 1, "season": 2}}); err != nil {
		t.Fatal(err)
	}
	cur := mustEntry(t, l, id)
	// an older source change is not applied
	older := cur.UpdatedAt.Add(-time.Hour)
	if ok, err := l.ApplyImport(bg, id, Entry{Status: Active}, &older); err != nil || ok {
		t.Fatalf("older source applied: %v %v", ok, err)
	}
	// a newer one updates status, rating and the primary track only
	newer := cur.UpdatedAt.Add(time.Hour)
	ok, err := l.ApplyImport(bg, id, Entry{Status: Active, Rating: ip(9), Progress: map[string]float64{"episode": 7}}, &newer)
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	got := mustEntry(t, l, id)
	if got.Status != Active || got.Rating == nil || *got.Rating != 9 || got.Progress["episode"] != 7 ||
		got.Progress["season"] != 2 || got.Comment != "mine" {
		t.Fatalf("%+v", got)
	}
	if got.UpdatedAt.Before(cur.UpdatedAt) {
		t.Fatalf("updated_at went back: %v -> %v", cur.UpdatedAt, got.UpdatedAt)
	}
	// nil fields and an absent primary track keep the current values
	ok, err = l.ApplyImport(bg, id, Entry{Progress: map[string]float64{}}, nil)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	again := mustEntry(t, l, id)
	if again.Status != Active || *again.Rating != 9 || again.Progress["episode"] != 7 {
		t.Fatalf("%+v", again)
	}
}

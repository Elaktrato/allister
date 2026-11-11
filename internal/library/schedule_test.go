package library

import (
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
)

// TestSaveScheduleReplacesAndMarks: the stored schedule is replaced as a
// whole (empty included), and the marker tells the feed the item was asked.
func TestSaveScheduleReplacesAndMarks(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	id := addShow(t, l, "s1", fp(12), Entry{Status: Active})
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	airings := []Airing{
		{Number: 1, AirsAt: now.Add(24 * time.Hour)},
		{Number: 2, AirsAt: now.Add(7 * 24 * time.Hour)},
	}
	if err := l.SaveSchedule(bg, id, airings, now); err != nil {
		t.Fatal(err)
	}
	got, err := l.ScheduleEntries(bg)
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	if got[0].ItemID != id || got[0].Number != 1 || !got[0].AirsAt.Equal(airings[0].AirsAt) ||
		got[0].PluginID != "tv" || got[0].Source != "tvdata" || len(got[0].Titles) == 0 {
		t.Fatalf("%+v", got[0])
	}
	// a shorter schedule replaces the old one
	if err := l.SaveSchedule(bg, id, airings[:1], now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, _ := l.ScheduleEntries(bg); len(got) != 1 || got[0].Number != 1 {
		t.Fatalf("%+v", got)
	}
	// an empty schedule clears the rows but still marks the item
	if err := l.SaveSchedule(bg, id, nil, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, _ := l.ScheduleEntries(bg); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	if st, _ := l.StaleSchedules(bg, now.Add(time.Hour)); len(st) != 0 {
		t.Fatalf("marked item reported stale: %+v", st)
	}
}

// TestScheduleOnlyForActiveEntries: the feed query and the stale list cover
// active entries only.
func TestScheduleOnlyForActiveEntries(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	active := addShow(t, l, "a1", fp(12), Entry{Status: Active})
	paused := addShow(t, l, "a2", fp(12), Entry{Status: Paused})
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	for _, id := range []int64{active, paused} {
		if err := l.SaveSchedule(bg, id, []Airing{{Number: 1, AirsAt: now.Add(time.Hour)}}, now); err != nil {
			t.Fatal(err)
		}
	}
	got, err := l.ScheduleEntries(bg)
	if err != nil || len(got) != 1 || got[0].ItemID != active {
		t.Fatalf("%+v %v", got, err)
	}
	st, err := l.StaleSchedules(bg, now.Add(-time.Hour))
	if err != nil || len(st) != 0 {
		t.Fatalf("%+v %v", st, err)
	}
	// a paused entry never goes stale
	st, _ = l.StaleSchedules(bg, now.Add(-time.Hour))
	if len(st) != 0 {
		t.Fatalf("paused entry stale: %+v", st)
	}
	// the active one without a marker does
	if _, err := l.db.Exec(`DELETE FROM item_schedules WHERE item_id = ?`, active); err != nil {
		t.Fatal(err)
	}
	st, _ = l.StaleSchedules(bg, now)
	if len(st) != 1 || st[0].ItemID != active || st[0].PluginID != "tv" || st[0].Source != "tvdata" {
		t.Fatalf("%+v", st)
	}
	// an old marker is stale, a fresh one is not
	if err := l.SaveSchedule(bg, active, nil, now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if st, _ := l.StaleSchedules(bg, now.Add(-24*time.Hour)); len(st) != 1 {
		t.Fatalf("old marker not stale: %+v", st)
	}
	if st, _ := l.StaleSchedules(bg, now.Add(-72*time.Hour)); len(st) != 0 {
		t.Fatalf("fresh marker stale: %+v", st)
	}
}

// TestScheduleCascadesWithTheItem: deleting an item removes its airings and
// marker.
func TestScheduleCascadesWithTheItem(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	id := addShow(t, l, "c1", fp(12), Entry{Status: Active})
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	if err := l.SaveSchedule(bg, id, []Airing{{Number: 1, AirsAt: now}}, now); err != nil {
		t.Fatal(err)
	}
	if err := l.DeleteItem(bg, id); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`SELECT COUNT(*) FROM airings`, `SELECT COUNT(*) FROM item_schedules`} {
		var n int
		if err := l.db.QueryRow(q).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s: %d %v", q, n, err)
		}
	}
}

var _ = plugin.Title{}

func TestScheduleKeepsSeasonsApart(t *testing.T) {
	l := newEnv(t).lib
	id := addShow(t, l, "s1", fp(10), Entry{Status: Active})
	at := time.Date(2026, 11, 12, 18, 30, 0, 0, time.UTC)
	airings := []Airing{
		{Season: 1, Number: 5, AirsAt: at},
		{Season: 2, Number: 5, AirsAt: at.Add(7 * 24 * time.Hour)},
	}
	if err := l.SaveSchedule(bg, id, airings, at); err != nil {
		t.Fatal(err)
	}
	got, err := l.ScheduleEntries(bg)
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	if got[0].Season != 1 || got[0].Number != 5 || got[1].Season != 2 || got[1].Number != 5 {
		t.Fatalf("%+v", got)
	}
}

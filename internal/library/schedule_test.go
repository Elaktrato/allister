package library

import (
	"fmt"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
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

// TestScheduleBetweenWindowSourcesAndLimit: the bounded query returns only
// airings of active entries inside [from, to) of the listed plugin/source
// pairs, in air order, cut at limit, with titles.
func TestScheduleBetweenWindowSourcesAndLimit(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	id := addShow(t, l, "w1", fp(12), Entry{Status: Active})
	paused := addShow(t, l, "w2", fp(12), Entry{Status: Paused})
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	airs := []Airing{
		{Number: 1, AirsAt: now.Add(-time.Minute)},
		{Number: 2, AirsAt: now},
		{Number: 3, AirsAt: now.Add(time.Hour)},
		{Number: 4, AirsAt: now.Add(2 * time.Hour)},
		{Number: 5, AirsAt: now.Add(48 * time.Hour)},
	}
	for _, i := range []int64{id, paused} {
		if err := l.SaveSchedule(bg, i, airs, now); err != nil {
			t.Fatal(err)
		}
	}
	tv := []ScheduleSource{{PluginID: "tv", Source: "tvdata"}}
	got, err := l.ScheduleBetween(bg, now, now.Add(24*time.Hour), tv, 10)
	if err != nil || len(got) != 3 || got[0].Number != 2 || got[2].Number != 4 || len(got[0].Titles) == 0 || got[0].ItemID != id {
		t.Fatalf("%+v %v", got, err)
	}
	got, _ = l.ScheduleBetween(bg, now, now.Add(24*time.Hour), tv, 2)
	if len(got) != 2 || got[1].Number != 3 {
		t.Fatalf("limit: %+v", got)
	}
	for _, other := range [][]ScheduleSource{nil, {{PluginID: "book", Source: "tvdata"}}, {{PluginID: "tv", Source: "other"}}} {
		if got, err := l.ScheduleBetween(bg, now, now.Add(24*time.Hour), other, 10); err != nil || len(got) != 0 {
			t.Fatalf("sources %v: %+v %v", other, got, err)
		}
	}
}

// TestBeyond: a release is beyond the progress when it is later on the
// primary track, or, with a group track and a grouped release, later
// lexicographically by (group, primary).
func TestBeyond(t *testing.T) {
	single := plugin.KindDef{Progress: plugin.ProgressSpec{PrimaryKey: "episode"}}
	grouped := plugin.KindDef{Progress: plugin.ProgressSpec{PrimaryKey: "episode", GroupKey: "season"}}
	p := map[string]float64{"season": 1, "episode": 10}
	cases := []struct {
		name     string
		def      plugin.KindDef
		progress map[string]float64
		s, n     int
		want     bool
	}{
		{"single beyond", single, map[string]float64{"episode": 3}, 0, 4, true},
		{"single equal", single, map[string]float64{"episode": 3}, 0, 3, false},
		{"single behind", single, map[string]float64{"episode": 3}, 0, 2, false},
		{"single ignores season", single, map[string]float64{"episode": 3, "season": 5}, 1, 2, false},
		{"same season next", grouped, p, 1, 11, true},
		{"next season first", grouped, p, 2, 1, true},
		{"same", grouped, p, 1, 10, false},
		{"behind", grouped, p, 1, 9, false},
		{"same season lower number", grouped, p, 1, 9, false},
		{"release season zero uses number", grouped, p, 0, 11, true},
		{"release season zero behind", grouped, p, 0, 10, false},
		{"missing progress is zero", grouped, nil, 1, 1, true},
		{"missing progress single", single, nil, 0, 1, true},
		{"nothing at zero", single, nil, 0, 0, false},
	}
	for _, c := range cases {
		if got := Beyond(c.def, c.progress, c.s, c.n); got != c.want {
			t.Errorf("%s: Beyond = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestAiredCounts: only aired releases beyond the first run's progress of an
// active entry are counted; other entries are absent (no hint).
func TestAiredCounts(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	past := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	future := time.Date(2999, 1, 1, 0, 0, 0, 0, time.UTC)

	behind := addShow(t, l, "b1", fp(12), Entry{Status: Active, Progress: map[string]float64{"episode": 2}})
	if err := l.SaveSchedule(bg, behind, []Airing{
		{Number: 1, AirsAt: past}, {Number: 2, AirsAt: past}, {Number: 3, AirsAt: past},
		{Number: 4, AirsAt: past.Add(time.Hour)}, {Number: 5, AirsAt: future}}, now); err != nil {
		t.Fatal(err)
	}
	futureOnly := addShow(t, l, "b2", fp(12), Entry{Status: Active})
	if err := l.SaveSchedule(bg, futureOnly, []Airing{{Number: 1, AirsAt: future}}, now); err != nil {
		t.Fatal(err)
	}
	done := addShow(t, l, "b3", fp(12), Entry{Status: Completed, Progress: map[string]float64{"episode": 1}})
	planned := addShow(t, l, "b4", fp(12), Entry{Status: Planned})
	for _, id := range []int64{done, planned} {
		if err := l.SaveSchedule(bg, id, []Airing{{Number: 5, AirsAt: past}}, now); err != nil {
			t.Fatal(err)
		}
	}
	repeat := addShow(t, l, "b5", fp(12), Entry{Status: Completed})
	if _, err := l.StartRun(bg, repeat, day(2026, 1, 1)); err != nil {
		t.Fatal(err)
	}
	if err := l.SaveSchedule(bg, repeat, []Airing{{Number: 1, AirsAt: past}}, now); err != nil {
		t.Fatal(err)
	}

	got, err := l.AiredCounts(bg, []int64{behind, futureOnly, done, planned, repeat}, now)
	if err != nil {
		t.Fatal(err)
	}
	if got[behind] != 2 || got[futureOnly] != 0 {
		t.Fatalf("%v", got)
	}
	if _, ok := got[futureOnly]; !ok {
		t.Fatalf("active run-1 entry without aired releases should be present with 0: %v", got)
	}
	for _, id := range []int64{done, planned, repeat} {
		if _, ok := got[id]; ok {
			t.Fatalf("entry %d must be absent: %v", id, got)
		}
	}
	if got, err := l.AiredCounts(bg, nil, now); err != nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}
}

// TestScheduleEntriesCarryProgress: scheduled entries carry the display run's
// number and progress.
func TestScheduleEntriesCarryProgress(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	id := addShow(t, l, "p1", fp(12), Entry{Status: Active, Progress: map[string]float64{"episode": 4}})
	if err := l.SaveSchedule(bg, id, []Airing{{Number: 5, AirsAt: now.Add(time.Hour)}}, now); err != nil {
		t.Fatal(err)
	}
	got, err := l.ScheduleEntries(bg)
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	if got[0].RunNumber != 1 || got[0].Progress["episode"] != 4 {
		t.Fatalf("%+v", got[0])
	}
}

// TestBeyondUnsetGroupIsFirstGroup: an unset group value counts as the first
// group, and a kind without a primary key has nothing to be beyond.
func TestBeyondUnsetGroupIsFirstGroup(t *testing.T) {
	grouped := plugin.KindDef{Progress: plugin.ProgressSpec{PrimaryKey: "episode", GroupKey: "season"}}
	p := map[string]float64{"episode": 5}
	for _, c := range []struct {
		s, n int
		want bool
	}{{1, 3, false}, {1, 5, false}, {1, 6, true}, {2, 1, true}, {0, 6, true}, {0, 5, false}} {
		if got := Beyond(grouped, p, c.s, c.n); got != c.want {
			t.Errorf("S%dE%d: Beyond = %v, want %v", c.s, c.n, got, c.want)
		}
	}
}

// TestAiredCountsUnsetSeason: a show advanced with +1 has no season value;
// its season-1 releases are compared by number only.
func TestAiredCountsUnsetSeason(t *testing.T) {
	l := groupedLib(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	id, err := l.AddToList(bg, "tvg", "show", tvData("u1", nil), Entry{Status: Active, Progress: map[string]float64{"episode": 5}},
		WithProgressKeys([]string{"season", "episode"}))
	if err != nil {
		t.Fatal(err)
	}
	var airs []Airing
	for n := 1; n <= 8; n++ {
		airs = append(airs, Airing{Season: 1, Number: n, AirsAt: past})
	}
	if err := l.SaveSchedule(bg, id, airs, now); err != nil {
		t.Fatal(err)
	}
	got, err := l.AiredCounts(bg, []int64{id}, now)
	if err != nil || got[id] != 3 {
		t.Fatalf("aired = %v (%v), want 3", got, err)
	}
}

// TestAiredCountsOnlyMarkable: the hint counts only releases whose tracks the
// calendar can mark (MarkableTracks).
func TestAiredCountsOnlyMarkable(t *testing.T) {
	e := newEnv(t)
	p := plugintest.TVPlugin(plugintest.WithID("tvm"))
	k := &p.M.Kinds[0]
	k.Progress.GroupKey = "season"
	k.Progress.Eligible[0].TotalField = "seasons"
	k.Fields = append(k.Fields, plugin.FieldDef{Key: "seasons", Label: "Seasons", Type: plugin.FieldNumber, Min: fp(0), Editable: true})
	if err := e.reg.Register(p); err != nil {
		t.Fatal(err)
	}
	l := e.lib
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	add := func(id string, seasons *float64, prog map[string]float64, keys []string) int64 {
		d := tvData(id, nil)
		if seasons != nil {
			d.Fields["seasons"] = plugin.FieldValue{Num: seasons}
		}
		itemID, err := l.AddToList(bg, "tvm", "show", d, Entry{Status: Active, Progress: prog}, WithProgressKeys(keys))
		if err != nil {
			t.Fatal(err)
		}
		return itemID
	}
	airs := []Airing{{Season: 1, Number: 1, AirsAt: past}, {Season: 1, Number: 2, AirsAt: past}, {Season: 2, Number: 1, AirsAt: past}}
	// episode-only combination: no season track, so nothing is markable
	solo := add("m1", nil, map[string]float64{"episode": 30}, []string{"episode"})
	// season 2 is above the seasons total of 1
	capped := add("m2", fp(1), map[string]float64{"season": 1, "episode": 1}, []string{"season", "episode"})
	both := add("m3", nil, map[string]float64{"season": 1, "episode": 1}, []string{"season", "episode"})
	for _, id := range []int64{solo, capped, both} {
		if err := l.SaveSchedule(bg, id, airs, now); err != nil {
			t.Fatal(err)
		}
	}
	got, err := l.AiredCounts(bg, []int64{solo, capped, both}, now)
	if err != nil {
		t.Fatal(err)
	}
	if got[solo] != 0 || got[capped] != 1 || got[both] != 2 {
		t.Fatalf("aired = %v, want solo 0, capped 1 (S1E2), both 2", got)
	}
}

// TestBeyondEmptyPrimaryKey: a kind without a primary key has nothing to be
// beyond.
func TestBeyondEmptyPrimaryKey(t *testing.T) {
	if Beyond(plugin.KindDef{}, map[string]float64{"": 0}, 0, 1) {
		t.Error("an empty primary key must never be beyond")
	}
}

// TestReleaseMarkable: the button predicate is false whenever marking would
// move nothing, in particular a release number above the total while the
// primary track already sits at the total (the call clamps to the total).
func TestReleaseMarkable(t *testing.T) {
	single := plugin.KindDef{Progress: plugin.ProgressSpec{PrimaryKey: "episode",
		Eligible: []plugin.ProgressDef{{Key: "episode", Type: plugin.Count, TotalField: "episodes"}}}}
	grouped := plugin.KindDef{Progress: plugin.ProgressSpec{PrimaryKey: "episode", GroupKey: "season",
		Eligible: []plugin.ProgressDef{{Key: "season", Type: plugin.Count}, {Key: "episode", Type: plugin.Count, TotalField: "episodes"}}}}
	keys := []string{"episode"}
	gkeys := []string{"season", "episode"}
	total := map[string]float64{"episode": 12}
	cases := []struct {
		name     string
		def      plugin.KindDef
		keys     []string
		totals   map[string]float64
		progress map[string]float64
		s, n     int
		want     bool
	}{
		{"beyond below total", single, keys, total, map[string]float64{"episode": 3}, 0, 4, true},
		{"beyond the total, below it now", single, keys, total, map[string]float64{"episode": 10}, 0, 13, true},
		{"beyond the total, at it now", single, keys, total, map[string]float64{"episode": 12}, 0, 13, false},
		{"unbounded", single, keys, nil, map[string]float64{"episode": 12}, 0, 13, true},
		{"not beyond", single, keys, total, map[string]float64{"episode": 4}, 0, 4, false},
		{"new season restarts the count", grouped, gkeys, total, map[string]float64{"season": 1, "episode": 12}, 2, 1, true},
		{"same season at the total", grouped, gkeys, total, map[string]float64{"season": 1, "episode": 12}, 1, 13, false},
		{"track inactive", single, nil, total, map[string]float64{"episode": 1}, 0, 2, false},
	}
	for _, c := range cases {
		if got := ReleaseMarkable(c.def, c.keys, c.totals, c.progress, c.s, c.n); got != c.want {
			t.Errorf("%s: ReleaseMarkable = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestAiredCountsSeveralItemsTotals: several listed items with different
// season totals each get the count their own total allows.
func TestAiredCountsSeveralItemsTotals(t *testing.T) {
	e := newEnv(t)
	p := plugintest.TVPlugin(plugintest.WithID("tvb"))
	k := &p.M.Kinds[0]
	k.Progress.GroupKey = "season"
	k.Progress.Eligible[0].TotalField = "seasons"
	k.Fields = append(k.Fields, plugin.FieldDef{Key: "seasons", Label: "Seasons", Type: plugin.FieldNumber, Min: fp(0), Editable: true})
	if err := e.reg.Register(p); err != nil {
		t.Fatal(err)
	}
	l := e.lib
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	airs := []Airing{{Season: 1, Number: 1, AirsAt: past}, {Season: 2, Number: 1, AirsAt: past}, {Season: 3, Number: 1, AirsAt: past}}
	var ids []int64
	want := map[int64]int{}
	for i, c := range []struct {
		seasons *float64
		want    int
	}{{fp(1), 0}, {fp(2), 1}, {fp(3), 2}, {nil, 2}} {
		d := tvData(fmt.Sprintf("s%d", i), nil)
		if c.seasons != nil {
			d.Fields["seasons"] = plugin.FieldValue{Num: c.seasons}
		}
		id, err := l.AddToList(bg, "tvb", "show", d, Entry{Status: Active, Progress: map[string]float64{"season": 1, "episode": 1}},
			WithProgressKeys([]string{"season", "episode"}))
		if err != nil {
			t.Fatal(err)
		}
		if err := l.SaveSchedule(bg, id, airs, now); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		want[id] = c.want
	}
	got, err := l.AiredCounts(bg, ids, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("%v, want %v", got, want)
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("item %d: %d aired, want %d (all: %v)", id, got[id], w, got)
		}
	}
}

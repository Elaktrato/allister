package library

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// sagaPlugin is the rich book fixture (id "saga") whose series link to its
// own kind: description <- isbn, started_on <- released, ended_on <- ended,
// parts_total <- volumes; the series field publisher has a same-named book
// field; parts release on released.
func sagaPlugin() *plugintest.Plugin {
	p := richBook(plugintest.WithID("saga"))
	k := &p.M.Kinds[0]
	k.Fields = append(k.Fields,
		plugin.FieldDef{Key: "ended", Label: "Ended", Type: plugin.FieldDate, Editable: true},
		plugin.FieldDef{Key: "volumes", Label: "Volumes", Type: plugin.FieldNumber, Min: fp(0), Editable: true},
		plugin.FieldDef{Key: "publisher", Label: "Publisher", Type: plugin.FieldText, Editable: true},
	)
	k.SeriesFields = []plugin.FieldDef{
		{Key: "publisher", Label: "Publisher", Type: plugin.FieldText},
		{Key: "note", Label: "Note", Type: plugin.FieldText},
	}
	k.SeriesLink = &plugin.SeriesLinkDef{Kind: "book", DescriptionField: "isbn", StartField: "released", EndField: "ended", PartsField: "volumes"}
	k.SeriesHint = &plugin.SeriesHintDef{ReleaseField: "released"}
	// a second kind with the same fields, which a book series cannot link to
	other := *k
	other.Key, other.Label, other.LabelPlural = "comic", "Comic", "Comics"
	other.Fields = slices.Clone(k.Fields)
	other.SeriesFields, other.SeriesLink, other.SeriesHint = nil, nil, nil
	p.M.Kinds = append(p.M.Kinds, other)
	return p
}

func sagaKind() plugin.KindDef { return sagaPlugin().M.Kinds[0] }

// addSaga adds a saga book to the list with the given fields.
func addSaga(t *testing.T, e *env, id string, st Status, fields map[string]plugin.FieldValue) int64 {
	t.Helper()
	d := bookWith(id, fp(100))
	for k, v := range fields {
		d.Fields[k] = v
	}
	itemID, err := e.lib.AddToList(bg, "saga", "book", d, Entry{Status: st})
	if err != nil {
		t.Fatal(err)
	}
	return itemID
}

func partRow(cover string, st Status, release *time.Time) Row {
	it := Item{Kind: "book", CoverPath: cover, Fields: map[string]plugin.FieldValue{}}
	if release != nil {
		it.Fields["released"] = plugin.FieldValue{Date: release}
	}
	return Row{Item: it, Entry: Entry{Status: st}}
}

func sameDay(a *time.Time, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func TestResolveOrder(t *testing.T) {
	k := sagaKind()
	main := []Row{
		partRow("p1.jpg", Completed, sday(2001, 1, 1)),
		partRow("p2.jpg", Active, sday(2003, 1, 1)),
	}
	linked := &Item{Kind: "book", CoverPath: "linked.jpg", Fields: map[string]plugin.FieldValue{
		"isbn": {Text: "Linked text"}, "released": {Date: sday(1999, 5, 5)}, "ended": {Date: sday(2009, 9, 9)},
		"volumes": {Num: fp(7.9)}, "publisher": {Text: "Linked Press"},
	}}
	own := Series{ID: 1, PluginID: "saga", Kind: "book", Title: "S", CoverPath: "own.jpg", Description: "Own text",
		StartedOn: sday(1990, 1, 1), EndedOn: sday(1995, 1, 1), Finished: true, PartsTotal: 4,
		Fields: map[string]plugin.FieldValue{"publisher": {Text: "Own Press"}}}

	// own beats linked
	r := ResolveSeries(own, k, linked, &Entry{Status: Active}, main, nil)
	if r.Cover != "own.jpg" || r.Description != "Own text" || !sameDay(r.StartedOn, own.StartedOn) ||
		!sameDay(r.EndedOn, own.EndedOn) || !r.Finished || r.PartsTotal != 4 || r.Fields["publisher"].Text != "Own Press" {
		t.Fatalf("own: %+v", r)
	}
	if r.Series.ID != 1 || r.MainParts != 2 || r.Completed != 1 {
		t.Fatalf("own counts: %+v", r)
	}

	// linked beats derived
	bare := Series{ID: 1, PluginID: "saga", Kind: "book", Title: "S"}
	r = ResolveSeries(bare, k, linked, &Entry{Status: Active}, main, nil)
	if r.Cover != "linked.jpg" || r.Description != "Linked text" || !sameDay(r.StartedOn, sday(1999, 5, 5)) ||
		!sameDay(r.EndedOn, sday(2009, 9, 9)) || r.PartsTotal != 7 || r.Fields["publisher"].Text != "Linked Press" {
		t.Fatalf("linked: %+v", r)
	}
	// an ended date on the linked entry counts as finished
	if !r.Finished {
		t.Fatal("linked end date does not count as finished")
	}
	// a series field without a same-named field stays empty
	if _, ok := r.Fields["note"]; ok {
		t.Fatalf("note = %+v", r.Fields["note"])
	}

	// derived when nothing else is there
	bare.Finished = true
	r = ResolveSeries(bare, k, nil, nil, main, nil)
	if r.Cover != "p1.jpg" || r.Description != "" || !sameDay(r.StartedOn, sday(2001, 1, 1)) ||
		!sameDay(r.EndedOn, sday(2003, 1, 1)) || !r.Finished || r.PartsTotal != 2 || len(r.Fields) != 0 {
		t.Fatalf("derived: %+v", r)
	}

	// a kind without a link ignores a linked item
	noLink := k
	noLink.SeriesLink = nil
	r = ResolveSeries(Series{Title: "S"}, noLink, linked, nil, main, nil)
	if r.Cover != "p1.jpg" || r.Description != "" || r.PartsTotal != 2 || r.Finished {
		t.Fatalf("no link: %+v", r)
	}
	// a negative linked number does not count
	linked.Fields["volumes"] = plugin.FieldValue{Num: fp(-3)}
	if r = ResolveSeries(Series{Title: "S"}, k, linked, nil, main, nil); r.PartsTotal != 2 {
		t.Fatalf("negative volumes: %d", r.PartsTotal)
	}
}

func TestResolveDerivedDates(t *testing.T) {
	k := sagaKind()
	main := []Row{
		partRow("", Completed, sday(2005, 6, 1)),
		partRow("", Completed, nil),
		partRow("", Completed, sday(2001, 2, 3)),
		partRow("", Completed, sday(2010, 12, 31)),
	}
	r := ResolveSeries(Series{Title: "S"}, k, nil, nil, main, nil)
	if !sameDay(r.StartedOn, sday(2001, 2, 3)) || r.EndedOn != nil || r.Finished {
		t.Fatalf("not finished: start %v end %v", r.StartedOn, r.EndedOn)
	}
	r = ResolveSeries(Series{Title: "S", Finished: true}, k, nil, nil, main, nil)
	if !sameDay(r.StartedOn, sday(2001, 2, 3)) || !sameDay(r.EndedOn, sday(2010, 12, 31)) {
		t.Fatalf("finished: start %v end %v", r.StartedOn, r.EndedOn)
	}
	// no release field: no derived dates
	noHint := k
	noHint.SeriesHint = &plugin.SeriesHintDef{PositionField: "pages"}
	if r = ResolveSeries(Series{Title: "S", Finished: true}, noHint, nil, nil, main, nil); r.StartedOn != nil || r.EndedOn != nil {
		t.Fatalf("no release field: %v %v", r.StartedOn, r.EndedOn)
	}
	noHint.SeriesHint = nil
	if r = ResolveSeries(Series{Title: "S", Finished: true}, noHint, nil, nil, main, nil); r.StartedOn != nil || r.EndedOn != nil {
		t.Fatalf("no hint: %v %v", r.StartedOn, r.EndedOn)
	}
}

// TestResolveFallsBackWhenLinkedBlank: a linked entry without volumes, dates
// or text gives the derived values, not zero or empty ones.
func TestResolveFallsBackWhenLinkedBlank(t *testing.T) {
	k := sagaKind()
	main := []Row{partRow("p1.jpg", Completed, sday(2002, 1, 1)), partRow("p2.jpg", Planned, sday(2004, 1, 1)), partRow("", Planned, nil)}
	for name, linked := range map[string]*Item{
		"no fields": {Kind: "book", Fields: map[string]plugin.FieldValue{}},
		"blank values": {Kind: "book", Fields: map[string]plugin.FieldValue{
			"isbn": {Text: "  "}, "volumes": {Num: fp(0)}, "publisher": {Text: ""}, "released": {}, "ended": {},
		}},
	} {
		r := ResolveSeries(Series{Title: "S", Finished: true, LinkedItemID: 9}, k, linked, &Entry{Status: Active}, main, nil)
		if r.Cover != "p1.jpg" || r.Description != "" || r.PartsTotal != 3 || !sameDay(r.StartedOn, sday(2002, 1, 1)) ||
			!sameDay(r.EndedOn, sday(2004, 1, 1)) || len(r.Fields) != 0 {
			t.Errorf("%s: %+v", name, r)
		}
		// not finished, and the linked entry has no end date: not finished
		if r = ResolveSeries(Series{Title: "S", LinkedItemID: 9}, k, linked, nil, main, nil); r.Finished || r.EndedOn != nil {
			t.Errorf("%s: finished %v end %v", name, r.Finished, r.EndedOn)
		}
	}
}

func TestResolveStatusUsesExpectedParts(t *testing.T) {
	k := sagaKind()
	var five []Row
	for range 5 {
		five = append(five, partRow("", Completed, nil))
	}
	if r := ResolveSeries(Series{Title: "S", PartsTotal: 12}, k, nil, nil, five, nil); r.Status != Active || r.Completed != 5 || r.PartsTotal != 12 {
		t.Fatalf("5/12: %+v", r)
	}
	linked := &Item{Kind: "book", Fields: map[string]plugin.FieldValue{"volumes": {Num: fp(12)}}}
	if r := ResolveSeries(Series{Title: "S"}, k, linked, nil, five, nil); r.Status != Active {
		t.Fatalf("5/12 linked: %s", r.Status)
	}
	if r := ResolveSeries(Series{Title: "S", PartsTotal: 5}, k, nil, nil, five, nil); r.Status != Completed {
		t.Fatalf("5/5: %s", r.Status)
	}
	// derived expected parts keep the dropped rule: completed and dropped is completed
	withDropped := []Row{partRow("", Completed, nil), partRow("", Dropped, nil)}
	if r := ResolveSeries(Series{Title: "S"}, k, nil, nil, withDropped, nil); r.Status != Completed || r.PartsTotal != 2 {
		t.Fatalf("derived: %+v", r)
	}
}

// TestResolveNoMainParts: without main parts the spin-off parts give the
// status, the counts and the cover fallback; with a main part they do not.
func TestResolveNoMainParts(t *testing.T) {
	k := sagaKind()
	spins := []Row{partRow("s1.jpg", Completed, nil), partRow("s2.jpg", Completed, nil)}
	r := ResolveSeries(Series{Title: "S"}, k, nil, nil, nil, spins)
	if r.Status != Completed || r.Completed != 2 || r.PartsTotal != 2 || r.Cover != "s1.jpg" || r.MainParts != 0 || r.SpinOffParts != 2 {
		t.Fatalf("no main: %+v", r)
	}
	main := []Row{partRow("", Planned, nil)}
	r = ResolveSeries(Series{Title: "S"}, k, nil, nil, main, spins)
	if r.Status != Planned || r.Completed != 0 || r.PartsTotal != 1 || r.Cover != "" || r.MainParts != 1 || r.SpinOffParts != 2 {
		t.Fatalf("with main: %+v", r)
	}
}

func TestDeriveSeriesStatusExpected(t *testing.T) {
	n := func(s Status, k int) []Status {
		out := make([]Status, k)
		for i := range out {
			out[i] = s
		}
		return out
	}
	for _, c := range []struct {
		parts    []Status
		expected int
		want     Status
	}{
		{n(Completed, 5), 12, Active},
		{n(Completed, 12), 12, Completed},
		{n(Completed, 13), 12, Completed},
		{append(n(Completed, 2), Dropped), 3, Completed},
		{append(n(Completed, 3), Dropped), 3, Completed},
		{append(n(Completed, 11), Dropped), 12, Completed},
		{append(n(Completed, 10), Dropped), 12, Active},
		{n(Dropped, 12), 12, Dropped},
		{n(Dropped, 2), 12, Dropped},
		{n(Planned, 2), 12, Planned},
		{[]Status{Completed, Paused}, 12, Paused},
		{nil, 3, Active},
		// expected 0: the old rules
		{n(Completed, 5), 0, Completed},
		{[]Status{Completed, Completed, Dropped}, 0, Completed},
		{[]Status{Dropped, Dropped}, 0, Dropped},
		{[]Status{Planned, Planned}, 0, Planned},
		{[]Status{Completed, Active, Planned}, 0, Active},
		{[]Status{Completed, Paused}, 0, Paused},
		{[]Status{Completed, Planned}, 0, Active},
	} {
		if got := DeriveSeriesStatus(c.parts, c.expected); got != c.want {
			t.Errorf("%v / %d = %s, want %s", c.parts, c.expected, got, c.want)
		}
	}
}

// TestResolveSpinOffsExcluded: spin-off parts count for nothing but
// SpinOffParts, in the loaded series and in the list rows.
func TestResolveSpinOffsExcluded(t *testing.T) {
	e := newEnvWith(t, sagaPlugin())
	spin := addSaga(t, e, "x", Active, map[string]plugin.FieldValue{"released": {Date: sday(1980, 1, 1)}})
	a := addSaga(t, e, "a", Completed, map[string]plugin.FieldValue{"released": {Date: sday(2001, 1, 1)}})
	b := addSaga(t, e, "b", Completed, map[string]plugin.FieldValue{"released": {Date: sday(2002, 1, 1)}})
	for id, c := range map[int64]string{spin: "x.jpg", a: "a.jpg", b: "b.jpg"} {
		if _, err := e.db.Exec(`UPDATE items SET cover_path = ? WHERE id = ?`, c, id); err != nil {
			t.Fatal(err)
		}
	}
	s := mustSeries(t, e, spin, "Saga")
	mustAddPart(t, e, s.ID, a, b)
	if err := e.lib.SetPartSpinOff(bg, s.ID, spin, true); err != nil {
		t.Fatal(err)
	}
	ser, err := e.lib.SeriesByUID(bg, "saga", s.UID)
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.lib.LoadResolvedSeries(bg, ser, "en")
	if err != nil {
		t.Fatal(err)
	}
	if r.MainParts != 2 || r.SpinOffParts != 1 || r.Completed != 2 || r.Status != Completed || r.PartsTotal != 2 ||
		r.Cover != "a.jpg" || !sameDay(r.StartedOn, sday(2001, 1, 1)) {
		t.Fatalf("resolved: %+v", r)
	}
	pg, err := e.lib.ListSeries(bg, SeriesQuery{PluginID: "saga"})
	if err != nil || len(pg.Rows) != 1 {
		t.Fatalf("%+v %v", pg, err)
	}
	if row := pg.Rows[0]; row.Parts != 2 || row.Completed != 2 || row.PartsTotal != 2 || row.Status != Completed || row.Cover != "a.jpg" {
		t.Fatalf("row: %+v", row)
	}
	counts, err := e.lib.SeriesCounts(bg, SeriesQuery{PluginID: "saga"})
	if err != nil || counts[Completed] != 1 || counts[Active] != 0 {
		t.Fatalf("counts %v %v", counts, err)
	}
	if got, _ := e.lib.ActiveSeries(bg, []string{"saga"}, 10); len(got) != 0 {
		t.Fatalf("active: %+v", got)
	}
	// with a main part planned it is active, with the main counts and cover
	if _, err := e.db.Exec(`UPDATE entry_runs SET status = 'planned' WHERE id = (SELECT display_run_id FROM entries WHERE item_id = ?)`, b); err != nil {
		t.Fatal(err)
	}
	got, err := e.lib.ActiveSeries(bg, []string{"saga"}, 10)
	if err != nil || len(got) != 1 || got[0].Parts != 2 || got[0].Completed != 1 || got[0].PartsTotal != 2 || got[0].Cover != "a.jpg" {
		t.Fatalf("active: %+v %v", got, err)
	}
}

// TestLoadResolvedSeriesLinked: the loader passes the linked item to the
// resolution, and the list rows carry the parent.
func TestLoadResolvedSeriesLinked(t *testing.T) {
	e := newEnvWith(t, sagaPlugin())
	k := sagaKind()
	a := addSaga(t, e, "a", Completed, nil)
	linked := addSaga(t, e, "l", Planned, map[string]plugin.FieldValue{
		"isbn": {Text: "About the saga"}, "volumes": {Num: fp(9)}, "publisher": {Text: "Acme"},
	})
	if _, err := e.db.Exec(`UPDATE items SET cover_path = 'l.jpg' WHERE id = ?`, linked); err != nil {
		t.Fatal(err)
	}
	parent := mustSeries(t, e, addSaga(t, e, "p", Planned, nil), "Parent")
	s := mustSeries(t, e, a, "Saga")
	if err := e.lib.UpdateSeriesDetails(bg, s.ID, k, SeriesDetails{Title: "Saga", LinkedItemID: linked, ParentID: parent.ID}); err != nil {
		t.Fatal(err)
	}
	ser, _ := e.lib.SeriesByUID(bg, "saga", s.UID)
	r, err := e.lib.LoadResolvedSeries(bg, ser, "en")
	if err != nil {
		t.Fatal(err)
	}
	if r.Description != "About the saga" || r.PartsTotal != 9 || r.Cover != "l.jpg" || r.Fields["publisher"].Text != "Acme" || r.Status != Active {
		t.Fatalf("resolved: %+v", r)
	}
	pg, _ := e.lib.ListSeries(bg, SeriesQuery{PluginID: "saga"})
	for _, row := range pg.Rows {
		if row.Series.ID != s.ID {
			continue
		}
		if row.PartsTotal != 9 || row.Cover != "l.jpg" || row.Status != Active || row.ParentID != parent.ID {
			t.Fatalf("row: %+v", row)
		}
		got, err := e.lib.ActiveSeries(bg, []string{"saga"}, 10)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, a := range got {
			if a.Series.ID != s.ID {
				continue
			}
			found = true
			if a.PartsTotal != 9 || a.Cover != "l.jpg" || a.ParentID != parent.ID {
				t.Fatalf("active row: %+v", a)
			}
		}
		if !found {
			t.Fatalf("not active: %+v", got)
		}
		return
	}
	t.Fatal("series not listed")
}

// sagaVariant sets up one series for the status equivalence: the main part
// statuses, an optional own or linked expected number of parts and an
// optional extra spin-off part.
type sagaVariant struct {
	name       string
	own        func(n int) int
	linked     func(n int) float64
	linkedKind string // "" is the link kind
	spinOff    Status
	extraMain  Status // an extra main part after the combination
	wantExpect func(n int) int
}

func TestActiveSeriesMatchesDerivedStatusExpected(t *testing.T) {
	e := newEnvWith(t, sagaPlugin())
	all := Statuses()
	var combos [][]Status
	for i, a := range all {
		combos = append(combos, []Status{a})
		for j := i; j < len(all); j++ {
			combos = append(combos, []Status{a, all[j]})
			for k := j; k < len(all); k++ {
				combos = append(combos, []Status{a, all[j], all[k]})
			}
		}
	}
	variants := []sagaVariant{
		{name: "own n", own: func(n int) int { return n }, wantExpect: func(n int) int { return n }},
		{name: "own n+1", own: func(n int) int { return n + 1 }, wantExpect: func(n int) int { return n + 1 }},
		{name: "linked n", linked: func(n int) float64 { return float64(n) }, wantExpect: func(n int) int { return n }},
		{name: "linked n+1.7", linked: func(n int) float64 { return float64(n) + 1.7 }, wantExpect: func(n int) int { return n + 1 }},
		{name: "linked 0", linked: func(int) float64 { return 0 }, wantExpect: func(int) int { return 0 }},
		{name: "spin-off completed", spinOff: Completed, wantExpect: func(int) int { return 0 }},
		{name: "spin-off active", spinOff: Active, wantExpect: func(int) int { return 0 }},
		{name: "extra dropped, own n", own: func(n int) int { return n }, extraMain: Dropped, wantExpect: func(n int) int { return n }},
		{name: "extra dropped, linked n+1", linked: func(n int) float64 { return float64(n + 1) }, extraMain: Dropped, wantExpect: func(n int) int { return n + 1 }},
		{name: "own n+1 and linked n", own: func(n int) int { return n + 1 }, linked: func(n int) float64 { return float64(n) },
			wantExpect: func(n int) int { return n + 1 }},
		{name: "linked other kind n+1", linked: func(n int) float64 { return float64(n + 1) }, linkedKind: "comic", wantExpect: func(int) int { return 0 }},
	}
	n := 0
	add := func(st Status, fields map[string]plugin.FieldValue) int64 {
		n++
		return addSaga(t, e, "v"+fmt.Sprint(n), st, fields)
	}
	want := map[int64]bool{}
	wantTotal := map[int64]int{}
	for _, v := range variants {
		for _, c := range combos {
			if v.extraMain != "" {
				c = append(slices.Clone(c), v.extraMain)
			}
			ids := make([]int64, len(c))
			for i, st := range c {
				ids[i] = add(st, nil)
			}
			s := mustSeries(t, e, ids[0], v.name+fmt.Sprint(c))
			mustAddPart(t, e, s.ID, ids[1:]...)
			if v.own != nil {
				if _, err := e.db.Exec(`UPDATE series SET parts_total = ? WHERE id = ?`, v.own(len(c)), s.ID); err != nil {
					t.Fatal(err)
				}
			}
			if v.linked != nil {
				fields := map[string]plugin.FieldValue{"volumes": {Num: fp(v.linked(len(c)))}}
				var l int64
				if v.linkedKind == "" {
					l = add(Planned, fields)
				} else {
					n++
					d := bookWith("v"+fmt.Sprint(n), fp(100))
					d.Kind, d.Fields["volumes"] = v.linkedKind, fields["volumes"]
					var err error
					if l, err = e.lib.AddToList(bg, "saga", v.linkedKind, d, Entry{Status: Planned}); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := e.db.Exec(`UPDATE series SET linked_item_id = ? WHERE id = ?`, l, s.ID); err != nil {
					t.Fatal(err)
				}
			}
			if v.spinOff != "" {
				sp := add(v.spinOff, nil)
				mustAddPart(t, e, s.ID, sp)
				if err := e.lib.SetPartSpinOff(bg, s.ID, sp, true); err != nil {
					t.Fatal(err)
				}
			}
			exp := v.wantExpect(len(c))
			want[s.ID] = DeriveSeriesStatus(c, exp) == Active
			wantTotal[s.ID] = len(c)
			if exp > 0 {
				wantTotal[s.ID] = exp
			}
		}
	}
	// series whose parts are all spin-offs: their status, counts and cover
	// come from the spin-off parts
	wantStatus := map[int64]Status{}
	wantCounts := map[int64][2]int{}
	for _, c := range [][]Status{{Completed}, {Completed, Completed}, {Completed, Active, Planned}, {Paused, Completed}, {Dropped, Dropped}, {Planned}} {
		ids := make([]int64, len(c))
		for i, st := range c {
			ids[i] = add(st, nil)
		}
		so := mustSeries(t, e, ids[0], "only spin-offs"+fmt.Sprint(c))
		mustAddPart(t, e, so.ID, ids[1:]...)
		for _, id := range ids {
			if err := e.lib.SetPartSpinOff(bg, so.ID, id, true); err != nil {
				t.Fatal(err)
			}
		}
		done := 0
		for _, st := range c {
			if st == Completed {
				done++
			}
		}
		wantStatus[so.ID] = DeriveSeriesStatus(c, 0)
		wantCounts[so.ID] = [2]int{len(c), done}
		want[so.ID] = wantStatus[so.ID] == Active
		wantTotal[so.ID] = len(c)
	}

	got, err := e.lib.ActiveSeries(bg, []string{"saga"}, 10000)
	if err != nil {
		t.Fatal(err)
	}
	list, err := e.lib.ListSeries(bg, SeriesQuery{PluginID: "saga", PageSize: maxPageSize})
	if err != nil {
		t.Fatal(err)
	}
	var listed []SeriesRow
	for p := 1; p <= list.Pages; p++ {
		pg, err := e.lib.ListSeries(bg, SeriesQuery{PluginID: "saga", PageSize: maxPageSize, Page: p})
		if err != nil {
			t.Fatal(err)
		}
		listed = append(listed, pg.Rows...)
	}
	byID := map[int64]SeriesRow{}
	for _, r := range listed {
		byID[r.Series.ID] = r
	}
	have := map[int64]SeriesRow{}
	for _, r := range got {
		if r.Status != Active {
			t.Fatalf("%+v", r)
		}
		have[r.Series.ID] = r
	}
	for id, w := range want {
		r, ok := have[id]
		if ok != w {
			t.Errorf("%s: active=%v, ActiveSeries has it=%v", byID[id].Series.Title, w, ok)
			continue
		}
		lr := byID[id]
		if (lr.Status == Active) != w {
			t.Errorf("%s: list status %s, want active=%v", lr.Series.Title, lr.Status, w)
		}
		if st, ok := wantStatus[id]; ok && lr.Status != st {
			t.Errorf("%s: list status %s, want %s", lr.Series.Title, lr.Status, st)
		}
		if c, ok := wantCounts[id]; ok && (lr.Parts != c[0] || lr.Completed != c[1]) {
			t.Errorf("%s: list parts %d/%d, want %d/%d", lr.Series.Title, lr.Completed, lr.Parts, c[1], c[0])
		}
		if total, ok := wantTotal[id]; ok && lr.PartsTotal != total {
			t.Errorf("%s: list parts total %d, want %d", lr.Series.Title, lr.PartsTotal, total)
		}
		if ok && (r.Parts != lr.Parts || r.Completed != lr.Completed || r.PartsTotal != lr.PartsTotal || r.Cover != lr.Cover) {
			t.Errorf("%s: active row %+v, list row %+v", lr.Series.Title, r, lr)
		}
	}
}

// TestResolveDerivedDatesFallBackToSpinOffs: without main parts the derived
// dates come from the spin-off parts; with a main part they do not.
func TestResolveDerivedDatesFallBackToSpinOffs(t *testing.T) {
	k := sagaKind()
	spins := []Row{partRow("", Completed, sday(2012, 5, 1)), partRow("", Completed, sday(2003, 1, 2))}
	r := ResolveSeries(Series{Title: "S", Finished: true}, k, nil, nil, nil, spins)
	if !sameDay(r.StartedOn, sday(2003, 1, 2)) || !sameDay(r.EndedOn, sday(2012, 5, 1)) {
		t.Fatalf("no main: start %v end %v", r.StartedOn, r.EndedOn)
	}
	main := []Row{partRow("", Planned, sday(2020, 1, 1))}
	r = ResolveSeries(Series{Title: "S", Finished: true}, k, nil, nil, main, spins)
	if !sameDay(r.StartedOn, sday(2020, 1, 1)) || !sameDay(r.EndedOn, sday(2020, 1, 1)) {
		t.Fatalf("with main: start %v end %v", r.StartedOn, r.EndedOn)
	}
}

// TestResolveExpectedPartsSet: ExpectedParts is the set number (own or
// linked), 0 when only derived, even when it equals the part count.
func TestResolveExpectedPartsSet(t *testing.T) {
	k := sagaKind()
	main := []Row{partRow("", Planned, nil), partRow("", Planned, nil)}
	if r := ResolveSeries(Series{Title: "S"}, k, nil, nil, main, nil); r.ExpectedParts != 0 || r.PartsTotal != 2 {
		t.Fatalf("derived: %+v", r)
	}
	if r := ResolveSeries(Series{Title: "S", PartsTotal: 2}, k, nil, nil, main, nil); r.ExpectedParts != 2 {
		t.Fatalf("own: %+v", r)
	}
	linked := &Item{Kind: "book", Fields: map[string]plugin.FieldValue{"volumes": {Num: fp(2)}}}
	if r := ResolveSeries(Series{Title: "S"}, k, linked, nil, main, nil); r.ExpectedParts != 2 {
		t.Fatalf("linked: %+v", r)
	}
}

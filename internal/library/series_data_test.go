package library

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/allister/allister/internal/plugin"
)

// seriesKind is the fixture book kind with two series fields and a link to
// entries of its own kind.
func seriesKind(t *testing.T, e *env) plugin.KindDef {
	t.Helper()
	k, err := e.lib.kindDef("book", "book")
	if err != nil {
		t.Fatal(err)
	}
	k.SeriesFields = []plugin.FieldDef{
		{Key: "volumes", Label: "Volumes", Type: plugin.FieldNumber, Min: fp(0), Editable: true},
		{Key: "publisher", Label: "Publisher", Type: plugin.FieldText, Editable: true},
	}
	k.SeriesLink = &plugin.SeriesLinkDef{Kind: "book"}
	return k
}

func sday(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}

func mustSeriesByUID(t *testing.T, e *env, uid string) *Series {
	t.Helper()
	s, err := e.lib.SeriesByUID(bg, "book", uid)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestUpdateSeriesDetails(t *testing.T) {
	e := newEnv(t)
	k := seriesKind(t, e)
	a := addBook(t, e.lib, "1", fp(1), Entry{Status: Planned})
	s := mustSeries(t, e, a, "S")
	linked := addBook(t, e.lib, "L", fp(1), Entry{Status: Planned})
	parent := mustSeries(t, e, addBook(t, e.lib, "P", fp(1), Entry{Status: Planned}), "Parent")
	d := SeriesDetails{
		Title: " Saga ", CoverPath: "cover.jpg", Description: "Line one\nLine two",
		StartedOn: sday(2001, 2, 3), EndedOn: sday(2010, 1, 1), Finished: true, PartsTotal: 12,
		LinkedItemID: linked, ParentID: parent.ID,
		Fields: map[string]plugin.FieldValue{"volumes": {Num: fp(12)}, "publisher": {Text: "Acme"}},
	}
	if err := e.lib.UpdateSeriesDetails(bg, s.ID, k, d); err != nil {
		t.Fatal(err)
	}
	got := mustSeriesByUID(t, e, s.UID)
	if got.Title != "Saga" || got.CoverPath != "cover.jpg" || got.Description != "Line one\nLine two" ||
		got.StartedOn == nil || !got.StartedOn.Equal(*d.StartedOn) || got.EndedOn == nil || !got.EndedOn.Equal(*d.EndedOn) ||
		!got.Finished || got.PartsTotal != 12 || got.LinkedItemID != linked || got.ParentID != parent.ID {
		t.Fatalf("got %+v", got)
	}
	if v := got.Fields["volumes"]; v.Num == nil || *v.Num != 12 {
		t.Fatalf("volumes = %+v", v)
	}
	if v := got.Fields["publisher"]; v.Text != "Acme" {
		t.Fatalf("publisher = %+v", v)
	}
	// the item's series carries the same data
	if ser, _, _, err := e.lib.SeriesOfItem(bg, a); err != nil || ser.Description != d.Description || ser.Fields["publisher"].Text != "Acme" {
		t.Fatalf("SeriesOfItem = %+v %v", ser, err)
	}
	// empty values clear
	if err := e.lib.UpdateSeriesDetails(bg, s.ID, k, SeriesDetails{Title: "Saga"}); err != nil {
		t.Fatal(err)
	}
	got = mustSeriesByUID(t, e, s.UID)
	if got.CoverPath != "" || got.Description != "" || got.StartedOn != nil || got.EndedOn != nil || got.Finished ||
		got.PartsTotal != 0 || got.LinkedItemID != 0 || got.ParentID != 0 || len(got.Fields) != 0 {
		t.Fatalf("not cleared: %+v", got)
	}
	if n := count(t, e.db, `SELECT count(*) FROM series_fields`); n != 0 {
		t.Fatalf("series_fields rows = %d", n)
	}
	if err := e.lib.UpdateSeriesDetails(bg, 9999, k, SeriesDetails{Title: "X"}); !errors.Is(err, ErrNoSeries) {
		t.Fatalf("unknown series: %v", err)
	}
}

func TestUpdateSeriesDetailsRefuses(t *testing.T) {
	e := newEnv(t)
	k := seriesKind(t, e)
	s := mustSeries(t, e, addBook(t, e.lib, "1", fp(1), Entry{Status: Planned}), "S")
	before := mustSeriesByUID(t, e, s.UID)
	otherPluginItem, err := e.lib.AddToList(bg, "book2", "book", bookWith("x", fp(1)), Entry{Status: Planned})
	if err != nil {
		t.Fatal(err)
	}
	otherPluginSeries := mustSeries(t, e, otherPluginItem, "Other plugin")
	grand := mustSeries(t, e, addBook(t, e.lib, "g", fp(1), Entry{Status: Planned}), "Grand")
	mid := mustSeries(t, e, addBook(t, e.lib, "m", fp(1), Entry{Status: Planned}), "Mid")
	if err := e.lib.UpdateSeriesDetails(bg, mid.ID, k, SeriesDetails{Title: "Mid", ParentID: grand.ID}); err != nil {
		t.Fatal(err)
	}
	// s gets a spin-off of its own, so it cannot become one
	withChild := mustSeries(t, e, addBook(t, e.lib, "w", fp(1), Entry{Status: Planned}), "With child")
	child := mustSeries(t, e, addBook(t, e.lib, "c", fp(1), Entry{Status: Planned}), "Child")
	if err := e.lib.UpdateSeriesDetails(bg, child.ID, k, SeriesDetails{Title: "Child", ParentID: withChild.ID}); err != nil {
		t.Fatal(err)
	}
	book := addBook(t, e.lib, "b", fp(1), Entry{Status: Planned})
	// saved as an item but without a list entry
	unlisted, err := e.lib.SaveItem(bg, "book", "book", bookWith("u", fp(1)))
	if err != nil {
		t.Fatal(err)
	}
	noLink := k
	noLink.SeriesLink = nil
	otherKind := k
	otherKind.SeriesLink = &plugin.SeriesLinkDef{Kind: "novel"}
	tvKind, err := e.lib.kindDef("tv", "show")
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct {
		id   int64
		kind plugin.KindDef
		d    SeriesDetails
		want string // a word the message names
	}{
		"empty title":          {s.ID, k, SeriesDetails{Title: " "}, "title"},
		"bad cover":            {s.ID, k, SeriesDetails{Title: "S", CoverPath: "../x.jpg"}, "cover"},
		"long description":     {s.ID, k, SeriesDetails{Title: "S", Description: strings.Repeat("x", 20001)}, "description"},
		"control description":  {s.ID, k, SeriesDetails{Title: "S", Description: "a\x00b"}, "description"},
		"negative parts total": {s.ID, k, SeriesDetails{Title: "S", PartsTotal: -1}, "parts"},
		"link other plugin":    {s.ID, k, SeriesDetails{Title: "S", LinkedItemID: otherPluginItem}, "link"},
		"link other kind":      {s.ID, otherKind, SeriesDetails{Title: "S", LinkedItemID: book}, "link"},
		"link not declared":    {s.ID, noLink, SeriesDetails{Title: "S", LinkedItemID: book}, "link"},
		"link unknown item":    {s.ID, k, SeriesDetails{Title: "S", LinkedItemID: 99999}, "link"},
		"link not in list":     {s.ID, k, SeriesDetails{Title: "S", LinkedItemID: unlisted}, "not in your list"},
		"parent self":          {s.ID, k, SeriesDetails{Title: "S", ParentID: s.ID}, "parent"},
		"parent has a parent":  {s.ID, k, SeriesDetails{Title: "S", ParentID: mid.ID}, "parent"},
		"parent other plugin":  {s.ID, k, SeriesDetails{Title: "S", ParentID: otherPluginSeries.ID}, "parent"},
		"parent unknown":       {s.ID, k, SeriesDetails{Title: "S", ParentID: 99999}, "parent"},
		"has spin-offs":        {withChild.ID, k, SeriesDetails{Title: "With child", ParentID: grand.ID}, "spin-off"},
		"unknown field":        {s.ID, k, SeriesDetails{Title: "S", Fields: map[string]plugin.FieldValue{"nope": {Text: "x"}}}, "nope"},
		"wrong type":           {s.ID, k, SeriesDetails{Title: "S", Fields: map[string]plugin.FieldValue{"volumes": {Text: "many"}}}, "volumes"},
		"below min":            {s.ID, k, SeriesDetails{Title: "S", Fields: map[string]plugin.FieldValue{"volumes": {Num: fp(-1)}}}, "volumes"},
		"other kind def":       {s.ID, tvKind, SeriesDetails{Title: "S"}, "kind"},
	}
	for name, c := range cases {
		// a valid value alongside, which must not be stored either
		c.d.Description = cmpOr(c.d.Description, "kept out")
		err := e.lib.UpdateSeriesDetails(bg, c.id, c.kind, c.d)
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want ErrInvalid naming %q", name, err, c.want)
		}
	}
	if got := mustSeriesByUID(t, e, s.UID); got.Title != before.Title || got.Description != "" || got.ParentID != 0 || got.LinkedItemID != 0 {
		t.Fatalf("stored after refusal: %+v", got)
	}
	if got := mustSeriesByUID(t, e, withChild.UID); got.ParentID != 0 || got.Description != "" {
		t.Fatalf("with child stored: %+v", got)
	}
	if n := count(t, e.db, `SELECT count(*) FROM series_fields`); n != 0 {
		t.Fatalf("series_fields rows = %d", n)
	}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// blocks returns the main and spin-off item ids of a series in position
// order, checking that each block's positions are dense.
func blocks(t *testing.T, e *env, seriesID int64) (main, spin []int64) {
	t.Helper()
	rows, err := e.db.Query(`SELECT item_id, position, spin_off FROM series_parts WHERE series_id = ? ORDER BY spin_off, position`, seriesID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var pos int
		var so bool
		if err := rows.Scan(&id, &pos, &so); err != nil {
			t.Fatal(err)
		}
		b := &main
		if so {
			b = &spin
		}
		if pos != len(*b)+1 {
			t.Fatalf("spin_off=%v: position %d at index %d: not dense", so, pos, len(*b))
		}
		*b = append(*b, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return main, spin
}

func wantBlocks(t *testing.T, e *env, seriesID int64, main, spin []int64) {
	t.Helper()
	gm, gs := blocks(t, e, seriesID)
	if !sameIDs(gm, main) || !sameIDs(gs, spin) {
		t.Fatalf("blocks = %v / %v, want %v / %v", gm, gs, main, spin)
	}
	order, err := e.lib.SeriesPartsOrder(bg, seriesID)
	if err != nil {
		t.Fatal(err)
	}
	var om, os []int64
	for _, p := range order {
		if p.SpinOff {
			if p.Position != len(os)+1 {
				t.Fatalf("SeriesPartsOrder spin-off position %+v", p)
			}
			os = append(os, p.ItemID)
		} else {
			if len(os) > 0 {
				t.Fatalf("main part after spin-offs: %+v", order)
			}
			if p.Position != len(om)+1 {
				t.Fatalf("SeriesPartsOrder main position %+v", p)
			}
			om = append(om, p.ItemID)
		}
	}
	if !sameIDs(om, main) || !sameIDs(os, spin) {
		t.Fatalf("SeriesPartsOrder = %+v", order)
	}
}

func TestSpinOffFlipKeepsBlocksDense(t *testing.T) {
	e := newEnv(t)
	a := addBook(t, e.lib, "a", fp(1), Entry{Status: Planned})
	b := addBook(t, e.lib, "b", fp(1), Entry{Status: Planned})
	c := addBook(t, e.lib, "c", fp(1), Entry{Status: Planned})
	s := mustSeries(t, e, a, "S")
	mustAddPart(t, e, s.ID, b, c)
	flip := func(item int64, on bool) {
		t.Helper()
		if err := e.lib.SetPartSpinOff(bg, s.ID, item, on); err != nil {
			t.Fatal(err)
		}
	}
	flip(b, true)
	wantBlocks(t, e, s.ID, []int64{a, c}, []int64{b})
	flip(c, true)
	wantBlocks(t, e, s.ID, []int64{a}, []int64{b, c})
	// the only main part becomes a spin-off: the series stays
	flip(a, true)
	wantBlocks(t, e, s.ID, nil, []int64{b, c, a})
	if ser, pos, so, err := e.lib.SeriesOfItem(bg, a); err != nil || ser == nil || ser.ID != s.ID || pos != 3 || !so {
		t.Fatalf("SeriesOfItem(a) = %+v %d %v %v", ser, pos, so, err)
	}
	flip(b, false)
	wantBlocks(t, e, s.ID, []int64{b}, []int64{c, a})
	flip(b, false) // already main: nothing changes
	wantBlocks(t, e, s.ID, []int64{b}, []int64{c, a})
	if ser, pos, so, err := e.lib.SeriesOfItem(bg, b); err != nil || ser == nil || pos != 1 || so {
		t.Fatalf("SeriesOfItem(b) = %+v %d %v %v", ser, pos, so, err)
	}
	// SeriesParts: main parts then spin-offs
	rows, err := e.lib.SeriesParts(bg, s.ID, "en")
	if err != nil || len(rows) != 3 || rows[0].Item.ID != b || rows[1].Item.ID != c || rows[2].Item.ID != a {
		t.Fatalf("SeriesParts = %v %v", rows, err)
	}
	m, err := e.lib.SeriesOfItems(bg, []int64{a, b, c})
	if err != nil || m[a].Position != 2 || !m[a].SpinOff || m[b].Position != 1 || m[b].SpinOff || m[c].Position != 1 || !m[c].SpinOff {
		t.Fatalf("SeriesOfItems = %+v %v", m, err)
	}
	// AddPart appends to the main block
	d := addBook(t, e.lib, "d", fp(1), Entry{Status: Planned})
	mustAddPart(t, e, s.ID, d)
	wantBlocks(t, e, s.ID, []int64{b, d}, []int64{c, a})
	// removing a spin-off renumbers its block
	if err := e.lib.RemovePart(bg, s.ID, c); err != nil {
		t.Fatal(err)
	}
	wantBlocks(t, e, s.ID, []int64{b, d}, []int64{a})
	if err := e.lib.SetPartSpinOff(bg, s.ID, c, true); !errors.Is(err, ErrNoSeries) {
		t.Fatalf("not a part: %v", err)
	}
	// main parts gone, a spin-off left: the series stays; then it goes
	for _, id := range []int64{b, d} {
		if err := e.lib.RemovePart(bg, s.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	wantBlocks(t, e, s.ID, nil, []int64{a})
	if err := e.lib.RemovePart(bg, s.ID, a); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.db, `SELECT count(*) FROM series`); n != 0 {
		t.Fatalf("empty series kept: %d", n)
	}
}

func TestMovePartStaysInBlock(t *testing.T) {
	e := newEnv(t)
	a := addBook(t, e.lib, "a", fp(1), Entry{Status: Planned})
	b := addBook(t, e.lib, "b", fp(1), Entry{Status: Planned})
	c := addBook(t, e.lib, "c", fp(1), Entry{Status: Planned})
	d := addBook(t, e.lib, "d", fp(1), Entry{Status: Planned})
	x := addBook(t, e.lib, "x", fp(1), Entry{Status: Planned})
	s := mustSeries(t, e, a, "S")
	mustAddPart(t, e, s.ID, b, c, d, x)
	for _, id := range []int64{c, x} {
		if err := e.lib.SetPartSpinOff(bg, s.ID, id, true); err != nil {
			t.Fatal(err)
		}
	}
	wantBlocks(t, e, s.ID, []int64{a, b, d}, []int64{c, x})
	move := func(item int64, delta int) {
		t.Helper()
		if err := e.lib.MovePart(bg, s.ID, item, delta); err != nil {
			t.Fatal(err)
		}
	}
	move(d, 1) // last main part: does not enter the spin-off block
	wantBlocks(t, e, s.ID, []int64{a, b, d}, []int64{c, x})
	move(c, -1) // first spin-off: does not enter the main block
	wantBlocks(t, e, s.ID, []int64{a, b, d}, []int64{c, x})
	move(b, 1)
	wantBlocks(t, e, s.ID, []int64{a, d, b}, []int64{c, x})
	move(x, -1)
	wantBlocks(t, e, s.ID, []int64{a, d, b}, []int64{x, c})
	move(a, -1)
	wantBlocks(t, e, s.ID, []int64{a, d, b}, []int64{x, c})
	move(c, 1)
	wantBlocks(t, e, s.ID, []int64{a, d, b}, []int64{x, c})
}

func TestSeriesLinkAndParentClearedOnDelete(t *testing.T) {
	e := newEnv(t)
	k := seriesKind(t, e)
	s := mustSeries(t, e, addBook(t, e.lib, "1", fp(1), Entry{Status: Planned}), "S")
	linked := addBook(t, e.lib, "L", fp(1), Entry{Status: Planned})
	parent := mustSeries(t, e, addBook(t, e.lib, "P", fp(1), Entry{Status: Planned}), "Parent")
	if err := e.lib.UpdateSeriesDetails(bg, s.ID, k, SeriesDetails{Title: "S", Description: "Own", LinkedItemID: linked, ParentID: parent.ID,
		Fields: map[string]plugin.FieldValue{"publisher": {Text: "Acme"}}}); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.DeleteItem(bg, linked); err != nil {
		t.Fatal(err)
	}
	got := mustSeriesByUID(t, e, s.UID)
	if got.LinkedItemID != 0 || got.ParentID != parent.ID || got.Description != "Own" || got.Fields["publisher"].Text != "Acme" {
		t.Fatalf("after deleting the linked entry: %+v", got)
	}
	if err := e.lib.DeleteSeries(bg, parent.ID, false); err != nil {
		t.Fatal(err)
	}
	got = mustSeriesByUID(t, e, s.UID)
	if got.ParentID != 0 || got.Description != "Own" || got.Fields["publisher"].Text != "Acme" {
		t.Fatalf("after deleting the parent: %+v", got)
	}
	// a parent keeps its spin-off when its last part goes (R6: it has a
	// spin-off series, so it stays without parts)
	last := addBook(t, e.lib, "Q", fp(1), Entry{Status: Planned})
	p2 := mustSeries(t, e, last, "Parent 2")
	if err := e.lib.UpdateSeriesDetails(bg, s.ID, k, SeriesDetails{Title: "S", ParentID: p2.ID}); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.RemovePart(bg, p2.ID, last); err != nil {
		t.Fatal(err)
	}
	if got := mustSeriesByUID(t, e, s.UID); got.ParentID != p2.ID {
		t.Fatalf("after the parent's last part went: %+v", got)
	}
}

func TestSpinOffsOf(t *testing.T) {
	e := newEnv(t)
	k := seriesKind(t, e)
	parent := mustSeries(t, e, addBook(t, e.lib, "P", fp(1), Entry{Status: Planned}), "Parent")
	beta := mustSeries(t, e, addBook(t, e.lib, "B", fp(1), Entry{Status: Planned}), "beta")
	alpha := mustSeries(t, e, addBook(t, e.lib, "A", fp(1), Entry{Status: Planned}), "Alpha")
	other := mustSeries(t, e, addBook(t, e.lib, "O", fp(1), Entry{Status: Planned}), "Other")
	for _, c := range []Series{beta, alpha} {
		if err := e.lib.UpdateSeriesDetails(bg, c.ID, k, SeriesDetails{Title: c.Title, ParentID: parent.ID}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := e.lib.SpinOffsOf(bg, parent.ID)
	if err != nil || len(got) != 2 || got[0].ID != alpha.ID || got[1].ID != beta.ID || got[0].ParentID != parent.ID {
		t.Fatalf("SpinOffsOf = %+v %v", got, err)
	}
	if got, err := e.lib.SpinOffsOf(bg, other.ID); err != nil || len(got) != 0 {
		t.Fatalf("no spin-offs: %+v %v", got, err)
	}
}

// TestSeriesDescriptionCapCountsNormalisedText: the 20000-character cap counts
// the stored text, after line breaks are normalised (CRLF is one character).
func TestSeriesDescriptionCapCountsNormalisedText(t *testing.T) {
	e := newEnv(t)
	k := seriesKind(t, e)
	s := mustSeries(t, e, addBook(t, e.lib, "1", fp(1), Entry{Status: Planned}), "S")
	// 20000 characters once each CRLF is one line break (30000 as sent)
	text := strings.Repeat("ab\r\n", maxSeriesDescriptionRunes/3) + "ab"
	want := strings.ReplaceAll(text, "\r\n", "\n")
	if n := utf8.RuneCountInString(want); n > maxSeriesDescriptionRunes || n < maxSeriesDescriptionRunes-3 {
		t.Fatalf("fixture has %d characters", n)
	}
	if err := e.lib.UpdateSeriesDetails(bg, s.ID, k, SeriesDetails{Title: "S", Description: text}); err != nil {
		t.Fatalf("description of %d characters after normalising: %v", utf8.RuneCountInString(want), err)
	}
	if got := mustSeriesByUID(t, e, s.UID).Description; got != want {
		t.Fatalf("stored %d characters, want %d", utf8.RuneCountInString(got), utf8.RuneCountInString(want))
	}
	// one character more is still refused
	over := want + strings.Repeat("x", maxSeriesDescriptionRunes-utf8.RuneCountInString(want)+1)
	if err := e.lib.UpdateSeriesDetails(bg, s.ID, k, SeriesDetails{Title: "S", Description: over}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("over the cap: %v", err)
	}
}

// TestSeriesStaleLinkKeepsSaving: a linked entry that left the list later
// (removed entry) does not block saving the series with the link unchanged;
// choosing it anew is still refused.
func TestSeriesStaleLinkKeepsSaving(t *testing.T) {
	e := newEnv(t)
	k := seriesKind(t, e)
	s := mustSeries(t, e, addBook(t, e.lib, "1", fp(1), Entry{Status: Planned}), "S")
	linked := addBook(t, e.lib, "L", fp(1), Entry{Status: Planned})
	if err := e.lib.UpdateSeriesDetails(bg, s.ID, k, SeriesDetails{Title: "S", LinkedItemID: linked}); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.RemoveEntry(bg, linked); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.UpdateSeriesDetails(bg, s.ID, k, SeriesDetails{Title: "S", Description: "New", LinkedItemID: linked}); err != nil {
		t.Fatalf("saving with the unchanged stale link: %v", err)
	}
	if got := mustSeriesByUID(t, e, s.UID); got.Description != "New" || got.LinkedItemID != linked {
		t.Fatalf("stored %+v", got)
	}
	other := mustSeries(t, e, addBook(t, e.lib, "2", fp(1), Entry{Status: Planned}), "Other")
	if err := e.lib.UpdateSeriesDetails(bg, other.ID, k, SeriesDetails{Title: "Other", LinkedItemID: linked}); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "not in your list") {
		t.Fatalf("a new link to an unlisted entry: %v", err)
	}
}

// TestPartlessSeriesKeptWithOwnData (R6): removing the last part (RemovePart,
// RemoveEntry or DeleteItem) keeps a series that holds own data or has
// spin-off series, with zero parts; one without either goes.
func TestPartlessSeriesKeptWithOwnData(t *testing.T) {
	e := newEnv(t)
	k := seriesKind(t, e)
	removals := map[string]func(seriesID, itemID int64) error{
		"remove part":  func(s, i int64) error { return e.lib.RemovePart(bg, s, i) },
		"remove entry": func(_, i int64) error { return e.lib.RemoveEntry(bg, i) },
		"delete item":  func(_, i int64) error { return e.lib.DeleteItem(bg, i) },
	}
	linked := addBook(t, e.lib, "L", fp(1), Entry{Status: Planned})
	own := map[string]SeriesDetails{
		"cover":       {CoverPath: "c.jpg"},
		"description": {Description: "Own"},
		"started":     {StartedOn: sday(2001, 1, 1)},
		"ended":       {EndedOn: sday(2002, 1, 1)},
		"finished":    {Finished: true},
		"parts total": {PartsTotal: 5},
		"field":       {Fields: map[string]plugin.FieldValue{"publisher": {Text: "Acme"}}},
		"link":        {LinkedItemID: linked},
		"spin-off":    {},
		"nothing":     {},
	}
	n := 0
	for what, d := range own {
		for how, remove := range removals {
			n++
			item := addBook(t, e.lib, fmt.Sprint("p", n), fp(1), Entry{Status: Completed})
			s := mustSeries(t, e, item, what+" "+how)
			d.Title = s.Title
			if err := e.lib.UpdateSeriesDetails(bg, s.ID, k, d); err != nil {
				t.Fatal(err)
			}
			if what == "spin-off" {
				kid := mustSeries(t, e, addBook(t, e.lib, fmt.Sprint("k", n), fp(1), Entry{Status: Planned}), "kid "+how)
				if err := e.lib.UpdateSeriesDetails(bg, kid.ID, k, SeriesDetails{Title: kid.Title, ParentID: s.ID}); err != nil {
					t.Fatal(err)
				}
			}
			if err := remove(s.ID, item); err != nil {
				t.Fatalf("%s, %s: %v", what, how, err)
			}
			got, err := e.lib.SeriesByUID(bg, "book", s.UID)
			if what == "nothing" {
				if !errors.Is(err, ErrNoSeries) {
					t.Errorf("%s, %s: an empty series without own data stays: %v", what, how, err)
				}
				continue
			}
			if err != nil {
				t.Errorf("%s, %s: the series went: %v", what, how, err)
				continue
			}
			if parts, _ := e.lib.SeriesPartsOrder(bg, got.ID); len(parts) != 0 {
				t.Errorf("%s, %s: %d parts", what, how, len(parts))
			}
		}
	}
}

// TestPartlessSeriesReads: every read path takes a series without parts: it
// is planned, 0/expected, listed, counted, backed up and resolved.
func TestPartlessSeriesReads(t *testing.T) {
	e := newEnv(t)
	k := seriesKind(t, e)
	item := addBook(t, e.lib, "1", fp(1), Entry{Status: Active})
	s := mustSeries(t, e, item, "Empty")
	if err := e.lib.UpdateSeriesDetails(bg, s.ID, k, SeriesDetails{Title: "Empty", Description: "Own", PartsTotal: 4}); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.RemovePart(bg, s.ID, item); err != nil {
		t.Fatal(err)
	}
	if got := DeriveSeriesStatus(nil, 0); got != Planned {
		t.Errorf("no parts: %s", got)
	}
	if got := DeriveSeriesStatus(nil, 4); got != Planned {
		t.Errorf("no parts, 4 expected: %s", got)
	}
	pg, err := e.lib.ListSeries(bg, SeriesQuery{PluginID: "book"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pg.Rows) != 1 || pg.Rows[0].Status != Planned || pg.Rows[0].Parts != 0 || pg.Rows[0].PartsTotal != 4 || pg.Rows[0].Updated.IsZero() {
		t.Fatalf("list: %+v", pg.Rows)
	}
	if pg, _ := e.lib.ListSeries(bg, SeriesQuery{PluginID: "book", Status: Planned}); len(pg.Rows) != 1 {
		t.Errorf("planned filter: %d rows", len(pg.Rows))
	}
	counts, err := e.lib.SeriesCounts(bg, SeriesQuery{PluginID: "book"})
	if err != nil {
		t.Fatal(err)
	}
	if counts[Planned] != 1 || counts[Active] != 0 {
		t.Errorf("counts: %v", counts)
	}
	if got, err := e.lib.ActiveSeries(bg, []string{"book"}, 10); err != nil || len(got) != 0 {
		t.Errorf("active: %v %+v", err, got)
	}
	res, err := e.lib.LoadResolvedSeries(bg, mustSeriesByUID(t, e, s.UID), "en")
	if err != nil || res.Status != Planned || res.PartsTotal != 4 || res.Description != "Own" || res.MainParts != 0 {
		t.Fatalf("resolved: %v %+v", err, res)
	}
	var backed []BackupSeries
	if err := e.lib.BackupSeries(bg, func(bs BackupSeries) error { backed = append(backed, bs); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(backed) != 1 || backed[0].UID != s.UID || len(backed[0].Parts) != 0 || backed[0].Parts == nil || backed[0].Description != "Own" {
		t.Fatalf("backup: %+v", backed)
	}
	// a part can be added again
	if err := e.lib.AddPart(bg, s.ID, item, ""); err != nil {
		t.Fatal(err)
	}
}

// TestRestoreSeriesCoverOutsideWriteLock: the series cover is resolved (and
// stored) before the write transaction starts, so the database stays
// writable meanwhile, like RestoreItem; an existing uid resolves nothing.
func TestRestoreSeriesCoverOutsideWriteLock(t *testing.T) {
	e := newEnv(t)
	addBook(t, e.lib, "1", fp(1), Entry{Status: Planned})
	calls := 0
	blocked := false
	opts := RestoreOptions{ResolveCover: func(ctx context.Context, name string) (string, string, error) {
		calls++
		done := make(chan error, 1)
		go func() {
			_, err := e.db.ExecContext(context.Background(), `UPDATE items SET updated_at = updated_at WHERE id < 0`)
			done <- err
		}()
		select {
		case err := <-done:
			if err != nil {
				return "", "", err
			}
		case <-time.After(300 * time.Millisecond):
			blocked = true
		}
		return name, "", nil
	}}
	bs := BackupSeries{UID: "u-c", PluginID: "book", Kind: "book", Title: "C", Cover: "c.png",
		Parts: []BackupPartRef{{PluginID: "book", Kind: "book", Source: "books", SourceID: "1"}}}
	res, err := e.lib.RestoreSeries(bg, bs, opts)
	if err != nil || !res.Created {
		t.Fatalf("restore: %v %+v", err, res)
	}
	if blocked {
		t.Error("the cover was resolved while the write transaction held the database")
	}
	if got := mustSeriesByUID(t, e, "u-c"); got.CoverPath != "c.png" {
		t.Errorf("cover %q", got.CoverPath)
	}
	if _, err := e.lib.RestoreSeries(bg, bs, opts); err != nil || calls != 1 {
		t.Errorf("existing uid: %v, %d cover calls", err, calls)
	}
}

// TestRestoreSeriesNotesHaveNoErrorPrefix: a value left out is explained
// without the library's raw "invalid: " prefix.
func TestRestoreSeriesNotesHaveNoErrorPrefix(t *testing.T) {
	e := newEnv(t)
	addBook(t, e.lib, "1", fp(1), Entry{Status: Planned})
	bs := BackupSeries{UID: "u-n", PluginID: "book", Kind: "book", Title: "N", PartsTotal: -3, Description: "a\x01b",
		Parts: []BackupPartRef{{PluginID: "book", Kind: "book", Source: "books", SourceID: "1"}}}
	res, err := e.lib.RestoreSeries(bg, bs, RestoreOptions{})
	if err != nil || !res.Created || len(res.Notes) != 2 {
		t.Fatalf("restore: %v %+v", err, res)
	}
	for _, n := range res.Notes {
		if strings.Contains(n, "invalid: ") {
			t.Errorf("note with the raw prefix: %q", n)
		}
	}
}

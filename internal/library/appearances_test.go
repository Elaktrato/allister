package library

import (
	"reflect"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

func TestAppearancesCharacterAndActor(t *testing.T) {
	l := newLib(t)
	save := func(pluginID, kind, id, title string, credits ...plugin.Credit) int64 {
		var d plugin.ItemData
		if pluginID == "tv" {
			d = tvData(id, nil)
		} else {
			d = bookData(id)
		}
		d.Titles = []plugin.Title{{Lang: "en", Text: title}}
		itemID := mustSave(t, l, pluginID, kind, d)
		if err := l.SaveCredits(bg, itemID, credits); err != nil {
			t.Fatal(err)
		}
		return itemID
	}
	hero := plugin.PersonRef{ID: "c1", Name: "Hero"}
	villain := plugin.PersonRef{ID: "c2", Name: "Villain"}
	actor := plugin.PersonRef{ID: "a1", Name: "Actor"}
	actor2 := plugin.PersonRef{ID: "a2", Name: "Other Actor"}
	// zeta: Actor voices Hero and Villain; Other Actor is credited directly
	zeta := save("tv", "show", "z", "Zeta",
		plugin.Credit{Role: "character", Person: hero, With: &actor, WithRole: "actor"},
		plugin.Credit{Role: "character", Person: villain, With: &actor, WithRole: "actor"},
		plugin.Credit{Role: "actor", Person: actor2})
	// alpha: Hero again with a different actor; Actor credited directly
	alpha := save("tv", "show", "a", "alpha",
		plugin.Credit{Role: "character", Person: hero, With: &actor2, WithRole: "actor"},
		plugin.Credit{Role: "actor", Person: actor})
	// a book of the other plugin with the same person ids: separate people rows
	save("book", "book", "b", "Book", plugin.Credit{Role: "author", Person: actor})
	save("book2", "book", "b2", "Book2", plugin.Credit{Role: "author", Person: hero})
	person := func(pluginID, sid string) *Person {
		t.Helper()
		var id int64
		if err := l.db.QueryRow(`SELECT id FROM `+tablePeople+` WHERE plugin_id = ? AND source_id = ?`, pluginID, sid).Scan(&id); err != nil {
			t.Fatal(err)
		}
		p, err := l.Person(bg, id)
		if err != nil || p == nil {
			t.Fatal(err)
		}
		return p
	}
	type row struct {
		item           int64
		role           string
		with, withRole string
	}
	flat := func(as []Appearance) []row {
		var out []row
		for _, a := range as {
			r := row{item: a.ItemID, role: a.Role, withRole: a.WithRole}
			if a.With != nil {
				r.with = a.With.Name
			}
			out = append(out, r)
		}
		return out
	}
	// Actor: voices two characters in zeta (two rows), credited directly in alpha
	a1 := person("tv", "a1")
	as, err := l.Appearances(bg, a1.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []row{
		{alpha, "actor", "", ""},
		{zeta, "actor", "Hero", "character"},
		{zeta, "actor", "Villain", "character"},
	}
	if got := flat(as); !reflect.DeepEqual(got, want) {
		t.Fatalf("actor rows = %+v", got)
	}
	if as[0].PluginID != "tv" || as[0].Kind != "show" || as[0].Source != "tvdata" || as[0].SourceID != "a" || as[0].CoverPath != "" ||
		len(as[0].Titles) != 1 || as[0].Titles[0].Text != "alpha" {
		t.Fatalf("appearance = %+v", as[0])
	}
	// Hero: a character in both shows, voiced by different actors (ordered by lower-cased title, id, seq)
	hid := person("tv", "c1").ID
	as, _ = l.Appearances(bg, hid)
	want = []row{{alpha, "character", "Other Actor", "actor"}, {zeta, "character", "Actor", "actor"}}
	if got := flat(as); !reflect.DeepEqual(got, want) {
		t.Fatalf("character rows = %+v", got)
	}
	// Other Actor is credited directly in zeta and is the counterpart of Hero in alpha: both kinds of rows
	as, _ = l.Appearances(bg, person("tv", "a2").ID)
	want = []row{{alpha, "actor", "Hero", "character"}, {zeta, "actor", "", ""}}
	if got := flat(as); !reflect.DeepEqual(got, want) {
		t.Fatalf("both kinds rows = %+v", got)
	}
	// people are per plugin: the book-side people only see book items
	as, _ = l.Appearances(bg, person("book", "a1").ID)
	if len(as) != 1 || as[0].PluginID != "book" || as[0].Role != "author" || as[0].With != nil {
		t.Fatalf("book person rows = %+v", as)
	}
	// unknown person: no rows
	if as, err := l.Appearances(bg, 99999); err != nil || len(as) != 0 {
		t.Fatalf("unknown = %v, %v", as, err)
	}
}

func TestAppearancesMarkCounterpartRows(t *testing.T) {
	l := newLib(t)
	hero := plugin.PersonRef{ID: "c1", Name: "Hero"}
	actor := plugin.PersonRef{ID: "a1", Name: "Actor"}
	d := tvData("z", nil)
	itemID := mustSave(t, l, "tv", "show", d)
	if err := l.SaveCredits(bg, itemID, []plugin.Credit{
		{Role: "character", Person: hero, With: &actor, WithRole: "actor"},
		{Role: "actor", Person: actor},
	}); err != nil {
		t.Fatal(err)
	}
	var aid, hid int64
	l.db.QueryRow(`SELECT id FROM ` + tablePeople + ` WHERE source_id = 'a1'`).Scan(&aid)
	l.db.QueryRow(`SELECT id FROM ` + tablePeople + ` WHERE source_id = 'c1'`).Scan(&hid)
	as, err := l.Appearances(bg, aid)
	if err != nil || len(as) != 2 {
		t.Fatalf("actor rows %+v %v", as, err)
	}
	// credit order: the counterpart row (seq 0) first, then the direct credit
	if !as[0].AsCounterpart || as[0].With == nil || as[0].With.Name != "Hero" || as[1].AsCounterpart {
		t.Fatalf("actor rows %+v", as)
	}
	as, _ = l.Appearances(bg, hid)
	if len(as) != 1 || as[0].AsCounterpart || as[0].With == nil || as[0].With.Name != "Actor" {
		t.Fatalf("character rows %+v", as)
	}
}

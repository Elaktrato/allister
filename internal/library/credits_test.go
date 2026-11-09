package library

import (
	"errors"
	"fmt"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

func tvItem(t *testing.T, l *Library, id string) int64 {
	t.Helper()
	return mustSave(t, l, "tv", "show", plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "tvdata", ID: id, Kind: "show", Titles: []plugin.Title{{Lang: "en", Text: "S" + id}}}})
}

func TestSaveCreditsIsIdempotent(t *testing.T) {
	e := newEnv(t)
	id := tvItem(t, e.lib, "1")
	cs := []plugin.Credit{
		{Role: "actor", Person: plugin.PersonRef{ID: "p1", Name: "A", ImageURL: "https://img/a.jpg", URL: "https://x/a"}},
		{Role: "actor", Person: plugin.PersonRef{ID: "p2", Name: "B"}},
	}
	for i := 0; i < 3; i++ {
		if err := e.lib.SaveCredits(ctx, id, cs); err != nil {
			t.Fatal(err)
		}
	}
	got, err := e.lib.Credits(ctx, id)
	if err != nil || len(got) != 2 {
		t.Fatalf("credits = %v, %v", got, err)
	}
	if n := count(t, e.db, `SELECT count(*) FROM people_g`); n != 2 {
		t.Fatalf("people = %d", n)
	}
	if got[0].Seq != 0 || got[1].Seq != 1 || got[0].Person.SourceID != "p1" || got[0].Person.Source != "tvdata" || got[0].Person.PluginID != "tv" ||
		got[0].Person.ImagePath != "https://img/a.jpg" || got[0].Person.ExternalURL != "https://x/a" || got[0].Role != "actor" {
		t.Fatalf("credit[0] = %+v", got[0])
	}
	// re-sync with fewer credits and a changed name
	cs = []plugin.Credit{{Role: "actor", Person: plugin.PersonRef{ID: "p2", Name: "B2"}}}
	if err := e.lib.SaveCredits(ctx, id, cs); err != nil {
		t.Fatal(err)
	}
	got, _ = e.lib.Credits(ctx, id)
	if len(got) != 1 || got[0].Person.Name != "B2" {
		t.Fatalf("credits = %+v", got)
	}
	if err := e.lib.SaveCredits(ctx, id, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.lib.Credits(ctx, id); len(got) != 0 {
		t.Fatal("not cleared")
	}
}

func TestCreditsKeepCounterpart(t *testing.T) {
	l := newLib(t)
	id := tvItem(t, l, "1")
	cs := []plugin.Credit{
		{Role: "main", Person: plugin.PersonRef{ID: "c1", Name: "Hero"}, With: &plugin.PersonRef{ID: "v1", Name: "Voice", ImageURL: "https://img/v.jpg"}, WithRole: "voice"},
		{Role: "author", Person: plugin.PersonRef{ID: "w1", Name: "Writer"}},
	}
	if err := l.SaveCredits(ctx, id, cs); err != nil {
		t.Fatal(err)
	}
	got, err := l.Credits(ctx, id)
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	if got[0].With == nil || got[0].With.Name != "Voice" || got[0].With.ID == 0 || got[0].WithRole != "voice" || got[0].Person.Name != "Hero" {
		t.Fatalf("credit 0 = %+v", got[0])
	}
	if got[1].With != nil || got[1].WithRole != "" {
		t.Fatalf("credit 1 = %+v", got[1])
	}
	people, err := l.ItemPeople(ctx, id)
	if err != nil || len(people) != 3 {
		t.Fatalf("people = %v, %v", people, err)
	}
	p, err := l.Person(ctx, got[0].With.ID)
	if err != nil || p == nil || p.Name != "Voice" {
		t.Fatalf("Person = %v, %v", p, err)
	}
	if p, err := l.Person(ctx, 9999); err != nil || p != nil {
		t.Fatalf("Person unknown = %v, %v", p, err)
	}
}

func TestPeopleAreNamespacedPerPlugin(t *testing.T) {
	e := newEnv(t)
	a := mustSave(t, e.lib, "book", "book", bookData("1"))
	b := mustSave(t, e.lib, "book2", "book", bookData("1"))
	c := []plugin.Credit{{Role: "author", Person: plugin.PersonRef{ID: "p1", Name: "Same"}}}
	for _, id := range []int64{a, b} {
		if err := e.lib.SaveCredits(ctx, id, c); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, e.db, `SELECT count(*) FROM people_g`); n != 2 {
		t.Fatalf("people = %d, want 2", n)
	}
	pa, _ := e.lib.ItemPeople(ctx, a)
	pb, _ := e.lib.ItemPeople(ctx, b)
	if pa[0].ID == pb[0].ID || pa[0].PluginID != "book" || pb[0].PluginID != "book2" {
		t.Fatalf("%+v %+v", pa, pb)
	}
}

func TestSetPersonImagePathSurvivesResync(t *testing.T) {
	l := newLib(t)
	id := tvItem(t, l, "1")
	c := []plugin.Credit{{Role: "actor", Person: plugin.PersonRef{ID: "p1", Name: "A", ImageURL: "https://img/a.jpg"}}}
	if err := l.SaveCredits(ctx, id, c); err != nil {
		t.Fatal(err)
	}
	pp, _ := l.ItemPeople(ctx, id)
	pid := pp[0].ID
	if err := l.SetPersonImagePath(ctx, pid, "1.jpg"); err != nil {
		t.Fatal(err)
	}
	c[0].Person.Name = "A2"
	c[0].Person.ImageURL = "https://img/new.jpg"
	if err := l.SaveCredits(ctx, id, c); err != nil {
		t.Fatal(err)
	}
	p, _ := l.Person(ctx, pid)
	if p.ImagePath != "1.jpg" || p.Name != "A2" {
		t.Fatalf("person = %+v", p)
	}
	// a remote url is replaced by a new remote url; empty incoming keeps the stored one
	if err := l.SetPersonImagePath(ctx, pid, "https://img/old.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := l.SaveCredits(ctx, id, c); err != nil {
		t.Fatal(err)
	}
	if p, _ := l.Person(ctx, pid); p.ImagePath != "https://img/new.jpg" {
		t.Fatalf("image = %q", p.ImagePath)
	}
	c[0].Person.ImageURL = ""
	if err := l.SaveCredits(ctx, id, c); err != nil {
		t.Fatal(err)
	}
	if p, _ := l.Person(ctx, pid); p.ImagePath != "https://img/new.jpg" {
		t.Fatalf("image = %q", p.ImagePath)
	}
	if err := l.SetPersonImagePath(ctx, 9999, "x"); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("err = %v", err)
	}
}

func TestDeleteItemCascadesButKeepsPeople(t *testing.T) {
	e := newEnv(t)
	d := bookData("1")
	d.ExternalIDs = map[string]string{"isbn": "1"}
	d.Relations = []plugin.Relation{{Kind: "sequel", Item: plugin.ItemSummary{Source: "books", ID: "2", Kind: "book"}}}
	d.Genres = []string{"g"}
	d.Fields["pages"] = plugin.FieldValue{Num: fp(3)}
	id := mustSave(t, e.lib, "book", "book", d)
	if err := e.lib.SaveCredits(ctx, id, []plugin.Credit{{Role: "author", Person: plugin.PersonRef{ID: "p1", Name: "A"}}}); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.DeleteItem(ctx, id); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"items", "item_titles", "item_fields", "external_ids", "relations_g", "item_terms", "credits"} {
		if n := count(t, e.db, `SELECT count(*) FROM `+tbl); n != 0 {
			t.Errorf("%s has %d rows", tbl, n)
		}
	}
	if n := count(t, e.db, `SELECT count(*) FROM people_g`); n != 1 {
		t.Errorf("people = %d, want 1 kept", n)
	}
}

func TestCreditsCapAndValidation(t *testing.T) {
	l := newLib(t)
	id := tvItem(t, l, "1")
	mk := func(n int) []plugin.Credit {
		out := make([]plugin.Credit, n)
		for i := range out {
			out[i] = plugin.Credit{Role: "actor", Person: plugin.PersonRef{ID: fmt.Sprint("p", i), Name: "N"}}
		}
		return out
	}
	if err := l.SaveCredits(ctx, id, mk(500)); err != nil {
		t.Fatal(err)
	}
	wantInvalid(t, l.SaveCredits(ctx, id, mk(501)), "credits")
	if got, _ := l.Credits(ctx, id); len(got) != 500 {
		t.Fatalf("a rejected save changed the credits: %d", len(got))
	}
	wantInvalid(t, l.SaveCredits(ctx, id, []plugin.Credit{{Role: "actor", Person: plugin.PersonRef{Name: "no id"}}}), "person")
	wantInvalid(t, l.SaveCredits(ctx, id, []plugin.Credit{{Role: "actor", Person: plugin.PersonRef{ID: "x"}, With: &plugin.PersonRef{}}}), "person")
	if err := l.SaveCredits(ctx, 9999, nil); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("err = %v", err)
	}
	if got, err := l.Credits(ctx, 9999); err != nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}
}

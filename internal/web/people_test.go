package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/provider"
	"github.com/allister/allister/internal/store"
)

func pperson(id, name, img, ext string) provider.Person {
	return provider.Person{Source: provider.AniList, ID: id, Name: name, ImageURL: img, URL: ext}
}

// withCast saves cast for animeID.
func (e *env) withCast(t *testing.T, animeID int64, cast ...provider.CastMember) {
	t.Helper()
	if err := e.st.SaveCast(context.Background(), animeID, cast); err != nil {
		t.Fatal(err)
	}
}

// personPath returns /people/{id} for the person with the given source id.
func (e *env) personPath(t *testing.T, sourceID string) string {
	t.Helper()
	for animeID := int64(1); animeID <= 20; animeID++ {
		people, err := e.st.AnimePeople(context.Background(), animeID)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range people {
			if p.SourceID == sourceID {
				return "/people/" + idStr(p.ID)
			}
		}
	}
	t.Fatalf("no person %s", sourceID)
	return ""
}

func titleEn(s string) []provider.Title { return []provider.Title{{Lang: "en", Text: s}} }

func TestPersonPageListsSavedAnimeOnly(t *testing.T) {
	e := newEnv(t)
	a := e.addEntry(t, provider.AniList, "1", nil, store.Watching, 0, titleEn("Saved Show")...)
	b := e.addEntry(t, provider.AniList, "2", nil, store.Watching, 0, titleEn("Other Show")...)
	va := pperson("v1", "Voice One", "", "")
	e.withCast(t, a, provider.CastMember{Character: pperson("c1", "Hero", "", ""), Actor: &va})
	e.withCast(t, b, provider.CastMember{Character: pperson("c2", "Villain", "", ""), Actor: &provider.Person{Source: provider.AniList, ID: "v2", Name: "Voice Two"}})
	rec := e.get(e.personPath(t, "v1"))
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	body := rec.Body.String()
	mustContain(t, body, "Voice One", "Saved Show", `href="/anime/anilist/1"`)
	mustNotContain(t, body, "Other Show", "Villain")
}

func TestVoiceActorShowsCharacter(t *testing.T) {
	e := newEnv(t)
	a := e.addEntry(t, provider.AniList, "1", nil, store.Watching, 0, titleEn("Show")...)
	va := pperson("v1", "Voice One", "", "")
	e.withCast(t, a, provider.CastMember{Character: pperson("c1", "Hero", "", ""), Actor: &va})
	body := e.get(e.personPath(t, "v1")).Body.String()
	mustContain(t, body, "as Hero")
}

func TestCharacterPageShowsAnimeWithoutCharacterLine(t *testing.T) {
	e := newEnv(t)
	a := e.addEntry(t, provider.AniList, "1", nil, store.Watching, 0, titleEn("Show")...)
	va := pperson("v1", "Voice One", "", "")
	e.withCast(t, a, provider.CastMember{Character: pperson("c1", "Hero", "", ""), Actor: &va})
	body := e.get(e.personPath(t, "c1")).Body.String()
	mustContain(t, body, "Hero", "Show", `href="/anime/anilist/1"`)
	mustNotContain(t, body, "as Hero", "Voice One")
}

func TestActorTwoCharactersInOneAnime(t *testing.T) {
	e := newEnv(t)
	a := e.addEntry(t, provider.AniList, "1", nil, store.Watching, 0, titleEn("Show")...)
	va := pperson("v1", "Voice One", "", "")
	e.withCast(t, a,
		provider.CastMember{Character: pperson("c1", "Hero", "", ""), Actor: &va},
		provider.CastMember{Character: pperson("c2", "Sidekick", "", ""), Actor: &va})
	body := e.get(e.personPath(t, "v1")).Body.String()
	mustContain(t, body, "as Hero", "as Sidekick")
	if n := strings.Count(body, "<li "); n != 2 {
		t.Fatalf("rows: %d", n)
	}
}

func TestActorAcrossTwoAnime(t *testing.T) {
	e := newEnv(t)
	a := e.addEntry(t, provider.AniList, "11", nil, store.Watching, 0, titleEn("Alpha")...)
	b := e.addEntry(t, provider.AniList, "22", nil, store.Watching, 0, titleEn("Beta")...)
	va := pperson("v1", "Voice One", "", "")
	e.withCast(t, a, provider.CastMember{Character: pperson("c1", "Hero", "", ""), Actor: &va})
	e.withCast(t, b, provider.CastMember{Character: pperson("c2", "Other", "", ""), Actor: &va})
	body := e.get(e.personPath(t, "v1")).Body.String()
	mustContain(t, body, `href="/anime/anilist/11"`, `href="/anime/anilist/22"`, "Alpha", "Beta")
}

func TestRemovedAnimeDisappearsAndEmptyPageRenders(t *testing.T) {
	e := newEnv(t)
	a := e.addEntry(t, provider.AniList, "1", nil, store.Watching, 0, titleEn("Gone Show")...)
	va := pperson("v1", "Voice One", "", "")
	e.withCast(t, a, provider.CastMember{Character: pperson("c1", "Hero", "", ""), Actor: &va})
	path := e.personPath(t, "v1")
	mustContain(t, e.get(path).Body.String(), "Gone Show")
	if err := e.st.DeleteAnime(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	rec := e.get(path)
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	mustContain(t, rec.Body.String(), "Voice One", "Not in any anime in your list")
	mustNotContain(t, rec.Body.String(), "Gone Show")
}

func TestPersonWithNoCastRowsRendersFriendlyMessage(t *testing.T) {
	e := newEnv(t)
	a := e.addEntry(t, provider.AniList, "1", nil, store.Watching, 0, titleEn("Show")...)
	va := pperson("v1", "Lonely", "", "")
	e.withCast(t, a, provider.CastMember{Character: pperson("c1", "Hero", "", ""), Actor: &va})
	path := e.personPath(t, "v1")
	// re-sync the cast without the person: the people row stays, appearances go
	e.withCast(t, a, provider.CastMember{Character: pperson("c1", "Hero", "", "")})
	rec := e.get(path)
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	mustContain(t, rec.Body.String(), "Lonely", "Not in any anime in your list")
}

func TestPersonPageHasExternalLink(t *testing.T) {
	e := newEnv(t)
	a := e.addEntry(t, provider.AniList, "1", nil, store.Watching, 0, titleEn("Show")...)
	va := pperson("v1", "Voice One", "", "https://anilist.co/staff/1")
	e.withCast(t, a, provider.CastMember{Character: pperson("c1", "Hero", "", ""), Actor: &va})
	mustContain(t, e.get(e.personPath(t, "v1")).Body.String(), `href="https://anilist.co/staff/1"`, "View on AniList", `rel="noopener noreferrer"`)
}

func TestPersonPageNonHTTPSExternalURLNoLink(t *testing.T) {
	for _, u := range []string{"javascript:alert(1)", "http://anilist.co/staff/1", "//evil.example/x", "data:text/html,x"} {
		e := newEnv(t)
		a := e.addEntry(t, provider.AniList, "1", nil, store.Watching, 0, titleEn("Show")...)
		va := pperson("v1", "Voice One", "", u)
		e.withCast(t, a, provider.CastMember{Character: pperson("c1", "Hero", "", ""), Actor: &va})
		// the store cleans or drops such URLs before storing them; put the
		// hostile value in the row anyway, the page must still not link it
		if _, err := e.d.Exec(`UPDATE people SET external_url = ? WHERE source_id = 'v1'`, u); err != nil {
			t.Fatal(err)
		}
		body := e.get(e.personPath(t, "v1")).Body.String()
		mustNotContain(t, body, "View on AniList", "javascript:", "evil.example", "data:text")
		if strings.Contains(body, `href="http://`) {
			t.Fatalf("%s: http link rendered", u)
		}
	}
}

func TestUnknownPerson404(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/people/9999", "/people/abc", "/people/-1", "/people/1x", "/people/99999999999999999999", "/people/%00"} {
		rec := e.get(p)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: %d", p, rec.Code)
		}
	}
}

func TestPersonPagePreferredTitleLanguage(t *testing.T) {
	e := newEnv(t)
	a := e.addEntry(t, provider.AniList, "1", nil, store.Watching, 0,
		provider.Title{Lang: "en", Text: "English Name"}, provider.Title{Lang: "ja", Text: "Japanese Name"})
	va := pperson("v1", "Voice One", "", "")
	e.withCast(t, a, provider.CastMember{Character: pperson("c1", "Hero", "", ""), Actor: &va})
	path := e.personPath(t, "v1")
	for lang, want := range map[string]string{"en": "English Name", "ja": "Japanese Name"} {
		if err := e.set.Set(context.Background(), "title_lang", lang); err != nil {
			t.Fatal(err)
		}
		body := e.get(path).Body.String()
		mustContain(t, body, want)
		other := "English Name"
		if lang == "en" {
			other = "Japanese Name"
		}
		mustNotContain(t, body, other)
	}
}

func TestPersonPageImages(t *testing.T) {
	e := newEnv(t)
	a := e.addEntry(t, provider.AniList, "1", nil, store.Watching, 0, titleEn("Show")...)
	va := pperson("v1", "Remote", "https://img.example/v.jpg", "")
	e.withCast(t, a, provider.CastMember{Character: pperson("c1", "Hero", "", ""), Actor: &va})
	mustContain(t, e.get(e.personPath(t, "v1")).Body.String(), `src="https://img.example/v.jpg"`)

	pid := e.personPath(t, "v1")
	id := strings.TrimPrefix(pid, "/people/")
	var n int64
	for _, c := range id {
		n = n*10 + int64(c-'0')
	}
	if err := e.st.SetPersonImagePath(context.Background(), n, "abc def.jpg"); err != nil {
		t.Fatal(err)
	}
	body := e.get(pid).Body.String()
	mustContain(t, body, `src="/covers/abc%20def.jpg"`)
	mustNotContain(t, body, "img.example/v.jpg")

	// no image: placeholder, no img tag for the photo
	if err := e.st.SetPersonImagePath(context.Background(), n, ""); err != nil {
		t.Fatal(err)
	}
	mustNotContain(t, e.get(pid).Body.String(), "/covers/", "img.example/v.jpg")
}

func TestPersonPageAnimeCover(t *testing.T) {
	e := newEnv(t)
	a := provider.Anime{SearchResult: provider.SearchResult{Source: provider.AniList, ID: "1", Titles: titleEn("Show"), CoverURL: "https://img.example/c.jpg"}}
	id, err := e.st.SaveAnime(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	va := pperson("v1", "Voice One", "", "")
	e.withCast(t, id, provider.CastMember{Character: pperson("c1", "Hero", "", ""), Actor: &va})
	mustContain(t, e.get(e.personPath(t, "v1")).Body.String(), `src="https://img.example/c.jpg"`, `loading="lazy"`)
}

func TestPersonPageEscapesHostileText(t *testing.T) {
	e := newEnv(t)
	a := e.addEntry(t, provider.AniList, "1", nil, store.Watching, 0, titleEn(`<script>alert("t")</script>`)...)
	va := pperson("v1", `<img src=x onerror=alert(1)>`, "", "")
	e.withCast(t, a, provider.CastMember{Character: pperson("c1", `<b>Hero</b>`, "", ""), Actor: &va})
	body := e.get(e.personPath(t, "v1")).Body.String()
	mustNotContain(t, body, "<script>alert", "<img src=x", "<b>Hero</b>")
	mustContain(t, body, "&lt;script&gt;", "&lt;img src=x", "&lt;b&gt;Hero&lt;/b&gt;")
}

func TestPersonPageOrderIsDeterministicByTitle(t *testing.T) {
	e := newEnv(t)
	// insertion order differs from title order; mixed case sorts case-insensitively
	c := e.addEntry(t, provider.AniList, "1", nil, store.Watching, 0, titleEn("charlie")...)
	a := e.addEntry(t, provider.AniList, "2", nil, store.Watching, 0, titleEn("Alpha")...)
	b := e.addEntry(t, provider.AniList, "3", nil, store.Watching, 0, titleEn("bravo")...)
	va := pperson("v1", "Voice One", "", "")
	for _, id := range []int64{c, a, b} {
		e.withCast(t, id, provider.CastMember{Character: pperson("c1", "Hero", "", ""), Actor: &va})
	}
	path := e.personPath(t, "v1")
	for i := 0; i < 3; i++ {
		body := e.get(path).Body.String()
		ia, ib, ic := strings.Index(body, "Alpha"), strings.Index(body, "bravo"), strings.Index(body, "charlie")
		if !(ia > 0 && ia < ib && ib < ic) {
			t.Fatalf("order %d %d %d", ia, ib, ic)
		}
	}
}

func TestPersonPageIsFullPageForHTMX(t *testing.T) {
	e := newEnv(t)
	a := e.addEntry(t, provider.AniList, "1", nil, store.Watching, 0, titleEn("Show")...)
	va := pperson("v1", "Voice One", "", "")
	e.withCast(t, a, provider.CastMember{Character: pperson("c1", "Hero", "", ""), Actor: &va})
	rec := e.do("GET", e.personPath(t, "v1"), nil, true)
	mustContain(t, rec.Body.String(), "<html")
	if rec.Header().Get("Vary") != "HX-Request" {
		t.Fatal(rec.Header())
	}
}

// tvPeople saves a tv show with "Carl Character, voiced by Ann Actor" and
// returns the people paths of the actor and the character and the show's
// item id.
func tvPeople(t *testing.T, e *env) (actor, character string, itemID int64) {
	t.Helper()
	id := e.saveFixture(t, "tv", "show", tvShow("1", "Cast Show"))
	err := e.lib.SaveCredits(context.Background(), id, []plugin.Credit{{
		Role: "character", Person: plugin.PersonRef{ID: "c1", Name: "Carl Character"},
		With: &plugin.PersonRef{ID: "a1", Name: "Ann Actor", URL: "https://tv.example/p/a1"}, WithRole: "Actor",
	}})
	if err != nil {
		t.Fatal(err)
	}
	ps, err := e.lib.ItemPeople(context.Background(), id)
	if err != nil || len(ps) != 2 {
		t.Fatalf("people %+v %v", ps, err)
	}
	return "/people/" + idStr(ps[1].ID), "/people/" + idStr(ps[0].ID), id
}

func TestGenericPersonPageForFixturePlugin(t *testing.T) {
	e := newFixtureEnv(t)
	actor, character, showID := tvPeople(t, e.env)
	rec := e.get(actor)
	if rec.Code != 200 {
		t.Fatalf("actor: %d", rec.Code)
	}
	b := rec.Body.String()
	mustContain(t, b, "Ann Actor", "Cast Show", `href="/tv/tvdata/1"`, "as Carl Character", "View on Fake TV", `href="https://tv.example/p/a1"`)
	b = e.get(character).Body.String()
	mustContain(t, b, "Carl Character", "Cast Show")
	mustNotContain(t, b, "as Carl Character", "Ann Actor")
	// empty state names the plugin's kind
	if err := e.lib.SaveCredits(context.Background(), showID, nil); err != nil {
		t.Fatal(err)
	}
	mustContain(t, e.get(actor).Body.String(), "Not in any show in your list")
}

func TestPersonPageOfDisabledPlugin404(t *testing.T) {
	e := newFixtureEnv(t)
	actor, _, _ := tvPeople(t, e.env)
	a := e.addEntry(t, provider.AniList, "77", nil, store.Watching, 0, titleEn("Anime Show")...)
	va := pperson("v1", "Voice One", "", "")
	e.withCast(t, a, provider.CastMember{Character: pperson("c9", "Hero", "", ""), Actor: &va})
	animePerson := e.personPath(t, "v1")
	for _, p := range []string{actor, animePerson} {
		if c := e.get(p).Code; c != 200 {
			t.Fatalf("%s enabled: %d", p, c)
		}
	}
	// the person's own plugin decides, not the anime flag
	e.disable(t, "tv")
	rec := e.get(actor)
	if rec.Code != 404 {
		t.Fatalf("tv person with tv disabled: %d", rec.Code)
	}
	mustNotContain(t, rec.Body.String(), "Ann Actor")
	if c := e.get(animePerson).Code; c != 200 {
		t.Fatalf("anime person with tv disabled: %d", c)
	}
	e.disable(t, "anime")
	if c := e.get(animePerson).Code; c != 404 {
		t.Fatalf("anime person with anime disabled: %d", c)
	}
	if err := e.reg.SetEnabled(context.Background(), "tv", true); err != nil {
		t.Fatal(err)
	}
	if c := e.get(actor).Code; c != 200 {
		t.Fatalf("tv person re-enabled with anime disabled: %d", c)
	}
}

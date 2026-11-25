package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/manga"
)

func rating(r int) *int { return &r }

// TestCardShowsRating: a card shows the entry's score only when it is rated;
// an explicit 0 is a rating, an unrated entry (NULL) shows none. The home
// page's cards are the same cards.
func TestCardShowsRating(t *testing.T) {
	e := newFixtureEnv(t)
	e.addFixture(t, "tv", "show", tvWithEpisodes("1", "Rated Eight", 10), library.Entry{Status: library.Active, Rating: rating(8)})
	e.addFixture(t, "tv", "show", tvWithEpisodes("2", "Rated Zero", 10), library.Entry{Status: library.Active, Rating: rating(0)})
	e.addFixture(t, "tv", "show", tvWithEpisodes("3", "Unrated", 10), library.Entry{Status: library.Active})
	for _, page := range []string{"/tv?view=cards", "/"} {
		b := e.get(page).Body.String()
		mustContain(t, cardOf(t, b, "Rated Eight"), `data-rating`, `<span aria-hidden="true">★</span><span class="sr-only">Rating</span> 8</span>`)
		mustContain(t, cardOf(t, b, "Rated Zero"), `data-rating`, `<span class="sr-only">Rating</span> 0</span>`)
		mustNotContain(t, cardOf(t, b, "Unrated"), `data-rating`, "★")
	}
}

// TestCardShowsEveryActiveTrackInDeclaredOrder: the card's progress line
// lists every active track with its label in the order the manifest
// declares them (TV: Season, Episode, Position, as before cards showed
// ratings), joined by " · "; +1 still names the primary track.
func TestCardShowsEveryActiveTrackInDeclaredOrder(t *testing.T) {
	e := newFixtureEnv(t)
	e.addFixture(t, "tv", "show", tvWithEpisodes("1", "Dark", 10), library.Entry{Status: library.Active, Progress: map[string]float64{"season": 2, "episode": 3}},
		library.WithProgressKeys([]string{"season", "episode"}))
	card := cardOf(t, e.get("/tv?view=cards").Body.String(), "Dark")
	mustContain(t, card, `<span class="min-w-0"><span><span class="text-slate-400">Season</span> 2</span><span class="text-slate-500" aria-hidden="true"> · </span><span><span class="text-slate-400">Episode</span> 3 / 10</span></span>`,
		`aria-label="Add one episode to Dark"`)
	table := e.get("/tv?view=table").Body.String()
	if s, ep := strings.Index(table, ">Season</span>"), strings.Index(table, ">Episode</span>"); s < 0 || ep < 0 || s > ep {
		t.Errorf("table order changed: season at %d, episode at %d", s, ep)
	}
	// a secondary track without a stored value reads "–" (the primary keeps 0)
	e.addFixture(t, "tv", "show", tvWithEpisodes("3", "Fresh", 10), library.Entry{Status: library.Active},
		library.WithProgressKeys([]string{"season", "episode"}))
	fresh := cardOf(t, e.get("/tv?view=cards").Body.String(), "Fresh")
	mustContain(t, fresh, `<span class="text-slate-400">Season</span> –</span>`, `<span class="text-slate-400">Episode</span> 0 / 10</span>`)
	mustContain(t, e.get("/tv?view=table").Body.String(), `<span class="text-slate-400">Season</span> <span>–</span>`)
	// a single track keeps its label for screen readers only
	e.addFixture(t, "tv", "show", tvWithEpisodes("2", "Solo", 10), library.Entry{Status: library.Active, Progress: map[string]float64{"episode": 4}})
	solo := cardOf(t, e.get("/tv?view=cards").Body.String(), "Solo")
	mustContain(t, solo, `<span class="sr-only">Episode</span> 4 / 10</span>`)
	mustNotContain(t, solo, " · ")
}

// newMangaEnv is a web env with the real manga plugin enabled.
func newMangaEnv(t *testing.T) *env {
	t.Helper()
	e := newEnvWith(t, withPlugins(manga.New(manga.Options{Provider: sampleAnimeProvider()})))
	if err := e.reg.SetEnabled(context.Background(), manga.PluginID, true); err != nil {
		t.Fatal(err)
	}
	return e
}

func mangaData(id, title string) plugin.ItemData {
	return plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: manga.SourceID, ID: id, Kind: manga.KindManga, Titles: ttl(title),
		Fields: map[string]plugin.FieldValue{"type": {Text: "MANGA"}, "chapters": {Num: num(120)}, "volumes": {Num: num(12)}}}}
}

// TestMangaTracksChapterAndVolume: an existing manga that tracked chapters
// only is upgraded to Chapter + Volume (its stored volume shows again), the
// Edit dialog saves a typed volume, +1 adds a chapter only, and the card
// shows both tracks, chapters first.
func TestMangaTracksChapterAndVolume(t *testing.T) {
	e := newMangaEnv(t)
	ctx := context.Background()
	old, err := e.lib.AddToList(ctx, manga.PluginID, manga.KindManga, mangaData("1", "Old Manga"),
		library.Entry{Status: library.Active, Progress: map[string]float64{"chapter": 10, "volume": 2}}, library.WithProgressKeys([]string{"chapter", "volume"}))
	if err != nil {
		t.Fatal(err)
	}
	// before this change: chapters only, the volume retained inactive
	if err := e.lib.SetActiveProgress(ctx, old, []string{"chapter"}); err != nil {
		t.Fatal(err)
	}
	if n, err := e.lib.ApplyProgressUpgrades(ctx); err != nil || n != 1 {
		t.Fatalf("upgrade: %d, %v", n, err)
	}
	mustContain(t, cardOf(t, e.get("/manga?view=cards").Body.String(), "Old Manga"), `Chapter</span> 10 / 120</span><span class="text-slate-500" aria-hidden="true"> · </span><span><span class="text-slate-400">Volume</span> 2 / 12</span>`)

	// a new manga tracks both; the dialog's volume field is active and saved
	id := e.addFixture(t, manga.PluginID, manga.KindManga, mangaData("2", "New Manga"), library.Entry{Status: library.Active})
	edit := "/list/" + idStr(id) + "/edit"
	dlg := e.do("GET", edit, nil, true).Body.String()
	mustContain(t, dlg, `name="progress.chapter"`, `name="progress.volume"`)
	mustNotContain(t, dlg, "data-inactive")
	rec := e.do("POST", edit, url.Values{"status": {"active"}, "progress.chapter": {"45"}, "progress.volume": {"5"}}, true)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("edit: %d %s", rec.Code, rec.Body.String())
	}
	en, err := e.lib.Entry(ctx, id)
	if err != nil || en.Progress["chapter"] != 45 || en.Progress["volume"] != 5 {
		t.Fatalf("saved %v, %v; want chapter 45 and volume 5", en, err)
	}

	// +1 adds one chapter and leaves the volume alone
	rec = e.do("POST", "/list/"+idStr(id)+"/increment", url.Values{"return": {"/manga?view=cards"}, "view": {"cards"}}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("increment: %d %s", rec.Code, rec.Body.String())
	}
	mustContain(t, rec.Body.String(), `Chapter</span> 46 / 120</span><span class="text-slate-500" aria-hidden="true"> · </span><span><span class="text-slate-400">Volume</span> 5 / 12</span>`,
		`aria-label="Add one chapter to New Manga"`)
	if en, err := e.lib.Entry(ctx, id); err != nil || en.Progress["chapter"] != 46 || en.Progress["volume"] != 5 {
		t.Fatalf("after +1 %v, %v; want chapter 46 and volume 5", en, err)
	}
}

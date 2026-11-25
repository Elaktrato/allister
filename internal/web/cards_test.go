package web

import (
	"strings"
	"testing"

	"github.com/allister/allister/internal/library"
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
	mustContain(t, card, `<span class="min-w-0"><span><span class="text-slate-400">Season</span> 2</span><span class="text-slate-500"> · </span><span><span class="text-slate-400">Episode</span> 3 / 10</span></span>`,
		`aria-label="Add one episode to Dark"`)
	table := e.get("/tv?view=table").Body.String()
	if s, ep := strings.Index(table, ">Season</span>"), strings.Index(table, ">Episode</span>"); s < 0 || ep < 0 || s > ep {
		t.Errorf("table order changed: season at %d, episode at %d", s, ep)
	}
	// a single track keeps its label for screen readers only
	e.addFixture(t, "tv", "show", tvWithEpisodes("2", "Solo", 10), library.Entry{Status: library.Active, Progress: map[string]float64{"episode": 4}})
	solo := cardOf(t, e.get("/tv?view=cards").Body.String(), "Solo")
	mustContain(t, solo, `<span class="sr-only">Episode</span> 4 / 10</span>`)
	mustNotContain(t, solo, " · ")
}

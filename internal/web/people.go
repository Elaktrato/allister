package web

import (
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/allister/allister/internal/covers"
	"github.com/allister/allister/internal/titles"
)

// appearanceView is one anime row on a person page. VoicedCharacter is set
// only when the person is the voice actor of that character.
type appearanceView struct {
	Title, Href, Cover string
	VoicedCharacter    string
}

type personPageView struct {
	Name, Photo, ExtURL string
	Appearances         []appearanceView
}

// getPerson renders /people/{id}: the person's photo, name, AniList link and
// the anime of the user's list they appear in. Rows are ordered by resolved
// title (case-insensitive), then anime id, then character name, so the page
// is deterministic.
func (s *server) getPerson(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("id")
	if !isASCIIDigits(raw, maxIDDigits) {
		s.personNotFound(w)
		return
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		s.personNotFound(w)
		return
	}
	ctx := r.Context()
	p, err := s.deps.Store.Person(ctx, id)
	if err != nil {
		log.Printf("person %d: %v", id, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if p == nil {
		s.personNotFound(w)
		return
	}
	apps, err := s.deps.Store.PersonAnime(ctx, id)
	if err != nil {
		log.Printf("person %d anime: %v", id, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	lang := s.deps.Settings.TitleLang(ctx)
	v := personPageView{Name: p.Name, Photo: covers.Src(p.ImagePath), ExtURL: httpsURL(p.ExternalURL)}
	sortKeys := make([]string, 0, len(apps))
	for _, a := range apps {
		av := appearanceView{
			Title: titles.Resolve(a.Titles, lang), Href: animeURL(string(a.Source), a.SourceID),
			Cover: covers.Src(a.CoverPath),
		}
		if av.Title == "" {
			av.Title = "(untitled)"
		}
		if a.AsActor {
			av.VoicedCharacter = a.Character
		}
		v.Appearances = append(v.Appearances, av)
		sortKeys = append(sortKeys, strings.ToLower(av.Title))
	}
	idx := make([]int, len(apps))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool {
		a, b := idx[i], idx[j]
		if sortKeys[a] != sortKeys[b] {
			return sortKeys[a] < sortKeys[b]
		}
		return apps[a].AnimeID < apps[b].AnimeID
	})
	sorted := make([]appearanceView, len(idx))
	for i, k := range idx {
		sorted[i] = v.Appearances[k]
	}
	v.Appearances = sorted
	s.render(w, r, "person", v)
}

func (s *server) personNotFound(w http.ResponseWriter) {
	s.errorPage(w, http.StatusNotFound, "Not found", "There is no such person.", "")
}

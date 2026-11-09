package web

import (
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/allister/allister/internal/covers"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// appearanceView is one item row on a person page. As is set only when the
// person is the counterpart of the credit (the voice actor of a character):
// it names the credited person ("as <character>").
type appearanceView struct {
	Title, Href, Cover string
	As                 string
}

type personPageView struct {
	Name, Photo, ExtURL, LinkLabel string
	Appearances                    []appearanceView
	// Empty is the friendly message without appearances.
	Empty string
}

// getPerson renders /people/{id}: the person's photo, name, external link
// and the saved items of THEIR plugin they appear in. The person's plugin
// guards the page: unknown or disabled, it answers 404 like an unknown
// person. Rows are ordered by resolved title (case-insensitive), then item
// id, then credit position, so the page is deterministic.
func (s *server) getPerson(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("id")
	if !isASCIIDigits(raw, maxIDDigits) {
		s.personNotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		s.personNotFound(w, r)
		return
	}
	ctx := r.Context()
	p, err := s.deps.Library.Person(ctx, id)
	if err != nil {
		log.Printf("person %d: %v", id, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if p == nil {
		s.personNotFound(w, r)
		return
	}
	m, ok := s.deps.Registry.Manifest(p.PluginID)
	if !ok || !s.deps.Registry.IsEnabled(p.PluginID) {
		s.personNotFound(w, r)
		return
	}
	apps, err := s.deps.Library.Appearances(ctx, id)
	if err != nil {
		log.Printf("person %d appearances: %v", id, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	lang := s.deps.Settings.TitleLang(ctx)
	v := personPageView{Name: p.Name, Photo: covers.Src(p.ImagePath), ExtURL: httpsURL(p.ExternalURL), Empty: emptyPersonMessage(m)}
	if v.ExtURL != "" {
		v.LinkLabel = "External page"
		if si, ok := sourceInfo(m, p.Source); ok {
			v.LinkLabel = orDefault(si.LinkLabel, "View on "+si.Name)
		}
	}
	type key struct {
		item    int64
		mirror  bool
		role    string
		against int64
	}
	type row struct {
		view   appearanceView
		sortBy string
		item   int64
	}
	seen := map[key]bool{}
	var rows []row
	for _, a := range apps {
		if a.PluginID != p.PluginID || !hasKind(m, a.Kind) {
			continue
		}
		k := key{item: a.ItemID, mirror: a.AsCounterpart, role: a.Role}
		if a.AsCounterpart && a.With != nil {
			k.against = a.With.ID
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		av := appearanceView{Title: library.ResolveTitle(a.Titles, lang), Href: itemURL(m, a.Source, a.SourceID, a.Kind), Cover: covers.Src(a.CoverPath)}
		if av.Title == "" {
			av.Title = "(untitled)"
		}
		if a.AsCounterpart && a.With != nil {
			av.As = a.With.Name
		}
		rows = append(rows, row{view: av, sortBy: strings.ToLower(av.Title), item: a.ItemID})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].sortBy != rows[j].sortBy {
			return rows[i].sortBy < rows[j].sortBy
		}
		return rows[i].item < rows[j].item
	})
	for _, r := range rows {
		v.Appearances = append(v.Appearances, r.view)
	}
	s.render(w, r, "person", v)
}

// emptyPersonMessage names the plugin's kind: "Not in any anime in your list."
func emptyPersonMessage(m plugin.Manifest) string {
	noun := "item"
	if len(m.Kinds) == 1 && m.Kinds[0].Label != "" {
		noun = strings.ToLower(m.Kinds[0].Label)
	}
	return "Not in any " + noun + " in your list."
}

func (s *server) personNotFound(w http.ResponseWriter, r *http.Request) {
	s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such person.", "")
}

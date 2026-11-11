package tv

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/tmdb"
)

type tmdbShow struct {
	ID               int                     `json:"id"`
	Name             string                  `json:"name"`
	OriginalName     string                  `json:"original_name"`
	Overview         string                  `json:"overview"`
	Status           string                  `json:"status"`
	Type             string                  `json:"type"`
	FirstAirDate     string                  `json:"first_air_date"`
	LastAirDate      string                  `json:"last_air_date"`
	Homepage         string                  `json:"homepage"`
	NumberOfSeasons  int                     `json:"number_of_seasons"`
	NumberOfEpisodes int                     `json:"number_of_episodes"`
	EpisodeRunTime   []int                   `json:"episode_run_time"`
	Genres           []struct{ Name string } `json:"genres"`
	Networks         []struct{ Name string } `json:"networks"`
	OriginCountry    []string                `json:"origin_country"`
	PosterPath       string                  `json:"poster_path"`
	Credits          struct {
		Cast []tmdbCast `json:"cast"`
	} `json:"credits"`
	Recommendations struct {
		Results []tmdbShow `json:"results"`
	} `json:"recommendations"`
	Keywords struct {
		Results []struct{ Name string } `json:"results"`
	} `json:"keywords"`
	ExternalIDs struct {
		TVDBID *int   `json:"tvdb_id"`
		IMDBID string `json:"imdb_id"`
	} `json:"external_ids"`
}

type tmdbCast struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Character string `json:"character"`
	CreditID  string `json:"credit_id"`
	Profile   string `json:"profile_path"`
	Order     int    `json:"order"`
}

// tmdbSource is the TV plugin's TMDB source. The HTTP plumbing lives in the
// shared tmdb package; the structs and mapping stay here.
type tmdbSource struct{ c *tmdb.Client }

func (s *tmdbSource) search(ctx context.Context, q string) ([]plugin.ItemSummary, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return []plugin.ItemSummary{}, nil
	}
	var out struct {
		Results []tmdbShow `json:"results"`
	}
	if err := s.c.Get(ctx, "/search/tv", url.Values{"query": {q}}, &out); err != nil {
		return nil, err
	}
	res := make([]plugin.ItemSummary, 0, len(out.Results))
	for _, show := range out.Results {
		fields := map[string]plugin.FieldValue{}
		if show.Type != "" {
			fields["type"] = plugin.FieldValue{Text: showType(show.Type)}
		}
		if y := yearOf(show.FirstAirDate); y > 0 {
			n := float64(y)
			fields["year"] = plugin.FieldValue{Num: &n}
		}
		if show.Status != "" {
			fields["air_status"] = plugin.FieldValue{Text: tmdbAirStatus(show.Status)}
		}
		res = append(res, tmdbSummary(show, fields))
	}
	return res, nil
}

func (s *tmdbSource) details(ctx context.Context, id string) (tmdbShow, error) {
	var show tmdbShow
	err := s.c.Get(ctx, "/tv/"+url.PathEscape(id), url.Values{"append_to_response": {"credits,recommendations,external_ids,keywords"}}, &show)
	return show, err
}

func (s *tmdbSource) data(ctx context.Context, id string) (plugin.ItemData, error) {
	show, err := s.details(ctx, id)
	if err != nil {
		return plugin.ItemData{}, err
	}
	return tmdbData(show), nil
}

func (s *tmdbSource) people(ctx context.Context, id string) ([]plugin.Credit, error) {
	show, err := s.details(ctx, id)
	if err != nil {
		return nil, err
	}
	return tmdbCredits(show.Credits.Cast), nil
}

func (s *tmdbSource) recommendations(ctx context.Context, id string) ([]plugin.ItemSummary, error) {
	show, err := s.details(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]plugin.ItemSummary, 0, len(show.Recommendations.Results))
	for _, rec := range show.Recommendations.Results {
		out = append(out, tmdbSummary(rec, nil))
	}
	return showRecommendations(out), nil
}

func (s *tmdbSource) lookup(ctx context.Context, idType, value string) (plugin.ItemData, error) {
	switch idType {
	case "tmdb":
		return s.data(ctx, value)
	case "imdb", "tvdb":
		source := "imdb_id"
		if idType == "tvdb" {
			source = "tvdb_id"
		}
		res, err := s.c.Find(ctx, value, source)
		if err != nil {
			return plugin.ItemData{}, err
		}
		if len(res.TVResults) == 0 {
			return plugin.ItemData{}, plugin.ErrNotFound
		}
		return s.data(ctx, fmt.Sprint(res.TVResults[0].ID))
	}
	return plugin.ItemData{}, fmt.Errorf("tmdb: cannot look up %q ids", idType)
}

func tmdbSummary(s tmdbShow, fields map[string]plugin.FieldValue) plugin.ItemSummary {
	if fields == nil {
		fields = map[string]plugin.FieldValue{}
	}
	return plugin.ItemSummary{Source: SourceTMDB, ID: fmt.Sprint(s.ID), Kind: KindShow,
		Titles:   []plugin.Title{{Lang: "en", Text: plugin.SingleLine(s.Name, maxTitleRunes)}},
		Fields:   fields,
		CoverURL: tmdbCover(s.PosterPath),
		URL:      tmdbURL(s.ID),
	}
}

func tmdbData(s tmdbShow) plugin.ItemData {
	fields := map[string]plugin.FieldValue{}
	if ty := showType(s.Type); ty != "" {
		fields["type"] = plugin.FieldValue{Text: ty}
	}
	if y := yearOf(s.FirstAirDate); y > 0 {
		n := float64(y)
		fields["year"] = plugin.FieldValue{Num: &n}
	}
	if s.NumberOfSeasons > 0 {
		n := float64(s.NumberOfSeasons)
		fields["seasons"] = plugin.FieldValue{Num: &n}
	}
	if s.NumberOfEpisodes > 0 {
		n := float64(s.NumberOfEpisodes)
		fields["episodes"] = plugin.FieldValue{Num: &n}
	}
	if len(s.EpisodeRunTime) > 0 && s.EpisodeRunTime[0] > 0 {
		n := float64(s.EpisodeRunTime[0])
		fields["runtime"] = plugin.FieldValue{Num: &n}
	}
	if len(s.Networks) > 0 {
		fields["network"] = plugin.FieldValue{Text: s.Networks[0].Name}
	}
	if len(s.OriginCountry) > 0 {
		fields["country"] = plugin.FieldValue{Text: s.OriginCountry[0]}
	}
	if s.Status != "" {
		fields["air_status"] = plugin.FieldValue{Text: tmdbAirStatus(s.Status)}
	}
	if s.Overview != "" {
		fields["synopsis"] = plugin.FieldValue{Text: s.Overview}
	}
	if d := dayOf(s.FirstAirDate); d != nil {
		fields["started_on"] = plugin.FieldValue{Date: d}
	}
	if d := dayOf(s.LastAirDate); d != nil {
		fields["ended_on"] = plugin.FieldValue{Date: d}
	}
	var genres []string
	for _, g := range s.Genres {
		genres = append(genres, g.Name)
	}
	var tags []plugin.Tag
	for _, k := range s.Keywords.Results {
		tags = append(tags, plugin.Tag{Name: k.Name})
	}
	ext := map[string]string{"tmdb": fmt.Sprint(s.ID)}
	if s.ExternalIDs.TVDBID != nil {
		ext["tvdb"] = fmt.Sprint(*s.ExternalIDs.TVDBID)
	}
	if s.ExternalIDs.IMDBID != "" {
		ext["imdb"] = s.ExternalIDs.IMDBID
	}
	return showData(showRecord{
		Source: SourceTMDB, ID: fmt.Sprint(s.ID),
		Titles: []plugin.Title{{Lang: "en", Text: s.Name}, {Lang: "original", Text: s.OriginalName}},
		Fields: fields, CoverURL: tmdbCover(s.PosterPath), ExternalURL: tmdbURL(s.ID),
		ExternalIDs: ext, Genres: genres, Tags: tags,
	})
}

func tmdbCredits(cs []tmdbCast) []plugin.Credit {
	out := make([]plugin.Credit, 0, len(cs))
	for _, c := range cs {
		name, cname := plugin.SingleLine(c.Name, maxNameRunes), plugin.SingleLine(c.Character, maxNameRunes)
		if c.ID <= 0 || name == "" || cname == "" {
			continue
		}
		id := c.CreditID
		if id == "" {
			id = fmt.Sprintf("%d-%s", c.ID, cname)
		}
		detail := "SUPPORTING"
		if c.Order < 10 {
			detail = "MAIN"
		}
		out = append(out, plugin.Credit{Role: "actor", Detail: detail,
			Person: plugin.PersonRef{ID: "character-" + id, Name: cname},
			With: &plugin.PersonRef{ID: fmt.Sprintf("person-%d", c.ID), Name: name,
				ImageURL: tmdbProfile(c.Profile)},
			WithRole: "actor",
		})
	}
	return out
}

func tmdbAirStatus(raw string) string {
	switch strings.ToLower(raw) {
	case "returning series":
		return "running"
	case "ended", "canceled":
		return "ended"
	case "in production", "planned", "pilot":
		return "upcoming"
	}
	return "other"
}

func tmdbCover(path string) string { return tmdb.Image(path, "") }

func tmdbProfile(path string) string { return tmdb.Image(path, "w185") }

func tmdbURL(id int) string { return fmt.Sprintf("https://www.themoviedb.org/tv/%d", id) }

func (s *tmdbSource) Search(ctx context.Context, kind, q string) ([]plugin.ItemSummary, error) {
	if kind != KindShow {
		return nil, plugin.ErrNotFound
	}
	return s.search(ctx, q)
}

func (s *tmdbSource) Details(ctx context.Context, kind, id string) (plugin.ItemData, error) {
	if kind != KindShow {
		return plugin.ItemData{}, plugin.ErrNotFound
	}
	return s.data(ctx, id)
}

func (s *tmdbSource) People(ctx context.Context, kind, id string) ([]plugin.Credit, error) {
	if kind != KindShow {
		return nil, plugin.ErrNotFound
	}
	cs, err := s.people(ctx, id)
	if err != nil {
		return nil, err
	}
	return showCredits(cs), nil
}

func (s *tmdbSource) Recommendations(ctx context.Context, kind, id string) ([]plugin.ItemSummary, error) {
	if kind != KindShow {
		return nil, plugin.ErrNotFound
	}
	return s.recommendations(ctx, id)
}

func (s *tmdbSource) Lookup(ctx context.Context, kind, idType, value string) (plugin.ItemData, error) {
	if kind != KindShow {
		return plugin.ItemData{}, plugin.ErrNotFound
	}
	return s.lookup(ctx, idType, value)
}

package movie

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/tmdb"
)

type tmdbMovie struct {
	ID                  int                     `json:"id"`
	Title               string                  `json:"title"`
	OriginalTitle       string                  `json:"original_title"`
	Overview            string                  `json:"overview"`
	Status              string                  `json:"status"`
	ReleaseDate         string                  `json:"release_date"`
	Runtime             int                     `json:"runtime"`
	OriginalLanguage    string                  `json:"original_language"`
	PosterPath          string                  `json:"poster_path"`
	Genres              []struct{ Name string } `json:"genres"`
	ProductionCompanies []struct{ Name string } `json:"production_companies"`
	ProductionCountries []struct{ Name string } `json:"production_countries"`
	SpokenLanguages     []tmdbSpokenLanguage    `json:"spoken_languages"`
	Collection          struct {
		Name string `json:"name"`
	} `json:"belongs_to_collection"`
	Credits struct {
		Cast []tmdbCast `json:"cast"`
		Crew []tmdbCrew `json:"crew"`
	} `json:"credits"`
	Keywords struct {
		// The movie append response nests the keywords under "keywords"; TV
		// uses "results". Accept both.
		Keywords []struct{ Name string } `json:"keywords"`
		Results  []struct{ Name string } `json:"results"`
	} `json:"keywords"`
	Recommendations struct {
		Results []tmdbMovie `json:"results"`
	} `json:"recommendations"`
	ExternalIDs struct {
		IMDBID string `json:"imdb_id"`
	} `json:"external_ids"`
}

type tmdbSpokenLanguage struct {
	EnglishName string `json:"english_name"`
	Name        string `json:"name"`
	Code        string `json:"iso_639_1"`
}

type tmdbCast struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Character string `json:"character"`
	CreditID  string `json:"credit_id"`
	Profile   string `json:"profile_path"`
	Order     int    `json:"order"`
}

type tmdbCrew struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Job     string `json:"job"`
	Profile string `json:"profile_path"`
}

// tmdbSource is the movie plugin's TMDB source. The HTTP plumbing lives in the
// shared tmdb package; the structs and mapping stay here.
type tmdbSource struct{ c *tmdb.Client }

func (s *tmdbSource) search(ctx context.Context, q string) ([]plugin.ItemSummary, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return []plugin.ItemSummary{}, nil
	}
	var out struct {
		Results []tmdbMovie `json:"results"`
	}
	values := url.Values{"query": {q}, "include_adult": {"false"}}
	if err := s.c.Get(ctx, "/search/movie", values, &out); err != nil {
		return nil, err
	}
	res := make([]plugin.ItemSummary, 0, len(out.Results))
	for _, m := range out.Results {
		res = append(res, tmdbMovieSummary(m))
	}
	return res, nil
}

func (s *tmdbSource) details(ctx context.Context, id string) (tmdbMovie, error) {
	var m tmdbMovie
	err := s.c.Get(ctx, "/movie/"+url.PathEscape(id), url.Values{"append_to_response": {"credits,recommendations,external_ids,keywords"}}, &m)
	return m, err
}

func (s *tmdbSource) data(ctx context.Context, id string) (plugin.ItemData, error) {
	m, err := s.details(ctx, id)
	if err != nil {
		return plugin.ItemData{}, err
	}
	return tmdbMovieData(m), nil
}

func (s *tmdbSource) people(ctx context.Context, id string) ([]plugin.Credit, error) {
	m, err := s.details(ctx, id)
	if err != nil {
		return nil, err
	}
	return tmdbMovieCredits(m), nil
}

func (s *tmdbSource) recommendations(ctx context.Context, id string) ([]plugin.ItemSummary, error) {
	m, err := s.details(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]plugin.ItemSummary, 0, len(m.Recommendations.Results))
	for _, rec := range m.Recommendations.Results {
		out = append(out, tmdbMovieSummary(rec))
	}
	return movieRecommendations(out), nil
}

func (s *tmdbSource) lookup(ctx context.Context, idType, value string) (plugin.ItemData, error) {
	switch idType {
	case "tmdb":
		return s.data(ctx, value)
	case "imdb":
		res, err := s.c.Find(ctx, value, "imdb_id")
		if err != nil {
			return plugin.ItemData{}, err
		}
		if len(res.MovieResults) == 0 {
			return plugin.ItemData{}, plugin.ErrNotFound
		}
		return s.data(ctx, fmt.Sprint(res.MovieResults[0].ID))
	}
	return plugin.ItemData{}, plugin.ErrNotFound
}

func tmdbMovieSummary(m tmdbMovie) plugin.ItemSummary {
	fields := map[string]plugin.FieldValue{}
	if y := tmdbYear(m.ReleaseDate); y > 0 {
		n := float64(y)
		fields["year"] = plugin.FieldValue{Num: &n}
	}
	return plugin.ItemSummary{Source: SourceTMDB, ID: fmt.Sprint(m.ID), Kind: KindMovie,
		Titles:   tmdbMovieTitles(m),
		Fields:   fields,
		CoverURL: tmdbCover(m.PosterPath),
		URL:      tmdbURL(m.ID),
	}
}

func tmdbMovieTitles(m tmdbMovie) []plugin.Title {
	var titles []plugin.Title
	if title := plugin.SingleLine(m.Title, maxTitleRunes); title != "" {
		titles = append(titles, plugin.Title{Lang: "en", Text: title})
	}
	if original := plugin.SingleLine(m.OriginalTitle, maxTitleRunes); original != "" {
		titles = append(titles, plugin.Title{Lang: "original", Text: original})
	}
	return titles
}

func tmdbMovieData(m tmdbMovie) plugin.ItemData {
	fields := map[string]plugin.FieldValue{}
	if y := tmdbYear(m.ReleaseDate); y > 0 {
		n := float64(y)
		fields["year"] = plugin.FieldValue{Num: &n}
	}
	if m.Runtime > 0 {
		n := float64(m.Runtime)
		fields["runtime"] = plugin.FieldValue{Num: &n}
	}
	if director := tmdbDirector(m.Credits.Crew); director != "" {
		fields["director"] = plugin.FieldValue{Text: director}
	}
	if len(m.ProductionCompanies) > 0 {
		fields["studio"] = plugin.FieldValue{Text: plugin.SingleLine(m.ProductionCompanies[0].Name, maxTextRunes)}
	}
	if len(m.ProductionCountries) > 0 {
		fields["country"] = plugin.FieldValue{Text: plugin.SingleLine(m.ProductionCountries[0].Name, maxTextRunes)}
	}
	if lang := tmdbLanguage(m); lang != "" {
		fields["language"] = plugin.FieldValue{Text: plugin.SingleLine(lang, maxTextRunes)}
	}
	if collection := plugin.SingleLine(m.Collection.Name, maxTextRunes); collection != "" {
		fields["collection"] = plugin.FieldValue{Text: collection}
	}
	if m.Status != "" {
		fields["release_status"] = plugin.FieldValue{Text: tmdbReleaseStatus(m.Status)}
	}
	if m.Overview != "" {
		fields["synopsis"] = plugin.FieldValue{Text: m.Overview}
	}
	if d := tmdbDay(m.ReleaseDate); d != nil {
		fields["released_on"] = plugin.FieldValue{Date: d}
	}
	var genres []string
	for _, g := range m.Genres {
		genres = append(genres, g.Name)
	}
	var tags []plugin.Tag
	for _, k := range m.Keywords.Keywords {
		tags = append(tags, plugin.Tag{Name: k.Name})
	}
	for _, k := range m.Keywords.Results {
		tags = append(tags, plugin.Tag{Name: k.Name})
	}
	ext := map[string]string{"tmdb": fmt.Sprint(m.ID)}
	if m.ExternalIDs.IMDBID != "" {
		ext["imdb"] = m.ExternalIDs.IMDBID
	}
	return movieData(movieRecord{
		Source: SourceTMDB, ID: fmt.Sprint(m.ID),
		Titles: tmdbMovieTitles(m), Fields: fields,
		CoverURL: tmdbCover(m.PosterPath), ExternalURL: tmdbURL(m.ID),
		ExternalIDs: ext, Genres: genres, Tags: tags,
	})
}

// tmdbMovieCredits maps the details call's cast and crew to the contract's
// credits: characters are the Person (with the actor as With), crew members
// are the Person. Crew is deduplicated per person and role.
func tmdbMovieCredits(m tmdbMovie) []plugin.Credit {
	out := make([]plugin.Credit, 0, len(m.Credits.Cast)+len(m.Credits.Crew))
	for _, c := range m.Credits.Cast {
		name := plugin.SingleLine(c.Name, maxNameRunes)
		character := plugin.SingleLine(c.Character, maxNameRunes)
		if c.ID <= 0 || name == "" || character == "" {
			continue
		}
		id := plugin.SingleLine(c.CreditID, maxIDRunes)
		if id == "" {
			id = fmt.Sprintf("%d-%s", c.ID, character)
		}
		detail := "SUPPORTING"
		if c.Order < 10 {
			detail = "MAIN"
		}
		out = append(out, plugin.Credit{Role: "actor", Detail: detail,
			Person: plugin.PersonRef{ID: "character-" + id, Name: character},
			With: &plugin.PersonRef{ID: fmt.Sprintf("person-%d", c.ID), Name: name,
				ImageURL: tmdbProfile(c.Profile)},
			WithRole: "actor",
		})
	}
	seen := map[string]bool{}
	for _, c := range m.Credits.Crew {
		role := tmdbCrewRole(c.Job)
		name := plugin.SingleLine(c.Name, maxNameRunes)
		if role == "" || c.ID <= 0 || name == "" {
			continue
		}
		key := fmt.Sprintf("%d-%s", c.ID, role)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, plugin.Credit{Role: role, Detail: plugin.SingleLine(c.Job, 100),
			Person: plugin.PersonRef{ID: fmt.Sprintf("person-%d", c.ID), Name: name,
				ImageURL: tmdbProfile(c.Profile)},
		})
	}
	return out
}

func tmdbDirector(crew []tmdbCrew) string {
	for _, c := range crew {
		if tmdbCrewRole(c.Job) != "director" {
			continue
		}
		if name := plugin.SingleLine(c.Name, maxNameRunes); name != "" {
			return name
		}
	}
	return ""
}

func tmdbCrewRole(job string) string {
	switch strings.ToLower(strings.TrimSpace(job)) {
	case "director":
		return "director"
	case "screenplay", "writer", "story":
		return "writer"
	}
	return ""
}

func tmdbReleaseStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "released":
		return "released"
	case "planned", "in production", "post production", "rumored":
		return "upcoming"
	}
	return "other"
}

// tmdbLanguage returns the English name of the spoken language that matches the
// original language, else the first English name, else the code.
func tmdbLanguage(m tmdbMovie) string {
	for _, l := range m.SpokenLanguages {
		if l.Code != "" && strings.EqualFold(l.Code, m.OriginalLanguage) {
			if name := tmdbLanguageName(l); name != "" {
				return name
			}
		}
	}
	for _, l := range m.SpokenLanguages {
		if name := tmdbLanguageName(l); name != "" {
			return name
		}
	}
	return strings.TrimSpace(m.OriginalLanguage)
}

func tmdbLanguageName(l tmdbSpokenLanguage) string {
	if l.EnglishName != "" {
		return l.EnglishName
	}
	return l.Name
}

func tmdbYear(date string) int {
	if d := tmdbDay(date); d != nil {
		return d.Year()
	}
	return 0
}

func tmdbDay(date string) *time.Time {
	date = strings.TrimSpace(date)
	if date == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return nil
	}
	d := plugin.DateOnly(t)
	return &d
}

func tmdbCover(path string) string { return tmdb.Image(path, "") }

func tmdbProfile(path string) string { return tmdb.Image(path, "w185") }

func tmdbURL(id int) string { return fmt.Sprintf("https://www.themoviedb.org/movie/%d", id) }

func (s *tmdbSource) Search(ctx context.Context, kind, q string) ([]plugin.ItemSummary, error) {
	if kind != KindMovie {
		return nil, plugin.ErrNotFound
	}
	return s.search(ctx, q)
}

func (s *tmdbSource) Details(ctx context.Context, kind, id string) (plugin.ItemData, error) {
	if kind != KindMovie {
		return plugin.ItemData{}, plugin.ErrNotFound
	}
	return s.data(ctx, id)
}

func (s *tmdbSource) People(ctx context.Context, kind, id string) ([]plugin.Credit, error) {
	if kind != KindMovie {
		return nil, plugin.ErrNotFound
	}
	cs, err := s.people(ctx, id)
	if err != nil {
		return nil, err
	}
	return movieCredits(cs), nil
}

func (s *tmdbSource) Recommendations(ctx context.Context, kind, id string) ([]plugin.ItemSummary, error) {
	if kind != KindMovie {
		return nil, plugin.ErrNotFound
	}
	return s.recommendations(ctx, id)
}

func (s *tmdbSource) Lookup(ctx context.Context, kind, idType, value string) (plugin.ItemData, error) {
	if kind != KindMovie {
		return plugin.ItemData{}, plugin.ErrNotFound
	}
	return s.lookup(ctx, idType, value)
}

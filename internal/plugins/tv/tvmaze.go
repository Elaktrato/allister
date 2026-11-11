package tv

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anilist"
)

const (
	tvmazeDefaultURL = "https://api.tvmaze.com"
	tvmazeUserAgent  = "Allister/1.0 (self-hosted TV tracker)"
	tvmazeMaxBody    = 8 << 20
)

type tvShow struct {
	ID        int      `json:"id"`
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Language  string   `json:"language"`
	Status    string   `json:"status"`
	Runtime   *int     `json:"runtime"`
	Premiered string   `json:"premiered"`
	Ended     string   `json:"ended"`
	Genres    []string `json:"genres"`
	Summary   string   `json:"summary"`
	Image     *struct {
		Medium, Original string
	} `json:"image"`
	Network *struct {
		Name    string                 `json:"name"`
		Country *struct{ Name string } `json:"country"`
	} `json:"network"`
	WebChannel *struct{ Name string } `json:"webChannel"`
	Externals  struct {
		TVDB *int `json:"thetvdb"`
		IMDB *int `json:"imdb"`
	} `json:"externals"`
	Embedded struct {
		Cast []tvCast `json:"cast"`
	} `json:"_embedded"`
}

type tvCast struct {
	Person struct {
		ID    int                                `json:"id"`
		Name  string                             `json:"name"`
		URL   string                             `json:"url"`
		Image *struct{ Medium, Original string } `json:"image"`
	} `json:"person"`
	Character struct {
		ID    int                                `json:"id"`
		Name  string                             `json:"name"`
		URL   string                             `json:"url"`
		Image *struct{ Medium, Original string } `json:"image"`
	} `json:"character"`
}

type tvEpisode struct {
	Season   int    `json:"season"`
	Number   int    `json:"number"`
	Airstamp string `json:"airstamp"`
	Airdate  string `json:"airdate"`
}

type tvmazeClient struct {
	base string
	hc   *http.Client
}

func newTVmazeClient(base string, hc *http.Client) *tvmazeClient {
	if base == "" {
		base = tvmazeDefaultURL
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	return &tvmazeClient{base: strings.TrimRight(base, "/"), hc: hc}
}

func (c *tvmazeClient) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return fmt.Errorf("tvmaze: %w", plugin.ErrUnavailable)
	}
	req.Header.Set("User-Agent", tvmazeUserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("tvmaze: %w", plugin.ErrUnavailable)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusNotFound:
		return plugin.ErrNotFound
	case resp.StatusCode == http.StatusTooManyRequests:
		return plugin.ErrRateLimited
	case resp.StatusCode >= 500, resp.StatusCode == http.StatusRequestTimeout:
		return plugin.ErrUnavailable
	default:
		return fmt.Errorf("%w: tvmaze status %d", plugin.ErrBadResponse, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, tvmazeMaxBody+1))
	if err != nil {
		return fmt.Errorf("tvmaze: %w", plugin.ErrUnavailable)
	}
	if len(body) > tvmazeMaxBody {
		return fmt.Errorf("%w: tvmaze response too large", plugin.ErrBadResponse)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%w: tvmaze: %v", plugin.ErrBadResponse, err)
	}
	return nil
}

func (c *tvmazeClient) search(ctx context.Context, q string) ([]plugin.ItemSummary, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return []plugin.ItemSummary{}, nil
	}
	var out []struct {
		Show tvShow `json:"show"`
	}
	if err := c.get(ctx, "/search/shows?q="+url.QueryEscape(q), &out); err != nil {
		return nil, err
	}
	res := make([]plugin.ItemSummary, 0, len(out))
	for _, x := range out {
		s := x.Show
		fields := map[string]plugin.FieldValue{}
		if ty := showType(s.Type); ty != "" {
			fields["type"] = plugin.FieldValue{Text: ty}
		}
		if y := yearOf(s.Premiered); y > 0 {
			n := float64(y)
			fields["year"] = plugin.FieldValue{Num: &n}
		}
		if s.Status != "" {
			fields["air_status"] = plugin.FieldValue{Text: airStatus(s.Status)}
		}
		res = append(res, plugin.ItemSummary{Source: SourceTVmaze, ID: fmt.Sprint(s.ID), Kind: KindShow,
			Titles: []plugin.Title{{Lang: "en", Text: plugin.SingleLine(s.Name, maxTitleRunes)}},
			Fields: fields, CoverURL: showImage(s.Image), URL: showURL(s.ID)})
	}
	return res, nil
}

func (c *tvmazeClient) show(ctx context.Context, id string, cast bool) (tvShow, error) {
	path := "/shows/" + url.PathEscape(id)
	if cast {
		path += "?embed[]=cast"
	}
	var s tvShow
	if err := c.get(ctx, path, &s); err != nil {
		return tvShow{}, err
	}
	return s, nil
}

func (c *tvmazeClient) episodes(ctx context.Context, id string) ([]tvEpisode, error) {
	var out []tvEpisode
	if err := c.get(ctx, "/shows/"+url.PathEscape(id)+"/episodes", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *tvmazeClient) details(ctx context.Context, id string) (plugin.ItemData, error) {
	s, err := c.show(ctx, id, false)
	if err != nil {
		return plugin.ItemData{}, err
	}
	eps, err := c.episodes(ctx, id)
	if err != nil {
		return plugin.ItemData{}, err
	}
	return tvmazeData(s, eps), nil
}

func (c *tvmazeClient) people(ctx context.Context, id string) ([]plugin.Credit, error) {
	s, err := c.show(ctx, id, true)
	if err != nil {
		return nil, err
	}
	return tvmazeCredits(s.Embedded.Cast), nil
}

func (c *tvmazeClient) schedule(ctx context.Context, id string) ([]plugin.Airing, error) {
	eps, err := c.episodes(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]plugin.Airing, 0, len(eps))
	for _, e := range eps {
		if e.Season < 1 || e.Number < 1 || e.Airstamp == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339, e.Airstamp)
		if err != nil {
			continue
		}
		out = append(out, plugin.Airing{Season: e.Season, Number: e.Number, AirsAt: at})
	}
	return out, nil
}

func (c *tvmazeClient) lookup(ctx context.Context, idType, value string) (plugin.ItemData, error) {
	switch idType {
	case "tvmaze":
		return c.details(ctx, value)
	case "tvdb":
		return c.lookupExternal(ctx, "thetvdb", value)
	case "imdb":
		return c.lookupExternal(ctx, "imdb", value)
	}
	return plugin.ItemData{}, fmt.Errorf("tvmaze: cannot look up %q ids", idType)
}

func (c *tvmazeClient) lookupExternal(ctx context.Context, param, value string) (plugin.ItemData, error) {
	var s tvShow
	if err := c.get(ctx, "/lookup/shows?"+param+"="+url.QueryEscape(value), &s); err != nil {
		return plugin.ItemData{}, err
	}
	return c.details(ctx, fmt.Sprint(s.ID))
}

func tvmazeData(s tvShow, eps []tvEpisode) plugin.ItemData {
	fields := map[string]plugin.FieldValue{}
	if ty := showType(s.Type); ty != "" {
		fields["type"] = plugin.FieldValue{Text: ty}
	}
	if y := yearOf(s.Premiered); y > 0 {
		n := float64(y)
		fields["year"] = plugin.FieldValue{Num: &n}
	}
	seasons, episodes := 0, 0
	for _, e := range eps {
		if e.Season >= 1 {
			episodes++
			if e.Season > seasons {
				seasons = e.Season
			}
		}
	}
	if seasons > 0 {
		n := float64(seasons)
		fields["seasons"] = plugin.FieldValue{Num: &n}
	}
	if episodes > 0 {
		n := float64(episodes)
		fields["episodes"] = plugin.FieldValue{Num: &n}
	}
	if s.Runtime != nil && *s.Runtime > 0 {
		n := float64(*s.Runtime)
		fields["runtime"] = plugin.FieldValue{Num: &n}
	}
	if s.Network != nil && s.Network.Name != "" {
		fields["network"] = plugin.FieldValue{Text: s.Network.Name}
		if s.Network.Country != nil && s.Network.Country.Name != "" {
			fields["country"] = plugin.FieldValue{Text: s.Network.Country.Name}
		}
	} else if s.WebChannel != nil && s.WebChannel.Name != "" {
		fields["network"] = plugin.FieldValue{Text: s.WebChannel.Name}
	}
	if s.Status != "" {
		fields["air_status"] = plugin.FieldValue{Text: airStatus(s.Status)}
	}
	if summary := plugin.MultiLine(anilist.PlainText(s.Summary), maxTextRunes); summary != "" {
		fields["synopsis"] = plugin.FieldValue{Text: summary}
	}
	if d := dayOf(s.Premiered); d != nil {
		fields["started_on"] = plugin.FieldValue{Date: d}
	}
	if d := dayOf(s.Ended); d != nil {
		fields["ended_on"] = plugin.FieldValue{Date: d}
	}
	ext := map[string]string{"tvmaze": fmt.Sprint(s.ID)}
	if s.Externals.TVDB != nil {
		ext["tvdb"] = fmt.Sprint(*s.Externals.TVDB)
	}
	if s.Externals.IMDB != nil {
		ext["imdb"] = fmt.Sprint(*s.Externals.IMDB)
	}
	return showData(showRecord{
		Source: SourceTVmaze, ID: fmt.Sprint(s.ID),
		Titles: []plugin.Title{{Lang: "en", Text: s.Name}},
		Fields: fields, CoverURL: showImage(s.Image), ExternalURL: showURL(s.ID),
		ExternalIDs: ext, Genres: s.Genres,
	})
}

func tvmazeCredits(cs []tvCast) []plugin.Credit {
	out := make([]plugin.Credit, 0, len(cs))
	for _, c := range cs {
		name, cname := plugin.SingleLine(c.Person.Name, maxNameRunes), plugin.SingleLine(c.Character.Name, maxNameRunes)
		if name == "" || cname == "" {
			continue
		}
		out = append(out, plugin.Credit{Role: "actor",
			Person: plugin.PersonRef{ID: fmt.Sprintf("character-%d", c.Character.ID), Name: cname,
				ImageURL: imageURL(c.Character.Image), URL: plugin.HTTPSURL(c.Character.URL)},
			With: &plugin.PersonRef{ID: fmt.Sprintf("person-%d", c.Person.ID), Name: name,
				ImageURL: imageURL(c.Person.Image), URL: plugin.HTTPSURL(c.Person.URL)},
			WithRole: "actor",
		})
	}
	return out
}

func showType(raw string) string {
	switch strings.ToLower(raw) {
	case "scripted", "animation":
		return "series"
	case "miniseries":
		return "miniseries"
	case "reality":
		return "reality"
	case "documentary":
		return "documentary"
	}
	return "other"
}

func airStatus(raw string) string {
	switch strings.ToLower(raw) {
	case "running":
		return "running"
	case "ended":
		return "ended"
	case "to be determined", "in development":
		return "upcoming"
	}
	return "other"
}

func yearOf(date string) int {
	if d := dayOf(date); d != nil {
		return d.Year()
	}
	return 0
}

func dayOf(date string) *time.Time {
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

func showImage(img *struct{ Medium, Original string }) string {
	if img == nil {
		return ""
	}
	if img.Original != "" {
		return plugin.HTTPSURL(img.Original)
	}
	return plugin.HTTPSURL(img.Medium)
}

func imageURL(img *struct{ Medium, Original string }) string {
	return showImage(img)
}

func showURL(id int) string { return fmt.Sprintf("https://www.tvmaze.com/shows/%d", id) }

// tvSource adapts the TVmaze client to plugin.Source and ScheduleSource.
type tvmazeSource struct{ c *tvmazeClient }

func (s *tvmazeSource) Search(ctx context.Context, kind, q string) ([]plugin.ItemSummary, error) {
	if kind != KindShow {
		return nil, plugin.ErrNotFound
	}
	return s.c.search(ctx, q)
}

func (s *tvmazeSource) Details(ctx context.Context, kind, id string) (plugin.ItemData, error) {
	if kind != KindShow {
		return plugin.ItemData{}, plugin.ErrNotFound
	}
	return s.c.details(ctx, id)
}

func (s *tvmazeSource) People(ctx context.Context, kind, id string) ([]plugin.Credit, error) {
	if kind != KindShow {
		return nil, plugin.ErrNotFound
	}
	cs, err := s.c.people(ctx, id)
	if err != nil {
		return nil, err
	}
	return showCredits(cs), nil
}

func (s *tvmazeSource) Recommendations(context.Context, string, string) ([]plugin.ItemSummary, error) {
	return nil, nil
}

func (s *tvmazeSource) Lookup(ctx context.Context, kind, idType, value string) (plugin.ItemData, error) {
	if kind != KindShow {
		return plugin.ItemData{}, plugin.ErrNotFound
	}
	return s.c.lookup(ctx, idType, value)
}

func (s *tvmazeSource) Schedule(ctx context.Context, kind, id string) ([]plugin.Airing, error) {
	if kind != KindShow {
		return nil, plugin.ErrNotFound
	}
	return s.c.schedule(ctx, id)
}

var _ plugin.ScheduleSource = (*tvmazeSource)(nil)

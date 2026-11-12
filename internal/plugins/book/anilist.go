package book

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anilist"
)

// novelClient is the part of the shared AniList client the series source, its
// account importer and the Yamtrack novel importer use.
type novelClient interface {
	SearchFormat(ctx context.Context, typ, format, q string) ([]anilist.SearchResult, error)
	Media(ctx context.Context, typ, id string) (anilist.Media, error)
	MediaByMALID(ctx context.Context, typ, malID string) (anilist.Media, error)
	MediaList(ctx context.Context, typ, userName string) ([]anilist.AccountEntry, error)
	MediaFormatsByMALIDs(ctx context.Context, typ string, malIDs []int) (map[int]string, error)
}

type anilistSource struct{ c novelClient }

func (s *anilistSource) Search(ctx context.Context, kind, q string) ([]plugin.ItemSummary, error) {
	if kind != KindSeries {
		return nil, plugin.ErrNotFound
	}
	res, err := s.c.SearchFormat(ctx, anilist.TypeManga, "NOVEL", q)
	if err != nil {
		return nil, mapAniListErr(err)
	}
	out := make([]plugin.ItemSummary, 0, len(res))
	for _, r := range res {
		if r.Type != "NOVEL" {
			continue
		}
		out = append(out, novelSummary(r))
	}
	return out, nil
}

func (s *anilistSource) Details(ctx context.Context, kind, id string) (plugin.ItemData, error) {
	if kind != KindSeries {
		return plugin.ItemData{}, plugin.ErrNotFound
	}
	m, err := s.media(ctx, id)
	if err != nil {
		return plugin.ItemData{}, err
	}
	return novelData(m), nil
}

func (s *anilistSource) People(ctx context.Context, kind, id string) ([]plugin.Credit, error) {
	if kind != KindSeries {
		return nil, plugin.ErrNotFound
	}
	m, err := s.media(ctx, id)
	if err != nil {
		return nil, err
	}
	return novelCredits(m.Staff), nil
}

func (s *anilistSource) Recommendations(context.Context, string, string) ([]plugin.ItemSummary, error) {
	return nil, nil
}

func (s *anilistSource) Lookup(ctx context.Context, kind, idType, value string) (plugin.ItemData, error) {
	if kind != KindSeries {
		return plugin.ItemData{}, plugin.ErrNotFound
	}
	switch idType {
	case "anilist":
		return s.Details(ctx, kind, value)
	case "mal":
		m, err := s.c.MediaByMALID(ctx, anilist.TypeManga, value)
		if err != nil {
			return plugin.ItemData{}, mapAniListErr(err)
		}
		if m.Format != "NOVEL" {
			return plugin.ItemData{}, plugin.ErrNotFound
		}
		return novelData(m), nil
	}
	return plugin.ItemData{}, fmt.Errorf("anilist: cannot look up %q ids", idType)
}

func (s *anilistSource) media(ctx context.Context, id string) (anilist.Media, error) {
	m, err := s.c.Media(ctx, anilist.TypeManga, id)
	if err != nil {
		return anilist.Media{}, mapAniListErr(err)
	}
	if m.Format != "NOVEL" {
		return anilist.Media{}, plugin.ErrNotFound
	}
	return m, nil
}

func mapAniListErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, anilist.ErrNotFound):
		return fmt.Errorf("%w: %w", plugin.ErrNotFound, err)
	case errors.Is(err, anilist.ErrRateLimited):
		return fmt.Errorf("%w: %w", plugin.ErrRateLimited, err)
	case errors.Is(err, anilist.ErrBadResponse):
		return fmt.Errorf("%w: %w", plugin.ErrBadResponse, err)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return fmt.Errorf("%w: %w", plugin.ErrUnavailable, err)
	}
}

func novelSummary(r anilist.SearchResult) plugin.ItemSummary {
	fields := map[string]plugin.FieldValue{}
	if r.Volumes != nil && *r.Volumes > 0 {
		v := float64(*r.Volumes)
		fields["volumes"] = plugin.FieldValue{Num: &v}
	}
	if r.Chapters != nil && *r.Chapters > 0 {
		v := float64(*r.Chapters)
		fields["chapters"] = plugin.FieldValue{Num: &v}
	}
	if r.Year != nil && *r.Year > 0 {
		v := float64(*r.Year)
		fields["year"] = plugin.FieldValue{Num: &v}
	}
	if s := plugin.MultiLine(r.Synopsis, maxTextRunes); s != "" {
		fields["synopsis"] = plugin.FieldValue{Text: s}
	}
	return plugin.ItemSummary{
		Source: SourceAniList, ID: r.ID, Kind: KindSeries,
		Titles:   novelTitles(r.Titles),
		Fields:   fields,
		CoverURL: plugin.HTTPSURL(r.CoverURL),
		URL:      "https://anilist.co/manga/" + url.PathEscape(r.ID),
	}
}

func novelData(m anilist.Media) plugin.ItemData {
	d := plugin.ItemData{
		ItemSummary: novelSummary(m.SearchResult),
		ExternalURL: plugin.HTTPSURL(m.ExternalURL),
	}
	if d.ExternalURL == "" {
		d.ExternalURL = "https://anilist.co/manga/" + url.PathEscape(m.ID)
	}
	d.ExternalIDs = map[string]string{"anilist": m.ID}
	if mal := plugin.SingleLine(m.MALID, maxIDRunes); mal != "" {
		d.ExternalIDs["mal"] = mal
	}
	d.Genres = cleanTerms(m.Genres, maxGenres)
	d.Tags = cleanTags(novelTags(m.Tags))
	if a := novelAuthor(m.Staff); a != "" {
		d.Fields["author"] = plugin.FieldValue{Text: a}
	}
	if m.StartDate != nil {
		day := plugin.DateOnly(*m.StartDate)
		d.Fields["published_on"] = plugin.FieldValue{Date: &day}
	}
	return d
}

func novelTags(tags []anilist.Tag) []plugin.Tag {
	out := make([]plugin.Tag, 0, len(tags))
	for _, t := range tags {
		out = append(out, plugin.Tag{Name: t.Name, Rank: t.Rank, Spoiler: t.Spoiler})
	}
	return out
}

func novelTitles(ts []anilist.Title) []plugin.Title {
	var out []plugin.Title
	for _, t := range ts {
		text := plugin.SingleLine(t.Text, maxTitleRunes)
		if text == "" {
			continue
		}
		out = append(out, plugin.Title{Lang: t.Lang, Text: text})
	}
	return out
}

func novelAuthor(staff []anilist.StaffMember) string {
	for _, s := range staff {
		if strings.Contains(strings.ToLower(s.Role), "story") {
			if n := plugin.SingleLine(s.Name, maxNameRunes); n != "" {
				return n
			}
		}
	}
	return ""
}

func novelCredits(staff []anilist.StaffMember) []plugin.Credit {
	out := make([]plugin.Credit, 0, len(staff))
	for _, s := range staff {
		name := plugin.SingleLine(s.Name, maxNameRunes)
		role := ""
		low := strings.ToLower(s.Role)
		switch {
		case strings.Contains(low, "story"):
			role = "author"
		case strings.Contains(low, "art"), strings.Contains(low, "illustration"):
			role = "illustrator"
		}
		if name == "" || role == "" || len(out) == maxCredits {
			continue
		}
		out = append(out, plugin.Credit{Role: role, Detail: plugin.SingleLine(s.Role, maxDetailRunes),
			Person: plugin.PersonRef{
				ID:       novelPersonID(name, s.URL),
				Name:     name,
				ImageURL: plugin.HTTPSURL(s.ImageURL),
				URL:      plugin.HTTPSURL(s.URL),
			}})
	}
	return out
}

func novelPersonID(name, staffURL string) string {
	if u := strings.TrimSpace(staffURL); u != "" {
		if i := strings.LastIndex(u, "/staff/"); i >= 0 {
			if id := strings.Trim(u[i+len("/staff/"):], "/"); id != "" {
				return "anilist-" + id
			}
		}
	}
	sum := sha256.Sum256([]byte(strings.ToLower(strings.Join(strings.Fields(name), " "))))
	return fmt.Sprintf("anilist-%x", sum[:8])
}

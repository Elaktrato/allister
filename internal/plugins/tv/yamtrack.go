package tv

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/yamtrack"
)

type yamtrackTVImporter struct{}

type tvAggregate struct {
	show     yamtrack.RawRow
	seasons  map[int]*yamtrack.RawRow
	episodes map[int]map[int]bool
	notes    []string
}

func (yamtrackTVImporter) Parse(ctx context.Context, r io.Reader) (plugin.ParseResult, error) {
	if err := ctx.Err(); err != nil {
		return plugin.ParseResult{}, err
	}
	rows, err := yamtrack.ParseRaw(r)
	if err != nil {
		return plugin.ParseResult{}, fmt.Errorf("%w: %w", plugin.ErrBadInput, err)
	}
	var res plugin.ParseResult
	groups := map[string]*tvAggregate{}
	order := []string{}
	for _, row := range rows {
		switch row.MediaType {
		case "tv":
			key := tvKey(row)
			if key == "" {
				res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: row.Line, Title: row.Title, Reason: "TV row has no source/id or title"})
				continue
			}
			if _, ok := groups[key]; ok {
				res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: row.Line, Title: row.Title, Reason: "duplicate TV row"})
				continue
			}
			groups[key] = &tvAggregate{show: row, seasons: map[int]*yamtrack.RawRow{}, episodes: map[int]map[int]bool{}}
			order = append(order, key)
		case "season":
			key := tvKey(row)
			g := groups[key]
			if g == nil {
				res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: row.Line, Title: row.Title, Reason: "season without a TV row"})
				continue
			}
			if row.SeasonNumber < 1 {
				g.notes = append(g.notes, fmt.Sprintf("season row %d ignored (specials are not progress)", row.SeasonNumber))
				continue
			}
			if _, exists := g.seasons[row.SeasonNumber]; exists {
				g.notes = append(g.notes, fmt.Sprintf("duplicate season %d ignored", row.SeasonNumber))
				continue
			}
			s := row
			g.seasons[row.SeasonNumber] = &s
			for _, n := range row.Notes {
				g.notes = append(g.notes, fmt.Sprintf("season %d: %s", row.SeasonNumber, n))
			}
		case "episode":
			key := tvKey(row)
			g := groups[key]
			if g == nil {
				res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: row.Line, Title: row.Title, Reason: "episode without a TV row"})
				continue
			}
			if row.SeasonNumber > 0 && row.EpisodeNumber > 0 {
				if g.episodes[row.SeasonNumber] == nil {
					g.episodes[row.SeasonNumber] = map[int]bool{}
				}
				g.episodes[row.SeasonNumber][row.EpisodeNumber] = true
			}
			for _, n := range row.Notes {
				g.notes = append(g.notes, fmt.Sprintf("season %d episode %d: %s", row.SeasonNumber, row.EpisodeNumber, n))
			}
		default:
			res.Ignored++
		}
	}
	for _, key := range order {
		g := groups[key]
		entry, _, reason := tvEntry(*g)
		if reason != "" {
			res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: g.show.Line, Title: g.show.Title, Reason: reason})
			continue
		}
		res.Entries = append(res.Entries, entry)
	}
	return res, nil
}

func tvKey(row yamtrack.RawRow) string {
	id := strings.TrimSpace(row.MediaID)
	if id != "" {
		return row.Source + "|" + id
	}
	if row.Title != "" {
		return row.Source + "|title|" + row.Title
	}
	return ""
}

func tvEntry(g tvAggregate) (plugin.ParsedEntry, []string, string) {
	show := g.show
	title := plugin.SingleLine(show.Title, maxTitleRunes)
	if title == "" {
		return plugin.ParsedEntry{}, nil, "TV row has no title"
	}
	if show.Status == "" {
		return plugin.ParsedEntry{}, nil, "TV row has an unknown status"
	}
	perSeason := map[int]int{}
	for season, set := range g.episodes {
		if season >= 1 {
			perSeason[season] = len(set)
		}
	}
	for season, s := range g.seasons {
		if season >= 1 && s.Progress > perSeason[season] {
			perSeason[season] = s.Progress
		}
	}
	current := 0
	for season, s := range g.seasons {
		if season < 1 {
			continue
		}
		if perSeason[season] > 0 || s.Status == plugin.Completed {
			if season > current {
				current = season
			}
		}
	}
	for season, watched := range perSeason {
		if watched > 0 && season > current {
			current = season
		}
	}
	episodes := show.Progress
	if episodes <= 0 {
		for season, watched := range perSeason {
			if season >= 1 {
				episodes += watched
			}
		}
	}
	notes := append([]string(nil), show.Notes...)
	notes = append(notes, g.notes...)
	if show.Progress > 0 {
		sum := 0
		for season, watched := range perSeason {
			if season >= 1 {
				sum += watched
			}
		}
		if sum > 0 && sum != show.Progress {
			notes = append(notes, fmt.Sprintf("TV progress %d differs from season rows (%d); kept the TV progress", show.Progress, sum))
		}
	}
	entry := plugin.ParsedEntry{
		Kind: KindShow, Title: title, Status: show.Status,
		Progress: map[string]float64{"season": float64(current), "episode": float64(episodes)},
		Rating:   show.Rating, StartedOn: show.StartedOn, FinishedOn: show.FinishedOn,
		Comment:  plugin.MultiLine(show.Comment, maxTextRunes),
		CoverURL: plugin.HTTPSURL(show.ImageURL),
		Notes:    notes,
	}
	if show.Source == SourceTMDB {
		if id := numericID(show.MediaID); id != "" {
			entry.ExternalIDs = map[string]string{"tmdb": id}
		}
	} else if show.Source == "tvdb" {
		if id := numericID(show.MediaID); id != "" {
			entry.ExternalIDs = map[string]string{"tvdb": id}
		}
	}
	return entry, notes, ""
}

func numericID(s string) string {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}

var _ plugin.Importer = yamtrackTVImporter{}

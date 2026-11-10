package anime

// The account import (AniList username) and the MyAnimeList XML export
// importer. Both normalise into ParsedEntry rows so the runner, the pacing
// and the import modes work exactly like the CSV import.

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anilist"
)

// accountListReader is the optional account-list lookup of a provider
// (anilist.Client); the manifest declares the account importer when it exists.
type accountListReader interface {
	MediaList(ctx context.Context, typ, userName string) ([]anilist.AccountEntry, error)
}

// accountStatus maps AniList list statuses onto the core classes.
var accountStatus = map[string]plugin.StatusClass{
	"CURRENT":   plugin.Active,
	"REPEATING": plugin.Active,
	"PLANNING":  plugin.Planned,
	"COMPLETED": plugin.Completed,
	"PAUSED":    plugin.Paused,
	"DROPPED":   plugin.Dropped,
}

// accountImporter imports an AniList user's list by name.
type accountImporter struct{ c anilist.Provider }

// Parse refuses a file: this importer takes an account name (the UI offers
// the field through ImporterInfo.AccountLabel).
func (accountImporter) Parse(ctx context.Context, r io.Reader) (plugin.ParseResult, error) {
	return plugin.ParseResult{}, fmt.Errorf("%w: this importer takes a username, not a file", plugin.ErrBadInput)
}

// ImportAccount fetches the user's list and maps it to rows keyed by the
// AniList id. An unknown user is ErrNotFound; the account's score (which the
// query asks AniList to normalise to 0–10) becomes the rating (0 stays
// unrated), and each entry keeps its source modification time for the
// "newer" import mode.
func (ai accountImporter) ImportAccount(ctx context.Context, account string) (plugin.ParseResult, error) {
	account = strings.TrimSpace(account)
	if account == "" || len(account) > 64 || strings.IndexFunc(account, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return plugin.ParseResult{}, fmt.Errorf("%w: enter the AniList username (one word)", plugin.ErrBadInput)
	}
	r, ok := ai.c.(accountListReader)
	if !ok {
		return plugin.ParseResult{}, fmt.Errorf("anime: the provider cannot list accounts: %w", plugin.ErrUnavailable)
	}
	entries, err := r.MediaList(ctx, anilist.TypeAnime, account)
	if err != nil {
		return plugin.ParseResult{}, mapErr(err)
	}
	var res plugin.ParseResult
	for i, e := range entries {
		st, ok := accountStatus[e.Status]
		if !ok {
			res.Skipped = append(res.Skipped, plugin.SkippedRow{
				Line: i + 1, Title: plugin.SingleLine(accountTitle(e), maxTitleRunes),
				Reason: "unknown list status " + plugin.SingleLine(e.Status, 40),
			})
			continue
		}
		row := plugin.ParsedEntry{
			Kind:        KindAnime,
			Title:       plugin.SingleLine(accountTitle(e), maxTitleRunes),
			ExternalIDs: map[string]string{"anilist": strconv.Itoa(e.Media.ID)},
			Status:      st,
			Progress:    map[string]float64{"episode": float64(max(e.Progress, 0))},
		}
		if row.Title == "" {
			row.Title = "AniList #" + strconv.Itoa(e.Media.ID)
		}
		if e.Score > 0 {
			s := min(10, max(1, int(math.Round(e.Score))))
			row.Rating = &s
		}
		if u := plugin.HTTPSURL(e.Media.Cover.Large); u != "" {
			row.CoverURL = u
		}
		if e.UpdatedAt > 0 {
			t := time.Unix(e.UpdatedAt, 0).UTC()
			row.UpdatedAt = &t
		}
		res.Entries = append(res.Entries, row)
	}
	return res, nil
}

func accountTitle(e anilist.AccountEntry) string {
	return firstNonEmpty(e.Media.Title.Romaji, e.Media.Title.English, e.Media.Title.Native)
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// malXMLImporter parses the XML export MyAnimeList offers from a user's list
// page.
type malXMLImporter struct{}

// malExport is the part of the export the importer reads.
type malExport struct {
	Anime []struct {
		SeriesID string `xml:"series_animedb_id"`
		Title    string `xml:"series_title"`
		Watched  int    `xml:"my_watched_episodes"`
		Score    int    `xml:"my_score"`
		Status   int    `xml:"my_status"`
	} `xml:"anime"`
}

// malStatus maps MyAnimeList's numeric list statuses onto the core classes.
var malStatus = map[int]plugin.StatusClass{
	1: plugin.Active,    // watching
	2: plugin.Completed, // completed
	3: plugin.Paused,    // on hold
	4: plugin.Dropped,   // dropped
	6: plugin.Planned,   // plan to watch
}

// Parse reads the export; a document without a single anime row is not a
// MyAnimeList export (ErrBadInput).
func (malXMLImporter) Parse(ctx context.Context, r io.Reader) (plugin.ParseResult, error) {
	if err := ctx.Err(); err != nil {
		return plugin.ParseResult{}, err
	}
	dec := xml.NewDecoder(r)
	var doc malExport
	if err := dec.Decode(&doc); err != nil {
		return plugin.ParseResult{}, fmt.Errorf("%w: not a MyAnimeList XML export: %v", plugin.ErrBadInput, err)
	}
	if len(doc.Anime) == 0 {
		return plugin.ParseResult{}, fmt.Errorf("%w: the export holds no entries", plugin.ErrBadInput)
	}
	var res plugin.ParseResult
	for i, a := range doc.Anime {
		malID := strings.TrimSpace(a.SeriesID)
		if malID == "" || len(malID) > maxIDDigits || strings.Trim(malID, "0123456789") != "" {
			res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: i + 1, Title: plugin.SingleLine(a.Title, maxTitleRunes), Reason: "no usable MyAnimeList id"})
			continue
		}
		st, ok := malStatus[a.Status]
		if !ok {
			res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: i + 1, Title: plugin.SingleLine(a.Title, maxTitleRunes), Reason: fmt.Sprintf("unknown list status %d", a.Status)})
			continue
		}
		e := plugin.ParsedEntry{
			Kind: KindAnime, Title: plugin.SingleLine(a.Title, maxTitleRunes),
			ExternalIDs: map[string]string{"mal": malID},
			Status:      st,
			Progress:    map[string]float64{"episode": float64(max(a.Watched, 0))},
		}
		if a.Score > 0 {
			s := min(a.Score, 10)
			e.Rating = &s
		}
		if e.Title == "" {
			e.Title = "MAL #" + malID
		}
		res.Entries = append(res.Entries, e)
	}
	return res, nil
}

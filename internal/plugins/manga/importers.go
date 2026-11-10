package manga

// The AniList account import (by username) and the MyAnimeList manga XML
// export importer; both produce rows the core's import runner processes like
// any other import.

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

// accountListReader is the account-list lookup of the shared client.
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

type accountImporter struct{ c anilist.Provider }

// Parse refuses a file: this importer takes an account name.
func (accountImporter) Parse(ctx context.Context, r io.Reader) (plugin.ParseResult, error) {
	return plugin.ParseResult{}, fmt.Errorf("%w: this importer takes a username, not a file", plugin.ErrBadInput)
}

// ImportAccount fetches a user's manga list and maps it to rows keyed by the
// AniList id.
func (ai accountImporter) ImportAccount(ctx context.Context, account string) (plugin.ParseResult, error) {
	account = strings.TrimSpace(account)
	if account == "" || len(account) > 64 || strings.IndexFunc(account, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return plugin.ParseResult{}, fmt.Errorf("%w: enter the AniList username (one word)", plugin.ErrBadInput)
	}
	r, ok := ai.c.(accountListReader)
	if !ok {
		return plugin.ParseResult{}, fmt.Errorf("manga: the provider cannot list accounts: %w", plugin.ErrUnavailable)
	}
	entries, err := r.MediaList(ctx, anilist.TypeManga, account)
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
			Kind:        KindManga,
			Title:       plugin.SingleLine(accountTitle(e), maxTitleRunes),
			ExternalIDs: map[string]string{"anilist": strconv.Itoa(e.Media.ID)},
			Status:      st,
			Progress:    map[string]float64{"chapter": float64(max(e.Progress, 0))},
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

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// malXMLImporter parses the manga part of a MyAnimeList XML export.
type malXMLImporter struct{}

type malExport struct {
	Manga []struct {
		SeriesID     string `xml:"series_animedb_id"`
		Title        string `xml:"series_title"`
		ReadChapters int    `xml:"my_read_chapters"`
		ReadVolumes  int    `xml:"my_read_volumes"`
		Score        int    `xml:"my_score"`
		Status       int    `xml:"my_status"`
	} `xml:"manga"`
}

// malStatus maps MyAnimeList's numeric list statuses onto the core classes.
var malStatus = map[int]plugin.StatusClass{
	1: plugin.Active,    // reading
	2: plugin.Completed, // completed
	3: plugin.Paused,    // on hold
	4: plugin.Dropped,   // dropped
	6: plugin.Planned,   // plan to read
}

// Parse reads the export; a document without a manga row is not a manga
// export (ErrBadInput).
func (malXMLImporter) Parse(ctx context.Context, r io.Reader) (plugin.ParseResult, error) {
	if err := ctx.Err(); err != nil {
		return plugin.ParseResult{}, err
	}
	dec := xml.NewDecoder(r)
	var doc malExport
	if err := dec.Decode(&doc); err != nil {
		return plugin.ParseResult{}, fmt.Errorf("%w: not a MyAnimeList XML export: %v", plugin.ErrBadInput, err)
	}
	if len(doc.Manga) == 0 {
		return plugin.ParseResult{}, fmt.Errorf("%w: the export holds no manga entries", plugin.ErrBadInput)
	}
	var res plugin.ParseResult
	for i, a := range doc.Manga {
		malID := strings.TrimSpace(a.SeriesID)
		if malID == "" || len(malID) > 12 || strings.Trim(malID, "0123456789") != "" {
			res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: i + 1, Title: plugin.SingleLine(a.Title, maxTitleRunes), Reason: "no usable MyAnimeList id"})
			continue
		}
		st, ok := malStatus[a.Status]
		if !ok {
			res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: i + 1, Title: plugin.SingleLine(a.Title, maxTitleRunes), Reason: fmt.Sprintf("unknown list status %d", a.Status)})
			continue
		}
		e := plugin.ParsedEntry{
			Kind: KindManga, Title: plugin.SingleLine(a.Title, maxTitleRunes),
			ExternalIDs: map[string]string{"mal": malID},
			Status:      st,
			Progress:    map[string]float64{"chapter": float64(max(a.ReadChapters, 0))},
		}
		if a.ReadVolumes > 0 {
			e.Progress["volume"] = float64(a.ReadVolumes)
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

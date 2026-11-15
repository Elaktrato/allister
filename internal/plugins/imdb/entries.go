package imdb

import (
	"fmt"

	"github.com/allister/allister/internal/plugin"
)

// Entries turns parsed rows into a ParseResult for one plugin. want is the
// classified type the plugin keeps (TypeMovie or TypeTV); rows of the other
// known types are only counted as ignored, an unknown Title Type is skipped
// with its value in the reason, and a repeated Const is reported once. A rated
// row is completed with its rating and Date Rated; the rest are planned.
func Entries(rows []Row, kind, want string) plugin.ParseResult {
	var res plugin.ParseResult
	seen := map[string]bool{}
	for _, row := range rows {
		switch c := Classify(row.TypeKey); {
		case c == TypeUnknown:
			res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: row.Line, Title: row.Title,
				Reason: fmt.Sprintf("unknown Title Type %q", row.TypeRaw)})
			continue
		case c != want:
			res.Ignored++
			continue
		}
		if row.Invalid != "" {
			res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: row.Line, Title: row.Title, Reason: row.Invalid})
			continue
		}
		if seen[row.Const] {
			res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: row.Line, Title: row.Title, Reason: "duplicate IMDb id " + row.Const})
			continue
		}
		seen[row.Const] = true
		title := row.Title
		if title == "" {
			title = "IMDb " + row.Const
		}
		e := plugin.ParsedEntry{
			Kind: kind, Title: title, ExternalIDs: map[string]string{"imdb": row.Const},
			Status: plugin.Planned,
		}
		if row.Rating != nil {
			e.Status, e.Rating, e.FinishedOn = plugin.Completed, row.Rating, row.RatedOn
		}
		if row.Year > 0 {
			y := float64(row.Year)
			e.Fields = map[string]plugin.FieldValue{"year": {Num: &y}}
		}
		res.Entries = append(res.Entries, e)
	}
	return res
}

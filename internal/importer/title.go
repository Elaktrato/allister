package importer

import (
	"strings"
	"unicode"

	"github.com/allister/allister/internal/plugin"
)

// normaliseTitle is the form two titles are compared in when a row is
// resolved by title: lower case, "&" read as "and", every rune that is not a
// letter, a combining mark or a number (digits, superscripts, Roman numeral runes) dropped. Diacritics are not folded and
// combining marks are kept, so "Amélie" (composed or not) and "Amelie"
// differ: the row then stays unmatched, as a wrong match is worse than none.
func normaliseTitle(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r == '&':
			b.WriteString("and")
		case unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsNumber(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// rowYear is the row's numeric "year" field.
func rowYear(e plugin.ParsedEntry) (float64, bool) {
	f, ok := e.Fields["year"]
	if !ok || f.Num == nil {
		return 0, false
	}
	return *f.Num, true
}

// matchTitle returns the one candidate of the source (or of no source) and
// of the kind (or of no kind) whose year equals year and one of whose titles
// equals title once both are normalised. ok is false when none or several
// match.
func matchTitle(cands []plugin.ItemSummary, source, kind, title string, year float64) (plugin.ItemSummary, bool) {
	want := normaliseTitle(title)
	var match plugin.ItemSummary
	n := 0
	for _, c := range cands {
		if (c.Kind != "" && c.Kind != kind) || (c.Source != "" && c.Source != source) {
			continue
		}
		if y, ok := c.Fields["year"]; !ok || y.Num == nil || *y.Num != year {
			continue
		}
		for _, t := range c.Titles {
			if want != "" && normaliseTitle(t.Text) == want {
				match = c
				n++
				break
			}
		}
	}
	return match, n == 1
}

// titleUnmatchedNote is the importer's UnmatchedNote for a row resolved by
// title, filled with the row's id of ExternalIDType, title and year; "" when
// the importer has none.
func titleUnmatchedNote(rc *rowCtx) string {
	year := 0
	if y, ok := rowYear(rc.entry); ok {
		year = int(y)
	}
	id := ""
	if rc.idType != "" {
		id = plugin.SingleLine(rc.entry.ExternalIDs[rc.idType], maxTitleRunes)
	}
	title := rc.entry.Title
	if rc.generatedTitle {
		title = ""
	}
	return plugin.FillUnmatchedNote(rc.ref.info.UnmatchedNote, id, title, year)
}

// storedEntry is the JSON of a stored row: the parsed entry plus what Submit
// noted about it.
type storedEntry struct {
	plugin.ParsedEntry
	// GeneratedTitle is set when the file had no title and Submit used
	// "<id label> #<id>" instead: such a title is never searched.
	GeneratedTitle bool `json:"generated_title,omitempty"`
}

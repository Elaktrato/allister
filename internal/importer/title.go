package importer

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/allister/allister/internal/plugin"
)

// normaliseTitle is the form two titles are compared in when a row is
// resolved by title: lower case, "&" read as "and", every rune that is not a
// letter or a digit dropped. Diacritics are not folded, so "Amélie" and
// "Amelie" differ (the row then stays unmatched, which is safe).
func normaliseTitle(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r == '&':
			b.WriteString("and")
		case unicode.IsLetter(r) || unicode.IsDigit(r):
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

// matchTitle returns the one candidate of the kind (or of no kind) whose year
// equals year and one of whose titles equals title once both are normalised.
// ok is false when none or several match.
func matchTitle(cands []plugin.ItemSummary, kind, title string, year float64) (plugin.ItemSummary, bool) {
	want := normaliseTitle(title)
	var match plugin.ItemSummary
	n := 0
	for _, c := range cands {
		if c.Kind != "" && c.Kind != kind {
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

// titleUnmatchedNote is the importer's UnmatchedNote for a row resolved by title,
// with {title}, {year} and {id} (the row's id of ExternalIDType) replaced;
// "" when the importer has none.
func titleUnmatchedNote(rc *rowCtx) string {
	note := rc.ref.info.UnmatchedNote
	if note == "" {
		return ""
	}
	year := ""
	if y, ok := rowYear(rc.entry); ok {
		year = strconv.FormatFloat(y, 'f', -1, 64)
	}
	id := ""
	if rc.idType != "" {
		id = plugin.SingleLine(rc.entry.ExternalIDs[rc.idType], maxTitleRunes)
	}
	return strings.NewReplacer("{title}", rc.entry.Title, "{year}", year, "{id}", id).Replace(note)
}

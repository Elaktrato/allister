package plugin

import (
	"strconv"
	"strings"
)

// FillUnmatchedNote fills an ImporterInfo.UnmatchedNote for one unmatched
// item: every "{id}" becomes id (the item's id of ExternalIDType), every
// "{title}" title and every "{year}" year (empty when year is 0). The
// placeholders are replaced in one pass, so a title containing one is not
// filled again.
func FillUnmatchedNote(note, id, title string, year int) string {
	if note == "" {
		return ""
	}
	y := ""
	if year != 0 {
		y = strconv.Itoa(year)
	}
	return strings.NewReplacer("{id}", id, "{title}", title, "{year}", y).Replace(note)
}

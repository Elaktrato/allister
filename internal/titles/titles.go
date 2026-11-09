// Package titles picks a display title from a set of per-language titles.
package titles

import "github.com/allister/allister/internal/provider"

// fallback is tried, in order, when the preferred language is missing.
var fallback = []string{"ja", "ja-latn", "en"}

// Resolve returns the title in the preferred language. If it is missing it
// falls back to native (ja), romaji (ja-latn), English (en), then the first
// available title. It returns "" only for an empty slice.
func Resolve(ts []provider.Title, preferred string) string {
	for _, lang := range append([]string{preferred}, fallback...) {
		for _, t := range ts {
			if t.Lang == lang {
				return t.Text
			}
		}
	}
	if len(ts) > 0 {
		return ts[0].Text
	}
	return ""
}

// Languages returns the distinct language tags of ts in input order.
func Languages(ts []provider.Title) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, t := range ts {
		if !seen[t.Lang] {
			seen[t.Lang] = true
			out = append(out, t.Lang)
		}
	}
	return out
}

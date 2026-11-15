package plugin

import "strings"

// CleanTerms sanitises genre-like terms for a plugin's item data: each term
// is a SingleLine of at most maxRunes, empty terms and case-insensitive
// duplicates are dropped (the first spelling wins), and at most limit are
// kept.
func CleanTerms(in []string, maxRunes, limit int) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range in {
		term := SingleLine(raw, maxRunes)
		key := strings.ToLower(term)
		if term == "" || seen[key] || len(out) == limit {
			continue
		}
		seen[key] = true
		out = append(out, term)
	}
	return out
}

// CleanTags sanitises tags like CleanTerms does terms, keeps each tag's
// spoiler flag, and keeps its rank only when it is within 0..100.
func CleanTags(in []Tag, maxRunes, limit int) []Tag {
	var out []Tag
	seen := map[string]bool{}
	for _, t := range in {
		name := SingleLine(t.Name, maxRunes)
		key := strings.ToLower(name)
		if name == "" || seen[key] || len(out) == limit {
			continue
		}
		seen[key] = true
		tag := Tag{Name: name, Spoiler: t.Spoiler}
		if t.Rank != nil && *t.Rank >= 0 && *t.Rank <= 100 {
			r := *t.Rank
			tag.Rank = &r
		}
		out = append(out, tag)
	}
	return out
}

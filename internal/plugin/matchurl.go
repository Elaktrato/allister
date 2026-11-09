package plugin

import "strings"

// Declarative source URL matching. A source declares URL patterns as data
// (SourceInfo.URLPatterns, for example "anilist.co/anime/{id}") and the core
// matches what a person pastes against them, so no plugin code parses URLs.

// maxIDDigits is the longest id {id} matches.
const maxIDDigits = 12

// normalizeID accepts 1 to maxIDDigits ASCII digits, strips leading zeros
// and rejects a value that is zero.
func normalizeID(s string) (string, bool) {
	if s == "" || len(s) > maxIDDigits {
		return "", false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return "", false
		}
	}
	s = strings.TrimLeft(s, "0")
	return s, s != ""
}

// MatchURL reduces a pasted URL to the id of the first pattern it matches.
//
// A pattern is "host/segment/.../{id}[/segment...]": the host (an optional
// leading "www." is ignored), literal path segments and exactly one {id}
// segment. A URL matches when, after trimming surrounding whitespace:
//   - the scheme is http or https (any case) or absent; other schemes and
//     scheme-relative "//" URLs are rejected;
//   - the host equals the pattern's host case-insensitively, with or without
//     one "www." (no port, no userinfo, nothing else);
//   - the path starts with the pattern's segments (literals compared exactly,
//     {id} being 1 to 12 ASCII digits, not zero; leading zeros are removed
//     from the returned id), optionally followed by one slug segment and/or
//     a trailing slash, then an optional query and fragment.
//
// Anything else, including control characters, spaces and backslashes
// anywhere, yields "", false. Patterns that are malformed are skipped.
func MatchURL(patterns []string, raw string) (id string, ok bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c <= ' ' || c == 0x7f || c == '\\' {
			return "", false
		}
	}
	lower := strings.ToLower(s)
	switch {
	case strings.HasPrefix(lower, "https://"):
		s = s[len("https://"):]
	case strings.HasPrefix(lower, "http://"):
		s = s[len("http://"):]
	case strings.Contains(s, "://"), strings.HasPrefix(s, "//"):
		return "", false
	}
	host, path, _ := strings.Cut(s, "/")
	host = strings.ToLower(host)
	if strings.HasPrefix(host, "www.") {
		host = host[len("www."):]
	}
	if host == "" {
		return "", false
	}
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	segs := strings.Split(path, "/")
	for _, p := range patterns {
		if id, ok := matchPattern(p, host, segs); ok {
			return id, true
		}
	}
	return "", false
}

func matchPattern(pattern, host string, segs []string) (string, bool) {
	ph, pp, ok := strings.Cut(pattern, "/")
	ph = strings.ToLower(ph)
	if strings.HasPrefix(ph, "www.") {
		ph = ph[len("www."):]
	}
	if !ok || ph == "" || strings.ContainsAny(ph, ":@?#{}") || ph != host {
		return "", false
	}
	psegs := strings.Split(pp, "/")
	if strings.Count(pp, "{id}") != 1 || len(segs) < len(psegs) {
		return "", false
	}
	var id string
	for i, ps := range psegs {
		if ps == "{id}" {
			n, ok := normalizeID(segs[i])
			if !ok {
				return "", false
			}
			id = n
			continue
		}
		if ps == "" || segs[i] != ps {
			return "", false
		}
	}
	switch rest := segs[len(psegs):]; {
	case len(rest) == 0:
	case len(rest) == 1: // a slug or a trailing slash
	case len(rest) == 2 && rest[0] != "" && rest[1] == "": // slug plus trailing slash
	default:
		return "", false
	}
	return id, id != ""
}

// MatchRef reduces what a person types as a source reference to a source
// id: a bare number (1 to 12 ASCII digits, not zero; leading zeros removed;
// surrounding whitespace trimmed) or a URL matching one of the patterns
// (MatchURL). With no patterns only a bare number is accepted, which is the
// rule for a picked search result.
func MatchRef(patterns []string, raw string) (id string, ok bool) {
	s := strings.TrimSpace(raw)
	if id, ok := normalizeID(s); ok {
		return id, true
	}
	if s != "" && strings.Trim(s, "0123456789") == "" {
		return "", false // all digits but too long or zero
	}
	return MatchURL(patterns, s)
}

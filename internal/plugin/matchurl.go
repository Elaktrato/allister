package plugin

import (
	"errors"
	"strings"
	"unicode"
)

// Declarative source URL matching. A source declares URL patterns as data
// (SourceInfo.URLPatterns, for example "example.org/item/{id}") and the core
// matches what a person pastes against them, so no plugin code parses URLs.

// maxIDDigits is the longest an all-digit id matches; maxIDBytes bounds an
// id of any kind.
const (
	maxIDDigits = 12
	maxIDBytes  = 64
)

// normalizeID accepts a source id: 1 to maxIDDigits ASCII digits (leading
// zeros stripped, a value of zero rejected) or, for sources whose ids are
// not numbers, 1 to maxIDBytes bytes of ASCII letters, digits, '_' and '-'
// that starts and ends with an ASCII alphanumeric.
func normalizeID(s string) (string, bool) {
	if s == "" || len(s) > maxIDBytes {
		return "", false
	}
	digits := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_', c == '-':
			digits = false
		default:
			return "", false
		}
	}
	if digits {
		if len(s) > maxIDDigits {
			return "", false
		}
		s = strings.TrimLeft(s, "0")
		return s, s != ""
	}
	if alnum(s[0]) && alnum(s[len(s)-1]) {
		return s, true
	}
	return "", false
}

// alnum reports whether c is an ASCII alphanumeric.
func alnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
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
//     {id} being a source id as normalizeID accepts it: 1 to 12 digits with
//     leading zeros removed, or 1 to 64 bytes of letters, digits, '_' and
//     '-'), optionally followed by one slug segment and/or a trailing
//     slash, then an optional query and fragment.
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

// checkURLPattern applies the pattern rules MatchURL relies on: no scheme,
// a non-empty host without port, userinfo or placeholder, then path segments
// that are non-empty literals plus exactly one whole "{id}" segment, and no
// query, fragment, whitespace, control character or backslash. Validate
// refuses manifests with a pattern that breaks them.
func checkURLPattern(p string) error {
	if strings.Contains(p, "://") || strings.HasPrefix(p, "//") {
		return errors.New("must not contain a scheme (write host/path/{id})")
	}
	for _, r := range p {
		if r <= ' ' || r == 0x7f || r == '\\' || unicode.IsSpace(r) || unicode.IsControl(r) {
			return errors.New("must not contain a space, control character or backslash")
		}
	}
	if strings.ContainsAny(p, "?#") {
		return errors.New("must not contain a query or fragment")
	}
	host, path, _ := strings.Cut(p, "/")
	if host == "" || strings.ContainsAny(host, ":@{}") {
		return errors.New("needs a host without port, userinfo or placeholder")
	}
	ids := 0
	for _, seg := range strings.Split(path, "/") {
		switch {
		case seg == "{id}":
			ids++
		case seg == "":
			return errors.New("has an empty path segment")
		case strings.ContainsAny(seg, "{}"):
			return errors.New(`"{id}" must be a whole path segment`)
		}
	}
	if ids != 1 {
		return errors.New(`must contain "{id}" exactly once as a path segment`)
	}
	return nil
}

func matchPattern(pattern, host string, segs []string) (string, bool) {
	if checkURLPattern(pattern) != nil {
		return "", false
	}
	ph, pp, _ := strings.Cut(pattern, "/")
	ph = strings.ToLower(ph)
	if strings.HasPrefix(ph, "www.") {
		ph = ph[len("www."):]
	}
	if ph != host {
		return "", false
	}
	psegs := strings.Split(pp, "/")
	if len(segs) < len(psegs) {
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
// id: a bare id (a number of 1 to 12 digits, leading zeros removed, not
// zero; or a non-numeric id as normalizeID accepts it) or a URL matching
// one of the patterns (MatchURL). Without patterns a bare id is still
// accepted, which is the rule for a picked search result.
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

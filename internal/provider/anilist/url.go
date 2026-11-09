package anilist

import (
	"net/url"
	"strings"
)

// ParseAnimeURL extracts the AniList anime id from what a person pastes: a
// bare number (1 to 9 digits, no leading zero) or a URL like
// https://anilist.co/anime/154587/Slug. The scheme is optional (http or https),
// the host must be anilist.co or www.anilist.co (case-insensitive, no port,
// no userinfo), the path must start with /anime/{id}; a slug, trailing slash,
// query and fragment are ignored. Anything else yields "", false.
func ParseAnimeURL(s string) (id string, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	if isAnimeID(s) {
		return s, true
	}
	for _, r := range s {
		if r <= ' ' || r == 0x7f || r == '\\' {
			return "", false
		}
	}
	lower := strings.ToLower(s)
	switch {
	case strings.HasPrefix(lower, "https://"), strings.HasPrefix(lower, "http://"):
	case strings.Contains(s, "://"), strings.HasPrefix(s, "//"):
		return "", false // other scheme or scheme-relative
	default:
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.User != nil || u.Port() != "" {
		return "", false
	}
	if h := strings.ToLower(u.Hostname()); h != "anilist.co" && h != "www.anilist.co" {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] != "anime" || !isAnimeID(parts[1]) {
		return "", false
	}
	return parts[1], true
}

// isAnimeID: 1 to 9 ASCII digits without a leading zero.
func isAnimeID(s string) bool {
	if len(s) == 0 || len(s) > 9 || s[0] == '0' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

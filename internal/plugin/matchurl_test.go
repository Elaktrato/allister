package plugin

import (
	"strings"
	"testing"
)

// The hostile-input table moved here from the legacy AniList URL parser
// (removed in Task 13: nothing used it). Differences to the legacy rules,
// decided with the move: ids are 1 to 12 digits (was 9) and leading zeros are
// normalised away (were rejected); "0" (also "000") is still rejected.
func TestURLPatternMatcher(t *testing.T) {
	pats := []string{"anilist.co/anime/{id}"}
	cases := []struct {
		in, id string
		ok     bool
	}{
		{"https://anilist.co/anime/154587", "154587", true},
		{"http://anilist.co/anime/154587", "154587", true},
		{"https://www.anilist.co/anime/154587", "154587", true},
		{"HTTPS://AniList.CO/anime/154587", "154587", true},
		{"anilist.co/anime/154587", "154587", true},
		{"www.anilist.co/anime/154587", "154587", true},
		{"  https://anilist.co/anime/154587  ", "154587", true},
		{"https://anilist.co/anime/154587/", "154587", true},
		{"https://anilist.co/anime/154587/Sousou-no-Frieren", "154587", true},
		{"https://anilist.co/anime/154587/Sousou-no-Frieren/", "154587", true},
		{"https://anilist.co/anime/154587?foo=bar", "154587", true},
		{"https://anilist.co/anime/154587#top", "154587", true},
		{"https://anilist.co/anime/154587/slug?x=1#y", "154587", true},
		{"https://anilist.co/anime/154587/?x=1", "154587", true},
		{"https://anilist.co/anime/1", "1", true},
		{"https://anilist.co/anime/123456789012", "123456789012", true}, // 12 digits
		{"https://anilist.co/anime/0154587", "154587", true},            // leading zeros normalised
		{"https://anilist.co/anime/007", "7", true},
		{"https://anilist.co/anime/5\n", "5", true}, // surrounding whitespace is trimmed

		{"", "", false},
		{"   ", "", false},
		{"154587", "", false}, // a bare number is MatchRef's job, not a URL
		{"https://anilist.co/anime/1234567890123", "", false}, // 13 digits
		{"https://anilist.co/anime/0", "", false},
		{"https://anilist.co/anime/000", "", false},
		{"https://anilist.co/anime/+5", "", false},
		{"https://anilist.co/anime/-5", "", false},
		{"https://anilist.co/anime/12.5", "", false},
		{"https://anilist.co/anime/%35", "", false},
		{"https://anilist.co/manga/1", "", false},
		{"anilist.co/manga/1", "", false},
		{"https://anilist.co/Anime/1", "", false},
		{"https://anilist.co/anime/", "", false},
		{"https://anilist.co/anime", "", false},
		{"anilist.co/anime/", "", false},
		{"https://anilist.co/anime/abc", "abc", true}, // non-numeric ids match too
		{"https://anilist.co/anime/12abc", "12abc", true},
		{"https://anilist.co/anime/5/slug/more", "", false},
		{"https://anilist.co/anime/5//", "", false},
		{"https://anilist.co/", "", false},
		{"https://anilist.co", "", false},
		{"https://anilist.co/user/anime/5", "", false},
		{"https://example.com/anime/5", "", false},
		{"https://anilist.co.evil.com/anime/5", "", false},
		{"https://evilanilist.co/anime/5", "", false},
		{"https://evil.com/anilist.co/anime/5", "", false},
		{"https://sub.anilist.co/anime/5", "", false},
		{"https://www.www.anilist.co/anime/5", "", false},
		{"https://anilist.co@evil.com/anime/5", "", false},
		{"https://user:pw@anilist.co/anime/5", "", false},
		{"https://anilist.co:8080/anime/5", "", false},
		{"https://anilist.co:443/anime/5", "", false},
		{"https://anilist.co./anime/5", "", false},
		{`https://anilist.co\@evil.com/anime/5`, "", false},
		{"https://anilist.co?x=/anime/5", "", false},
		{"https://anilist.co#/anime/5", "", false},
		{"https://anilist.co/anime/5\n/x", "", false},
		{"https://anilist.co/anime/5 /x", "", false},
		{"https://anilist.co/anime/5\x00", "", false},
		{"javascript:alert(1)", "", false},
		{"javascript://anilist.co/anime/5", "", false},
		{"ftp://anilist.co/anime/5", "", false},
		{"data:text/html,anilist.co/anime/5", "", false},
		{"//anilist.co/anime/5", "", false},
		{"garbage", "", false},
		{"https://", "", false},
		{"://", "", false},
		{"%", "", false},
	}
	for _, c := range cases {
		id, ok := MatchURL(pats, c.in)
		if id != c.id || ok != c.ok {
			t.Errorf("MatchURL(%q) = %q, %v; want %q, %v", c.in, id, ok, c.id, c.ok)
		}
	}
}

// TestMatchRefAcceptsNonNumericIds: sources whose ids are not numbers (an
// IMDb-style id, an OpenLibrary-style work key) match by a bare id or a
// pasted URL. All-digit ids keep their own rules (leading zeros stripped,
// at most 12 digits, zero rejected); an id is 1 to 64 bytes of ASCII
// letters, digits, '_' and '-' that starts and ends with an alphanumeric.
func TestMatchRefAcceptsNonNumericIds(t *testing.T) {
	pats := []string{"imdb.example/title/{id}", "books.example/work/{id}"}
	id64 := strings.Repeat("ab", 32)
	accept := map[string]string{
		"tt0944947":                    "tt0944947",
		"OL717W":                       "OL717W",
		"a-b_c":                        "a-b_c",
		"12abc":                        "12abc",
		"imdb.example/title/tt0944947": "tt0944947",
		"https://imdb.example/title/tt0944947/Slug": "tt0944947",
		"books.example/work/OL717W":                 "OL717W",
		id64:                                        id64,
	}
	for in, want := range accept {
		if id, ok := MatchRef(pats, in); !ok || id != want {
			t.Errorf("MatchRef(%q) = %q, %v; want %q", in, id, ok, want)
		}
	}
	for in := range map[string]bool{
		"-abc": true, "abc-": true, "_abc": true, "abc_": true,
		"a b": true, "a/b": true, "a%20b": true, "a?b": true, "a#b": true, `a\b`: true,
		"a.b": true, "12.5": true, "títulos": true, "１２": true,
		strings.Repeat("a", 65): true,
		"1234567890123":         true, // 13 digits keep the digit cap
	} {
		if id, ok := MatchRef(pats, in); ok {
			t.Errorf("MatchRef(%q) = %q, %v; want rejected", in, id, ok)
		}
	}
	// a bare non-numeric id is accepted without patterns too (the source's
	// Details verifies it; the paste field is not a search)
	if id, ok := MatchRef(nil, "tt0944947"); !ok || id != "tt0944947" {
		t.Errorf("bare id without patterns = %q, %v", id, ok)
	}
}

func TestMatchURLSeveralPatternsAndBadPatterns(t *testing.T) {
	pats := []string{"", "no-placeholder.example/x", "{id}", "books.example/work/{id}", "www.tv.example/show/{id}/info"}
	for in, want := range map[string]string{
		"https://books.example/work/42":         "42",
		"books.example/work/42/Title":           "42",
		"https://tv.example/show/9/info":        "9", // a pattern's www. is optional too
		"https://www.tv.example/show/9/info/":   "9",
		"https://tv.example/show/9/info/x":      "9",
		"https://no-placeholder.example/x":      "",
		"https://tv.example/show/9":             "",
		"https://tv.example/show/9/other":       "",
		"https://books.example/work/42/a/b":     "",
		"https://BOOKS.example/work/42?x#y":     "42",
		"https://books.example.evil/work/42":    "",
		"https://evil.example/books.example/42": "",
	} {
		id, ok := MatchURL(pats, in)
		if id != want || ok != (want != "") {
			t.Errorf("MatchURL(%q) = %q, %v; want %q", in, id, ok, want)
		}
	}
	if id, ok := MatchURL(nil, "https://anilist.co/anime/5"); ok || id != "" {
		t.Fatal("no patterns matched something")
	}
}

func TestMatchRefAcceptsBareNumbersAndURLs(t *testing.T) {
	pats := []string{"anilist.co/anime/{id}"}
	for in, want := range map[string]string{
		"154587":                          "154587",
		" 154587 ":                        "154587",
		"1":                               "1",
		"0154587":                         "154587",
		"123456789012":                    "123456789012",
		"https://anilist.co/anime/5/Slug": "5",
		"1234567890123":                   "",
		"0":                               "",
		"00":                              "",
		"+5":                              "",
		"-5":                              "",
		"12.5":                            "",
		"1 2":                             "",
		"１２":                              "", // full-width digits are not ASCII
		"12abc":                           "12abc",
		"":                                "",
		"https://anilist.co/manga/5":      "",
	} {
		id, ok := MatchRef(pats, in)
		if id != want || ok != (want != "") {
			t.Errorf("MatchRef(%q) = %q, %v; want %q", in, id, ok, want)
		}
	}
	// without patterns only a bare number is accepted (a search pick)
	if id, ok := MatchRef(nil, "42"); !ok || id != "42" {
		t.Fatalf("pick = %q %v", id, ok)
	}
	if _, ok := MatchRef(nil, "https://anilist.co/anime/5"); ok {
		t.Fatal("url accepted without patterns")
	}
}

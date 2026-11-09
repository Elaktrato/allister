package anilist

import "testing"

func TestParseAnimeURL(t *testing.T) {
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
		{"154587", "154587", true},
		{" 154587 ", "154587", true},
		{"1", "1", true},
		{"123456789", "123456789", true},

		{"", "", false},
		{"   ", "", false},
		{"1234567890", "", false}, // 10 digits
		{"0", "", false},
		{"0154587", "", false}, // leading zero rejected
		{"+5", "", false},
		{"-5", "", false},
		{"12.5", "", false},
		{"1 2", "", false},
		{"https://anilist.co/anime/0154587", "", false},
		{"https://anilist.co/anime/+5", "", false},
		{"https://anilist.co/manga/1", "", false},
		{"anilist.co/manga/1", "", false},
		{"https://anilist.co/anime/", "", false},
		{"https://anilist.co/anime", "", false},
		{"anilist.co/anime/", "", false},
		{"https://anilist.co/anime/abc", "", false},
		{"https://anilist.co/anime/12abc", "", false},
		{"https://anilist.co/", "", false},
		{"https://anilist.co", "", false},
		{"https://anilist.co/user/anime/5", "", false},
		{"https://example.com/anime/5", "", false},
		{"https://anilist.co.evil.com/anime/5", "", false},
		{"https://evilanilist.co/anime/5", "", false},
		{"https://evil.com/anilist.co/anime/5", "", false},
		{"https://sub.anilist.co/anime/5", "", false},
		{"https://anilist.co@evil.com/anime/5", "", false},
		{"https://user:pw@anilist.co/anime/5", "", false},
		{"https://anilist.co:8080/anime/5", "", false},
		{`https://anilist.co\@evil.com/anime/5`, "", false},
		{"https://anilist.co/anime/5\n", "5", true}, // surrounding whitespace is trimmed
		{"https://anilist.co/anime/5\n/x", "", false},
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
		id, ok := ParseAnimeURL(c.in)
		if id != c.id || ok != c.ok {
			t.Errorf("ParseAnimeURL(%q) = %q, %v; want %q, %v", c.in, id, ok, c.id, c.ok)
		}
	}
}

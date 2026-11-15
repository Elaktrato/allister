package importer

import "testing"

func TestNormaliseTitle(t *testing.T) {
	for _, c := range []struct {
		a, b string
		same bool
	}{
		{"Spider-Man: Into the Spider-Verse", "spider man into the spider verse", true},
		{"Fast & Furious", "Fast and Furious", true},
		{"Amélie", "Amelie", false},
		{"Ame\u0301lie", "Amelie", false}, // NFD: the combining accent is kept
		{"ก\u0e34น", "กน", false},         // a Thai vowel sign is kept
		{"Se7en", "se7en", true},
		{"Alien", "Aliens", false},
	} {
		if got := normaliseTitle(c.a) == normaliseTitle(c.b); got != c.same {
			t.Errorf("normaliseTitle(%q) == normaliseTitle(%q) is %v, want %v (%q, %q)",
				c.a, c.b, got, c.same, normaliseTitle(c.a), normaliseTitle(c.b))
		}
	}
	if got := normaliseTitle("  "); got != "" {
		t.Errorf("normaliseTitle(blank) = %q, want empty", got)
	}
}

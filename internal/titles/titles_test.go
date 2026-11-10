package titles

import (
	"reflect"
	"testing"

	"github.com/allister/allister/internal/plugins/anime/anilist"
)

func TestResolvePreferred(t *testing.T) {
	ts := []anilist.Title{{Lang: "ja", Text: "進撃の巨人"}, {Lang: "en", Text: "Attack on Titan"}}
	if got := Resolve(ts, "en"); got != "Attack on Titan" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveFallbackChain(t *testing.T) {
	cases := []struct {
		name string
		ts   []anilist.Title
		want string
	}{
		{"romaji before english", []anilist.Title{{Lang: "en", Text: "E"}, {Lang: "ja-latn", Text: "R"}}, "R"},
		{"native first", []anilist.Title{{Lang: "en", Text: "E"}, {Lang: "ja", Text: "N"}, {Lang: "ja-latn", Text: "R"}}, "N"},
		{"only english", []anilist.Title{{Lang: "en", Text: "E"}}, "E"},
		{"only french", []anilist.Title{{Lang: "fr", Text: "F"}}, "F"},
		{"first of others", []anilist.Title{{Lang: "fr", Text: "F"}, {Lang: "es", Text: "S"}}, "F"},
	}
	for _, c := range cases {
		if got := Resolve(c.ts, "de"); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestResolveEmpty(t *testing.T) {
	if got := Resolve(nil, "ja"); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestLanguages(t *testing.T) {
	ts := []anilist.Title{{Lang: "en", Text: "a"}, {Lang: "ja", Text: "b"}, {Lang: "en", Text: "c"}, {Lang: "de", Text: "d"}}
	want := []string{"en", "ja", "de"}
	if got := Languages(ts); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	if got := Languages(nil); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

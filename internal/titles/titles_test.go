package titles

import (
	"reflect"
	"testing"

	"github.com/allister/allister/internal/provider"
)

func TestResolvePreferred(t *testing.T) {
	ts := []provider.Title{{Lang: "ja", Text: "進撃の巨人"}, {Lang: "en", Text: "Attack on Titan"}}
	if got := Resolve(ts, "en"); got != "Attack on Titan" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveFallbackChain(t *testing.T) {
	cases := []struct {
		name string
		ts   []provider.Title
		want string
	}{
		{"romaji before english", []provider.Title{{Lang: "en", Text: "E"}, {Lang: "ja-latn", Text: "R"}}, "R"},
		{"native first", []provider.Title{{Lang: "en", Text: "E"}, {Lang: "ja", Text: "N"}, {Lang: "ja-latn", Text: "R"}}, "N"},
		{"only english", []provider.Title{{Lang: "en", Text: "E"}}, "E"},
		{"only french", []provider.Title{{Lang: "fr", Text: "F"}}, "F"},
		{"first of others", []provider.Title{{Lang: "fr", Text: "F"}, {Lang: "es", Text: "S"}}, "F"},
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
	ts := []provider.Title{{Lang: "en", Text: "a"}, {Lang: "ja", Text: "b"}, {Lang: "en", Text: "c"}, {Lang: "de", Text: "d"}}
	want := []string{"en", "ja", "de"}
	if got := Languages(ts); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	if got := Languages(nil); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

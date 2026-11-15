package plugin

import "testing"

func TestFillUnmatchedNote(t *testing.T) {
	for _, c := range []struct {
		note, id, title string
		year            int
		want            string
	}{
		{`No match for #{id}.`, "42", "", 0, `No match for #42.`},
		{`No unique match for "{title}" ({year}), id {id}.`, "/f/x/", "Heat", 1995, `No unique match for "Heat" (1995), id /f/x/.`},
		{`"{title}" ({year})`, "", "Heat", 0, `"Heat" ()`},
		{`{title}`, "", "{year}", 2000, `{year}`}, // a title is not filled again
		{"", "1", "T", 1, ""},
	} {
		if got := FillUnmatchedNote(c.note, c.id, c.title, c.year); got != c.want {
			t.Errorf("FillUnmatchedNote(%q, %q, %q, %d) = %q, want %q", c.note, c.id, c.title, c.year, got, c.want)
		}
	}
}

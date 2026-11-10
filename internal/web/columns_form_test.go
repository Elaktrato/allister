package web

import (
	"strings"
	"testing"
)

// plusNote is the progress row's note as html/template writes it ("+" is
// escaped in HTML text).
const plusNote = "includes the &#43;1 button"

// TestColumnFormDetails: the progress row notes the +1 button (core text,
// keyed on the core column), the title checkbox has an accessible name and
// the column checkboxes are a fieldset with a legend.
func TestColumnFormDetails(t *testing.T) {
	e := newFixtureEnv(t, bookColumns)
	f := columnForm(t, e.get("/settings/plugins/anime").Body.String(), "anime")
	mustContain(t, f, `type="checkbox" checked disabled aria-label="Title"`, "<fieldset", "<legend")
	rows := strings.Split(f, "<li")
	notes := 0
	for _, r := range rows {
		if strings.Contains(r, plusNote) {
			notes++
			mustContain(t, r, `value="progress"`)
		}
	}
	if notes != 1 {
		t.Errorf("%d +1 notes", notes)
	}
	// the book fixture offers no progress column: no note
	mustNotContain(t, columnForm(t, e.get("/settings/plugins/book").Body.String(), "book"), plusNote)
}

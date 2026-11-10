package web

import (
	"context"
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/allister/allister/internal/appearance"
)

// stylesheetLink returns the version of the /appearance.css link of a full
// page, failing when there is none or more than one.
func stylesheetLink(t *testing.T, body string) string {
	t.Helper()
	m := regexp.MustCompile(`<link rel="stylesheet" href="/appearance\.css\?v=([0-9a-f]{16})">`).FindAllStringSubmatch(body, -1)
	if len(m) != 1 {
		t.Fatalf("want one versioned /appearance.css link, found %d in %s", len(m), body)
	}
	return m[0][1]
}

// TestAppearanceStylesheetServed: /appearance.css answers the generated CSS
// as text/css with nosniff; the current version is cached immutably, any
// other (or none) must be revalidated.
func TestAppearanceStylesheetServed(t *testing.T) {
	e := newEnv(t)
	css, v := e.srv.appearance.Stylesheet(context.Background())
	rec := e.get("/appearance.css?v=" + v)
	if rec.Code != 200 || rec.Body.String() != string(css) {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
	h := rec.Header()
	if h.Get("Content-Type") != "text/css; charset=utf-8" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("headers %v", h)
	}
	if h.Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Errorf("current version: Cache-Control %q", h.Get("Cache-Control"))
	}
	for _, target := range []string{"/appearance.css", "/appearance.css?v=0000000000000000", "/appearance.css?v=" + v + "x"} {
		rec := e.get(target)
		if rec.Code != 200 || rec.Body.String() != string(css) || rec.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: %d %q", target, rec.Code, rec.Header().Get("Cache-Control"))
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: no nosniff", target)
		}
	}
	if rec := e.do("POST", "/appearance.css", nil, false); rec.Code != 405 {
		t.Errorf("POST: %d", rec.Code)
	}
}

// TestEveryPageLinksTheVersionedStylesheet: every full page (list,
// settings, an error page) links /appearance.css with the current version,
// render-blocking in the head after app.css; a settings change changes the
// version on the next page.
func TestEveryPageLinksTheVersionedStylesheet(t *testing.T) {
	e := newEnv(t)
	_, v := e.srv.appearance.Stylesheet(context.Background())
	for _, target := range []string{"/anime", "/settings", "/settings/backup", "/settings/plugins", "/people/999"} {
		rec := e.get(target)
		body := rec.Body.String()
		if got := stylesheetLink(t, body); got != v {
			t.Errorf("%s (%d): version %s, want %s", target, rec.Code, got, v)
		}
		head := body[:strings.Index(body, "</head>")]
		if i, j := strings.Index(head, `href="/static/app.css"`), strings.Index(head, `href="/appearance.css`); i < 0 || j < i {
			t.Errorf("%s: /appearance.css must follow app.css in the head", target)
		}
	}
	if rec := e.get("/people/999"); rec.Code != 404 {
		t.Fatalf("error page status %d", rec.Code)
	}
	if err := e.srv.appearance.SetPreset(context.Background(), "paper"); err != nil {
		t.Fatal(err)
	}
	if got := stylesheetLink(t, e.get("/settings").Body.String()); got == v {
		t.Error("the version did not change after a settings change")
	}
}

// colourClass finds Tailwind colour utilities naming a palette colour.
var colourClass = regexp.MustCompile(`(?:^|[\s"':{}])((?:bg|text|border|divide|ring-offset|ring|accent|placeholder|from|via|to|outline|decoration|caret|fill|stroke|shadow)-(?:white|black|(?:slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose)-(?:50|[1-9]00|950)))(?:/\d+)?\b`)

// utilityTheme maps a colour utility prefix to the Tailwind theme key the
// map configures (divide colours follow borderColor).
var utilityTheme = map[string]string{"bg": "backgroundColor", "text": "textColor", "border": "borderColor", "divide": "borderColor", "ring": "ringColor", "accent": "accentColor"}

// TestTemplatesUseOnlyThemedColours: every colour class in a template is
// mapped to a theme token (internal/appearance/tailwind-map.json, read by
// tailwind.config.js), so it follows the appearance settings; an unmapped
// class would generate no CSS at all. No template carries an arbitrary
// colour or an inline style (one pre-existing width is allowed).
func TestTemplatesUseOnlyThemedColours(t *testing.T) {
	m, err := appearance.LoadTailwindMap("../appearance/tailwind-map.json")
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	err = fs.WalkDir(templateFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := templateFS.ReadFile(path)
		if err != nil {
			return err
		}
		body := string(b)
		for _, sm := range colourClass.FindAllStringSubmatch(body, -1) {
			found++
			class := sm[1]
			var util, colour string
			for p := range utilityTheme {
				if c, ok := strings.CutPrefix(class, p+"-"); ok && (util == "" || len(p) > len(util)) {
					util, colour = p, c
				}
			}
			if util == "" {
				t.Errorf("%s: %s is a colour utility the theme does not map (use bg, text, border, divide, ring or accent)", path, class)
				continue
			}
			if _, ok := m[utilityTheme[util]][colour]; !ok {
				t.Errorf("%s: %s is not mapped to a token in tailwind-map.json (%s)", path, class, utilityTheme[util])
			}
		}
		if regexp.MustCompile(`-\[(#|rgb|hsl|color)`).MatchString(body) {
			t.Errorf("%s: arbitrary colour value", path)
		}
		for _, st := range regexp.MustCompile(`\sstyle="[^"]*"`).FindAllString(body, -1) {
			// pre-existing: the import progress bar's width (no colour)
			if path == "templates/import.html" && st == ` style="width: {{.Percent}}%"` {
				continue
			}
			t.Errorf("%s: inline style attribute%s", path, st)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	css, err := staticFS.ReadFile("static/input.css")
	if err != nil {
		t.Fatal(err)
	}
	if lit := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b|rgba?\(\s*\d|hsla?\(`).FindAllString(string(css), -1); len(lit) > 0 {
		t.Errorf("input.css names fixed colours %v: use rgb(var(--al-<token>))", lit)
	}
	if found < 100 {
		t.Fatalf("only %d colour classes found: the pattern is broken", found)
	}
	// the pattern catches what it should
	for _, c := range []string{`class="bg-slate-950"`, `class="x hover:text-white"`, `class="from-rose-500"`, `{{if .A}}text-gray-50{{end}}`} {
		if !colourClass.MatchString(c) {
			t.Errorf("pattern misses %s", c)
		}
	}
	for _, c := range []string{`class="text-sm"`, `class="text-[1rem]"`, `class="border-b"`, `class="ring-2"`} {
		if colourClass.MatchString(c) {
			t.Errorf("pattern matches %s", c)
		}
	}
}

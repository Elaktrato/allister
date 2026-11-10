package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/allister/allister/internal/appearance"
)

func (e *env) setting(t *testing.T, key string) string {
	t.Helper()
	v, _, err := e.set.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// paletteForm is the editor's form for p: every token's colour picker,
// text field and the value it was rendered with.
func paletteForm(p appearance.Palette) url.Values {
	f := url.Values{}
	for _, tk := range appearance.Tokens {
		f.Set("color."+tk.Name, p[tk.Name])
		f.Set("hex."+tk.Name, p[tk.Name])
		f.Set("was."+tk.Name, p[tk.Name])
	}
	return f
}

func midnight() appearance.Palette {
	p, _ := appearance.PresetByID(appearance.DefaultPreset)
	return p.Colors
}

func TestAppearancePageRenders(t *testing.T) {
	e := newEnv(t)
	rec := e.get("/settings/appearance")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	mustContain(t, body, `<h1 class="mb-2 text-2xl font-semibold">Appearance</h1>`,
		`href="/settings/appearance" aria-current="true"`,
		`<form method="post" action="/settings/appearance/preset"`, `<select id="preset" name="preset"`,
		`<form method="post" action="/settings/appearance/palette"`,
		`<form method="post" action="/settings/appearance/palette/reset"`,
		`<form method="post" action="/settings/appearance/font"`)
	for _, p := range appearance.PresetChoices() {
		mustContain(t, body, `<option value="`+p.ID+`"`)
	}
	mustContain(t, body, `<option value="midnight" selected>`)
	for _, f := range appearance.Fonts(nil) {
		mustContain(t, body, `id="font-`+f.ID+`" name="font" value="`+f.ID+`"`, `class="al-font-`+f.ID)
	}
	mustContain(t, body, `id="font-system-sans" name="font" value="system-sans" checked`)
	for _, s := range appearance.Scales {
		mustContain(t, body, `id="scale-`+s.ID+`" name="scale" value="`+s.ID+`"`)
	}
	mustContain(t, body, `id="scale-default" name="scale" value="default" checked`)
	for _, tk := range appearance.Tokens {
		v := midnight()[tk.Name]
		mustContain(t, body,
			`<input type="color" id="color-`+tk.Name+`" name="color.`+tk.Name+`" value="`+v+`"`,
			`<label for="color-`+tk.Name+`"`,
			`id="hex-`+tk.Name+`" name="hex.`+tk.Name+`" value="`+v+`"`,
			`<input type="hidden" name="was.`+tk.Name+`" value="`+v+`">`)
	}
	// the default preset is readable: no contrast warning
	mustNotContain(t, body, "below 4.5:1")

	// General links to Appearance; the tab column lists it
	gen := e.get("/settings").Body.String()
	mustContain(t, gen, `href="/settings/appearance"`)
	mustContain(t, e.get("/settings/backup").Body.String(), `href="/settings/appearance"`)
}

func TestAppearancePresetApply(t *testing.T) {
	e := newEnv(t)
	_, v0 := e.srv.appearance.Stylesheet(context.Background())
	rec := e.do("POST", "/settings/appearance/preset", url.Values{"preset": {"forest"}}, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings/appearance" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if e.setting(t, appearance.KeyPreset) != "forest" {
		t.Fatal("preset not stored")
	}
	if got := stylesheetLink(t, e.get("/settings/appearance").Body.String()); got == v0 {
		t.Error("stylesheet version unchanged")
	}
	mustContain(t, e.get("/settings/appearance").Body.String(), `<option value="forest" selected>`)
	if rec := e.do("POST", "/settings/appearance/preset", url.Values{"preset": {"system"}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("system: %d", rec.Code)
	}
	for _, bad := range []string{"", "nope", "forest;}body{display:none", "</style>"} {
		rec := e.do("POST", "/settings/appearance/preset", url.Values{"preset": {bad}}, false)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%q: %d", bad, rec.Code)
		}
		mustContain(t, rec.Body.String(), "There is no colour preset")
		mustNotContain(t, rec.Body.String(), "</style>\n", "display:none<")
	}
	if e.setting(t, appearance.KeyPreset) != "system" {
		t.Error("a refused preset was stored")
	}
}

// TestAppearanceRefusesCrossOrigin: every appearance form is behind the
// same-origin check.
func TestAppearanceRefusesCrossOrigin(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{"/settings/appearance/preset", "/settings/appearance/palette", "/settings/appearance/palette/reset", "/settings/appearance/font"} {
		form := url.Values{"preset": {"paper"}, "font": {"system-serif"}, "scale": {"large"}}
		for k, v := range paletteForm(midnight()) {
			form[k] = v
		}
		req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	for _, k := range []string{appearance.KeyPreset, appearance.KeyPalette, appearance.KeyFont, appearance.KeyScale} {
		if v := e.setting(t, k); v != "" {
			t.Errorf("%s stored %q", k, v)
		}
	}
}

func TestAppearancePaletteSave(t *testing.T) {
	e := newEnv(t)
	f := paletteForm(midnight())
	f.Set("hex.accent", "#ABC")      // typed into the text field: it wins
	f.Set("color.accent", "#000000") // the untouched picker would say otherwise
	f.Set("color.link", "#123456")   // picked; its text field is unchanged
	rec := e.do("POST", "/settings/appearance/palette", f, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings/appearance" {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	p, err := appearance.DecodePalette(e.setting(t, appearance.KeyPalette))
	if err != nil {
		t.Fatal(err)
	}
	if p["accent"] != "#aabbcc" || p["link"] != "#123456" || p["bg"] != "#020617" {
		t.Fatalf("stored %v", p)
	}
	css, _ := e.srv.appearance.Stylesheet(context.Background())
	mustContain(t, string(css), "--al-accent: 170 187 204;")
	page := e.get("/settings/appearance").Body.String()
	mustContain(t, page, `name="color.accent" value="#aabbcc"`, "Custom colours")
	// #123456 links on the dark background: warned (not blocked)
	mustContain(t, page, "Links and accent text on Page background: contrast", "below 4.5:1")
}

func TestAppearancePaletteRefused(t *testing.T) {
	cases := map[string]func(f url.Values){
		"bad hex":       func(f url.Values) { f.Set("hex.bg", "#12") },
		"injection":     func(f url.Values) { f.Set("hex.bg", "red;}body{display:none") },
		"url":           func(f url.Values) { f.Set("hex.text", "url(https://evil.example/)") },
		"style end":     func(f url.Values) { f.Set("hex.link", "</style><script>") },
		"bad picker":    func(f url.Values) { f.Set("color.focus", "blue") },
		"unknown token": func(f url.Values) { f.Set("color.shadow", "#000000") },
		"unknown hex":   func(f url.Values) { f.Set("hex.x;}", "#000000") },
		"missing token": func(f url.Values) { f.Del("color.bg"); f.Del("hex.bg"); f.Del("was.bg") },
		"other field":   func(f url.Values) { f.Set("bogus", "1") },
	}
	for name, mutate := range cases {
		e := newEnv(t)
		f := paletteForm(midnight())
		mutate(f)
		rec := e.do("POST", "/settings/appearance/palette", f, false)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d", name, rec.Code)
			continue
		}
		body := rec.Body.String()
		mustContain(t, body, `role="alert"`)
		mustNotContain(t, body, "<script>", "display:none}")
		if v := e.setting(t, appearance.KeyPalette); v != "" {
			t.Errorf("%s: stored %q", name, v)
		}
	}
	// an oversized form is 413
	e := newEnv(t)
	big := paletteForm(midnight())
	big.Set("hex.bg", strings.Repeat("a", 70<<10))
	if rec := e.do("POST", "/settings/appearance/palette", big, false); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized: %d", rec.Code)
	}
}

func TestAppearancePaletteReset(t *testing.T) {
	e := newEnv(t)
	f := paletteForm(midnight())
	f.Set("hex.bg", "#101010")
	if rec := e.do("POST", "/settings/appearance/palette", f, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("save %d", rec.Code)
	}
	rec := e.do("POST", "/settings/appearance/palette/reset", url.Values{}, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings/appearance" {
		t.Fatalf("reset %d", rec.Code)
	}
	if v := e.setting(t, appearance.KeyPalette); v != "" {
		t.Errorf("palette kept: %q", v)
	}
	css, _ := e.srv.appearance.Stylesheet(context.Background())
	mustContain(t, string(css), "--al-bg: 2 6 23;")
}

func TestAppearanceFontAndScale(t *testing.T) {
	e := newEnv(t)
	rec := e.do("POST", "/settings/appearance/font", url.Values{"font": {"system-serif"}, "scale": {"large"}}, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings/appearance" {
		t.Fatalf("got %d", rec.Code)
	}
	if e.setting(t, appearance.KeyFont) != "system-serif" || e.setting(t, appearance.KeyScale) != "large" {
		t.Fatal("not stored")
	}
	css, _ := e.srv.appearance.Stylesheet(context.Background())
	mustContain(t, string(css), "--al-root-size: 112.5%;", "--al-font: ui-serif")
	page := e.get("/settings/appearance").Body.String()
	mustContain(t, page, `value="system-serif" checked`, `value="large" checked`)
	for name, f := range map[string]url.Values{
		"font":      {"font": {"comic"}, "scale": {"large"}},
		"scale":     {"font": {"system-sans"}, "scale": {"huge"}},
		"no font":   {"scale": {"small"}},
		"injection": {"font": {`x";}*{`}, "scale": {"small"}},
		"upload":    {"font": {"upload-1"}, "scale": {"small"}},
	} {
		rec := e.do("POST", "/settings/appearance/font", f, false)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if e.setting(t, appearance.KeyFont) != "system-serif" || e.setting(t, appearance.KeyScale) != "large" {
		t.Error("a refused choice was stored")
	}
}

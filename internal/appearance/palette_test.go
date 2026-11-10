package appearance

import (
	"encoding/json"
	"math"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestContrastMatchesWCAG checks the ratio against known WCAG values.
func TestContrastMatchesWCAG(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want float64
	}{
		{"#000000", "#ffffff", 21},
		{"#ffffff", "#ffffff", 1},
		{"#ffffff", "#4f46e5", 6.29},
		{"#94a3b8", "#020617", 7.87},
		{"#767676", "#ffffff", 4.54},
	} {
		got := Contrast(c.a, c.b)
		if got < c.want-0.01 || got > c.want+0.01 {
			t.Errorf("Contrast(%s, %s) = %.3f, want %.2f", c.a, c.b, got, c.want)
		}
		if back := Contrast(c.b, c.a); back != got {
			t.Errorf("Contrast is not symmetric for %s/%s", c.a, c.b)
		}
	}
}

// TestPresetsAreComplete: every preset defines exactly the tokens, with
// normalised hex values, a known scheme, a unique id and a name.
func TestPresetsAreComplete(t *testing.T) {
	if len(Presets) < 7 {
		t.Fatalf("%d presets, want at least 7", len(Presets))
	}
	hex := regexp.MustCompile(`^#[0-9a-f]{6}$`)
	ids := map[string]bool{}
	for _, p := range Presets {
		if p.ID == "" || p.Name == "" || ids[p.ID] || p.ID == SystemPreset {
			t.Errorf("preset %+v: empty, duplicate or reserved id/name", p.ID)
		}
		ids[p.ID] = true
		if p.Scheme != "dark" && p.Scheme != "light" {
			t.Errorf("%s: scheme %q", p.ID, p.Scheme)
		}
		if got := SchemeOf(p.Colors); got != p.Scheme {
			t.Errorf("%s: declared %s, background reads as %s", p.ID, p.Scheme, got)
		}
		if len(p.Colors) != len(Tokens) {
			t.Errorf("%s: %d colours, want %d", p.ID, len(p.Colors), len(Tokens))
		}
		for _, tok := range Tokens {
			if v := p.Colors[tok.Name]; !hex.MatchString(v) {
				t.Errorf("%s: %s = %q", p.ID, tok.Name, v)
			}
		}
	}
	for _, id := range []string{DefaultPreset, SystemDark, SystemLight} {
		if _, ok := PresetByID(id); !ok {
			t.Errorf("preset %q missing", id)
		}
	}
	if d, _ := PresetByID(SystemDark); d.Scheme != "dark" {
		t.Error("SystemDark is not dark")
	}
	if l, _ := PresetByID(SystemLight); l.Scheme != "light" {
		t.Error("SystemLight is not light")
	}
	light, dark := 0, 0
	for _, p := range Presets {
		if p.Scheme == "light" {
			light++
		} else {
			dark++
		}
	}
	if light < 2 || dark < 4 {
		t.Errorf("%d light and %d dark presets, want at least 2 and 4", light, dark)
	}
	if _, ok := PresetByID(SystemPreset); ok {
		t.Error("PresetByID resolves the system pseudo preset")
	}
	if !ValidPreset(SystemPreset) || !ValidPreset(DefaultPreset) || ValidPreset("nope") || ValidPreset("") {
		t.Error("ValidPreset")
	}
}

// TestDefaultPresetIsTodaysLook: the default preset keeps the colours the
// templates had before theming (Tailwind's slate/indigo/amber shades), so
// the upgrade does not change the look. The four merged shades are listed
// in palette.go (Tokens).
func TestDefaultPresetIsTodaysLook(t *testing.T) {
	p, _ := PresetByID(DefaultPreset)
	want := map[string]string{
		"bg": "#020617", "surface": "#0f172a", "raised": "#1e293b", "control": "#334155", "control-hover": "#475569", "overlay": "#000000",
		"text-strong": "#ffffff", "text": "#f1f5f9", "text-secondary": "#cbd5e1", "text-muted": "#94a3b8", "text-faint": "#64748b",
		"border": "#334155", "border-muted": "#1e293b", "border-strong": "#475569",
		"accent": "#4f46e5", "accent-hover": "#6366f1", "accent-text": "#ffffff", "link": "#a5b4fc", "accent-icon": "#818cf8", "focus": "#6366f1",
		"warning": "#fcd34d", "warning-bg": "#451a03", "warning-border": "#b45309", "warning-text": "#fde68a",
		"success": "#34d399", "info": "#7dd3fc", "danger": "#fca5a5",
	}
	for k, v := range want {
		if p.Colors[k] != v {
			t.Errorf("default %s = %s, want %s", k, p.Colors[k], v)
		}
	}
	if len(want) != len(Tokens) {
		t.Errorf("the test lists %d tokens, there are %d", len(want), len(Tokens))
	}
}

// midnightShortfalls are the pairs where the default preset, kept identical
// to the look before theming, stays below the minimum: pinned to their exact
// ratios so they can never silently get worse.
var midnightShortfalls = map[[2]string]float64{
	{"text-faint", "bg"}: 4.24, {"text-faint", "surface"}: 3.75,
	{"border", "bg"}: 1.95, {"border", "surface"}: 1.72,
}

// TestPresetsAreReadable: every shipped preset meets each pair's minimum
// contrast (4.5:1 for text, 3:1 for the documented non-text and hover
// pairs), the high contrast preset 7:1 (WCAG AAA) for every text pair, and
// only Midnight has shortfalls: exactly the pinned ones, through Shortfalls.
func TestPresetsAreReadable(t *testing.T) {
	if len(Pairs) < 15 {
		t.Fatalf("only %d checked pairs", len(Pairs))
	}
	has := map[[2]string]bool{}
	for _, pr := range Pairs {
		has[[2]string{pr.Fg, pr.Bg}] = true
		if pr.Min != 4.5 && pr.Reason == "" {
			t.Errorf("pair %s/%s: a minimum below 4.5 needs a documented reason", pr.Fg, pr.Bg)
		}
		if !slices.ContainsFunc(Tokens, func(tk Token) bool { return tk.Name == pr.Fg }) || !slices.ContainsFunc(Tokens, func(tk Token) bool { return tk.Name == pr.Bg }) {
			t.Errorf("pair %s/%s names an unknown token", pr.Fg, pr.Bg)
		}
	}
	for _, want := range [][2]string{{"text-faint", "bg"}, {"text-faint", "surface"}, {"text-muted", "raised"}, {"text-secondary", "raised"}, {"border", "bg"}, {"border", "surface"}} {
		if !has[want] {
			t.Errorf("pair %s/%s is not checked", want[0], want[1])
		}
	}
	for _, pr := range Pairs {
		if pr.Fg == "text-faint" && pr.Min != 4.5 {
			t.Errorf("text-faint carries real labels: %s/%s needs 4.5:1, not %.1f", pr.Fg, pr.Bg, pr.Min)
		}
	}
	for _, p := range Presets {
		for _, pr := range Pairs {
			min := pr.Min
			if p.ID == "contrast" && pr.Min == 4.5 {
				min = 7
			}
			c := Contrast(p.Colors[pr.Fg], p.Colors[pr.Bg])
			if want, ok := midnightShortfalls[[2]string{pr.Fg, pr.Bg}]; ok && p.ID == DefaultPreset {
				if got := math.Round(c*100) / 100; got != want {
					t.Errorf("midnight %s/%s = %.2f:1, pinned %.2f", pr.Fg, pr.Bg, got, want)
				}
				continue
			}
			if c < min {
				t.Errorf("%s: %s %s on %s %s = %.2f:1, want >= %.1f", p.ID, pr.Fg, p.Colors[pr.Fg], pr.Bg, p.Colors[pr.Bg], c, min)
			}
		}
		if w := Warnings(p.Colors); len(w) != 0 {
			t.Errorf("%s: warnings %v", p.ID, w)
		}
	}
	if len(Shortfalls) != len(midnightShortfalls) {
		t.Errorf("%d shortfalls, %d pinned", len(Shortfalls), len(midnightShortfalls))
	}
	def, _ := PresetByID(DefaultPreset)
	for _, sf := range Shortfalls {
		if _, ok := midnightShortfalls[[2]string{sf.Fg, sf.Bg}]; !ok || def.Colors[sf.Fg] != sf.FgColor || def.Colors[sf.Bg] != sf.BgColor {
			t.Errorf("shortfall %+v is not one of Midnight's pinned pairs", sf)
		}
	}
	// a palette that changes one colour of a shortfall pair is warned
	m := full()
	m["text-faint"] = "#5a6578"
	if !slices.ContainsFunc(Warnings(m), func(w Warning) bool { return w.Fg == "text-faint" && w.Bg == "bg" }) {
		t.Error("an edited faint colour below 4.5:1 is not warned")
	}
}

func TestNormalizeHex(t *testing.T) {
	for in, want := range map[string]string{
		"#a1b2c3": "#a1b2c3", "#A1B2C3": "#a1b2c3", "#abc": "#aabbcc", "#ABC": "#aabbcc", " #abc ": "#aabbcc",
	} {
		got, err := NormalizeHex(in)
		if err != nil || got != want {
			t.Errorf("NormalizeHex(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "#", "abc", "a1b2c3", "#ab", "#abcd", "#a1b2c3d4", "#a1b2c", "#ggg", "#12345g",
		"red", "rgb(0,0,0)", "red;}body{display:none", "#000;}body{display:none}", "url(x)", "</style>", "#abc\n", "#abc;", "#ab\u0000c",
		"#ａｂｃ", "var(--x)", "#abc /*", "inherit"} {
		if got, err := NormalizeHex(bad); err == nil {
			t.Errorf("NormalizeHex(%q) accepted as %q", bad, got)
		}
	}
}

// full returns the default preset's colours as a form-like map.
func full() map[string]string {
	p, _ := PresetByID(DefaultPreset)
	m := map[string]string{}
	for k, v := range p.Colors {
		m[k] = v
	}
	return m
}

func TestParsePalette(t *testing.T) {
	in := full()
	in["accent"] = "#ABC"
	p, err := ParsePalette(in)
	if err != nil {
		t.Fatal(err)
	}
	if p["accent"] != "#aabbcc" || len(p) != len(Tokens) {
		t.Errorf("got %v", p)
	}

	cases := map[string]func(m map[string]string){
		"missing token":     func(m map[string]string) { delete(m, "bg") },
		"unknown token":     func(m map[string]string) { m["shadow"] = "#000000" },
		"injection":         func(m map[string]string) { m["bg"] = "red;}body{display:none" },
		"url":               func(m map[string]string) { m["text"] = "url(https://x.example/a.png)" },
		"style end":         func(m map[string]string) { m["link"] = "</style><script>" },
		"named colour":      func(m map[string]string) { m["focus"] = "red" },
		"token injection":   func(m map[string]string) { m["bg;}x{"] = "#000000" },
		"empty":             func(m map[string]string) { m["accent"] = "" },
		"control character": func(m map[string]string) { m["accent"] = "#000\r" },
	}
	for name, mutate := range cases {
		m := full()
		mutate(m)
		if p, err := ParsePalette(m); err == nil {
			t.Errorf("%s: accepted %v", name, p)
		}
	}
	if _, err := ParsePalette(nil); err == nil {
		t.Error("nil accepted")
	}
}

// TestPaletteJSONRoundTrip: the stored form is a JSON object of token to
// hex; DecodePalette re-validates it (an invalid stored value is refused,
// so the caller falls back to the preset).
func TestPaletteJSONRoundTrip(t *testing.T) {
	p, err := ParsePalette(full())
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodePalette(string(b))
	if err != nil || len(back) != len(Tokens) || back["bg"] != p["bg"] {
		t.Fatalf("round trip: %v %v", back, err)
	}
	for _, bad := range []string{"", "null", "[]", "{}", `{"bg":"red"}`, strings.Replace(string(b), `"#020617"`, `"#020617;}"`, 1), "not json"} {
		if _, err := DecodePalette(bad); err == nil {
			t.Errorf("DecodePalette(%q) accepted", bad)
		}
	}
}

// TestWarningsNameFailingPairs: a palette with poor contrast gets one
// warning per failing pair, naming both tokens and the ratio.
func TestWarningsNameFailingPairs(t *testing.T) {
	m := full()
	m["text-muted"] = "#1e293b"
	p, err := ParsePalette(m)
	if err != nil {
		t.Fatal(err)
	}
	ws := Warnings(p)
	if len(ws) == 0 {
		t.Fatal("no warning")
	}
	for _, w := range ws {
		if w.Fg != "text-muted" {
			t.Errorf("unexpected warning %+v", w)
		}
		if w.Ratio >= w.Min || !strings.Contains(w.Message(), ":1") {
			t.Errorf("warning %+v: %q", w, w.Message())
		}
	}
}

func TestSchemeOf(t *testing.T) {
	m := full()
	if SchemeOf(m) != "dark" {
		t.Error("dark not detected")
	}
	m["bg"] = "#fafafa"
	if SchemeOf(m) != "light" {
		t.Error("light not detected")
	}
}

// TestTailwindMapUsesKnownTokens: the class map the Tailwind config reads
// (tailwind-map.json) points every class at a token of the palette, and
// every token is used by at least one class (or is reachable through the
// accent rule in input.css: accent-text).
func TestTailwindMapUsesKnownTokens(t *testing.T) {
	m, err := LoadTailwindMap("tailwind-map.json")
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{"accent-text": true}
	for util, classes := range m {
		if len(classes) == 0 {
			t.Errorf("%s: empty", util)
		}
		for class, tok := range classes {
			if !slices.ContainsFunc(Tokens, func(tk Token) bool { return tk.Name == tok }) {
				t.Errorf("%s.%s -> unknown token %q", util, class, tok)
			}
			used[tok] = true
		}
	}
	for _, tk := range Tokens {
		if !used[tk.Name] {
			t.Errorf("token %s is not used by any class", tk.Name)
		}
	}
	if _, err := os.Stat("tailwind-map.json"); err != nil {
		t.Fatal(err)
	}
}

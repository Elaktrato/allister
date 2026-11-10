package appearance

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// golden compares got with testdata/<name>; UPDATE_GOLDEN=1 rewrites it.
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := "testdata/" + name
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("%s differs:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func TestCSSDefaultGolden(t *testing.T) {
	golden(t, "default.css", CSS(Config{}, nil))
	// the explicit default settings render the same
	if string(CSS(Config{Preset: DefaultPreset, Font: DefaultFont, Scale: DefaultScale}, nil)) != string(CSS(Config{}, nil)) {
		t.Error("explicit defaults differ from the zero config")
	}
}

func TestCSSSystemGolden(t *testing.T) {
	golden(t, "system.css", CSS(Config{Preset: SystemPreset, Font: "system-serif", Scale: "large"}, nil))
}

// TestCSSSystemFollowsColourScheme: the system preset is the dark preset by
// default and the light preset inside a prefers-color-scheme media query.
func TestCSSSystemFollowsColourScheme(t *testing.T) {
	css := string(CSS(Config{Preset: SystemPreset}, nil))
	dark, _ := PresetByID(SystemDark)
	light, _ := PresetByID(SystemLight)
	i := strings.Index(css, "@media (prefers-color-scheme: light)")
	if i < 0 {
		t.Fatalf("no media query in %s", css)
	}
	mustHave(t, css[:i], "color-scheme: dark;", "--al-bg: "+triplet(dark.Colors["bg"])+";")
	mustHave(t, css[i:], "color-scheme: light;", "--al-bg: "+triplet(light.Colors["bg"])+";")
	if strings.Count(css, "@media") != 1 {
		t.Error("more than one media query")
	}
	if strings.Contains(string(CSS(Config{Preset: "paper"}, nil)), "@media") {
		t.Error("a fixed preset has a media query")
	}
}

func TestCSSCustomPaletteWins(t *testing.T) {
	m := full()
	m["bg"] = "#fafafa"
	m["accent"] = "#123456"
	p, err := ParsePalette(m)
	if err != nil {
		t.Fatal(err)
	}
	css := string(CSS(Config{Preset: SystemPreset, Palette: p}, nil))
	mustHave(t, css, "--al-bg: 250 250 250;", "--al-accent: 18 52 86;", "color-scheme: light;")
	if strings.Contains(css, "@media") {
		t.Error("a custom palette is one scheme, no media query")
	}
}

// TestCSSNeverEmitsUnvalidatedValues: whatever reaches the generator (a
// hostile palette, font or scale, as if read from a tampered database), the
// output holds only validated hex triplets, whitelisted font stacks and
// known sizes, and nothing that could end a rule or load a resource.
func TestCSSNeverEmitsUnvalidatedValues(t *testing.T) {
	hostile := Palette{}
	for _, tk := range Tokens {
		hostile[tk.Name] = "red;}body{display:none}"
	}
	hostile["bg"] = "</style><script>alert(1)</script>"
	hostile["text"] = "url(https://evil.example/x)"
	hostile["evil;}*{"] = "#000000"
	for _, c := range []Config{
		{Preset: "x;}body{display:none", Palette: hostile, Font: `x"; } * { font-family: "a`, Scale: "999%;}"},
		{Preset: "midnight", Palette: Palette{"bg": "#000000"}, Font: "../../etc", Scale: "</style>"},
	} {
		css := string(CSS(c, []Font{{ID: 3, Name: `bad"name`, File: "../x.woff2", Format: "woff2"}}))
		for _, bad := range []string{"display:none", "<", ">", "evil", "alert", "url(https", "../", "999%", `bad"name`} {
			if strings.Contains(css, bad) {
				t.Errorf("CSS contains %q:\n%s", bad, css)
			}
		}
		vars := regexp.MustCompile(`--al-([a-z-]+): ([^;]*);`).FindAllStringSubmatch(css, -1)
		if len(vars) < len(Tokens) {
			t.Fatalf("only %d variables", len(vars))
		}
		for _, v := range vars {
			switch v[1] {
			case "font":
			case "root-size":
				if v[2] != "100%" {
					t.Errorf("root size %q", v[2])
				}
			default:
				if !regexp.MustCompile(`^\d{1,3} \d{1,3} \d{1,3}$`).MatchString(v[2]) {
					t.Errorf("--al-%s: %q", v[1], v[2])
				}
			}
		}
		// fell back to the default font and preset
		def, _ := PresetByID(DefaultPreset)
		mustHave(t, css, "--al-font: "+SystemFonts[0].Stack+";", "--al-text: "+triplet(def.Colors["text"])+";")
	}
}

// TestCSSFontAndScale: the chosen font's stack and the scale's root size.
func TestCSSFontAndScale(t *testing.T) {
	css := string(CSS(Config{Font: "system-mono", Scale: "small"}, nil))
	mono, ok := FontByID("system-mono", nil)
	if !ok {
		t.Fatal("system-mono missing")
	}
	mustHave(t, css, "--al-font: "+mono.Stack+";", "--al-root-size: 87.5%;")
	for _, f := range Fonts(nil) {
		mustHave(t, css, ".al-font-"+f.ID+" { font-family: "+f.Stack+"; }")
	}
	if len(Scales) != 3 {
		t.Errorf("%d scales", len(Scales))
	}
}

func mustHave(t *testing.T, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Errorf("missing %q in:\n%s", sub, s)
		}
	}
}

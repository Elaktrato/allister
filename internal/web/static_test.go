package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/allister/allister/internal/appearance"
)

// TestHtmxChecksumMatches verifies the vendored (and embedded, so served)
// htmx file against static/htmx.min.js.sha256 (sha256sum format), and that
// its version is the one PINS.md records. Updating htmx is a deliberate
// change of all three.
func TestHtmxChecksumMatches(t *testing.T) {
	js, err := staticFS.ReadFile("static/htmx.min.js")
	if err != nil {
		t.Fatal(err)
	}
	sumFile, err := staticFS.ReadFile("static/htmx.min.js.sha256")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(sumFile))
	if len(fields) != 2 || fields[1] != "htmx.min.js" || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(fields[0]) {
		t.Fatalf("htmx.min.js.sha256 is not `<sha256>  htmx.min.js`: %q", sumFile)
	}
	sum := sha256.Sum256(js)
	if got := hex.EncodeToString(sum[:]); got != fields[0] {
		t.Errorf("htmx.min.js sha256 = %s, recorded %s", got, fields[0])
	}

	v := regexp.MustCompile(`version:"([0-9.]+)"`).FindSubmatch(js)
	if v == nil {
		t.Fatal("no version in htmx.min.js")
	}
	pins, err := os.ReadFile("../../PINS.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pins), "htmx "+string(v[1])) {
		t.Errorf("PINS.md does not record htmx %s", v[1])
	}
}

// iconFont is the vendored Material Symbols Outlined font (see PINS.md).
const iconFont = "material-symbols-outlined-v383.woff2"

// TestIconFontChecksumMatches verifies the vendored icon font against its
// .sha256 file and that PINS.md records its version and sum, like htmx.
func TestIconFontChecksumMatches(t *testing.T) {
	font, err := staticFS.ReadFile("static/" + iconFont)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(font), "wOF2") {
		t.Fatalf("%s is not a woff2 file", iconFont)
	}
	sumFile, err := staticFS.ReadFile("static/" + iconFont + ".sha256")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(sumFile))
	if len(fields) != 2 || fields[1] != iconFont || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(fields[0]) {
		t.Fatalf("%s.sha256 is not `<sha256>  %s`: %q", iconFont, iconFont, sumFile)
	}
	sum := sha256.Sum256(font)
	if got := hex.EncodeToString(sum[:]); got != fields[0] {
		t.Errorf("%s sha256 = %s, recorded %s", iconFont, got, fields[0])
	}
	pins, err := os.ReadFile("../../PINS.md")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, string(pins), "Material Symbols Outlined v383", "`internal/web/static/"+iconFont+"`", fields[0], "Apache")
}

// TestIconFontServedWithFontHeaders: the embedded font answers with the font
// MIME type, a year-long immutable cache (the file name carries the version)
// and nosniff; other static files keep their headers.
func TestIconFontServedWithFontHeaders(t *testing.T) {
	font, _ := staticFS.ReadFile("static/" + iconFont)
	rec := get(t, "/static/"+iconFont)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "font/woff2" {
		t.Errorf("Content-Type %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control %q", cc)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("no nosniff")
	}
	if rec.Body.String() != string(font) {
		t.Error("body differs from the embedded font")
	}
	if rec := get(t, "/static/htmx.min.js"); rec.Header().Get("Cache-Control") != "" {
		t.Errorf("htmx got a cache header: %q", rec.Header().Get("Cache-Control"))
	}
	if rec := get(t, "/static/nope.woff2"); rec.Code != 404 || rec.Header().Get("Cache-Control") != "" {
		t.Errorf("missing font: %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
}

// TestIconFontDeclaredWithoutFlash: the stylesheet declares the font from the
// embedded file (never a CDN) with font-display: block, so a ligature never
// shows as text, and every page preloads it.
func TestIconFontDeclaredWithoutFlash(t *testing.T) {
	css, err := staticFS.ReadFile("static/input.css")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, string(css), "@font-face", `font-family: "Material Symbols Outlined"`, `url("/static/`+iconFont+`") format("woff2")`,
		"font-display: block", ".icon")
	mustNotContain(t, string(css), "googleapis", "gstatic", "http:", "https:")
	b := get(t, "/settings").Body.String()
	mustContain(t, b, `<link rel="preload" href="/static/`+iconFont+`" as="font" type="font/woff2" crossorigin>`)
	mustNotContain(t, b, "googleapis", "gstatic")
}

// bundledFontFiles are the vendored app fonts (see PINS.md): Inter, Source
// Serif 4 and JetBrains Mono from the Fontsource variable packages, each as
// its Latin and Latin Extended subset.
var bundledFontFiles = []string{
	"inter-latin-wght-v5.3.0.woff2", "inter-latin-ext-wght-v5.3.0.woff2",
	"source-serif-4-latin-wght-v5.3.1.woff2", "source-serif-4-latin-ext-wght-v5.3.1.woff2",
	"jetbrains-mono-latin-wght-v5.3.0.woff2", "jetbrains-mono-latin-ext-wght-v5.3.0.woff2",
}

// TestBundledFontsPinned: every bundled font file is woff2, matches its
// .sha256, is recorded in PINS.md and has its licence in LICENSES/; the
// stylesheet declares exactly these files (font-display: swap, a
// unicode-range per subset) and they are served with the font headers.
func TestBundledFontsPinned(t *testing.T) {
	pins, err := os.ReadFile("../../PINS.md")
	if err != nil {
		t.Fatal(err)
	}
	css, _ := appearance.NewService(nil).Stylesheet(context.Background())
	for _, name := range bundledFontFiles {
		font, err := staticFS.ReadFile("static/fonts/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(string(font), "wOF2") {
			t.Errorf("%s is not woff2", name)
		}
		sumFile, err := staticFS.ReadFile("static/fonts/" + name + ".sha256")
		if err != nil {
			t.Fatal(err)
		}
		fields := strings.Fields(string(sumFile))
		if len(fields) != 2 || fields[1] != name || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(fields[0]) {
			t.Fatalf("%s.sha256 is not `<sha256>  %s`: %q", name, name, sumFile)
		}
		sum := sha256.Sum256(font)
		if got := hex.EncodeToString(sum[:]); got != fields[0] {
			t.Errorf("%s sha256 = %s, recorded %s", name, got, fields[0])
		}
		mustContain(t, string(pins), "`internal/web/static/fonts/"+name+"`", fields[0])
		mustContain(t, string(css), `src: url("/static/fonts/`+name+`") format("woff2");`)

		rec := get(t, "/static/fonts/"+name)
		if rec.Code != 200 || rec.Header().Get("Content-Type") != "font/woff2" ||
			rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s served %d %v", name, rec.Code, rec.Header())
		}
	}
	if n := strings.Count(string(css), "@font-face"); n != len(bundledFontFiles) {
		t.Errorf("%d @font-face rules, want %d", n, len(bundledFontFiles))
	}
	if n := strings.Count(string(css), "font-display: swap;"); n != len(bundledFontFiles) {
		t.Errorf("%d font-display: swap, want %d", n, len(bundledFontFiles))
	}
	if n := strings.Count(string(css), "unicode-range: U+"); n != len(bundledFontFiles) {
		t.Errorf("%d unicode ranges, want %d", n, len(bundledFontFiles))
	}
	for _, lic := range []string{"inter-OFL-1.1.txt", "source-serif-4-OFL-1.1.txt", "jetbrains-mono-OFL-1.1.txt"} {
		b, err := os.ReadFile("../../LICENSES/" + lic)
		if err != nil {
			t.Fatal(err)
		}
		mustContain(t, string(b), "SIL OPEN FONT LICENSE Version 1.1")
		mustContain(t, string(pins), "`LICENSES/"+lic+"`")
	}
	if got := len(appearance.BundledFonts); got != 3 {
		t.Errorf("%d bundled fonts, want 3", got)
	}
	// every static .sha256 (fonts included) is listed for the pins checks
	mustContain(t, string(pins), "OFL-1.1")
}

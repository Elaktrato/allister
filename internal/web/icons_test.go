package web

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
	"github.com/allister/allister/internal/plugins/anime"
)

// codepointsFile is upstream's name list of the pinned font (see PINS.md):
// test data only, never embedded.
const codepointsFile = "testdata/material-symbols-outlined.codepoints"

// iconNames reads the pinned codepoints file (verified against its .sha256
// and PINS.md) into a set of icon names.
func iconNames(t *testing.T) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(codepointsFile)
	if err != nil {
		t.Fatal(err)
	}
	sumFile, err := os.ReadFile(codepointsFile + ".sha256")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(sumFile))
	sum := sha256.Sum256(data)
	if len(fields) != 2 || fields[1] != "material-symbols-outlined.codepoints" || hex.EncodeToString(sum[:]) != fields[0] {
		t.Fatalf("codepoints sha256 %x does not match %q", sum, sumFile)
	}
	pins, err := os.ReadFile("../../PINS.md")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, string(pins), fields[0], "`internal/web/"+codepointsFile+"`",
		"https://raw.githubusercontent.com/google/material-design-icons/10ecc14790612ca8fd164e3f4ffe9c16fc50d5b7/variablefont/MaterialSymbolsOutlined%5BFILL,GRAD,opsz,wght%5D.codepoints")
	names := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 {
			names[f[0]] = true
		}
	}
	if len(names) < 3000 {
		t.Fatalf("only %d names in %s", len(names), codepointsFile)
	}
	return names
}

var (
	// openTag is one <span …> opening tag of a template.
	openTag = regexp.MustCompile(`<span\b[^>]*>`)
	// attrRe is one attribute of a tag, wherever it sits among the others.
	attrRe = regexp.MustCompile(`([a-zA-Z-]+)="([^"]*)"`)
	// action is a template action; the literal text around actions is icon
	// names, the fields inside them render names from data.
	action = regexp.MustCompile(`\{\{(.*?)\}\}`)
	// fieldRef is a template data field reference (.Icon, .Sort, …).
	fieldRef = regexp.MustCompile(`\.([A-Za-z_][A-Za-z0-9_]*)`)
)

// iconSpans finds the icon elements of a template body: <span> tags whose
// class attribute (wherever it sits among the tag's attributes, whatever
// else it lists) names "icon". The span's literal text contributes icon
// names — including the literals between an if/else's actions — and
// template actions contribute the data fields they render.
func iconSpans(body string) (literals, fields []string) {
	for _, loc := range openTag.FindAllStringIndex(body, -1) {
		tag := body[loc[0]:loc[1]]
		rest := body[loc[1]:]
		end := strings.Index(rest, "</span>")
		if end < 0 || !spanHasIconClass(tag) {
			continue
		}
		text := rest[:end]
		literals = append(literals, strings.Fields(action.ReplaceAllString(text, " "))...)
		for _, m := range action.FindAllStringSubmatch(text, -1) {
			for _, f := range fieldRef.FindAllStringSubmatch(m[1], -1) {
				fields = append(fields, f[1])
			}
		}
	}
	return literals, fields
}

// spanHasIconClass reports whether the opening tag's class attribute lists
// "icon", whatever the attribute order.
func spanHasIconClass(tag string) bool {
	for _, m := range attrRe.FindAllStringSubmatch(tag, -1) {
		if m[1] != "class" {
			continue
		}
		for _, c := range strings.Fields(m[2]) {
			if c == "icon" {
				return true
			}
		}
	}
	return false
}

// TestIconSpanExtraction keeps the extractor honest: attributes in any
// order, other classes and attributes around, data-driven names visible.
func TestIconSpanExtraction(t *testing.T) {
	for _, c := range []struct {
		name             string
		body             string
		literals, fields []string
	}{
		{"class first", `<span class="icon" aria-hidden="true">add</span>`, []string{"add"}, nil},
		{"class last", `<span aria-hidden="true" class="icon">arrow_upward</span>`, []string{"arrow_upward"}, nil},
		{"class with more classes and attrs", `<span data-x="1" title="t" class="icon text-sm" aria-hidden="true">tune</span>`, []string{"tune"}, nil},
		{"data driven", `<span class="icon" aria-hidden="true">{{.Icon}}</span>`, nil, []string{"Icon"}},
		{"conditional with fields and literals", `<span class="icon">{{if eq .Sort "ascending"}}arrow_upward{{else}}arrow_downward{{end}}</span>`, []string{"arrow_upward", "arrow_downward"}, []string{"Sort"}},
		{"nav row shape", `<span class="icon x">{{if eq .Href "/s"}}settings{{else}}{{with .Icon}}{{.}}{{else}}category{{end}}{{end}}</span>`, []string{"settings", "category"}, []string{"Href", "Icon"}},
		{"no end tag", `<span class="icon">add`, nil, nil},
		{"not an icon class", `<span class="iconography" aria-hidden="true">add</span>`, nil, nil},
		{"two spans", `<span class="icon">add</span><span class="not-icon">x</span><span class="icon">tune</span>`, []string{"add", "tune"}, nil},
	} {
		literals, fields := iconSpans(c.body)
		if strings.Join(literals, ",") != strings.Join(c.literals, ",") {
			t.Errorf("%s: literals %v, want %v", c.name, literals, c.literals)
		}
		if strings.Join(fields, ",") != strings.Join(c.fields, ",") {
			t.Errorf("%s: fields %v, want %v", c.name, fields, c.fields)
		}
	}
}

// tabIcon is the icon argument of a settings tab ({{template "settings_tab" (tab href label icon .Current)}}).
var tabIcon = regexp.MustCompile(`\(tab "[^"]*" "[^"]*" "([^"]*)"`)

// templateIconNames returns every literal icon name the core templates use
// and the template data fields an icon span can render (name/field ->
// template file).
func templateIconNames(t *testing.T) (map[string]string, map[string]string) {
	t.Helper()
	out := map[string]string{}    // icon name -> template file
	fields := map[string]string{} // data field -> template file
	err := fs.WalkDir(templateFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := templateFS.ReadFile(path)
		if err != nil {
			return err
		}
		literals, spans := iconSpans(string(b))
		for _, name := range literals {
			out[name] = path
		}
		for _, field := range spans {
			fields[field] = path
		}
		for _, m := range tabIcon.FindAllStringSubmatch(string(b), -1) {
			out[m[1]] = path
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out, fields
}

// TestIconNamesExistInThePinnedFont: every icon name the core templates use
// and every compiled-in manifest icon is in the pinned font's name list (a
// missing name would render as a clipped letter). The list is upstream's
// codepoints file, a proxy for the font's ligatures: the pinned static
// instance carries 4012 of them while the list names 4304, so in principle
// a listed name could lack a glyph — the check still catches the common
// cases (typos, renamed icons) for the handful of names actually used.
func TestIconNamesExistInThePinnedFont(t *testing.T) {
	names := iconNames(t)
	used, dataFields := templateIconNames(t)
	// the extractor must see the core's icons (sidebar, default plugin icon,
	// sort arrows, settings tabs, search bar), or it would check nothing
	for _, want := range []string{"collections_bookmark", "settings", "category", "arrow_upward", "arrow_downward", "unfold_more",
		"search", "filter_list", "add", "tune", "extension", "upload_file", "backup", "grid_view", "table_view"} {
		if used[want] == "" {
			t.Errorf("extractor did not find %q in the templates", want)
		}
	}
	for name, file := range used {
		if !names[name] {
			t.Errorf("%s: icon %q is not in the pinned font", file, name)
		}
	}
	// every data field an icon span renders must be covered elsewhere: a new
	// field here is a new icon source — extend this list only together with
	// the check that covers it
	covered := map[string]string{
		"Icon":    "the manifest icons, checked below",
		"Href":    "only compared against literals (settings vs the rest)",
		"Sort":    "only compared against literals (ascending or descending)",
		"DirIcon": "the two arrow icons, literal values in filters.go (must-find list)",
	}
	for field, file := range dataFields {
		if covered[field] == "" {
			t.Errorf("%s: icon span renders .%s, which no check covers (see the covered list)", file, field)
		}
	}
	for _, p := range []plugin.Plugin{anime.New(anime.Options{}), plugintest.BookPlugin(), plugintest.TVPlugin()} {
		m := p.Manifest()
		if m.Icon != "" && !names[m.Icon] {
			t.Errorf("plugin %s: icon %q is not in the pinned font", m.ID, m.Icon)
		}
	}
	// the name list is test data only: never embedded or served
	if _, err := staticFS.Open("static/material-symbols-outlined.codepoints"); err == nil {
		t.Error("codepoints embedded in the static files")
	}
}

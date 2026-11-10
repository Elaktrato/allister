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
	// iconContent is the content of an icon element in a template.
	iconContent = regexp.MustCompile(`class="icon(?: [^"]*)?" aria-hidden="true">(.*?)</span>`)
	// tabIcon is the icon argument of a settings tab ({{template "settings_tab" (tab href label icon .Current)}}).
	tabIcon = regexp.MustCompile(`\(tab "[^"]*" "[^"]*" "([^"]*)"`)
	// action is a template action; the literal text around actions is icon names.
	action = regexp.MustCompile(`\{\{.*?\}\}`)
)

// templateIconNames returns every literal icon name the core templates use.
func templateIconNames(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{} // name -> template file
	err := fs.WalkDir(templateFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := templateFS.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range iconContent.FindAllStringSubmatch(string(b), -1) {
			for _, name := range strings.Fields(action.ReplaceAllString(m[1], " ")) {
				out[name] = path
			}
		}
		for _, m := range tabIcon.FindAllStringSubmatch(string(b), -1) {
			out[m[1]] = path
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestIconNamesExistInThePinnedFont: every icon name the core templates use
// and every compiled-in manifest icon is in the pinned font's name list (a
// missing name would render as a clipped letter).
func TestIconNamesExistInThePinnedFont(t *testing.T) {
	names := iconNames(t)
	used := templateIconNames(t)
	// the extractor must see the core's icons (sidebar, default plugin icon,
	// sort arrows, settings tabs, search bar), or it would check nothing
	for _, want := range []string{"collections_bookmark", "settings", "category", "arrow_upward", "arrow_downward", "unfold_more",
		"search", "filter_list", "add", "tune", "extension", "upload_file", "backup"} {
		if used[want] == "" {
			t.Errorf("extractor did not find %q in the templates", want)
		}
	}
	for name, file := range used {
		if !names[name] {
			t.Errorf("%s: icon %q is not in the pinned font", file, name)
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

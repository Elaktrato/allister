// Package guard holds only tests: they keep the core of Allister generic.
// Media-specific code belongs in a plugin (internal/plugins/...); the core
// knows plugins only through the contract (internal/plugin) and the one
// registration in cmd/allister.
package guard

import (
	"bufio"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// root is the repository root, seen from internal/guard.
const root = "../.."

// module is the module path of the repository.
const module = "github.com/allister/allister"

// coreDirsIn returns the core directories of the repository at base
// (relative, forward slashes): cmd and every directory directly under
// internal except internal/plugins (the plugins). A new core package is
// scanned without being listed anywhere.
func coreDirsIn(t *testing.T, base string) []string {
	t.Helper()
	dirs := []string{"cmd"}
	ents, err := os.ReadDir(filepath.Join(base, "internal"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.IsDir() && e.Name() != "plugins" {
			dirs = append(dirs, "internal/"+e.Name())
		}
	}
	return dirs
}

// excludedDirs are skipped inside the core directories: the plugin test fixtures may use
// any example data.
var excludedDirs = []string{
	"internal/plugin/plugintest",
}

// mediaWords are the words of the first plugin (anime) that the core must not
// contain, case-insensitively.
var mediaWords = regexp.MustCompile(`(?i)anime|anilist|episode|yamtrack|mal_id|myanimelist|manga|\bmal\b`)

// allowed lists, per file, the exact (trimmed) lines that may contain a media
// word. Keep it minimal: every entry needs a reason.
var allowed = map[string][]string{
	"cmd/allister/main.go": {
		// the one place that compiles in the default plugin: its import ...
		`"github.com/allister/allister/internal/plugins/anime"`,
		// ... and its registration
		`if err := registry.Register(anime.New(anime.Options{})); err != nil {`,
	},
}

// productionFiles returns the .go (not _test.go) and .html files under the
// core directories (coreDirsIn), relative to root with forward slashes, sorted.
func productionFiles(t *testing.T) []string {
	t.Helper()
	return productionFilesIn(t, root)
}

// productionFilesIn is productionFiles for the repository at base.
func productionFilesIn(t *testing.T, base string) []string {
	t.Helper()
	root := base
	var out []string
	for _, dir := range coreDirsIn(t, base) {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				for _, ex := range excludedDirs {
					if rel == ex {
						return filepath.SkipDir
					}
				}
				return nil
			}
			name := d.Name()
			if strings.HasSuffix(name, "_test.go") {
				return nil
			}
			if strings.HasSuffix(name, ".go") || strings.HasSuffix(name, ".html") {
				out = append(out, rel)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		t.Fatal("no production files found: is the root right?")
	}
	return out
}

// scan returns one message per offending line of the file rel read from r
// (lines in allowed[rel] are skipped and recorded in used), plus one when the
// file name itself has a media word.
func scan(rel string, r io.Reader, used map[string]map[string]bool) ([]string, error) {
	var out []string
	if mediaWords.MatchString(rel) {
		out = append(out, fmt.Sprintf("%s: media-specific word %q in a core file name", rel, mediaWords.FindString(rel)))
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if !mediaWords.MatchString(line) {
			continue
		}
		trimmed := strings.TrimSpace(line)
		ok := false
		for _, a := range allowed[rel] {
			if trimmed == a {
				ok = true
				if used[rel] == nil {
					used[rel] = map[string]bool{}
				}
				used[rel][a] = true
			}
		}
		if !ok {
			out = append(out, fmt.Sprintf("%s:%d: media-specific word %q in the core: %s", rel, n, mediaWords.FindString(line), trimmed))
		}
	}
	return out, sc.Err()
}

func TestCoreHasNoMediaSpecificWords(t *testing.T) {
	used := map[string]map[string]bool{}
	for _, rel := range productionFiles(t) {
		f, err := os.Open(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		msgs, err := scan(rel, f, used)
		f.Close()
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		for _, m := range msgs {
			t.Error(m)
		}
	}
	// a stale allow-list entry hides nothing but must go
	for rel, lines := range allowed {
		for _, a := range lines {
			if !used[rel][a] {
				t.Errorf("allow-list entry not found in %s (remove it): %s", rel, a)
			}
		}
	}
}

func TestCoreDoesNotImportPlugins(t *testing.T) {
	const plugins = module + "/internal/plugins/"
	fset := token.NewFileSet()
	checked := 0
	for _, top := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if rel == "internal/plugins" {
					return filepath.SkipDir // the plugins themselves
				}
				return nil
			}
			if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			checked++
			pkgDir := filepath.ToSlash(filepath.Dir(rel))
			for _, imp := range f.Imports {
				p, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					return err
				}
				if strings.HasPrefix(p, plugins) && pkgDir != "cmd/allister" {
					t.Errorf("%s imports %s: only cmd/allister may import a plugin", rel, p)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if checked == 0 {
		t.Fatal("no Go files checked")
	}
}

func TestNoLegacyPackagesRemain(t *testing.T) {
	for _, dir := range []string{
		"internal/store",
		"internal/provider",
		"internal/titles",
		"internal/importer/yamtrack",
		"internal/plugins/anime/animedef",
		"internal/plugins/anime/animemap",
	} {
		if _, err := os.Stat(filepath.Join(root, dir)); err == nil {
			t.Errorf("legacy package %s still exists", dir)
		} else if !os.IsNotExist(err) {
			t.Errorf("%s: %v", dir, err)
		}
	}
}

// migrationName is NNNNN_name.sql.
var migrationName = regexp.MustCompile(`^(\d{5})_[a-z0-9_]+\.sql$`)

// TestMigrationsAreStrictlyIncreasing: goose applies migrations by number and
// cannot apply a lower number to a database already at a higher version, so
// the numbers sorted by file name must strictly increase. Gaps are allowed.
// With five-digit names, sorting by name already orders by number; the real
// value of this test is the name pattern (which keeps that true and catches
// a duplicate number) and the assertion that 00008 stays absent.
func TestMigrationsAreStrictlyIncreasing(t *testing.T) {
	ents, err := os.ReadDir(filepath.Join(root, "internal/db/migrations"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatal("no migrations")
	}
	last := 0
	seen := map[int]string{}
	for _, n := range names {
		m := migrationName.FindStringSubmatch(n)
		if m == nil {
			t.Errorf("%s: not named NNNNN_name.sql", n)
			continue
		}
		v, _ := strconv.Atoi(m[1])
		if prev, dup := seen[v]; dup {
			t.Errorf("%s: number %d already used by %s", n, v, prev)
		}
		seen[v] = n
		if v <= last {
			t.Errorf("%s: number %d does not increase (previous %d)", n, v, last)
		}
		last = v
	}
	// 00008 is deliberately unused: it was planned for the import plugin
	// columns, which landed as 00010 after 00009 had shipped. Reusing 8 now
	// would never run on a database already at version 9 or later.
	if _, ok := seen[8]; ok {
		t.Errorf("migration 00008 must stay unused (see 00009_rewrite_sync_jobs.sql)")
	}
}

// TestScannerFlagsPlantedWords keeps the guard honest: the scanner flags
// planted media words in Go and HTML lines and in file names, leaves clean
// lines alone, and the walked files cover the core (so nobody can neuter the
// word list or the core directory walk unnoticed).
func TestScannerFlagsPlantedWords(t *testing.T) {
	for _, line := range []string{
		"// Episode counter", "func AnimeID() {}", "<h1>Anime</h1>", `x := "anilist.co"`,
		"var yamtrackRows int", "SELECT mal_id FROM t", "the MyAnimeList id", "a Manga shelf", "MAL #5", "id (mal)",
	} {
		msgs, err := scan("internal/web/x.go", strings.NewReader("package web\n"+line+"\n"), map[string]map[string]bool{})
		if err != nil || len(msgs) != 1 || !strings.Contains(msgs[0], "internal/web/x.go:2:") {
			t.Errorf("%q: %v %v", line, msgs, err)
		}
	}
	html := "<p>clean</p>\n<td>{{.Episodes}}</td>\n"
	if msgs, _ := scan("internal/web/templates/x.html", strings.NewReader(html), map[string]map[string]bool{}); len(msgs) != 1 ||
		!strings.Contains(msgs[0], "x.html:2:") {
		t.Errorf("html: %v", msgs)
	}
	for _, clean := range []string{"normal malformed animation? no", "// a smallish formal note", "items and units"} {
		// "animation" contains "anim" but not "anime"; "normal", "malformed" are not the word mal
		if msgs, _ := scan("internal/web/x.go", strings.NewReader(clean+"\n"), map[string]map[string]bool{}); len(msgs) != 0 {
			t.Errorf("clean line %q flagged: %v", clean, msgs)
		}
	}
	if msgs, _ := scan("internal/web/anime_page.go", strings.NewReader(""), map[string]map[string]bool{}); len(msgs) != 1 {
		t.Errorf("file name not flagged: %v", msgs)
	}
	// an allow-listed line passes and is recorded
	used := map[string]map[string]bool{}
	line := allowed["cmd/allister/main.go"][1]
	if msgs, _ := scan("cmd/allister/main.go", strings.NewReader("\t"+line+"\n"), used); len(msgs) != 0 || !used["cmd/allister/main.go"][line] {
		t.Errorf("allowed line: %v %v", msgs, used)
	}
	files := map[string]bool{}
	for _, f := range productionFiles(t) {
		files[f] = true
	}
	for _, want := range []string{"cmd/allister/main.go", "internal/web/server.go", "internal/web/templates/base.html",
		"internal/library/library.go", "internal/importer/runner.go", "internal/plugin/contract.go", "internal/db/db.go"} {
		if !files[want] {
			t.Errorf("%s is not scanned", want)
		}
	}
	for f := range files {
		if strings.HasPrefix(f, "internal/plugin/plugintest/") || strings.HasPrefix(f, "internal/plugins/") || strings.HasSuffix(f, "_test.go") {
			t.Errorf("%s must not be scanned", f)
		}
	}
	for _, d := range []string{"internal/backup", "internal/jobs", "internal/covers", "internal/settings", "internal/ids", "internal/config"} {
		found := false
		for f := range files {
			if strings.HasPrefix(f, d+"/") {
				found = true
			}
		}
		if !found {
			t.Errorf("no file of %s is scanned", d)
		}
	}
}

// TestGuardScansEveryCoreDir: a directory added under internal/ later (not
// a plugin) is core and is scanned without anyone remembering to list it;
// internal/plugins and the plugin test fixtures are not.
func TestGuardScansEveryCoreDir(t *testing.T) {
	base := t.TempDir()
	for _, f := range []string{"cmd/app/main.go", "internal/newcore/x.go", "internal/newcore/templates/p.html",
		"internal/plugins/media/m.go", "internal/plugin/plugintest/f.go", "internal/newcore/x_test.go"} {
		p := filepath.Join(base, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := strings.Join(productionFilesIn(t, base), " ")
	if want := "cmd/app/main.go internal/newcore/templates/p.html internal/newcore/x.go"; got != want {
		t.Fatalf("scanned %q, want %q", got, want)
	}
}

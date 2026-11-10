// Package guard holds only tests: they keep the core of Allister generic.
// Media-specific code belongs in a plugin (internal/plugins/...); the core
// knows plugins only through the contract (internal/plugin) and the one
// registration in cmd/allister.
package guard

import (
	"bufio"
	"go/parser"
	"go/token"
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

// coreDirs are the directories (relative to root) of the core whose
// production sources and templates must not name a media type.
var coreDirs = []string{
	"cmd",
	"internal/plugin",
	"internal/library",
	"internal/web",
	"internal/jobs",
	"internal/covers",
	"internal/importer",
	"internal/backup",
	"internal/settings",
	"internal/db",
	"internal/ids",
	"internal/config",
}

// excludedDirs are skipped inside coreDirs: the plugin test fixtures may use
// any example data.
var excludedDirs = []string{
	"internal/plugin/plugintest",
}

// mediaWords are the words of the first plugin (anime) that the core must not
// contain, case-insensitively.
var mediaWords = regexp.MustCompile(`(?i)anime|anilist|episode|yamtrack|mal_id|myanimelist`)

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
// core directories, relative to root with forward slashes, sorted.
func productionFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, dir := range coreDirs {
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

func TestCoreHasNoMediaSpecificWords(t *testing.T) {
	used := map[string]map[string]bool{}
	for _, rel := range productionFiles(t) {
		if mediaWords.MatchString(rel) {
			t.Errorf("%s: media-specific word %q in a core file name", rel, mediaWords.FindString(rel))
		}
		f, err := os.Open(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(f)
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
				t.Errorf("%s:%d: media-specific word %q in the core: %s", rel, n, mediaWords.FindString(line), trimmed)
			}
		}
		if err := sc.Err(); err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		f.Close()
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

package backup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/allister/allister/internal/appearance"
	"github.com/allister/allister/internal/settings"
)

// appearanceEnv is an env with appearance settings and uploaded fonts
// (DATA_DIR/fonts below the env's root).
type appearanceEnv struct {
	*env
	set *settings.Store
	app *appearance.Service
}

func newAppearanceEnv(t *testing.T) *appearanceEnv {
	t.Helper()
	e := newEnv(t)
	st := settings.New(e.db)
	return &appearanceEnv{env: e, set: st, app: appearance.NewService(st, appearance.NewFontStore(e.db, filepath.Join(e.root, "fonts")))}
}

func (e *appearanceEnv) archive() []byte {
	e.t.Helper()
	var b bytes.Buffer
	if err := Write(bg, &b, e.lib, nil, e.covers, e.app, clock); err != nil {
		e.t.Fatal(err)
	}
	return b.Bytes()
}

func (e *appearanceEnv) restore(data []byte) *Report {
	e.t.Helper()
	rep, err := Restore(bg, bytes.NewReader(data), int64(len(data)), Options{Library: e.lib, CoversDir: e.covers, Appearance: e.app})
	if err != nil {
		e.t.Fatal(err)
	}
	if rep.Aborted != "" {
		e.t.Fatalf("aborted: %s", rep.Aborted)
	}
	return rep
}

func (e *appearanceEnv) get(key string) string {
	v, _, err := e.set.Get(bg, key)
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

func (e *appearanceEnv) fonts() []appearance.Font {
	fs, err := e.app.Fonts().List(bg)
	if err != nil {
		e.t.Fatal(err)
	}
	return fs
}

func fontBytes(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../web/static/fonts/inter-latin-wght-v5.3.0.woff2")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fakeFont(magic string, n int) []byte {
	b := bytes.Repeat([]byte{0x42}, n)
	copy(b, magic)
	return b
}

func TestBackupRoundTripsAppearance(t *testing.T) {
	a := newAppearanceEnv(t)
	a.book("1", "", nil)
	// an earlier font, deleted, so the restored ids differ from the backup's
	gone, _, _ := a.app.AddFont(bg, "Gone", bytes.NewReader(fakeFont("wOFF", 64)))
	f, _, err := a.app.AddFont(bg, "My Font", bytes.NewReader(fontBytes(t)))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.app.DeleteFont(bg, gone.ID); err != nil {
		t.Fatal(err)
	}
	pal, _ := appearance.PresetByID("forest")
	custom := appearance.Palette{}
	for k, v := range pal.Colors {
		custom[k] = v
	}
	custom["accent"] = "#123456"
	for _, err := range []error{a.app.SetPreset(bg, "forest"), a.app.SetPalette(bg, custom), a.app.SetFont(bg, "upload-"+strconv.FormatInt(f.ID, 10)), a.app.SetScale(bg, "large")} {
		if err != nil {
			t.Fatal(err)
		}
	}
	data := a.archive()
	_, files := open(t, data)
	if !bytes.Equal(files["fonts/"+f.File], fontBytes(t)) {
		t.Fatalf("fonts/%s missing or different", f.File)
	}
	var head struct {
		Appearance json.RawMessage `json:"appearance"`
	}
	json.Unmarshal(files["backup.json"], &head)
	for _, want := range []string{`"preset":"forest"`, `"font":"upload-` + strconv.FormatInt(f.ID, 10) + `"`, `"font_scale":"large"`, `"accent":"#123456"`, `"name":"My Font"`, `"file":"` + f.File + `"`} {
		if !strings.Contains(string(head.Appearance), want) {
			t.Errorf("appearance member lacks %s: %s", want, head.Appearance)
		}
	}

	b := newAppearanceEnv(t)
	other, _, _ := b.app.AddFont(bg, "Local", bytes.NewReader(fakeFont("OTTO", 80)))
	rep := b.restore(data)
	if rep.Created != 1 {
		t.Errorf("items: %+v", rep)
	}
	fs := b.fonts()
	if len(fs) != 2 || fs[0].ID != other.ID || fs[1].Name != "My Font" || fs[1].File != f.File {
		t.Fatalf("fonts after restore: %+v", fs)
	}
	if got, err := os.ReadFile(filepath.Join(b.root, "fonts", f.File)); err != nil || !bytes.Equal(got, fontBytes(t)) {
		t.Fatalf("font file: %v", err)
	}
	if b.get(appearance.KeyPreset) != "forest" || b.get(appearance.KeyScale) != "large" ||
		b.get(appearance.KeyFont) != "upload-"+strconv.FormatInt(fs[1].ID, 10) {
		t.Errorf("settings: %q %q %q", b.get(appearance.KeyPreset), b.get(appearance.KeyScale), b.get(appearance.KeyFont))
	}
	if p, err := appearance.DecodePalette(b.get(appearance.KeyPalette)); err != nil || p["accent"] != "#123456" {
		t.Errorf("palette: %v %v", p, err)
	}
	notes := strings.Join(rep.Appearance, "\n")
	for _, want := range []string{"1 uploaded font restored", "Appearance settings restored"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes lack %q: %s", want, notes)
		}
	}
	css, _ := b.app.Stylesheet(bg)
	if !strings.Contains(string(css), "/fonts/"+f.File) || !strings.Contains(string(css), "--al-accent: 18 52 86;") {
		t.Error("restored appearance not in the stylesheet")
	}

	// a second restore changes nothing: the font is deduplicated, the
	// settings are this installation's now
	rep = b.restore(data)
	if len(b.fonts()) != 2 {
		t.Errorf("second restore duplicated a font: %+v", b.fonts())
	}
	if notes := strings.Join(rep.Appearance, "\n"); !strings.Contains(notes, "already") || !strings.Contains(notes, "kept") {
		t.Errorf("second restore notes: %s", notes)
	}
}

// TestBackupRestoreKeepsLocalAppearance: a restore never overwrites
// appearance settings this installation has made.
func TestBackupRestoreKeepsLocalAppearance(t *testing.T) {
	a := newAppearanceEnv(t)
	if err := a.app.SetPreset(bg, "rose"); err != nil {
		t.Fatal(err)
	}
	data := a.archive()
	b := newAppearanceEnv(t)
	if err := b.app.SetScale(bg, "small"); err != nil {
		t.Fatal(err)
	}
	rep := b.restore(data)
	if b.get(appearance.KeyPreset) != "" || b.get(appearance.KeyScale) != "small" {
		t.Errorf("local settings overwritten: %q %q", b.get(appearance.KeyPreset), b.get(appearance.KeyScale))
	}
	if !strings.Contains(strings.Join(rep.Appearance, "\n"), "kept") {
		t.Errorf("notes: %v", rep.Appearance)
	}
}

// TestBackupRestoreRevalidatesAppearance: the restore trusts nothing in the
// archive: fonts are sniffed and capped like uploads, names validated,
// file names never used as given, settings validated; refusals are notes.
func TestBackupRestoreRevalidatesAppearance(t *testing.T) {
	sha := func(c byte) string { return strings.Repeat(string([]byte{c}), 64) }
	good := fontBytes(t)
	member := map[string]any{
		"preset": "nope;}body{display:none", "palette": map[string]string{"bg": "red;}"}, "font": "upload-77", "font_scale": "</style>",
		"fonts": []map[string]any{
			{"id": 1, "name": "Disguised", "file": sha('a') + ".woff2", "format": "woff2"},
			{"id": 2, "name": "Missing", "file": sha('b') + ".woff2", "format": "woff2"},
			{"id": 3, "name": "Bad\x07name", "file": sha('c') + ".woff2", "format": "woff2"},
			{"id": 4, "name": "Traversal", "file": "../../x.woff2", "format": "woff2"},
			{"id": 5, "name": "Too big", "file": sha('d') + ".woff2", "format": "woff2"},
			{"id": 6, "name": "Good", "file": sha('e') + ".woff2", "format": "woff2"},
		},
	}
	head, _ := json.Marshal(map[string]any{"format": Format, "format_version": FormatVersion, "appearance": member, "items": []any{}})
	data := zipOf(t,
		entry{name: "backup.json", body: head},
		entry{name: "fonts/" + sha('a') + ".woff2", body: []byte("<html><script>alert(1)</script></html>")},
		entry{name: "fonts/" + sha('c') + ".woff2", body: good},
		entry{name: "fonts/../../x.woff2", body: good},
		entry{name: "fonts/" + sha('d') + ".woff2", body: fakeFont("wOF2", appearance.MaxFontBytes+1)},
		entry{name: "fonts/" + sha('e') + ".woff2", body: good}, // stored under its real hash, not this name
	)
	b := newAppearanceEnv(t)
	rep := b.restore(data)
	fs := b.fonts()
	if len(fs) != 1 || fs[0].Name != "Good" || fs[0].File == sha('e')+".woff2" {
		t.Fatalf("fonts: %+v", fs)
	}
	ents, _ := os.ReadDir(filepath.Join(b.root, "fonts"))
	if len(ents) != 1 {
		t.Errorf("%d files in the fonts dir", len(ents))
	}
	if _, err := os.Stat(filepath.Join(b.root, "x.woff2")); err == nil {
		t.Error("a file was written by its archive name")
	}
	for _, k := range []string{appearance.KeyPreset, appearance.KeyPalette, appearance.KeyFont, appearance.KeyScale} {
		if v := b.get(k); v != "" {
			t.Errorf("%s = %q stored from a bad archive", k, v)
		}
	}
	notes := strings.Join(rep.Appearance, "\n")
	for _, want := range []string{"Disguised", "not a font", "Missing", "missing from the backup", "control character", "Too big", "larger than 2 MiB", "Traversal", "colour preset", "text size", "font"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes lack %q:\n%s", want, notes)
		}
	}
	if strings.Contains(notes, "\x07") || strings.Contains(notes, "<script>") {
		t.Errorf("notes carry raw archive text: %q", notes)
	}

	// the cap of uploaded fonts holds for a restore too
	c := newAppearanceEnv(t)
	for i := 0; i < appearance.MaxFonts; i++ {
		if _, _, err := c.app.AddFont(bg, "F", bytes.NewReader(fakeFont("wOFF", 100+i))); err != nil {
			t.Fatal(err)
		}
	}
	rep = c.restore(data)
	if len(c.fonts()) != appearance.MaxFonts || !strings.Contains(strings.Join(rep.Appearance, "\n"), "fonts are uploaded already") {
		t.Errorf("cap: %d fonts, notes %v", len(c.fonts()), rep.Appearance)
	}

	// without an appearance service the member is skipped with a note
	e := newEnv(t)
	r2, err := e.restore(data, Limits{}, nil)
	if err != nil || !strings.Contains(strings.Join(r2.Appearance, "\n"), "not restored") {
		t.Errorf("no service: %v %v", r2.Appearance, err)
	}
}

// TestBackupWithoutAppearanceUnchanged: a backup written without the
// appearance (and an older archive) has no appearance member or fonts.
func TestBackupWithoutAppearanceUnchanged(t *testing.T) {
	e := newEnv(t)
	e.book("1", "", nil)
	d, files := open(t, e.archive())
	if len(d.Items) != 1 {
		t.Fatal(d)
	}
	if strings.Contains(string(files["backup.json"]), `"appearance"`) {
		t.Error("appearance member without a service")
	}
	a := newAppearanceEnv(t)
	rep := a.restore(e.archive())
	if len(rep.Appearance) != 0 {
		t.Errorf("notes for an archive without appearance: %v", rep.Appearance)
	}
}

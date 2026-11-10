package web

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/allister/allister/internal/appearance"
)

// TestBackupCarriesAppearance: the download holds the appearance settings
// and the uploaded fonts; restoring it on another installation brings
// both back, the page reports it, and the stylesheet follows at once.
func TestBackupCarriesAppearance(t *testing.T) {
	a := newEnv(t)
	if rec := a.uploadFont("Mine", "m.woff2", testWoff2(t)); rec.Code != 303 {
		t.Fatal(rec.Code)
	}
	id := a.fonts(t)[0].ID
	for path, form := range map[string]url.Values{
		"/settings/appearance/preset": {"preset": {"sand"}},
		"/settings/appearance/font":   {"font": {"upload-" + itoa64(id)}, "scale": {"small"}},
	} {
		if rec := a.do("POST", path, form, false); rec.Code != 303 {
			t.Fatalf("%s: %d", path, rec.Code)
		}
	}
	data := a.download(t)
	_, _, names := backupJSON(t, data)
	if !strings.Contains(strings.Join(names, " "), "fonts/"+a.fonts(t)[0].File) {
		t.Errorf("no font file in the archive: %v", names)
	}

	b := newEnv(t)
	_, v0 := b.srv.appearance.Stylesheet(context.Background())
	rec := b.restoreUpload(t, data)
	if rec.Code != 200 {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body.String())
	}
	mustContain(t, rec.Body.String(), "Appearance", "1 uploaded font restored.", "Appearance settings restored.")
	fs := b.fonts(t)
	if len(fs) != 1 || fs[0].Name != "Mine" || !bytes.Equal(b.get("/fonts/"+fs[0].File).Body.Bytes(), testWoff2(t)) {
		t.Fatalf("fonts: %+v", fs)
	}
	if b.setting(t, appearance.KeyPreset) != "sand" || b.setting(t, appearance.KeyScale) != "small" || b.setting(t, appearance.KeyFont) != "upload-"+itoa64(fs[0].ID) {
		t.Error("settings not restored")
	}
	if got := stylesheetLink(t, b.get("/settings").Body.String()); got == v0 {
		t.Error("stylesheet version unchanged after the restore")
	}
	mustContain(t, b.get("/settings/backup").Body.String(), "appearance settings and uploaded fonts")
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

// TestBackupRefusedHeaderStoresNoAppearance: an archive refused for its
// header shows the refusal and stores no font and no setting, even when
// its appearance member comes before the header.
func TestBackupRefusedHeaderStoresNoAppearance(t *testing.T) {
	font := testWoff2(t)
	sum := sha256.Sum256(font)
	file := hex.EncodeToString(sum[:]) + ".woff2"
	head := `{"appearance":{"preset":"rose","font_scale":"large","fonts":[{"id":1,"name":"Sneaky","file":"` + file + `","format":"woff2"}]},"format":"allister.backup","format_version":2,"items":[]}`
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string][]byte{"backup.json": []byte(head), "fonts/" + file: font} {
		w, _ := zw.Create(name)
		w.Write(body)
	}
	zw.Close()
	e := newEnv(t)
	rec := e.restoreUpload(t, buf.Bytes())
	if rec.Code != 422 {
		t.Fatalf("status %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), `role="alert"`, "format version 2")
	if fs := e.fonts(t); len(fs) != 0 {
		t.Errorf("fonts stored: %v", fs)
	}
	if e.setting(t, appearance.KeyPreset) != "" || e.setting(t, appearance.KeyScale) != "" {
		t.Error("settings stored")
	}
}

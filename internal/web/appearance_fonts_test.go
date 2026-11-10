package web

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/allister/allister/internal/appearance"
)

// testWoff2 is a real font: a bundled file.
func testWoff2(t *testing.T) []byte {
	t.Helper()
	b, err := staticFS.ReadFile("static/fonts/jetbrains-mono-latin-ext-wght-v5.3.0.woff2")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fakeFontBytes(magic string, n int) []byte {
	b := bytes.Repeat([]byte{0x41}, n)
	copy(b, magic)
	return b
}

// uploadFont posts the upload form (name, file named filename); extra
// headers are set on the request.
func (e *env) uploadFont(name, filename string, content []byte, hdr ...string) *httptest.ResponseRecorder {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("name", name)
	if filename != "" {
		fw, _ := mw.CreateFormFile("file", filename)
		fw.Write(content)
	}
	mw.Close()
	req := httptest.NewRequest("POST", "/settings/appearance/fonts", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *env) fonts(t *testing.T) []appearance.Font {
	t.Helper()
	fs, err := e.srv.appearance.Fonts().List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

func TestFontUploadAcceptsEveryFormat(t *testing.T) {
	e := newEnv(t)
	for i, c := range []struct {
		file, format string
		content      []byte
	}{
		{"a.woff2", "woff2", testWoff2(t)}, {"b.woff", "woff", fakeFontBytes("wOFF", 200)},
		{"c.ttf", "truetype", fakeFontBytes("\x00\x01\x00\x00", 300)}, {"d.otf", "opentype", fakeFontBytes("OTTO", 400)},
		{"no-extension", "truetype", fakeFontBytes("true", 500)},
	} {
		rec := e.uploadFont("Font "+strconv.Itoa(i), c.file, c.content)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings/appearance" {
			t.Fatalf("%s: %d %s", c.file, rec.Code, rec.Body.String())
		}
		fs := e.fonts(t)
		got := fs[len(fs)-1]
		if got.Format != c.format || got.Name != "Font "+strconv.Itoa(i) {
			t.Errorf("%s: %+v", c.file, got)
		}
	}
	page := e.get("/settings/appearance").Body.String()
	for _, f := range e.fonts(t) {
		id := strconv.FormatInt(f.ID, 10)
		mustContain(t, page, `id="font-upload-`+id+`" name="font" value="upload-`+id+`"`, `class="al-font-upload-`+id,
			`action="/settings/appearance/fonts/`+id+`/delete"`, f.Name)
	}
	mustContain(t, page, "5 of 8 fonts uploaded", `enctype="multipart/form-data"`, `<label for="font-name"`, `<label for="font-file"`)
	css, _ := e.srv.appearance.Stylesheet(context.Background())
	mustContain(t, string(css), `src: url("/fonts/`+e.fonts(t)[0].File+`") format("woff2");`)
}

func TestFontUploadRefused(t *testing.T) {
	cases := []struct {
		name, label, file string
		content           []byte
		status            int
		msg               string
	}{
		{"html", "X", "evil.woff2", []byte("<!doctype html><script>alert(1)</script>"), 422, "not a font"},
		{"svg", "X", "font.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><font/></svg>`), 422, "not a font"},
		{"empty", "X", "empty.ttf", nil, 422, "not a font"},
		{"wrong magic", "X", "x.otf", fakeFontBytes("ttcf", 100), 422, "not a font"},
		{"no file", "X", "", nil, 422, "choose a font file"},
		{"no name", "", "a.woff2", testWoff2Bytes(), 422, "give the font a name"},
		{"long name", strings.Repeat("n", 41), "a.woff2", testWoff2Bytes(), 422, "longer than 40"},
		{"control name", "a\x1bb", "a.woff2", testWoff2Bytes(), 422, "control character"},
		{"oversize", "X", "big.woff2", fakeFontBytes("wOF2", appearance.MaxFontBytes+1), 413, "larger than 2 MiB"},
		{"way oversize", "X", "huge.woff2", fakeFontBytes("wOF2", 3*appearance.MaxFontBytes), 413, "larger than 2 MiB"},
	}
	for _, c := range cases {
		e := newEnv(t)
		rec := e.uploadFont(c.label, c.file, c.content)
		if rec.Code != c.status {
			t.Errorf("%s: %d, want %d", c.name, rec.Code, c.status)
			continue
		}
		mustContain(t, rec.Body.String(), `role="alert"`, c.msg)
		mustNotContain(t, rec.Body.String(), "<script>alert")
		if fs := e.fonts(t); len(fs) != 0 {
			t.Errorf("%s: stored %v", c.name, fs)
		}
		if ents, _ := os.ReadDir(filepath.Join(filepath.Dir(e.covers), "fonts")); len(ents) != 0 {
			t.Errorf("%s: %d files left in the fonts dir", c.name, len(ents))
		}
	}
}

func testWoff2Bytes() []byte {
	b, _ := staticFS.ReadFile("static/fonts/jetbrains-mono-latin-ext-wght-v5.3.0.woff2")
	return b
}

func TestFontUploadCapAndDedupe(t *testing.T) {
	e := newEnv(t)
	if rec := e.uploadFont("Same", "a.woff2", testWoff2(t)); rec.Code != http.StatusSeeOther {
		t.Fatal(rec.Code)
	}
	if rec := e.uploadFont("Again", "b.woff2", testWoff2(t)); rec.Code != http.StatusSeeOther {
		t.Fatalf("duplicate: %d", rec.Code)
	}
	if fs := e.fonts(t); len(fs) != 1 || fs[0].Name != "Same" {
		t.Fatalf("duplicate stored: %v", fs)
	}
	for i := 1; i < appearance.MaxFonts; i++ {
		if rec := e.uploadFont("F", "f.woff", fakeFontBytes("wOFF", 100+i)); rec.Code != http.StatusSeeOther {
			t.Fatalf("font %d: %d", i+1, rec.Code)
		}
	}
	rec := e.uploadFont("Ninth", "n.otf", fakeFontBytes("OTTO", 50))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("over the cap: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "8 fonts are uploaded already")
	mustContain(t, e.get("/settings/appearance").Body.String(), "8 of 8 fonts uploaded", "Delete a font to upload another")
}

func TestFontUploadRefusesCrossOrigin(t *testing.T) {
	e := newEnv(t)
	rec := e.uploadFont("X", "a.woff2", testWoff2(t), "Origin", "https://evil.example")
	if rec.Code != http.StatusForbidden || len(e.fonts(t)) != 0 {
		t.Fatalf("cross-origin upload: %d", rec.Code)
	}
	if rec := e.uploadFont("X", "a.woff2", testWoff2(t), "Sec-Fetch-Site", "same-origin"); rec.Code != http.StatusSeeOther {
		t.Fatalf("same-origin upload: %d", rec.Code)
	}
	f := e.fonts(t)[0]
	req := httptest.NewRequest("POST", "/settings/appearance/fonts/"+strconv.FormatInt(f.ID, 10)+"/delete", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rr := httptest.NewRecorder()
	e.h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden || len(e.fonts(t)) != 1 {
		t.Fatalf("cross-origin delete: %d", rr.Code)
	}
}

func TestFontServedAndDeleted(t *testing.T) {
	e := newEnv(t)
	content := testWoff2(t)
	e.uploadFont("Served", "a.woff2", content)
	e.uploadFont("Other", "b.ttf", fakeFontBytes("\x00\x01\x00\x00", 99))
	fs := e.fonts(t)
	f, other := fs[0], fs[1]

	rec := e.get("/fonts/" + f.File)
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), content) {
		t.Fatalf("serve: %d", rec.Code)
	}
	h := rec.Header()
	if h.Get("Content-Type") != "font/woff2" || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Errorf("headers %v", h)
	}
	if ct := e.get("/fonts/" + other.File).Header().Get("Content-Type"); ct != "font/ttf" {
		t.Errorf("ttf Content-Type %q", ct)
	}

	// an unlisted file in the fonts dir, a secret next to it
	dir := filepath.Join(filepath.Dir(e.covers), "fonts")
	unlisted := strings.Repeat("ab", 32) + ".woff2"
	os.WriteFile(filepath.Join(dir, unlisted), content, 0o644)
	os.WriteFile(filepath.Join(filepath.Dir(dir), "secret.txt"), []byte("secret"), 0o644)
	for _, p := range []string{"/fonts/", "/fonts", "/fonts/" + unlisted, "/fonts/missing.woff2", "/fonts/" + strings.TrimSuffix(f.File, ".woff2") + ".woff",
		"/fonts/../secret.txt", "/fonts/%2e%2e/secret.txt", "/fonts/..%2fsecret.txt", "/fonts/%2e%2e%2fsecret.txt", "/fonts/" + strings.ToUpper(f.File),
		"/fonts/" + f.File + "/", "/fonts/sub/" + f.File} {
		rec, final := e.get(p), p
		for i := 0; i < 3 && (rec.Code == http.StatusMovedPermanently || rec.Code == http.StatusTemporaryRedirect); i++ {
			final = rec.Header().Get("Location")
			rec = e.get(final)
		}
		if rec.Code != http.StatusNotFound || rec.Body.String() == "secret" || rec.Header().Get("Cache-Control") != "" {
			t.Errorf("%s: %d %q", p, rec.Code, rec.Header().Get("Cache-Control"))
		}
		// a path the mux cleans away from /fonts is not the font handler's
		if strings.HasPrefix(final, "/fonts") && rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: no nosniff", p)
		}
	}

	// choose it, then delete it: the app font falls back to the default
	id := strconv.FormatInt(f.ID, 10)
	if rec := e.do("POST", "/settings/appearance/font", url.Values{"font": {"upload-" + id}, "scale": {"default"}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("choose: %d", rec.Code)
	}
	mustContain(t, e.get("/settings/appearance").Body.String(), `value="upload-`+id+`" checked`)
	rec = e.do("POST", "/settings/appearance/fonts/"+id+"/delete", url.Values{}, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings/appearance" {
		t.Fatalf("delete: %d", rec.Code)
	}
	if e.setting(t, appearance.KeyFont) != "" {
		t.Error("the deleted font is still the app font")
	}
	if rec := e.get("/fonts/" + f.File); rec.Code != http.StatusNotFound {
		t.Errorf("deleted font served: %d", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, f.File)); !os.IsNotExist(err) {
		t.Errorf("file kept: %v", err)
	}
	mustContain(t, e.get("/settings/appearance").Body.String(), `value="system-sans" checked`)
	for _, bad := range []string{id, "0", "abc", "-1", "99999999999999999999"} {
		if rec := e.do("POST", "/settings/appearance/fonts/"+bad+"/delete", url.Values{}, false); rec.Code != http.StatusNotFound {
			t.Errorf("delete %s: %d", bad, rec.Code)
		}
	}
}

// TestFontRoutesAbsentWithoutStore: a server without a font store offers
// no upload form and answers 404 on the font routes.
func TestFontRoutesAbsentWithoutStore(t *testing.T) {
	h, _, _ := newTestServer(t)
	rec := serve(h, "/settings/appearance")
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	mustNotContain(t, rec.Body.String(), `action="/settings/appearance/fonts"`)
	if rec := serve(h, "/fonts/"+strings.Repeat("ab", 32)+".woff2"); rec.Code != http.StatusNotFound {
		t.Errorf("serve: %d", rec.Code)
	}
	if rec := postForm(h, "/settings/appearance/fonts/1/delete", url.Values{}); rec.Code != http.StatusNotFound {
		t.Errorf("delete: %d", rec.Code)
	}
}

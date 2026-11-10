package web

import (
	"io/fs"
	"strings"
	"testing"
)

// TestTemplatesCarryNoInlineHandlers: no template carries an inline hx-on
// handler (or any other inline script); the page's scripts live in
// /static/app.js so a strict Content-Security-Policy stays possible.
func TestTemplatesCarryNoInlineHandlers(t *testing.T) {
	err := fs.WalkDir(templateFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := templateFS.ReadFile(path)
		if err != nil {
			return err
		}
		for _, bad := range []string{"hx-on"} {
			if strings.Contains(string(b), bad) {
				t.Errorf("%s: contains %q (move it to static/app.js)", path, bad)
			}
		}
		for _, tag := range scriptTags(string(b)) {
			if !strings.Contains(tag, " src=") {
				t.Errorf("%s: inline script %q (external files only)", path, tag)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// scriptTags returns every <script …> opening tag of a template body.
func scriptTags(body string) []string {
	var out []string
	for from := 0; ; {
		i := strings.Index(body[from:], "<script")
		if i < 0 {
			return out
		}
		i += from
		end := strings.IndexByte(body[i:], '>')
		if end < 0 {
			return out
		}
		out = append(out, body[i:i+end+1])
		from = i + end + 1
	}
}

// TestAppJSIsServedAndReferenced: the dialogs' scripts are one external
// file, served with a script content type and loaded by every page.
func TestAppJSIsServedAndReferenced(t *testing.T) {
	if _, err := staticFS.ReadFile("static/app.js"); err != nil {
		t.Fatalf("static/app.js: %v", err)
	}
	rec := get(t, "/static/app.js")
	if rec.Code != 200 || rec.Body.Len() == 0 {
		t.Fatalf("app.js: %d %d bytes", rec.Code, rec.Body.Len())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Fatalf("app.js Content-Type = %q", ct)
	}
	base, err := templateFS.ReadFile("templates/base.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(base), `<script src="/static/app.js" defer></script>`) {
		t.Error("base.html does not load /static/app.js")
	}
}

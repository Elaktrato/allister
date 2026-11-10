package web

import (
	"context"
	"html/template"
	"net/http"
)

// appearanceMarker is emitted by base.html where the appearance stylesheet
// link goes (filled by fillMarkers, like navMarker).
const appearanceMarker = "<!--allister-appearance-->"

// mountAppearance mounts the stylesheet and the appearance settings.
func (s *server) mountAppearance(mux *http.ServeMux) {
	mux.HandleFunc("GET /appearance.css", s.getAppearanceCSS)
}

// appearanceLink is the render-blocking link to the current stylesheet. Its
// URL carries the stylesheet's content hash, so a change takes effect on
// the next page and the file itself can be cached forever.
func (s *server) appearanceLink(ctx context.Context) []byte {
	_, v := s.appearance.Stylesheet(ctx)
	return []byte(`<link rel="stylesheet" href="/appearance.css?v=` + template.HTMLEscapeString(v) + `">`)
}

// getAppearanceCSS serves the generated stylesheet. The current version
// (?v= as linked) is immutable; any other URL answers the current CSS but
// must be revalidated, so an old link never pins stale colours.
func (s *server) getAppearanceCSS(w http.ResponseWriter, r *http.Request) {
	css, v := s.appearance.Stylesheet(r.Context())
	h := w.Header()
	h.Set("Content-Type", "text/css; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	if r.URL.Query().Get("v") == v {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	w.Write(css)
}

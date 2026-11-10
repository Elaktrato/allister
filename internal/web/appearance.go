package web

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/allister/allister/internal/appearance"
)

// appearanceMarker is emitted by base.html where the appearance stylesheet
// link goes (filled by fillMarkers, like navMarker).
const appearanceMarker = "<!--allister-appearance-->"

// mountAppearance mounts the stylesheet and the appearance settings.
func (s *server) mountAppearance(mux *http.ServeMux) {
	mux.HandleFunc("GET /appearance.css", s.getAppearanceCSS)
	mux.HandleFunc("GET /settings/appearance", s.getAppearance)
	mux.HandleFunc("POST /settings/appearance/preset", s.postAppearancePreset)
	mux.HandleFunc("POST /settings/appearance/palette", s.postAppearancePalette)
	mux.HandleFunc("POST /settings/appearance/palette/reset", s.postAppearancePaletteReset)
	mux.HandleFunc("POST /settings/appearance/font", s.postAppearanceFont)
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

// appearancePage is the data of Settings > Appearance.
type appearancePage struct {
	Error        string
	Presets      []appearance.PresetChoice
	Preset       string // the chosen preset (after fallback)
	PresetName   string
	System       bool
	Custom       bool
	OpenAdvanced bool
	Groups       []colourGroup
	Fonts        []fontRow
	Scales       []scaleRow
}

type colourGroup struct {
	Name string
	Rows []colourRow
}

// colourRow is one token of the editor: Value is a valid colour (the
// picker and the hidden was field), Hex the text field (after a refused
// save, what was typed).
type colourRow struct {
	Token, Label, Value, Hex string
	Warnings                 []string
}

type fontRow struct {
	ID, Name, Kind string
	Checked        bool
}

type scaleRow struct {
	ID, Label string
	Checked   bool
}

// fontKinds words a font's kind for the picker.
var fontKinds = map[string]string{"system": "system font", "bundled": "bundled", "uploaded": "uploaded"}

// appearancePage prepares the page for the stored settings; typed holds
// the text fields of a refused palette save (token -> text) to show again.
func (s *server) appearancePage(ctx context.Context, msg string, typed map[string]string) appearancePage {
	uploads := s.appearance.Uploads(ctx)
	res := appearance.Resolve(s.appearance.Config(ctx), uploads)
	p := appearancePage{Error: msg, Presets: appearance.PresetChoices(), Preset: res.Preset, System: res.Preset == appearance.SystemPreset && !res.Custom,
		Custom: res.Custom, OpenAdvanced: res.Custom || typed != nil}
	for _, c := range p.Presets {
		if c.ID == res.Preset {
			p.PresetName = c.Name
		}
	}
	warn := map[string][]string{}
	for _, w := range appearance.Warnings(res.Colors) {
		warn[w.Fg] = append(warn[w.Fg], w.Message())
	}
	for _, tk := range appearance.Tokens {
		if len(p.Groups) == 0 || p.Groups[len(p.Groups)-1].Name != tk.Group {
			p.Groups = append(p.Groups, colourGroup{Name: tk.Group})
		}
		row := colourRow{Token: tk.Name, Label: tk.Label, Value: res.Colors[tk.Name], Hex: res.Colors[tk.Name], Warnings: warn[tk.Name]}
		if v, ok := typed[tk.Name]; ok {
			row.Hex = v
		}
		g := &p.Groups[len(p.Groups)-1]
		g.Rows = append(g.Rows, row)
	}
	for _, f := range appearance.Fonts(uploads) {
		p.Fonts = append(p.Fonts, fontRow{ID: f.ID, Name: f.Name, Kind: fontKinds[f.Kind], Checked: f.ID == res.Font.ID})
	}
	for _, sc := range appearance.Scales {
		p.Scales = append(p.Scales, scaleRow{ID: sc.ID, Label: sc.Label, Checked: sc.ID == res.Scale.ID})
	}
	return p
}

func (s *server) getAppearance(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "settings_appearance", s.appearancePage(r.Context(), "", nil))
}

// appearanceDone answers a settings change: back to the page (303), the
// page again with the message for a refused value (422), or a 500.
func (s *server) appearanceDone(w http.ResponseWriter, r *http.Request, err error, typed map[string]string) {
	switch {
	case err == nil:
		http.Redirect(w, r, "/settings/appearance", http.StatusSeeOther)
	case errors.Is(err, appearance.ErrInvalid):
		s.renderStatus(w, r, http.StatusUnprocessableEntity, "settings_appearance", s.appearancePage(r.Context(), invalidMessage(err), typed))
	default:
		s.internalError(w, r, err)
	}
}

func (s *server) postAppearancePreset(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	s.appearanceDone(w, r, s.appearance.SetPreset(r.Context(), r.PostForm.Get("preset")), nil)
}

// postAppearancePalette saves the edited palette. Each token has three
// fields: the colour picker (color.<token>), the text field (hex.<token>)
// and the colour the page showed (was.<token>). A text field changed from
// what was shown wins (it was typed); otherwise the picker's colour is
// taken. Without JavaScript the two inputs are not linked, so this keeps
// either way of editing working. Any other field is refused.
func (s *server) postAppearancePalette(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	picked, typed, was := map[string]string{}, map[string]string{}, map[string]string{}
	var bad error
	for key, vals := range r.PostForm {
		kind, token, _ := strings.Cut(key, ".")
		var dst map[string]string
		switch kind {
		case "color":
			dst = picked
		case "hex":
			dst = typed
		case "was":
			dst = was
		}
		switch {
		case dst == nil || len(vals) != 1:
			bad = appearanceInvalid("the form has an unexpected field")
		case !appearance.IsToken(token):
			bad = appearanceInvalid("the form names an unknown colour")
		default:
			dst[token] = vals[0]
		}
	}
	if bad != nil {
		s.appearanceDone(w, r, bad, typed)
		return
	}
	chosen := map[string]string{}
	for _, tk := range appearance.Tokens {
		v, ok := picked[tk.Name]
		if t, typedOK := typed[tk.Name]; typedOK && strings.TrimSpace(t) != was[tk.Name] {
			v, ok = t, true
		}
		if ok {
			chosen[tk.Name] = v
		}
	}
	s.appearanceDone(w, r, s.appearance.SetPalette(r.Context(), chosen), typed)
}

func appearanceInvalid(msg string) error { return fmt.Errorf("%w: %s", appearance.ErrInvalid, msg) }

func (s *server) postAppearancePaletteReset(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	s.appearanceDone(w, r, s.appearance.ResetPalette(r.Context()), nil)
}

// postAppearanceFont saves the font and the text size together; both are
// checked before either is stored.
func (s *server) postAppearanceFont(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	ctx := r.Context()
	font, scale := r.PostForm.Get("font"), r.PostForm.Get("scale")
	if _, ok := appearance.FontByID(font, s.appearance.Uploads(ctx)); !ok {
		s.appearanceDone(w, r, appearanceInvalid("please choose one of the fonts"), nil)
		return
	}
	if !appearance.ValidScale(scale) {
		s.appearanceDone(w, r, appearanceInvalid("please choose one of the text sizes"), nil)
		return
	}
	err := s.appearance.SetFont(ctx, font)
	if err == nil {
		err = s.appearance.SetScale(ctx, scale)
	}
	s.appearanceDone(w, r, err, nil)
}

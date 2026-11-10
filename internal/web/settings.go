package web

import (
	"log"
	"net/http"

	"github.com/allister/allister/internal/settings"
)

type langOption struct{ Value, Label string }

var baseLangOptions = []langOption{
	{"ja", "Japanese (native)"},
	{"ja-latn", "Romaji"},
	{"en", "English"},
}

var langNames = map[string]string{"de": "Deutsch", "fr": "Français", "es": "Español"}

// titleLangOptions returns the built-in options followed by other language
// tags found in stored titles.
func (s *server) titleLangOptions(r *http.Request) ([]langOption, error) {
	known, err := s.deps.Settings.KnownLanguages(r.Context())
	if err != nil {
		return nil, err
	}
	opts := append([]langOption(nil), baseLangOptions...)
	have := map[string]bool{}
	for _, o := range opts {
		have[o.Value] = true
	}
	for _, l := range known {
		if have[l] {
			continue
		}
		label := l
		if n, ok := langNames[l]; ok {
			label = n
		}
		opts = append(opts, langOption{l, label})
	}
	return opts, nil
}

func (s *server) getSettings(w http.ResponseWriter, r *http.Request) {
	opts, err := s.titleLangOptions(r)
	if err != nil {
		log.Printf("settings: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, r, "settings", map[string]any{
		"Options":  opts,
		"Selected": s.deps.Settings.TitleLang(r.Context()),
		"Import":   s.importLinkLabel(),
		"Plugins":  s.pluginRows(),
		"Problems": s.deps.Registry.Problems(),
	})
}

func (s *server) postSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	lang := r.PostForm.Get("title_lang")
	opts, err := s.titleLangOptions(r)
	if err != nil {
		log.Printf("settings: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	valid := false
	for _, o := range opts {
		valid = valid || o.Value == lang
	}
	if !valid {
		http.Error(w, "unknown language", http.StatusBadRequest)
		return
	}
	if err := s.deps.Settings.Set(r.Context(), settings.KeyTitleLang, lang); err != nil {
		log.Printf("settings: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// importLinkLabel is the text of the Settings link to the import page, or ""
// when there is no import page or no enabled plugin offers an importer.
func (s *server) importLinkLabel() string {
	if s.deps.Imports == nil {
		return ""
	}
	switch opts := s.importerOptions(""); len(opts) {
	case 0:
		return ""
	case 1:
		return "Import from " + opts[0].Name
	}
	return "Import a file"
}

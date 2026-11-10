package web

// A plugin's own settings page, GET /settings/plugins/{id}: the title
// language of the plugin's items (POST /settings/plugins/{id}/title-language).
// Every choice is validated against what the plugin's manifest declares; the
// page answers for every installed plugin (enabled or not, like its Enable
// button), unknown ids get the 404 page and are never echoed.

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"slices"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// pluginSettingsURL is the settings page of plugin id.
func pluginSettingsURL(id string) string { return "/settings/plugins/" + url.PathEscape(id) }

type langOption struct{ Value, Label string }

// declaredLanguages are the title languages of every kind of m, in order,
// each once.
func declaredLanguages(m plugin.Manifest) []string {
	var out []string
	for _, k := range m.Kinds {
		for _, l := range k.TitleLanguages {
			if !slices.Contains(out, l) {
				out = append(out, l)
			}
		}
	}
	return out
}

// defaultTitleLang is m's title language before the user chooses: its first
// declared title language.
func defaultTitleLang(m plugin.Manifest) string {
	if langs := declaredLanguages(m); len(langs) > 0 {
		return langs[0]
	}
	return ""
}

// titleLang is the title language chosen for plugin m (its default when
// nothing is stored).
func (s *server) titleLang(ctx context.Context, m plugin.Manifest) string {
	if s.deps.Settings != nil {
		if v := s.deps.Settings.TitleLang(ctx, m.ID); v != "" {
			return v
		}
	}
	return defaultTitleLang(m)
}

// titleFallback is the declared fallback order of kind key of m, else of k
// (the kind of the page shown, for summaries naming another kind).
func titleFallback(m plugin.Manifest, k plugin.KindDef, key string) []string {
	if kd, ok := kindDef(m, key); ok && key != "" {
		return kd.TitleFallback
	}
	return k.TitleFallback
}

// resolveTitle is the title of ts shown on pages of plugin m for an item of
// kind key: the chosen language, then the kind's declared fallback, then
// the first stored title.
func (s *server) resolveTitle(ctx context.Context, m plugin.Manifest, k plugin.KindDef, key string, ts []plugin.Title) string {
	return library.ResolveTitle(ts, s.titleLang(ctx, m), titleFallback(m, k, key))
}

// titleLangOptions are the title languages plugin m offers: those its
// manifest declares, then other tags found in its own stored titles.
func (s *server) titleLangOptions(ctx context.Context, m plugin.Manifest) ([]langOption, error) {
	langs := declaredLanguages(m)
	known, err := s.deps.Settings.KnownLanguages(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	for _, l := range known {
		if !slices.Contains(langs, l) {
			langs = append(langs, l)
		}
	}
	opts := make([]langOption, 0, len(langs))
	for _, l := range langs {
		opts = append(opts, langOption{Value: l, Label: langLabel(l)})
	}
	return opts, nil
}

// settingsPlugin resolves {id} of a plugin settings route; an unknown id
// gets the 404 page.
func (s *server) settingsPlugin(w http.ResponseWriter, r *http.Request) (plugin.Manifest, bool) {
	m, ok := s.deps.Registry.Manifest(r.PathValue("id"))
	if !ok || len(m.Kinds) == 0 {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such plugin.", "")
		return plugin.Manifest{}, false
	}
	return m, true
}

// pluginSettingsView is the data of a plugin's settings page.
type pluginSettingsView struct {
	ID, Name    string
	Enabled     bool
	LangAction  string
	LangOptions []langOption
	Lang        string
}

func (s *server) getPluginSettings(w http.ResponseWriter, r *http.Request) {
	m, ok := s.settingsPlugin(w, r)
	if !ok {
		return
	}
	opts, err := s.titleLangOptions(r.Context(), m)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	v := pluginSettingsView{ID: m.ID, Name: m.Name, Enabled: s.deps.Registry.IsEnabled(m.ID),
		LangAction: pluginSettingsURL(m.ID) + "/title-language", LangOptions: opts, Lang: s.titleLang(r.Context(), m)}
	s.render(w, r, "settings_plugin", v)
}

// postTitleLanguage stores the plugin's title language (field title_lang),
// which must be one of the options the page offers.
func (s *server) postTitleLanguage(w http.ResponseWriter, r *http.Request) {
	m, ok := s.settingsPlugin(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	lang := r.PostForm.Get("title_lang")
	opts, err := s.titleLangOptions(r.Context(), m)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !slices.ContainsFunc(opts, func(o langOption) bool { return o.Value == lang }) {
		http.Error(w, "unknown language", http.StatusBadRequest)
		return
	}
	if err := s.deps.Settings.SetTitleLang(r.Context(), m.ID, lang); err != nil {
		log.Printf("settings %s: %v", m.ID, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, pluginSettingsURL(m.ID), http.StatusSeeOther)
}

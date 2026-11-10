package web

// A plugin's own settings page, GET /settings/plugins/{id}: the title
// language of the plugin's items (POST /settings/plugins/{id}/title-language)
// and, per kind, the columns of its list table (POST
// /settings/plugins/{id}/columns).
// Every choice is validated against what the plugin's manifest declares; the
// page answers for every installed plugin (enabled or not, like its Enable
// button), unknown ids get the 404 page and are never echoed.

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"

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
	// a stored choice that is no longer offered still applies to the titles,
	// so it is offered (and shown selected) rather than hidden
	if s.deps.Settings != nil {
		if cur := s.deps.Settings.TitleLang(ctx, m.ID); cur != "" && !slices.Contains(langs, cur) {
			langs = append(langs, cur)
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
	Columns     []columnsForm
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
		LangAction: pluginSettingsURL(m.ID) + "/title-language", LangOptions: opts, Lang: s.titleLang(r.Context(), m),
		Columns: s.columnsForms(r.Context(), m)}
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

// columnsForm is the column choice of one kind on the settings page.
type columnsForm struct {
	Kind, Heading, Action, Anchor string
	Rows                          []columnRow
	Custom                        bool // a choice is stored (Reset applies)
}

// columnRow is one offered column of a columnsForm: the shown ones first,
// in their order, then the others in the kind's order.
type columnRow struct {
	Key, Label, Width, Note string
	Shown, Up, Down         bool
}

// columnNotes are core notes on core columns, shown on the settings page.
var columnNotes = map[string]string{plugin.ColumnProgress: "includes the +1 button"}

// columnWidths describe the screen width a column needs.
var columnWidths = map[string]string{"sm": "from 640 px wide", "md": "from 768 px wide", "lg": "from 1024 px wide"}

// columnsForms builds the column forms of every kind of m.
func (s *server) columnsForms(ctx context.Context, m plugin.Manifest) []columnsForm {
	var out []columnsForm
	for _, k := range m.Kinds {
		offered := kindColumns(k)
		shown := s.visibleColumns(ctx, m, k)
		_, custom := s.deps.Settings.Columns(ctx, m.ID, k.Key)
		f := columnsForm{Kind: k.Key, Action: pluginSettingsURL(m.ID) + "/columns", Anchor: "columns-" + k.Key, Custom: custom}
		if len(m.Kinds) > 1 {
			f.Heading = k.LabelPlural
		}
		row := func(c listColumn, on bool) columnRow {
			return columnRow{Key: c.Key, Label: c.Label, Width: columnWidths[c.Hide], Note: columnNotes[c.Core], Shown: on}
		}
		for i, c := range shown {
			r := row(c, true)
			r.Up, r.Down = i > 0, i < len(shown)-1
			f.Rows = append(f.Rows, r)
		}
		for _, c := range offered {
			if !slices.ContainsFunc(shown, func(v listColumn) bool { return v.Key == c.Key }) {
				f.Rows = append(f.Rows, row(c, false))
			}
		}
		out = append(out, f)
	}
	return out
}

// postColumns stores the column choice of one kind (fields kind, order:
// every offered column key in the order shown, show: the checked ones),
// optionally moving one shown column (move=up:<key> or down:<key>, the
// form's arrow buttons), or removes it (reset=1). Every value must be a
// column the kind offers; anything else is refused with 400.
func (s *server) postColumns(w http.ResponseWriter, r *http.Request) {
	m, ok := s.settingsPlugin(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	kind := r.PostForm.Get("kind")
	k, ok := kindDef(m, kind)
	if !ok || kind == "" {
		http.Error(w, "unknown kind", http.StatusBadRequest)
		return
	}
	done := func() { http.Redirect(w, r, pluginSettingsURL(m.ID)+"#columns-"+k.Key, http.StatusSeeOther) }
	if r.PostForm.Get("reset") == "1" {
		if err := s.deps.Settings.ResetColumns(r.Context(), m.ID, k.Key); err != nil {
			s.internalError(w, r, err)
			return
		}
		done()
		return
	}
	keys, err := chosenColumns(kindColumns(k), r.PostForm["order"], r.PostForm["show"], r.PostForm.Get("move"))
	if err != "" {
		http.Error(w, err, http.StatusBadRequest)
		return
	}
	if err := s.deps.Settings.SetColumns(r.Context(), m.ID, k.Key, keys); err != nil {
		s.internalError(w, r, err)
		return
	}
	done()
}

// chosenColumns checks a posted column form against the offered columns and
// returns the shown keys in order, with move applied; a non-empty problem
// refuses the form.
func chosenColumns(offered []listColumn, order, show []string, move string) (keys []string, problem string) {
	isOffered := func(key string) bool {
		return slices.ContainsFunc(offered, func(c listColumn) bool { return c.Key == key })
	}
	seen := map[string]bool{}
	for _, key := range order {
		if !isOffered(key) {
			return nil, "unknown column"
		}
		if seen[key] {
			return nil, "column listed twice"
		}
		seen[key] = true
	}
	checked := map[string]bool{}
	for _, key := range show {
		if !isOffered(key) || !seen[key] || checked[key] {
			return nil, "unknown column"
		}
		checked[key] = true
	}
	keys = []string{}
	for _, key := range order {
		if checked[key] {
			keys = append(keys, key)
		}
	}
	if move == "" {
		return keys, ""
	}
	dir, key, _ := strings.Cut(move, ":")
	if (dir != "up" && dir != "down") || !isOffered(key) {
		return nil, "unknown move"
	}
	i := slices.Index(keys, key)
	switch {
	case i < 0: // a hidden column has no place to move
	case dir == "up" && i > 0:
		keys[i-1], keys[i] = keys[i], keys[i-1]
	case dir == "down" && i < len(keys)-1:
		keys[i+1], keys[i] = keys[i], keys[i+1]
	}
	return keys, ""
}

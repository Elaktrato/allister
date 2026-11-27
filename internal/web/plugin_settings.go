package web

// A plugin's own settings page, GET /settings/plugins/{id}: the title
// language of the plugin's items (POST /settings/plugins/{id}/title-language),
// what its list opens with — the view and the status (POST
// /settings/plugins/{id}/home), per kind, the columns of its list table
// (POST /settings/plugins/{id}/columns) and, for a plugin with an IdlePause
// importer, the idle cutoff in days (POST /settings/plugins/{id}/idle) and,
// for a plugin whose kinds declare an adult flag, whether its adult items are
// hidden (POST /settings/plugins/{id}/adult) and, for a plugin with a source,
// the bulk "Update from source" run (POST /settings/plugins/{id}/refresh,
// its state polled by GET /settings/plugins/{id}/refresh).
// Every choice is validated against what the plugin's manifest declares; the
// page answers for every installed plugin (enabled or not, like its Enable
// button), unknown ids get the 404 page and are never echoed.

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/allister/allister/internal/importer"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/settings"
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
	ID, Name     string
	Enabled      bool
	LangAction   string
	LangOptions  []langOption
	Lang         string
	HomeAction   string
	HomeView     string
	HomeStatus   string
	HomeStatuses []option
	Imports      []importForm
	Error        string
	Idle         *idleForm
	Adult        *adultForm
	Refresh      *refreshForm
	Columns      []columnsForm
	// MissingKeys names the plugin's sources that declare a key label and
	// have no effective key; OtherSources says the plugin also has a source
	// that needs no key, so the rest still works.
	MissingKeys  []string
	OtherSources bool
}

// idleForm is the idle cutoff of a plugin with an IdlePause importer.
type idleForm struct {
	Action    string
	Value     string // the stored days, or the refused input
	Max       int
	Importers string // the names of the IdlePause importers
	// Active, Paused, Kept name the kind's status labels in the help text.
	Active, Paused, Kept string
	Error                string
	On                   bool // the stored cutoff is above 0
}

// adultForm is the "Hide adult items" choice of a plugin whose kinds declare
// an adult flag.
type adultForm struct {
	Action string
	On     bool
}

// hasAdultField reports whether a kind of m declares an AdultField.
func hasAdultField(m plugin.Manifest) bool {
	return slices.ContainsFunc(m.Kinds, func(k plugin.KindDef) bool { return k.AdultField != "" })
}

// hideAdult reports whether plugin m hides its adult items: the setting is
// on and a kind of m declares an AdultField.
func (s *server) hideAdult(ctx context.Context, m plugin.Manifest) bool {
	return s.deps.Settings != nil && hasAdultField(m) && s.deps.Settings.HideAdult(ctx, m.ID)
}

// hiddenField is the field whose value "yes" hides an item of kind k of m
// (k's AdultField while m hides adult items), "" when nothing is hidden.
func (s *server) hiddenField(ctx context.Context, m plugin.Manifest, k plugin.KindDef) string {
	if k.AdultField == "" || !s.hideAdult(ctx, m) {
		return ""
	}
	return k.AdultField
}

// summaryHider reports for the summaries shown on a page of kind k of m
// (related items, recommendations, search results) whether each is hidden:
// its kind's (default k's) AdultField has the value "yes" while m hides
// adult items. The setting is read once.
func (s *server) summaryHider(ctx context.Context, m plugin.Manifest, k plugin.KindDef) func(plugin.ItemSummary) bool {
	if !s.hideAdult(ctx, m) {
		return func(plugin.ItemSummary) bool { return false }
	}
	return func(sum plugin.ItemSummary) bool {
		kd := k
		if sk, ok := kindDef(m, sum.Kind); ok && sum.Kind != "" {
			kd = sk
		}
		return kd.AdultField != "" && sum.Fields[kd.AdultField].Text == "yes"
	}
}

// hiddenItems returns the ids of the items of ms that are hidden (their
// kind's hidden field has the value "yes"), for views that read no Query.
func (s *server) hiddenItems(ctx context.Context, ms []plugin.Manifest) (map[int64]bool, error) {
	out := map[int64]bool{}
	for _, m := range ms {
		for _, k := range m.Kinds {
			f := s.hiddenField(ctx, m, k)
			if f == "" {
				continue
			}
			ids, err := s.deps.Library.FlaggedItems(ctx, m.ID, k.Key, f)
			if err != nil {
				return nil, err
			}
			for id := range ids {
				out[id] = true
			}
		}
	}
	return out, nil
}

// idleImporters names m's IdlePause importers ("" when it has none).
func idleImporters(m plugin.Manifest) string {
	var names []string
	for _, im := range m.Importers {
		if im.IdlePause {
			names = append(names, im.Name)
		}
	}
	return strings.Join(names, ", ")
}

// idleKind is the kind m's IdlePause importers bring rows of, when that is
// one kind: the manifest's only kind, or else the one kind every such
// importer's ResolveVia source declares. An importer does not declare the
// kinds of its rows, so a plugin with several kinds and no such source (or
// sources of several kinds) has none, and the help text uses the neutral
// core status names.
func idleKind(m plugin.Manifest) (plugin.KindDef, bool) {
	if len(m.Kinds) == 1 {
		return m.Kinds[0], true
	}
	key := ""
	for _, im := range m.Importers {
		if !im.IdlePause {
			continue
		}
		var kinds []string
		for _, src := range m.Sources {
			if src.ID == im.ResolveVia {
				kinds = src.Kinds
			}
		}
		if len(kinds) != 1 || (key != "" && kinds[0] != key) {
			return plugin.KindDef{}, false
		}
		key = kinds[0]
	}
	return kindDef(m, key)
}

// idleFormFor is m's idle form showing value (nil without an IdlePause
// importer).
func idleFormFor(m plugin.Manifest, value, msg string) *idleForm {
	names := idleImporters(m)
	if names == "" {
		return nil
	}
	label := statsStatusLabel
	if k, ok := idleKind(m); ok {
		label = func(st library.Status) string { return statusLabel(k, st) }
	}
	return &idleForm{Action: pluginSettingsURL(m.ID) + "/idle", Value: value, Max: settings.MaxIdleDays, Importers: names,
		Active: label(library.Active), Paused: label(library.Paused),
		Kept: label(library.Completed) + " and " + label(library.Dropped), Error: msg}
}

// importForm is one importer of the plugin on its settings page.
type importForm struct {
	ID, Name, Action string
	Account          bool // an AccountImporter: a name field, and it can repeat
	AccountLabel     string
	AccountValue     string
	Mode, Schedule   string
}

func (s *server) getPluginSettings(w http.ResponseWriter, r *http.Request) {
	m, ok := s.settingsPlugin(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	opts, err := s.titleLangOptions(ctx, m)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	v, err := s.pluginSettingsData(r, m)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	v.LangOptions, v.Lang = opts, s.titleLang(ctx, m)
	s.render(w, r, "settings_plugin", v)
}

// pluginSettingsData builds the settings page of m (lang options come from
// the caller, ordered by the stored languages).
func (s *server) pluginSettingsData(r *http.Request, m plugin.Manifest) (pluginSettingsView, error) {
	ctx := r.Context()
	home := []option{{Value: "", Label: "Every status"}}
	for _, st := range listTabOrder {
		home = append(home, option{Value: string(st), Label: statusLabel(m.Kinds[0], st)})
	}
	v := pluginSettingsView{ID: m.ID, Name: m.Name, Enabled: s.deps.Registry.IsEnabled(m.ID),
		LangAction: pluginSettingsURL(m.ID) + "/title-language",
		HomeAction: pluginSettingsURL(m.ID) + "/home", HomeView: s.deps.Settings.HomeView(ctx, m.ID),
		HomeStatus: s.homeStatus(ctx, m), HomeStatuses: home,
		Columns: s.columnsForms(ctx, m)}
	for i := range v.HomeStatuses {
		v.HomeStatuses[i].Selected = v.HomeStatuses[i].Value == v.HomeStatus
	}
	for _, im := range m.Importers {
		f := importForm{ID: im.ID, Name: im.Name, Action: pluginSettingsURL(m.ID) + "/import/" + im.ID,
			Account: im.AccountLabel != "", AccountLabel: im.AccountLabel,
			Mode: s.deps.Settings.ImportMode(ctx, m.ID, im.ID), Schedule: s.deps.Settings.ImportSchedule(ctx, m.ID, im.ID)}
		if f.Account {
			f.AccountValue = s.deps.Settings.ImportAccount(ctx, m.ID, im.ID)
		}
		v.Imports = append(v.Imports, f)
	}
	days := s.deps.Settings.IdleDays(ctx, m.ID)
	if v.Idle = idleFormFor(m, strconv.Itoa(days), ""); v.Idle != nil {
		v.Idle.On = days > 0
	}
	if hasAdultField(m) {
		v.Adult = &adultForm{Action: pluginSettingsURL(m.ID) + "/adult", On: s.hideAdult(ctx, m)}
	}
	var err error
	if v.Refresh, err = s.refreshFormFor(ctx, m); err != nil {
		return v, err
	}
	v.MissingKeys, v.OtherSources, err = s.sourceKeyState(ctx, m)
	return v, err
}

// postHideAdult stores whether the plugin hides its adult items (field
// hide_adult, "1" for on; absent is off) for a plugin whose kinds declare an
// adult flag; other plugins get the 404 page.
func (s *server) postHideAdult(w http.ResponseWriter, r *http.Request) {
	m, ok := s.settingsPlugin(w, r)
	if !ok {
		return
	}
	if !hasAdultField(m) {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "This plugin has no such setting.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var on bool
	switch r.PostForm.Get("hide_adult") {
	case "1":
		on = true
	case "":
	default:
		http.Error(w, "bad value", http.StatusBadRequest)
		return
	}
	if err := s.deps.Settings.SetHideAdult(r.Context(), m.ID, on); err != nil {
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, pluginSettingsURL(m.ID)+"#adult", http.StatusSeeOther)
}

// postIdleDays stores the idle cutoff (field idle_days, a whole number of
// days from 0 to settings.MaxIdleDays; 0 turns the rule off) of a plugin
// with an IdlePause importer; other plugins get the 404 page. Invalid input
// answers the page with a message and 422.
func (s *server) postIdleDays(w http.ResponseWriter, r *http.Request) {
	m, ok := s.settingsPlugin(w, r)
	if !ok {
		return
	}
	if idleImporters(m) == "" {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "This plugin has no such setting.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	raw := strings.TrimSpace(r.PostForm.Get("idle_days"))
	days, err := strconv.Atoi(raw)
	if err != nil || days < 0 || days > settings.MaxIdleDays || raw != strconv.Itoa(days) {
		v, verr := s.pluginSettingsData(r, m)
		if verr != nil {
			s.internalError(w, r, verr)
			return
		}
		opts, verr := s.titleLangOptions(ctx, m)
		if verr != nil {
			s.internalError(w, r, verr)
			return
		}
		v.LangOptions, v.Lang = opts, s.titleLang(ctx, m)
		on := v.Idle.On
		v.Idle = idleFormFor(m, plugin.SingleLine(raw, 20),
			"Enter a whole number of days from 0 to "+strconv.Itoa(settings.MaxIdleDays)+".")
		v.Idle.On = on
		s.renderStatus(w, r, http.StatusUnprocessableEntity, "settings_plugin", v)
		return
	}
	if err := s.deps.Settings.SetIdleDays(ctx, m.ID, days); err != nil {
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, pluginSettingsURL(m.ID)+"#idle", http.StatusSeeOther)
}

// sourceKeyState names m's sources that declare a key label and have no
// effective key, and reports whether m also has a source that needs no key.
func (s *server) sourceKeyState(ctx context.Context, m plugin.Manifest) (missing []string, other bool, err error) {
	seen := map[string]bool{}
	for _, src := range m.Sources {
		if seen[src.ID] {
			continue
		}
		seen[src.ID] = true
		if src.KeyLabel == "" {
			other = true
			continue
		}
		ok, err := s.sourceConfigured(ctx, src)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			missing = append(missing, src.Name)
		}
	}
	return missing, other, nil
}

// postPluginImport stores an importer's settings - the rule for existing
// entries, and for an account importer the name and the repeat schedule -
// and starts an import: an account import right away, or the uploaded file.
func (s *server) postPluginImport(w http.ResponseWriter, r *http.Request) {
	m, ok := s.settingsPlugin(w, r)
	if !ok {
		return
	}
	if s.deps.Imports == nil {
		http.NotFound(w, r)
		return
	}
	impID := r.PathValue("importer")
	var info plugin.ImporterInfo
	found := false
	for _, im := range m.Importers {
		if im.ID == impID {
			info, found = im, true
		}
	}
	if !found {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such importer.", "")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBody)
	if err := r.ParseMultipartForm(maxImportMemory); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "The file is larger than 20 MiB.", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "bad request", http.StatusBadRequest)
		}
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	ctx := r.Context()
	fail := func(msg string) {
		v, err := s.pluginSettingsData(r, m)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		opts, err := s.titleLangOptions(ctx, m)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		v.LangOptions, v.Lang, v.Error = opts, s.titleLang(ctx, m), msg
		s.render(w, r, "settings_plugin", v)
	}
	mode := r.FormValue("mode")
	switch mode {
	case "":
		mode = importer.ModeSkip
	case importer.ModeSkip, importer.ModeNewer, importer.ModeAlways:
	default:
		http.Error(w, "unknown import mode", http.StatusBadRequest)
		return
	}
	if err := s.deps.Settings.SetImportMode(ctx, m.ID, impID, mode); err != nil {
		s.internalError(w, r, err)
		return
	}
	if info.AccountLabel != "" {
		account := strings.TrimSpace(r.FormValue("account"))
		schedule := r.FormValue("schedule")
		switch schedule {
		case "", "off", "daily", "weekly":
		default:
			http.Error(w, "unknown schedule", http.StatusBadRequest)
			return
		}
		for _, set := range []func() error{
			func() error { return s.deps.Settings.SetImportAccount(ctx, m.ID, impID, account) },
			func() error { return s.deps.Settings.SetImportSchedule(ctx, m.ID, impID, schedule) },
		} {
			if err := set(); err != nil {
				s.internalError(w, r, err)
				return
			}
		}
		if account == "" {
			http.Redirect(w, r, pluginSettingsURL(m.ID), http.StatusSeeOther)
			return
		}
		// saving a schedule starts the repeat chain (the job re-enqueues)
		if schedule != "" && schedule != "off" && s.deps.ScheduleImport != nil {
			kind, payload := s.deps.ScheduleImport(m.ID, impID)
			if err := s.deps.Jobs.Enqueue(ctx, kind, payload); err != nil {
				log.Printf("settings %s: schedule import: %v", m.ID, err)
			}
		}
		_, err := s.deps.Imports.SubmitAccount(ctx, m.ID, impID, account, mode)
		var active *importer.ActiveError
		switch {
		case errors.As(err, &active):
			http.Redirect(w, r, "/settings/import", http.StatusSeeOther)
		case err != nil:
			fail(err.Error())
		default:
			http.Redirect(w, r, "/settings/import", http.StatusSeeOther)
		}
		return
	}
	fh := firstFile(r, "file")
	if fh == nil {
		fail("Please choose the file to upload.")
		return
	}
	f, err := fh.Open()
	if err != nil {
		fail("The file cannot be read.")
		return
	}
	defer f.Close()
	_, err = s.deps.Imports.SubmitMode(ctx, m.ID, impID, fh.Filename, mode, f)
	var bad *importer.BadFileError
	var active *importer.ActiveError
	switch {
	case errors.As(err, &bad):
		fail(bad.Reason)
	case errors.As(err, &active):
		http.Redirect(w, r, "/settings/import", http.StatusSeeOther)
	case err != nil:
		fail(err.Error())
	default:
		http.Redirect(w, r, "/settings/import", http.StatusSeeOther)
	}
}

// postHomeSettings stores the plugin's home settings (fields view and
// statuses): the view its list opens in (cards or table) and the status it
// opens with ("" for every status).
func (s *server) postHomeSettings(w http.ResponseWriter, r *http.Request) {
	m, ok := s.settingsPlugin(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	view := r.PostForm.Get("view")
	if view == "" {
		view = "cards" // no radio chosen: the default
	}
	if view != "cards" && view != "table" {
		http.Error(w, "unknown view", http.StatusBadRequest)
		return
	}
	st := r.PostForm.Get("statuses")
	if st != "" && !slices.Contains(library.Statuses(), library.Status(st)) {
		http.Error(w, "unknown status", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	if err := s.deps.Settings.SetHomeView(ctx, m.ID, view); err != nil {
		log.Printf("settings %s: %v", m.ID, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := s.deps.Settings.SetHomeStatuses(ctx, m.ID, st); err != nil {
		log.Printf("settings %s: %v", m.ID, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, pluginSettingsURL(m.ID), http.StatusSeeOther)
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

// refreshForm is the bulk "Update from source" section of a plugin with a
// source: the running run's progress, else the last result and the button
// (or, after the button, the confirmation naming Count).
type refreshForm struct {
	Action      string
	PageURL     string // the settings page (the confirmation's Cancel)
	Running     bool
	Disabled    bool // the plugin is disabled: its jobs wait
	Done, Total int
	Last        *library.RefreshRun // the newest finished run, nil when none
	Credential  bool                // Last stopped on a missing credential
	Confirm     bool
	Count       int
	Error       string
}

// refreshOffered reports whether m's settings offer the bulk update: m
// declares a source and the server can enqueue its jobs.
func (s *server) refreshOffered(m plugin.Manifest) bool {
	return len(m.Sources) > 0 && s.deps.RefreshJobs != nil && s.deps.Jobs != nil && s.deps.Library != nil
}

// refreshFormFor is m's refresh section (nil when it is not offered).
func (s *server) refreshFormFor(ctx context.Context, m plugin.Manifest) (*refreshForm, error) {
	if !s.refreshOffered(m) {
		return nil, nil
	}
	run, err := s.deps.Library.LatestRefreshRun(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	f := &refreshForm{Action: pluginSettingsURL(m.ID) + "/refresh", PageURL: pluginSettingsURL(m.ID), Disabled: !s.deps.Registry.IsEnabled(m.ID)}
	switch {
	case run == nil:
	case run.FinishedAt == nil:
		f.Running, f.Done, f.Total = true, min(run.Done(), run.Total), run.Total
	default:
		f.Last, f.Credential = run, run.Stopped == library.RefreshStoppedCredential
	}
	return f, nil
}

// getPluginRefresh answers the refresh section alone to htmx (its progress
// poll); other requests go to the settings page.
func (s *server) getPluginRefresh(w http.ResponseWriter, r *http.Request) {
	m, ok := s.settingsPlugin(w, r)
	if !ok {
		return
	}
	if !s.refreshOffered(m) {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "This plugin has no source to update from.", "")
		return
	}
	if !isPartialRequest(r) {
		http.Redirect(w, r, pluginSettingsURL(m.ID)+"#refresh", http.StatusSeeOther)
		return
	}
	f, err := s.refreshFormFor(r.Context(), m)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	w.Header().Set("Vary", "HX-Request, HX-History-Restore-Request")
	s.renderPartial(w, "settings_plugin", "plugin_refresh", f)
}

// postPluginRefresh starts the plugin's bulk "Update from source" run. The
// first post answers the settings page with the confirmation naming the
// number of items it will ask; the confirmation posts confirm=1, which
// starts the run (one job per item) and redirects to the page's refresh
// section. A run that is still going is refused with 409.
func (s *server) postPluginRefresh(w http.ResponseWriter, r *http.Request) {
	m, ok := s.settingsPlugin(w, r)
	if !ok {
		return
	}
	if !s.refreshOffered(m) {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "This plugin has no source to update from.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	page := func(status int, mod func(*refreshForm)) {
		v, err := s.pluginSettingsData(r, m)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		opts, err := s.titleLangOptions(ctx, m)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		v.LangOptions, v.Lang = opts, s.titleLang(ctx, m)
		mod(v.Refresh)
		s.renderStatus(w, r, status, "settings_plugin", v)
	}
	const running = "An update from source is already running."
	if r.PostForm.Get("confirm") != "1" {
		n, err := s.deps.Library.RefreshCandidates(ctx, m.ID)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		status := http.StatusOK
		latest, err := s.deps.Library.LatestRefreshRun(ctx, m.ID)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		if latest != nil && latest.FinishedAt == nil {
			status = http.StatusConflict
		}
		page(status, func(f *refreshForm) {
			if f.Running {
				f.Error = running
				return
			}
			f.Confirm, f.Count = true, n
		})
		return
	}
	run, ids, err := s.deps.Library.StartRefreshRun(ctx, m.ID)
	if errors.Is(err, library.ErrRefreshRunning) {
		page(http.StatusConflict, func(f *refreshForm) { f.Error = running })
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	job := s.deps.RefreshJobs(m.ID, run.ID)
	for _, id := range ids {
		kind, payload := job(id)
		if err := s.deps.Jobs.Enqueue(ctx, kind, payload); err != nil {
			// the jobs already queued record their items as failed without
			// asking the source, and the run finishes once they are done
			if serr := s.deps.Library.StopRefreshRun(context.WithoutCancel(ctx), run.ID, "enqueue"); serr != nil {
				log.Printf("settings %s: stop refresh: %v", logSafe(m.ID), serr)
			}
			s.internalError(w, r, err)
			return
		}
	}
	http.Redirect(w, r, pluginSettingsURL(m.ID)+"#refresh", http.StatusSeeOther)
}

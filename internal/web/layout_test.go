package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
	"github.com/allister/allister/internal/plugins/anime/anilist"
)

// mainNav is the sidebar navigation of a full page: the <nav aria-label="Main">
// element inside the header.
func mainNav(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, `<nav aria-label="Main"`)
	if i < 0 {
		t.Fatalf("no main nav in %s", body)
	}
	j := strings.Index(body[i:], "</nav>")
	return body[i : i+j]
}

// navRow returns the <a ...>...</a> of the nav row linking href.
func navRow(t *testing.T, nav, href string) string {
	t.Helper()
	i := strings.Index(nav, `href="`+href+`"`)
	if i < 0 {
		t.Fatalf("no nav row %s in %s", href, nav)
	}
	start := strings.LastIndex(nav[:i], "<a ")
	return nav[start : i+strings.Index(nav[i:], "</a>")]
}

func TestSidebarOneRowPerEnabledPluginWithIcons(t *testing.T) {
	book := plugintest.BookPlugin() // declares the icon "menu_book"; tv declares none
	e := newEnvWith(t, withPlugins(book, plugintest.TVPlugin()))
	body := e.get("/settings").Body.String()
	nav := mainNav(t, body)
	if strings.Count(nav, "<a ") != 4 {
		t.Fatalf("want 3 plugin rows and Settings: %s", nav)
	}
	// manifest (registration) order, Settings last
	last := -1
	for _, href := range []string{"/anime", "/book", "/tv", "/settings"} {
		i := strings.Index(nav, `href="`+href+`"`)
		if i < last {
			t.Errorf("%s out of order in %s", href, nav)
		}
		last = i
	}
	for _, r := range []struct{ href, icon, label string }{
		{"/anime", "live_tv", "Anime"}, {"/book", "menu_book", "Books"}, {"/tv", "category", "Shows"}, {"/settings", "settings", "Settings"},
	} {
		row := navRow(t, nav, r.href)
		mustIcon(t, row, r.icon)
		mustContain(t, row, `<span>`+r.label+`</span>`)
	}
	// Settings sits in its own list at the bottom of the sidebar
	if i, j := strings.LastIndex(nav, "<ul"), strings.Index(nav, `href="/settings"`); i < 0 || i > j || strings.Index(nav, `href="/tv"`) > i {
		t.Errorf("Settings is not in the bottom list: %s", nav)
	}

	e.disable(t, "book")
	nav = mainNav(t, e.get("/settings").Body.String())
	mustNotContain(t, nav, `href="/book"`, "menu_book", "Books")
	mustContain(t, nav, `href="/anime"`, `href="/tv"`, `href="/settings"`)
}

// TestSidebarIconComesFromTheManifestOnly: an icon is plain data, escaped
// like every other manifest value (Validate refuses anything but a name; the
// template would escape it anyway).
func TestSidebarIconIsEscapedData(t *testing.T) {
	e := newEnv(t)
	m, _ := e.reg.Manifest("anime")
	if m.Icon != "live_tv" || plugin.Validate(m) != nil {
		t.Fatalf("anime icon %q", m.Icon)
	}
	v := e.srv.navItems(httptestRequest("/anime"))
	if len(v.Items) != 1 || v.Items[0].Icon != "live_tv" || !v.Items[0].Active || v.Settings.Active {
		t.Fatalf("nav %+v", v)
	}
}

// TestAriaCurrentExactlyOncePerPage: exactly one aria-current="page" on every
// full page, on the sidebar row of the area the page belongs to. In-page tab
// groups (status, kinds, settings sections) use aria-current="true".
func TestAriaCurrentExactlyOncePerPage(t *testing.T) {
	e := newEnvWith(t, withPlugins(plugintest.BookPlugin()))
	id := e.addEntry(t, anilist.AniList, "1535", nil, library.Active, 1, titleEn("Death Note")...)
	e.withCast(t, id, anilist.CastMember{Character: pperson("c1", "Light", "", ""), Role: "MAIN"})
	for _, p := range []struct{ path, area string }{
		{"/anime", "/anime"}, {"/anime?status=active", "/anime"}, {"/anime?q=death&f.type=TV", "/anime"},
		{"/anime/anilist/1535", "/anime"}, {"/anime/search", "/anime"}, {"/book", "/book"},
		{"/list/" + idStr(id) + "/edit", "/anime"}, {e.personPath(t, "c1"), "/anime"},
		{"/settings", "/settings"}, {"/settings/plugins", "/settings"}, {"/settings/backup", "/settings"},
	} {
		rec := e.get(p.path)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: %d", p.path, rec.Code)
			continue
		}
		b := rec.Body.String()
		if n := strings.Count(b, `aria-current="page"`); n != 1 {
			t.Errorf("%s: %d aria-current=page", p.path, n)
			continue
		}
		if row := navRow(t, mainNav(t, b), p.area); !strings.Contains(row, `aria-current="page"`) {
			t.Errorf("%s: row %s not current: %s", p.path, p.area, row)
		}
	}
}

// mustIcon asserts that s shows the icon name as a decorative icon element.
func mustIcon(t *testing.T, s, name string) {
	t.Helper()
	if !regexp.MustCompile(`<span class="icon(?: [^"]*)?" aria-hidden="true">` + name + `</span>`).MatchString(s) {
		t.Errorf("no icon %q in %s", name, s)
	}
}

// iconSpan matches every element that carries the icon class.
var iconSpan = regexp.MustCompile(`<[a-z]+[^>]*class="(?:[^"]* )?icon(?: [^"]*)?"[^>]*>`)

// TestIconsAreDecorativeAndPagesHaveNoInlineScript: every icon is
// aria-hidden (its text label is always present), and no page carries an
// inline <script> or on* handler attribute (CSP-friendly).
func TestIconsAreDecorativeAndPagesHaveNoInlineScript(t *testing.T) {
	e := newEnv(t)
	e.addEntry(t, anilist.AniList, "1535", nil, library.Active, 1, titleEn("Death Note")...)
	for _, p := range []string{"/anime", "/anime?sort=title&f.type=TV", "/anime/anilist/1535", "/anime/search", "/settings", "/settings/plugins", "/settings/backup"} {
		b := e.get(p).Body.String()
		icons := iconSpan.FindAllString(b, -1)
		if len(icons) < 2 {
			t.Errorf("%s: only %d icons", p, len(icons))
		}
		for _, s := range icons {
			if !strings.Contains(s, `aria-hidden="true"`) {
				t.Errorf("%s: icon without aria-hidden: %s", p, s)
			}
		}
		for _, s := range regexp.MustCompile(`<script[^>]*>`).FindAllString(b, -1) {
			if !strings.Contains(s, " src=") {
				t.Errorf("%s: inline script %s", p, s)
			}
		}
		if m := regexp.MustCompile(`\son[a-z]+=`).FindString(b); m != "" {
			t.Errorf("%s: inline handler %q", p, m)
		}
	}
}

// settingsTabs is the settings section navigation of a page.
func settingsTabs(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, `<nav aria-label="Settings sections"`)
	if i < 0 {
		t.Fatalf("no settings tabs in %s", body)
	}
	return body[i : i+strings.Index(body[i:], "</nav>")]
}

func TestSettingsSectionTabs(t *testing.T) {
	e := newImpEnv(t, notFoundExcept())
	pages := []struct{ path, label, icon string }{
		{"/settings", "General", "tune"}, {"/settings/plugins", "Plugins", "extension"},
		{"/settings/import", "Import", "upload_file"}, {"/settings/backup", "Backup", "backup"},
	}
	for _, cur := range pages {
		b := e.get(cur.path, false).Body.String()
		tabs := settingsTabs(t, b)
		last := -1
		for _, p := range pages {
			i := strings.Index(tabs, `href="`+p.path+`"`)
			if i < last {
				t.Errorf("%s: tab %s missing or out of order", cur.path, p.path)
			}
			last = i
			row := navRow(t, tabs, p.path)
			mustIcon(t, row, p.icon)
			mustContain(t, row, `<span>`+p.label+`</span>`)
			if (p.path == cur.path) != strings.Contains(row, `aria-current="true"`) {
				t.Errorf("%s: tab %s current state wrong: %s", cur.path, p.path, row)
			}
		}
		if strings.Count(tabs, "aria-current") != 1 {
			t.Errorf("%s: %s", cur.path, tabs)
		}
		// the sidebar marks Settings as the page's area
		if row := navRow(t, mainNav(t, b), "/settings"); !strings.Contains(row, `aria-current="page"`) {
			t.Errorf("%s: Settings not current in the sidebar", cur.path)
		}
	}
	// without an import page there is no Import tab
	plain := newEnv(t)
	mustNotContain(t, settingsTabs(t, plain.get("/settings").Body.String()), `href="/settings/import"`, "Import")
}

func TestSettingsPluginsPage(t *testing.T) {
	e := newEnvWith(t, withPlugins(plugintest.BookPlugin()))
	general := e.get("/settings").Body.String()
	// the title language moved to each plugin's page; General links them
	mustNotContain(t, general, `name="title_lang"`, `data-plugin=`)
	mustContain(t, general, `href="/settings/plugins/anime"`, `href="/settings/plugins/book"`)
	rec := e.get("/settings/plugins")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	b := rec.Body.String()
	mustContain(t, b, `data-plugin="anime"`, `data-plugin="book"`, `action="/settings/plugins/book"`, "<title>Plugins - Allister</title>")
	mustNotContain(t, b, `name="title_lang"`)
	got := e.do("POST", "/settings/plugins/book", map[string][]string{"enabled": {"0"}}, false)
	if got.Code != http.StatusSeeOther || got.Header().Get("Location") != "/settings/plugins" {
		t.Fatalf("toggle: %d %q", got.Code, got.Header().Get("Location"))
	}
	if e.reg.IsEnabled("book") {
		t.Fatal("not disabled")
	}
}

func httptestRequest(path string) *http.Request {
	return httptest.NewRequest(http.MethodGet, path, nil)
}

// TestIconsSizedWithoutLineHeight: text-sm, text-lg and friends set a
// line-height as well, which moves an icon's glyph out of its one-em box;
// icons are sized with font-size-only classes (text-[1rem]).
func TestIconsSizedWithoutLineHeight(t *testing.T) {
	ents, err := templateFS.ReadDir("templates")
	if err != nil {
		t.Fatal(err)
	}
	bad := regexp.MustCompile(`class="(?:[^"]* )?icon [^"]*\btext-(?:xs|sm|base|lg|xl|[2-9]xl)\b`)
	for _, e := range ents {
		b, _ := templateFS.ReadFile("templates/" + e.Name())
		if m := bad.FindString(string(b)); m != "" {
			t.Errorf("%s: %s", e.Name(), m)
		}
	}
}

// TestSettingsTabsOnResultPages: a settings section rendered as the answer
// of a POST below its path (a refused restore, a refused resume) still
// marks its tab and the sidebar's Settings row, exactly once each.
func TestSettingsTabsOnResultPages(t *testing.T) {
	check := func(what string, rec *httptest.ResponseRecorder, tab string) {
		t.Helper()
		b := rec.Body.String()
		if !strings.Contains(b, "<html") {
			t.Fatalf("%s: not a page (%d): %s", what, rec.Code, b)
		}
		if n := strings.Count(b, `aria-current="page"`); n != 1 {
			t.Errorf("%s: %d aria-current=page", what, n)
		}
		if row := navRow(t, mainNav(t, b), "/settings"); !strings.Contains(row, `aria-current="page"`) {
			t.Errorf("%s: Settings not current", what)
		}
		tabs := settingsTabs(t, b)
		if strings.Count(tabs, `aria-current="true"`) != 1 || !strings.Contains(navRow(t, tabs, tab), `aria-current="true"`) {
			t.Errorf("%s: tab %s not current: %s", what, tab, tabs)
		}
	}
	e := newEnv(t)
	rec := e.do("POST", "/settings/backup/restore", url.Values{}, false)
	if rec.Code < 400 {
		t.Fatalf("restore without a file: %d", rec.Code)
	}
	check("restore", rec, "/settings/backup")
	ie := newImpEnv(t, notFoundExcept())
	rec = ie.postPath("/settings/import/resume")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("resume: %d", rec.Code)
	}
	check("resume", rec, "/settings/import")
	for p, want := range map[string]string{"/settings": "/settings", "/settings/plugins": "/settings/plugins", "/settings/backup/restore": "/settings/backup",
		"/settings/import/resume": "/settings/import", "/settings/importx": "/settings", "/settings/plugins/anime": "/settings/plugins"} {
		if got := settingsSection(p); got != want {
			t.Errorf("settingsSection(%q) = %q, want %q", p, got, want)
		}
	}
}

// TestPluginHintsLeadToSettingsPlugins: with no plugin enabled the home page
// and the import page's hints lead to Settings > Plugins, where a plugin is
// switched on.
func TestPluginHintsLeadToSettingsPlugins(t *testing.T) {
	e := newImpEnv(t, notFoundExcept())
	if err := e.reg.SetEnabled(context.Background(), "anime", false); err != nil {
		t.Fatal(err)
	}
	rec := e.get("/", false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings/plugins" {
		t.Fatalf("home: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	mustContain(t, e.get("/settings/import", false).Body.String(), `Enable a plugin in <a href="/settings/plugins"`)
}

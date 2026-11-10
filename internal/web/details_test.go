package web

import (
	"context"
	"html"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// titleRows encodes title rows (lang, text pairs) the way the edit form
// posts them: parallel title_lang and title values.
func titleRows(pairs ...string) url.Values {
	v := url.Values{}
	for i := 0; i+1 < len(pairs); i += 2 {
		v.Add("title_lang", pairs[i])
		v.Add("title", pairs[i+1])
	}
	return v
}

func with(v url.Values, kv ...string) url.Values {
	out := url.Values{}
	for k, x := range v {
		out[k] = append([]string(nil), x...)
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out.Set(kv[i], kv[i+1])
	}
	return out
}

// manualAnimeItem saves manual anime "m1" in the list (two titles, type TV,
// 12 episodes, entry at episode 3).
func (e *env) manualAnimeItem(t *testing.T, cover string) int64 {
	t.Helper()
	d := plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "manual", ID: "m1", Kind: "anime",
		Titles:   []plugin.Title{{Lang: "ja-latn", Text: "Old Name"}, {Lang: "en", Text: "Old EN"}},
		Fields:   map[string]plugin.FieldValue{"type": {Text: "TV"}, "episodes": {Num: num(12)}},
		CoverURL: cover}}
	return e.addFixture(t, "anime", "anime", d, library.Entry{Status: library.Active, Rating: ip(8), Comment: "keep",
		Progress: map[string]float64{"episode": 3}}, library.WithSyncState("done"))
}

func (e *env) item(t *testing.T, id int64) library.Item {
	t.Helper()
	it, err := e.lib.Item(context.Background(), id)
	if err != nil || it == nil {
		t.Fatalf("item %d: %v %v", id, it, err)
	}
	return *it
}

func TestEditDetailsForManualUnmatchedAndSourcedItems(t *testing.T) {
	e := newFixtureEnv(t)
	manual := e.manualAnimeItem(t, "")
	unmatched := e.saveFixture(t, "anime", "anime", plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "manual", ID: "u1",
		Titles: ttl("Imported"), Fields: map[string]plugin.FieldValue{"type": {Text: "OVA"}}}, ExternalIDs: map[string]string{"mal": "5114"}},
		library.WithSyncState("done"))
	sourced := e.saveFixture(t, "anime", "anime", plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "anilist", ID: "1535",
		Titles: []plugin.Title{{Lang: "ja", Text: "デスノート"}}, Fields: map[string]plugin.FieldValue{"type": {Text: "TV"}}}})
	show := e.saveFixture(t, "tv", "show", tvShow("5", "The Show"))
	for path, id := range map[string]int64{"/anime/manual/m1": manual, "/anime/manual/u1": unmatched,
		"/anime/anilist/1535": sourced, "/tv/tvdata/5": show} {
		before := e.item(t, id)
		rec := e.get(path + "/edit")
		if rec.Code != 200 || rec.Header().Get("Vary") != "HX-Request" {
			t.Fatalf("%s: GET %d", path, rec.Code)
		}
		b := rec.Body.String()
		mustContain(t, b, `action="`+path+`/edit"`, `enctype="multipart/form-data"`, `name="field.type"`,
			`value="`+before.Titles[0].Text+`"`, "Edit details", `href="`+path+`"`)
		mustNotContain(t, b, `name="status"`, `name="rating"`, `name="progress`)
		mustContain(t, e.get(path).Body.String(), "Edit details", `href="`+path+`/edit?return=`)
		form := with(titleRows("en", "Renamed "+path), "field.type", before.Fields["type"].Text)
		rec = e.postMultipart(t, path+"/edit", form, nil)
		if rec.Code != 303 || rec.Header().Get("Location") != path {
			t.Fatalf("%s: POST %d %q %s", path, rec.Code, rec.Header().Get("Location"), rec.Body.String())
		}
		after := e.item(t, id)
		if !reflect.DeepEqual(after.Titles, []plugin.Title{{Lang: "en", Text: "Renamed " + path}}) ||
			after.Source != before.Source || after.SourceID != before.SourceID || !reflect.DeepEqual(after.ExternalIDs, before.ExternalIDs) {
			t.Fatalf("%s: %+v", path, after)
		}
		mustContain(t, e.get(path).Body.String(), "Renamed "+path)
	}
	// unsaved, unknown and malformed items: 404, nothing saved
	for _, path := range []string{"/anime/anilist/999/edit", "/anime/manual/nope/edit", "/anime/mal/5/edit", "/tv/tvdata/77/edit"} {
		if c := e.get(path).Code; c != 404 {
			t.Errorf("GET %s: %d", path, c)
		}
		if c := e.postMultipart(t, path, titleRows("en", "x"), nil).Code; c != 404 {
			t.Errorf("POST %s: %d", path, c)
		}
	}
	if e.tv.Calls(plugintest.OpDetails) != 0 {
		t.Fatal("edit called the source")
	}
}

func TestEditDetailsValidatesTypesAndBounds(t *testing.T) {
	e := newFixtureEnv(t, func(book, _ *plugintest.Plugin) {
		book.M.Kinds[0].Fields = append(book.M.Kinds[0].Fields,
			plugin.FieldDef{Key: "homepage", Label: "Homepage", Type: plugin.FieldURL, Editable: true},
			plugin.FieldDef{Key: "released", Label: "Released", Type: plugin.FieldDate, Editable: true})
	})
	show := e.saveFixture(t, "tv", "show", tvShow("5", "The Show"))
	book := e.saveFixture(t, "book", "book", bookItem("7", "Earthsea"))
	okShow := with(titleRows("en", "The Show"), "field.type", "series", "field.year", "2019")
	okBook := with(titleRows("en", "Earthsea"), "field.author", "Le Guin")
	for name, c := range map[string]struct {
		path string
		form url.Values
		want string
	}{
		"year nan":       {"/tv/tvdata/5", with(okShow, "field.year", "abc"), "Year must be a number"},
		"year fraction":  {"/tv/tvdata/5", with(okShow, "field.year", "1e3"), "Year must be a number"},
		"episodes min":   {"/tv/tvdata/5", with(okShow, "field.episodes", "-1"), "Episodes must be at least 0"},
		"enum":           {"/tv/tvdata/5", with(okShow, "field.type", "bogus"), "Please choose a type from the list"},
		"title lang":     {"/tv/tvdata/5", with(okShow, "title_lang", "xx"), "title language"},
		"title control":  {"/tv/tvdata/5", with(okShow, "title", "a\x07b"), "control character"},
		"title long":     {"/tv/tvdata/5", with(okShow, "title", strings.Repeat("t", 301)), "at most 300"},
		"no title":       {"/tv/tvdata/5", with(okShow, "title", "  "), "Please keep at least one title"},
		"rows mismatch":  {"/tv/tvdata/5", with(okShow, "title_lang", "en", "title", "x", "field.year", "1"), ""},
		"unknown field":  {"/tv/tvdata/5", with(okShow, "field.nope", "x"), "not a field"},
		"bad url":        {"/book/books/7", with(okBook, "field.homepage", "javascript:alert(1)"), "Homepage must be an http"},
		"bad date":       {"/book/books/7", with(okBook, "field.released", "2024-02-30"), "Released must be a date"},
		"pages min":      {"/book/books/7", with(okBook, "field.pages", "0"), "Pages must be at least 1"},
		"tags not here":  {"/book/books/7", with(okBook, "tags", "x"), "no tags"},
		"author newline": {"/book/books/7", with(okBook, "field.author", "a\nb"), "single line"},
	} {
		before := dump(t, e.env)
		if name == "rows mismatch" {
			c.form["title_lang"] = []string{"en", "original"}
		}
		rec := e.postMultipart(t, c.path+"/edit", c.form, nil)
		if rec.Code != 422 {
			t.Errorf("%s: %d", name, rec.Code)
			continue
		}
		if c.want != "" && !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s: missing %q in %s", name, c.want, rec.Body.String())
		}
		if dump(t, e.env) != before {
			t.Errorf("%s: database changed", name)
		}
	}
	// the input is kept on errors (escaped)
	rec := e.postMultipart(t, "/tv/tvdata/5/edit", with(okShow, "field.year", "abc", "title", `<b>"T"</b>`), nil)
	mustContain(t, rec.Body.String(), `value="abc"`, "&lt;b&gt;&#34;T&#34;&lt;/b&gt;")
	mustNotContain(t, rec.Body.String(), "<b>")
	// valid values are stored
	if rec := e.postMultipart(t, "/book/books/7/edit", with(okBook, "field.homepage", "https://x.example/", "field.released", "2001-02-03", "field.pages", "250"), nil); rec.Code != 303 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	it := e.item(t, book)
	if it.Fields["homepage"].Text != "https://x.example/" || fmtDay(it.Fields["released"].Date) != "2001-02-03" || *it.Fields["pages"].Num != 250 {
		t.Fatalf("%+v", it.Fields)
	}
	_ = show
}

func TestEditDetailsSetsGenresAndTags(t *testing.T) {
	e := newFixtureEnv(t)
	show := e.saveFixture(t, "tv", "show", tvShow("5", "The Show"))
	base := with(titleRows("en", "The Show"), "field.type", "series")
	rec := e.postMultipart(t, "/tv/tvdata/5/edit", with(base, "genres", "Drama\ndrama\nMystery\n<script>x</script>",
		"tags", "Time Travel:80!\nTwist\ntwist"), nil)
	if rec.Code != 303 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	it := e.item(t, show)
	if !reflect.DeepEqual(it.Genres, []string{"Drama", "Mystery", "<script>x</script>"}) {
		t.Fatalf("genres %q", it.Genres)
	}
	if !reflect.DeepEqual(it.Tags, []plugin.Tag{{Name: "Time Travel", Rank: ip(80), Spoiler: true}, {Name: "Twist"}}) {
		t.Fatalf("tags %+v", it.Tags)
	}
	b := e.get("/tv/tvdata/5").Body.String()
	mustContain(t, b, "&lt;script&gt;x&lt;/script&gt;", "Time Travel")
	mustNotContain(t, b, "<script>x")
	edit := e.get("/tv/tvdata/5/edit").Body.String()
	mustContain(t, edit, "Drama\nMystery\n&lt;script&gt;x&lt;/script&gt;", "Time Travel:80!\nTwist")
	many := make([]string, 51)
	for i := range many {
		many[i] = "g" + idStr(int64(i))
	}
	for name, form := range map[string]url.Values{
		"51 genres":     with(base, "genres", strings.Join(many, "\n")),
		"rank":          with(base, "tags", "x:101"),
		"rank sign":     with(base, "tags", "x:-5"),
		"long tag":      with(base, "tags", strings.Repeat("t", 61)),
		"control genre": with(base, "genres", "a\x07b"),
	} {
		before := e.item(t, show)
		if rec := e.postMultipart(t, "/tv/tvdata/5/edit", form, nil); rec.Code != 422 {
			t.Errorf("%s: %d", name, rec.Code)
		}
		if after := e.item(t, show); !reflect.DeepEqual(before, after) {
			t.Errorf("%s: changed", name)
		}
	}
	// emptying removes every term
	if rec := e.postMultipart(t, "/tv/tvdata/5/edit", with(base, "genres", "", "tags", ""), nil); rec.Code != 303 {
		t.Fatal(rec.Code)
	}
	if it := e.item(t, show); len(it.Genres) != 0 || len(it.Tags) != 0 {
		t.Fatalf("%+v", it)
	}
}

func TestEditDetailsRenamesTitleAndSearchSeesIt(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.manualAnimeItem(t, "")
	if _, err := e.d.Exec(`INSERT INTO item_titles(item_id, lang, text) VALUES (?, 'de', 'Alter Name')`, id); err != nil {
		t.Fatal(err)
	}
	b := e.get("/anime/manual/m1/edit").Body.String()
	// one row per stored title, an empty row per missing kind language, a free row
	mustContain(t, b, `value="Old Name"`, `value="Old EN"`, `value="Alter Name"`, `name="title_lang" value="ja"`, "Add a title")
	form := with(titleRows("ja-latn", "Shin Name", "en", "", "de", "Alter Name", "ja", "", "ja", "新しい名前"), "field.type", "TV")
	if rec := e.postMultipart(t, "/anime/manual/m1/edit", form, nil); rec.Code != 303 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	want := []plugin.Title{{Lang: "ja-latn", Text: "Shin Name"}, {Lang: "de", Text: "Alter Name"}, {Lang: "ja", Text: "新しい名前"}}
	if it := e.item(t, id); !reflect.DeepEqual(it.Titles, want) {
		t.Fatalf("titles %+v", it.Titles)
	}
	search := func(text string) int {
		p, err := e.lib.List(context.Background(), library.Query{PluginID: "anime", Kind: "anime", Text: text}, "en")
		if err != nil {
			t.Fatal(err)
		}
		return p.Total
	}
	if search("shin") != 1 || search("新しい") != 1 {
		t.Fatal("new titles not found")
	}
	if search("Old") != 0 {
		t.Fatal("removed titles still found")
	}
	// at least one title must remain; a language outside the kind's and the
	// stored ones is refused
	for name, f := range map[string]url.Values{
		"none":     with(titleRows("ja-latn", "", "de", " "), "field.type", "TV"),
		"bad lang": with(titleRows("original", "X"), "field.type", "TV"),
	} {
		if rec := e.postMultipart(t, "/anime/manual/m1/edit", f, nil); rec.Code != 422 {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if it := e.item(t, id); !reflect.DeepEqual(it.Titles, want) {
		t.Fatalf("changed: %+v", it.Titles)
	}
}

func writeCover(t *testing.T, e *env, name string) {
	t.Helper()
	if err := os.MkdirAll(e.covers, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.covers, name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasFile(e *env, name string) bool {
	_, err := os.Stat(filepath.Join(e.covers, name))
	return err == nil
}

var hashedPNG = regexp.MustCompile(`^[0-9a-f]{64}\.png$`)

func TestEditDetailsReplacesCover(t *testing.T) {
	e := newFixtureEnv(t)
	writeCover(t, e.env, "old.png")
	writeCover(t, e.env, "shared.png")
	id := e.manualAnimeItem(t, "old.png")
	d := tvShow("5", "The Show")
	d.CoverURL = "shared.png"
	show := e.saveFixture(t, "tv", "show", d)
	d2 := tvShow("6", "Other")
	d2.CoverURL = "shared.png"
	e.saveFixture(t, "tv", "show", d2)
	form := with(titleRows("ja-latn", "Old Name"), "field.type", "TV")
	b := e.get("/anime/manual/m1/edit").Body.String()
	mustContain(t, b, `src="/covers/old.png"`, `name="cover"`, `name="remove_cover"`)

	// replace: the new file is stored, the unreferenced old one deleted
	if rec := e.postMultipart(t, "/anime/manual/m1/edit", form, &upload{"cover", "x.gif", "image/gif", png1("new")}); rec.Code != 303 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	it := e.item(t, id)
	if !hashedPNG.MatchString(it.CoverPath) || !hasFile(e.env, it.CoverPath) || hasFile(e.env, "old.png") {
		t.Fatalf("cover %q files %v", it.CoverPath, e.coverFiles(t))
	}
	newCover := it.CoverPath

	// a shared old file stays
	showForm := with(titleRows("en", "The Show"), "field.type", "series")
	if rec := e.postMultipart(t, "/tv/tvdata/5/edit", showForm, &upload{"cover", "y.png", "image/png", png1("tv")}); rec.Code != 303 {
		t.Fatal(rec.Code)
	}
	if !hasFile(e.env, "shared.png") || e.item(t, show).CoverPath == "shared.png" {
		t.Fatal("shared file removed or cover not replaced")
	}

	// rejected uploads change nothing and leave no file
	files := e.coverFiles(t)
	for name, c := range map[string]struct {
		form url.Values
		up   *upload
	}{
		"svg":       {form, &upload{"cover", "x.svg", "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)}},
		"html":      {form, &upload{"cover", "x.png", "image/png", []byte("<html><script>alert(1)</script></html>")}},
		"too large": {form, &upload{"cover", "x.png", "image/png", png1(strings.Repeat("a", 10<<20))}},
		"bad form":  {with(form, "field.year", "abc"), &upload{"cover", "x.png", "image/png", png1("other")}},
	} {
		rec := e.postMultipart(t, "/anime/manual/m1/edit", c.form, c.up)
		if rec.Code != 422 {
			t.Errorf("%s: %d", name, rec.Code)
		}
		if name == "bad form" {
			mustContain(t, rec.Body.String(), "choose the cover file again")
		}
		if got := e.coverFiles(t); !reflect.DeepEqual(got, files) || e.item(t, id).CoverPath != newCover {
			t.Errorf("%s: files %v cover %q", name, got, e.item(t, id).CoverPath)
		}
	}
	if rec := e.postMultipart(t, "/anime/manual/m1/edit", form, &upload{"cover", "x.png", "image/png", png1(strings.Repeat("a", 13<<20))}); rec.Code != 413 {
		t.Fatalf("oversize: %d", rec.Code)
	}

	// a database failure removes the just-written file and keeps the old cover
	if _, err := e.d.Exec(`CREATE TRIGGER boom BEFORE INSERT ON item_titles BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	if rec := e.postMultipart(t, "/anime/manual/m1/edit", form, &upload{"cover", "x.png", "image/png", png1("fail")}); rec.Code != 500 {
		t.Fatalf("db failure: %d", rec.Code)
	}
	if got := e.coverFiles(t); !reflect.DeepEqual(got, files) || e.item(t, id).CoverPath != newCover {
		t.Fatalf("after failure: files %v cover %q", got, e.item(t, id).CoverPath)
	}
	if _, err := e.d.Exec(`DROP TRIGGER boom`); err != nil {
		t.Fatal(err)
	}

	// remove: cleared, the file is deleted
	if rec := e.postMultipart(t, "/anime/manual/m1/edit", with(form, "remove_cover", "1"), nil); rec.Code != 303 {
		t.Fatal(rec.Code)
	}
	if it := e.item(t, id); it.CoverPath != "" || hasFile(e.env, newCover) {
		t.Fatalf("not removed: %q %v", it.CoverPath, e.coverFiles(t))
	}
	// a remote cover is cleared too (nothing to delete locally)
	d3 := tvShow("8", "Remote")
	d3.CoverURL = "https://img.example/r.jpg"
	remote := e.saveFixture(t, "tv", "show", d3)
	if rec := e.postMultipart(t, "/tv/tvdata/8/edit", with(titleRows("en", "Remote"), "remove_cover", "1"), nil); rec.Code != 303 {
		t.Fatal(rec.Code)
	}
	if e.item(t, remote).CoverPath != "" {
		t.Fatal("remote cover kept")
	}
	// unchanged when neither uploaded nor removed
	before := e.item(t, show).CoverPath
	if rec := e.postMultipart(t, "/tv/tvdata/5/edit", showForm, nil); rec.Code != 303 || e.item(t, show).CoverPath != before {
		t.Fatal("cover changed without upload")
	}
}

func TestEditDetailsCoverWithoutCoversDir(t *testing.T) {
	e := newFixtureEnv(t)
	e.saveFixture(t, "tv", "show", tvShow("5", "The Show"))
	_, e.h = newServer(Deps{Settings: e.set, Jobs: e.jobs, Registry: e.reg, Library: e.lib}, func() time.Time { return *e.now })
	mustNotContain(t, e.get("/tv/tvdata/5/edit").Body.String(), `name="cover"`)
	rec := e.postMultipart(t, "/tv/tvdata/5/edit", titleRows("en", "X"), &upload{"cover", "c.png", "image/png", png1("")})
	if rec.Code != 422 {
		t.Fatalf("%d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "not available")
}

func TestEditReturnsToOverview(t *testing.T) {
	e := newFixtureEnv(t)
	e.manualAnimeItem(t, "")
	const overview = "/anime/manual/m1"
	form := with(titleRows("ja-latn", "Old Name"), "field.type", "TV")
	for _, ret := range []string{
		"", overview, "/anime", "/anime?status=active", "https://evil.example/", "//evil.example/anime/manual/m1",
		"/\\evil.example", "javascript:alert(1)", "/anime/manual/m1/../../settings", "/anime/manual/m1?x=1",
		"/anime/manual/m1#x", "/anime/manual/m10", "/anime/manual/m1/edit", "/book/books/1", "/list/1/edit",
		"/anime/manual/m1%2f..", "/anime/manual/m1\nLocation: x", " /anime/manual/m1", "/settings",
	} {
		rec := e.postMultipart(t, overview+"/edit", with(form, "return", ret), nil)
		if rec.Code != 303 || rec.Header().Get("Location") != overview {
			t.Errorf("return %q: %d %q", ret, rec.Code, rec.Header().Get("Location"))
		}
		b := e.get(overview + "/edit?return=" + url.QueryEscape(ret)).Body.String()
		mustContain(t, b, `name="return" value="`+overview+`"`)
	}
	// a plugin with several kinds returns to the overview with its ?kind=
	fe := newFixtureEnv(t, withComicKind)
	fe.saveFixture(t, "book", "book", bookItem("7", "Novel"))
	cd := bookItem("7", "Comic")
	cd.Kind = "comic"
	fe.saveFixture(t, "book", "comic", cd)
	rec := fe.postMultipart(t, "/book/books/7/edit?kind=comic", with(titleRows("en", "Comic 2"), "field.author", "A", "return", "/book/books/7?kind=comic"), nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/book/books/7?kind=comic" {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = fe.postMultipart(t, "/book/books/7/edit?kind=comic", with(titleRows("en", "Comic 3"), "field.author", "A", "return", "/book/books/7?kind=book"), nil)
	if rec.Header().Get("Location") != "/book/books/7?kind=comic" {
		t.Fatalf("other kind accepted: %q", rec.Header().Get("Location"))
	}
	mustContain(t, fe.get("/book/books/7?kind=comic").Body.String(), "Comic 3", `href="/book/books/7/edit?kind=comic&amp;return=`)
}

func TestReturnToItem(t *testing.T) {
	for _, c := range []struct{ raw, overview, want string }{
		{"/tv/tvdata/5", "/tv/tvdata/5", "/tv/tvdata/5"},
		{"/tv/tvdata/5?kind=show", "/tv/tvdata/5?kind=show", "/tv/tvdata/5?kind=show"},
		{"/tv/tvdata/5", "/tv/tvdata/5?kind=show", "/tv/tvdata/5?kind=show"},
		{"/tv/tvdata/5?kind=other", "/tv/tvdata/5?kind=show", "/tv/tvdata/5?kind=show"},
		{"/tv/tvdata/50", "/tv/tvdata/5", "/tv/tvdata/5"},
		{"https://evil/tv/tvdata/5", "/tv/tvdata/5", "/tv/tvdata/5"},
		{"/tv/a%20b", "/tv/a%20b", "/tv/a%20b"}, // equal to the overview: the overview itself
	} {
		if got := returnToItem(c.raw, c.overview); got != c.want {
			t.Errorf("returnToItem(%q, %q) = %q, want %q", c.raw, c.overview, got, c.want)
		}
	}
}

func TestEditDoesNotTouchEntryData(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.manualAnimeItem(t, "")
	ctx := context.Background()
	before, _ := e.lib.Entry(ctx, id)
	storedBefore, _ := e.lib.StoredProgress(ctx, id)
	keysBefore := e.item(t, id).ProgressKeys
	form := with(titleRows("ja-latn", "Old Name"), "field.type", "TV", "field.episodes", "24",
		"status", "dropped", "rating", "1", "progress", "9", "progress.episode", "9", "comment", "changed",
		"started_on", "2020-01-01", "progress_keys", "episode")
	if rec := e.postMultipart(t, "/anime/manual/m1/edit", form, nil); rec.Code != 303 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	after, _ := e.lib.Entry(ctx, id)
	storedAfter, _ := e.lib.StoredProgress(ctx, id)
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(storedBefore, storedAfter) || !reflect.DeepEqual(keysBefore, e.item(t, id).ProgressKeys) {
		t.Fatalf("entry touched:\n%+v\n%+v\n%v %v", before, after, storedBefore, storedAfter)
	}
	if *e.item(t, id).Fields["episodes"].Num != 24 {
		t.Fatal("details not saved")
	}
}

func TestEditDetailsDisabledPlugin404(t *testing.T) {
	e := newFixtureEnv(t)
	e.saveFixture(t, "tv", "show", tvShow("5", "The Show"))
	e.disable(t, "tv")
	before := dump(t, e.env)
	if c := e.get("/tv/tvdata/5/edit").Code; c != 404 {
		t.Fatalf("GET %d", c)
	}
	if c := e.postMultipart(t, "/tv/tvdata/5/edit", titleRows("en", "X"), &upload{"cover", "c.png", "image/png", png1("")}).Code; c != 404 {
		t.Fatalf("POST %d", c)
	}
	if dump(t, e.env) != before || len(e.coverFiles(t)) != 0 {
		t.Fatal("changed while disabled")
	}
}

func TestEditDetailsKeepsNonEditableFields(t *testing.T) {
	e := newFixtureEnv(t, func(book, _ *plugintest.Plugin) {
		book.M.Kinds[0].Fields[2].Editable = false // isbn
		book.M.Kinds[0].Genres.Editable = false
	})
	d := bookItem("7", "Earthsea")
	d.Fields["isbn"] = plugin.FieldValue{Text: "978-1"}
	id := e.saveFixture(t, "book", "book", d)
	b := e.get("/book/books/7/edit").Body.String()
	mustNotContain(t, b, `name="field.isbn"`, `name="genres"`)
	form := with(titleRows("en", "Earthsea"), "field.author", "Le Guin")
	if rec := e.postMultipart(t, "/book/books/7/edit", with(form, "field.isbn", "x"), nil); rec.Code != 422 {
		t.Fatalf("locked field accepted: %d", rec.Code)
	}
	if rec := e.postMultipart(t, "/book/books/7/edit", with(form, "genres", "x"), nil); rec.Code != 422 {
		t.Fatalf("locked genres accepted: %d", rec.Code)
	}
	if rec := e.postMultipart(t, "/book/books/7/edit", form, nil); rec.Code != 303 {
		t.Fatal(rec.Code)
	}
	it := e.item(t, id)
	if it.Fields["isbn"].Text != "978-1" || !reflect.DeepEqual(it.Genres, []string{"Fantasy"}) || it.Fields["author"].Text != "Le Guin" {
		t.Fatalf("%+v", it)
	}
}

func TestOverviewOffersEditDetailsForSavedItemsOnly(t *testing.T) {
	e := newFixtureEnv(t)
	e.tv.Data = tvShow("9", "Live Show")
	mustNotContain(t, e.get("/tv/tvdata/9").Body.String(), "Edit details")
	e.saveFixture(t, "tv", "show", tvShow("5", "Saved, not listed"))
	mustContain(t, e.get("/tv/tvdata/5").Body.String(), "Edit details", `href="/tv/tvdata/5/edit?return=%2Ftv%2Ftvdata%2F5"`)
	e.addFixture(t, "tv", "show", tvShow("6", "Listed"), library.Entry{Status: library.Active})
	b := e.get("/tv/tvdata/6").Body.String()
	mustContain(t, b, "Edit details", ">Edit<")
}

var (
	formTokenRe = regexp.MustCompile(`(?s)<input [^>]*>|<textarea [^>]*>.*?</textarea>|<select [^>]*>.*?</select>`)
	formAttrRe  = regexp.MustCompile(`([a-z_-]+)="([^"]*)"`)
	formOptRe   = regexp.MustCompile(`<option value="([^"]*)"( selected)?>`)
)

func formAttrs(tag string) map[string]string {
	head := tag
	if i := strings.Index(tag, ">"); i >= 0 {
		head = tag[:i]
	}
	out := map[string]string{}
	for _, m := range formAttrRe.FindAllStringSubmatch(head, -1) {
		out[m[1]] = html.UnescapeString(m[2])
	}
	if strings.Contains(head, " checked") {
		out["checked"] = "1"
	}
	return out
}

// browserForm extracts what a browser submits for the page's form, in
// document order: inputs (no file or unchecked checkbox), textareas and
// selects (the selected option, else the first). It models the browser: the
// HTML parser drops the newline right after a textarea's start tag and turns
// CRLF and lone CR in its content into LF, so a rendered stored value comes
// back with LF line breaks (newlines a user types are sent as CRLF: tests
// that edit a textarea set such values themselves).
func browserForm(t *testing.T, page string) url.Values {
	t.Helper()
	v := url.Values{}
	for _, tok := range formTokenRe.FindAllString(page, -1) {
		a := formAttrs(tok)
		name := a["name"]
		if name == "" {
			continue
		}
		switch {
		case strings.HasPrefix(tok, "<input"):
			if a["type"] == "file" || (a["type"] == "checkbox" && a["checked"] == "") {
				continue
			}
			v.Add(name, a["value"])
		case strings.HasPrefix(tok, "<textarea"):
			body := tok[strings.Index(tok, ">")+1 : strings.LastIndex(tok, "</textarea>")]
			body = strings.TrimPrefix(html.UnescapeString(body), "\n")
			body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n")
			v.Add(name, body)
		default:
			opts := formOptRe.FindAllStringSubmatch(tok, -1)
			if len(opts) == 0 {
				continue
			}
			val := opts[0][1]
			for _, o := range opts {
				if o[2] != "" {
					val = o[1]
					break
				}
			}
			v.Add(name, html.UnescapeString(val))
		}
	}
	return v
}

// richTV gives the tv fixture one more field of every type: a text-section
// synopsis, a one-line note, a list, a fractional score, a date and a url.
func richTV(_, tv *plugintest.Plugin) {
	k := &tv.M.Kinds[0]
	k.Fields = append(k.Fields,
		plugin.FieldDef{Key: "synopsis", Label: "Synopsis", Type: plugin.FieldText, Editable: true},
		plugin.FieldDef{Key: "note", Label: "Note", Type: plugin.FieldText, Editable: true},
		plugin.FieldDef{Key: "memo", Label: "Memo", Type: plugin.FieldText, Editable: true},
		plugin.FieldDef{Key: "tagline", Label: "Tagline", Type: plugin.FieldText, Editable: true},
		plugin.FieldDef{Key: "networks", Label: "Networks", Type: plugin.FieldList, Editable: true},
		plugin.FieldDef{Key: "score", Label: "Score", Type: plugin.FieldNumber, Editable: true},
		plugin.FieldDef{Key: "aired", Label: "Aired", Type: plugin.FieldDate, Editable: true},
		plugin.FieldDef{Key: "homepage", Label: "Homepage", Type: plugin.FieldURL, Editable: true},
	)
	k.Sections[1].Field = "synopsis"
}

// richShow saves a sourced show whose data the form can only reproduce by
// keeping it verbatim: odd tags, a genre with a comma, multi-line text, a
// newline in a one-line field, values over the form's limits.
func richShow(t *testing.T, e *env) (int64, library.Item) {
	t.Helper()
	nets := make([]string, 60) // the form accepts 50, the library 1000
	for i := range nets {
		nets[i] = "net" + idStr(int64(i))
	}
	aired := time.Date(2017, 12, 1, 0, 0, 0, 0, time.UTC)
	d := tvShow("5", "The Show")
	d.Titles = append(d.Titles, plugin.Title{Lang: "original", Text: "Original"})
	d.Fields["type"] = plugin.FieldValue{Text: "miniseries"}
	d.Fields["episodes"] = plugin.FieldValue{Num: num(12)}
	d.Fields["score"] = plugin.FieldValue{Num: num(12.5)}
	d.Fields["year"] = plugin.FieldValue{Num: num(1234567890123456789)}
	d.Fields["synopsis"] = plugin.FieldValue{Text: "A\r\nB\r\n\r\nC\r\n  Indented."}
	d.Fields["note"] = plugin.FieldValue{Text: "x\r\ny"}                         // a one-line field holding CRLF
	d.Fields["memo"] = plugin.FieldValue{Text: "first line\nsecond line\rthird"} // LF and a lone CR
	d.Fields["tagline"] = plugin.FieldValue{Text: strings.Repeat("t", 1500)}
	d.Fields["networks"] = plugin.FieldValue{List: nets}
	d.Fields["aired"] = plugin.FieldValue{Date: &aired}
	d.Fields["homepage"] = plugin.FieldValue{Text: "https://tv.example/home"}
	d.Genres = []string{"Sci-Fi, Fantasy", "Drama"}
	d.Tags = []plugin.Tag{{Name: "Wow!"}, {Name: "Season:2"}, {Name: "Re:Zero"}, {Name: "Plot Twist", Rank: ip(90), Spoiler: true}}
	d.CoverURL = "https://img.example/show.jpg"
	id := e.addFixture(t, "tv", "show", d, library.Entry{Status: library.Active})
	return id, e.item(t, id)
}

func TestEditDetailsKeepsUntouchedDataVerbatim(t *testing.T) {
	e := newFixtureEnv(t, richTV)
	id, before := richShow(t, e.env)
	form := browserForm(t, e.get("/tv/tvdata/5/edit").Body.String())
	if form.Get("title") != "The Show" {
		t.Fatalf("form %v", form)
	}
	form["title"][0] = "Renamed"
	if rec := e.postMultipart(t, "/tv/tvdata/5/edit", form, nil); rec.Code != 303 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	after := e.item(t, id)
	if !reflect.DeepEqual(after.Titles, []plugin.Title{{Lang: "en", Text: "Renamed"}, {Lang: "original", Text: "Original"}}) {
		t.Fatalf("titles %+v", after.Titles)
	}
	for k, v := range before.Fields {
		if !reflect.DeepEqual(after.Fields[k], v) {
			t.Errorf("field %s: %+v -> %+v", k, v, after.Fields[k])
		}
	}
	if len(after.Fields) != len(before.Fields) {
		t.Errorf("fields %v -> %v", before.Fields, after.Fields)
	}
	if !reflect.DeepEqual(after.Genres, before.Genres) || !reflect.DeepEqual(after.Tags, before.Tags) || after.CoverPath != before.CoverPath {
		t.Fatalf("terms or cover changed:\n%+v %+v %q\n%+v %+v %q", before.Genres, before.Tags, before.CoverPath, after.Genres, after.Tags, after.CoverPath)
	}
}

func TestEditDetailsChangingOneFieldKeepsTheRest(t *testing.T) {
	e := newFixtureEnv(t, richTV)
	id, before := richShow(t, e.env)
	form := browserForm(t, e.get("/tv/tvdata/5/edit").Body.String())
	form.Set("field.episodes", "13")
	if rec := e.postMultipart(t, "/tv/tvdata/5/edit", form, nil); rec.Code != 303 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	after := e.item(t, id)
	want := before.Fields
	want["episodes"] = plugin.FieldValue{Num: num(13)}
	if !reflect.DeepEqual(after.Fields, want) || !reflect.DeepEqual(after.Titles, before.Titles) ||
		!reflect.DeepEqual(after.Genres, before.Genres) || !reflect.DeepEqual(after.Tags, before.Tags) {
		t.Fatalf("\n%+v\n%+v", want, after)
	}
	// an edited tags text follows the documented syntax
	form.Set("tags", "Wow!\nSeason:2")
	if rec := e.postMultipart(t, "/tv/tvdata/5/edit", form, nil); rec.Code != 303 {
		t.Fatal(rec.Code)
	}
	if it := e.item(t, id); !reflect.DeepEqual(it.Tags, []plugin.Tag{{Name: "Wow", Spoiler: true}, {Name: "Season", Rank: ip(2)}}) {
		t.Fatalf("tags %+v", it.Tags)
	}
}

// A required field the source never filled does not block an edit; clearing
// a required field that has a value is refused; the HTML required attribute
// follows the same rule.
func TestEditDetailsRequiredOnlyWhenStored(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.saveFixture(t, "anime", "anime", plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "anilist", ID: "7",
		Titles: ttl("Typo")}})
	b := e.get("/anime/anilist/7/edit").Body.String()
	mustContain(t, b, `<select name="field.type" class=`)
	if rec := e.postMultipart(t, "/anime/anilist/7/edit", titleRows("en", "Fixed"), nil); rec.Code != 303 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if it := e.item(t, id); it.Titles[0].Text != "Fixed" || len(it.Fields) != 0 {
		t.Fatalf("%+v", it)
	}
	if rec := e.postMultipart(t, "/anime/anilist/7/edit", with(titleRows("en", "Fixed"), "field.type", "TV"), nil); rec.Code != 303 {
		t.Fatal(rec.Code)
	}
	mustContain(t, e.get("/anime/anilist/7/edit").Body.String(), `<select name="field.type" required`)
	rec := e.postMultipart(t, "/anime/anilist/7/edit", with(titleRows("en", "Fixed"), "field.type", ""), nil)
	if rec.Code != 422 {
		t.Fatalf("cleared required: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Please choose a type")
	if it := e.item(t, id); it.Fields["type"].Text != "TV" {
		t.Fatal("cleared")
	}
	mustContain(t, e.get("/anime/manual/new").Body.String(), `<select name="field.type" required`)
}

// A changed text is stored as posted (the browser sends typed newlines as
// CRLF), only trimmed; a changed one-line field must be a single line.
func TestEditDetailsTypedTextStoredAsPosted(t *testing.T) {
	e := newFixtureEnv(t, richTV)
	id, before := richShow(t, e.env)
	form := browserForm(t, e.get("/tv/tvdata/5/edit").Body.String())
	form.Set("field.synopsis", "New\r\nText\r\n")
	if rec := e.postMultipart(t, "/tv/tvdata/5/edit", form, nil); rec.Code != 303 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	after := e.item(t, id)
	if after.Fields["synopsis"].Text != "New\r\nText" {
		t.Fatalf("synopsis %q", after.Fields["synopsis"].Text)
	}
	for _, k := range []string{"note", "memo", "tagline"} {
		if !reflect.DeepEqual(after.Fields[k], before.Fields[k]) {
			t.Errorf("%s changed: %q", k, after.Fields[k].Text)
		}
	}
	form = browserForm(t, e.get("/tv/tvdata/5/edit").Body.String())
	form.Set("field.note", "a\r\nb")
	rec := e.postMultipart(t, "/tv/tvdata/5/edit", form, nil)
	if rec.Code != 422 {
		t.Fatalf("changed one-line field with a newline: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Note must be a single line")
}

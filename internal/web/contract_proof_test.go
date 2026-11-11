package web

// Proof of the plugin contract: the book and TV fixture plugins (test-only,
// internal/plugin/plugintest) are registered next to the real anime plugin in
// one server wired like cmd/allister (one database, one job queue with the
// plugin gate, one import runner, the generic sync job for every plugin, the
// universal backup), and the real flows run through the real handlers. No
// test here reaches into a handler: everything goes through HTTP, the job
// queue's own worker loop and the backup archive.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/db"
	"github.com/allister/allister/internal/importer"
	"github.com/allister/allister/internal/jobs"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
	"github.com/allister/allister/internal/plugins/anime"
	"github.com/allister/allister/internal/settings"
)

// proofEnv is the three-plugin server. env.jobs records every enqueued job,
// which also lands in the real jobs table worked by queue.
type proofEnv struct {
	*env
	book, tv *plugintest.Catalog
	queue    *jobs.Queue
	imports  *importer.Runner
	imgs     *httptest.Server
}

// newProofEnv builds the server; mod may change the fixture plugins and
// their catalogs before they are registered.
func newProofEnv(t *testing.T, mod ...func(book, tv *plugintest.Plugin, bc, tc *plugintest.Catalog)) *proofEnv {
	t.Helper()
	imgs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctype, body := proofImage(r.URL.Path)
		w.Header().Set("Content-Type", ctype)
		w.Write(body)
	}))
	t.Cleanup(imgs.Close)

	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "covers"), 0o755); err != nil { // as cmd/allister does
		t.Fatal(err)
	}
	e := &env{fp: sampleAnimeProvider(), jobs: &fakeJobs{}, set: settings.New(d), covers: filepath.Join(dataDir, "covers"), d: d}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	e.now = &now
	e.fp.anime.CoverURL = imgs.URL + "/anime.jpg"
	e.fp.cast[0].Character.ImageURL = imgs.URL + "/light.jpg"
	e.fp.cast[0].Actor.ImageURL = imgs.URL + "/miyano.jpg"

	pe := &proofEnv{env: e, book: plugintest.BookCatalog(), tv: plugintest.TVCatalog(), imgs: imgs}
	book, tv := plugintest.BookPlugin(), plugintest.TVPlugin()
	book.Sources = map[string]plugin.Source{"books": pe.book}
	book.Importers = map[string]plugin.Importer{"books-csv": plugintest.LinesImporter{Kind: "book", IDType: "isbn"}}
	book.M.Exporters = []plugin.ExporterInfo{{ID: "lines", Name: "Lines", Extension: ".txt", MIME: "text/plain"}}
	book.Exporters = map[string]plugin.Exporter{"lines": &plugintest.FakeExporter{}}
	tv.Sources = map[string]plugin.Source{"tvdata": pe.tv}
	tv.Importers = map[string]plugin.Importer{"tv-json": plugintest.LinesImporter{Kind: "show", IDType: "tvdata"}}
	for _, m := range mod {
		m(book, tv, pe.book, pe.tv)
	}
	e.reg = plugin.NewRegistry(e.set)
	for _, p := range []plugin.Plugin{anime.New(anime.Options{Provider: searchProvider{e.fp}}), book, tv} {
		if err := e.reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	e.lib = library.New(d, e.reg)

	// cmd/allister's wiring, with a fast poll and no pacing or backoff
	pe.queue = jobs.NewQueue(d, jobs.WithPollInterval(time.Millisecond), jobs.WithPluginGate(e.reg.IsEnabled),
		jobs.WithBackoff(func(int) time.Duration { return 0 }))
	pe.queue.Register(jobs.KindSyncItem, jobs.SyncItem(jobs.SyncDeps{Library: e.lib, Registry: e.reg, HTTP: imgs.Client(), CoversDir: e.covers}))
	syncJobs := map[string]func(int64) (string, any){}
	for _, m := range e.reg.Manifests() {
		syncJobs[m.ID] = jobs.SyncJobFor(m.ID)
	}
	q := dbJobs{e.jobs, pe.queue}
	pe.imports = importer.New(importer.Deps{DB: d, Library: e.lib, Registry: e.reg, Queue: q, SyncJobs: syncJobs,
		Sleep: func(ctx context.Context, _ time.Duration) error { return ctx.Err() }, Logf: t.Logf})
	pe.queue.Register(importer.KindRun, pe.imports.Handler())
	e.srv, e.h = newServer(Deps{
		Settings: e.set, Jobs: q, SyncJobs: syncJobs, CoversDir: e.covers, DataDir: dataDir,
		Imports: pe.imports, Registry: e.reg, Library: e.lib,
	}, func() time.Time { return *e.now })
	return pe
}

// proofImage is what the image server answers for path: a .avif path gets
// an AVIF file, anything else a JPEG file (each carrying the path).
func proofImage(path string) (ctype string, body []byte) {
	if strings.HasSuffix(path, ".avif") {
		return "image/avif", append([]byte("\x00\x00\x00\x1cftypavif\x00\x00\x00\x00avifmif1miaf"), path...)
	}
	return "image/jpeg", append([]byte("\xff\xd8\xff\xe0"), path...)
}

// drain runs the queue's own worker loop until no job of an enabled plugin
// is pending or running (jobs of disabled plugins stay pending), then stops
// it. A job that failed fails the test.
func (e *proofEnv) drain(t *testing.T) {
	t.Helper()
	busy := func() bool {
		rows, err := e.d.Query(`SELECT state, COALESCE(json_extract(payload_json, '$.plugin_id'), '') FROM jobs WHERE state IN ('pending','running')`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var state, pid string
			rows.Scan(&state, &pid)
			if state == "running" || pid == "" || e.reg.IsEnabled(pid) {
				return true
			}
		}
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	if busy() { // started only with work to do, so Run's own startup reset is never cut short
		go func() { defer close(done); e.queue.Run(ctx) }()
	} else {
		close(done)
	}
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(10 * time.Second)
	for busy() {
		if time.Now().After(deadline) {
			t.Fatalf("queue did not drain: %v", e.jobRows(t))
		}
		time.Sleep(2 * time.Millisecond)
	}
	var failed []string
	for _, j := range e.jobRows(t) {
		if j.State == "failed" {
			failed = append(failed, j.Kind+" "+j.PluginID+": "+j.Err)
		}
	}
	if len(failed) > 0 {
		t.Fatalf("failed jobs: %v", failed)
	}
}

type jobRow struct{ Kind, PluginID, State, Err string }

// jobRows lists the jobs table in id order.
func (e *proofEnv) jobRows(t *testing.T) []jobRow {
	t.Helper()
	rows, err := e.d.Query(`SELECT kind, COALESCE(json_extract(payload_json, '$.plugin_id'), ''), state, last_error FROM jobs ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []jobRow
	for rows.Next() {
		var j jobRow
		if err := rows.Scan(&j.Kind, &j.PluginID, &j.State, &j.Err); err != nil {
			t.Fatal(err)
		}
		out = append(out, j)
	}
	return out
}

// add posts the add form of plugin/source/id (htmx) and expects the
// redirect to the plugin's list.
func (e *proofEnv) add(t *testing.T, pluginID, source, id string, form url.Values) {
	t.Helper()
	rec := e.do("POST", "/"+pluginID+"/"+source+"/"+id+"/add", form, true)
	if rec.Code != http.StatusNoContent || rec.Header().Get("HX-Redirect") != "/"+pluginID {
		t.Fatalf("add %s/%s/%s: %d %q %s", pluginID, source, id, rec.Code, rec.Header().Get("HX-Redirect"), rec.Body.String())
	}
}

// page GETs target and expects 200.
func (e *proofEnv) page(t *testing.T, target string) string {
	t.Helper()
	rec := e.get(target)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", target, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// itemID is the id of the one saved item of plugin/source/id.
func (e *proofEnv) itemID(t *testing.T, pluginID, source, id string) string {
	t.Helper()
	items := e.savedItems(t, pluginID, source, id)
	if len(items) != 1 {
		t.Fatalf("%s/%s/%s: %d saved items", pluginID, source, id, len(items))
	}
	return itoa(items[0].ID)
}

func mustRows(t *testing.T, body string, want ...string) {
	t.Helper()
	got := rowTitles(body)
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %q, want %q", got, want)
	}
}

// navOf is the header navigation of a full page.
func navOf(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, "</header>")
	if i < 0 {
		t.Fatal("no header")
	}
	return body[:i]
}

// statusCell is the rendered status cell of a list row.
func statusCell(label string) string { return `whitespace-nowrap">` + label + `</td>` }

// -----------------------------------------------------------------------------

func TestBookPluginSearchAddListEditFlow(t *testing.T) {
	e := newProofEnv(t)

	// search through the book source: only the matching book, linked to its overview
	b := e.page(t, "/book/search?q=wizard")
	mustContain(t, b, "A Wizard of Earthsea", `href="/book/books/earthsea"`, "Ursula Writer")
	mustNotContain(t, b, "Dune", "デスノート")
	if e.book.Calls(plugintest.OpSearch) != 1 {
		t.Fatalf("book searches: %d", e.book.Calls(plugintest.OpSearch))
	}

	// overview offers adding; the add dialog asks for pages out of 250
	mustContain(t, e.page(t, "/book/books/earthsea"), "A Wizard of Earthsea</h1>", "Add to list", "View on Fake Books")
	mustContain(t, e.do("GET", "/book/books/earthsea/add", nil, true).Body.String(), `name="progress.pages"`, `max="250"`, "/ 250")

	// add with page progress
	e.add(t, "book", "books", "earthsea", url.Values{"status": {"active"}, "rating": {"8"}, "progress.pages": {"120"}})
	b = e.page(t, "/book?view=table")
	mustRows(t, b, "A Wizard of Earthsea")
	mustContain(t, b, statusCell("Reading"), "120 / 250", "Reading (1)", "Want to read (0)")
	id := e.itemID(t, "book", "books", "earthsea")

	// +1 adds a page
	rec := e.do("POST", "/list/"+id+"/increment", url.Values{}, true)
	if rec.Code != 200 {
		t.Fatalf("increment: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "121 / 250")

	// the entry dialog offers both combinations; switch to percent
	b = e.do("GET", "/list/"+id+"/edit", nil, true).Body.String()
	mustContain(t, b, `name="progress_keys"`, `value="pages"`, `value="percent"`, `name="progress.pages"`, `name="progress.percent"`)
	rec = e.do("POST", "/list/"+id+"/edit", url.Values{"status": {"active"}, "rating": {"8"}, "progress_keys": {"percent"}, "progress.percent": {"40"}}, true)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("switch to percent: %d %s", rec.Code, rec.Body.String())
	}
	b = e.page(t, "/book?view=table")
	mustContain(t, b, statusCell("Reading"), "40%")
	mustNotContain(t, b, "/ 250", "/increment") // no +1 without an active page track
	mustContain(t, e.page(t, "/book/books/earthsea"), "Percent: 40%")

	// Edit details changes the item's own data and returns to the overview;
	// the progress (and the retained page count) is untouched
	mustContain(t, e.page(t, "/book/books/earthsea/edit"), `name="field.pages"`, `value="250"`, `name="genres"`)
	mustNotContain(t, e.page(t, "/book/books/earthsea/edit"), `name="tags"`)
	form := url.Values{"title_lang": {"en"}, "title": {"A Wizard of Earthsea"}, "field.author": {"Ursula Writer"}, "field.pages": {"300"},
		"field.isbn": {"9780000000001"}, "genres": {"Fantasy\nClassic"}, "return": {"/book/books/earthsea"}}
	rec = e.do("POST", "/book/books/earthsea/edit", form, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/book/books/earthsea" {
		t.Fatalf("edit details: %d %q %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	mustContain(t, e.page(t, "/book?view=table"), "40%")

	// back to pages: the retained value against the edited total
	rec = e.do("POST", "/list/"+id+"/edit", url.Values{"status": {"active"}, "rating": {"8"}, "progress_keys": {"pages"}}, true)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("switch to pages: %d %s", rec.Code, rec.Body.String())
	}
	b = e.page(t, "/book?view=table")
	mustContain(t, b, "121 / 300", "/increment")
	mustNotContain(t, b, "40%")

	// a percent above 100 is refused, nothing changes
	rec = e.do("POST", "/list/"+id+"/edit", url.Values{"status": {"active"}, "progress_keys": {"percent"}, "progress.percent": {"101"}}, true)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("percent 101: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Percent must be a whole number from 0 to 100.")
	mustContain(t, e.page(t, "/book?view=table"), "121 / 300")
}

func TestTVPluginEpisodeAndPositionProgress(t *testing.T) {
	e := newProofEnv(t)
	e.add(t, "tv", "tvdata", "dark", url.Values{"status": {"active"}, "progress.episode": {"3"}})
	e.add(t, "tv", "tvdata", "1899", url.Values{"status": {"planned"}, "progress.episode": {"0"}})
	dark, ship := e.itemID(t, "tv", "tvdata", "dark"), e.itemID(t, "tv", "tvdata", "1899")
	b := e.page(t, "/tv?view=table")
	mustRows(t, b, "1899", "Dark")
	mustContain(t, b, statusCell("Watching"), statusCell("Planned"), "3 / 26", "0 / 8")

	// episode count plus a position inside the episode
	b = e.do("GET", "/list/"+dark+"/edit", nil, true).Body.String()
	mustContain(t, b, `value="episode,position"`, `name="progress.position"`)
	rec := e.do("POST", "/list/"+dark+"/edit", url.Values{"status": {"active"}, "progress_keys": {"episode,position"},
		"progress.episode": {"3"}, "progress.position": {"12:34"}}, true)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("edit: %d %s", rec.Code, rec.Body.String())
	}
	b = e.page(t, "/tv?view=table")
	mustContain(t, b, ">Episode</span>", "<span>3 / 26</span>", ">Position</span>", "<span>0:12:34</span>")
	mustContain(t, e.page(t, "/tv/tvdata/dark"), "Episode: 3 / 26", "Position: 0:12:34")

	// +1 increments the primary episode and resets the position track
	rec = e.do("POST", "/list/"+dark+"/increment", url.Values{}, true)
	if rec.Code != 200 {
		t.Fatalf("increment: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "<span>4 / 26</span>", "<span>0:00:00</span>")
	if en, _ := e.lib.Entry(context.Background(), mustID(t, dark)); en == nil || en.Progress["episode"] != 4 {
		t.Fatalf("entry %+v", en)
	}
	// a bad position is refused
	rec = e.do("POST", "/list/"+dark+"/edit", url.Values{"status": {"active"}, "progress.episode": {"4"}, "progress.position": {"1:99:00"}}, true)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad position: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Position must be a time like")

	// progress filters: not started / in progress / finished
	mustRows(t, e.page(t, "/tv?progress=not_started&view=table"), "1899")
	mustRows(t, e.page(t, "/tv?progress=in_progress&view=table"), "Dark")
	mustRows(t, e.page(t, "/tv?progress=finished&view=table"))
	rec = e.do("POST", "/list/"+ship+"/edit", url.Values{"status": {"completed"}, "progress.episode": {"8"}}, true)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("finish: %d %s", rec.Code, rec.Body.String())
	}
	b = e.page(t, "/tv?progress=finished&view=table")
	mustRows(t, b, "1899")
	mustContain(t, b, statusCell("Watched"), "8 / 8", `<option value="finished" selected>`, "Clear filters")
	mustNotContain(t, b, `/list/`+ship+`/increment`) // at the total: no +1
	mustRows(t, e.page(t, "/tv?progress=not_started&view=table"))
	// the filter combines with the status tab
	mustRows(t, e.page(t, "/tv?status=active&progress=finished&view=table"))
	// an unknown state is named in the notice and ignored
	b = e.page(t, "/tv?progress=halfway&view=table")
	mustContain(t, b, "Ignored unknown or invalid parameters: <code>progress</code>")
	mustRows(t, b, "1899", "Dark")
	// +1 at the total stays capped
	if rec := e.do("POST", "/list/"+ship+"/increment", url.Values{}, true); rec.Code != 200 || !strings.Contains(rec.Body.String(), "8 / 8") {
		t.Fatalf("capped increment: %d %s", rec.Code, rec.Body.String())
	}
}

func TestGenresAndTagsWorkForFixturePlugins(t *testing.T) {
	e := newProofEnv(t)
	e.add(t, "book", "books", "earthsea", url.Values{"status": {"active"}})
	e.add(t, "book", "books", "dune", url.Values{"status": {"planned"}})
	e.add(t, "tv", "tvdata", "dark", url.Values{"status": {"active"}})
	e.add(t, "tv", "tvdata", "1899", url.Values{"status": {"planned"}})

	// book: genre chips, no tags anywhere
	b := e.page(t, "/book/books/earthsea")
	mustContain(t, b, ">Genres<", ">Fantasy</li>", ">Classic</li>")
	mustNotContain(t, b, ">Tags<", "Show spoiler tags")
	b = e.page(t, "/book?view=table")
	mustContain(t, b, `id="filter-genre-select" name="genre"`, ">Fantasy (1)<", ">Science Fiction (1)<")
	mustNotContain(t, b, `name="tag"`, `name="tag_mode"`)
	mustRows(t, e.page(t, "/book?genre=fantasy&view=table"), "A Wizard of Earthsea")
	b = e.page(t, "/book?tag=Ship&view=table")
	mustContain(t, b, "Ignored unknown or invalid parameters: <code>tag</code>")
	mustRows(t, b, "Dune", "A Wizard of Earthsea")

	// tv: genres and tags, ranks and spoilers
	b = e.page(t, "/tv/tvdata/dark")
	mustContain(t, b, ">Genres<", ">Mystery</li>", ">Drama</li>", ">Tags<", `Time Travel <span class="text-xs text-slate-400">90%</span>`,
		"Show spoiler tags", ">Twist</li>")
	b = e.page(t, "/tv?view=table")
	mustContain(t, b, `name="genre"`, `name="tag"`, ">Mystery (2)<", ">Time Travel (1)<", ">Ship (1)<")
	mustRows(t, e.page(t, "/tv?genre=Mystery&view=table"), "1899", "Dark")
	mustRows(t, e.page(t, "/tv?genre=Mystery&genre=Drama&genre_mode=all&view=table"), "Dark")
	mustRows(t, e.page(t, "/tv?tag=Ship&tag=Time+Travel&view=table"), "1899", "Dark")
	mustRows(t, e.page(t, "/tv?tag=Ship&tag=Time+Travel&tag_mode=all&view=table"))

	// terms on a kind that does not enable them are refused, nothing changes
	before := dump(t, e.env)
	form := url.Values{"title_lang": {"en"}, "title": {"Dune"}, "field.author": {"Frank Writer"}, "field.pages": {"600"},
		"field.isbn": {"9780000000002"}, "genres": {"Science Fiction"}, "tags": {"Sand"}}
	rec := e.do("POST", "/book/books/dune/edit", form, false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("book tags: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "This item has no tags to edit.")
	rec = e.do("POST", "/book/manual/new", url.Values{"title_lang": {"en"}, "title": {"Manual"}, "status": {"planned"}, "tags": {"Sand"}}, false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("manual book with tags: %d %s", rec.Code, rec.Body.String())
	}
	mustContain(t, rec.Body.String(), "This item has no tags to edit.")
	if dump(t, e.env) != before {
		t.Fatal("refused terms changed the database")
	}

	// genres are editable on the book, tags on the show
	form.Del("tags")
	form.Set("genres", "Science Fiction\nDesert")
	if rec := e.do("POST", "/book/books/dune/edit", form, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("book genres: %d %s", rec.Code, rec.Body.String())
	}
	mustRows(t, e.page(t, "/book?genre=Desert&view=table"), "Dune")
	rec = e.do("POST", "/tv/tvdata/1899/edit", url.Values{"title_lang": {"en"}, "title": {"1899"}, "field.type": {"miniseries"},
		"field.episodes": {"8"}, "field.year": {"2022"}, "genres": {"Mystery"}, "tags": {"Ship\nFog:40\nEnding!"}}, false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("tv tags: %d %s", rec.Code, rec.Body.String())
	}
	mustContain(t, e.page(t, "/tv/tvdata/1899"), ">Ship</li>", `Fog <span class="text-xs text-slate-400">40%</span>`, "Show spoiler tags", ">Ending</li>")
	mustRows(t, e.page(t, "/tv?tag=Fog&view=table"), "1899")
}

func TestPluginsAreIsolated(t *testing.T) {
	// the TV cast and the anime cast share a source person id and name: people
	// are namespaced per plugin, so they are two people
	e := newProofEnv(t, func(_, _ *plugintest.Plugin, _, tc *plugintest.Catalog) {
		it := tc.Items["dark"]
		it.Credits[0].With = &plugin.PersonRef{ID: "a1", Name: "Mamoru Miyano"}
		tc.Items["dark"] = it
	})
	e.add(t, "anime", "anilist", "1535", url.Values{"status": {"active"}, "progress": {"3"}})
	e.add(t, "book", "books", "earthsea", url.Values{"status": {"active"}, "progress.pages": {"10"}})
	e.add(t, "tv", "tvdata", "dark", url.Values{"status": {"active"}})
	e.drain(t)

	mustRows(t, e.page(t, "/anime?view=table"), "デスノート")
	mustRows(t, e.page(t, "/book?view=table"), "A Wizard of Earthsea")
	mustRows(t, e.page(t, "/tv?view=table"), "Dark")
	// list search and filters only ever see the plugin's own items
	mustRows(t, e.page(t, "/book?q=Death&view=table"))
	mustRows(t, e.page(t, "/tv?q=Earthsea&view=table"))
	// source search asks the plugin's own source
	b := e.page(t, "/book/search?q=a")
	mustNotContain(t, b, "デスノート", "Dark")
	if e.book.Calls(plugintest.OpSearch) != 1 || e.tv.Calls(plugintest.OpSearch) != 0 {
		t.Fatalf("searches book=%d tv=%d", e.book.Calls(plugintest.OpSearch), e.tv.Calls(plugintest.OpSearch))
	}
	// an item is only reachable under its own plugin
	for _, p := range []string{"/book/anilist/1535", "/anime/books/earthsea", "/tv/books/earthsea", "/book/tvdata/dark"} {
		if rec := e.get(p); rec.Code != http.StatusNotFound {
			t.Errorf("%s -> %d", p, rec.Code)
		}
	}

	// person pages: two people with the same name, each listing only their plugin's item
	var animeActor, tvActor, author string
	for _, p := range e.people(t) {
		switch {
		case p.PluginID == "anime" && p.Name == "Mamoru Miyano":
			animeActor = "/people/" + itoa(p.ID)
		case p.PluginID == "tv" && p.Name == "Mamoru Miyano":
			tvActor = "/people/" + itoa(p.ID)
		case p.PluginID == "book" && p.Name == "Ursula Writer":
			author = "/people/" + itoa(p.ID)
		}
	}
	if animeActor == "" || tvActor == "" || author == "" || animeActor == tvActor {
		t.Fatalf("people: %+v", e.people(t))
	}
	b = e.page(t, animeActor)
	mustContain(t, b, "デスノート", "as Light Yagami")
	mustNotContain(t, b, "Dark", "Jonas")
	b = e.page(t, tvActor)
	mustContain(t, b, `href="/tv/tvdata/dark"`, "as Jonas")
	mustNotContain(t, b, "デスノート", "Light Yagami")
	b = e.page(t, author)
	mustContain(t, b, `href="/book/books/earthsea"`)
	mustNotContain(t, b, "デスノート", "Dark")
	mustContain(t, e.page(t, "/tv/tvdata/dark"), `href="`+tvActor+`"`)
	mustNotContain(t, e.page(t, "/tv/tvdata/dark"), `href="`+animeActor+`"`)

	// the per-plugin export selects only the plugin's entries
	rec := e.get("/book/export/lines")
	if rec.Code != 200 || rec.Body.String() != "book|A Wizard of Earthsea|active|pages=10\n" {
		t.Fatalf("book export: %d %q", rec.Code, rec.Body.String())
	}
	rec = e.get("/anime/export/json")
	if rec.Code != 200 {
		t.Fatalf("anime export: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Death Note")
	mustNotContain(t, rec.Body.String(), "Earthsea", "Dark")

	// disabling the book plugin hides only it
	bookID, animeID, tvID := e.itemID(t, "book", "books", "earthsea"), e.itemID(t, "anime", "anilist", "1535"), e.itemID(t, "tv", "tvdata", "dark")
	before := dump(t, e.env)
	if rec := e.do("POST", "/settings/plugins/book", url.Values{"enabled": {"0"}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("disable: %d", rec.Code)
	}
	for _, p := range []string{"/book", "/book/search?q=a", "/book/books/earthsea", "/book/export/lines", "/list/" + bookID + "/edit", author} {
		if rec := e.get(p); rec.Code != http.StatusNotFound {
			t.Errorf("disabled %s -> %d", p, rec.Code)
		}
	}
	if rec := e.do("POST", "/list/"+bookID+"/increment", url.Values{}, true); rec.Code != http.StatusNotFound {
		t.Errorf("disabled increment -> %d", rec.Code)
	}
	b = e.page(t, "/settings")
	nav := navOf(t, b)
	mustContain(t, nav, `href="/anime"`, `href="/tv"`)
	mustNotContain(t, nav, `href="/book"`)
	imp := e.page(t, "/settings/import")
	mustContain(t, imp, `value="anime/yamtrack"`, `value="tv/tv-json"`)
	mustNotContain(t, imp, "book/books-csv")
	mustRows(t, e.page(t, "/anime?view=table"), "デスノート")
	mustRows(t, e.page(t, "/tv?view=table"), "Dark")
	mustContain(t, e.page(t, tvActor), "as Jonas")
	mustContain(t, e.page(t, animeActor), "as Light Yagami")
	for _, id := range []string{animeID, tvID} {
		if rec := e.do("POST", "/list/"+id+"/increment", url.Values{}, true); rec.Code != 200 {
			t.Errorf("increment %s -> %d", id, rec.Code)
		}
	}
	// the book's rows are all still there (only the settings flag and the two increments changed)
	after := dump(t, e.env)
	for _, line := range strings.Split(before, "\n") {
		if strings.HasPrefix(line, "items ") || strings.HasPrefix(line, "people ") || strings.HasPrefix(line, "item_genres ") {
			if !strings.Contains(after, line) {
				t.Errorf("row lost or changed while disabled: %s", line)
			}
		}
	}
	// enabling brings it back unchanged
	if rec := e.do("POST", "/settings/plugins/book", url.Values{"enabled": {"1"}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("enable: %d", rec.Code)
	}
	b = e.page(t, "/book?view=table")
	mustRows(t, b, "A Wizard of Earthsea")
	mustContain(t, b, "10 / 250")
	mustContain(t, e.page(t, author), `href="/book/books/earthsea"`)
}

// people lists every stored person.
func (e *proofEnv) people(t *testing.T) []library.Person {
	t.Helper()
	rows, err := e.d.Query(`SELECT id FROM people ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	var out []library.Person
	for _, id := range ids {
		p, err := e.lib.Person(context.Background(), id)
		if err != nil || p == nil {
			t.Fatalf("person %d: %v", id, err)
		}
		out = append(out, *p)
	}
	return out
}

// uploadImport posts a file to the import page for importer ("plugin/id").
func (e *proofEnv) uploadImport(t *testing.T, importerRef, name, data string) *httptest.ResponseRecorder {
	t.Helper()
	body, ct := multipartBody(t, url.Values{"importer": {importerRef}}, &upload{field: "file", filename: name, ctype: "text/plain", data: []byte(data)})
	req := httptest.NewRequest("POST", "/settings/import", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func TestThreePluginsShareOneQueueAndImportRunner(t *testing.T) {
	e := newProofEnv(t)
	e.add(t, "anime", "anilist", "1535", url.Values{"status": {"active"}})
	e.add(t, "book", "books", "earthsea", url.Values{"status": {"active"}})
	e.add(t, "tv", "tvdata", "dark", url.Values{"status": {"active"}})
	want := []jobRow{{"sync_item", "anime", "pending", ""}, {"sync_item", "book", "pending", ""}, {"sync_item", "tv", "pending", ""}}
	if got := e.jobRows(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("jobs = %+v", got)
	}

	// the one queue pauses only the disabled plugin's job
	e.disable(t, "tv")
	e.drain(t)
	want = []jobRow{{"sync_item", "anime", "done", ""}, {"sync_item", "book", "done", ""}, {"sync_item", "tv", "pending", ""}}
	if got := e.jobRows(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("jobs = %+v", got)
	}
	mustContain(t, e.page(t, "/book/books/earthsea"), "Ursula Writer")
	if err := e.reg.SetEnabled(context.Background(), "tv", true); err != nil {
		t.Fatal(err)
	}
	mustContain(t, e.page(t, "/tv?view=table"), "partially synced")
	e.drain(t)
	b := e.page(t, "/tv/tvdata/dark")
	mustContain(t, b, "Jonas", "Louis Actor")
	mustNotContain(t, e.page(t, "/tv?view=table"), "partially synced")
	// covers of every plugin went through the same download path
	for _, p := range []struct{ plugin, source, id string }{{"anime", "anilist", "1535"}, {"book", "books", "earthsea"}, {"tv", "tvdata", "dark"}} {
		it := e.savedItems(t, p.plugin, p.source, p.id)[0]
		if p.plugin == "anime" && !strings.HasSuffix(it.CoverPath, ".jpg") {
			t.Errorf("anime cover %q", it.CoverPath)
		}
		if it.SyncState != "done" {
			t.Errorf("%s sync state %q", p.plugin, it.SyncState)
		}
	}

	// the one import runner: a book import, then (while it is still pending)
	// a tv import is refused, then the tv import after the book one
	rec := e.uploadImport(t, "book/books-csv", "books.txt", "9780000000002;Dune;completed;pages=600\n9999;Lost Book;planned\nbroken line\n")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("book import: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.uploadImport(t, "tv/tv-json", "tv.txt", "1899;1899;active;episode=2\n")
	if rec.Code != http.StatusConflict {
		t.Fatalf("second import while one is active: %d", rec.Code)
	}
	e.drain(t)
	b = e.page(t, "/settings/import")
	mustContain(t, b, "books.txt", "Done", `data-count="imported">1<`, `data-count="unmatched">1<`, `data-count="skipped">1<`,
		"Lost Book", "Not found on Fake Books")
	b = e.page(t, "/book?view=table")
	mustRows(t, b, "Lost Book", "Dune", "A Wizard of Earthsea")
	mustContain(t, b, "600 / 600", statusCell("Read"), ">unmatched</span>")

	rec = e.uploadImport(t, "tv/tv-json", "tv.txt", "1899;1899;active;episode=2\n")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("tv import: %d %s", rec.Code, rec.Body.String())
	}
	e.drain(t)
	mustContain(t, e.page(t, "/settings/import"), "tv.txt", "Done", `data-count="imported">1<`)
	b = e.page(t, "/tv?view=table")
	mustRows(t, b, "1899", "Dark")
	mustContain(t, b, "2 / 8")

	// an import of a disabled plugin waits in the shared queue: the page says
	// why, it still blocks a second import, and it finishes once enabled
	rec = e.uploadImport(t, "book/books-csv", "again.txt", "9780000000001;A Wizard of Earthsea;completed\n")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("book import again: %d %s", rec.Code, rec.Body.String())
	}
	e.disable(t, "book")
	e.drain(t)
	b = e.page(t, "/settings/import")
	mustContain(t, b, "again.txt", `data-state="pending"`, "Cancel import",
		"This import is paused because the Books plugin is disabled. Enable it on the Plugins page of Settings to continue, or cancel the import.")
	rec = e.uploadImport(t, "tv/tv-json", "tv2.txt", "dark;Dark;active\n")
	if rec.Code != http.StatusConflict {
		t.Fatalf("import while a paused one is pending: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "An import is already running.", "This import is paused because the Books plugin is disabled.")
	if err := e.reg.SetEnabled(context.Background(), "book", true); err != nil {
		t.Fatal(err)
	}
	mustNotContain(t, e.page(t, "/settings/import"), "is paused because")
	e.drain(t)
	b = e.page(t, "/settings/import")
	mustContain(t, b, "again.txt", `data-state="done"`)
	mustNotContain(t, b, "is paused because")

	// everything ran through the one jobs table: three imports and a sync job
	// per saved item, each naming its plugin
	var kinds []string
	for _, j := range e.jobRows(t) {
		if j.State != "done" || j.PluginID == "" {
			t.Errorf("job %+v", j)
		}
		kinds = append(kinds, j.Kind+"/"+j.PluginID)
	}
	slices.Sort(kinds)
	wantKinds := []string{"import_run/book", "import_run/book", "import_run/tv", "sync_item/anime", "sync_item/book", "sync_item/book", "sync_item/book", "sync_item/tv", "sync_item/tv"}
	if !reflect.DeepEqual(kinds, wantKinds) {
		t.Fatalf("job kinds %v, want %v", kinds, wantKinds)
	}
	// the imported Dune got its author through the shared sync job
	mustContain(t, e.page(t, "/book/books/dune"), "Frank Writer")
}

func TestNavigationAndSettingsWithThreePlugins(t *testing.T) {
	e := newProofEnv(t)
	b := e.page(t, "/settings")
	nav := navOf(t, b)
	order := []string{`href="/anime"`, `href="/book"`, `href="/tv"`, `href="/settings"`}
	last := -1
	for _, s := range order {
		i := strings.Index(nav, s)
		if i <= last {
			t.Fatalf("nav order: %s at %d (after %d): %s", s, i, last, nav)
		}
		last = i
	}
	mustContain(t, nav, ">Anime<", ">Books<", ">Shows<")
	b = e.page(t, "/settings/plugins")
	// Settings > Plugins lists the three plugins with their sources and importers and a toggle each
	for _, p := range []struct{ id, name, source, importer string }{
		{"anime", "Anime", "AniList", "Yamtrack CSV"}, {"book", "Books", "Fake Books", "Books CSV"}, {"tv", "TV", "Fake TV", "TV JSON"}} {
		i := strings.Index(b, `data-plugin="`+p.id+`"`)
		if i < 0 {
			t.Fatalf("no settings row for %s", p.id)
		}
		row := b[i : i+strings.Index(b[i:], "</li>")]
		mustContain(t, row, p.name, p.source, p.importer, `action="/settings/plugins/`+p.id+`"`, `name="enabled" value="0"`, `href="/`+p.id+`"`)
	}
	// the import page offers every plugin's importer
	mustContain(t, e.page(t, "/settings/import"), `<option value="anime/yamtrack">Yamtrack CSV (Anime)</option>`,
		`<option value="book/books-csv">Books CSV (Books)</option>`, `<option value="tv/tv-json">TV JSON (TV)</option>`)

	// every list page marks its own nav item and uses its plugin's labels
	for _, p := range []struct {
		path, heading string
		tabs          []string
	}{
		{"/anime", "Anime", []string{"Watching (0)", "Plan to watch (0)"}},
		{"/book", "Books", []string{"Reading (0)", "Want to read (0)", "Read (0)"}},
		{"/tv", "Shows", []string{"Watching (0)", "Planned (0)", "Watched (0)"}},
	} {
		b := e.page(t, p.path)
		nav := navOf(t, b)
		if strings.Count(nav, `aria-current="page"`) != 1 {
			t.Fatalf("%s: %s", p.path, nav)
		}
		i := strings.Index(nav, `aria-current="page"`)
		if a := nav[strings.LastIndex(nav[:i], "<a "):i]; !strings.Contains(a, `href="`+p.path+`"`) {
			t.Errorf("%s: active item %s", p.path, a)
		}
		mustContain(t, b, `<h1 class="text-3xl font-semibold">`+p.heading+`</h1>`)
		mustContain(t, b, p.tabs...)
	}
	mustContain(t, e.page(t, "/book/search"), "Search books")
	mustContain(t, e.page(t, "/tv/search"), "Search shows")

	// the home page follows the enabled plugins in registry order
	for _, step := range []struct{ toggle, want string }{{"", "/anime"}, {"anime", "/book"}, {"book", "/tv"}, {"tv", "/settings/plugins"}} {
		if step.toggle != "" {
			if rec := e.do("POST", "/settings/plugins/"+step.toggle, url.Values{"enabled": {"0"}}, false); rec.Code != http.StatusSeeOther {
				t.Fatalf("disable %s: %d", step.toggle, rec.Code)
			}
		}
		if rec := e.get("/"); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != step.want {
			t.Errorf("after disabling %q: / -> %d %q", step.toggle, rec.Code, rec.Header().Get("Location"))
		}
	}
	// with all three off the nav has only Settings and every row offers Enable
	b = e.page(t, "/settings/plugins")
	nav = navOf(t, b)
	mustNotContain(t, nav, `href="/anime"`, `href="/book"`, `href="/tv"`)
	if n := strings.Count(b, `name="enabled" value="1"`); n != 3 {
		t.Fatalf("enable buttons: %d", n)
	}
	mustContain(t, e.page(t, "/settings/import"), "data-no-importers")
	// enabling tv alone brings back only tv
	if rec := e.do("POST", "/settings/plugins/tv", url.Values{"enabled": {"1"}}, false); rec.Code != http.StatusSeeOther {
		t.Fatal(rec.Code)
	}
	nav = navOf(t, e.page(t, "/settings"))
	mustContain(t, nav, `href="/tv"`)
	mustNotContain(t, nav, `href="/anime"`, `href="/book"`)
	if rec := e.get("/"); rec.Header().Get("Location") != "/tv" {
		t.Fatalf("/ -> %q", rec.Header().Get("Location"))
	}
}

func TestBackupRoundTripWithThreePlugins(t *testing.T) {
	setCovers := func(imgs string, bc, tc *plugintest.Catalog) {
		it := bc.Items["earthsea"]
		it.Data.CoverURL = imgs + "/earthsea.jpg"
		bc.Items["earthsea"] = it
		it = tc.Items["dark"]
		it.Data.CoverURL = imgs + "/dark.avif"
		tc.Items["dark"] = it
	}
	a := newProofEnv(t)
	setCovers(a.imgs.URL, a.book, a.tv)
	a.add(t, "anime", "anilist", "1535", url.Values{"status": {"active"}, "rating": {"9"}, "progress": {"5"}, "comment": {"anime note"}})
	a.add(t, "book", "books", "earthsea", url.Values{"status": {"active"}, "progress.pages": {"42"}})
	a.add(t, "tv", "tvdata", "dark", url.Values{"status": {"paused"}, "progress.episode": {"7"}})
	dark := a.itemID(t, "tv", "tvdata", "dark")
	if rec := a.do("POST", "/list/"+dark+"/edit", url.Values{"status": {"paused"}, "progress_keys": {"episode,position"},
		"progress.episode": {"7"}, "progress.position": {"1:02:03"}}, true); rec.Code != http.StatusNoContent {
		t.Fatalf("edit: %d", rec.Code)
	}
	a.drain(t)
	// a disabled plugin is still part of the backup
	a.disable(t, "tv")
	data := a.download(t)
	_, want, names := backupJSON(t, data)
	if len(want) != 3 {
		t.Fatalf("backup items: %d", len(want))
	}
	var coverFiles int
	for _, n := range names {
		if strings.HasPrefix(n, "covers/") {
			coverFiles++
		}
	}
	if coverFiles != 3 {
		t.Fatalf("cover files in the archive: %v", names)
	}

	b := newProofEnv(t)
	rec := b.restoreUpload(t, data)
	if rec.Code != 200 {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body.String())
	}
	mustContain(t, rec.Body.String(), `data-count="created">3<`, `data-count="skipped">0<`)
	_, got, _ := backupJSON(t, b.download(t))
	if !reflect.DeepEqual(got, want) {
		gj, _ := json.Marshal(got)
		wj, _ := json.Marshal(want)
		t.Fatalf("round trip differs:\n%s\n%s", gj, wj)
	}

	// the restored server shows every plugin's data: entries, progress, covers, people and terms
	l := b.page(t, "/book?view=table")
	mustRows(t, l, "A Wizard of Earthsea")
	mustContain(t, l, "42 / 250", statusCell("Reading"))
	l = b.page(t, "/tv?view=table")
	mustRows(t, l, "Dark")
	mustContain(t, l, "<span>7 / 26</span>", "<span>1:02:03</span>", statusCell("Paused"))
	l = b.page(t, "/anime?view=table")
	mustRows(t, l, "デスノート")
	mustContain(t, l, "5 / 37", `data-score="9"`)
	for _, p := range []struct{ plugin, source, id, img string }{
		{"anime", "anilist", "1535", "/anime.jpg"}, {"book", "books", "earthsea", "/earthsea.jpg"}, {"tv", "tvdata", "dark", "/dark.avif"}} {
		it := b.savedItems(t, p.plugin, p.source, p.id)[0]
		ctype, want := proofImage(p.img)
		if ext := filepath.Ext(p.img); !strings.HasSuffix(it.CoverPath, ext) || strings.Contains(it.CoverPath, "/") {
			t.Fatalf("%s cover %q", p.plugin, it.CoverPath)
		}
		rec := b.get("/covers/" + it.CoverPath)
		if body, _ := io.ReadAll(rec.Body); rec.Code != 200 || !bytes.Equal(body, want) || rec.Header().Get("Content-Type") != ctype {
			t.Errorf("%s cover served %d %q", p.plugin, rec.Code, body)
		}
	}
	o := b.page(t, "/tv/tvdata/dark")
	mustContain(t, o, ">Mystery</li>", ">Drama</li>", `Time Travel <span class="text-xs text-slate-400">90%</span>`, "Show spoiler tags", ">Twist</li>",
		"Jonas", "Louis Actor")
	mustContain(t, b.page(t, "/book/books/earthsea"), ">Fantasy</li>", "Ursula Writer")
	var louis, miyano string
	for _, p := range b.people(t) {
		switch p.Name {
		case "Louis Actor":
			louis = "/people/" + itoa(p.ID)
		case "Mamoru Miyano":
			miyano = "/people/" + itoa(p.ID)
		}
	}
	if louis == "" || miyano == "" {
		t.Fatalf("people: %+v", b.people(t))
	}
	mustContain(t, b.page(t, louis), `href="/tv/tvdata/dark"`, "as Jonas")
	mustContain(t, b.page(t, miyano), `href="/anime/anilist/1535"`, "as Light Yagami")
	// restoring the same archive again skips all three
	mustContain(t, b.restoreUpload(t, data).Body.String(), `data-count="created">0<`, `data-count="skipped">3<`)
}

package jobs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// The legacy sync tests, ported to the generic handler: the "tv" fixture
// plugin (source "tvdata", a plugintest.FakeSource) stands in for the
// former AniList provider, its People result for the former cast.

type syncEnv struct {
	d    *sql.DB
	lib  *library.Library
	reg  *plugin.Registry
	deps SyncDeps
	src  *plugintest.FakeSource
	tv   *plugintest.Plugin
	srv  *httptest.Server
}

// newSyncEnv serves /ok/<n> as images and everything else as 404.
func newSyncEnv(t *testing.T) *syncEnv {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/ok/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("img:" + r.URL.Path))
	}))
	t.Cleanup(srv.Close)
	d := openDB(t)
	tv, book := plugintest.TVPlugin(), plugintest.BookPlugin()
	reg := plugin.NewRegistry(plugintest.NewMemEnabled())
	for _, p := range []plugin.Plugin{tv, book} {
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	lib := library.New(d, reg)
	src := tv.Sources["tvdata"].(*plugintest.FakeSource)
	return &syncEnv{d: d, lib: lib, reg: reg, src: src, tv: tv, srv: srv, deps: SyncDeps{
		Library:   lib,
		Registry:  reg,
		HTTP:      srv.Client(),
		CoversDir: t.TempDir(),
	}}
}

// save stores a pending "tv" show of source src (tvdata or manual).
func (e *syncEnv) save(t *testing.T, src, cover string) int64 {
	t.Helper()
	return e.saveID(t, src, "42", cover)
}

func (e *syncEnv) saveID(t *testing.T, src, sourceID, cover string) int64 {
	t.Helper()
	d := plugin.ItemData{ItemSummary: plugin.ItemSummary{
		Source: src, ID: sourceID, Kind: "show", CoverURL: cover, Titles: []plugin.Title{{Lang: "en", Text: "T"}},
	}}
	id, err := e.lib.SaveItem(context.Background(), "tv", "show", d, library.WithSyncState("pending"))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func person(id, img string) plugin.PersonRef {
	return plugin.PersonRef{ID: id, Name: "P" + id, ImageURL: img}
}

func payload(id int64, pluginID string) json.RawMessage {
	b, _ := json.Marshal(SyncPayload{ItemID: id, PluginID: pluginID})
	return b
}

func (e *syncEnv) run(id int64) error {
	return SyncItem(e.deps)(context.Background(), payload(id, "tv"))
}

func (e *syncEnv) saved(t *testing.T, id int64) *library.Item {
	t.Helper()
	s, err := e.lib.Item(context.Background(), id)
	if err != nil || s == nil {
		t.Fatalf("get: %v %v", s, err)
	}
	return s
}

func (e *syncEnv) people(t *testing.T, id int64) []library.Person {
	t.Helper()
	ppl, err := e.lib.ItemPeople(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return ppl
}

func isLocal(p string) bool { return p != "" && !strings.HasPrefix(p, "http") }

func TestSyncItemCompletesAndStoresCredits(t *testing.T) {
	e := newSyncEnv(t)
	id := e.save(t, "tvdata", e.srv.URL+"/ok/cover")
	actor := person("a1", e.srv.URL+"/ok/actor")
	e.src.Credits = []plugin.Credit{{Role: "actor", Person: person("c1", e.srv.URL+"/ok/char"), With: &actor, WithRole: "Voice", Detail: "MAIN"}}
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	s := e.saved(t, id)
	if s.SyncState != "done" || !isLocal(s.CoverPath) {
		t.Fatalf("state=%q cover=%q", s.SyncState, s.CoverPath)
	}
	ppl := e.people(t, id)
	if len(ppl) != 2 {
		t.Fatalf("people %v", ppl)
	}
	for _, p := range ppl {
		if !isLocal(p.ImagePath) {
			t.Fatalf("image not local: %+v", p)
		}
	}
	cs, err := e.lib.Credits(context.Background(), id)
	if err != nil || len(cs) != 1 || cs[0].Detail != "MAIN" || cs[0].With == nil || cs[0].WithRole != "Voice" {
		t.Fatalf("credits %+v %v", cs, err)
	}
	if e.src.Calls(plugintest.OpPeople) != 1 {
		t.Fatalf("people calls %d", e.src.Calls(plugintest.OpPeople))
	}
	// idempotent re-run keeps local paths and stays done
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	if s2 := e.saved(t, id); s2.SyncState != "done" || s2.CoverPath != s.CoverPath {
		t.Fatalf("rerun: %+v", s2)
	}
	if ppl2 := e.people(t, id); len(ppl2) != 2 || !isLocal(ppl2[0].ImagePath) {
		t.Fatalf("rerun people: %+v", ppl2)
	}
}

// A second fixture plugin (books, authors without counterpart) syncs the
// same way: nothing in the handler is specific to one plugin.
func TestSyncItemBookFixtureStoresCredits(t *testing.T) {
	e := newSyncEnv(t)
	p, _ := e.reg.Get("book")
	books := p.Source("books").(*plugintest.FakeSource)
	books.Credits = []plugin.Credit{{Role: "author", Person: person("u", e.srv.URL+"/ok/u")}}
	id, err := e.lib.SaveItem(context.Background(), "book", "book", plugin.ItemData{ItemSummary: plugin.ItemSummary{
		Source: "books", ID: "7", Kind: "book", Titles: []plugin.Title{{Lang: "en", Text: "B"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := SyncItem(e.deps)(context.Background(), payload(id, "book")); err != nil {
		t.Fatal(err)
	}
	if s := e.saved(t, id); s.SyncState != "done" {
		t.Fatalf("state %q", s.SyncState)
	}
	if ppl := e.people(t, id); len(ppl) != 1 || ppl[0].Name != "Pu" || !isLocal(ppl[0].ImagePath) {
		t.Fatalf("people %+v", ppl)
	}
}

func TestSyncItemManualIsDoneWithoutSource(t *testing.T) {
	e := newSyncEnv(t)
	e.tv.Sources = nil
	id := e.save(t, "manual", "")
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	if s := e.saved(t, id); s.SyncState != "done" {
		t.Fatalf("state %q", s.SyncState)
	}
}

func TestSyncItemManualDownloadsRemoteCoverWithoutCredits(t *testing.T) {
	e := newSyncEnv(t)
	id := e.save(t, "manual", e.srv.URL+"/ok/cover")
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	if s := e.saved(t, id); s.SyncState != "done" || !isLocal(s.CoverPath) {
		t.Fatalf("state=%q cover=%q", s.SyncState, s.CoverPath)
	}
	// a manual item never asks the source
	if n := e.src.Calls(plugintest.OpPeople); n != 0 {
		t.Fatalf("people calls %d", n)
	}
}

func TestSyncItemManualCoverFailureIsPartialAndKeepsURL(t *testing.T) {
	e := newSyncEnv(t)
	id := e.save(t, "manual", e.srv.URL+"/missing")
	if err := e.run(id); !errors.Is(err, ErrRetryLater) {
		t.Fatalf("err = %v", err)
	}
	if s := e.saved(t, id); s.SyncState != "partial" || s.CoverPath != e.srv.URL+"/missing" {
		t.Fatalf("%+v", s)
	}
}

func TestSyncItemCoverFailureIsPartial(t *testing.T) {
	e := newSyncEnv(t)
	id := e.save(t, "tvdata", e.srv.URL+"/missing")
	e.src.Credits = []plugin.Credit{{Role: "actor", Person: person("c1", e.srv.URL+"/ok/char")}}
	err := e.run(id)
	if !errors.Is(err, ErrRetryLater) {
		t.Fatalf("err = %v", err)
	}
	s := e.saved(t, id)
	if s.SyncState != "partial" || s.CoverPath != e.srv.URL+"/missing" {
		t.Fatalf("%+v", s)
	}
	// the credits were still synced and their image downloaded
	ppl := e.people(t, id)
	if len(ppl) != 1 || !isLocal(ppl[0].ImagePath) {
		t.Fatalf("people %+v", ppl)
	}
}

func TestSyncItemPersonImageFailureIsPartial(t *testing.T) {
	e := newSyncEnv(t)
	id := e.save(t, "tvdata", e.srv.URL+"/ok/cover")
	e.src.Credits = []plugin.Credit{
		{Role: "actor", Person: person("c1", e.srv.URL+"/missing")},
		{Role: "actor", Person: person("c2", e.srv.URL+"/ok/c2")},
	}
	if err := e.run(id); !errors.Is(err, ErrRetryLater) {
		t.Fatalf("err = %v", err)
	}
	if s := e.saved(t, id); s.SyncState != "partial" || !isLocal(s.CoverPath) {
		t.Fatalf("%+v", s)
	}
	ppl := e.people(t, id)
	var local int
	for _, p := range ppl {
		if isLocal(p.ImagePath) {
			local++
		}
	}
	if local != 1 {
		t.Fatalf("local images = %d (%+v)", local, ppl)
	}
	// once the image becomes available a retry completes
	e.src.Credits[0].Person.ImageURL = e.srv.URL + "/ok/c1"
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	if s := e.saved(t, id); s.SyncState != "done" {
		t.Fatalf("%+v", s)
	}
}

// TestSyncItemRateLimitUsesRetryLater: short source trouble on People leaves
// the item partial and asks for a retry on the bounded partial path, with
// the cause in the chain.
func TestSyncItemRateLimitUsesRetryLater(t *testing.T) {
	for _, perr := range []error{plugin.ErrRateLimited, plugin.ErrUnavailable} {
		e := newSyncEnv(t)
		id := e.save(t, "tvdata", e.srv.URL+"/ok/cover")
		e.src.Errs = map[string]error{plugintest.OpPeople: perr}
		err := e.run(id)
		if !errors.Is(err, perr) || !errors.Is(err, ErrRetryLater) {
			t.Fatalf("err = %v", err)
		}
		if s := e.saved(t, id); s.SyncState != "partial" || !isLocal(s.CoverPath) {
			t.Fatalf("%+v", s)
		}
	}
}

// TestSyncItemPeopleBadResponseFailsPermanently: a source that answered
// with unusable data will answer the same way again, so the job fails at
// once (ErrPermanent, the cause in the chain) with the item left partial,
// instead of burning attempts on retries.
func TestSyncItemPeopleBadResponseFailsPermanently(t *testing.T) {
	e := newSyncEnv(t)
	id := e.save(t, "tvdata", e.srv.URL+"/ok/cover")
	e.src.Errs = map[string]error{plugintest.OpPeople: plugin.ErrBadResponse}
	err := e.run(id)
	if !errors.Is(err, plugin.ErrBadResponse) || !errors.Is(err, ErrPermanent) {
		t.Fatalf("err = %v", err)
	}
	if s := e.saved(t, id); s.SyncState != "partial" || !isLocal(s.CoverPath) {
		t.Fatalf("%+v", s)
	}
}

func TestSyncItemPeopleNotFoundIsDone(t *testing.T) {
	e := newSyncEnv(t)
	id := e.save(t, "tvdata", e.srv.URL+"/ok/cover")
	e.src.Errs = map[string]error{plugintest.OpPeople: plugin.ErrNotFound}
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	if s := e.saved(t, id); s.SyncState != "done" {
		t.Fatalf("%+v", s)
	}
}

func TestSyncItemDeletedItemSucceeds(t *testing.T) {
	e := newSyncEnv(t)
	id := e.save(t, "tvdata", e.srv.URL+"/ok/cover")
	if _, err := e.d.Exec(`DELETE FROM items WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if err := e.run(id); err != nil || e.src.Calls(plugintest.OpPeople) != 0 {
		t.Fatalf("err=%v calls=%d", err, e.src.Calls(plugintest.OpPeople))
	}
}

func TestSyncItemBadPayloadAndMissingSource(t *testing.T) {
	e := newSyncEnv(t)
	if err := SyncItem(e.deps)(context.Background(), json.RawMessage(`{`)); err == nil {
		t.Fatal("bad payload accepted")
	}
	// a plugin that does not provide the item's source: nothing to fetch
	e.tv.Sources = nil
	id := e.save(t, "tvdata", e.srv.URL+"/ok/cover")
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	if s := e.saved(t, id); s.SyncState != "done" || !isLocal(s.CoverPath) {
		t.Fatalf("%+v", s)
	}
	// an item of a plugin that is not installed is a plain error
	e.deps.Registry = plugin.NewRegistry(plugintest.NewMemEnabled())
	other := e.saveID(t, "tvdata", "43", "")
	if err := e.run(other); err == nil || errors.Is(err, ErrRetryLater) {
		t.Fatalf("err = %v", err)
	}
}

func TestSyncItemPluginMismatchFailsPermanently(t *testing.T) {
	e := newSyncEnv(t)
	id := e.save(t, "tvdata", e.srv.URL+"/ok/cover")
	for _, pid := range []string{"book", ""} {
		err := SyncItem(e.deps)(context.Background(), payload(id, pid))
		if !errors.Is(err, ErrPermanent) || errors.Is(err, ErrRetryLater) || !strings.Contains(err.Error(), "plugin") {
			t.Fatalf("%q: err = %v", pid, err)
		}
	}
	if s := e.saved(t, id); s.SyncState != "pending" || e.src.Calls(plugintest.OpPeople) != 0 {
		t.Fatalf("mismatch touched the item: %+v", s)
	}
	// in the queue the job fails at once instead of burning attempts
	clk := newClock()
	q := testQueue(t, e.d, clk)
	q.Register(KindSyncItem, SyncItem(e.deps))
	jid := enqueue(t, q, KindSyncItem, SyncPayload{ItemID: id, PluginID: "book"})
	step(t, q)
	if r := row(t, e.d, jid); r.State != "failed" || r.Attempts != 0 || !strings.Contains(r.LastError, "plugin") {
		t.Fatalf("%+v", r)
	}
}

func TestSyncItemStopsOnCancelledContext(t *testing.T) {
	e := newSyncEnv(t)
	id := e.save(t, "tvdata", e.srv.URL+"/ok/cover")
	e.src.Credits = []plugin.Credit{{Role: "actor", Person: person("c1", e.srv.URL+"/ok/c1")}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := SyncItem(e.deps)(ctx, payload(id, "tv")); err == nil {
		t.Fatal("expected error")
	}
	if s := e.saved(t, id); s.SyncState == "done" {
		t.Fatal("must not be done")
	}
}

func TestSyncJobFor(t *testing.T) {
	kind, p := SyncJobFor("tv")(42)
	if kind != KindSyncItem || p != (SyncPayload{ItemID: 42, PluginID: "tv"}) {
		t.Fatalf("got %q %+v", kind, p)
	}
	b, _ := json.Marshal(p)
	if string(b) != `{"item_id":42,"plugin_id":"tv"}` {
		t.Fatalf("payload json %s", b)
	}
}

// raceServer serves an image and, while serving it, lets the user replace
// the cover (the job read the remote URL before).
func raceServer(t *testing.T, e *syncEnv, id *int64, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := e.lib.SetCoverPath(context.Background(), *id, "user.png"); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	e.deps.HTTP = srv.Client()
	return srv.URL + "/cover"
}

func coverDirFiles(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, x := range ents {
		out = append(out, x.Name())
	}
	return out
}

func TestSyncItemKeepsCoverReplacedDuringDownload(t *testing.T) {
	for _, src := range []string{"tvdata", "manual"} {
		t.Run(src, func(t *testing.T) {
			e := newSyncEnv(t)
			var id int64
			url := raceServer(t, e, &id, "race-"+src)
			id = e.save(t, src, url)
			if err := e.run(id); err != nil {
				t.Fatal(err)
			}
			if s := e.saved(t, id); s.CoverPath != "user.png" || s.SyncState != "done" {
				t.Fatalf("cover %q state %q", s.CoverPath, s.SyncState)
			}
			if f := coverDirFiles(t, e.deps.CoversDir); len(f) != 0 {
				t.Fatalf("downloaded file left behind: %v", f)
			}
		})
	}
}

func TestSyncItemStaleDownloadKeepsSharedFile(t *testing.T) {
	e := newSyncEnv(t)
	var id int64
	url := raceServer(t, e, &id, "shared-bytes")
	// another item already holds the file with the same content
	other := e.saveID(t, "manual", "m1", "")
	id = e.save(t, "tvdata", url)
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	files := coverDirFiles(t, e.deps.CoversDir)
	if len(files) != 0 {
		t.Fatalf("unreferenced stale file kept: %v", files)
	}
	// now let the other item reference the same content and race again
	sum := sha256.Sum256([]byte("shared-bytes"))
	shared := hex.EncodeToString(sum[:]) + ".jpg"
	if err := e.lib.SetCoverPath(context.Background(), other, shared); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.SetCoverPath(context.Background(), id, url); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.SetSyncState(context.Background(), id, "pending"); err != nil {
		t.Fatal(err)
	}
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	if f := coverDirFiles(t, e.deps.CoversDir); len(f) != 1 || f[0] != shared {
		t.Fatalf("shared file removed: %v", f)
	}
}

// A person image the user (or another sync) replaced during the download is
// kept, and the unreferenced download is discarded.
func TestSyncItemPersonImageCompareAndSet(t *testing.T) {
	e := newSyncEnv(t)
	var personID int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := e.lib.SetPersonImagePath(context.Background(), personID, "mine.png"); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("person-bytes"))
	}))
	t.Cleanup(srv.Close)
	e.deps.HTTP = srv.Client()
	id := e.save(t, "tvdata", "")
	e.src.Credits = []plugin.Credit{{Role: "actor", Person: person("c1", srv.URL+"/p.jpg")}}
	// store the credits first to learn the person id the race will touch
	if err := e.lib.SaveCredits(context.Background(), id, e.src.Credits); err != nil {
		t.Fatal(err)
	}
	personID = e.people(t, id)[0].ID
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	if ppl := e.people(t, id); len(ppl) != 1 || ppl[0].ImagePath != "mine.png" {
		t.Fatalf("people %+v", ppl)
	}
	if s := e.saved(t, id); s.SyncState != "done" {
		t.Fatalf("state %q", s.SyncState)
	}
	if f := coverDirFiles(t, e.deps.CoversDir); len(f) != 0 {
		t.Fatalf("downloaded file left behind: %v", f)
	}
}

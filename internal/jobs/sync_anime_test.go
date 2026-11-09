package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/allister/allister/internal/provider"
	"github.com/allister/allister/internal/store"
)

type fakeProv struct {
	cast  []provider.CastMember
	err   error
	calls int
}

func (f *fakeProv) Name() provider.Source { return provider.AniList }
func (f *fakeProv) Search(context.Context, string) ([]provider.SearchResult, error) {
	return nil, nil
}
func (f *fakeProv) Anime(context.Context, string) (provider.Anime, error) {
	return provider.Anime{}, nil
}
func (f *fakeProv) Cast(context.Context, string) ([]provider.CastMember, error) {
	f.calls++
	return f.cast, f.err
}

type syncEnv struct {
	d    *sql.DB
	st   *store.Store
	deps SyncDeps
	prov *fakeProv
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
	st := store.New(d)
	prov := &fakeProv{}
	return &syncEnv{d: d, st: st, prov: prov, srv: srv, deps: SyncDeps{
		Store:     st,
		Providers: map[provider.Source]provider.Provider{provider.AniList: prov},
		HTTP:      srv.Client(),
		CoversDir: t.TempDir(),
	}}
}

func (e *syncEnv) save(t *testing.T, src provider.Source, cover string) int64 {
	t.Helper()
	a := provider.Anime{SearchResult: provider.SearchResult{
		Source: src, ID: "42", CoverURL: cover, Titles: []provider.Title{{Lang: "en", Text: "T"}},
	}}
	id, err := e.st.SaveAnime(context.Background(), a, store.WithSyncState("pending"))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *syncEnv) person(id, img string) provider.Person {
	return provider.Person{Source: provider.AniList, ID: id, Name: "P" + id, ImageURL: img}
}

func (e *syncEnv) run(id int64) error {
	b, _ := json.Marshal(SyncPayload{AnimeID: id})
	return SyncAnime(e.deps)(context.Background(), b)
}

func (e *syncEnv) saved(t *testing.T, id int64) *store.Saved {
	t.Helper()
	s, err := e.st.GetByID(context.Background(), id)
	if err != nil || s == nil {
		t.Fatalf("get: %v %v", s, err)
	}
	return s
}

func isLocal(p string) bool { return p != "" && !strings.HasPrefix(p, "http") }

func TestSyncAnimeCompletes(t *testing.T) {
	e := newSyncEnv(t)
	id := e.save(t, provider.AniList, e.srv.URL+"/ok/cover")
	actor := e.person("a1", e.srv.URL+"/ok/actor")
	e.prov.cast = []provider.CastMember{{Character: e.person("c1", e.srv.URL+"/ok/char"), Actor: &actor, Role: "MAIN"}}
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	s := e.saved(t, id)
	if s.SyncState != "done" || !isLocal(s.CoverPath) {
		t.Fatalf("state=%q cover=%q", s.SyncState, s.CoverPath)
	}
	ppl, err := e.st.AnimePeople(context.Background(), id)
	if err != nil || len(ppl) != 2 {
		t.Fatalf("people %v %v", ppl, err)
	}
	for _, p := range ppl {
		if !isLocal(p.ImagePath) {
			t.Fatalf("image not local: %+v", p)
		}
	}
	// idempotent re-run keeps local paths and stays done
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	if s2 := e.saved(t, id); s2.SyncState != "done" || s2.CoverPath != s.CoverPath {
		t.Fatalf("rerun: %+v", s2)
	}
	if ppl2, _ := e.st.AnimePeople(context.Background(), id); len(ppl2) != 2 || !isLocal(ppl2[0].ImagePath) {
		t.Fatalf("rerun people: %+v", ppl2)
	}
}

func TestSyncAnimeManualIsDoneWithoutProvider(t *testing.T) {
	e := newSyncEnv(t)
	e.deps.Providers = nil
	id := e.save(t, provider.Manual, "")
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	if s := e.saved(t, id); s.SyncState != "done" {
		t.Fatalf("state %q", s.SyncState)
	}
}

func TestSyncAnimeManualDownloadsRemoteCoverWithoutCast(t *testing.T) {
	e := newSyncEnv(t)
	e.deps.Providers = nil // a manual entry never asks a provider
	id := e.save(t, provider.Manual, e.srv.URL+"/ok/cover")
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	if s := e.saved(t, id); s.SyncState != "done" || !isLocal(s.CoverPath) {
		t.Fatalf("state=%q cover=%q", s.SyncState, s.CoverPath)
	}
}

func TestSyncAnimeManualCoverFailureIsPartialAndKeepsURL(t *testing.T) {
	e := newSyncEnv(t)
	id := e.save(t, provider.Manual, e.srv.URL+"/missing")
	if err := e.run(id); !errors.Is(err, ErrRetryLater) {
		t.Fatalf("err = %v", err)
	}
	if s := e.saved(t, id); s.SyncState != "partial" || s.CoverPath != e.srv.URL+"/missing" {
		t.Fatalf("%+v", s)
	}
}

func TestSyncAnimeCoverFailureIsPartial(t *testing.T) {
	e := newSyncEnv(t)
	id := e.save(t, provider.AniList, e.srv.URL+"/missing")
	e.prov.cast = []provider.CastMember{{Character: e.person("c1", e.srv.URL+"/ok/char")}}
	err := e.run(id)
	if !errors.Is(err, ErrRetryLater) {
		t.Fatalf("err = %v", err)
	}
	s := e.saved(t, id)
	if s.SyncState != "partial" || s.CoverPath != e.srv.URL+"/missing" {
		t.Fatalf("%+v", s)
	}
	// the cast was still synced and its image downloaded
	ppl, _ := e.st.AnimePeople(context.Background(), id)
	if len(ppl) != 1 || !isLocal(ppl[0].ImagePath) {
		t.Fatalf("people %+v", ppl)
	}
}

func TestSyncAnimePersonImageFailureIsPartial(t *testing.T) {
	e := newSyncEnv(t)
	id := e.save(t, provider.AniList, e.srv.URL+"/ok/cover")
	e.prov.cast = []provider.CastMember{
		{Character: e.person("c1", e.srv.URL+"/missing")},
		{Character: e.person("c2", e.srv.URL+"/ok/c2")},
	}
	if err := e.run(id); !errors.Is(err, ErrRetryLater) {
		t.Fatalf("err = %v", err)
	}
	if s := e.saved(t, id); s.SyncState != "partial" || !isLocal(s.CoverPath) {
		t.Fatalf("%+v", s)
	}
	ppl, _ := e.st.AnimePeople(context.Background(), id)
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
	e.prov.cast[0].Character.ImageURL = e.srv.URL + "/ok/c1"
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	if s := e.saved(t, id); s.SyncState != "done" {
		t.Fatalf("%+v", s)
	}
}

func TestSyncAnimeCastFailureLeavesPartial(t *testing.T) {
	for _, perr := range []error{provider.ErrRateLimited, provider.ErrUnavailable} {
		e := newSyncEnv(t)
		id := e.save(t, provider.AniList, e.srv.URL+"/ok/cover")
		e.prov.err = perr
		err := e.run(id)
		if !errors.Is(err, perr) || errors.Is(err, ErrRetryLater) {
			t.Fatalf("err = %v", err)
		}
		if s := e.saved(t, id); s.SyncState != "partial" || !isLocal(s.CoverPath) {
			t.Fatalf("%+v", s)
		}
	}
}

func TestSyncAnimeCastNotFoundIsDone(t *testing.T) {
	e := newSyncEnv(t)
	id := e.save(t, provider.AniList, e.srv.URL+"/ok/cover")
	e.prov.err = provider.ErrNotFound
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	if s := e.saved(t, id); s.SyncState != "done" {
		t.Fatalf("%+v", s)
	}
}

func TestSyncAnimeDeletedAnimeSucceeds(t *testing.T) {
	e := newSyncEnv(t)
	id := e.save(t, provider.AniList, e.srv.URL+"/ok/cover")
	if _, err := e.d.Exec(`DELETE FROM anime WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if err := e.run(id); err != nil || e.prov.calls != 0 {
		t.Fatalf("err=%v calls=%d", err, e.prov.calls)
	}
}

func TestSyncAnimeBadPayloadAndMissingProvider(t *testing.T) {
	e := newSyncEnv(t)
	if err := SyncAnime(e.deps)(context.Background(), json.RawMessage(`{`)); err == nil {
		t.Fatal("bad payload accepted")
	}
	e.deps.Providers = nil
	id := e.save(t, provider.AniList, "")
	if err := e.run(id); err == nil || errors.Is(err, ErrRetryLater) {
		t.Fatalf("err = %v", err)
	}
}

func TestSyncAnimeStopsOnCancelledContext(t *testing.T) {
	e := newSyncEnv(t)
	id := e.save(t, provider.AniList, e.srv.URL+"/ok/cover")
	e.prov.cast = []provider.CastMember{{Character: e.person("c1", e.srv.URL+"/ok/c1")}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b, _ := json.Marshal(SyncPayload{AnimeID: id})
	if err := SyncAnime(e.deps)(ctx, b); err == nil {
		t.Fatal("expected error")
	}
	if s := e.saved(t, id); s.SyncState == "done" {
		t.Fatal("must not be done")
	}
}

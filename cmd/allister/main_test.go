package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/allister/allister/internal/config"
	"github.com/allister/allister/internal/jobs"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
	"github.com/allister/allister/internal/plugins/anime"
	"github.com/allister/allister/internal/plugins/book"
	"github.com/allister/allister/internal/plugins/game"
	"github.com/allister/allister/internal/plugins/manga"
	"github.com/allister/allister/internal/plugins/movie"
	"github.com/allister/allister/internal/plugins/tv"
)

func TestServeDrainsInFlightRequestsOnShutdown(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.Write([]byte("done"))
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- serveListener(ctx, srv, ln, 5*time.Second) }()

	body := make(chan string, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String())
		if err != nil {
			body <- "error: " + err.Error()
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		body <- string(b)
	}()
	<-started
	cancel()
	select {
	case err := <-served:
		t.Fatalf("serve returned (%v) while a request was in flight", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("serve = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after the request finished")
	}
	if got := <-body; got != "done" {
		t.Fatalf("response %q", got)
	}
}

func TestServeReturnsListenError(t *testing.T) {
	srv := &http.Server{Addr: "256.0.0.1:bad"}
	if err := serve(context.Background(), srv, time.Second); err == nil {
		t.Fatal("expected listen error")
	}
}

// TestSyncJobsWiresAllInstalledPlugins: every installed plugin (enabled or
// not, with or without sources: a manual item may still hold a remote cover)
// gets the generic sync job carrying its own plugin id.
func TestSyncJobsWiresAllInstalledPlugins(t *testing.T) {
	reg := plugin.NewRegistry(plugintest.NewMemEnabled())
	noSources := plugintest.BookPlugin(plugintest.WithID("nosrc"), plugintest.WithDefaultEnabled(false))
	noSources.M.Sources, noSources.Sources = nil, nil
	noSources.M.Importers, noSources.Importers = nil, nil
	for _, p := range []plugin.Plugin{anime.New(anime.Options{}), plugintest.BookPlugin(), plugintest.TVPlugin(), noSources} {
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	got := syncJobs(reg)
	if len(got) != 4 {
		t.Fatalf("sync jobs for %d plugins: %v", len(got), got)
	}
	for i, id := range []string{"anime", "book", "tv", "nosrc"} {
		job := got[id]
		if job == nil {
			t.Fatalf("no sync job for %s", id)
		}
		if kind, payload := job(int64(i + 7)); kind != jobs.KindSyncItem || payload != (jobs.SyncPayload{ItemID: int64(i + 7), PluginID: id}) {
			t.Fatalf("%s: got %q %+v", id, kind, payload)
		}
	}
}

func TestRemoveRestoreTempsDeletesOnlyRestoreSpools(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"restore-123.zip", "restore-x.zip", "keep.txt"} {
		if err := os.WriteFile(filepath.Join(tmp, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	removeRestoreTemps(dir)
	left, _ := os.ReadDir(tmp)
	if len(left) != 1 || left[0].Name() != "keep.txt" {
		t.Fatalf("left = %v", left)
	}
	removeRestoreTemps(filepath.Join(dir, "missing")) // no tmp dir: no-op
}

func TestRemoveRestoreTempsWithGlobCharactersInDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data[1]*")
	tmp := filepath.Join(dir, "tmp")
	if err := os.MkdirAll(filepath.Join(tmp, "restore-dir.zip"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"restore-1.zip", "restore-2.zip.part", "other.zip"} {
		if err := os.WriteFile(filepath.Join(tmp, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	removeRestoreTemps(dir)
	var left []string
	ents, _ := os.ReadDir(tmp)
	for _, e := range ents {
		left = append(left, e.Name())
	}
	if len(left) != 3 || left[0] != "other.zip" || left[1] != "restore-2.zip.part" || left[2] != "restore-dir.zip" {
		t.Fatalf("left = %v", left)
	}
}

// TestResolveSourceKeyField: credentials resolve per named field (a single-key
// source uses the field "key"): a stored field beats the environment, an
// empty one falls back, an unknown one is empty, and a lookup failure is
// reported instead of silently falling back.
func TestResolveSourceKeyField(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("settings store unavailable")
	fallback := map[string]map[string]string{
		"igdb": {"client_id": "env-id", "client_secret": "env-secret"},
		"tmdb": {"key": "env-key"},
	}
	cases := []struct {
		name     string
		stored   func(context.Context, string, string) (string, error)
		sourceID string
		field    string
		want     string
		wantErr  error
	}{
		{
			name:     "stored field wins",
			stored:   func(context.Context, string, string) (string, error) { return "stored-id", nil },
			sourceID: "igdb",
			field:    "client_id",
			want:     "stored-id",
		},
		{
			name:     "empty stored field falls back to the environment",
			stored:   func(context.Context, string, string) (string, error) { return "", nil },
			sourceID: "igdb",
			field:    "client_secret",
			want:     "env-secret",
		},
		{
			name:     "a single key falls back to the environment",
			stored:   func(context.Context, string, string) (string, error) { return "", nil },
			sourceID: "tmdb",
			field:    "key",
			want:     "env-key",
		},
		{
			name:     "unknown field falls back to empty",
			stored:   func(context.Context, string, string) (string, error) { return "", nil },
			sourceID: "igdb",
			field:    "nope",
			want:     "",
		},
		{
			name:     "unknown source falls back to empty",
			stored:   func(context.Context, string, string) (string, error) { return "", nil },
			sourceID: "nope",
			field:    "key",
			want:     "",
		},
		{
			name:     "lookup error is returned instead of falling back",
			stored:   func(context.Context, string, string) (string, error) { return "", boom },
			sourceID: "igdb",
			field:    "client_id",
			wantErr:  boom,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveSourceKeyField(ctx, c.stored, fallback, c.sourceID, c.field)
			if got != c.want || !errors.Is(err, c.wantErr) {
				t.Fatalf("resolveSourceKeyField = %q, %v; want %q, %v", got, err, c.want, c.wantErr)
			}
		})
	}
}

// TestGoogleBooksKeyFallback: a stored Google Books key wins over
// GOOGLE_BOOKS_API_KEY; the environment applies when nothing is stored.
func TestGoogleBooksKeyFallback(t *testing.T) {
	t.Setenv("GOOGLE_BOOKS_API_KEY", "env-key")
	fallback := sourceKeyFallback(config.Load())
	ctx := context.Background()
	stored := func(v string) func(context.Context, string, string) (string, error) {
		return func(context.Context, string, string) (string, error) { return v, nil }
	}
	if got, err := resolveSourceKeyField(ctx, stored("stored-key"), fallback, "googlebooks", "key"); err != nil || got != "stored-key" {
		t.Fatalf("stored: %q %v", got, err)
	}
	if got, err := resolveSourceKeyField(ctx, stored(""), fallback, "googlebooks", "key"); err != nil || got != "env-key" {
		t.Fatalf("env: %q %v", got, err)
	}
}

// TestMALClientIDFallback: a stored MyAnimeList Client ID wins over
// MAL_CLIENT_ID; the environment applies when nothing is stored.
func TestMALClientIDFallback(t *testing.T) {
	t.Setenv("MAL_CLIENT_ID", "env-id")
	fallback := sourceKeyFallback(config.Load())
	ctx := context.Background()
	stored := func(v string) func(context.Context, string, string) (string, error) {
		return func(context.Context, string, string) (string, error) { return v, nil }
	}
	if got, err := resolveSourceKeyField(ctx, stored("stored-id"), fallback, "anilist", "mal_client_id"); err != nil || got != "stored-id" {
		t.Fatalf("stored: %q %v", got, err)
	}
	if got, err := resolveSourceKeyField(ctx, stored(""), fallback, "anilist", "mal_client_id"); err != nil || got != "env-id" {
		t.Fatalf("env: %q %v", got, err)
	}
}

// Every shipped kind declares its own repeat labels instead of the core's
// neutral defaults.
func TestShippedKindsDeclareRepeat(t *testing.T) {
	for _, p := range []plugin.Plugin{
		anime.New(anime.Options{}), manga.New(manga.Options{}), book.New(book.Options{}),
		tv.New(tv.Options{}), movie.New(movie.Options{}), game.New(game.Options{}),
	} {
		m := p.Manifest()
		for _, k := range m.Kinds {
			if k.Repeat == nil || k.Repeat.Action == "" || k.Repeat.Marker == "" {
				t.Errorf("%s/%s: no repeat labels", m.ID, k.Key)
			}
		}
	}
}

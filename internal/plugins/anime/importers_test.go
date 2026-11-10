package anime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
)

func testContext() context.Context { return context.Background() }

// TestAccountImporterMapsTheList: an AniList account's entries become rows —
// statuses mapped, the POINT_10 score rounded, progress and the source
// modification time kept, rows keyed by the AniList id.
func TestAccountImporterMapsTheList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req struct{ Query string }
		_ = json.Unmarshal(b, &req)
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, route(req.Query)))
	}))
	t.Cleanup(srv.Close)
	p := New(Options{AniListURL: srv.URL, HTTP: srv.Client()})
	imp, ok := p.Importer(AniListImporter).(plugin.AccountImporter)
	if !ok {
		t.Fatal("anilist-user is not an account importer")
	}
	res, err := imp.ImportAccount(testContext(), "someone")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 3 || len(res.Skipped) != 1 {
		t.Fatalf("entries %d skipped %d: %+v %+v", len(res.Entries), len(res.Skipped), res.Entries, res.Skipped)
	}
	e := res.Entries[0]
	if e.ExternalIDs["anilist"] != "189123" || e.Status != plugin.Active || e.Progress["episode"] != 3 ||
		e.Rating == nil || *e.Rating != 8 || e.Title != "Ao no Hako Season 2" ||
		e.CoverURL != "https://img.example/cover.jpg" {
		t.Fatalf("%+v", e)
	}
	if e.UpdatedAt == nil || !e.UpdatedAt.Equal(time.Unix(1762700000, 0).UTC()) {
		t.Fatalf("updatedAt = %v", e.UpdatedAt)
	}
	// REPEATING is watching; no score stays unrated
	if res.Entries[1].Status != plugin.Active || res.Entries[1].Rating != nil {
		t.Fatalf("%+v", res.Entries[1])
	}
	if res.Entries[2].Status != plugin.Completed || res.Entries[2].Rating == nil || *res.Entries[2].Rating != 9 {
		t.Fatalf("%+v", res.Entries[2])
	}
	// bad input never reaches the network
	if _, err := imp.ImportAccount(testContext(), "two words"); !errors.Is(err, plugin.ErrBadInput) {
		t.Fatalf("bad account: %v", err)
	}
}

// TestAccountImporterUnknownUser: AniList answers "User not found" as a
// GraphQL error; the importer surfaces it as not found, not as a row failure.
func TestAccountImporterUnknownUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"errors":[{"message":"User not found","status":404}]}`))
	}))
	t.Cleanup(srv.Close)
	p := New(Options{AniListURL: srv.URL, HTTP: srv.Client()})
	imp := p.Importer(AniListImporter).(plugin.AccountImporter)
	if _, err := imp.ImportAccount(testContext(), "nobody"); !errors.Is(err, plugin.ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
}

// TestMALXMLImporter: the export's rows map to entries; unusable rows are
// reported, not imported.
func TestMALXMLImporter(t *testing.T) {
	f, err := os.Open(filepath.Join("anilist", "testdata", "account_mal.xml"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	res, err := malXMLImporter{}.Parse(testContext(), f)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 2 || len(res.Skipped) != 2 {
		t.Fatalf("entries %d skipped %d: %+v %+v", len(res.Entries), len(res.Skipped), res.Entries, res.Skipped)
	}
	op := res.Entries[0]
	if op.ExternalIDs["mal"] != "21" || op.Status != plugin.Active || op.Progress["episode"] != 1050 ||
		op.Rating == nil || *op.Rating != 9 || op.Title != "One Piece" {
		t.Fatalf("%+v", op)
	}
	if res.Entries[1].Status != plugin.Completed || *res.Entries[1].Rating != 10 {
		t.Fatalf("%+v", res.Entries[1])
	}
	// a file that is not an export is bad input
	if _, err := (malXMLImporter{}).Parse(testContext(), strings.NewReader("not xml")); !errors.Is(err, plugin.ErrBadInput) {
		t.Fatalf("bad file: %v", err)
	}
}

// TestLookupByAniListID: account imports resolve their rows through the
// source's own id type.
func TestLookupByAniListID(t *testing.T) {
	s := newSource(t, nil, 0, nil)
	d, err := s.Lookup(testContext(), KindAnime, "anilist", "1535")
	if err != nil || d.ID != "154587" {
		t.Fatalf("%+v %v", d, err)
	}
}

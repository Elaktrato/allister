package manga

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "anilist", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// newSource serves the manga fixture for every query and returns the source.
func newSource(t *testing.T) plugin.Source {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req struct {
			Query     string
			Variables map[string]any
		}
		_ = json.Unmarshal(b, &req)
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, "manga.json"))
	}))
	t.Cleanup(srv.Close)
	s := New(Options{AniListURL: srv.URL, HTTP: srv.Client()}).Source("anilist")
	if s == nil {
		t.Fatal("no source")
	}
	return s
}

func TestManifest(t *testing.T) {
	m := New(Options{}).Manifest()
	if m.ID != "manga" || m.DefaultEnabled || len(m.Kinds) != 1 || m.Kinds[0].Key != KindManga {
		t.Fatalf("%+v", m)
	}
	k := m.Kinds[0]
	if len(k.Progress.Eligible) != 2 || k.Progress.PrimaryKey != "chapter" ||
		k.Progress.Eligible[0].TotalField != "chapters" || k.Progress.Eligible[1].TotalField != "volumes" {
		t.Fatalf("progress = %+v", k.Progress)
	}
	if len(m.Sources) != 1 || m.Sources[0].SupportsSchedule || !m.Sources[0].SupportsLookup ||
		m.Sources[0].URLPatterns[0] != "anilist.co/manga/{id}" {
		t.Fatalf("source = %+v", m.Sources)
	}
	if len(m.Importers) != 2 || m.Importers[0].AccountLabel == "" || m.Importers[1].ExternalIDType != "mal" {
		t.Fatalf("importers = %+v", m.Importers)
	}
}

func TestDetailsMapsChaptersVolumesAndStaff(t *testing.T) {
	s := newSource(t)
	d, err := s.Details(context.Background(), KindManga, "30013")
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != KindManga || d.ID != "30013" || d.ExternalIDs["mal"] != "13" ||
		d.Fields["chapters"].Num == nil || *d.Fields["chapters"].Num != 1100 ||
		d.Fields["volumes"].Num == nil || *d.Fields["volumes"].Num != 108 ||
		d.Fields["type"].Text != "MANGA" ||
		d.Fields["author"].Text != "Eiichiro Oda" {
		t.Fatalf("%+v", d)
	}
	if len(d.Genres) != 2 || len(d.Tags) != 2 || !d.Tags[1].Spoiler {
		t.Fatalf("genres %v tags %v", d.Genres, d.Tags)
	}
	// an adaptation relation points at the anime, the rest at manga
	if len(d.Relations) != 2 || d.Relations[0].Item.Kind != "anime" || d.Relations[1].Item.Kind != KindManga {
		t.Fatalf("relations %+v", d.Relations)
	}
	if d.Relations[0].Item.URL != "https://anilist.co/anime/21" {
		t.Fatalf("relation URL %q", d.Relations[0].Item.URL)
	}
}

func TestPeopleAreStaffCredits(t *testing.T) {
	s := newSource(t)
	c, err := s.People(context.Background(), KindManga, "30013")
	if err != nil {
		t.Fatal(err)
	}
	if len(c) != 2 || c[0].Role != "story-art" || c[0].Person.Name != "Eiichiro Oda" ||
		c[1].Role != "art" || c[1].Detail != "Art" {
		t.Fatalf("%+v", c)
	}
}

func TestRecommendationsAndLookup(t *testing.T) {
	s := newSource(t)
	recs, err := s.Recommendations(context.Background(), KindManga, "30013")
	if err != nil || len(recs) != 1 || recs[0].ID != "30015" || recs[0].Kind != KindManga {
		t.Fatalf("%+v %v", recs, err)
	}
	d, err := s.Lookup(context.Background(), KindManga, "anilist", "30013")
	if err != nil || d.ID != "30013" {
		t.Fatalf("%+v %v", d, err)
	}
	// a bare MAL lookup needs the fake to answer MediaByMALID; the fixture's
	// media has the id, so the same data comes back
	if d, err := s.Lookup(context.Background(), KindManga, "mal", "13"); err != nil || d.ExternalIDs["mal"] != "13" {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestMALXMLMangaImport(t *testing.T) {
	xmlDoc := `<?xml version="1.0"?><myanimelist>
  <manga>
    <series_animedb_id>13</series_animedb_id>
    <series_title><![CDATA[One Piece]]></series_title>
    <my_read_chapters>1050</my_read_chapters>
    <my_read_volumes>100</my_read_volumes>
    <my_score>9</my_score>
    <my_status>1</my_status>
  </manga>
  <manga>
    <series_animedb_id>2</series_animedb_id>
    <series_title>Berserk</series_title>
    <my_read_chapters>380</my_read_chapters>
    <my_read_volumes>0</my_read_volumes>
    <my_score>0</my_score>
    <my_status>3</my_status>
  </manga>
</myanimelist>`
	res, err := malXMLImporter{}.Parse(context.Background(), strings.NewReader(xmlDoc))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 2 {
		t.Fatalf("%+v", res.Entries)
	}
	e := res.Entries[0]
	if e.ExternalIDs["mal"] != "13" || e.Status != plugin.Active || e.Progress["chapter"] != 1050 ||
		e.Progress["volume"] != 100 || e.Rating == nil || *e.Rating != 9 {
		t.Fatalf("%+v", e)
	}
	if res.Entries[1].Status != plugin.Paused || res.Entries[1].Rating != nil || len(res.Entries[1].Progress) != 1 {
		t.Fatalf("%+v", res.Entries[1])
	}
}

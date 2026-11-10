package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
	"github.com/allister/allister/internal/plugins/anime/animemap"
)

// TestExportJSONViaRoute streams the real anime plugin's JSON exporter.
func TestExportJSONViaRoute(t *testing.T) {
	e := newEnv(t)
	d := animemap.ItemData(e.fp.anime)
	id, err := e.lib.AddToList(context.Background(), "anime", "anime", d, library.Entry{Status: library.Active, Rating: ip(9),
		Progress: map[string]float64{"episode": 5}, Comment: "c <b>"})
	if err != nil {
		t.Fatal(err)
	}
	_ = id
	rec := e.get("/anime/export/json")
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	h := rec.Header()
	if h.Get("Content-Type") != "application/json" || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("Content-Disposition") != `attachment; filename="anime-json-2026-01-01.json"` {
		t.Fatalf("headers: %v", h)
	}
	var doc struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
		Entries []struct {
			Kind        string             `json:"kind"`
			Titles      []plugin.Title     `json:"titles"`
			ExternalIDs map[string]string  `json:"external_ids"`
			Status      string             `json:"status"`
			Rating      *int               `json:"rating"`
			Progress    map[string]float64 `json:"progress"`
			Comment     string             `json:"comment"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, rec.Body.String())
	}
	if doc.Format != "allister.anime.export" || doc.Version != 1 || len(doc.Entries) != 1 {
		t.Fatalf("doc: %+v", doc)
	}
	en := doc.Entries[0]
	if en.Kind != "anime" || en.Status != "active" || en.Rating == nil || *en.Rating != 9 || en.Progress["episode"] != 5 ||
		en.Comment != "c <b>" || len(en.Titles) != len(d.Titles) || en.Titles[0] != d.Titles[0] {
		t.Fatalf("entry: %+v", en)
	}
}

// exportFixture is the book fixture with a line exporter ("lines") and an
// exporter whose manifest has hostile extension and MIME values ("odd").
func exportFixture(lines, odd *plugintest.FakeExporter) func(book, tv *plugintest.Plugin) {
	return func(book, _ *plugintest.Plugin) {
		book.M.Exporters = []plugin.ExporterInfo{
			{ID: "lines", Name: "Lines", Extension: ".TXT", MIME: "text/plain; charset=utf-8"},
			{ID: "odd", Name: "Odd", Extension: "\".x\r\nSet-Cookie: a=b", MIME: "text/html\r\nX-Evil: 1"},
			{ID: "ghost", Name: "Ghost", Extension: ".g", MIME: "text/plain"}, // declared, not implemented
		}
		book.Exporters = map[string]plugin.Exporter{"lines": lines, "odd": odd}
	}
}

func TestExportRouteStreamsFixtureExporterWithHeaders(t *testing.T) {
	lines := &plugintest.FakeExporter{Header: "# books\n"}
	e := newFixtureEnv(t, exportFixture(lines, &plugintest.FakeExporter{}))
	b1 := bookItem("1", "Earthsea")
	b1.Fields["pages"] = plugin.FieldValue{Num: num(300)}
	e.addFixture(t, "book", "book", b1, library.Entry{Status: library.Active, Progress: map[string]float64{"pages": 12}})
	e.addFixture(t, "book", "book", bookItem("2", "Tehanu"), library.Entry{Status: library.Planned})
	e.addFixture(t, "tv", "show", tvShow("9", "Other Plugin"), library.Entry{Status: library.Active})
	rec := e.get("/book/export/lines")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	h := rec.Header()
	if h.Get("Content-Disposition") != `attachment; filename="book-lines-2026-01-01.txt"` ||
		h.Get("Content-Type") != "text/plain; charset=utf-8" || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("Cache-Control") != "no-store" {
		t.Fatalf("headers: %v", h)
	}
	if got, want := rec.Body.String(), "# books\nbook|Earthsea|active|pages=12\nbook|Tehanu|planned|\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	// ?kind= filters by a declared kind
	if rec := e.get("/book/export/lines?kind=book"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Tehanu") {
		t.Fatalf("kind filter: %d %s", rec.Code, rec.Body.String())
	}
}

func TestExportRouteGuard(t *testing.T) {
	e := newFixtureEnv(t, exportFixture(&plugintest.FakeExporter{}, &plugintest.FakeExporter{}))
	for _, target := range []string{"/book/export/nope", "/book/export/ghost", "/book/export/json", "/tv/export/lines",
		"/book/export/lines?kind=show", "/book/export/LINES"} {
		if rec := e.get(target); rec.Code != http.StatusNotFound || rec.Header().Get("Content-Disposition") != "" {
			t.Errorf("%s: %d %v", target, rec.Code, rec.Header())
		}
	}
	if err := e.reg.SetEnabled(context.Background(), "book", false); err != nil {
		t.Fatal(err)
	}
	if rec := e.get("/book/export/lines"); rec.Code != http.StatusNotFound || rec.Header().Get("Content-Disposition") != "" {
		t.Fatalf("disabled plugin: %d", rec.Code)
	}
	if !contains(e.srv.pluginRoutes["book"], "GET /book/export/{exporter}") {
		t.Fatalf("export route not mounted behind the plugin guard: %v", e.srv.pluginRoutes["book"])
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func TestExportFilenameAndTypeComeOnlyFromValidatedValues(t *testing.T) {
	e := newFixtureEnv(t, exportFixture(&plugintest.FakeExporter{}, &plugintest.FakeExporter{Header: "x"}))
	rec := e.get("/book/export/odd")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	h := rec.Header()
	if h.Get("Content-Disposition") != `attachment; filename="book-odd-2026-01-01"` {
		t.Errorf("disposition = %q", h.Get("Content-Disposition"))
	}
	if h.Get("Content-Type") != "application/octet-stream" || h.Get("Set-Cookie") != "" || h.Get("X-Evil") != "" {
		t.Errorf("headers: %v", h)
	}
}

func TestExportWriteErrorStopsTheStream(t *testing.T) {
	// failing before anything was written: a plain 500, no download
	e := newFixtureEnv(t, exportFixture(&plugintest.FakeExporter{Err: errors.New("boom")}, &plugintest.FakeExporter{Header: "partial", Err: errors.New("late")}))
	rec := e.get("/book/export/lines")
	if rec.Code != http.StatusInternalServerError || rec.Header().Get("Content-Disposition") != "" || strings.Contains(rec.Body.String(), "boom") {
		t.Fatalf("early failure: %d %v %q", rec.Code, rec.Header(), rec.Body.String())
	}
	// failing mid-stream: the response just ends
	rec = e.get("/book/export/odd")
	if rec.Code != 200 || rec.Body.String() != "partial" {
		t.Fatalf("late failure: %d %q", rec.Code, rec.Body.String())
	}
}

func TestExportLinksOnListAndSettings(t *testing.T) {
	e := newFixtureEnv(t, exportFixture(&plugintest.FakeExporter{}, &plugintest.FakeExporter{}))
	list := e.get("/book").Body.String()
	mustContain(t, list, `href="/book/export/lines"`, "Export Lines", `href="/book/export/odd"`)
	mustNotContain(t, list, `href="/book/export/ghost"`)
	mustNotContain(t, e.get("/tv").Body.String(), "/export/")
	settings := e.get("/settings").Body.String()
	mustContain(t, settings, `href="/book/export/lines"`)
	if err := e.reg.SetEnabled(context.Background(), "book", false); err != nil {
		t.Fatal(err)
	}
	mustNotContain(t, e.get("/settings").Body.String(), `href="/book/export/lines"`)
	// the anime plugin's exporter is linked too
	mustContain(t, e.get("/anime").Body.String(), `href="/anime/export/json"`, "Export JSON")
}

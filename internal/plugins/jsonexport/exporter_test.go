package jsonexport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
)

func exporter(t *testing.T) plugin.Exporter {
	t.Helper()
	return New("allister.test.export")
}

func intp(i int) *int { return &i }

func sampleEntries() []plugin.ExportEntry {
	// a zoned time must be exported as its own calendar day
	started := time.Date(2024, 1, 5, 23, 30, 0, 0, time.FixedZone("x", -5*3600))
	finished := time.Date(2024, 2, 10, 0, 0, 0, 0, time.UTC)
	return []plugin.ExportEntry{
		{
			Kind:        "anime",
			Titles:      []plugin.Title{{Lang: "ja-latn", Text: "Sousou no Frieren"}, {Lang: "en", Text: "Frieren <&>"}},
			ExternalIDs: map[string]string{"mal": "52991", "anilist": "154587"},
			Status:      plugin.Completed, Rating: intp(9),
			Progress:  map[string]float64{"episode": 28},
			StartedOn: &started, FinishedOn: &finished, Notes: "great\nend",
			Runs: []plugin.ExportRun{
				{Number: 1, Status: plugin.Dropped, Rating: intp(4), Review: "too slow", Progress: map[string]float64{"episode": 3}, StartedOn: &started},
				{Number: 2, Status: plugin.Completed, Rating: intp(9), Progress: map[string]float64{"episode": 28}, StartedOn: &started, FinishedOn: &finished},
			},
			UserTags: []string{"comfort"}, Lists: []string{"Favourites", "Rewatch"},
		},
		{Kind: "anime", Titles: []plugin.Title{{Lang: "en", Text: "Unrated"}}, Status: plugin.Planned},
	}
}

func export(t *testing.T, entries []plugin.ExportEntry) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := exporter(t).Export(context.Background(), entries, &b); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestExporterRoundTripsJSON(t *testing.T) {
	out := export(t, sampleEntries())
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
			StartedOn   *string            `json:"started_on"`
			FinishedOn  *string            `json:"finished_on"`
			Notes       string             `json:"notes"`
			Runs        []struct {
				Number     int                `json:"number"`
				Status     string             `json:"status"`
				Rating     *int               `json:"rating"`
				Review     string             `json:"review"`
				Progress   map[string]float64 `json:"progress"`
				StartedOn  *string            `json:"started_on"`
				FinishedOn *string            `json:"finished_on"`
			} `json:"runs"`
			UserTags []string `json:"user_tags"`
			Lists    []string `json:"lists"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if doc.Format != "allister.test.export" || doc.Version != 2 || len(doc.Entries) != 2 {
		t.Fatalf("doc = %+v", doc)
	}
	e := doc.Entries[0]
	if e.Kind != "anime" || len(e.Titles) != 2 || e.Titles[1].Text != "Frieren <&>" || e.ExternalIDs["mal"] != "52991" ||
		e.Status != "completed" || e.Rating == nil || *e.Rating != 9 || e.Progress["episode"] != 28 || e.Notes != "great\nend" {
		t.Errorf("entry = %+v", e)
	}
	if e.StartedOn == nil || *e.StartedOn != "2024-01-05" || e.FinishedOn == nil || *e.FinishedOn != "2024-02-10" {
		t.Errorf("dates = %v %v", e.StartedOn, e.FinishedOn)
	}
	if len(e.Runs) != 2 || e.Runs[0].Number != 1 || e.Runs[0].Status != "dropped" || *e.Runs[0].Rating != 4 || e.Runs[0].Review != "too slow" ||
		e.Runs[0].Progress["episode"] != 3 || *e.Runs[0].StartedOn != "2024-01-05" || e.Runs[0].FinishedOn != nil ||
		e.Runs[1].Number != 2 || *e.Runs[1].FinishedOn != "2024-02-10" {
		t.Errorf("runs = %+v", e.Runs)
	}
	if len(e.UserTags) != 1 || e.UserTags[0] != "comfort" || len(e.Lists) != 2 || e.Lists[1] != "Rewatch" {
		t.Errorf("tags %v lists %v", e.UserTags, e.Lists)
	}
	u := doc.Entries[1]
	if u.Rating != nil || u.StartedOn != nil || u.FinishedOn != nil || u.Status != "planned" {
		t.Errorf("unrated = %+v", u)
	}
	if u.Runs == nil || u.UserTags == nil || u.Lists == nil || len(u.Runs)+len(u.UserTags)+len(u.Lists) != 0 {
		t.Errorf("an entry without runs, tags or lists must carry empty lists: %+v", u)
	}
	if strings.Contains(string(out), `"comment"`) {
		t.Errorf("the export has no comment any more:\n%s", out)
	}
	if !strings.Contains(string(out), `"rating":null`) {
		t.Errorf("an unrated entry must carry rating null:\n%s", out)
	}
	if strings.Contains(string(out), `\`+"u003c") {
		t.Errorf("html must not be escaped:\n%s", out)
	}
}

func TestExporterIsDeterministic(t *testing.T) {
	first := export(t, sampleEntries())
	for i := 0; i < 20; i++ {
		if got := export(t, sampleEntries()); !bytes.Equal(got, first) {
			t.Fatalf("run %d differs:\n%s\n%s", i, first, got)
		}
	}
	s := string(first)
	order := []string{`"format"`, `"version"`, `"entries"`, `"kind"`, `"titles"`, `"external_ids"`, `"status"`, `"rating"`, `"progress"`, `"started_on"`, `"finished_on"`, `"notes"`, `"runs"`, `"user_tags"`, `"lists"`}
	pos := -1
	for _, k := range order {
		i := strings.Index(s, k)
		if i <= pos {
			t.Errorf("key %s out of order in\n%s", k, s)
		}
		pos = i
	}
	if strings.Index(s, `"anilist"`) > strings.Index(s, `"mal"`) {
		t.Errorf("map keys must be sorted:\n%s", s)
	}
}

func TestExporterEmpty(t *testing.T) {
	out := export(t, nil)
	var doc struct {
		Entries []any `json:"entries"`
	}
	if err := json.Unmarshal(out, &doc); err != nil || doc.Entries == nil || len(doc.Entries) != 0 {
		t.Fatalf("out = %s err=%v", out, err)
	}
}

type failWriter struct{ n int }

func (f *failWriter) Write(p []byte) (int, error) {
	if f.n--; f.n < 0 {
		return 0, errors.New("disk full")
	}
	return len(p), nil
}

func TestExporterReturnsWriteErrors(t *testing.T) {
	for n := 0; n < 4; n++ {
		err := exporter(t).Export(context.Background(), sampleEntries(), &failWriter{n: n})
		if err == nil || !strings.Contains(err.Error(), "disk full") {
			t.Errorf("n=%d err = %v", n, err)
		}
	}
}

func TestExporterStreamsEntryByEntry(t *testing.T) {
	var writes int
	w := writerFunc(func(p []byte) (int, error) { writes++; return len(p), nil })
	if err := exporter(t).Export(context.Background(), sampleEntries(), w); err != nil {
		t.Fatal(err)
	}
	if writes < 3 {
		t.Errorf("writes = %d, want the header and each entry written separately", writes)
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func TestExporterCancelledContext(t *testing.T) {
	c, cancel := context.WithCancel(context.Background())
	cancel()
	err := exporter(t).Export(c, sampleEntries(), &bytes.Buffer{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestExporterWritesSeriesOnlyWhenSet(t *testing.T) {
	out := export(t, []plugin.ExportEntry{
		{Kind: "k", Status: plugin.Planned, Series: "Saga", SeriesPosition: 2},
		{Kind: "k", Status: plugin.Planned},
	})
	var doc struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Entries[0]["series"] != "Saga" || doc.Entries[0]["series_position"] != float64(2) {
		t.Fatalf("series entry = %v", doc.Entries[0])
	}
	if _, ok := doc.Entries[1]["series"]; ok {
		t.Fatalf("standalone has series: %v", doc.Entries[1])
	}
	if _, ok := doc.Entries[1]["series_position"]; ok {
		t.Fatalf("standalone has position: %v", doc.Entries[1])
	}
}

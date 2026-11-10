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
			StartedOn: &started, FinishedOn: &finished, Comment: "great\nend",
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
			Comment     string             `json:"comment"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if doc.Format != "allister.test.export" || doc.Version != 1 || len(doc.Entries) != 2 {
		t.Fatalf("doc = %+v", doc)
	}
	e := doc.Entries[0]
	if e.Kind != "anime" || len(e.Titles) != 2 || e.Titles[1].Text != "Frieren <&>" || e.ExternalIDs["mal"] != "52991" ||
		e.Status != "completed" || e.Rating == nil || *e.Rating != 9 || e.Progress["episode"] != 28 || e.Comment != "great\nend" {
		t.Errorf("entry = %+v", e)
	}
	if e.StartedOn == nil || *e.StartedOn != "2024-01-05" || e.FinishedOn == nil || *e.FinishedOn != "2024-02-10" {
		t.Errorf("dates = %v %v", e.StartedOn, e.FinishedOn)
	}
	u := doc.Entries[1]
	if u.Rating != nil || u.StartedOn != nil || u.FinishedOn != nil || u.Status != "planned" {
		t.Errorf("unrated = %+v", u)
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
	order := []string{`"format"`, `"version"`, `"entries"`, `"kind"`, `"titles"`, `"external_ids"`, `"status"`, `"rating"`, `"progress"`, `"started_on"`, `"finished_on"`, `"comment"`}
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

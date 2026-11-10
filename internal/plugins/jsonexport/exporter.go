package jsonexport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/allister/allister/internal/plugin"
)

// exportVersion is the version of the export format.
const exportVersion = 1

// Exporter writes {"format":<name>,"version":1,"entries":[...]}. Field order
// is fixed by the structs and map keys are sorted by encoding/json, so the
// output is deterministic. It streams: the header, each entry and the footer
// are separate writes.
type Exporter struct{ format string }

// New returns an exporter whose format field is name (for example
// "allister.anime.export").
func New(name string) plugin.Exporter { return Exporter{format: name} }

type exportEntry struct {
	Kind        string             `json:"kind"`
	Titles      []plugin.Title     `json:"titles"`
	ExternalIDs map[string]string  `json:"external_ids"`
	Status      plugin.StatusClass `json:"status"`
	Rating      *int               `json:"rating"`
	Progress    map[string]float64 `json:"progress"`
	StartedOn   *string            `json:"started_on"`
	FinishedOn  *string            `json:"finished_on"`
	Comment     string             `json:"comment"`
}

func exportDay(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := plugin.DateOnly(*t).Format("2006-01-02")
	return &s
}

func encode(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

func (e Exporter) Export(ctx context.Context, entries []plugin.ExportEntry, w io.Writer) error {
	write := func(p string) error {
		if _, err := io.WriteString(w, p); err != nil {
			return fmt.Errorf("export: write: %w", err)
		}
		return nil
	}
	if err := write(fmt.Sprintf("{\"format\":%q,\"version\":%d,\"entries\":[", e.format, exportVersion)); err != nil {
		return err
	}
	for i, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		ee := exportEntry{
			Kind: e.Kind, Titles: e.Titles, ExternalIDs: e.ExternalIDs, Status: e.Status, Rating: e.Rating,
			Progress: e.Progress, StartedOn: exportDay(e.StartedOn), FinishedOn: exportDay(e.FinishedOn), Comment: e.Comment,
		}
		if ee.Titles == nil {
			ee.Titles = []plugin.Title{}
		}
		if ee.ExternalIDs == nil {
			ee.ExternalIDs = map[string]string{}
		}
		if ee.Progress == nil {
			ee.Progress = map[string]float64{}
		}
		b, err := encode(ee)
		if err != nil {
			return fmt.Errorf("export: encode entry %d: %w", i, err)
		}
		sep := "\n"
		if i > 0 {
			sep = ",\n"
		}
		if err := write(sep + string(b)); err != nil {
			return err
		}
	}
	return write("\n]}\n")
}

package library

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/db"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

func fp(f float64) *float64 { return &f }
func ip(i int) *int         { return &i }

// richBook is the book fixture with one field of every type added.
func richBook(opts ...plugintest.Option) *plugintest.Plugin {
	p := plugintest.BookPlugin(opts...)
	k := &p.M.Kinds[0]
	k.Fields[0].Required = true // author
	k.Fields = append(k.Fields,
		plugin.FieldDef{Key: "released", Label: "Released", Type: plugin.FieldDate, Editable: true, Filterable: true, Sortable: true},
		plugin.FieldDef{Key: "translators", Label: "Translators", Type: plugin.FieldList, Editable: true, Filterable: true},
		plugin.FieldDef{Key: "homepage", Label: "Homepage", Type: plugin.FieldURL, Editable: true},
		plugin.FieldDef{Key: "format", Label: "Format", Type: plugin.FieldEnum, EnumValues: []string{"hardcover", "paperback"}, Editable: true, Filterable: true},
		plugin.FieldDef{Key: "score", Label: "Score", Type: plugin.FieldNumber, Min: fp(0), Max: fp(10), Editable: true, Filterable: true, Sortable: true},
	)
	k.Fields[1].Sortable = true // pages
	return p
}

type env struct {
	lib *Library
	db  *sql.DB
	reg *plugin.Registry
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	reg := plugin.NewRegistry(plugintest.NewMemEnabled())
	nop := richBook(plugintest.WithID("nop")) // a kind without a primary progress key
	nop.M.Kinds[0].Progress.PrimaryKey = ""
	for _, p := range []plugin.Plugin{richBook(), richBook(plugintest.WithID("book2")), plugintest.TVPlugin(), nop} {
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	return &env{lib: New(d, reg), db: d, reg: reg}
}

func newLib(t *testing.T) *Library { return newEnv(t).lib }

func bookData(id string) plugin.ItemData {
	return plugin.ItemData{
		ItemSummary: plugin.ItemSummary{
			Source: "books", ID: id, Kind: "book",
			Titles:   []plugin.Title{{Lang: "en", Text: "Dune"}, {Lang: "ja", Text: "デューン"}, {Lang: "de", Text: "Der Wüstenplanet"}},
			Fields:   map[string]plugin.FieldValue{"author": {Text: "Frank Herbert"}},
			CoverURL: "https://img.example/" + id + ".jpg",
		},
		ExternalURL: "https://books.example/work/" + id,
	}
}

func mustSave(t *testing.T, l *Library, pluginID, kind string, d plugin.ItemData, opts ...SaveOption) int64 {
	t.Helper()
	id, err := l.SaveItem(context.Background(), pluginID, kind, d, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustItem(t *testing.T, l *Library, id int64) *Item {
	t.Helper()
	it, err := l.Item(context.Background(), id)
	if err != nil || it == nil {
		t.Fatalf("Item(%d) = %v, %v", id, it, err)
	}
	return it
}

func count(t *testing.T, d *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := d.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func wantInvalid(t *testing.T, err error, substr string) {
	t.Helper()
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	if substr != "" && !strings.Contains(err.Error(), substr) {
		t.Fatalf("err = %q, want it to mention %q", err, substr)
	}
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

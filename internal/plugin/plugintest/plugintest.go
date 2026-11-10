// Package plugintest provides fully valid example plugins and fakes for tests.
// The book and tv manifests prove the contract can express other media types;
// they are test-only and never shipped as plugins.
package plugintest

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/allister/allister/internal/plugin"
)

// Plugin is a data-driven plugin.Plugin. Tests may modify M (it is returned by
// value from Manifest, but slices are shared) before registering.
type Plugin struct {
	M         plugin.Manifest
	Sources   map[string]plugin.Source
	Importers map[string]plugin.Importer
	Exporters map[string]plugin.Exporter
}

func (p *Plugin) Manifest() plugin.Manifest { return p.M }
func (p *Plugin) Source(id string) plugin.Source {
	if s, ok := p.Sources[id]; ok {
		return s
	}
	return nil
}
func (p *Plugin) Importer(id string) plugin.Importer {
	if i, ok := p.Importers[id]; ok {
		return i
	}
	return nil
}
func (p *Plugin) Exporter(id string) plugin.Exporter {
	if e, ok := p.Exporters[id]; ok {
		return e
	}
	return nil
}

// Option customises a fixture.
type Option func(*Plugin)

// WithID sets the plugin id (to register two copies or collide ids).
func WithID(id string) Option { return func(p *Plugin) { p.M.ID = id } }

// WithDefaultEnabled sets the manifest's DefaultEnabled flag.
func WithDefaultEnabled(on bool) Option { return func(p *Plugin) { p.M.DefaultEnabled = on } }

func num(f float64) *float64 { return &f }

func apply(p *Plugin, opts []Option) *Plugin {
	for _, o := range opts {
		o(p)
	}
	return p
}

// BookPlugin is a book-like plugin (id "book", kind "book"): page or percent
// progress per item, authors as people, genres but no tags.
func BookPlugin(opts ...Option) *Plugin {
	p := &Plugin{
		M: plugin.Manifest{
			ID: "book", Name: "Books", Version: "1.0.0", ContractVersion: plugin.ContractVersion, DefaultEnabled: true,
			Kinds: []plugin.KindDef{{
				Key: "book", Label: "Book", LabelPlural: "Books",
				Fields: []plugin.FieldDef{
					{Key: "author", Label: "Author", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true},
					{Key: "pages", Label: "Pages", Type: plugin.FieldNumber, Min: num(1), Editable: true},
					{Key: "isbn", Label: "ISBN", Type: plugin.FieldText, Editable: true},
				},
				Genres:   plugin.TermDef{Enabled: true, Label: "Genres", Editable: true, Filterable: true},
				Statuses: plugin.StatusLabels{Planned: "Want to read", Active: "Reading", Paused: "Paused", Completed: "Read", Dropped: "Dropped"},
				Progress: plugin.ProgressSpec{
					Eligible: []plugin.ProgressDef{
						{Key: "pages", Type: plugin.Pages, Label: "Page", TotalField: "pages"},
						{Key: "percent", Type: plugin.Percent, Label: "Percent"},
					},
					Defaults:     []string{"pages"},
					Combinations: [][]string{{"pages"}, {"percent"}},
					PrimaryKey:   "pages",
				},
				Sections: []plugin.SectionDef{
					{Type: plugin.SectionInfo, Label: "Details"},
					{Type: plugin.SectionText, Label: "Summary"},
					{Type: plugin.SectionPeople, Label: "Authors", Roles: []string{"author"}},
					{Type: plugin.SectionGenres, Label: "Genres"},
					{Type: plugin.SectionLinks, Label: "Links"},
				},
				TitleLanguages: []string{"en"},
			}},
			Sources:   []plugin.SourceInfo{{ID: "books", Name: "Fake Books", RequestsPerMinute: 60, ExternalIDTypes: []string{"isbn"}, SupportsLookup: true, LinkLabel: "View on Fake Books", URLPatterns: []string{"books.example/work/{id}"}}},
			Importers: []plugin.ImporterInfo{{ID: "books-csv", Name: "Books CSV", FileHint: "CSV export", Accept: []string{".csv"}, ExternalIDType: "isbn", ResolveVia: "books", FallbackTitleLang: "en"}},
		},
		Sources:   map[string]plugin.Source{"books": &FakeSource{}},
		Importers: map[string]plugin.Importer{"books-csv": &FakeImporter{}},
	}
	return apply(p, opts)
}

// TVPlugin is a TV-like plugin (id "tv", kind "show"): season and episode
// counts plus a duration position, actors as credits with a counterpart,
// genres and tags.
func TVPlugin(opts ...Option) *Plugin {
	p := &Plugin{
		M: plugin.Manifest{
			ID: "tv", Name: "TV", Version: "1.0.0", ContractVersion: plugin.ContractVersion, DefaultEnabled: true,
			Kinds: []plugin.KindDef{{
				Key: "show", Label: "Show", LabelPlural: "Shows",
				Fields: []plugin.FieldDef{
					{Key: "type", Label: "Type", Type: plugin.FieldEnum, EnumValues: []string{"series", "miniseries"}, Show: true, Editable: true},
					{Key: "episodes", Label: "Episodes", Type: plugin.FieldNumber, Min: num(0), Editable: true},
					{Key: "year", Label: "Year", Type: plugin.FieldNumber, Show: true, Editable: true, Filterable: true, Sortable: true},
				},
				Genres:   plugin.TermDef{Enabled: true, Label: "Genres", Editable: true, Filterable: true},
				Tags:     plugin.TermDef{Enabled: true, Label: "Tags", Editable: true, Filterable: true},
				Statuses: plugin.StatusLabels{Planned: "Planned", Active: "Watching", Paused: "Paused", Completed: "Watched", Dropped: "Dropped"},
				Progress: plugin.ProgressSpec{
					Eligible: []plugin.ProgressDef{
						{Key: "season", Type: plugin.Count, Label: "Season"},
						{Key: "episode", Type: plugin.Count, Label: "Episode", TotalField: "episodes"},
						{Key: "position", Type: plugin.Duration, Label: "Position"},
					},
					Defaults:     []string{"episode"},
					Combinations: [][]string{{"episode"}, {"season", "episode"}, {"episode", "position"}},
					PrimaryKey:   "episode",
				},
				Sections: []plugin.SectionDef{
					{Type: plugin.SectionInfo, Label: "Details"},
					{Type: plugin.SectionText, Label: "Synopsis"},
					{Type: plugin.SectionTitles, Label: "Titles"},
					{Type: plugin.SectionPeople, Label: "Cast", Roles: []string{"actor"}},
					{Type: plugin.SectionGenres, Label: "Genres"},
					{Type: plugin.SectionTags, Label: "Tags"},
					{Type: plugin.SectionRecommendations, Label: "Recommendations"},
					{Type: plugin.SectionLinks, Label: "Links"},
				},
				TitleLanguages: []string{"en", "original"},
			}},
			Sources:   []plugin.SourceInfo{{ID: "tvdata", Name: "Fake TV", RequestsPerMinute: 30, ExternalIDTypes: []string{"tvdata"}, SupportsLookup: true, LinkLabel: "View on Fake TV", URLPatterns: []string{"tv.example/show/{id}"}}},
			Importers: []plugin.ImporterInfo{{ID: "tv-json", Name: "TV JSON", FileHint: "JSON export", Accept: []string{".json"}, ExternalIDType: "tvdata", ResolveVia: "tvdata", FallbackTitleLang: "en"}},
		},
		Sources:   map[string]plugin.Source{"tvdata": &FakeSource{}},
		Importers: map[string]plugin.Importer{"tv-json": &FakeImporter{}},
	}
	return apply(p, opts)
}

// Operation names for FakeSource.Errs and FakeSource.Calls.
const (
	OpSearch          = "search"
	OpDetails         = "details"
	OpPeople          = "people"
	OpRecommendations = "recommendations"
	OpLookup          = "lookup"
)

// FakeSource is a canned plugin.Source. Configure the exported fields before
// use; they must not be changed while calls are in flight.
type FakeSource struct {
	Summaries []plugin.ItemSummary // Search and Recommendations result
	Data      plugin.ItemData      // Details and Lookup result
	Credits   []plugin.Credit      // People result
	Recs      []plugin.ItemSummary // Recommendations result (falls back to Summaries when nil)
	Err       error                // returned by every operation when set
	Errs      map[string]error     // per operation error (keys Op*), checked after Err
	Delay     time.Duration        // per call delay; honours ctx cancellation (timeout tests)

	mu    sync.Mutex
	calls map[string]int
}

// Calls returns how many times the operation (Op*) was called.
func (f *FakeSource) Calls(op string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[op]
}

func (f *FakeSource) enter(ctx context.Context, op string) error {
	f.mu.Lock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[op]++
	f.mu.Unlock()
	if f.Delay > 0 {
		t := time.NewTimer(f.Delay)
		defer t.Stop()
		select {
		case <-t.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f.Err != nil {
		return f.Err
	}
	return f.Errs[op]
}

func (f *FakeSource) Search(ctx context.Context, kind, q string) ([]plugin.ItemSummary, error) {
	if err := f.enter(ctx, OpSearch); err != nil {
		return nil, err
	}
	return f.Summaries, nil
}

func (f *FakeSource) Details(ctx context.Context, kind, id string) (plugin.ItemData, error) {
	if err := f.enter(ctx, OpDetails); err != nil {
		return plugin.ItemData{}, err
	}
	return f.Data, nil
}

func (f *FakeSource) People(ctx context.Context, kind, id string) ([]plugin.Credit, error) {
	if err := f.enter(ctx, OpPeople); err != nil {
		return nil, err
	}
	return f.Credits, nil
}

func (f *FakeSource) Recommendations(ctx context.Context, kind, id string) ([]plugin.ItemSummary, error) {
	if err := f.enter(ctx, OpRecommendations); err != nil {
		return nil, err
	}
	if f.Recs != nil {
		return f.Recs, nil
	}
	return f.Summaries, nil
}

func (f *FakeSource) Lookup(ctx context.Context, kind, idType, value string) (plugin.ItemData, error) {
	if err := f.enter(ctx, OpLookup); err != nil {
		return plugin.ItemData{}, err
	}
	return f.Data, nil
}

// FakeImporter returns a canned ParseResult (or Err) and ignores the input.
type FakeImporter struct {
	Result plugin.ParseResult
	Err    error
}

func (f *FakeImporter) Parse(ctx context.Context, r io.Reader) (plugin.ParseResult, error) {
	if f.Err != nil {
		return plugin.ParseResult{}, f.Err
	}
	return f.Result, nil
}

// MemEnabled is an in-memory plugin.EnabledStore, safe for concurrent use. The
// zero value is ready to use.
type MemEnabled struct {
	mu  sync.Mutex
	m   map[string]bool
	err error
}

// NewMemEnabled returns an empty store (every plugin falls back to its default).
func NewMemEnabled() *MemEnabled { return &MemEnabled{m: map[string]bool{}} }

// SetErr makes every later call fail with err (nil restores normal behaviour).
func (s *MemEnabled) SetErr(err error) {
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
}

func (s *MemEnabled) IsEnabled(ctx context.Context, id string, def bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return false, s.err
	}
	if v, ok := s.m[id]; ok {
		return v, nil
	}
	return def, nil
}

func (s *MemEnabled) SetEnabled(ctx context.Context, id string, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if s.m == nil {
		s.m = map[string]bool{}
	}
	s.m[id] = on
	return nil
}

// LookupSource is a FakeSource whose Lookup is answered by Fn (when set) and
// records every looked-up value in order. The other operations behave like
// FakeSource's.
type LookupSource struct {
	FakeSource
	// Fn answers Lookup; n counts the calls for this value so far (1 for the
	// first). Set it before use.
	Fn func(ctx context.Context, kind, idType, value string, n int) (plugin.ItemData, error)

	lmu    sync.Mutex
	values []string
}

// Lookup records the value and asks Fn (FakeSource's Lookup when Fn is nil).
func (s *LookupSource) Lookup(ctx context.Context, kind, idType, value string) (plugin.ItemData, error) {
	s.lmu.Lock()
	s.values = append(s.values, value)
	n := 0
	for _, v := range s.values {
		if v == value {
			n++
		}
	}
	fn := s.Fn
	s.lmu.Unlock()
	if fn == nil {
		return s.FakeSource.Lookup(ctx, kind, idType, value)
	}
	return fn(ctx, kind, idType, value, n)
}

// Looked returns the values passed to Lookup so far, in order.
func (s *LookupSource) Looked() []string {
	s.lmu.Lock()
	defer s.lmu.Unlock()
	return append([]string(nil), s.values...)
}

// ResetLooked forgets the recorded values (and the per-value call counts).
func (s *LookupSource) ResetLooked() {
	s.lmu.Lock()
	s.values = nil
	s.lmu.Unlock()
}

// FakeExporter writes one line per entry: "kind|first title|status|progress"
// with the progress as sorted key=value pairs joined by ",". It fails with
// Err (after writing Header) when set.
type FakeExporter struct {
	Header string
	Err    error
}

func (f *FakeExporter) Export(ctx context.Context, entries []plugin.ExportEntry, w io.Writer) error {
	if _, err := io.WriteString(w, f.Header); err != nil {
		return err
	}
	if f.Err != nil {
		return f.Err
	}
	for _, e := range entries {
		title := ""
		if len(e.Titles) > 0 {
			title = e.Titles[0].Text
		}
		keys := make([]string, 0, len(e.Progress))
		for k := range e.Progress {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var prog []string
		for _, k := range keys {
			prog = append(prog, k+"="+strconv.FormatFloat(e.Progress[k], 'f', -1, 64))
		}
		if _, err := fmt.Fprintf(w, "%s|%s|%s|%s\n", e.Kind, title, e.Status, strings.Join(prog, ",")); err != nil {
			return err
		}
	}
	return nil
}

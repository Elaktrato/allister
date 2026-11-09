package plugin

import (
	"context"
	"errors"
	"io"
)

var (
	// ErrNotFound: the source does not know the requested item.
	ErrNotFound = errors.New("plugin: not found")
	// ErrRateLimited: the source throttled the request.
	ErrRateLimited = errors.New("plugin: rate limited")
	// ErrUnavailable: server error, network error, timeout or unreadable response.
	ErrUnavailable = errors.New("plugin: unavailable")
)

// Plugin is a compiled-in or (later) loaded plugin. Source, Importer and
// Exporter return nil for ids the manifest does not declare.
type Plugin interface {
	Manifest() Manifest
	Source(id string) Source
	Importer(id string) Importer
	Exporter(id string) Exporter
}

// Source finds and fetches item data. Operations are kept separate so the
// core can pace, retry and load them independently. Errors wrap ErrNotFound,
// ErrRateLimited or ErrUnavailable.
type Source interface {
	Search(ctx context.Context, kind, q string) ([]ItemSummary, error)
	Details(ctx context.Context, kind, id string) (ItemData, error)
	People(ctx context.Context, kind, id string) ([]Credit, error)
	Recommendations(ctx context.Context, kind, id string) ([]ItemSummary, error)
	Lookup(ctx context.Context, kind, idType, value string) (ItemData, error)
}

// Importer turns an uploaded file into normalised rows.
type Importer interface {
	Parse(ctx context.Context, r io.Reader) (ParseResult, error)
}

// Exporter writes entries to a file format.
type Exporter interface {
	Export(ctx context.Context, entries []ExportEntry, w io.Writer) error
}

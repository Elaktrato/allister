package plugin

import (
	"context"
	"errors"
	"io"
	"time"
)

// Airing is one scheduled release of a source item: its number and when it
// airs (UTC).
type Airing struct {
	Number int       `json:"number"`
	AirsAt time.Time `json:"airs_at"`
}

// ScheduleSource is the optional capability of a source that knows the air
// dates of a kind's numbered releases. A source declares it with
// SourceInfo.SupportsSchedule; the schedule job calls it only for such
// sources, and the calendar feed lists what it stored.
type ScheduleSource interface {
	Schedule(ctx context.Context, kind, id string) ([]Airing, error)
}

var (
	// ErrNotFound: the source does not know the requested item.
	ErrNotFound = errors.New("plugin: not found")
	// ErrRateLimited: the source throttled the request.
	ErrRateLimited = errors.New("plugin: rate limited")
	// ErrUnavailable: server error, network error, timeout or unreadable response.
	ErrUnavailable = errors.New("plugin: unavailable")
	// ErrBadResponse: the source answered, but with data the core cannot use
	// (malformed, or an error the source will repeat); retrying will not help.
	// The web shows it as a bad gateway (502); an import fails that row and
	// moves on, it never retries it.
	ErrBadResponse = errors.New("plugin: bad response from the source")
	// ErrBadInput: an importer was given a file that is not in its format.
	ErrBadInput = errors.New("plugin: bad input")
)

// Plugin is a compiled-in or (later) loaded plugin. Manifest is called once,
// when the plugin is registered; the registry validates and keeps a deep copy,
// so later changes to what Manifest would return have no effect. Source, Importer and
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

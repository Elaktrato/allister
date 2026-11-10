package plugin

import "time"

// Dates in this contract (FieldValue.Date, ParsedEntry.StartedOn/FinishedOn,
// ExportEntry.StartedOn/FinishedOn) are CALENDAR DAYS: the value is the UTC
// midnight of that day and only the date part is meaningful. The type stays
// *time.Time (JSON RFC 3339, e.g. 2024-03-05T00:00:00Z). Nothing in this
// package enforces the convention: producers (plugins, importers) should
// supply UTC-midnight values, and consumers that persist dates (the library,
// added in a later task) should normalise them with DateOnly.

// DateOnly returns the UTC midnight of t's calendar date (in t's own zone).
func DateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Title is one title of an item in one language.
type Title struct {
	Lang string `json:"lang"`
	Text string `json:"text"`
}

// FieldValue is the typed value of one field; only the member matching the
// field type is set.
type FieldValue struct {
	Text string     `json:"text,omitempty"`
	Num  *float64   `json:"num,omitempty"`
	Date *time.Time `json:"date,omitempty"` // calendar day, UTC midnight by convention (see DateOnly)
	List []string   `json:"list,omitempty"`
}

// PersonRef identifies a person at a source.
type PersonRef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ImageURL string `json:"image_url,omitempty"`
	URL      string `json:"url,omitempty"`
}

// Credit is a person with a role on an item. With is an optional counterpart
// ("character, voiced by X": Person is the character, With the voice actor).
// Detail is an optional free-text qualifier of the role shown next to it (for
// example MAIN or SUPPORTING for an anime character): a single line of at most
// 100 characters (the library rejects control characters and longer values);
// empty means none and is omitted from JSON.
type Credit struct {
	Role     string     `json:"role"`
	Detail   string     `json:"detail,omitempty"`
	Person   PersonRef  `json:"person"`
	With     *PersonRef `json:"with,omitempty"`
	WithRole string     `json:"with_role,omitempty"`
}

// ItemSummary is the short form of an item as returned by search. URL is an
// optional http(s) link to the item's page on the source site (used for
// relations the core cannot link internally, for example a manga related to
// an anime); empty means none and is omitted from JSON. The core only ever
// renders it as a link when it is https.
type ItemSummary struct {
	Source   string                `json:"source"`
	ID       string                `json:"id"`
	Kind     string                `json:"kind"`
	Titles   []Title               `json:"titles,omitempty"`
	Fields   map[string]FieldValue `json:"fields,omitempty"`
	CoverURL string                `json:"cover_url,omitempty"`
	URL      string                `json:"url,omitempty"`
}

// Relation links an item to a related item.
type Relation struct {
	Kind string      `json:"kind"`
	Item ItemSummary `json:"item"`
}

// Tag is a named tag with an optional relevance rank (0 to 100) and spoiler flag.
type Tag struct {
	Name    string `json:"name"`
	Rank    *int   `json:"rank,omitempty"`
	Spoiler bool   `json:"spoiler,omitempty"`
}

// ItemData is the full form of an item as returned by Details/Lookup.
type ItemData struct {
	ItemSummary
	ExternalURL string            `json:"external_url,omitempty"`
	ExternalIDs map[string]string `json:"external_ids,omitempty"`
	Relations   []Relation        `json:"relations,omitempty"`
	Genres      []string          `json:"genres,omitempty"`
	Tags        []Tag             `json:"tags,omitempty"`
}

// ParsedEntry is one normalised row produced by an importer.
type ParsedEntry struct {
	Kind        string             `json:"kind"`
	Title       string             `json:"title"`
	ExternalIDs map[string]string  `json:"external_ids,omitempty"`
	Status      StatusClass        `json:"status,omitempty"`
	Rating      *int               `json:"rating,omitempty"`
	Progress    map[string]float64 `json:"progress,omitempty"`
	StartedOn   *time.Time         `json:"started_on,omitempty"`  // calendar day, UTC midnight by convention (see DateOnly)
	FinishedOn  *time.Time         `json:"finished_on,omitempty"` // calendar day, UTC midnight by convention (see DateOnly)
	Comment     string             `json:"comment,omitempty"`
	CoverURL    string             `json:"cover_url,omitempty"`
	Notes       []string           `json:"notes,omitempty"`
}

// SkippedRow reports an input row an importer could not use.
type SkippedRow struct {
	Line   int    `json:"line"`
	Title  string `json:"title,omitempty"`
	Reason string `json:"reason"`
}

// ParseResult is the output of an importer. Skipped lists the rows the user
// should hear about; Ignored only counts rows that are not meant for this
// importer at all (another media type in a mixed export) and are not worth
// listing. Ignored is optional (omitted when 0), so it does not change the
// contract version.
type ParseResult struct {
	Entries []ParsedEntry `json:"entries,omitempty"`
	Skipped []SkippedRow  `json:"skipped,omitempty"`
	Ignored int           `json:"ignored,omitempty"`
}

// ExportEntry is one list entry handed to an exporter.
type ExportEntry struct {
	Kind        string             `json:"kind"`
	Titles      []Title            `json:"titles,omitempty"`
	ExternalIDs map[string]string  `json:"external_ids,omitempty"`
	Status      StatusClass        `json:"status,omitempty"`
	Rating      *int               `json:"rating,omitempty"`
	Progress    map[string]float64 `json:"progress,omitempty"`
	StartedOn   *time.Time         `json:"started_on,omitempty"`  // calendar day, UTC midnight by convention (see DateOnly)
	FinishedOn  *time.Time         `json:"finished_on,omitempty"` // calendar day, UTC midnight by convention (see DateOnly)
	Comment     string             `json:"comment,omitempty"`
}

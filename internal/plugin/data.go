package plugin

import "time"

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
	Date *time.Time `json:"date,omitempty"`
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
type Credit struct {
	Role     string     `json:"role"`
	Person   PersonRef  `json:"person"`
	With     *PersonRef `json:"with,omitempty"`
	WithRole string     `json:"with_role,omitempty"`
}

// ItemSummary is the short form of an item as returned by search.
type ItemSummary struct {
	Source   string                `json:"source"`
	ID       string                `json:"id"`
	Kind     string                `json:"kind"`
	Titles   []Title               `json:"titles,omitempty"`
	Fields   map[string]FieldValue `json:"fields,omitempty"`
	CoverURL string                `json:"cover_url,omitempty"`
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
	StartedOn   *time.Time         `json:"started_on,omitempty"`
	FinishedOn  *time.Time         `json:"finished_on,omitempty"`
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

// ParseResult is the output of an importer.
type ParseResult struct {
	Entries []ParsedEntry `json:"entries,omitempty"`
	Skipped []SkippedRow  `json:"skipped,omitempty"`
}

// ExportEntry is one list entry handed to an exporter.
type ExportEntry struct {
	Kind        string             `json:"kind"`
	Titles      []Title            `json:"titles,omitempty"`
	ExternalIDs map[string]string  `json:"external_ids,omitempty"`
	Status      StatusClass        `json:"status,omitempty"`
	Rating      *int               `json:"rating,omitempty"`
	Progress    map[string]float64 `json:"progress,omitempty"`
	StartedOn   *time.Time         `json:"started_on,omitempty"`
	FinishedOn  *time.Time         `json:"finished_on,omitempty"`
	Comment     string             `json:"comment,omitempty"`
}

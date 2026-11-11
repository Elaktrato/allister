// Package plugin defines the data-only plugin contract of Allister and the
// registry that loads plugins.
//
// A plugin is described by a Manifest (plain, versioned data) plus request/
// response behaviour (Source, Importer, Exporter) whose inputs and outputs are
// plain data too. No Go type, database handle or HTTP handler crosses the
// contract, so a runtime loader can later sit behind the same shapes. Every
// type in this package round-trips through encoding/json unchanged.
package plugin

// ContractVersion is the contract major version this core speaks. A manifest
// declaring a different major is refused.
const ContractVersion = 1

// ProgressType is one member of the CLOSED core enum of progress types.
// Plugins cannot invent one; adding a type is a core change plus a contract
// version bump. Percent is a whole number 0 to 100, Duration whole seconds,
// Count and Pages whole numbers.
type ProgressType string

const (
	Count    ProgressType = "count"
	Pages    ProgressType = "pages"
	Percent  ProgressType = "percent"
	Duration ProgressType = "duration"
)

// ProgressTypes returns the closed enum in a stable order.
func ProgressTypes() []ProgressType { return []ProgressType{Count, Pages, Percent, Duration} }

// ValidProgressType reports whether t is a member of the closed enum.
func ValidProgressType(t ProgressType) bool {
	for _, v := range ProgressTypes() {
		if v == t {
			return true
		}
	}
	return false
}

// FieldType is the type of an item field.
type FieldType string

const (
	FieldText   FieldType = "text"
	FieldNumber FieldType = "number"
	FieldDate   FieldType = "date"
	FieldEnum   FieldType = "enum"
	FieldList   FieldType = "list"
	FieldURL    FieldType = "url"
)

// FieldTypes returns every field type in a stable order.
func FieldTypes() []FieldType {
	return []FieldType{FieldText, FieldNumber, FieldDate, FieldEnum, FieldList, FieldURL}
}

func validFieldType(t FieldType) bool {
	for _, v := range FieldTypes() {
		if v == t {
			return true
		}
	}
	return false
}

// StatusClass is one of the five fixed core status classes; plugins only
// supply labels for them.
type StatusClass string

const (
	Planned   StatusClass = "planned"
	Active    StatusClass = "active"
	Paused    StatusClass = "paused"
	Completed StatusClass = "completed"
	Dropped   StatusClass = "dropped"
)

// StatusClasses returns the five classes in a stable order.
func StatusClasses() []StatusClass {
	return []StatusClass{Planned, Active, Paused, Completed, Dropped}
}

// FieldDef declares one typed item field. Unit is an optional short unit
// label of a number field ("eps", "pages"): a single line of at most 20
// characters without control characters, shown after the number wherever
// the core prints the value ("37 eps"); empty means none and is omitted
// from JSON.
type FieldDef struct {
	Key        string    `json:"key"`
	Label      string    `json:"label"`
	Type       FieldType `json:"type"`
	EnumValues []string  `json:"enum_values,omitempty"`
	Min        *float64  `json:"min,omitempty"`
	Max        *float64  `json:"max,omitempty"`
	Show       bool      `json:"show,omitempty"`
	Editable   bool      `json:"editable,omitempty"`
	Filterable bool      `json:"filterable,omitempty"`
	Sortable   bool      `json:"sortable,omitempty"`
	Required   bool      `json:"required,omitempty"`
	Unit       string    `json:"unit,omitempty"`
}

// StatusLabels names the five status classes for one media kind.
type StatusLabels struct {
	Planned   string `json:"planned"`
	Active    string `json:"active"`
	Paused    string `json:"paused"`
	Completed string `json:"completed"`
	Dropped   string `json:"dropped"`
}

// ProgressDef is one eligible progress track. The same Type may appear more
// than once with different keys and labels. TotalField, when set, names a
// number field of the kind that supplies the track's total.
type ProgressDef struct {
	Key        string       `json:"key"`
	Type       ProgressType `json:"type"`
	Label      string       `json:"label"`
	TotalField string       `json:"total_field,omitempty"`
}

// ProgressSpec lists the eligible progress tracks of a kind, the default
// combination of active track keys, every allowed combination, and the
// primary key (a count or pages track) the "+1" action applies to. An empty
// PrimaryKey hides "+1".
type ProgressSpec struct {
	Eligible     []ProgressDef `json:"eligible,omitempty"`
	Defaults     []string      `json:"defaults,omitempty"`
	Combinations [][]string    `json:"combinations,omitempty"`
	PrimaryKey   string        `json:"primary_key,omitempty"`
}

// SectionType is one of the core's fixed page building blocks.
type SectionType string

const (
	SectionInfo            SectionType = "info"
	SectionText            SectionType = "text"
	SectionTitles          SectionType = "titles"
	SectionRelated         SectionType = "related"
	SectionPeople          SectionType = "people"
	SectionGenres          SectionType = "genres"
	SectionTags            SectionType = "tags"
	SectionRecommendations SectionType = "recommendations"
	SectionLinks           SectionType = "links"
)

// SectionTypes returns every section type in a stable order.
func SectionTypes() []SectionType {
	return []SectionType{SectionInfo, SectionText, SectionTitles, SectionRelated, SectionPeople,
		SectionGenres, SectionTags, SectionRecommendations, SectionLinks}
}

func validSectionType(t SectionType) bool {
	for _, v := range SectionTypes() {
		if v == t {
			return true
		}
	}
	return false
}

// SectionDef places one building block on the item overview. Field, when set,
// must name a field of the kind. Roles labels the credit roles of a people
// section.
type SectionDef struct {
	Type  SectionType `json:"type"`
	Label string      `json:"label,omitempty"`
	Field string      `json:"field,omitempty"`
	Roles []string    `json:"roles,omitempty"`
}

// TermDef enables genres or tags as first-class metadata of a kind.
type TermDef struct {
	Enabled    bool   `json:"enabled"`
	Label      string `json:"label,omitempty"`
	Editable   bool   `json:"editable,omitempty"`
	Filterable bool   `json:"filterable,omitempty"`
}

// Core columns of the list table: values the core keeps for every entry,
// which a kind may offer as columns by key (ColumnDef.Key). The title column
// is always shown and is not one of them.
const (
	ColumnProgress = "progress"
	ColumnRating   = "rating"
	ColumnStatus   = "status"
	ColumnUpdated  = "updated"
)

// CoreColumns returns the closed set of core column keys in a stable order
// (a fresh slice).
func CoreColumns() []string {
	return []string{ColumnProgress, ColumnRating, ColumnStatus, ColumnUpdated}
}

// ColumnDef offers one column of the kind's list table. Key names a field of
// the kind or a core column (CoreColumns); Label overrides the heading (the
// field's label, or the core's "Progress", "Score", "Status", "Updated"):
// a single line of at most 40 characters. Default columns are shown until
// the user chooses, in declaration order. Hide is the screen width the
// column needs: "" always shown, "sm", "md" or "lg" from that width on.
type ColumnDef struct {
	Key     string `json:"key"`
	Label   string `json:"label,omitempty"`
	Default bool   `json:"default,omitempty"`
	Hide    string `json:"hide,omitempty"`
}

// KindDef is one media kind of a plugin. TitleLanguages are the language
// tags of its titles; the user picks one of them per plugin. TitleFallback is
// the optional order of title languages the core tries when an item has no
// title in the chosen language (each a member of TitleLanguages, listed
// once); after it, or without it, the item's first stored title is shown.
// It is omitted from JSON when empty. Columns, when set, are the columns its
// list table offers (see ColumnDef); without them the core derives the table
// from the fields. It is omitted from JSON when empty.
type KindDef struct {
	Key            string       `json:"key"`
	Label          string       `json:"label"`
	LabelPlural    string       `json:"label_plural"`
	Fields         []FieldDef   `json:"fields,omitempty"`
	Genres         TermDef      `json:"genres"`
	Tags           TermDef      `json:"tags"`
	Statuses       StatusLabels `json:"statuses"`
	Progress       ProgressSpec `json:"progress"`
	Sections       []SectionDef `json:"sections,omitempty"`
	TitleLanguages []string     `json:"title_languages"`
	TitleFallback  []string     `json:"title_fallback,omitempty"`
	Columns        []ColumnDef  `json:"columns,omitempty"`
}

// SourceInfo describes a data source. URLPatterns look like
// "example.org/item/{id}"; the core matches pasted URLs against them so no
// plugin code parses URLs.
type SourceInfo struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	RequestsPerMinute int      `json:"requests_per_minute"`
	ExternalIDTypes   []string `json:"external_id_types,omitempty"`
	SupportsLookup    bool     `json:"supports_lookup,omitempty"`
	// SupportsSchedule: the source implements ScheduleSource (the core then
	// keeps its airing schedule for the calendar feed).
	SupportsSchedule bool     `json:"supports_schedule,omitempty"`
	LinkLabel        string   `json:"link_label,omitempty"`
	URLPatterns      []string `json:"url_patterns,omitempty"`
	// Kinds lists the kinds this source serves. Empty means every kind.
	Kinds []string `json:"kinds,omitempty"`
}

// ImporterInfo describes an import format. ResolveVia names the declared
// source used to look up ExternalIDType (that source must have
// SupportsLookup and list ExternalIDType in its ExternalIDTypes; an empty
// ResolveVia makes the importer manual-only); FallbackTitleLang is the title
// language of the manual entry kept when nothing matches. ExternalIDLabel is
// an optional human name of ExternalIDType ("Example DB"): a single line of
// at most 40 characters; the core shows such an id as "<label> #<value>"
// (the type itself when empty). UnmatchedNote is an optional sentence the
// core shows on an imported item that the source did not match (a manual
// item that still carries its ExternalIDType id), for example "Imported from
// Example CSV; Example DB had no match for #{id}.": a single line of at most
// 200 characters without control characters, in which every "{id}" is
// replaced by the item's id of ExternalIDType. Empty means the core's own
// neutral sentence.
type ImporterInfo struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	FileHint string   `json:"file_hint,omitempty"`
	Accept   []string `json:"accept,omitempty"`
	// AccountLabel names the account field of an AccountImporter ("Username");
	// empty means the importer takes a file.
	AccountLabel      string `json:"account_label,omitempty"`
	ExternalIDType    string `json:"external_id_type,omitempty"`
	ExternalIDLabel   string `json:"external_id_label,omitempty"`
	ResolveVia        string `json:"resolve_via,omitempty"`
	FallbackTitleLang string `json:"fallback_title_lang,omitempty"`
	UnmatchedNote     string `json:"unmatched_note,omitempty"`
}

// ExporterInfo describes an export format.
type ExporterInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Extension string `json:"extension"`
	MIME      string `json:"mime"`
}

// Manifest is the complete declarative description of a plugin. Icon is the
// optional NAME of a Material Symbols icon ("live_tv", "menu_book"): plain
// data, lower-case letters, digits and "_", 1 to 40 characters. The core
// shows it next to the plugin's navigation entry; empty means the core's
// default icon. It is omitted from JSON when empty.
type Manifest struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Version         string         `json:"version"`
	ContractVersion int            `json:"contract_version"`
	DefaultEnabled  bool           `json:"default_enabled"`
	Icon            string         `json:"icon,omitempty"`
	Kinds           []KindDef      `json:"kinds"`
	Sources         []SourceInfo   `json:"sources,omitempty"`
	Importers       []ImporterInfo `json:"importers,omitempty"`
	Exporters       []ExporterInfo `json:"exporters,omitempty"`
}

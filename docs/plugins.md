# Writing an Allister plugin

This guide describes the plugin contract as it is implemented today (contract version 1, package `internal/plugin`). It is written for someone adding a media type, such as books or TV, next to the built-in anime and manga plugins.

## What a plugin is

A plugin is **data plus four request/response operations**. It declares what its media type looks like (a `Manifest`) and answers plain-data calls: search a source, fetch details, parse an import file, write an export file. The core owns everything else: storage, pages and templates, forms, the list with its search and filters, the job queue, image downloads, imports, exports, the universal backup, and cross-origin protection.

A plugin never ships HTML, CSS or JavaScript, never registers HTTP routes and never touches the database. The core draws every page from a fixed set of building blocks (see [Sections](#sections)). Every type that crosses the contract round-trips through `encoding/json` unchanged (`contract_json_test.go`). A later runtime loader (a sandboxed module or a separate process) could sit behind the same shapes, but that loader does not exist yet (see [Marketplace bundles](#future-marketplace-bundles-not-implemented)).

Today plugins are compiled in: a Go package that implements `plugin.Plugin` and is registered in `cmd/allister/main.go`:

```go
registry := plugin.NewRegistry(settingsStore)
if err := registry.Register(anime.New(anime.Options{})); err != nil {
	log.Printf("startup: %v", err)
}
```

The reference implementations are `internal/plugins/anime`, `internal/plugins/book` (three sources, two kinds) and `internal/plugins/tv` (two sources, a schedule). The test-only fixtures `plugintest.BookPlugin()` and `plugintest.TVPlugin()` in `internal/plugin/plugintest` show a book-like kind (pages or percent progress, authors as people) and a TV-like kind (season, episode and position tracks). `internal/web/contract_proof_test.go` runs both through the real server next to anime.

## The Go interface

```go
type Plugin interface {
	Manifest() Manifest
	Source(id string) Source      // nil for ids the manifest does not declare
	Importer(id string) Importer  // nil likewise
	Exporter(id string) Exporter  // nil likewise
}

type Source interface {
	Search(ctx context.Context, kind, q string) ([]ItemSummary, error)
	Details(ctx context.Context, kind, id string) (ItemData, error)
	People(ctx context.Context, kind, id string) ([]Credit, error)
	Recommendations(ctx context.Context, kind, id string) ([]ItemSummary, error)
	Lookup(ctx context.Context, kind, idType, value string) (ItemData, error)
}

type Importer interface {
	Parse(ctx context.Context, r io.Reader) (ParseResult, error)
}

type Exporter interface {
	Export(ctx context.Context, entries []ExportEntry, w io.Writer) error
}
```

`Register` calls `Manifest()` exactly once, deep-copies the result through its JSON form, validates the copy (`plugin.Validate`) and keeps only that snapshot. Changing what `Manifest()` returns later has no effect. A nil or panicking plugin, an invalid manifest, a reserved id or an id that is already registered is refused. The reason is listed under "Refused plugins" in Settings > Plugins, and the other plugins still load.

## Manifest

| Field | Meaning |
|---|---|
| `ID` | Stable slug: lower-case letters, digits and `-`, 1 to 32 characters, starting with a letter. It is the URL prefix (`/{id}/...`) and the namespace of all the plugin's data. Never change it once users have data. |
| `Name` | Display name (Settings, the import page). Required. |
| `Version` | The plugin's own version string, shown in Settings as `v<Version>`. Not checked by the core. |
| `ContractVersion` | Must equal `plugin.ContractVersion` (1). Any other value is refused with "plugin speaks contract N, core speaks 1". |
| `DefaultEnabled` | Whether the plugin is on before the user has chosen. The user's choice is stored as the setting `plugin.<id>.enabled` (`1` or `0`). |
| `Icon` | Optional. The name of a [Material Symbols](https://fonts.google.com/icons) icon shown next to the plugin's entry in the sidebar, for example `live_tv` (the anime plugin) or `menu_book`. Plain data: lower-case letters, digits and `_`, 1 to 40 characters (`^[a-z0-9_]{1,40}$`); anything else is refused. Empty means the core's default icon (`category`). JSON `icon`, omitted when empty. The core ships the whole icon font (see `PINS.md`), so every icon name works; a name the font does not have shows a clipped letter instead of an icon. A test checks every compiled-in manifest icon against the font's name list (`internal/web/testdata/material-symbols-outlined.codepoints`), so a typo fails the build's tests. |
| `Kinds` | One or more media kinds (below). At least one is required. |
| `Sources`, `Importers`, `Exporters` | Optional declarations (below). |

**Reserved ids.** Plugin ids `list`, `people`, `settings`, `covers`, `static`, `healthz` and `api` belong to the core's first URL segment. Source ids `manual`, `export` and `search` would be hidden by the core's own routes under `/{plugin}/` and are refused too. Kind, source, importer and exporter ids use the slug rule above. Field and progress keys use `^[a-z][a-z0-9_]{0,31}$`.

### Kinds

A `KindDef` describes one media kind:

- `Key`, `Label`, `LabelPlural`: id and display names, all three required. The list page heading uses `LabelPlural`.
- `TitleLanguages`: the language tags the plugin's titles use (at least one, no duplicates or empty values). Every item has titles per language. The user picks the display language **per plugin** on the plugin's settings page (`/settings/plugins/{id}`) from these tags (the union over the plugin's kinds, in order) plus tags found in the plugin's stored titles. Before the user chooses, the first declared tag is used.
- `TitleFallback`: optional, the order of languages the core tries when an item has no title in the chosen language, for example `["ja", "ja-latn", "en"]` (the anime plugin). Every entry must be one of `TitleLanguages`, listed once. After it (or without it) the item's first stored title is shown. JSON `title_fallback`, omitted when empty. There is no fallback order in the core any more.
- `Columns`: optional, the columns of the kind's list table (see [Table columns](#table-columns)).
- `Fields`: the item schema (below).
- `Genres`, `Tags`: `TermDef{Enabled, Label, Editable, Filterable}`. When enabled, items carry genres (names) and tags (a name, an optional rank from 0 to 100 and a spoiler flag) as dedicated, indexed rows. `Label` is required when enabled. `Editable` puts a text area in Edit details and the manual form (one term per line; tags as `Name:80!`, where `:80` is the rank and a trailing `!` marks a spoiler). `Filterable` adds a multi-select with counts from the user's own library and an any/all switch to the list. The core trims terms, removes case-insensitive duplicates and allows at most 60 characters per term, 50 genres and 100 tags per item.
- `Statuses`: labels for the five fixed core status classes `planned`, `active`, `paused`, `completed` and `dropped` ("Plan to watch", "Watching", ...). All five labels are required. A plugin cannot add a status.
- `Progress`: see [Progress](#progress).
- `Sections`: see [Sections](#sections).

### Table columns

```go
type ColumnDef struct {
	Key     string // a field key of the kind, or a core column
	Label   string // optional heading, <= 40 characters, single line
	Default bool   // shown before the user chooses
	Hide    string // "", "sm", "md" or "lg"
}
```

The list table always has the cover, the title (which sorts) and the actions. Everything else is a column the kind offers in `KindDef.Columns`, in the order given. `Key` names a field of the kind (its value is shown as on the overview, with the unit) or one of the **core columns** `progress`, `rating` (heading "Score"), `status` and `updated` (`plugin.CoreColumns()`; a closed set). `Label` replaces the field's label or the core heading. `Hide` is the screen width the column needs: empty means always, `sm`, `md` and `lg` show it from 640, 768 and 1024 px on. JSON `columns` (each `key`, `label`, `default`, `hide`, the last three omitted when unset), omitted when empty.

`Validate` refuses a column whose key is neither a field nor a core column, a field key that is also a core column name, `title` (always shown), a key listed twice, a label that is blank (only spaces; leave it empty for the default heading), over 40 characters after trimming or with control characters, and any other `Hide`.

The `Default` columns, in declaration order, are the table until the user chooses. The user's choice (Settings > Plugins > Settings, one form per kind) is stored as the setting `plugin.<id>.columns.<kind>`, a JSON list of column keys; posted keys the kind does not offer are refused, and stored keys it no longer offers (after a plugin update removed a column) are dropped when read. A column heading sorts when its key is a sort key: a sortable field (see `Sortable`), `progress` (with a +1 track), `rating` and `updated`; `status` never sorts. The compact Sort by control of the filter panel always offers every sort key, also of columns that are not shown.

Without `Columns` the core derives the table as before: the type column (the first `Show` enum field, else the first `Show` text field; from `sm`), every sortable field (from `md`), Progress, Score, Status and Updated (from `lg`), all shown by default. The anime plugin declares Type, Year, Progress, Score, Status and Updated as defaults (exactly its earlier table) and offers Episodes, Studio, Source, Started and Ended.

**Upgrade note.** `TitleFallback` and `Columns` are optional and omitted from JSON when unset, so the contract version stays 1 and an existing manifest keeps working: without `TitleFallback` a missing language shows the first stored title (the core no longer tries `ja`, `ja-latn`, `en` for every plugin), and without `Columns` the table is derived as above. The old global title language setting (`title_lang`) is moved to `plugin.anime.title_lang` by migration 00011.

### Fields

```go
type FieldDef struct {
	Key, Label string
	Type       FieldType // text, number, date, enum, list, url
	EnumValues []string  // enum only, at least one
	Min, Max   *float64  // finite bounds, Min <= Max
	Show, Editable, Filterable, Sortable, Required bool
	Unit       string    // number fields only
}
```

`Key` (unique within the kind) and `Label` are required, and `Type` must be one of the six field types.

| Flag | Effect in the core |
|---|---|
| `Show` | The field appears in the overview's info section, and in search results unless it is a URL field or the text of a text section. Without declared `Columns`, the kind's first `Show` enum field (else its first `Show` text field) is the list's type column. |
| `Editable` | The field is in Edit details and in the manual-entry form. Fields that are not editable keep their stored value on every edit. |
| `Filterable` | The field gets a filter in the list's filter bar and a URL parameter `f.<key>`. Enum: a select, which matches exactly (several values match any of them). Number and date: a from/to range (`f.<key>.from`, `f.<key>.to`, canonical `f.<key>=a..b`). List: matches any of the given values. Text and URL: case-insensitive "contains" (ASCII case folding). |
| `Sortable` | The field is offered as a sort (`sort=<key>`) for number, date, text, enum and URL fields, and its column heading sorts while the column is shown. On a list field the flag is accepted but has no effect: no sort is offered. |
| `Required` | A manual entry must have a value. In Edit details the field is required only when the item already has a value stored, so a source that lacks the field never blocks other edits. A backup restore refuses a manual item that is missing one. Items saved from a source may lack it. |
| `Unit` | A short label (at most 20 characters, single line, no control characters) shown after a number ("37 eps"). Number fields only. |

Stored values are typed (a number column, a date column, text, list rows), so ranges and sorts compare correctly. Text values are limited to 20,000 characters, titles to 300, and list fields to 1,000 values. A field's type must not change between versions of a plugin, because existing rows keep their stored type.

**Convention:** the related-items section shows a related item's field `type` (text) next to its title when the summary carries one.

### Progress

Progress types are a **closed core enum**: `count`, `pages`, `percent` and `duration`. A plugin cannot invent one. Adding a type is a core change and a contract version bump.

- `count` and `pages`: whole numbers, at least 0.
- `percent`: a whole number from 0 to 100. It has no total.
- `duration`: whole seconds. The user types `h:mm:ss`, `m:ss` or a number of seconds, and the core shows `h:mm:ss`.

```go
type ProgressDef struct {
	Key        string       // unique within the kind
	Type       ProgressType
	Label      string       // "Episode", "Page", "Position"
	TotalField string       // optional: a number field holding the total
}
type ProgressSpec struct {
	Eligible     []ProgressDef
	Defaults     []string   // the active keys of a new item
	Combinations [][]string // every allowed set of active keys
	PrimaryKey   string     // the "+1" track; "" hides +1
}
```

Rules enforced by `Validate`:

- Each eligible key is unique, has a label and a known type. The same type may appear more than once under different keys (a TV kind: `count` Season, `count` Episode, `duration` Position).
- `TotalField` must name a **number** field of the kind, and is not allowed on `percent`.
- When anything is eligible, `Defaults` and at least one combination are required. Every key in them must be eligible and listed once, no combination may be empty, and `Defaults` must equal one of the combinations (as a set).
- `PrimaryKey`, when set, must be an eligible `count` or `pages` track.
- `ResetOnPrimary` is for a track that belongs to the release the +1 advances: the TV position is cleared when the episode increments. It is not set on the primary track.

Behaviour: an item starts with the default combination. The **entry dialog** (Edit on a list row or on the overview, route `/list/{id}/edit`) offers the allowed combinations when there is more than one, for example a book switching from pages to percent. Values of tracks that become inactive are kept and come back when the user switches again. Edit details does not change the combination. A bounded track cannot exceed its total. "Finished" means every active bounded track is at its total (percent at 100), "not started" means every active track is 0, and anything else is "in progress". The list's progress filter (`progress=not_started|in_progress|finished`) uses these rules. Setting the status to completed does not change progress. "+1" raises the primary track by one, capped at its total. It is hidden when there is no primary key or the primary track is not active.

### Sections

Sections choose which of the core's fixed building blocks appear on an item's overview, and in which order:

| Type | Shows |
|---|---|
| `info` | Every `Show` field with a value (default heading "Information"). It also carries the source link unless a `links` section exists. |
| `text` | The text field named by `Field`, in paragraphs (heading: the field's label). |
| `titles` | All titles, the displayed one first. |
| `genres`, `tags` | Chips. Spoiler tags stay hidden behind "Show spoiler tags". Only allowed when the term type is enabled. |
| `related` | Relations. An item of a kind and source this plugin declares links to its overview. Anything else links to its https `URL` when it has one. |
| `people` | Credits. `Roles` limits the section to those credit roles (empty means all). A credit's counterpart (`With`, labelled `WithRole`) is shown with it, as in "character, voiced by X". |
| `recommendations` | A placeholder that loads the source's recommendations after the page has rendered (4 s timeout, cached for 10 minutes). It never appears for manual items. |
| `links` | The "View on ..." link (`SourceInfo.LinkLabel`, else "View on <source name>") to `ItemData.ExternalURL`, https only. |

`Label` overrides a section's heading. `Field` must name a field of the kind.

## Sources

```go
type SourceInfo struct {
	ID, Name          string
	RequestsPerMinute int      // > 0
	ExternalIDTypes   []string // id types Lookup understands, e.g. "mal"
	SupportsLookup    bool
	SupportsSchedule  bool     // the source implements ScheduleSource
	LinkLabel         string   // "View on AniList"
	URLPatterns       []string // "anilist.co/anime/{id}"
	Kinds             []string // kinds this source serves; empty = all
}
```

- Items from a source live at `/{plugin}/{source}/{id}`. Saved items keep their data locally. A source refresh never overwrites user edits, because there is none.
- `Search` returns `ItemSummary` values. `Details` and `Lookup` return `ItemData` (summary plus external URL, external ids, relations, genres and tags). `People` returns credits. `Recommendations` returns summaries. The operations are separate so the core can load them independently. An overview that is not saved makes one `Details` call when it renders and one `Recommendations` call afterwards. Both answers are cached for 10 minutes. Adding an item enqueues one background job that fetches `People` and downloads the cover and the people's images (at most 2 MiB per image).
- `RequestsPerMinute` paces the import runner's lookups through this source. Background jobs run one at a time, with at least 2 seconds between them. Interactive requests (search, overview, recommendations) are not paced. Overview details and recommendations are cached, searches are not.
- `SupportsLookup` and `ExternalIDTypes` say which external ids `Lookup` resolves. Importers use them, and the "Match to <source>" dialog offers its search through the first source that supports lookups.
- `Kinds` restricts a source to some of the plugin's kinds. The search page and match dialog then offer it only for those kinds; empty means every kind. A plugin with several sources can therefore put Open Library and Google Books on books and AniList on light-novel series without dead combinations.
- `SupportsSchedule` declares the optional `ScheduleSource` capability: `Schedule(ctx, kind, id) ([]Airing, error)`, where an `Airing` is a season (0 for none), a release number and its air time. The core keeps such schedules of watching entries and lists their upcoming releases in the calendar feed (`/calendar.ics`); it refreshes them through the job queue (at most daily through the feed, and once when an entry is added or synced). Sources without the capability simply have no calendar events. Errors follow the same rules as the other source calls. The calendar labels a season-aware airing as `Season 2, Episode 5`; season 0 keeps the old `Episode 12` form.
- `URLPatterns` let the core turn a pasted URL into an id, so no plugin code parses URLs. A pattern is `host/segment/.../{id}[/...]` with exactly one `{id}`. A leading `www.` is ignored, and the URL may end with one slug segment, a trailing slash, a query or a fragment. `{id}` (and a bare id typed into the match dialog) is 1 to 12 ASCII digits with leading zeros removed and not zero, or — for sources with non-numeric ids — 1 to 64 bytes of ASCII letters, digits, `_` and `-` that starts and ends with an alphanumeric.

### Errors

Source calls return errors wrapping these sentinels:

| Sentinel | Meaning | What the core does |
|---|---|---|
| `plugin.ErrNotFound` | The source does not know the item. | 404 page. A search shows no results. An import keeps the row as a manual entry (see below). The sync job stores no credits. |
| `plugin.ErrRateLimited` | The source throttled the request. | 503 page with a retry link; the search page says the source is rate limiting. Imports wait and retry the row, then pause the import and try again later. Sync jobs retry later. |
| `plugin.ErrUnavailable` | Network error, server error, timeout, unreadable response. | Same as `ErrRateLimited`, except that the search page says search is unavailable. |
| `plugin.ErrBadResponse` | The source answered with data the core cannot use, and a retry would get the same answer. | 502 page. An import records the row as failed and moves on, without retrying it. |
| `plugin.ErrBadInput` | (Importers only) the file is not in the format. | The upload is refused with the error's text. |

Any other error from a source gives a 500 on the overview, an "unavailable" message on the search page, and a failed row in imports. `jobs.ErrRetryLater` and `jobs.ErrPermanent` belong to the core's job queue. Plugins never return them: the core's sync job and import runner wrap plugin errors with them (rate limits and outages become "retry later"; a bad answer from People and a job whose plugin id contradicts the stored item fail permanently).

### Sanitising source text

The library **rejects** control characters, NUL, invalid UTF-8, over-long values and non-http(s) URLs, because user input must never be silently rewritten. A plugin must therefore clean every string it takes from an external source, so that one stray character never makes an item unsaveable:

- `plugin.SingleLine(s, max)`: titles, names, genres, tags, single-line fields. Runs of whitespace and control characters become one space, and the result is trimmed and cut to `max` runes.
- `plugin.MultiLine(s, max)`: synopses and other multi-line text. Controls are removed except newline and tab, CRLF and CR become LF, and the result is trimmed and cut.
- `plugin.HTTPSURL(s)`: image and page URLs. The result is an absolute http(s) URL or `""`. `//host/x` becomes `https://host/x`, userinfo is removed, the scheme and host are lower-cased, and URLs longer than 2,048 characters are dropped.

Other limits the library enforces: at most 500 credits per item, a credit `Detail` of at most 100 characters, and 200 relations.

## Importers

```go
type ImporterInfo struct {
	ID, Name          string
	FileHint          string   // shown on the import page
	Accept            []string // file input accept list, e.g. ".csv"
	ExternalIDType    string   // the id each row is matched and de-duplicated by
	ExternalIDLabel   string   // human name of that id, <= 40 characters
	ResolveVia        string   // a declared source id; "" = manual-only
	ResolveViaFallback []string // further sources tried on ErrNotFound
	ManualRows        bool     // keep rows without the external id as manual entries
	FallbackTitleLang string   // title language of an unmatched row's manual item
	UnmatchedNote     string   // <= 200 characters, "{id}" is replaced by the id
	AccountLabel      string   // non-empty: the importer takes an account name, not a file
}
```

An importer may also implement the optional `AccountImporter` capability (`ImportAccount(ctx, account string) (ParseResult, error)`) and declare it with `AccountLabel` (for example "Username"): the plugin settings page then offers a name field instead of the file picker, and the generic import page leaves it out. The rows are the same `ParsedEntry` values; a row may carry `UpdatedAt` (the entry's source-side modification time) so the user can choose to update existing entries only when the source is newer.

`Parse` receives the uploaded file (at most 20 MiB) and returns `ParseResult{Entries, Skipped, Ignored}`. Each `ParsedEntry` holds the kind, title, external ids, status class, rating (0 to 10), progress per track key, calendar-day dates (UTC midnight, see `plugin.DateOnly`), comment, optional cover URL, item fields (`Fields`) and notes. `Fields` supplies item data for a manual row and fills only the fields a source left empty. `Skipped` lists rows the user should hear about. `Ignored` only counts rows meant for another media type. Return `ErrBadInput` (wrapped, with a readable reason) when the file is not in your format.

The core then does the rest, as a resumable job on the import page:

- A row without a value for `ExternalIDType`, or with an unknown kind, is stored as skipped with the reason. An importer therefore needs an `ExternalIDType` to import anything, unless it sets `ManualRows`: then a titled row without that id becomes a normal imported manual entry. When no row is importable, the upload is refused.
- A row whose external id is already saved is matched to that item instead of creating a duplicate: it is skipped when the item already has a list entry, and otherwise the entry is created from the row. All external ids on the row are checked, not only the declared one, so an additional id (for example a Goodreads book id) makes re-importing idempotent. A source match also copies the row's extra ids and fills missing item fields from `Fields`; source values win.
- With `ResolveVia`, the core looks the id up through that source, paced by its `RequestsPerMinute`, then through each `ResolveViaFallback` source only on `ErrNotFound`. A hit is saved as a normal source item. If every source misses, the row stays a **manual item** titled in `FallbackTitleLang` that carries the external id. Such an item counts as "unmatched": it gets a badge, the `unmatched=1` list filter and the `UnmatchedNote`, and the user can match it later. `ErrBadResponse` fails the row. Rate limits and outages retry/pause the same source and never fall through. Updating an existing entry writes only progress tracks that the entry has active; inactive tracks are skipped with a note.
- Without `ResolveVia`, every row becomes a manual item.

`Validate` refuses an importer whose `ResolveVia` or fallback names an undeclared source, a source without `SupportsLookup`, or a source whose `ExternalIDTypes` does not contain the importer's `ExternalIDType`, which is also required when `ResolveVia` is set. Fallbacks need a primary and may not repeat it. When the upload arrives, the core also refuses an importer whose resolution sources are missing from the plugin (`Source(id)` returns nil). Imports of a disabled plugin pause, and the import page says so.

## Exporters

```go
type ExporterInfo struct{ ID, Name, Extension, MIME string } // all required
```

`Export` receives every list entry of the plugin (titles, external ids, status, rating, active progress, dates, comment) and writes the file. The core serves it at `/{plugin}/export/{exporter}` as `<plugin>-<exporter>-<date><extension>`, linked from the list page and the plugin's row in Settings > Plugins. The whole list is built in memory, which is fine for a few thousand entries.

An export is the plugin's own interchange format. The **universal backup** (Settings > Backup) is separate and owned by the core. It covers every item of every installed plugin, including disabled ones, so a plugin needs no code for backups.

## Storage and enabling

The user's per-plugin choices live in the core's settings table, keyed by plugin id: `plugin.<id>.enabled`, `plugin.<id>.title_lang` and `plugin.<id>.columns.<kind>`. Plugin data lives in generic, plugin-namespaced tables (items, typed fields, titles, terms, people, credits, relations, external ids, list entries, progress values), so a plugin never brings migrations. People are namespaced per plugin and never shared across plugins. Disabling a plugin in Settings hides its navigation, routes (404), person pages, import choices and export links, and pauses its background jobs. All its rows stay. Enabling it brings everything back.

## Versioning

- `ContractVersion` is a major version: the core refuses any other value and does not negotiate.
- Additive, optional changes keep the version when the new member is omitted from JSON while unset (`omitempty`), as `ParseResult.Ignored` was added. Removing or changing the meaning of a member, or adding a progress type, bumps the contract.
- `Manifest.Version` is the plugin's own version. The core shows it but does not compare it. Keep ids, kind keys, field keys, field types and progress keys stable across plugin versions, because stored data refers to them.

## Future: marketplace bundles (not implemented)

Nothing loads plugins at runtime today. There is no marketplace, no signing and no sandbox, and every plugin is compiled in. The contract was kept to plain data so that a later bundle could map onto it without changing plugins:

- The **manifest** is already a JSON document (`Manifest` round-trips through JSON, `Register` keeps only a validated JSON copy). A bundle could ship it as a file and go through the same `Validate` and `Register` refusals.
- **Sources, importers and exporters** are request/response calls with JSON-shaped inputs and outputs. A loader could implement `plugin.Plugin` by forwarding each call to a sandboxed module or a separate process. Network access and pacing would stay with the core, as declared by `RequestsPerMinute`.
- **No HTML, JavaScript, routes or database access** is part of the contract, so a bundle has nothing to inject into pages.

Signing, trust, installation, updates, and how a loader would sandbox code are all open questions for that future work.

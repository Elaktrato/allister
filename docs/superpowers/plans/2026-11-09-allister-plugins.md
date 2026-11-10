# Allister Generic Core and Plugins Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
> Historical: written before the plugin era; the global `title_lang` setting it describes became per-plugin (`plugin.<id>.title_lang`, migration 00011).

**Goal:** Turn the working anime app into a generic, data-only-plugin core with anime as the first, switchable plugin, plus the approved follow-ups (edit details, late-loading recommendations, smaller images, list search and filters, pinned and checksummed builds, universal backup).

**Architecture:** A new `internal/plugin` package defines a versioned, serialisable, data-only contract (manifest, schema, progress types from a closed core enum, page sections, sources, importers, exporters) and a registry that persists enable/disable. A new `internal/library` package stores everything in generic plugin-namespaced tables. The existing `internal/store` is first re-implemented as a thin adapter over the library so the whole suite stays green, then each web/job/import area is ported to library + plugin, then the adapter and the anime-specific provider package are deleted. The anime plugin (`internal/plugins/anime`) is compiled in and talks to the core only through the contract; a runtime loader/marketplace is future work and must stay possible.

**Tech Stack:** Go 1.23 (pure Go, no CGO), `modernc.org/sqlite`, goose migrations, htmx, Tailwind, Docker (one volume), `net/http` ServeMux.

**Spec:** `docs/superpowers/specs/2026-11-09-allister-plugins-design.md` (this iteration) on top of `docs/superpowers/specs/2026-11-09-allister-anime-design.md` (baseline behaviour). The existing test suite expresses the anime behaviour baseline.

## Global Constraints

- Go 1.23, pure Go (no CGO), htmx + Tailwind, SQLite, Docker with one volume, no login (unchanged).
- Plugins are **data-only**: no plugin HTML, CSS or JavaScript, no plugin HTTP routes, no plugin database access. The core draws every page from a fixed set of building blocks: info list, text block, titles list, related items, people with roles, recommendation cards, external links.
- The contract is **serialisable and versioned** (`ContractVersion` major 1): plain data in and out; no Go types, `*sql.DB` or `http.Handler` cross it. The core refuses a manifest whose contract major differs, naming the plugin and the reason, and keeps starting with the other plugins.
- Progress types are a **closed core enum**: `count`, `pages`, `percent`, `duration`. A plugin lists the eligible types per media kind (label, optional total field), the default, and the allowed combinations. Percent = whole numbers 0 to 100; duration = whole seconds; count/pages = whole numbers. "Finished" = every active bounded type at its total (percent at 100); "not started" = every active type zero; else "in progress". Setting status to completed does not change progress. "+1" applies to the plugin's primary count/pages type and is hidden if none.
- Five fixed core status classes: `planned`, `active`, `paused`, `completed`, `dropped`; plugins only supply labels.
- Rating is nullable 0..10 (0 is a valid rating, blank is unrated). Dates are calendar days (UTC midnight of the typed day).
- A plugin's pages live under `/{plugin-id}/...` (anime keeps `/anime`, `/anime/search`, `/anime/{source}/{id}`, `/anime/manual/new`, `/anime/{source}/{id}/match`). Generic core routes stay: `/list/{id}/edit|increment|remove`, `/people/{id}`, `/settings`, `/settings/import`, `/covers/`, `/healthz`. Plugin ids may not collide with those first segments.
- Plugin data lives in generic plugin-namespaced tables (no per-plugin tables or migrations). Disabling a plugin never deletes rows: its jobs pause, its items vanish from every page and search, its URLs return 404; people are per plugin and never shared.
- Backup: a versioned archive (JSON + user-uploaded covers only); downloaded images are not included and are fetched again; import merges (skips existing plugin+source+source id), never overwrites or deletes; a newer major format is refused.
- AniList pacing stays 30 requests/minute; each source declares its own limit in its manifest. Person photos use the source's smaller `medium` size, covers keep the larger size, per-file download limit 2 MiB.
- Everything the current tests assert about anime behaviour must keep holding (tests may be moved/adapted to the new structure, never weakened). Rating and calendar-day rules, CSRF protection (`refuseCrossOrigin`), `Vary: HX-Request`/`isPartialRequest`, `hx-on::before-swap` dialog slots, per-page template sets, validate-before-save and transactional writes stay.
- No personal data in the repository (`yamtrack_*.csv` stays git-ignored and docker-ignored). Do not run broad Docker commands on this machine (it runs unrelated containers); Go runs via `scripts/go.sh`.

## Review Focus

- A disabled plugin: its URLs (GET and POST), search results, list rows, person pages and backup selection must be unreachable (404), and its queued jobs must not run, while all rows stay; enabling restores everything.
- Contract validation with a malformed manifest (duplicate field or progress keys, reserved plugin id such as `list`, unknown progress type, `TotalField` naming a missing or non-number field, enum field without values, a default type not in the eligible list, a combination using an ineligible type): refused with a clear message while other plugins still load.
- List search and filters with hostile text (`%`, `_`, quotes, backslashes, 10,000 characters, Unicode, SQL fragments in filter keys, sort names, genre and tag values): no injection, no wildcard semantics, bounded work; unknown filter keys and sort names rejected, not passed to SQL; genre/tag values compared case-insensitively as bound parameters.
- Progress edges: an item without a total, switching an item's active progress types while values exist (values for inactive types are retained, not lost), percent over 100, negative duration, `+1` with no primary type, completed with zero progress, progress above a total.
- Backup import: malformed JSON, truncated or oversized archive, path traversal (`../`) in image names, a newer format version, duplicate items (merged, not duplicated), an archive containing a disabled plugin's data, importing twice.

## Safety net

Tasks 5 to 12 keep the full test suite green after each task. `internal/store` becomes a temporary adapter over the generic schema (Task 5) so unported handlers keep working while each area is ported; adapters and legacy code are deleted in Task 13 and a guard test then forbids anime words in core packages.

## File Structure (target)

```
internal/plugin/            contract types, enums, validation, Registry (+ plugintest/ fixtures for tests)
internal/plugins/anime/     manifest, schema, AniList source, Yamtrack importer, exporter (the anime plugin)
internal/library/           generic store: items, titles, fields, external ids, people, credits, relations,
                            entries, progress, list/query engine, delete/convert, backup
internal/jobs/              queue + generic sync job (plugin.Source)
internal/covers/            image download/save (size policy)
internal/importer/          generic import/export runner (resumable, paced)
internal/web/               generic pages and building-block templates, plugin routing, settings
internal/db/migrations/     00005_generic_core, 00006_entries, 00007_drop_legacy_tables (baseline replace), 00008_imports_plugin, 00009_rewrite_sync_jobs
```

---

### Task 1: Plugin contract, registry and test fixtures

**Files:**
- Create: `internal/plugin/contract.go`, `internal/plugin/data.go`, `internal/plugin/interfaces.go`, `internal/plugin/validate.go`, `internal/plugin/registry.go`, `internal/plugin/plugintest/plugintest.go`
- Test: `internal/plugin/validate_test.go`, `internal/plugin/registry_test.go`, `internal/plugin/contract_json_test.go`

**Interfaces:**
- Produces (all JSON-serialisable, no behaviour in the data types):
  - `const ContractVersion = 1`; `type ProgressType string` with `Count`, `Pages`, `Percent`, `Duration`; `type FieldType string` with `FieldText`, `FieldNumber`, `FieldDate`, `FieldEnum`, `FieldList`, `FieldURL`; `type StatusClass string` with `Planned`, `Active`, `Paused`, `Completed`, `Dropped`.
  - `FieldDef{Key, Label string; Type FieldType; EnumValues []string; Min, Max *float64; Show, Editable, Filterable, Sortable, Required bool}`; `StatusLabels{Planned, Active, Paused, Completed, Dropped string}`.
  - `ProgressDef{Key string; Type ProgressType; Label, TotalField string}`; `ProgressSpec{Eligible []ProgressDef; Defaults []string; Combinations [][]string; PrimaryKey string}`.
  - `SectionType` (`SectionInfo`, `SectionText`, `SectionTitles`, `SectionRelated`, `SectionPeople`, `SectionGenres`, `SectionTags`, `SectionRecommendations`, `SectionLinks`); `SectionDef{Type SectionType; Label, Field string; Roles []string}`.
  - `TermDef{Enabled bool; Label string; Editable, Filterable bool}`; `KindDef{Key, Label, LabelPlural string; Fields []FieldDef; Genres, Tags TermDef; Statuses StatusLabels; Progress ProgressSpec; Sections []SectionDef; TitleLanguages []string}`; `SourceInfo{ID, Name string; RequestsPerMinute int; ExternalIDTypes []string; SupportsLookup bool; LinkLabel string; URLPatterns []string}` (`URLPatterns` like `anilist.co/anime/{id}`: the core matches pasted URLs against them, so no plugin code parses URLs); `ImporterInfo{ID, Name, FileHint, ExternalIDType, ResolveVia, FallbackTitleLang string; Accept []string}`; `ExporterInfo{ID, Name, Extension, MIME string}`; `Manifest{ID, Name, Version string; ContractVersion int; DefaultEnabled bool; Kinds []KindDef; Sources []SourceInfo; Importers []ImporterInfo; Exporters []ExporterInfo}`.
  - Runtime data: `Title{Lang, Text string}`; `FieldValue{Text string; Num *float64; Date *time.Time; List []string}`; `PersonRef{ID, Name, ImageURL, URL string}`; `Credit{Role string; Person PersonRef; With *PersonRef; WithRole string}` (counterpart = "voiced by"); `ItemSummary{Source, ID, Kind string; Titles []Title; Fields map[string]FieldValue; CoverURL string}`; `Relation{Kind string; Item ItemSummary}`; `Tag{Name string; Rank *int; Spoiler bool}`; `ItemData{ItemSummary; ExternalURL string; ExternalIDs map[string]string; Relations []Relation; Genres []string; Tags []Tag}`; `ParsedEntry{Kind string; Title string; ExternalIDs map[string]string; Status StatusClass; Rating *int; Progress map[string]float64; StartedOn, FinishedOn *time.Time; Comment, CoverURL string; Notes []string}`; `SkippedRow{Line int; Title, Reason string}`; `ParseResult{Entries []ParsedEntry; Skipped []SkippedRow}`.
  - Behaviour interfaces: `Plugin{Manifest() Manifest; Source(id string) Source; Importer(id string) Importer; Exporter(id string) Exporter}`; `Source{Search(ctx, kind, q string) ([]ItemSummary, error); Details(ctx, kind, id string) (ItemData, error); People(ctx, kind, id string) ([]Credit, error); Recommendations(ctx, kind, id string) ([]ItemSummary, error); Lookup(ctx, kind, idType, value string) (ItemData, error)}` (errors `ErrNotFound`, `ErrRateLimited`, `ErrUnavailable` move here); `Importer{Parse(ctx, r io.Reader) (ParseResult, error)}`; `Exporter{Export(ctx, entries []ExportEntry, w io.Writer) error}` with `ExportEntry` carrying kind, titles, external ids, status, rating, progress, dates, comment.
  - `Validate(m Manifest) error` (all rules in Review Focus plus unique kind/field/progress/source/importer ids, defaults and combinations subsets of eligible keys, primary key a count/pages type, section fields exist, title languages non-empty, importer `ResolveVia` names a declared source); `Registry` with `NewRegistry(enabled EnabledStore)`, `Register(p Plugin) error` (validates; rejects duplicate ids and reserved ids `list`, `people`, `settings`, `covers`, `static`, `healthz`, `api`), `Plugins() []Plugin` (installed), `Enabled() []Plugin`, `Get(id) (Plugin, bool)`, `IsEnabled(id) bool`, `SetEnabled(ctx, id string, on bool) error`; `EnabledStore{IsEnabled(ctx, id string, def bool) (bool, error); SetEnabled(ctx, id string, on bool) error}`; `Registry.Problems() []string` lists refused plugins with reasons.
  - `plugintest`: `BookPlugin()` (kind `book`: fields author/pages/isbn, progress eligible `pages` (total from `pages`) and `percent`, default `[pages]`, combinations `[[pages],[percent]]`, authors as people) and `TVPlugin()` (kind `show`: progress `count` Season, `count` Episode (total from `episodes`), `duration` Position; default `[episode]`, combinations `[[episode],[season,episode],[episode,position]]`; actors as credits) plus `FakeSource`, `FakeImporter`, `MemEnabled` helpers.

- [ ] **Step 1: Write failing tests**: `TestManifestJSONRoundTrip` (a full manifest and every runtime data type survive `json.Marshal`/`Unmarshal` unchanged), `TestValidateAcceptsFixtures` (book and tv manifests valid), `TestValidateRejects` (table: duplicate field key, duplicate progress key, unknown progress type, `TotalField` missing, `TotalField` not a number field, enum without values, default not eligible, combination with an ineligible key, primary key of type percent, section field missing, a genres or tags section while that term type is not enabled, empty title languages, importer `ResolveVia` unknown, contract major 2), `TestRegisterRejectsReservedAndDuplicateIDs`, `TestRegistryEnabledPersistence` (default flag respected, `SetEnabled` persists through `EnabledStore`, disabled plugins absent from `Enabled()` but present in `Plugins()`), `TestRegistryKeepsGoodPluginsWhenOneIsRefused` (`Problems()` names the refused plugin and the reason), `TestProgressTypesAreClosedEnum`.
- [ ] **Step 2:** Run `scripts/go.sh test ./internal/plugin/...`. Expected: FAIL (package missing).
- [ ] **Step 3:** Implement the types and `Validate` with the exact names above; the registry keeps plugins in registration order.
- [ ] **Step 4:** Run the tests. Expected: PASS.
- [ ] **Step 5: Commit** `feat: plugin contract and registry`.

### Task 2: Generic schema and library: items, titles, fields, people, credits

**Files:**
- Create: `internal/db/migrations/00005_generic_core.sql`, `internal/library/library.go`, `internal/library/items.go`, `internal/library/fields.go`, `internal/library/credits.go`
- Test: `internal/library/items_test.go`, `internal/library/credits_test.go`, `internal/library/helpers_test.go`

**Interfaces:**
- Consumes: `plugin` types, `db.Open`.
- Produces: migration 00005 adds the generic tables next to the legacy ones (legacy tables untouched until Task 5): `items(id, plugin_id, kind, source, source_id, cover_path, external_url, sync_state, progress_keys TEXT JSON, created_at, updated_at, UNIQUE(plugin_id, kind, source, source_id))`, `item_titles(item_id, lang, text)`, `item_fields(item_id, key, text NULL, num REAL NULL, date TEXT NULL, seq)` with indexes on `(key, num)`, `(key, text)`, `(key, date)`, `external_ids(item_id, type, value)` indexed on `(type, value)`, `people_g(id, plugin_id, source, source_id, name, image_path, external_url, UNIQUE(plugin_id, source, source_id))`, `credits(item_id, seq, role, person_id, counterpart_person_id NULL, counterpart_role)`, `relations_g(item_id, seq, kind, source, source_id, kind_item, titles_json, fields_json)`, `item_terms(item_id, kind TEXT CHECK (kind IN ('genre','tag')), term TEXT, term_key TEXT, rank INTEGER NULL, spoiler INTEGER NOT NULL DEFAULT 0, seq, UNIQUE(item_id, kind, term_key))` indexed on `(kind, term_key)` where `term_key` is the lower-cased trimmed term; all child tables `ON DELETE CASCADE`. `library.New(db *sql.DB, reg *plugin.Registry) *Library`; `Item{ID int64; PluginID, Kind, Source, SourceID, CoverPath, ExternalURL, SyncState string; Titles []plugin.Title; Fields map[string]plugin.FieldValue; ExternalIDs map[string]string; Relations []plugin.Relation; ProgressKeys []string; Genres []string; Tags []plugin.Tag}`; `(*Library) SaveItem(ctx, pluginID, kind string, d plugin.ItemData, opts ...SaveOption) (int64, error)` (one transaction; `ErrAlreadySaved`; field values validated against the kind's `FieldDef` types and bounds, unknown field keys rejected with `ErrInvalid`; `WithSyncState(string)`; cover URL stored in `cover_path` per the existing "remote URL until downloaded" convention), `Item(ctx, id) (*Item, error)`, `ItemBySource(ctx, pluginID, kind, source, sourceID string) (*Item, error)`, `ItemByExternalID(ctx, pluginID, kind, idType, value string) (*Item, error)` (nil, nil when absent), `SaveCredits(ctx, itemID int64, credits []plugin.Credit) error` (delete then insert in one transaction, idempotent, people upserted by `(plugin_id, source, source_id)` keeping a local image path), `Credits(ctx, itemID int64) ([]SavedCredit, error)` (with local person ids and counterparts), `Person(ctx, id int64) (*Person, error)`, `ItemPeople(ctx, itemID int64) ([]Person, error)`, `SetCoverPath`, `SetPersonImagePath`, `SetSyncState`, `DeleteItem(ctx, id) error` (`ErrNoEntry` for unknown; cascades; people rows and files stay); sentinel errors `ErrAlreadySaved`, `ErrInvalid`, `ErrNoEntry`.

- [ ] **Step 1: Write failing tests** (temp-file SQLite per test, `plugintest` fixtures registered): `TestSaveItemRoundTripsAllParts` (titles in three languages, typed fields incl. list and date, external ids, relations), `TestSaveItemRejectsUnknownFieldAndBadType` (number out of `Min`/`Max`, enum value outside `EnumValues`, text for a number field), `TestSaveItemBlocksDuplicate`, `TestSameSourceIDDifferentPluginAllowed`, `TestItemByExternalID`, `TestSaveCreditsIsIdempotent`, `TestCreditsKeepCounterpart` (character voiced by actor; credit without counterpart for an author), `TestPeopleAreNamespacedPerPlugin` (same source id under two plugins gives two people), `TestSaveItemStoresGenresAndTags` (trimmed, case-insensitively de-duplicated, limits 60 characters / 50 genres / 100 tags enforced with `ErrInvalid`, a tag keeps rank 0 to 100 and the spoiler flag, terms of a kind that does not enable them are rejected), `TestSetTermsReplacesAtomically`, `TestSetPersonImagePathSurvivesResync`, `TestDeleteItemCascadesButKeepsPeople`, `TestSaveItemIsAtomicOnFailure` (trigger aborting a child insert leaves nothing), `TestFieldIndexesExist`.
- [ ] **Step 2:** Run `scripts/go.sh test ./internal/library/... ./internal/db/...`. Expected: FAIL.
- [ ] **Step 3:** Implement migration and library; reuse the existing patterns from `internal/store` (transactions, `AlreadySavedError` style) rather than inventing new ones.
- [ ] **Step 4:** Run the tests plus `scripts/go.sh test ./...`. Expected: PASS (legacy code untouched).
- [ ] **Step 5: Commit** `feat: generic library schema for items, people and credits`.

### Task 3: Library: entries, progress, list/query engine

**Files:**
- Create: `internal/library/entries.go`, `internal/library/progress.go`, `internal/library/query.go`, `internal/library/appearances.go`
- Modify: `internal/db/migrations/00005_generic_core.sql` is NOT edited; add `internal/db/migrations/00006_entries.sql` with `entries(item_id PK, status CHECK in the five classes, rating NULL CHECK 0..10, comment, started_on, finished_on, updated_at)` and `entry_progress(item_id, key, value REAL, PRIMARY KEY(item_id, key))` (cascading)
- Test: `internal/library/entries_test.go`, `internal/library/progress_test.go`, `internal/library/query_test.go`, `internal/library/appearances_test.go`

**Interfaces:**
- Consumes: Task 2.
- Produces: `Status` constants `Planned`, `Active`, `Paused`, `Completed`, `Dropped`; `Entry{ItemID int64; Status Status; Rating *int; Comment string; StartedOn, FinishedOn *time.Time; Progress map[string]float64; UpdatedAt time.Time}`; `(*Library) ValidateEntry(kind plugin.KindDef, item *Item, e Entry) error` (`ErrInvalid` with readable message: rating outside 0..10, unknown status, finish before start by calendar day, progress key not in the item's active keys, count/pages/percent not whole or negative, percent above 100, duration negative, bounded type above its total when the total field is set); `AddToList(ctx, pluginID, kind string, d plugin.ItemData, e Entry, opts ...SaveOption) (int64, error)` (validate first, then ONE transaction saving item + entry; rollback tested), `UpsertEntry(ctx, e Entry) error`, `Entry(ctx, itemID) (*Entry, error)`, `RemoveItem` = `DeleteItem`, `Increment(ctx, itemID int64) error` (single atomic UPDATE on the primary count/pages key, capped at a known total, `ErrNoEntry` when no entry, no-op when no primary key), `SetActiveProgress(ctx, itemID int64, keys []string) error` (must be one of the kind's allowed combinations; values of keys that become inactive are retained), `Finished(kind, item, entry) / NotStarted(...)` helpers implementing the global rules; `Query{PluginID, Kind string; Text string; Status *Status; Filters []Filter; Genres, Tags []string; GenresAll, TagsAll bool; RatingMin, RatingMax *int; Unrated, RatingZero bool; Unmatched, PartialSync bool; Sort Sort; Page, PageSize int}`, `Filter{Key string; Op FilterOp; Values []string}` with ops `eq`, `in`, `range`, `contains`; `Sort` enum `SortUpdated` (default), `SortTitle`, `SortRating`, `SortYear` (any numeric/date field marked `Sortable`: `SortField(key)`), `SortProgress`; `(*Library) List(ctx, q Query, lang string) (Page, error)` where `Page{Rows []Row; Total, PageNo, Pages int}` and `Row{Item; Entry; Flags}` titles resolved by the caller's language, ordering deterministic (`updated_at DESC, id DESC`); `(*Library) Counts(ctx, q Query) (map[Status]int, error)` (same filters without the status); `(*Library) Appearances(ctx, personID int64) ([]Appearance, error)`; `(*Library) SetTerms(ctx, itemID int64, genres []string, tags []plugin.Tag) error` (one transaction, same validation as `SaveItem`); `(*Library) Terms(ctx, q Query, kind string) ([]TermCount, error)` with `TermCount{Term string; Count int}` counting items of the user's library matching the query WITHOUT that term filter (for the filter bar); genre/tag filters match case-insensitively on `term_key`, `any` = at least one selected term, `all` = every selected term, spoiler tags filter like any other tag; text search: substring over ALL stored titles (any language), `%`, `_` and `\` escaped, query capped at 200 runes; filter keys and sort names validated against the kind's schema (`Filterable`/`Sortable`) and rejected otherwise; "unmatched" = manual item carrying an external id of a type declared by an importer of its plugin; "partial sync" = `sync_state != 'done'`.

- [ ] **Step 1: Write failing tests**: `TestValidateEntryRules` (table incl. rating -1/0/10/11, percent 101, duration -1, count 2.5, progress above total, key not active, finish before start across time zones), `TestAddToListRollsBackWhenEntryInsertFails` (trigger-based, real rollback), `TestIncrementCapsAtTotalAndNoPrimary`, `TestSetActiveProgressRetainsInactiveValues`, `TestSetActiveProgressRejectsIneligibleCombination`, `TestFinishedAndNotStartedRules` (book pages vs percent, tv episode + position), `TestQueryTextSearchAllLanguages`, `TestQueryTextEscapesWildcards` (`%`, `_`, `\`, quotes), `TestQueryFilterTypedRanges` (year range, studio contains, enum eq), `TestQueryRejectsUnknownFilterAndSort`, `TestQueryGenresAndTagsAnyAll` (any vs all, case-insensitive, combined with text and status), `TestQueryTermsHostileInputIsBound` (`%`, `_`, quotes, 10,000 characters), `TestTermsCountsReflectOtherFilters`, `TestQueryRatingUnratedAndZero`, `TestQueryFlagsUnmatchedAndPartial`, `TestQuerySortsAndPagination` (101 items, stable order, last-page clamp), `TestCountsIgnoreStatusButKeepFilters`, `TestQueryIsPluginScoped` (a second plugin's items never appear), `TestAppearancesCharacterAndActor`.
- [ ] **Step 2:** Run `scripts/go.sh test ./internal/library/...`. Expected: FAIL.
- [ ] **Step 3:** Implement; compile filters to parameterised SQL joining `item_fields`, never interpolating keys (map schema keys to bound parameters).
- [ ] **Step 4:** Run the tests plus the full suite. Expected: PASS.
- [ ] **Step 5: Commit** `feat: library entries, progress and query engine`.

### Task 4: Anime plugin (manifest, source, importer, exporter)

**Files:**
- Create: `internal/plugins/anime/plugin.go`, `internal/plugins/anime/manifest.go`, `internal/plugins/anime/source.go`, `internal/plugins/anime/importer.go`, `internal/plugins/anime/exporter.go`
- Test: `internal/plugins/anime/manifest_test.go`, `internal/plugins/anime/source_test.go`, `internal/plugins/anime/importer_test.go`, `internal/plugins/anime/exporter_test.go`

**Interfaces:**
- Consumes: `plugin` contract; the existing `internal/provider/anilist` client and `internal/importer/yamtrack` parser (wrapped, not moved yet).
- Produces: `anime.New(opts Options) plugin.Plugin` (`Options{AniListURL string; HTTP *http.Client}`); manifest id `anime`, kind `anime` ("Anime"), `DefaultEnabled: true`, title languages `ja`, `ja-latn`, `en`; genres and tags enabled (`Genres{Enabled, Label "Genres", Editable, Filterable}`, `Tags{Enabled, Label "Tags", Editable, Filterable}`) and sections for them; fields `type` (enum TV, TV_SHORT, MOVIE, SPECIAL, OVA, ONA, MUSIC; filterable, editable), `episodes` (number 1..10000, editable), `year` (number 1900..2100, filterable, sortable), `studio` (text, filterable, editable), `synopsis` (text, editable), `source_material` (text), `started_on`, `ended_on` (date); statuses labelled Watching/Plan to watch/Paused/Completed/Dropped; progress eligible `episode` (count, total field `episodes`), default `[episode]`, combinations `[[episode]]`, primary `episode`; sections: info (type, episodes, aired, studio, source), text (synopsis), titles, related, people (roles character with counterpart "Voice actor"), recommendations, links; source `anilist` (30 requests/minute, external id type `mal`, supports lookup, link label "View on AniList", URL pattern `anilist.co/anime/{id}`) mapping the existing client results into `plugin.ItemData`/`Credit` (character credits carry the Japanese voice actor as `With`; source relations kept; image URLs: covers `large`, people `medium`); importer `yamtrack` (accepts `.csv`, external id type `mal`, resolves via source `anilist`, fallback manual with title language `ja-latn`) converting `yamtrack.Parse` rows into `plugin.ParsedEntry` (status classes mapped, progress key `episode`, notes preserved); exporter `json` writing entries as JSON.

- [ ] **Step 1: Write failing tests**: `TestManifestIsValid` (passes `plugin.Validate`), `TestManifestMatchesBaseline` (statuses labels, fields, progress, sections as listed), `TestSourceSearchMapsToSummaries`, `TestSourceDetailsMapsFieldsTitlesRelations`, `TestSourceDetailsMapsGenresAndTagsWithRankAndSpoiler` (the AniList query also requests `genres` and `tags { name rank isMediaSpoiler }`; update the recorded fixture), `TestSourcePeopleCarriesVoiceActorAsCounterpart`, `TestSourceLookupByMALID` and `TestSourceLookupNotFound`, `TestSourceUsesMediumPersonImages`, `TestImporterMapsRows` (every status, score/date rules and notes exactly as the current Yamtrack tests assert, progress under key `episode`), `TestExporterRoundTripsJSON`, `TestPluginRegistersInRegistry`. Reuse the existing AniList fixtures.
- [ ] **Step 2:** Run `scripts/go.sh test ./internal/plugins/...`. Expected: FAIL.
- [ ] **Step 3:** Implement as thin wrappers; no behaviour change in the wrapped packages (the `medium` image size switch is done in Task 11 inside the client; here the wrapper requests the field through the client option added in Task 11, so only assert the contract: `ImageURL` is whatever the client returns).
- [ ] **Step 4:** Run the tests plus the full suite. Expected: PASS.
- [ ] **Step 5: Commit** `feat: anime plugin on the plugin contract`.

### Task 5: Store adapter over the library (baseline schema replacement)

**Files:**
- Create: `internal/db/migrations/00007_drop_legacy_tables.sql`
- Modify: `internal/store/*.go` (reimplemented on `library`; exported API and types unchanged), `cmd/allister/main.go` (construct registry, library, anime plugin; pass to store), `internal/web` test helpers only if they construct the store
- Test: all existing `internal/store/*_test.go`, `internal/web/*_test.go`, `internal/jobs/*_test.go`, `internal/importer/...` must pass unchanged (except where a status value string or table name is asserted: adapt, do not weaken); add `internal/store/adapter_test.go`

**Interfaces:**
- Consumes: Tasks 1 to 4.
- Produces: `store.New(db *sql.DB) *Store` keeps working; internally it uses `library` with plugin `anime`, kind `anime`; the legacy status strings `watching`, `plan_to_watch` are mapped to the core classes at the adapter boundary only (the database stores `active`, `planned`); legacy tables (`anime`, `anime_titles`, `anime_relations`, `anime_cast`, `people`, `list_entries`) are dropped by migration 00007 (the owner has no data; the app starts cleanly on an empty volume and on a volume created by this iteration); the `jobs`, `settings`, `imports`, `import_rows` tables remain; `anime.mal_id` becomes external id type `mal`; "unmatched" keeps its meaning; `sync_state` moves to `items.sync_state`; the exposed `store.Saved`/`ListRow`/`PersonAppearance`/`Person` types and every method keep their signatures.

- [ ] **Step 1: Write failing tests**: `TestAdapterKeepsLegacyStatusStrings`, `TestAdapterStoresGenericRows` (after `SaveAnime`/`AddToList` the rows are in `items`, `item_titles`, `item_fields`, `entries`, `entry_progress`, none in legacy tables), `TestLegacyTablesDropped`, `TestMigrationsApplyOnEmptyAndExistingVolume` (apply 00001 to 00007 on a fresh file; reopen a file already at 00006), `TestAdapterMalIDIsExternalID`. Capture the failing run of the whole existing suite after the migration and before the adapter rewrite.
- [ ] **Step 2:** Run `scripts/go.sh test ./...`. Expected: FAIL (legacy tables gone).
- [ ] **Step 3:** Rewrite `internal/store` as an adapter: mapping functions between legacy types and `plugin`/`library` types in one file (`adapter.go`), each exported method a thin call; no business rules duplicated (validation delegates to `library`).
- [ ] **Step 4:** Run the whole suite (`scripts/go.sh test ./...`), vet, fmt. Expected: PASS with no weakened assertions (report each adapted assertion).
- [ ] **Step 5: Commit** `refactor: store becomes an adapter over the generic library`.

### Task 6: Plugin-aware web shell: routing, navigation, enable/disable

**Files:**
- Create: `internal/web/plugins.go`, `internal/web/templates/settings_plugins.html`
- Modify: `internal/web/server.go`, `internal/web/templates/base.html`, `internal/web/settings.go`, `internal/web/templates/settings.html`, `cmd/allister/main.go`, `internal/jobs/jobs.go` (skip jobs of disabled plugins)
- Test: `internal/web/plugins_test.go`, `internal/jobs/disabled_test.go`

**Interfaces:**
- Consumes: `plugin.Registry`.
- Produces: `Deps.Registry *plugin.Registry`; at startup the server registers every INSTALLED plugin's routes under literal `/{plugin-id}/...` (no wildcard first segment, so no ServeMux conflicts; the existing anime handlers are mounted via a per-plugin handler set resolved from the registry instead of hard-coded anime), a per-request `enabled` check makes a disabled plugin's routes return the 404 page (GET and POST), navigation in `base.html` lists enabled plugins' `LabelPlural` linking to `/{plugin-id}`; Settings gets a "Plugins" section (list with name, version, source/importer/exporter names, enabled toggle `POST /settings/plugins/{id}` with CSRF middleware in force; refused plugins from `Registry.Problems()` shown with reasons); `settings.Store` implements `plugin.EnabledStore` (key `plugin.<id>.enabled`); queue claims skip `sync_anime` and `import` jobs whose plugin is disabled and resume when re-enabled (newly enqueued sync and import jobs carry `plugin_id` in their payload and the queue reads `$.plugin_id`; jobs without it are never blocked; Task 11 rewrites any legacy rows by migration); `/` redirects to the first enabled plugin's list (or `/settings` when none is enabled).

- [ ] **Step 1: Write failing tests**: `TestDisabledPluginRoutes404` (every anime route GET and POST returns 404 when disabled, nothing mutated), `TestEnablingRestoresRoutes`, `TestNavShowsOnlyEnabledPlugins`, `TestSettingsTogglePersistsAndIsCSRFProtected`, `TestSettingsListsRefusedPluginsWithReason`, `TestDisabledPluginJobsPauseAndResume` (queue), `TestRootRedirectsToFirstEnabledOrSettings`, `TestReservedIDsNeverReachServeMux` (registering a plugin with id `list` fails and the server still starts), `TestAllExistingAnimeURLsStillWork` (a table of today's anime URLs).
- [ ] **Step 2:** Run `scripts/go.sh test ./internal/web/... ./internal/jobs/...`. Expected: FAIL.
- [ ] **Step 3:** Implement; keep `refuseCrossOrigin`, `Vary`, per-page template sets; handlers still call the adapter store at this point.
- [ ] **Step 4:** Run the full suite. Expected: PASS.
- [ ] **Step 5: Commit** `feat: plugin-aware routing, navigation and enable/disable`.

### Task 7: Generic item overview and people pages (building blocks)

**Files:**
- Create: `internal/web/item.go`, `internal/web/templates/item.html`, `internal/web/templates/sections.html` (one template per building block), `internal/web/templates/person.html` (generic)
- Modify: `internal/web/anime.go` (overview part removed), `internal/web/people.go`, `internal/web/templates/anime.html` (deleted when unused)
- Test: `internal/web/item_test.go` (moved/adapted from `anime_test.go`), `internal/web/people_test.go`

**Interfaces:**
- Consumes: `library`, `plugin.Source`, the registry.
- Produces: `GET /{plugin}/{source}/{id}` renders any saved or live item from the kind's `Sections` list using only the building blocks (info list from `Show` fields with typed formatting, text, titles with language labels, related with internal link when the relation's plugin/source is known else external, people with roles and counterparts ("as ...", "voiced by ..."), genre chips, tag chips with spoiler tags collapsed behind a native "Show spoiler tags" control (no JavaScript needed), recommendation cards, links); live (unsaved) data comes from `Source.Details`/`People` with the existing 10-minute overview cache; saved items from the library; manual items from the library only; the page shows progress per active type, status label from the plugin's `StatusLabels`, the partial-sync badge, the "Match to source" and "Edit details" buttons (targets in later tasks); `GET /people/{id}` shows a person with their credits across items of THEIR plugin (a person of a disabled plugin is 404); every external link only when https, images via `covers.Src`; no `template.HTML`.

- [ ] **Step 1: Write failing tests**: the existing overview tests ported (`TestOverviewLiveForUnsaved`, `TestOverviewFromDatabaseForSaved`, `TestOverviewShowsAllTitleLanguages`, `TestOverviewSynopsisHasNoRawHTML`, `TestPersonLinksExternalOnlyWhenUnsaved`, people page tests incl. "as <character>") plus `TestOverviewRendersOnlyDeclaredSections` (book fixture shows authors and no cast; tv fixture shows episode progress), `TestOverviewUnknownPluginOrSource404`, `TestPersonPageOfDisabledPlugin404`, `TestGenreAndTagChipsRender` (tags show rank, spoiler tags hidden until expanded, escaped), `TestSectionsEscapeHostileValues`, `TestOverviewShowsProgressPerActiveType`.
- [ ] **Step 2:** Run `scripts/go.sh test ./internal/web/...`. Expected: FAIL.
- [ ] **Step 3:** Implement; templates take plain view structs, never plugin-supplied markup.
- [ ] **Step 4:** Run the full suite. Expected: PASS.
- [ ] **Step 5: Commit** `feat: schema-driven item overview and people pages`.

### Task 8: Generic search and add-to-list flow

**Files:**
- Create: `internal/web/add.go`, `internal/web/templates/progress_inputs.html`
- Modify: `internal/web/search.go`, `internal/web/anime.go` (add flow removed), `internal/web/form.go`, `internal/web/templates/add_dialog.html`, `internal/web/templates/search_results.html`
- Test: `internal/web/search_test.go`, `internal/web/add_test.go`

**Interfaces:**
- Consumes: Tasks 3, 6, 7.
- Produces: `GET /{plugin}/search?q=` uses the plugin's (single, default) source via `Source.Search` (a plugin with several sources shows a source switch; anime has one); `GET|POST /{plugin}/{source}/{id}/add` builds the dialog from the kind's progress spec (one input per active type of the default combination: number for count/pages with `/ total`, 0 to 100 for percent, `hh:mm:ss` for duration stored as seconds), status select with the plugin's labels (the five classes), rating (blank, 0..10), dates, comment; validation via `library.ValidateEntry` BEFORE saving; `library.AddToList` in one transaction; enqueue the sync job with the plugin id; success `HX-Redirect`/303 as today; the shared `parseEntryForm` becomes `parseEntryForm(kind plugin.KindDef, ...)` and stays the single parser for add, edit and manual.

- [ ] **Step 1: Write failing tests**: existing search and add tests ported (`TestSearchUsesPluginSource`, `TestSearchEmptyQueryMakesNoRequest`, `TestSearchProviderRateLimitedShowsMessage`, `TestAddCreatesEntryAndJob`, `TestAddRejectsInvalidRating`, `TestAddDuplicateIsBlocked`, `TestAddWithProviderDownUsesAlreadyFetchedDetails`, rating blank/0, progress above total 422 nothing saved) plus `TestAddDialogShowsDefaultProgressInputs` (book: pages; tv: episode), `TestAddParsesDurationInput` (`1:02:03` = 3723 s; `-1`, `99:99:99` rejected), `TestAddPercentOver100Rejected`, `TestAddRejectsProgressKeyNotActive`.
- [ ] **Step 2:** Run the web tests. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run the full suite. Expected: PASS.
- [ ] **Step 5: Commit** `feat: generic search and add flow with typed progress`.

### Task 9: Generic list page, quick progress and entry editing

**Files:**
- Create: `internal/web/entries.go`, `internal/web/templates/entry_dialog.html`
- Modify: `internal/web/list.go`, `internal/web/templates/list.html`, `internal/web/templates/list_row.html`, `internal/web/templates/list_rows` partials
- Test: `internal/web/list_test.go`, `internal/web/entries_test.go`

**Interfaces:**
- Consumes: Task 3 query engine, Task 6 routing.
- Produces: `GET /{plugin}` (the list, 100 per page, tabs per status class with the plugin's labels and counts from `library.Counts`, current behaviour incl. `?page=` clamping and `return` handling); rows show cover, resolved title linking to the overview, type from the schema's first `Show` enum/text field, progress per active type (count/pages "N / total", percent "N%", duration `h:mm:ss`), score ("–" unrated, 0 shown), status label, partial-sync and "unmatched" badges, actions; `POST /list/{id}/increment` (htmx row; hidden when the plugin has no primary type); `GET|POST /list/{id}/edit` edits the ENTRY (status, rating, dates, comment, progress values, and the active progress combination only among the plugin's allowed ones); `POST /list/{id}/remove` as before (deletes the item); all keep `safeReturn`, `Vary`, 422/409 swaps.

- [ ] **Step 1: Write failing tests**: existing list tests ported with the same assertions (`TestListFiltersByStatus`, `TestListShowsPreferredTitle`, `TestIncrementReturnsUpdatedRow`, `TestIncrementAtTotalDoesNotExceed`, `TestEditValidationReturns422`, `TestRemoveDeletesEntry`, `TestEmptyListShowsHint`, `TestListShowsUnmatchedBadge`, pagination 101 rows, `safeReturn` table, page clamping) plus `TestListShowsPluginStatusLabels` (book plugin says Reading), `TestListRowRendersEachProgressType`, `TestIncrementHiddenWithoutPrimaryType`, `TestEditChangesActiveCombinationOnlyWithinAllowed`, `TestListScopedToPlugin`.
- [ ] **Step 2:** Run the web tests. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run the full suite. Expected: PASS.
- [ ] **Step 5: Commit** `feat: generic list page and entry editing`.

### Task 10: Manual create, Edit details, Match to source, return to overview

**Files:**
- Create: `internal/web/details.go`, `internal/web/templates/details_form.html`
- Modify: `internal/web/manual.go`, `internal/web/match.go`, `internal/web/templates/manual_form.html`, `internal/web/templates/match_dialog.html`, `internal/web/templates/item.html`
- Test: `internal/web/manual_test.go`, `internal/web/details_test.go`, `internal/web/match_test.go`

**Interfaces:**
- Consumes: `library` (add `(*Library) UpdateDetails(ctx, itemID int64, u DetailsUpdate) error` and `ConvertToSource(ctx, itemID int64, d plugin.ItemData) error` with the existing rules: same item id, entry untouched, progress clamped to a new total only when exceeded, `AlreadySavedError{ExistingID}`), Tasks 7 to 9.
- Produces: `GET|POST /{plugin}/manual/new` (form generated from the kind's `Editable` fields, title language select, type validated against enum values, cover upload with the existing sniffing/size rules via `covers.Save`) plus entry fields; `GET|POST /{plugin}/{source}/{id}/edit` for EVERY saved item (manual, unmatched, matched, sourced): titles per language (add/remove/rename within the plugin's `TitleLanguages` plus any stored), every `Editable` field validated by type/bounds, cover upload/replace/remove, genres and tags (add/remove terms, spoiler flag and rank for tags; the same limits as storage) when the kind enables them as editable, the item's active progress combination among the allowed ones; edits are local and never overwritten except by an explicit match; after saving an edit that started from the overview the user returns to that overview (`return` param limited by `safeReturn`); `GET|POST /{plugin}/{source}/{id}/match` for manual items using `Source.Search` and `Source.Lookup` with a URL/id field parsed by a per-source parser the source exposes through the manifest as data (`SourceInfo.URLPatterns []string` of the form `anilist.co/anime/{id}`; the core owns the matching so no plugin code parses URLs: replace the injected `ParseAnimeURL` with this declarative matcher and move its table tests); ids are digits-only unless the pattern says otherwise.

- [ ] **Step 1: Write failing tests**: existing manual and match tests ported (`TestManualRequiresTitleAndType`, cover rejection cases, `TestMatchByURLConverts`, `TestMatchRefusesAlreadySavedAnime`, atomic conversion, `TestConvertClampsProgressToNewTotal`) plus `TestEditDetailsForManualUnmatchedAndSourcedItems`, `TestEditDetailsValidatesTypesAndBounds`, `TestEditDetailsSetsGenresAndTags` (limits, duplicates, hostile text escaped), `TestManualFormOffersGenresAndTags`, `TestEditDetailsRenamesTitleAndSearchSeesIt` (list search finds the new title, not the old), `TestEditDetailsReplacesCover`, `TestEditReturnsToOverview` (hostile `return` values rejected), `TestEditDoesNotTouchEntryData`, `TestURLPatternMatcher` (table incl. the hostile cases of the old `ParseAnimeURL` test), `TestMatchUsesSourceLookup`.
- [ ] **Step 2:** Run the web and library tests. Expected: FAIL.
- [ ] **Step 3:** Implement; delete `anilist.ParseAnimeURL` use from the web layer (the function may stay in the anime plugin until Task 13).
- [ ] **Step 4:** Run the full suite. Expected: PASS.
- [ ] **Step 5: Commit** `feat: edit details for every item, schema-driven manual entries and source matching`.

### Task 11: Sync jobs via plugin sources, image size policy, late-loading recommendations

**Files:**
- Create: `internal/web/recommendations.go`, `internal/web/templates/recommendations.html`, `internal/db/migrations/00009_rewrite_sync_jobs.sql`
- Modify: `internal/jobs/sync_anime.go` (becomes `sync_item.go`, kind `sync_item`), `internal/covers/covers.go`, `internal/plugins/anime/source.go`, `internal/provider/anilist/client.go`, `internal/web/item.go`, `README.md`
- Test: `internal/jobs/sync_item_test.go`, `internal/covers/covers_test.go`, `internal/web/recommendations_test.go`, `internal/provider/anilist/client_test.go`

**Interfaces:**
- Consumes: Tasks 2, 4, 7.
- Produces: `jobs.SyncItem(deps SyncDeps) jobs.Handler` payload `{"item_id": int64, "plugin_id": string}` (a new migration `00009_rewrite_sync_jobs.sql` rewrites any queued legacy `sync_anime` jobs to this kind and payload, so no legacy handling stays in code): cover download, `Source.People`, `library.SaveCredits`, person photos, same partial/`ErrRetryLater` semantics and startup sweep as today (`NeedsSync` generic); AniList client requests `medium` for person photos (`image { medium }`) and `large` for covers; `covers.Download`/`Save` default limit 2 MiB (constant `MaxImageBytes`); the item overview no longer fetches recommendations inline: it renders a placeholder that loads `GET /{plugin}/{source}/{id}/recommendations` with `hx-trigger="load"`, the endpoint calls `Source.Recommendations` with a 4 s timeout, caches results (10 minutes, bounded) and returns the cards partial; failure or timeout returns an empty-state fragment with a retry link and HTTP 200; README disk-use paragraph corrected with the measured numbers (cover about 40 KB at the larger size, person photo about 21 KB at `medium` versus 87 KB at `large`, about 52 people per anime, roughly 1 MB of person photos per anime at the smaller size; shared people are downloaded once; files of removed items are not deleted).

- [ ] **Step 1: Write failing tests**: `TestSyncItemCompletesAndStoresCredits`, `TestMigrationRewritesLegacySyncJobs`, `TestSyncItemRateLimitUsesRetryLater`, `TestSweepEnqueuesSyncItemForPendingItems`, `TestDownloadRejectsOver2MiB`, `TestAniListRequestsMediumForPeopleAndLargeForCovers` (request body assertion), `TestOverviewRendersWithoutCallingRecommendations` (provider call count zero on page render), `TestRecommendationsEndpointCachesAndTimesOut` (fake clock, slow source returns the fallback fragment within the timeout), `TestRecommendationsFailureIsNon500`, `TestRecommendationsForDisabledPlugin404`.
- [ ] **Step 2:** Run the covered packages. Expected: FAIL.
- [ ] **Step 3:** Implement; do not change pacing (2 s minimum interval, 30 requests/minute).
- [ ] **Step 4:** Run the full suite. Expected: PASS.
- [ ] **Step 5: Commit** `feat: generic sync job, 2 MiB image limit, smaller person photos, late recommendations`.

### Task 12: Generic import/export runner and universal backup

**Files:**
- Create: `internal/importer/runner.go`, `internal/importer/report.go` (moved/generalised from `internal/importer/yamtrack/import.go` and `report.go`), `internal/library/backup.go`, `internal/web/backup.go`, `internal/web/templates/backup.html`
- Modify: `internal/web/import.go`, `internal/web/templates/import.html`, `internal/db/migrations/00008_imports_plugin.sql` (adds `plugin_id`, `importer_id` to `imports`), `internal/plugins/anime/importer.go`, `internal/importer/yamtrack/*` (parsing stays, runner logic leaves)
- Test: `internal/importer/runner_test.go`, `internal/library/backup_test.go`, `internal/web/import_test.go`, `internal/web/backup_test.go`

**Interfaces:**
- Consumes: Tasks 1 to 4, 6, 11.
- Produces: `GET|POST /settings/import` becomes plugin/importer aware (a select of enabled plugins' importers; anime shows Yamtrack); `importer.Runner` implements the existing behaviour generically from `ImporterInfo`: the importer's `Parse` returns `ParsedEntry` rows, the runner resolves each row through `ResolveVia` source `Lookup(idType, value)` paced by the SOURCE's `RequestsPerMinute`, skips rows already saved (`ItemByExternalID`), creates entries via `library.AddToList`, falls back to a manual item (title in `FallbackTitleLang`, external id kept, remote cover URL kept) when `ErrNotFound`, applies the same adjustments (progress clamped to the total with a note, dates dropped with a note if finish precedes start), the one-active-import rule, reconcile/resume/cancel, outcome-per-row persistence, enqueues `sync_item`; exporters: `GET /{plugin}/export/{exporter}` streams `Exporter.Export` for the plugin's entries; universal backup: `GET /settings/backup` downloads a versioned `.zip` (`backup.json` with `format_version: 1`, items with titles/fields/external ids/relations/credits/people/genres/tags, entries with progress, plus `covers/` files only for covers uploaded by the user, i.e. not remote-sourced), `POST /settings/backup` restores by MERGING (skip same plugin+source+source id; nothing overwritten or deleted; entries only for newly created items), refusing a newer major `format_version`, malformed JSON, entries naming unknown plugins/kinds/fields (reported, not fatal), names with `..` or absolute paths in the archive (zip-slip), files that are not images (sniffed), archives over 512 MiB; disabled plugins' data included in backups.

- [ ] **Step 1: Write failing tests**: the existing import tests ported to the generic runner (`TestImportSavesEntriesAndEnqueuesSync`, `TestImportTwiceCreatesNoDuplicates`, `TestImportResumesAfterInterruption`, `TestImportSkipsAlreadySavedByExternalID`, `TestImportSavesUnmatchedAsManualEntry`, `TestImportPacesBySourceLimit`, reconcile/resume/cancel, one active import) plus `TestRunnerWorksForBookFixture` (a fixture importer with a different external id type and a 5 requests/minute source limit), `TestExportJSONViaRoute`, `TestBackupRoundTripsAllPluginsIncludingDisabled`, `TestBackupRestoreMergesAndIsIdempotent`, `TestBackupRefusesNewerFormat`, `TestBackupRejectsZipSlipAndNonImages`, `TestBackupRejectsOversizedArchive`, `TestBackupReportsUnknownPluginRowsWithoutFailing`, `TestBackupIncludesOnlyUserUploadedCovers`, `TestBackupRoundTripsGenresAndTags`.
- [ ] **Step 2:** Run the covered packages. Expected: FAIL.
- [ ] **Step 3:** Implement; reuse the resume/reconcile logic as-is rather than rewriting it.
- [ ] **Step 4:** Run the full suite. Expected: PASS.
- [ ] **Step 5: Commit** (two or three commits are fine) `feat: generic import runner, exporters and universal backup`.

### Task 13: Remove legacy code and guard the core

**Files:**
- Delete: `internal/store/` (adapter), `internal/provider/provider.go` types superseded by `plugin`, legacy handlers/templates left behind (`anime.go`, `anime.html`, etc. if still present)
- Move: `internal/provider/anilist/` → `internal/plugins/anime/anilist/` (package kept), `internal/importer/yamtrack/` parser → `internal/plugins/anime/yamtrack/`
- Create: `internal/guard/guard_test.go`
- Test: the whole suite

**Interfaces:**
- Consumes: Tasks 5 to 12 (nothing may still import `internal/store` or `internal/provider`).
- Produces: a guard test that scans the production Go sources and templates (not `_test.go` files and not `internal/plugin/plugintest`) of the core packages (`internal/plugin`, `internal/library`, `internal/web`, `internal/jobs`, `internal/covers`, `internal/importer`, `internal/settings`, `internal/db`, `cmd`) and fails on any of the words `anime`, `anilist`, `episode`, `yamtrack`, `mal_id` (case-insensitive) outside an allow-list of exactly the files that must name the default plugin (`cmd/allister/main.go` registration line, migration comments, README), and a test that the core packages do not import `internal/plugins/...` except `cmd/allister`; core tests use `plugintest` fixtures; the anime-specific tests live in `internal/plugins/anime`.

- [ ] **Step 1: Write failing tests**: `TestCoreHasNoMediaSpecificWords`, `TestCoreDoesNotImportPlugins`, `TestNoLegacyPackagesRemain`.
- [ ] **Step 2:** Run `scripts/go.sh test ./internal/guard/...`. Expected: FAIL until the legacy code and words are gone.
- [ ] **Step 3:** Delete and move; fix imports; rename any leftover anime-named identifiers, strings and template text in core packages into generic ones (labels come from the plugin).
- [ ] **Step 4:** Run the full suite, vet, fmt. Expected: PASS; report every deleted file and the allow-list.
- [ ] **Step 5: Commit** `refactor: remove legacy anime code from the core`.

### Task 14: Search and filters on the list

**Files:**
- Create: `internal/web/filters.go`, `internal/web/templates/filter_bar.html`
- Modify: `internal/web/list.go`, `internal/web/templates/list.html`, `internal/web/templates/list_rows`
- Test: `internal/web/filters_test.go`

**Interfaces:**
- Consumes: Task 3 `Query` engine, Task 9 list.
- Produces: the list accepts `q` (text), `status`, `f.<fieldkey>` (per filterable field: enum `f.type=TV`, number/date ranges `f.year=2000..2010`, text contains `f.studio=Madhouse`), `genre` and `tag` (repeatable) with `genre_mode`/`tag_mode` = `any`|`all`, `rmin`/`rmax`, `unrated=1`, `zero=1`, `unmatched=1`, `partial=1`, `sort` (`updated`, `title`, `rating`, `progress`, or a `Sortable` field key) with `dir` asc/desc, `page`; the filter bar is generated from the kind's schema (only `Filterable` fields appear; genres and tags appear as multi-select lists with counts from `Terms` when the kind enables them as filterable, with an any/all switch), updates the rows live with htmx (`hx-trigger="keyup changed delay:300ms, change"`, `hx-push-url="true"`, `isPartialRequest` returns the rows region), works without JavaScript through a plain GET form, keeps filters across pagination and tab links, has a "Clear filters" link, shows "Showing X–Y of Z", handles unknown or malformed parameters by ignoring them with a visible notice for rejected keys (never passing them to SQL), caps `q` at 200 runes and each value at 200 characters.

- [ ] **Step 1: Write failing tests**: `TestFilterByTypeYearRangeStudio`, `TestFilterByGenresAnyAndAll`, `TestFilterByTagsWithCounts`, `TestGenreTagFilterHostileValuesIgnoredOrBound`, `TestFilterByStatusTabAndText`, `TestSearchMatchesAnyLanguageTitle`, `TestSearchEscapesWildcardsAndHostileInput` (`%`, `_`, quotes, 10,000 chars, SQL fragments in keys), `TestRatingFiltersUnratedAndZeroSeparately`, `TestFlagsUnmatchedAndPartial`, `TestSortOptionsAndDirection`, `TestFiltersSurvivePaginationLinks`, `TestPartialResponseHasOnlyRows`, `TestUnknownFilterKeyIgnoredWithNotice`, `TestFilterBarOnlyShowsFilterableFields` (book fixture differs from anime), `TestClearFiltersLink`.
- [ ] **Step 2:** Run the web tests. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run the full suite. Expected: PASS.
- [ ] **Step 5: Commit** `feat: search and filters on the list`.

### Task 15: Pin and checksum everything

**Files:**
- Modify: `Dockerfile`, `docker-compose.yml`, `go.mod`, `scripts/go.sh`, `README.md`, `.dockerignore`
- Create: `scripts/update-pins.sh`, `PINS.md` (the pinned references and their checksums), `internal/web/static/htmx.min.js.sha256`
- Test: `internal/web/static_test.go` (checksum test), `scripts/pins_test.go` or a Go test in `internal/guard` that checks the Dockerfile

**Interfaces:**
- Produces: every `FROM` in the Dockerfile pinned by `@sha256:` digest (golang, distroless, and any other stage image) with the tag kept as a comment; the Tailwind standalone CLI downloaded with a per-architecture `sha256sum -c` check against values recorded in the Dockerfile `ARG`s; `go.mod` `go` line and a `toolchain` line pinned to the exact patch version used by `scripts/go.sh` (which uses the pinned golang image digest); the vendored htmx file verified by a test against `htmx.min.js.sha256` and recorded with its version; README helper images pinned by digest (`alpine`, `keinos/sqlite3`); `scripts/update-pins.sh` resolves current digests/checksums and rewrites the pins (it only prints a diff unless `--write` is passed); the README states that base-image security updates now require running that script. Verification: `docker build` succeeds from the pinned references (run once with the project's own compose project name, tearing down only that project), and the checksum steps fail on a deliberately wrong checksum (shown once, then restored).

- [ ] **Step 1: Write failing tests**: `TestDockerfilePinsEveryFromByDigest`, `TestDockerfileVerifiesTailwindChecksum`, `TestHtmxChecksumMatches`, `TestGoVersionPinnedExactly` (go.mod has `toolchain`), `TestReadmeHelperImagesPinned`.
- [ ] **Step 2:** Run the tests. Expected: FAIL.
- [ ] **Step 3:** Resolve the digests and checksums from the network (record how), update files and write the script.
- [ ] **Step 4:** Run the full suite, then the docker build and the deliberate-wrong-checksum failure check. Expected: PASS and a failing build for the wrong checksum.
- [ ] **Step 5: Commit** `build: pin and checksum base images, tooling and vendored assets`.

### Task 16: Prove the contract with book and TV plugins through the real server

**Files:**
- Create: `internal/web/contract_proof_test.go`
- Modify: `internal/plugin/plugintest/plugintest.go` (fixture sources, importers with canned data)
- Test: `internal/web/contract_proof_test.go`

**Interfaces:**
- Consumes: everything.
- Produces: end-to-end tests (no shipped plugin) that register `plugintest.BookPlugin()` and `plugintest.TVPlugin()` next to the anime plugin in a test server and run the real flows through the real handlers.

- [ ] **Step 1: Write failing tests**: `TestBookPluginSearchAddListEditFlow` (search, add with page progress, then switch to percent progress via edit details, list shows "Reading" and `N / pages` then `N%`), `TestTVPluginEpisodeAndPositionProgress` (episode count plus a duration position, `+1` increments the primary episode only, finished/not-started filters), `TestGenresAndTagsWorkForFixturePlugins` (book fixture enables genres only, tv fixture enables both; a kind without them shows no chips, no filter and rejects terms), `TestPluginsAreIsolated` (anime items never show on the book list, search, person pages or backup selection by plugin; disabling the book plugin hides only it), `TestThreePluginsShareOneQueueAndImportRunner`, `TestNavigationAndSettingsWithThreePlugins`, `TestBackupRoundTripWithThreePlugins`.
- [ ] **Step 2:** Run the tests. Expected: FAIL until fixtures are complete (any core gap discovered here is fixed in the core, not worked around in the fixture, and listed in the report).
- [ ] **Step 3:** Implement the fixtures and fix core gaps.
- [ ] **Step 4:** Run the full suite. Expected: PASS.
- [ ] **Step 5: Commit** `test: prove the plugin contract with book and tv fixtures`.

### Task 17: README, documentation and end-to-end smoke

**Files:**
- Modify: `README.md`, `docker-compose.yml`
- Create: `docs/plugins.md` (plugin author guide for the contract: manifest, fields, progress types, sections, sources, importers/exporters, versioning, how a future marketplace bundle would map onto it)
- Test: `internal/web/e2e_test.go` (extended)

**Interfaces:**
- Consumes: all tasks.
- Produces: README updated (plugins section: what a plugin is, enabling/disabling in Settings, the anime plugin, backup/restore of data and the universal backup, filters, pinned builds and how to update pins, disk-use numbers, what changed in URLs/behaviour); `docs/plugins.md`; extended e2e tests (`TestPluginToggleEndToEnd`, `TestListSearchEndToEnd`, `TestEditDetailsEndToEnd`, `TestBackupEndToEnd`); a curl smoke against the real container using a private compose project name and port (search → add → list with progress → edit details returns to overview → filter by text → recommendations fragment loads → toggle the anime plugin off and back on → backup download and restore into a second empty volume of the same project → restart persistence), transcript in the report with key output lines, teardown of only that project.

- [ ] **Step 1: Write failing tests** for the new e2e tests above.
- [ ] **Step 2:** Run `scripts/go.sh test ./...`. Expected: FAIL until wiring is complete.
- [ ] **Step 3:** Finish wiring; write the docs; run the real-container smoke.
- [ ] **Step 4:** Run the full suite, vet, fmt. Expected: PASS and a complete smoke transcript.
- [ ] **Step 5: Commit** `docs: plugin guide, readme and end-to-end checks`.

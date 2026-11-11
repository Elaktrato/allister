# Books Plugin Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the opt-in Books plugin (individual books and light-novel series) with Open Library, Google Books and AniList sources, Goodreads CSV and AniList-account imports, JSON export and the generic backup, plus the small generic core capabilities those need.

**Architecture:** Two plugin kinds: `book` (pages/percent; Open Library and Google Books) and `series` (volume/cumulative chapter; AniList novels). Add source-kind declarations, importer fallback sources, manual import rows with fields, all-id import dedupe and active-progress-only updates to the core; then build the plugin package around the existing contract and test fixtures.

**Tech Stack:** Go 1.27.3 (pinned Docker image), SQLite, htmx, Tailwind, the existing `internal/plugin` contract, `internal/library`, `internal/importer`, `internal/web` and shared `internal/plugins/anilist`/`jsonexport`.

**Spec:** `docs/superpowers/specs/2026-11-10-books-plugin-design.md`

## Global Constraints

- No new Go dependencies; everything runs through `scripts/go.sh` (`test`, `vet`, `fmt`). `-race` is unavailable.
- Plugins are data only: no plugin HTML/JS, no core media-specific words; `internal/plugins/book` is exempt from the guard. No new database migration.
- Text hygiene: `plugin.SingleLine`, `plugin.MultiLine`, `plugin.HTTPSURL`; list caps from the spec (titles 300, text 20000, terms 60, names 300, ids 100, genres 50, tags 100, credits 500).
- Progress is the closed enum; the book kind uses pages/percent, the series kind uses volume/chapter (primary chapter). No hierarchy/sub-items.
- No calendar, recommendations or related sections for books.
- README stays at or below 150 lines. Docs tests must stay green.
- Preview only on the assistant-owned port 18083 and project `allister-preview`. Never touch the owner's containers/ports 18080-18082. The owner pushes; the agent does not push.
- Every commit: package tests first, then `scripts/go.sh test -count=1 ./...`, `scripts/go.sh vet ./...`, `scripts/go.sh fmt ./...`.

## Review Focus

- Re-importing the same Goodreads CSV twice must not duplicate ISBN-less rows; the `Book Id` is a second identity.
- An existing entry tracked by percent (book) or volume only (series) must not fail an import that brings pages/chapter; inactive progress is skipped and noted.
- A rate-limited primary import source must not silently fall back; only `ErrNotFound` moves to the next source.
- An Open Library edition without pages/authors must still import, using the Goodreads row's fields and the synthetic/known author data.
- A source error must never log the Google Books API key, and malformed source JSON must be `ErrBadResponse`, not a panic.
- The same ISBN imported from a second source must not create a second item.

---

### Task 1: Source-kind declarations

**Files:**
- Modify: `internal/plugin/contract.go` (`SourceInfo`)
- Modify: `internal/plugin/validate.go`
- Modify: `internal/plugin/validate_test.go`, `internal/plugin/contract_json_test.go`
- Modify: `internal/web/search.go`, `internal/web/match.go`
- Modify: `internal/web/search_test.go`, `internal/web/match_test.go`

**Interfaces:**
- `SourceInfo.Kinds []string` (`json:"kinds,omitempty"`); empty means every kind.
- `func sourceServesKind(si plugin.SourceInfo, kind string) bool`
- `func sourcesForKind(m plugin.Manifest, kind string) []plugin.SourceInfo`
- `func defaultSourceForKind(m plugin.Manifest, kind string) plugin.SourceInfo`
- `func chooseSourceForKind(m plugin.Manifest, kind, id string) (plugin.SourceInfo, bool)`
- Search and match use these for the source selector and default; the visible source choices contain only sources serving the chosen kind. A request naming a source that does not serve the kind falls back to the kind's first valid source; no valid source is a 404.

- [ ] **RED:** `TestSourceKindsValidation` (empty valid; declared kind valid; unknown kind, duplicate and empty string rejected). Extend the manifest JSON round trip with `Kinds`. `TestSearchFiltersSourcesByKind`: with kinds `book`/`series` and sources Open Library (`Kinds: [book]`), AniList (`Kinds: [series]`), `/book/search?q=dune` uses Open Library; `?kind=series` uses AniList; `?kind=series&source=openlibrary` falls back to AniList; the source select has one option. `TestMatchSourcesFollowKind`: a manual series sees only AniList; a manual book sees only Open Library.
- [ ] **Run:** `scripts/go.sh test -count=1 ./internal/plugin/ ./internal/web/` — expected FAIL (field/helpers missing, wrong source choice).
- [ ] **Implement:** add the field, validate each kind exists and is listed once, add the web helpers and thread them through search/match. Keep the existing `defaultSource`/`chooseSource` behaviour for plugins without `Kinds`.
- [ ] **Run:** same command — expected PASS.
- [ ] **Full suite, vet, fmt; commit** `feat: declare source-kind support`.

---

### Task 2: Importer fallback sources

**Files:**
- Modify: `internal/plugin/contract.go` (`ImporterInfo`)
- Modify: `internal/plugin/validate.go`
- Modify: `internal/plugin/validate_test.go`, `internal/plugin/contract_json_test.go`
- Modify: `internal/importer/runner.go`
- Modify: `internal/importer/runner_test.go`

**Interfaces:**
- `ImporterInfo.ResolveViaFallback []string` (`json:"resolve_via_fallback,omitempty"`).
- Validation: every fallback source exists, supports lookup and declares `ExternalIDType`; no duplicates; not also the primary; a fallback without a primary `ResolveVia` is invalid.
- `type sourceRef struct { source plugin.Source; info plugin.SourceInfo }`; `importerRef` gains `sources []sourceRef` (primary first, then fallbacks), filled by `resolve`.
- `handleRow` tries sources in order only on `ErrNotFound`; rate-limit/unavailable retries the same source (existing `maxRetries` behaviour) and never falls back; all not-found saves one unmatched row whose reason names the sources.

- [ ] **RED:** `TestImporterFallbackUsesNextSourceOnNotFound` (primary fake returns `ErrNotFound`, fallback returns a book; row imported, item source is the fallback). `TestImporterFallbackDoesNotHideRateLimit` (primary `ErrRateLimited`; fallback `Calls(OpLookup)==0`; import is retried/paused). `TestImporterFallbackAllNotFoundSavesUnmatched` (both not found; one manual item). `TestImporterFallbackValidation` (unknown, duplicate, no lookup, missing id type, fallback without primary). Extend the manifest JSON round trip.
- [ ] **Run:** `scripts/go.sh test -count=1 ./internal/plugin/ ./internal/importer/` — expected FAIL.
- [ ] **Implement:** the contract field/validation, `sourceRef`, the ordered lookup loop and per-source pacing; use the existing `ErrNotFound`/rate-limit paths.
- [ ] **Run:** same command — expected PASS.
- [ ] **Full suite, vet, fmt; commit** `feat: importer source fallback`.

---

### Task 3: Manual import rows and row fields

**Files:**
- Modify: `internal/plugin/contract.go` (`ImporterInfo.ManualRows`)
- Modify: `internal/plugin/data.go` (`ParsedEntry.Fields`)
- Modify: `internal/plugin/contract_json_test.go`
- Modify: `internal/importer/submit.go`
- Modify: `internal/importer/runner.go`
- Modify: `internal/importer/runner_test.go`

**Interfaces:**
- `ImporterInfo.ManualRows bool` (`json:"manual_rows,omitempty"`).
- `ParsedEntry.Fields map[string]FieldValue` (`json:"fields,omitempty"`).
- `storedRows`: a row whose declared external id is empty is stored pending (not skipped) when `ManualRows` and the title is non-empty; a title-less row is still skipped. `entries` counts it.
- `handleRow`: an empty declared id with `ManualRows` calls a shared manual-save path with outcome `imported` and reason `saved as a manual entry (no <id label>)`; without `ManualRows` it stays skipped.
- Both manual and unmatched saves copy **all** `ParsedEntry.ExternalIDs` and `ParsedEntry.Fields` into the new manual item; the title comes from the row (or the id fallback for unmatched rows with an id).

- [ ] **RED:** `TestManualRowsImportWithoutExternalID` (title/author/pages/year fields; one manual item; outcome imported; no unmatched badge). `TestManualRowsNeedTitle` (no id, no title → skipped). `TestSourceMissKeepsManualFields` (id present but source not found; fields kept; outcome unmatched). Extend the JSON round trip.
- [ ] **Run:** `scripts/go.sh test -count=1 ./internal/plugin/ ./internal/importer/` — expected FAIL.
- [ ] **Implement:** contract fields, `storedRows`/`handleRow` branch, shared manual save with all IDs/fields. `saveUnmatched` keeps the unmatched outcome; the new path records imported.
- [ ] **Run:** same command — expected PASS.
- [ ] **Full suite, vet, fmt; commit** `feat: manual import rows with fields`.

---

### Task 4: Import identity and field merge

**Files:**
- Modify: `internal/importer/runner.go`
- Modify: `internal/importer/runner_test.go`
- Modify: `internal/importer/import_mode_test.go` if the helper is shared

**Interfaces:**
- Before resolving a row, `handleRow` tries `Library.ItemByExternalID` for every id in `rc.entry.ExternalIDs` (sorted for determinism), not only the importer's declared type. A hit goes to `handleSaved`.
- A source match copies the row's external ids into `data.ExternalIDs` when the source did not supply that type (source wins), and fills a missing `data.Fields` key from `rc.entry.Fields` (source wins). `library.AddToList` validates the merged data.
- A re-import of an ISBN-less Goodreads row finds the item by its `goodreads` id and follows the normal skip/newer/always path.

- [ ] **RED:** `TestImportDedupesBySecondaryExternalID` (item saved with `goodreads` id, re-import same row → still one item; outcome skipped/updated by mode). `TestImportDedupesByPrimaryISBNAcrossSources` (an Open Library item already holds ISBN `978...`; a Goodreads row with the same ISBN updates/skips it, not a second item). `TestImportMergesMissingSourceFields` (source lacks `pages`; row supplies 300; saved item pages 300; completed progress bounded at 300). `TestImportSourceFieldsWin` (source 250, row 999 → 250). `TestImportStoresSecondaryExternalIDs` (item keeps ISBN plus Goodreads id).
- [ ] **Run:** `scripts/go.sh test -count=1 ./internal/importer/` — expected FAIL.
- [ ] **Implement:** all-id dedupe, merge row ids/fields on a source match, keep source values first; reuse the existing `AddToList` validation.
- [ ] **Run:** same command — expected PASS.
- [ ] **Full suite, vet, fmt; commit** `feat: import identity and field fallback`.

---

### Task 5: Import updates respect the active progress combination

**Files:**
- Modify: `internal/library/entries.go` (`ApplyImport`)
- Modify: `internal/library/apply_import_test.go`
- Modify: `internal/importer/runner.go` (`handleSaved`)
- Modify: `internal/importer/import_mode_test.go`

**Interfaces:**
- `ApplyImport` updates status/rating as before and writes **every** `in.Progress` key that is present in the item's `ProgressKeys`, not only `PrimaryKey`. Inactive keys are ignored, never a validation error.
- `handleSaved` computes the row's progress keys that are not active on the saved item and adds a note such as `progress not active: volume` to the updated/skipped row; the entry is still updated for status/rating/active tracks.
- No change to the `ApplyImport` signature.

- [ ] **RED:** `TestApplyImportUpdatesEveryActiveTrack` (entry `[volume, chapter]`, input both → both updated). `TestApplyImportSkipsInactiveTrack` (entry `[pages]`, input `percent: 50` → no error, no percent value). `TestImportUpdateNotesInactiveProgress` (runner outcome has the note and the active track still updates). Update the existing "primary track only" assertion.
- [ ] **Run:** `scripts/go.sh test -count=1 ./internal/library/ ./internal/importer/` — expected FAIL.
- [ ] **Implement:** loop over `in.Progress`, filter by `it.ProgressKeys`; add the runner note.
- [ ] **Run:** same command — expected PASS.
- [ ] **Full suite, vet, fmt; commit** `feat: import updates active progress tracks`.

---

### Task 6: AniList client extensions

**Files:**
- Modify: `internal/plugins/anilist/types.go` (`AccountEntry`)
- Modify: `internal/plugins/anilist/client.go` (`mediaListQuery`, new `SearchFormat`)
- Modify: `internal/plugins/anilist/client_test.go`
- Modify: `internal/plugins/anilist/testdata/account.json` if needed

**Interfaces:**
- `AccountEntry.ProgressVolumes int`, `AccountEntry.Media.Format string`.
- `mediaListQuery` requests `format` and `progressVolumes`.
- `func (c *Client) SearchFormat(ctx context.Context, typ, format, q string) ([]SearchResult, error)`; new `searchFormatQuery` uses `media(search: $q, type: $type, format: $format, ...)`. The existing `Search` query and behaviour stay unchanged.

- [ ] **RED:** `TestMediaListRequestsFormatAndProgressVolumes` (request body contains both fields; a fixture maps `Format` and `ProgressVolumes`). `TestSearchFormatSendsTheFormat` (server asserts the query contains `format: $format` and variables `format: "NOVEL"`; the result maps). A control asserts `Search` still sends the old query without the format variable.
- [ ] **Run:** `scripts/go.sh test -count=1 ./internal/plugins/anilist/` — expected FAIL.
- [ ] **Implement:** fields, query, method; keep `Search` untouched.
- [ ] **Run:** same command — expected PASS.
- [ ] **Full suite, vet, fmt; commit** `feat: AniList format search and volume progress`.

---

### Task 7: Books manifest and skeleton

**Files:**
- Create: `internal/plugins/book/manifest.go`
- Create: `internal/plugins/book/plugin.go`
- Create: `internal/plugins/book/manifest_test.go`

**Interfaces:**
- `const PluginID = "book"`, `KindBook = "book"`, `KindSeries = "series"`, `SourceOpenLibrary = "openlibrary"`, `SourceGoogleBooks = "googlebooks"`, `SourceAniList = "anilist"`, `ImporterGoodreads = "goodreads-csv"`, `ImporterAniList = "anilist-account"`, `ExporterJSON = "json"`.
- `type Options struct { HTTP *http.Client; OpenLibraryURL, GoogleBooksURL, AniListURL, GoogleBooksKey string }`
- `func New(opts Options) plugin.Plugin`
- `func manifest() plugin.Manifest`
- `type booksPlugin struct { sources map[string]plugin.Source; importers map[string]plugin.Importer; exporter plugin.Exporter }` (later tasks fill the maps; `New` may leave them nil in this task).
- Manifest exact shape from the spec: two kinds, fields, progress combinations/primary, sources with `Kinds`, importers, `jsonexport` format `allister.book.export`, icon `auto_stories`, `DefaultEnabled:false`.
- Importer info: `goodreads-csv` has `ExternalIDType: "isbn"`, `ExternalIDLabel: "ISBN"`, `ResolveVia: "openlibrary"`, `ResolveViaFallback: ["googlebooks"]`, `ManualRows: true`, fallback title language `en`; `anilist-account` has `ExternalIDType: "anilist"`, `ExternalIDLabel: "AniList"`, `ResolveVia: "anilist"`, fallback title language `ja-latn`.

- [ ] **RED:** `TestBooksManifest` asserts the plugin id/name/version/icon/default, the two kind keys, book fields (`pages` total, `series`/`series_number`), book progress (`[pages]`, `[percent]`, primary `pages`), series fields (`volumes`, `chapters`), series progress (`[volume,chapter]`, `[chapter]`, `[volume]`, primary `chapter`), source `Kinds`/URL patterns/ids, importer ids and exporter. `plugin.Validate(manifest())` must return nil.
- [ ] **Run:** `scripts/go.sh test -count=1 ./internal/plugins/book/` — expected FAIL.
- [ ] **Implement:** manifest and `New` skeleton with the declared maps (empty/pending source implementations). No plugin HTML/JS.
- [ ] **Run:** same command — expected PASS.
- [ ] **Full suite, vet, fmt; commit** `feat: books plugin manifest`.

---

### Task 8: Book mapping helpers

**Files:**
- Create: `internal/plugins/book/mapping.go`
- Create: `internal/plugins/book/mapping_test.go`

**Interfaces:**
- `type bookRecord struct { Source, ID, Title, Subtitle, Author, ISBN, Publisher, Language, Synopsis, CoverURL, ExternalURL, Series string; Pages, Year int; PublishedOn *time.Time; Genres []string; Tags []plugin.Tag; Credits []plugin.Credit; ExternalIDs map[string]string }`
- `func bookData(r bookRecord) plugin.ItemData` — sets kind `book`, titles (title + subtitle under `Language`, title 300 runes), fields `author`, `pages`, `year`, `publisher`, `isbn`, `series`, `published_on`, `synopsis`, genres/tags with caps, cover/external URL, external ids.
- `func bookCredits(r bookRecord) []plugin.Credit` — sanitises roles/names/URLs and caps credits.
- Keep caps as constants in this file; no media-specific code in core.

- [ ] **RED:** `TestBookDataMapsTheBook` (German title language, subtitle, pages/year/publisher/ISBN/series/full date; genres and tags capped; empty/oversized values dropped). `TestBookCredits` (role/name caps, empty names dropped).
- [ ] **Run:** `scripts/go.sh test -count=1 ./internal/plugins/book/` — expected FAIL.
- [ ] **Implement:** the record, mapping, sanitisation, caps.
- [ ] **Run:** same command — expected PASS.
- [ ] **Full suite, vet, fmt; commit** `feat: book item mapping`.

---

### Task 9: Open Library source

**Files:**
- Create: `internal/plugins/book/openlibrary.go`
- Create: `internal/plugins/book/openlibrary_test.go`
- Create fixtures under `internal/plugins/book/testdata/`: `openlibrary_search.json`, `openlibrary_work.json`, `openlibrary_editions.json`, `openlibrary_edition.json`, `openlibrary_author.json`

**Interfaces:**
- `type openLibraryClient struct { base string; hc *http.Client; maxBody int64 }`
- `func newOpenLibraryClient(base string, hc *http.Client) *openLibraryClient`
- `func (c *openLibraryClient) search(ctx context.Context, q string) ([]plugin.ItemSummary, error)`
- `func (c *openLibraryClient) details(ctx context.Context, id string) (plugin.ItemData, error)`
- `func (c *openLibraryClient) lookupISBN(ctx context.Context, isbn string) (plugin.ItemData, error)`
- `func (c *openLibraryClient) people(ctx context.Context, id string) ([]plugin.Credit, error)`
- `type openLibrarySource struct { c *openLibraryClient }` implements `plugin.Source`; `Recommendations` returns nil; `Lookup` supports `isbn` only (the match dialog already uses Details for an Open Library work id).
- Edition heuristic: prefer pages+cover, then pages, then first; prefer English. `series` from the edition; work title as `en`; edition title under its MARC language when different/known; author records supply ids/photos/URLs; subjects → tags with the spec's noise filter; a short exact genre allow-list.

- [ ] **RED:** `TestOpenLibrarySearchMapsSummaries` (key/id, title, author, year, cover). `TestOpenLibraryDetailsChoosesEditionAndMaps` (pages/ISBN/publisher/series/date/title language/tags/credits; edition without pages still yields a book; an author record that 404s is skipped and the book still maps). `TestOpenLibraryLookupByISBN` (ISBN redirect → edition → work; exact pages/ISBN). `TestOpenLibraryPeople` (author id/name/photo/URL). `TestOpenLibraryErrors` (404 → ErrNotFound, 429 → ErrRateLimited, 500/network → ErrUnavailable, bad JSON → ErrBadResponse).
- [ ] **Run:** `scripts/go.sh test -count=1 ./internal/plugins/book/` — expected FAIL.
- [ ] **Implement:** HTTP client, JSON shapes, mapping to `bookRecord`, source wrapper, error mapping, UA.
- [ ] **Run:** same command — expected PASS.
- [ ] **Full suite, vet, fmt; commit** `feat: Open Library source`.

---

### Task 10: Google Books source

**Files:**
- Create: `internal/plugins/book/googlebooks.go`
- Create: `internal/plugins/book/googlebooks_test.go`
- Create fixtures under `internal/plugins/book/testdata/`: `googlebooks_search.json`, `googlebooks_volume.json`

**Interfaces:**
- `type googleBooksClient struct { base, key string; hc *http.Client; maxBody int64 }`
- `func newGoogleBooksClient(base, key string, hc *http.Client) *googleBooksClient`
- `func (c *googleBooksClient) search(ctx, q string) ([]plugin.ItemSummary, error)`
- `func (c *googleBooksClient) details(ctx, id string) (plugin.ItemData, error)`
- `func (c *googleBooksClient) lookupISBN(ctx, isbn string) (plugin.ItemData, error)` — `q=isbn:...`, first matching `industryIdentifiers`.
- `func (c *googleBooksClient) people(ctx, id string) ([]plugin.Credit, error)`
- `type googleBooksSource struct { c *googleBooksClient }` implements `plugin.Source`; no recommendations.
- `func googleBooksPersonID(name string) string` returns `"gb-" + 16 hex` of SHA-256 of the normalised name; same name → same id; no image/URL.
- `key=` appended only when set; errors redact the URL/key.

- [ ] **RED:** `TestGoogleBooksSearchMapsSummaries`; `TestGoogleBooksDetailsMapsVolume` (title+subtitle, authors, pages, publisher, ISBN-13/10 conversion, categories → genres, language, date, cover); `TestGoogleBooksLookupByISBN` (matching identifier, no match → ErrNotFound); `TestGoogleBooksAuthorIDsAreStable`; `TestGoogleBooksKeyIsSentAndRedacted`; `TestGoogleBooksErrors` (404/429/quota 403/500/bad JSON).
- [ ] **Run:** `scripts/go.sh test -count=1 ./internal/plugins/book/` — expected FAIL.
- [ ] **Implement:** client, JSON mapping, synthetic ids, key handling/redaction, source wrapper.
- [ ] **Run:** same command — expected PASS.
- [ ] **Full suite, vet, fmt; commit** `feat: Google Books source`.

---

### Task 11: AniList light-novel source

**Files:**
- Create: `internal/plugins/book/anilist.go`
- Create: `internal/plugins/book/anilist_test.go`
- Create `internal/plugins/anilist/testdata/novel.json` (a NOVEL fixture; do not reuse the manga fixture)

**Interfaces:**
- `type novelClient interface { SearchFormat(ctx, typ, format, q string) ([]anilist.SearchResult, error); Media(ctx, typ, id string) (anilist.Media, error); MediaByMALID(ctx, typ, malID string) (anilist.Media, error); MediaList(ctx, typ, userName string) ([]anilist.AccountEntry, error) }`
- `type anilistSource struct { c novelClient }` implements `plugin.Source`; `Recommendations` nil.
- `func novelData(m anilist.Media) plugin.ItemData` — kind `series`, titles `ja`/`ja-latn`/`en`, fields `author`, `volumes`, `chapters`, `year`, `published_on`, `synopsis`, genres/tags caps, cover, external URL/IDs.
- `func novelCredits(staff []anilist.StaffMember) []plugin.Credit` — story → author, art/illustration → illustrator; source role in `Detail`.
- `Search` uses `SearchFormat(anilist.TypeManga, "NOVEL", q)` and keeps only `Type == "NOVEL"`; `Details`/`Lookup` reject non-NOVEL with `ErrNotFound`.

- [ ] **RED:** `TestNovelSearchFiltersNonNovels` (fixture returns a MANGA and a NOVEL; only the novel, query has format NOVEL). `TestNovelDetailsMapsFieldsAndStaff` (titles/volumes/chapters/year/author/illustrator/genres/tags/cover/external ids). `TestNovelDetailsRejectsMangaFormat`. `TestNovelLookupByMALID`. `TestNovelErrors` (not found/rate limited/unavailable/bad response).
- [ ] **Run:** `scripts/go.sh test -count=1 ./internal/plugins/book/ ./internal/plugins/anilist/` — expected FAIL.
- [ ] **Implement:** local interface, source, mapping, novel fixture.
- [ ] **Run:** same command — expected PASS.
- [ ] **Full suite, vet, fmt; commit** `feat: AniList light-novel source`.

---

### Task 12: Goodreads CSV importer

**Files:**
- Create: `internal/plugins/book/goodreads.go`
- Create: `internal/plugins/book/goodreads_test.go`
- Create `internal/plugins/book/testdata/goodreads.csv`

**Interfaces:**
- `type goodreadsImporter struct{}` with `Parse(ctx context.Context, r io.Reader) (plugin.ParseResult, error)`.
- Header lookup by name, BOM-tolerant; only Title required; ISBN13 preferred, ISBN10 converted; invalid ISBN is absent with a note; `Book Id` stored as an additional `goodreads` external id.
- Status mapping read/currently-reading/to-read; rating 0-5 → 0 or ×2; pages/year/publisher/author fields; `Date Read` → `finished_on`; `My Review` → comment; completed pages progress; no cover.
- `ManualRows` and `Fields` set on every row; no timestamps.

- [ ] **RED:** `TestGoodreadsCSVMapsRows` (statuses, rating 4→8, 0→nil, pages/year/publisher/author/date/review, ISBN13 and converted ISBN10, `goodreads` id). `TestGoodreadsCSVNoISBNIsManual` (row without ISBN keeps fields; no skip). `TestGoodreadsCSVSkipsUnknownShelfAndBadHeader` (`ErrBadInput` for a non-Goodreads header; unknown shelf skipped with reason). `TestGoodreadsCSVQuotedFields` (commas/quotes/BOM).
- [ ] **Run:** `scripts/go.sh test -count=1 ./internal/plugins/book/` — expected FAIL.
- [ ] **Implement:** CSV parser, ISBN normaliser, field/status/rating/date mapping.
- [ ] **Run:** same command — expected PASS.
- [ ] **Full suite, vet, fmt; commit** `feat: Goodreads CSV importer`.

---

### Task 13: AniList account importer for light novels

**Files:**
- Modify: `internal/plugins/book/anilist.go`
- Create: `internal/plugins/book/account_test.go`
- Create `internal/plugins/anilist/testdata/account_novel.json`

**Interfaces:**
- `type anilistAccountImporter struct { c novelClient }` with `Parse` returning `ErrBadInput` and `ImportAccount(ctx, account string) (plugin.ParseResult, error)`.
- `MediaList` type MANGA; keep only `Media.Format == "NOVEL"`; other entries increment `Ignored`.
- Rows: kind `series`, AniList id/title, status class, score, `Progress` → chapter, `ProgressVolumes` → volume, cover, `UpdatedAt`; no file.
- Status map: CURRENT/REPEATING → active, PLANNING → planned, COMPLETED → completed, PAUSED → paused, DROPPED → dropped.

- [ ] **RED:** `TestAniListAccountFiltersNovels` (novel + manga fixture; one row, one ignored). `TestAniListAccountMapsProgress` (chapter and volume values, score, status, updatedAt, cover, external id). `TestAniListAccountParseRefusesFile`.
- [ ] **Run:** `scripts/go.sh test -count=1 ./internal/plugins/book/ ./internal/plugins/anilist/` — expected FAIL.
- [ ] **Implement:** importer and account fixture.
- [ ] **Run:** same command — expected PASS.
- [ ] **Full suite, vet, fmt; commit** `feat: AniList light-novel account import`.

---

### Task 14: Plugin wiring, config and registration

**Files:**
- Modify: `internal/plugins/book/plugin.go`
- Create: `internal/plugins/book/plugin_test.go`
- Modify: `internal/config/config.go` (+ test if the package has one)
- Modify: `cmd/allister/main.go`
- Modify: `docker-compose.yml`
- Modify: `internal/guard/guard_test.go` only if the guard requires a registration allow-list entry

**Interfaces:**
- `New` builds `openLibrarySource`, `googleBooksSource`, `anilistSource`, `goodreadsImporter`, `anilistAccountImporter` and `jsonexport.New("allister.book.export")`; default URLs are the production bases; nil `HTTP` uses `http.DefaultClient`.
- `config.Config.GoogleBooksKey string` from `GOOGLE_BOOKS_API_KEY`; `cmd` passes it to `book.New`.
- Compose passes `GOOGLE_BOOKS_API_KEY: "${GOOGLE_BOOKS_API_KEY:-}"`.
- `cmd` registers the plugin; the plugin can be enabled/disabled like the others.

- [ ] **RED:** `TestNewProvidesEveryDeclaredSourceImporterAndExporter` (all three source ids, both importers, JSON exporter non-nil). `TestConfigLoadsGoogleBooksKey` (env set/empty). Full registry registration with `book.New(book.Options{})` succeeds; manifest validates. Full suite must stay green with the new plugin compiled in.
- [ ] **Run:** `scripts/go.sh test -count=1 ./internal/plugins/book/ ./internal/config/ ./cmd/allister/` — expected FAIL.
- [ ] **Implement:** wiring, config/env/compose, registration, defaults.
- [ ] **Run:** same command — expected PASS.
- [ ] **Full suite, vet, fmt; commit** `feat: register the books plugin`.

---

### Task 15: Documentation

**Files:**
- Modify: `README.md` (Books section, routes, imports, limitations; keep ≤150 lines)
- Modify: `docs/usage.md` (routes, sources, both kinds, imports, no calendar, known limitations)
- Modify: `docs/plugins.md` (`SourceInfo.Kinds`, `ResolveViaFallback`, `ManualRows`, `ParsedEntry.Fields`; reference the books plugin)
- Modify: `docs/handoff.md` (what exists, decisions, follow-ups incl. GitHub issue #1)
- Modify: `docs/superpowers/specs/2026-11-10-books-plugin-design.md` only if implementation reveals a correction

- [ ] **RED:** docs tests (README line cap, links) fail if the new text is missing or over the cap. Check the docs package tests.
- [ ] **Run:** `scripts/go.sh test -count=1 ./...` — expected FAIL only where docs tests check the new content (or PASS before edits; the deliverable is the updated text).
- [ ] **Implement:** the docs, with exact routes `/book`, `/book/search`, `/book/{source}/{id}`, `/book/manual/...`, `/book/export/json`, source/kind names, Goodreads/AniList import rules, Google key env, model-3 issue link.
- [ ] **Run:** full suite, vet, fmt — expected PASS.
- [ ] **Commit** `docs: books plugin`.

---

### Task 16: Preview verification and finish

**Files:** none expected; fix files only if verification finds a defect.

- [ ] **Build:** `PORT=18083 docker compose -p allister-preview up -d --build`; `curl http://localhost:18083/healthz` returns `ok`.
- [ ] **Enable Books** in Settings > Plugins; verify `/book` and `/book?kind=series`.
- [ ] **Search:** Open Library (`dune`), Google Books (`dune`; expect a clean rate-limit message without a key and success with a key if configured), AniList (`sword art online`; only NOVEL results).
- [ ] **Add:** one standalone book (pages) and one light-novel series (volume+chapter); edit both; check covers, authors, genres/tags; check the series progress and +1.
- [ ] **Imports:** a small Goodreads CSV with one ISBN row and one no-ISBN row: both imported, the second manual, no duplicate on a second run; the AniList account importer filters novels (fixture/real account as available).
- [ ] **Export/backup:** `/book/export/json` contains both kinds; a backup/restore round trip keeps the items (no hierarchy exists yet).
- [ ] **Calendar:** `/calendar` and `/calendar.ics` contain no book events.
- [ ] **Finish:** full suite, vet, fmt; report the preview result. Do not push; the owner pushes. If a fix was needed, commit it with the relevant `fix:`/`feat:` message after the full suite.

---

## Self-review notes

- Spec coverage: both kinds, all three sources, both imports, source-kind filtering, import fallback/manual rows/dedupe/active updates, Google key, export/backup, docs, no calendar.
- Reviewer focus tests: re-import by both identities, inactive progress, no fallback on rate limit, missing Open Library pages/authors, Google key redaction, cross-source ISBN dedupe.
- The model-3 hierarchy remains out of scope and is documented in GitHub issue #1.
- Interface names above are the intended ones; later tasks consume the same names (`SourceInfo.Kinds`, `ImporterInfo.ResolveViaFallback`, `ImporterInfo.ManualRows`, `ParsedEntry.Fields`, `bookRecord`, `searchFormatQuery`, `googleBooksPersonID`).

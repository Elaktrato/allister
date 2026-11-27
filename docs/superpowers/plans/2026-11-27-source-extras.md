# Source Extras Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Import release status (AniList, IGDB) and the AniList extras (season, country, adult flag, animation studios, community score, popularity, synonyms, streaming links, banner, trailer) as filterable/sortable data, show banner, trailer and links on the item page, hide adult items on request, and add a bulk "Update from source" that only fills in what is missing.

**Architecture:** Filterable data are plain manifest fields of the plugins. Banner, trailer and links are small data-only contract additions stored by `internal/library` (migration 00023), downloaded by the existing `sync_item` job through `internal/covers`, and compared by the existing compare core. The bulk update is a run record plus one queued job per item that applies a new additive "fill" path in `internal/library`.

**Tech Stack:** Go 1.27.4, SQLite (modernc), goose, htmx, Tailwind theme tokens.

**Spec:** `docs/superpowers/specs/2026-11-27-source-extras-design.md`

## Global Constraints

- No media words in core Go/HTML (`internal/guard`); media words only in `internal/plugins/*` and docs.
- Contract types stay JSON-serialisable data; extend `TestManifestJSONRoundTrip` / `TestRuntimeDataJSONRoundTrip` (`internal/plugin/contract_json_test.go`) for every new field.
- One migration, `internal/db/migrations/00023_source_extras.sql`; never edit applied ones.
- No third-party resource loads on any page: banner and trailer thumbnail served from the local store; the trailer is a plain `<a>` with `rel="noopener noreferrer" target="_blank"`.
- Templates use only colour classes in `internal/appearance/tailwind-map.json`; no inline `style`, no inline scripts.
- Sources sanitise with `plugin.SingleLine`, `plugin.MultiLine`, `plugin.HTTPSURL`.
- Backup becomes format 5; formats 1–4 still restore.
- Bulk update: single values only where stored is empty; lists additive (append missing, never remove, reorder or change).
- Tests: `CGO_ENABLED=0 go test ./... -count=1`; `go vet ./...`; `gofmt -l .` empty; `/tmp/claude-0/gl/golangci-lint run ./...` 0 issues (build it with `GOTOOLCHAIN=go1.27.4 GOBIN=/tmp/claude-0/gl CGO_ENABLED=0 go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0` if missing). Each commit green.
- Commit messages end with the session's attribution lines; prefixes `feat:`, `fix:`, `docs:`, `test:`.

## Review Focus

1. **An existing anime whose `studio` is a stored single text value** must read, filter, edit, compare, back up and restore as a one-element list after the field becomes a list (Task 2 test `TestTextValueReadsAsListAfterTypeChange`).
2. **An item whose only titles are alternative titles** (or alt titles stored before real ones) must not display or sort by an alt title while any other title exists (Task 2 `TestResolveTitleSkipsAltTitles`, `TestSortTitleSkipsAltTitles`).
3. **A bulk run interrupted by a restart, or with the plugin disabled mid-run,** resumes and its counters still add up to the total (Task 9 `TestRefreshRunSurvivesRecover`, `TestRefreshRunPausedWhilePluginDisabled`).
4. **A bulk run on an item the owner edited** (a changed title, a removed genre, an own cover) leaves every stored value as it is and only appends missing list elements (Task 8 `TestFillKeepsStoredValues`, `TestFillListsAppendOnly`).
5. **A source answering with a banner/trailer/link URL that is http, has userinfo, or a trailer site other than YouTube/Dailymotion** stores nothing for it (Task 5 `TestTrailerSitesAndURLs`; Task 1 validation via `plugin.HTTPSURL`).

---

### Task 1: Contract additions

**Files:**
- Modify: `internal/plugin/contract.go` (KindDef), `internal/plugin/data.go` (ItemData), `internal/plugin/validate.go`
- Test: `internal/plugin/contract_json_test.go`, `internal/plugin/validate_test.go`

**Interfaces:**
- Produces:
  - `const plugin.AltTitleLang = "alt"`: the reserved title language of alternative titles.
  - `KindDef.AltTitles bool \`json:"alt_titles,omitempty"\`` (the kind stores titles with `AltTitleLang`), `KindDef.Banner bool \`json:"banner,omitempty"\``, `KindDef.Trailer bool \`json:"trailer,omitempty"\``, `KindDef.AdultField string \`json:"adult_field,omitempty"\`` (an enum field whose value `"yes"` marks an adult item).
  - `type Trailer struct { URL string \`json:"url"\`; ThumbnailURL string \`json:"thumbnail_url,omitempty"\` }`, `type Link struct { Label string \`json:"label"\`; URL string \`json:"url"\` }`.
  - `ItemData.BannerURL string \`json:"banner_url,omitempty"\``, `ItemData.Trailer *Trailer \`json:"trailer,omitempty"\``, `ItemData.Links []Link \`json:"links,omitempty"\``.

- [ ] **Step 1: Failing tests.** Extend both round-trip tests with the new fields set. Add `TestValidateKindExtras` (table): `TitleLanguages` containing `"alt"` → problem at `.title_languages[i]` "reserved"; `TitleFallback` containing `"alt"` → problem; `AdultField: "nope"` → "unknown field"; `AdultField` naming a non-enum field or an enum without `"yes"` in `EnumValues` → problem; valid kind with all four set → no problems.
- [ ] **Step 2:** Run `CGO_ENABLED=0 go test ./internal/plugin/ -run 'RoundTrip|ValidateKindExtras' -count=1`; expect FAIL (unknown fields).
- [ ] **Step 3:** Implement types, constants and checks in `validateKind`.
- [ ] **Step 4:** Rerun; PASS. Run the full suite (manifests of all plugins still validate).
- [ ] **Step 5:** Commit `feat: contract for alternative titles, banner, trailer, links and adult flag`.

### Task 2: Storage (migration 00023) and title/field read rules

**Files:**
- Create: `internal/db/migrations/00023_source_extras.sql`
- Modify: `internal/library/library.go` (Item), `internal/library/items.go` (insert/read), `internal/library/fields.go` (read-compat), `internal/library/query.go` (ResolveTitleIndex, SortTitle), `internal/library/details.go` or wherever item parts are replaced on edit
- Test: `internal/library/items_test.go`, `internal/library/query_test.go`, `internal/library/fields` tests

**Interfaces:**
- Consumes: Task 1 types.
- Produces: `Item.BannerPath string`, `Item.Trailer *plugin.Trailer` (URL plus local or remote thumbnail path in `ThumbnailURL`), `Item.Links []plugin.Link`; `SaveItem` stores `d.BannerURL`, `d.Trailer`, `d.Links`; `Item()` loads them. Migration SQL:
  - `ALTER TABLE items ADD COLUMN banner_path TEXT NOT NULL DEFAULT '';`
  - `ALTER TABLE items ADD COLUMN trailer_url TEXT NOT NULL DEFAULT '';`
  - `ALTER TABLE items ADD COLUMN trailer_thumb TEXT NOT NULL DEFAULT '';`
  - `CREATE TABLE item_links (item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE, seq INTEGER NOT NULL, label TEXT NOT NULL, url TEXT NOT NULL, PRIMARY KEY(item_id, seq));`
  - `CREATE TABLE source_refresh_runs (id INTEGER PRIMARY KEY, plugin_id TEXT NOT NULL, started_at TEXT NOT NULL, finished_at TEXT, total INTEGER NOT NULL, updated INTEGER NOT NULL DEFAULT 0, unchanged INTEGER NOT NULL DEFAULT 0, failed INTEGER NOT NULL DEFAULT 0, failed_titles TEXT NOT NULL DEFAULT '[]', stopped TEXT NOT NULL DEFAULT '');` (used in Task 9)
  - `-- +goose Down` drops the table(s) and columns as earlier migrations do.

- [ ] **Step 1: Failing tests.**
  - `TestSaveItemStoresBannerTrailerLinks`: save with `BannerURL "https://img.example/b.jpg"`, trailer `{URL "https://www.youtube.com/watch?v=x", ThumbnailURL "https://img.example/t.jpg"}`, two links; `Item()` returns them in order; links over 20 are refused with ErrInvalid; a non-https link/banner URL is ErrInvalid.
  - `TestResolveTitleSkipsAltTitles`: titles `[{alt "Alt"}, {en "Real"}]`, preferred `"ja"`, no fallback → `"Real"`; only `[{alt "Alt"}]` → `"Alt"`.
  - `TestSortTitleSkipsAltTitles`: two items sorted by title, one whose first stored row is alt; order follows the real titles.
  - `TestTextSearchFindsAltTitles`: `Query.Text` matching only the alt title finds the item.
  - `TestTextValueReadsAsListAfterTypeChange`: store an item under a fixture kind where `studio` is text, re-register the kind with `studio` as `FieldList` (Filterable), then `Item()` gives `List: ["Studio A"]`, a list filter on `studio=Studio A` matches, saving the item writes list rows.
- [ ] **Step 2:** Run `CGO_ENABLED=0 go test ./internal/library/ -run 'BannerTrailer|AltTitles|TypeChange' -count=1`; FAIL.
- [ ] **Step 3:** Implement. Alt-title rule: in `ResolveTitleIndex` the final "first stored title" picks the first title whose `Lang != plugin.AltTitleLang`, else index 0; the SortTitle SQL's last pick adds `AND t.lang <> ?` with `plugin.AltTitleLang` before the unconditional pick. Read-compat: when the kind's def is `FieldList` and the stored row's vtype is text, decode it as a one-element list (filters on list fields must match it too).
- [ ] **Step 4:** Rerun; PASS. Full suite.
- [ ] **Step 5:** Commit `feat: store banners, trailers and links (migration 00023)`.

### Task 3: Downloading banner and trailer thumbnail

**Files:** Modify `internal/jobs/sync_item.go`; Test `internal/jobs/sync_item_test.go`.

**Interfaces:**
- Consumes: Task 2 `Item.BannerPath`, `Item.Trailer`.
- Produces: `library.SetBannerPath(ctx, itemID int64, from, to string) error` and `library.SetTrailerThumb(ctx, itemID int64, from, to string) error` (compare-and-set like the cover's setter), used by the job.

- [ ] **Step 1: Failing test** `TestSyncItemDownloadsBannerAndThumbnail`: an item with remote banner and thumbnail, an httptest server serving a PNG; after the job both paths are local file names in the covers dir; a failing banner download leaves `sync_state` partial with `ErrRetryLater` like a failing cover. `CoverOnly` jobs also download them.
- [ ] **Step 2:** Run `CGO_ENABLED=0 go test ./internal/jobs/ -run Banner -count=1`; FAIL.
- [ ] **Step 3:** Implement beside the cover download using `covers.Download`; the item page uses `covers.Src` for both.
- [ ] **Step 4:** PASS; full suite.
- [ ] **Step 5:** Commit `feat: download banners and trailer thumbnails`.

### Task 4: Comparison page and item page

**Files:** Modify `internal/library/changes.go`, `internal/web/item_view.go`, `internal/web/templates/item.html`, `internal/web/templates/sections.html`, `internal/web/templates/changes.html` (only if a new row shape is needed); Tests `internal/library/changes_test.go`, `internal/web/item_view_test.go` (or the existing item page test file).

**Interfaces:**
- Consumes: Tasks 1–3.
- Produces: change keys `banner`, `trailer`, `links` (whole-part rows; a missing incoming value is never a difference, as for the cover); `ApplyChanges` writes them and enqueues the cover-only sync (as taking a cover does) for remote images.

- [ ] **Step 1: Failing tests.**
  - `TestItemChangesBannerTrailerLinks`: stored empty, incoming set → three changes with `StoredEmpty`; stored equal → none; incoming empty → none; `ApplyChanges` with `["links"]` replaces links only.
  - Web: `TestItemPageShowsBannerTrailerLinks`: a kind with `Banner`, `Trailer`, a `SectionLinks` section; the page contains an `<img` with the local banner src, an `<a href="https://www.youtube.com/watch?v=x" ... rel="noopener noreferrer"` wrapping the thumbnail with text "Trailer", and each link label in the Links section next to the source link; no `youtube.com` in any `src=` attribute; a kind without `Banner` shows no banner even if stored. Alt titles appear in the Titles section under the label "Alternative titles".
- [ ] **Step 2:** Run `CGO_ENABLED=0 go test ./internal/library/ ./internal/web/ -run 'BannerTrailerLinks' -count=1`; FAIL.
- [ ] **Step 3:** Implement; banner full width above the header (`w-full` image with an existing radius/shadow token class), trailer tile in the info area; the "first stored title" fix of Task 2 already keeps alt titles out of the header.
- [ ] **Step 4:** PASS; full suite (includes `TestTemplatesUseOnlyThemedColours`, guard tests).
- [ ] **Step 5:** Commit `feat: banner, trailer and links on the item page and comparison page`.

### Task 5: AniList data in the anime, manga and light-novel kinds

**Files:** Modify `internal/plugins/anilist/client.go`, `internal/plugins/anilist/types.go`, `internal/plugins/anime/{manifest,mapping}.go`, `internal/plugins/manga/{manifest,mapping}.go`, `internal/plugins/book/{manifest,anilist}.go`; Tests in each package's existing `*_test.go` (client fixtures in `internal/plugins/anilist`).

**Interfaces:**
- Consumes: Task 1 (`AltTitles`, `Banner`, `Trailer`, `AdultField`, `ItemData` extras).
- Produces on `anilist.Media`: `Season string`, `Country string`, `Adult bool`, `AverageScore *int`, `Popularity *int`, `Synonyms []string`, `Studios []string` (animation studios only, main first), `StreamingLinks []LinkRef{Site, URL}`, `BannerURL string`, `Trailer *TrailerRef{ID, Site, Thumbnail}`; `Status` stays.
- GraphQL additions to `mediaFields`: `season countryOfOrigin isAdult averageScore popularity synonyms bannerImage trailer { id site thumbnail } externalLinks { url site type }`, and `studios(isMain: true)` replaced by `studios { edges { isMain node { name isAnimationStudio } } }` (keep `SearchResult.Studio` as the first main animation studio).
- Manifest fields (anime; manga and the book series kind the same minus `season` and `studio`):
  - `release_status` "Release status" enum `finished, releasing, upcoming, hiatus, cancelled` (from `FINISHED, RELEASING, NOT_YET_RELEASED, HIATUS, CANCELLED`), Show, Editable, Filterable
  - `season` "Season" enum `winter, spring, summer, fall`, Show, Editable, Filterable
  - `country` "Country" text (ISO code as given, `SingleLine` max 8), Show, Editable, Filterable
  - `adult` "Adult" enum `yes, no`, Show, Editable, Filterable; kind `AdultField: "adult"`
  - `community_score` "Community score" number 0–100, Unit "%", Show, Sortable
  - `popularity` "Popularity" number 0–100000000, Show, Sortable
  - `studio` becomes `FieldList` (same key and label "Studio"), Filterable
  - kinds set `AltTitles: true, Banner: true, Trailer: true`; the existing Links section (add one labelled "Links" where a kind lacks it).
- Mapping: synonyms → `plugin.Title{Lang: plugin.AltTitleLang}` (SingleLine, dropped when equal to another title, at most 20); streaming links → `ItemData.Links` only for `type == "STREAMING"`, label `site`, URL through `HTTPSURL`, at most 20; trailer URL `https://www.youtube.com/watch?v=<id>` for site `youtube`, `https://www.dailymotion.com/video/<id>` for `dailymotion`, id matching `^[A-Za-z0-9_-]{1,32}$`, else no trailer; thumbnail through `HTTPSURL`.

- [ ] **Step 1: Failing tests.** Client: `TestMediaParsesExtras` from a fixture JSON with every new field (two animation studios, one producer with `isAnimationStudio:false` → excluded; main first). Anime mapping: `TestItemDataExtras` asserting each field value above, alt titles, two streaming links (an `INFO` link dropped), the trailer URL. `TestTrailerSitesAndURLs`: youtube ok, dailymotion ok, `vimeo` → nil, id `"x y"` → nil, thumbnail `http://` → thumbnail empty. Manga and book: the same fields minus season/studio. Manifest tests updated (column/field lists).
- [ ] **Step 2:** Run `CGO_ENABLED=0 go test ./internal/plugins/... -count=1`; FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** PASS; full suite (guard: media words only in plugins).
- [ ] **Step 5:** Commit `feat: AniList release status, season, country, adult flag, scores, studios, synonyms, links, banner and trailer`.

### Task 6: IGDB release status

**Files:** Modify `internal/plugins/game/{igdb,mapping,manifest}.go`; Tests `igdb_test.go`, `mapping_test.go`, `manifest_test.go`.

**Interfaces:** field `release_status` "Release status" enum `released, early_access, upcoming, cancelled, delisted, other`, Show, Editable, Filterable; add `game_status.status` to `igdbGameFields`.

- [ ] **Step 1: Failing test** `TestGameReleaseStatus` (table): `Released`→released; `Alpha`, `Beta`, `Early Access`→early_access; `Cancelled`→cancelled; `Offline`, `Delisted`→delisted; `Rumored`→other; no status with `first_release_date` after today → upcoming; no status, past date → released; no status, no date → field absent.
- [ ] **Step 2:** Run `CGO_ENABLED=0 go test ./internal/plugins/game/ -count=1`; FAIL.
- [ ] **Step 3:** Implement (today via the mapper's clock parameter or `time.Now` injected for the test).
- [ ] **Step 4:** PASS; full suite.
- [ ] **Step 5:** Commit `feat: IGDB release status for games`.

### Task 7: Hide adult items

**Files:** Modify `internal/library/query.go` (Query), `internal/web/plugin_settings.go` and `templates/settings*.html`/plugin settings template, `internal/web/{list,home,pick,calendar,item_view,recommendations,search,add}.go` where items of a plugin are listed; Tests `internal/library/query_test.go`, `internal/web/*_test.go`.

**Interfaces:**
- Produces: settings key `plugin.<id>.hide_adult` (`"1"` on); `Query.HideField string` (the kind's `AdultField` when hiding is on: rows whose field value is `"yes"` are excluded); `func (s *server) hideAdult(ctx context.Context, m plugin.Manifest) bool`; route `POST /settings/plugins/{id}/adult` (CSRF like the other plugin settings posts).

- [ ] **Step 1: Failing tests.**
  - `TestListHidesAdultRows`: two items, one `adult=yes`; `Query{HideField:"adult"}` returns one; zero value returns both.
  - Web `TestHideAdultSetting`: settings page shows the checkbox only for plugins with an `AdultField` kind; on, the list, cards, home, random pick, calendar, Related and Recommendations sections and add-search results leave out the adult item; its item page still answers 200; stats counts unchanged; off by default.
- [ ] **Step 2:** Run the tests; FAIL.
- [ ] **Step 3:** Implement (summaries in Related/Recommendations/search carry `Fields`; filter on the same field value).
- [ ] **Step 4:** PASS; full suite.
- [ ] **Step 5:** Commit `feat: hide adult items setting`.

### Task 8: Additive fill core

**Files:** Create `internal/library/fill.go`; Test `internal/library/fill_test.go`.

**Interfaces:**
- Consumes: `changeParts`/`itemChanges` internals of `changes.go`, Task 2 storage.
- Produces: `func (l *Library) FillFromSource(ctx context.Context, itemID int64, in plugin.ItemData, credits []plugin.Credit, withCredits bool) (changed bool, err error)`: one transaction; single values (non-list fields, cover, banner, trailer) written only where stored is empty; lists (titles incl. alt, list fields, genres, tags, links, relations, credits) get the incoming elements the stored list lacks appended (identity: titles by lang+text, list fields/genres by value, tags by name, links by URL, relations by kind+source+id, credits by the `creditKey` of changes.go); nothing removed, reordered or changed; remote images enqueue nothing here (the caller's sync does). Not an entry event.

- [ ] **Step 1: Failing tests.**
  - `TestFillKeepsStoredValues`: stored title "Mine", synopsis "Mine", cover local file; incoming different → `changed == false` for those, values unchanged.
  - `TestFillFillsEmpty`: empty `release_status`, banner, trailer → filled; `changed == true`.
  - `TestFillListsAppendOnly`: stored genres `[B]`, incoming `[A, B, C]` → `[B, A, C]`; stored studio list `[X]`, incoming `[Y, X]` → `[X, Y]`; stored tag `T` rank 50, incoming `T` rank 90 → rank 50 kept; alt titles appended.
  - `TestFillNothingNew`: second call with the same data → `changed == false`, no writes (updated_at unchanged).
  - `TestFillRespectsBounds`: an appended list beyond the field's max values is cut at the max without error.
- [ ] **Step 2:** Run `CGO_ENABLED=0 go test ./internal/library/ -run Fill -count=1`; FAIL.
- [ ] **Step 3:** Implement, reusing the item write paths and validation of `ApplyChanges`.
- [ ] **Step 4:** PASS; full suite.
- [ ] **Step 5:** Commit `feat: fill an item from its source without changing stored values`.

### Task 9: Bulk "Update from source" run

**Files:** Create `internal/jobs/refresh_item.go`, `internal/library/refresh_runs.go`; Modify `internal/web/plugin_settings.go`, the plugin settings template, `cmd/allister/main.go` (register the handler only; no plugin imports added); Tests `internal/jobs/refresh_item_test.go`, `internal/library/refresh_runs_test.go`, `internal/web/plugin_settings_test.go`.

**Interfaces:**
- Consumes: Task 8 `FillFromSource`, `jobs.Queue.Enqueue`, `SyncJobFor`/`CoverJobFor`.
- Produces:
  - library: `type RefreshRun struct { ID int64; PluginID string; StartedAt time.Time; FinishedAt *time.Time; Total, Updated, Unchanged, Failed int; FailedTitles []string; Stopped string }`; `StartRefreshRun(ctx, pluginID string) (RefreshRun, []int64, error)` (counts sourced, non-manual items with an entry; refuses with `ErrRefreshRunning` while one is unfinished); `RecordRefresh(ctx, runID int64, outcome string, title string) error` with outcome `"updated" | "unchanged" | "failed"` (finishes the run when the counters reach Total; at most 50 failed titles kept); `StopRefreshRun(ctx, runID int64, reason string) error`; `LatestRefreshRun(ctx, pluginID string) (*RefreshRun, error)`.
  - jobs: `const KindRefreshItem = "refresh_item"`; payload `RefreshPayload{RunID, ItemID int64; PluginID string}` (plugin gate applies); handler `RefreshItem(deps SyncDeps, enqueueSync func(ctx, itemID) error) Handler`: Details (+People when the kind has a people section) → `FillFromSource` → record updated/unchanged, enqueue the item's sync when changed and images are remote; `ErrRateLimited`/`ErrUnavailable` → `ErrRetryLater`; `ErrNotFound`/`ErrBadResponse` → record failed; `ErrNotConfigured` → `StopRefreshRun(run, "credential")` and record failed for this and later jobs of the run without asking the source.
  - web: `POST /settings/plugins/{id}/refresh` (confirm step showing "Update N items from source?"), state shown on the plugin settings page: running "N of M", or the last result "Updated a, nothing new b, failed c" with the failed titles, or the credential notice.

- [ ] **Step 1: Failing tests.**
  - `TestStartRefreshRunCountsSourcedEntries` (manual items and items without entries excluded; second start while running refused).
  - `TestRefreshItemOutcomes`: fake source returning new data / same data / `ErrNotFound` / `ErrRateLimited` → counters updated/unchanged/failed, rate limit retried.
  - `TestRefreshRunStopsOnNotConfigured`.
  - `TestRefreshRunSurvivesRecover`: enqueue, `Recover`, run → totals add up and the run finishes.
  - `TestRefreshRunPausedWhilePluginDisabled`: jobs wait while the plugin is disabled, finish after enabling.
  - Web `TestPluginSettingsRefresh`: button only for plugins with a source; confirm shows the count; POST starts the run (303); the page shows progress and the final summary; a second POST while running shows "already running".
- [ ] **Step 2:** Run them; FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** PASS; full suite.
- [ ] **Step 5:** Commit `feat: bulk update from source in plugin settings`.

### Task 10: Backup format 5

**Files:** Modify `internal/backup/backup.go` (+ restore), `internal/library/backup.go`; Tests `internal/backup/backup_test.go`, `internal/library/backup_test.go`.

**Interfaces:** item backup JSON gains `banner`, `trailer` (`url`, `thumbnail`), `links`; banner and thumbnail files archived like covers (missing file → empty plus `banner_missing`/`thumbnail_missing` true); format constant 5; formats 1–4 restore.

- [ ] **Step 1: Failing tests** `TestBackupRoundTripBannerTrailerLinks` (backup, restore into an empty data dir, values and files equal) and `TestRestoreRefusesFutureFormat` updated so 6 is refused and 4 still restores.
- [ ] **Step 2:** Run `CGO_ENABLED=0 go test ./internal/backup/ ./internal/library/ -run 'Backup|Restore' -count=1`; FAIL.
- [ ] **Step 3:** Implement; update the format comment block in `backup.go`.
- [ ] **Step 4:** PASS; full suite.
- [ ] **Step 5:** Commit `feat!: backup format 5 for banners, trailers and links`.

### Task 11: Docs and changelog

**Files:** `docs/usage.md` (new fields and filters, hide adult, bulk update route), `docs/plugins.md` (contract: `alt_titles`, `banner`, `trailer`, `adult_field`, `banner_url`, `trailer`, `links`, reserved title language `alt`), `CHANGELOG.md` Unreleased (Added: release status filters for anime/manga/light novels/games; AniList extras; banner and trailer; hide adult items; bulk update from source. Changed: studio is a list of animation studios; backups are format 5 and v0.6.0 and older refuse them). README stays ≤150 lines.

- [ ] **Step 1:** Write the docs.
- [ ] **Step 2:** Run the full suite (docs tests), `go vet`, `gofmt -l .`, golangci-lint; all clean.
- [ ] **Step 3:** Commit `docs: source extras`.

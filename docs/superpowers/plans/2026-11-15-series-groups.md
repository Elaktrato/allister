# Series Groups Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Hand-built series groups in every list: a Series tab with derived progress and status, a series page to add, reorder and remove parts, series hints from plugin fields, and the grouping kept in backup and export.

**Architecture:** A core feature in `internal/library` (migration 00018: `series` and `series_parts`, one kind per series, one series per item), `internal/web` (series page, Series tab, part badge, item overview actions) and `internal/backup`. Plugins only declare data: the optional `KindDef.SeriesHint`. Progress and status are derived on read.

**Tech Stack:** Go 1.27, SQLite (modernc, goose migrations), html/template + htmx, Tailwind theme tokens; everything through `scripts/go.sh`.

**Spec:** `docs/superpowers/specs/2026-11-15-series-groups-design.md`

## Global Constraints

- Go is not installed: `scripts/go.sh test -count=1 ./...`, `scripts/go.sh vet ./...`, `scripts/go.sh fmt ./...`, `scripts/ci.sh lint` (0 issues) before each commit; output pristine.
- TDD: failing test first, run it to see the real RED, then implement.
- Migration number **00018** (00008 stays unused); never edit an applied migration.
- No plugin HTML/JS; no media words in core files (`internal/guard`); no inline scripts or `style`; colour classes only from `internal/appearance/tailwind-map.json`.
- htmx: `hx-history="false"` stays on `#list-rows`; the list keeps `Vary: HX-Request, HX-Target, HX-History-Restore-Request`.
- Series title: `plugin.SingleLine`, max 300 runes, refused (422) when empty after cleaning.
- Contract changes only: reserved source id `series`, optional `KindDef.SeriesHint`, optional `ExportEntry.Series`/`SeriesPosition`; every one JSON round-trip tested; `ContractVersion` stays 1.
- Backup `format_version` stays 1; `series` is a top-level member written after `items`.
- README ≤ 150 lines (docs test).
- Work on a branch; merge through a pull request (rebase), CI green. Commit messages `feat:`/`fix:`/`docs:`/`test:` with the `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>` trailer.

## Review Focus

1. **Two tabs at once:** the same entry is added to a series from two browser tabs → the second add is refused (`UNIQUE(item_id)`), answered 409 with a message, not a 500. *(Task 3: `TestAddPartAlreadyInSeries` asserts `ErrAlreadyInSeries`; Task 5: the web handler maps it to 409.)*
2. **Deleting a part from the list** (the existing Remove on a list row) → it disappears from its series, positions close up, and a series that loses its last part is gone. *(Task 3: `TestDeleteItemRenumbersAndDropsEmptySeries`.)*
3. **Disabling the plugin** of a series → the series page and Series tab answer like the plugin's other pages (guarded), and series of disabled plugins are not listed. *(Task 6: `TestSeriesRoutesAreGuarded`.)*
4. **Restoring a backup twice** → the second restore skips the existing series by uid and creates nothing new; parts already in another series are left out and reported. *(Task 8: `TestRestoreSeriesIdempotent`.)*
5. **Hostile titles and positions:** a series title with control characters or 301 runes is refused; a reorder of the first part "up" or the last part "down" is a no-op 303, not an error; a non-numeric or foreign position/item id is 404. *(Task 3 and Task 5 tests named below.)*

---

### Task 1: Contract and the books manifest

**Files:**
- Modify: `internal/plugin/contract.go` (KindDef, new SeriesHintDef), `internal/plugin/validate.go` (reserved source id, hint validation), `internal/plugin/data.go` (ExportEntry)
- Modify: `internal/plugins/book/manifest.go` (hint on the book kind; light-novel labels)
- Test: `internal/plugin/contract_json_test.go`, `internal/plugin/validate_test.go`, `internal/plugins/book/manifest_test.go`
- Docs: `docs/plugins.md` (SeriesHint, reserved `series`), README/`docs/usage.md`/`docs/handoff.md` ("Light novels")

**Interfaces:**
- Produces:
  - `type SeriesHintDef struct { TitleField string \`json:"title_field"\`; PositionField string \`json:"position_field,omitempty"\` }`
  - `KindDef.SeriesHint *SeriesHintDef \`json:"series_hint,omitempty"\``
  - `ExportEntry.Series string \`json:"series,omitempty"\``, `ExportEntry.SeriesPosition int \`json:"series_position,omitempty"\``
  - `reservedSourceIDs` contains `"series"`

- [ ] **Step 1: Write the failing tests**
  - `TestKindSeriesHintJSON`: a KindDef with `SeriesHint{TitleField:"series", PositionField:"series_number"}` marshals to contain `"series_hint":{"title_field":"series","position_field":"series_number"}`; a KindDef without one has no `series_hint`; round trip equal. Add the hint to the "full" manifest of `TestManifestJSONRoundTrip`.
  - `TestExportEntrySeriesJSON`: `Series:"The Expanse", SeriesPosition:3` → `"series":"The Expanse","series_position":3`; zero values omitted.
  - `TestValidateSeriesHint`: errors for an unknown title field, a title field that is not `FieldText`, an unknown position field, a position field that is not `FieldNumber`, an empty `TitleField`; no error for a valid hint and for a hint without `PositionField`.
  - `TestValidateRefusesSeriesSourceID`: a source with id `series` is refused with "reserved by the core".
  - In `internal/plugins/book/manifest_test.go`: the book kind's `SeriesHint` is `{series, series_number}`; the `series` kind's `Label`/`LabelPlural` are `"Light novel"`/`"Light novels"` and its `Key` is still `"series"`.
- [ ] **Step 2: Run** `scripts/go.sh test -count=1 ./internal/plugin/ ./internal/plugins/book/` — expect FAIL (undefined `SeriesHintDef`, labels).
- [ ] **Step 3: Implement** the types, `validateKind` hint checks (the hint refers only to fields of the same kind, so it fits in `validateKind`), the reserved id, the book manifest changes, and the docs (`docs/plugins.md` KindDef table + reserved ids line; "Light novels" wherever the docs name the light-novel kind).
- [ ] **Step 4: Run** the full suite, vet, fmt, lint — expect PASS (README still ≤ 150).
- [ ] **Step 5: Commit** `feat: series hints in the contract; relabel the light-novel kind`

### Task 2: Migration 00018 and the series store

**Files:**
- Create: `internal/db/migrations/00018_series.sql` (the two tables and the index exactly as in the spec's Data model)
- Create: `internal/library/series.go`, `internal/library/series_test.go`
- Modify: `internal/db` migration tests if they list the latest number

**Interfaces:**
- Produces (all in `internal/library`):
  - `type Series struct { ID int64; UID, PluginID, Kind, Title string; CreatedAt, UpdatedAt time.Time }`
  - `var ErrAlreadyInSeries = errors.New("library: the entry is already in a series")`, `var ErrNoSeries = errors.New("library: no such series")`
  - `func (l *Library) CreateSeries(ctx context.Context, itemID int64, title string) (Series, error)` — the item must have an entry (else `ErrNoEntry`), must be in no series (`ErrAlreadyInSeries`); title cleaned with `plugin.SingleLine(title, 300)`, empty → `ErrInvalid`; uid from `ids.NewManualID`; the item becomes position 1.
  - `func (l *Library) SeriesByUID(ctx context.Context, pluginID, uid string) (*Series, error)` — `ErrNoSeries` when missing or of another plugin.
  - `func (l *Library) SeriesOfItem(ctx context.Context, itemID int64) (*Series, int, error)` — the series and the item's position; `nil, 0, nil` when none.
  - `func (l *Library) RenameSeries(ctx context.Context, id int64, title string) error`

- [ ] **Step 1: Write the failing tests** in `series_test.go`:
  - `TestCreateSeries`: creating from an entry gives uid non-empty, `PluginID`/`Kind` of the item, title trimmed; `SeriesOfItem` returns it with position 1.
  - `TestCreateSeriesRefuses`: item without entry → `ErrNoEntry`; item already in a series → `ErrAlreadyInSeries`; title `"  "`, `"a\x00b"` or 301 runes → `ErrInvalid`.
  - `TestSeriesByUIDOtherPlugin`: the uid under another plugin id → `ErrNoSeries`.
  - `TestRenameSeries`: renamed title stored, `UpdatedAt` advanced; invalid title → `ErrInvalid`.
- [ ] **Step 2: Run** `scripts/go.sh test -count=1 ./internal/library/ ./internal/db/` — expect FAIL.
- [ ] **Step 3: Implement** the migration and the four methods (one transaction each).
- [ ] **Step 4: Run** the full suite, vet, fmt, lint — PASS.
- [ ] **Step 5: Commit** `feat: series store (migration 00018)`

### Task 3: Parts: add, move, remove, delete, and the cascade

**Files:**
- Modify: `internal/library/series.go`, `internal/library/series_test.go`, `internal/library/library.go` (`DeleteItem`)

**Interfaces:**
- Consumes: Task 2.
- Produces:
  - `func (l *Library) AddPart(ctx context.Context, seriesID, itemID int64, positionField string) error` — same plugin and kind as the series (else `ErrInvalid`), has an entry (`ErrNoEntry`), in no series (`ErrAlreadyInSeries`). Placement: when `positionField != ""` and the item has that number field, insert after the last part whose own number is ≤ the item's (parts without a number count as "before"); otherwise append. Renumber densely.
  - `func (l *Library) MovePart(ctx context.Context, seriesID, itemID int64, delta int) error` — delta −1/+1; moving past either end is a no-op (nil); an item not in that series → `ErrNoSeries`.
  - `func (l *Library) RemovePart(ctx context.Context, seriesID, itemID int64) error` — keeps the entry; renumbers; deletes the series when it was the last part.
  - `func (l *Library) DeleteSeries(ctx context.Context, seriesID int64, removeEntries bool) error` — `removeEntries` deletes every part item (as `DeleteItem` does), else only the series.
  - `DeleteItem` renumbers the item's former series and drops it when empty, in the same transaction.

- [ ] **Step 1: Write the failing tests:**
  - `TestAddPartPlacesByHint`: parts numbered 1 and 3 (field `series_number`), adding one with 2 lands at position 2; one without a number lands at the end; with `positionField == ""` always the end.
  - `TestAddPartRefuses`: other kind or plugin → `ErrInvalid`; no entry → `ErrNoEntry`.
  - `TestAddPartAlreadyInSeries`: an item in series A added to series B → `ErrAlreadyInSeries` (Review Focus 1).
  - `TestMovePart`: [a,b,c] move b −1 → [b,a,c]; a −1 → unchanged, nil error; c +1 → unchanged, nil (Review Focus 5).
  - `TestRemovePartKeepsEntry`: entry still exists; positions dense; removing the last part deletes the series (`SeriesByUID` → `ErrNoSeries`).
  - `TestDeleteSeries`: `false` keeps all entries ungrouped; `true` removes the part items.
  - `TestDeleteItemRenumbersAndDropsEmptySeries` (Review Focus 2).
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the full suite, vet, fmt, lint — PASS.
- [ ] **Step 5: Commit** `feat: series parts (add, move, remove, delete)`

### Task 4: Listing series with derived progress and status

**Files:**
- Modify: `internal/library/series.go`, `internal/library/series_test.go`

**Interfaces:**
- Consumes: Tasks 2–3, `Row`, `Status`.
- Produces:
  - `func DeriveSeriesStatus(parts []Status) Status` — pure; the six rules of the spec's "Derived values", in order.
  - `type SeriesRow struct { Series Series; Parts, Completed int; Status Status; Cover string; Updated time.Time }`
  - `type SeriesQuery struct { PluginID string; Text string; Status Status; Sort string /* "" (updated) or "title" */; Page, PageSize int }`
  - `type SeriesPage struct { Rows []SeriesRow; Total, Page, Pages int }`
  - `func (l *Library) ListSeries(ctx context.Context, q SeriesQuery) (SeriesPage, error)` — every kind of the plugin; text matches the series title or any part's titles (case-insensitive substring, like the list); status filters on the derived status; pagination like `List` (page past the end clamps).
  - `func (l *Library) SeriesCounts(ctx context.Context, q SeriesQuery) (map[Status]int, error)` — ignores `q.Status`; sums to the series count.
  - `func (l *Library) SeriesParts(ctx context.Context, seriesID int64, lang string) ([]Row, error)` — position order; the existing `Row` shape (reuse `loadRowParts`).
  - `func (l *Library) HasSeries(ctx context.Context, pluginID string) (bool, error)`

- [ ] **Step 1: Write the failing tests:**
  - `TestDeriveSeriesStatus` table: `[dropped,dropped]`→dropped; `[completed,completed,dropped]`→completed; `[planned,planned]`→planned; `[completed,active,planned]`→active; `[completed,paused]`→paused; `[completed,planned]`→active.
  - `TestListSeries`: two series in a plugin, one in another plugin; rows carry `Parts`, `Completed`, derived `Status`, the first part's cover, the newest part update; default order newest first; `Sort:"title"` by title.
  - `TestListSeriesText`: matches the series title ("Expanse") and a part title; no match → empty page.
  - `TestListSeriesStatusAndCounts`: status filter on the derived status; `SeriesCounts` values sum to `Total`.
  - `TestSeriesParts`: rows in position order.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement** (derive per series from its parts' statuses in Go after one batched query; the series count per plugin is small).
- [ ] **Step 4: Run** the full suite, vet, fmt, lint — PASS.
- [ ] **Step 5: Commit** `feat: list series with derived progress and status`

### Task 5: The series page and its actions

**Files:**
- Create: `internal/web/series.go`, `internal/web/series_test.go`, `internal/web/templates/series.html`
- Modify: `internal/web/item.go` (routes in the plugin route set)

**Interfaces:**
- Consumes: Tasks 1–4.
- Produces (routes, all under the plugin prefix and its enabled guard):
  - `GET /series/{uid}` — the page (title, derived "3/7" and status, parts with cover/title/status/progress/rating linking to `itemURL`, actions below).
  - `POST /series/{uid}/rename` (`title`) → 303 to the page; invalid → 422 page with the message.
  - `POST /series/{uid}/parts` (`item` = item id from "From your list") → 303; already in a series → 409 with "This entry is already in a series."; other plugin/kind/no entry → 404.
  - `POST /series/{uid}/parts/{item}/move` (`dir` = `up`|`down`) → 303 (no-op at the ends).
  - `POST /series/{uid}/parts/{item}/remove` → 303 to the page, or to the plugin list when the series is gone.
  - `POST /series/{uid}/delete` (`entries` = `keep`|`remove`) → 303 to the plugin list with `tab=series`.
  - `GET /series/{uid}/candidates?q=` — htmx fragment: entries of the same plugin and kind in no series; entries whose hint title field equals the series title (case-insensitive, trimmed) first; at most 20.
  - helper `func seriesURL(pluginID, uid string) string` → `"/" + pluginID + "/series/" + uid`.

- [ ] **Step 1: Write the failing tests** (`newFixtureEnv`, the TV fixture plugin):
  - `TestSeriesPageShowsParts`: create a series from an entry through the library, add two parts; GET shows the title, "1/3" (or the completed count), the parts in order with their links.
  - `TestSeriesRenameAndRefusals`: rename works; `"\x01"` → 422; unknown uid → 404 (Review Focus 5).
  - `TestSeriesAddFromList`: POST parts adds; the same item again → 409 (Review Focus 1); an item of another plugin → 404; `item=abc` → 404.
  - `TestSeriesMoveRemoveDelete`: up/down reorder; up on the first part → 303 and unchanged; remove the last part → 303 to the list; delete with `entries=keep` keeps the entries, with `entries=remove` removes them.
  - `TestSeriesCandidatesPreferHint`: with a hint declared on the fixture kind, a candidate whose hint field equals the series title is listed first; entries already in a series are not listed.
- [ ] **Step 2: Run** `scripts/go.sh test -count=1 -run TestSeries ./internal/web/` — FAIL.
- [ ] **Step 3: Implement** the handlers, the template (existing overview/list styles, theme colour classes only, the delete confirmation as a small form with the two choices, the move buttons as forms) and the routes.
- [ ] **Step 4: Run** the full suite (incl. `TestTemplatesUseOnlyThemedColours`, guard), vet, fmt, lint — PASS.
- [ ] **Step 5: Commit** `feat: series page with add, reorder, remove and delete`

### Task 6: Item overview, search-add into a series, and the guard

**Files:**
- Modify: `internal/web/item.go`, `internal/web/item_view.go`, `internal/web/templates/item.html`, `internal/web/add.go`, `internal/web/search.go` (carry `series`), `internal/web/series.go`
- Test: `internal/web/series_test.go`

**Interfaces:**
- Consumes: Task 5 (`seriesURL`, routes).
- Produces:
  - `POST /{source}/{id}/series` (`title`) — "Make this a series": creates the series with the saved entry as part 1 → 303 to `seriesURL`; prefill of the form's title: the kind's `SeriesHint.TitleField` value when set, else the display title.
  - The overview of a part shows "Part N of <series title>" linking to the series page, previous/next part links, and a "Remove from series" form posting to Task 5's remove route.
  - Search with `?series=<uid>` keeps the parameter on result links and the add form; a successful add with it calls `AddPart` (with the kind's hint `PositionField`) after `AddToList`/`attachEntry`, then redirects to the series page. An already-listed item found through search is attached without a second add.

- [ ] **Step 1: Write the failing tests:**
  - `TestMakeSeriesFromOverview`: POST creates the series, 303 to its page; the overview of an entry not in the list has no "Make this a series".
  - `TestMakeSeriesPrefillsFromHint`: the form's title value is the hint field's text.
  - `TestPartOverviewShowsSeriesNav`: "Part 2 of", links to the series and to parts 1 and 3.
  - `TestSearchAddIntoSeries`: `POST /tv/tvdata/5/add?series=<uid>` adds and attaches, 303 to the series page; an unknown uid → 404 and nothing saved.
  - `TestSeriesRoutesAreGuarded`: with the plugin disabled, `GET /tv/series/<uid>` answers like the plugin's other pages (Review Focus 3).
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the full suite, vet, fmt, lint — PASS.
- [ ] **Step 5: Commit** `feat: make an entry a series; add into a series from search`

### Task 7: The Series tab and the part badge

**Files:**
- Modify: `internal/web/list.go`, `internal/web/templates/list.html`, `internal/web/templates/list_row.html`, `internal/web/templates/list_cards.html`
- Create: `internal/web/templates/series_rows.html` (series row + expanded parts fragment)
- Test: `internal/web/series_list_test.go`

**Interfaces:**
- Consumes: Task 4 (`ListSeries`, `SeriesCounts`, `SeriesParts`, `HasSeries`), Task 5 (`seriesURL`).
- Produces:
  - `GET /{plugin}/?tab=series` (with `q`, `status`, `sort`, `view`, `page`) renders the Series tab; `GET /series/{uid}/parts` returns the expanded parts fragment (htmx; without htmx the expand control is a link to the series page).
  - The tab bar: kind tabs as today plus "Series" when `HasSeries`; a one-kind plugin shows "All | Series" (only once it has a series).
  - `rowView` gains `Series string; SeriesPos int; SeriesHref string` for the badge "N · <title>".

- [ ] **Step 1: Write the failing tests:**
  - `TestSeriesTabListsSeries`: rows with title, "x/y", derived status; status tabs by derived status with counts summing to All; `q` matches a series title.
  - `TestSeriesTabHiddenWithoutSeries`: no "Series" tab link before the first series exists.
  - `TestSeriesTabExpandFragment`: the parts fragment lists the parts in order; the page sets `hx-history="false"` on `#list-rows` and the `Vary` header as before.
  - `TestPartBadgeOnEntryTab`: a part's row on its kind tab shows "2 · <title>" linking to the series; a standalone entry has no badge.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement** (no `+1` on series rows; card view: a series card links to the series page).
- [ ] **Step 4: Run** the full suite, vet, fmt, lint — PASS.
- [ ] **Step 5: Commit** `feat: Series tab and series badges in the list`

### Task 8: Backup, restore and export

**Files:**
- Modify: `internal/library/backup.go` (writer and restore helpers), `internal/backup/backup.go` (top-level `series` member, report), `internal/library/export.go`, `internal/plugins/jsonexport/exporter.go`
- Test: `internal/backup/backup_test.go`, `internal/library/series_test.go`, `internal/plugins/jsonexport/exporter_test.go`
- Docs: `docs/usage.md` (backup and export), `docs/handoff.md` (feature bullet), README one line if it fits

**Interfaces:**
- Consumes: Tasks 2–4.
- Produces:
  - `type BackupSeries struct { UID, PluginID, Kind, Title string \`json:...\`; CreatedAt, UpdatedAt time.Time; Parts []BackupPartRef }`, `type BackupPartRef struct { PluginID, Kind, Source, SourceID string }` (json names as in the spec).
  - `func (l *Library) BackupSeries(ctx context.Context, fn func(BackupSeries) error) error`
  - `func (l *Library) RestoreSeries(ctx context.Context, bs BackupSeries) (created bool, leftOut int, err error)` — skip when the uid exists; resolve parts by identity; leave out missing parts and parts in another series; create nothing when no part resolves.
  - `Report.SeriesCreated, Report.SeriesSkipped int` and a note per left-out part.
  - `ExportEntries` fills `ExportEntry.Series`/`SeriesPosition`; the JSON exporter writes them.

- [ ] **Step 1: Write the failing tests:**
  - `TestBackupRoundTripKeepsSeries`: backup → restore into an empty library → the same series, titles and part order.
  - `TestRestoreSeriesIdempotent`: restoring the same archive again creates no series (`SeriesSkipped`), no duplicates (Review Focus 4).
  - `TestRestoreSeriesLeavesOutTakenParts`: a part already in another series is left out and noted; a series with no resolvable part is not created.
  - `TestRestoreWithoutSeriesMember`: an archive without `series` restores as before.
  - `TestExportCarriesSeries`: an exported entry has `"series"` and `"series_position"`; a standalone one has neither.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement**: write `series` after `items`; in restore, accept `series` only after `items` (like `appearance`, a misplaced member is an error) and apply it after the items.
- [ ] **Step 4: Run** the full suite, vet, fmt, lint, `wc -l README.md` — PASS, ≤ 150.
- [ ] **Step 5: Commit** `feat: series in backup, restore and export`

### Task 9: Preview and whole-branch check

- [ ] **Step 1:** `PORT=18083 docker compose -p allister-series up -d --build`; enable Books; create a series from a book, add a part from the list and one from search, reorder, check the Series tab (expand, status tabs), the part badge, delete with "keep"; `docker compose -p allister-series down -v`.
- [ ] **Step 2:** `scripts/ci.sh all` — every check green (fmt, tidy, vet, lint, test, vuln, hadolint, actionlint, shell, gitleaks).
- [ ] **Step 3:** Open the pull request (labels `feat`), wait for CI, rebase-merge, close issue #1 with a comment linking the PR.

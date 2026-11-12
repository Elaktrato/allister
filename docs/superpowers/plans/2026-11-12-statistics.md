# Statistics Page and Activity Heatmap Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a core Statistics page with completion-based totals/averages, per-kind and per-dimension breakdowns, a monthly/yearly SVG time chart, a rating distribution and a 12-month activity heatmap; add anime episode duration from AniList and a per-item Update from source action.

**Architecture:** A data-only `StatsDef` on `KindDef` describes how a kind derives time and units. A new `internal/stats` package aggregates `library.Row`s. A new `entry_events` table records entry changes for the heatmap and future activity metrics. The page is a core `/stats` route rendered from manifests; no plugin HTML/JS and no new JavaScript.

**Tech Stack:** Go, SQLite (modernc), html/template, htmx/Tailwind, inline SVG charts. Go runs only through `scripts/go.sh`.

**Spec:** `docs/superpowers/specs/2026-11-12-statistics-design.md`

## Global Constraints

- No new Go dependencies. HTTP via the standard library.
- Go runs only through `scripts/go.sh`; never a host `go`. `-race` is unavailable.
- Contract types stay serialisable data; the full-manifest JSON round trip covers `StatsDef`; contract version stays 1.
- No plugin HTML/JS. Core Go/HTML/templates stay media-agnostic (the guard scans them).
- No inline scripts. No inline `style` attributes. SVG dimensions/positions/opacities are attributes.
- Templates use only colour classes in `internal/appearance/tailwind-map.json`; SVG marks use `fill="currentColor"` with an allowed `text-*` class.
- Migrations strictly increase; **00008 stays unused; the new migration is 00017**, and the next number after this work is 00018.
- README stays at or below 150 lines.
- No event backfill: activity starts with this release. Imports and restores are not heatmap activity.
- Disabled plugins are excluded from the stats page and heatmap.
- Every task ends with `scripts/go.sh test -count=1 ./...`, `scripts/go.sh vet ./...`, `scripts/go.sh fmt ./...` clean and pristine, then a commit.
- Preview only on port 18083 / project `allister-preview`; the owner's 18080-18082 containers stay untouched.

## Review Focus

These inputs or failure modes are most likely to bite a person using the software; each has a test in the owning task.

1. **Period boundaries and undated entries.** A finished date exactly on `from`/`to` must be included; an entry with no finished date must be in All time totals but not a period or the time chart; a completed entry with no finished date must be counted in the page's note.
2. **Event origins and heatmap counting.** A manual `+1`, a bulk progress save, a completion, an import update, a restore and a progress decrease must each land in the right origin and the heatmap must count only manual progress increases and completions.
3. **Time/unit computation.** Completed movies use runtime over position; in-progress movies use position (capped by runtime); TV/anime use episodes x runtime/duration; a completed entry with zero units falls back to the total; missing runtime/duration is "missing", never zero; inactive progress keys are ignored.
4. **Backup events.** The newest 5,000 events per entry are backed up, `EventsTruncated` is set when older ones are dropped, restored events keep their origin, malformed events fail the item, and an existing item is skipped without merging events.
5. **Stats filters and rendering.** `media=all` ignores per-kind `f.*` filters with a notice; invalid media/period/status/date values are ignored with a notice; the heatmap follows only the media filter; the page uses mapped colours and no inline styles.

---

## File Structure

**Contract**
- Modify `internal/plugin/contract.go` — `StatsDef`, `StatsTimeDef`, `StatsUnitsDef`, `KindDef.Stats`.
- Modify `internal/plugin/validate.go` — `validateStats`.
- Modify `internal/plugin/contract_json_test.go`, `internal/plugin/validate_test.go`.
- Modify `internal/plugin/plugintest/plugintest.go` — `WithStats`.

**Plugins**
- Modify `internal/plugins/anilist/types.go`, `client.go`, `client_test.go` — `duration`.
- Modify `internal/plugins/anime/manifest.go`, `mapping.go` and tests — duration field, Stats.
- Modify the manga, book, tv, movie and game manifests and tests — Stats.
- Modify `internal/plugins/tv/manifest.go` — runtime `Unit: "min"`.

**Library**
- Create `internal/db/migrations/00017_entry_events.sql`.
- Create `internal/library/events.go`, `events_test.go`.
- Modify `internal/library/entries.go`, `progress.go`, `entries_test.go`, `progress_test.go` — event writes.
- Modify `internal/library/query.go`, `query_test.go` — `StatsRows`.
- Modify `internal/library/details.go`, `details_test.go` — `FillMissingFields`.
- Modify `internal/library/backup.go`, `backup_test.go` — event backup/restore.
- Modify `internal/db/sync_jobs_test.go` — `latestVersion`.

**Stats package**
- Create `internal/stats/stats.go`, `stats_test.go`.

**Web**
- Create `internal/web/stats.go`, `stats_chart.go`, `stats_test.go`, `stats_chart_test.go`, `templates/stats.html`.
- Modify `internal/web/server.go` — `/stats` and page registration.
- Modify `internal/web/plugins.go`, `templates/nav.html` — Statistics nav row.
- Modify `internal/web/layout_test.go` — sidebar expectations.
- Modify `internal/web/item.go`, `item_view.go`, `templates/item.html`, `item_test.go` — Update from source.

**Docs**
- Modify `README.md`, `docs/usage.md`, `docs/plugins.md`, `docs/handoff.md`, `AGENTS.md`.

---

### Task 1: Statistics contract

**Files:**
- Modify: `internal/plugin/contract.go`
- Modify: `internal/plugin/validate.go`
- Modify: `internal/plugin/contract_json_test.go`
- Modify: `internal/plugin/validate_test.go`
- Modify: `internal/plugin/plugintest/plugintest.go`

**Interfaces:**
- Consumes: `KindDef`, `ProgressSpec`, `ProgressDef`, `FieldDef`.
- Produces:

```go
type StatsDef struct {
    Time  *StatsTimeDef  `json:"time,omitempty"`
    Units *StatsUnitsDef `json:"units,omitempty"`
}
type StatsTimeDef struct {
    Label          string `json:"label"`
    DurationKey    string `json:"duration_key,omitempty"`
    CompletedField string `json:"completed_field,omitempty"`
    UnitsKey       string `json:"units_key,omitempty"`
    PerUnitField   string `json:"per_unit_field,omitempty"`
}
type StatsUnitsDef struct {
    Key         string `json:"key"`
    LabelPlural string `json:"label_plural"`
}
// KindDef gains: Stats *StatsDef `json:"stats,omitempty"`
// plugintest gains: func WithStats(s plugin.StatsDef) Option
```

- [ ] **Step 1: Write the failing JSON round-trip and validation tests**

In `contract_json_test.go`, add a `d` `duration` progress to the full fixture and add to its kind:

```go
Stats: &plugin.StatsDef{
    Time: &plugin.StatsTimeDef{Label: "Time", DurationKey: "d", CompletedField: "n", UnitsKey: "x", PerUnitField: "n"},
    Units: &plugin.StatsUnitsDef{Key: "x", LabelPlural: "Units"},
},
```

In `validate_test.go`, add a valid Stats fixture and rejection cases for: empty `StatsDef`; `Time` with no source; `UnitsKey` without `PerUnitField`; `DurationKey` naming a non-duration progress; `UnitsKey` naming a non-count/pages progress; `CompletedField`/`PerUnitField` naming an unknown or non-number field; `Units.Key` missing or the wrong type; empty/too-long/control-character labels. Assert the error path names `stats`, `stats.time`, `stats.units`, or the offending field.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `scripts/go.sh test ./internal/plugin/`
Expected: FAIL — `undefined: plugin.StatsDef` / validation cases not rejected.

- [ ] **Step 3: Implement the types and validation**

Add the types above. In `validateKind`, call `validateStats(p, path+".stats", k)`. `validateStats` enforces every rule in the spec: at least one of Time/Units; Time label; a complete time source; unit-pair together; progress/field types; Units key and label. Use the existing `keyRe`, `isControl`, and 40-rune label helpers. Add `WithStats` to `plugintest` setting `p.M.Kinds[0].Stats = &s`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `scripts/go.sh test ./internal/plugin/`
Expected: PASS.

- [ ] **Step 5: Full verification and commit**

Run: `scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...`
Expected: all packages pass, vet and fmt silent.
Commit: `feat: add the statistics contract`

---

### Task 2: AniList episode duration and anime stats

**Files:**
- Modify: `internal/plugins/anilist/types.go`
- Modify: `internal/plugins/anilist/client.go`
- Modify: `internal/plugins/anilist/client_test.go`
- Modify: `internal/plugins/anime/manifest.go`
- Modify: `internal/plugins/anime/mapping.go`
- Modify: `internal/plugins/anime/manifest_test.go`
- Modify: `internal/plugins/anime/mapping_test.go`
- Modify: `internal/plugins/anime/source_test.go`

**Interfaces:**
- Consumes: Task 1's `StatsDef`.
- Produces: `anilist.SearchResult.Duration *int`; anime field `duration` (number, `min`, 1-10000); anime `Stats` with `Time{Label:"Watchtime", UnitsKey:"episode", PerUnitField:"duration"}` and `Units{Key:"episode", LabelPlural:"Episodes"}`.

- [ ] **Step 1: Write the failing tests**

Add to the AniList client tests: the search and detail queries contain `duration`; a response `{"duration":24}` decodes to `SearchResult.Duration == 24`. Add to the anime manifest test: the `duration` field exact type/unit/range and the exact Stats. Add to the anime mapping tests: `summaryFields`/`ItemData` map `Duration` 24 to `duration: 24`; `0` and `10001` are dropped. Update the exact-query-body assertions in `source_test.go`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `scripts/go.sh test ./internal/plugins/anilist/ ./internal/plugins/anime/`
Expected: FAIL — no `Duration`, no query field, no manifest field/Stats.

- [ ] **Step 3: Implement**

Add `Duration *int` to `SearchResult`; add `duration` to `searchFields` and `mediaFields`; add `Duration *int` to `apiMedia` and copy it in `searchResult()`. Add the anime manifest field and Stats. Add `duration` to `summaryFields` using `c.numField(r.Duration, 1, 10000)`. Update the tests' fixtures.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `scripts/go.sh test ./internal/plugins/anilist/ ./internal/plugins/anime/`
Expected: PASS.

- [ ] **Step 5: Full verification and commit**

Run the full suite, vet and fmt. Commit: `feat: collect anime episode duration`

---

### Task 3: Statistics declarations for the other kinds

**Files:**
- Modify: `internal/plugins/manga/manifest.go`, `manifest_test.go`
- Modify: `internal/plugins/book/manifest.go`, `manifest_test.go`
- Modify: `internal/plugins/tv/manifest.go`, `manifest_test.go`
- Modify: `internal/plugins/movie/manifest.go`, `manifest_test.go`
- Modify: `internal/plugins/game/manifest.go`, `manifest_test.go`

**Interfaces:**
- Consumes: Task 1's types.
- Produces: the exact `Stats` table:

| Kind | Stats |
|---|---|
| Manga | `Units{Key:"chapter", LabelPlural:"Chapters"}` |
| Book | `Units{Key:"pages", LabelPlural:"Pages"}` |
| Series | `nil` |
| Show | `Time{Label:"Watchtime", UnitsKey:"episode", PerUnitField:"runtime"}`, `Units{Key:"episode", LabelPlural:"Episodes"}`, runtime `Unit:"min"` |
| Movie | `Time{Label:"Watchtime", CompletedField:"runtime", DurationKey:"position"}` |
| Game | `Time{Label:"Playtime", DurationKey:"playtime"}` |

- [ ] **Step 1: Write the failing manifest assertions**

Each manifest test asserts its kind's Stats exactly (pointer nil for Series) and TV's runtime `Unit == "min"`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `scripts/go.sh test ./internal/plugins/manga/ ./internal/plugins/book/ ./internal/plugins/tv/ ./internal/plugins/movie/ ./internal/plugins/game/`
Expected: FAIL — Stats nil and TV runtime unit empty.

- [ ] **Step 3: Implement the declarations**

Add the Stats above; set TV runtime `Unit: "min"`. Do not add Stats to Series.

- [ ] **Step 4: Run the tests to verify they pass**

Run the focused tests. Expected: PASS.

- [ ] **Step 5: Full verification and commit**

Run the full suite, vet and fmt. Commit: `feat: declare statistics for the media kinds`

---

### Task 4: Entry event log and write paths

**Files:**
- Create: `internal/db/migrations/00017_entry_events.sql`
- Modify: `internal/db/sync_jobs_test.go:9` (`latestVersion = 17`)
- Create: `internal/library/events.go`
- Create: `internal/library/events_test.go`
- Modify: `internal/library/entries.go`, `internal/library/entries_test.go`
- Modify: `internal/library/progress.go`, `internal/library/progress_test.go`

**Interfaces:**
- Consumes: `Entry`, `writeEntryMode`, `txItem`, `itemMeta`.
- Produces:

```go
type EventOrigin string
const (
    OriginManual  EventOrigin = "manual"
    OriginImport  EventOrigin = "import"
    OriginRestore EventOrigin = "restore"
)
type EntryOption func(*entryOptions)
func WithEventOrigin(o EventOrigin) EntryOption
func (l *Library) UpsertEntry(ctx context.Context, e Entry, opts ...EntryOption) error
// internal:
func insertEvent(ctx context.Context, tx *sql.Tx, itemID int64, eventType, progressKey string,
    oldNum, newNum *float64, oldText, newText string, origin EventOrigin) error
func recordEvents(ctx context.Context, tx *sql.Tx, itemID int64, old *Entry, e Entry, origin EventOrigin) error
func entryTx(ctx context.Context, tx *sql.Tx, itemID int64) (*Entry, error)
```

- [ ] **Step 1: Write the migration and failing tests**

Schema (exact):

```sql
CREATE TABLE entry_events (
    id INTEGER PRIMARY KEY,
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    occurred_at TEXT NOT NULL,
    event_type TEXT NOT NULL CHECK (event_type IN ('progress','status','rating')),
    progress_key TEXT NOT NULL DEFAULT '',
    old_num REAL,
    new_num REAL,
    old_text TEXT NOT NULL DEFAULT '',
    new_text TEXT NOT NULL DEFAULT '',
    origin TEXT NOT NULL DEFAULT 'manual' CHECK (origin IN ('manual','import','restore'))
);
CREATE INDEX entry_events_item ON entry_events(item_id);
CREATE INDEX entry_events_time ON entry_events(origin, occurred_at);
```

Add tests: `TestIncrementRecordsProgressAndStatusEvents`; `TestIncrementAtTotalWritesNoEvent`; `TestUpsertEntryRecordsChanges`; `TestUpsertEntryCreationWritesNoEvents`; `TestApplyImportUsesImportOrigin`; `TestUpdateEntryRecordsChanges`; `TestInvalidEntryWritesNoEvent`. Use a `readEvents(t, lib, itemID)` helper that selects `event_type, progress_key, old_num, new_num, old_text, new_text, origin` ordered by id.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `scripts/go.sh test ./internal/db/ ./internal/library/`
Expected: FAIL — no `entry_events` table / no event code.

- [ ] **Step 3: Implement the event writes**

Implement the schema and helpers. In `UpsertEntry`, accept options (default manual), read the old entry inside the transaction, validate, write, then `recordEvents`; skip recording when the old entry is nil. In `UpdateEntry`, do the same inside its existing transaction. In `Increment`, read the old status and primary value before the SQL, execute, read the new values, and insert a progress event when the value increased and a status event when the status changed; write nothing when the SQL affected no row. `ApplyImport` passes `WithEventOrigin(OriginImport)` to `UpsertEntry`. Add/import/restore creation paths write no events.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `scripts/go.sh test ./internal/db/ ./internal/library/`
Expected: PASS.

- [ ] **Step 5: Full verification and commit**

Run the full suite, vet and fmt. Commit: `feat: record entry events`

---

### Task 5: Activity queries

**Files:**
- Modify: `internal/library/events.go`, `internal/library/events_test.go`

**Interfaces:**
- Produces:

```go
type KindRef struct{ PluginID, Kind string }
// ActivityDays counts manual consumption events by UTC day. from is
// inclusive, to exclusive; an empty scope means every kind.
func (l *Library) ActivityDays(ctx context.Context, from, to time.Time, scope []KindRef) (map[string]int, error)
func (l *Library) FirstActivityDay(ctx context.Context) (string, bool, error)
```

- [ ] **Step 1: Write the failing tests**

Use a direct `insertEventAt(t, lib, itemID, occurredAt, eventType, key, oldNum, newNum, oldText, newText, origin)` helper (raw SQL with an explicit timestamp). Add: `TestActivityDaysCountsProgressAndCompletions`; `TestActivityDaysExcludesImportsAndDecreases`; `TestActivityDaysFiltersScope`; `TestActivityDaysGroupsByUTCDayAndRange`; `TestFirstActivityDay`. Assert a status completion with `old_text="active"` counts, a progress decrease does not, and a `to` exactly at midnight excludes that day.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `scripts/go.sh test ./internal/library/`
Expected: FAIL — `ActivityDays` undefined.

- [ ] **Step 3: Implement the SQL**

Query `entry_events` joined to `items`, `origin='manual'`, `occurred_at >= from` and `< to`, and `(progress AND new_num > old_num) OR (status AND new_text='completed' AND old_text<>'completed')`; group by `substr(occurred_at,1,10)`. Add the scope as an `OR` of `(plugin_id=? AND kind=?)`. `FirstActivityDay` is `MIN(substr(occurred_at,1,10))` over manual events.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `scripts/go.sh test ./internal/library/`
Expected: PASS.

- [ ] **Step 5: Full verification and commit**

Run the full suite, vet and fmt. Commit: `feat: query activity days`

---

### Task 6: Backup and restore events

**Files:**
- Modify: `internal/library/backup.go`, `internal/library/backup_test.go`

**Interfaces:**
- Produces:

```go
type BackupEvent struct {
    OccurredAt  string   `json:"occurred_at"`
    Type        string   `json:"type"`
    ProgressKey string   `json:"progress_key,omitempty"`
    OldNum      *float64 `json:"old_num,omitempty"`
    NewNum      *float64 `json:"new_num,omitempty"`
    OldText     string   `json:"old_text,omitempty"`
    NewText     string   `json:"new_text,omitempty"`
    Origin      string   `json:"origin,omitempty"`
}
// BackupEntry gains:
Events          []BackupEvent `json:"events,omitempty"`
EventsTruncated bool          `json:"events_truncated,omitempty"`
```

- [ ] **Step 1: Write the failing tests**

Add `TestBackupIncludesEvents`; `TestBackupKeepsNewestEvents` (insert 5,001, expect 5,000, `EventsTruncated`, newest kept); `TestRestoreCreatesEvents` (fresh library, query the restored events); `TestRestoreRejectsBadEvent` (bad type/origin/timestamp/progress key -> `RestoreFailed`); `TestRestoreSkipsExistingItemEvents` (existing item skipped, no events added).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `scripts/go.sh test ./internal/library/`
Expected: FAIL — no `BackupEvent`/events loaded.

- [ ] **Step 3: Implement**

In `BackupItems`, query events for the batch with `ORDER BY item_id, id DESC`; append to the entry until 5,000, then set `EventsTruncated` and skip older rows. In `planRestore`, validate at most 5,000 events per entry, the type/origin enums, RFC 3339 timestamps, eligible `progress_key` for progress events, and finite numbers. In `writeRestore`, insert the events after the entry with their stored timestamp/origin. Keep the existing backup format version (additive optional fields).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `scripts/go.sh test ./internal/library/`
Expected: PASS.

- [ ] **Step 5: Full verification and commit**

Run the full suite, vet and fmt. Commit: `feat: back up entry events`

---

### Task 7: Stats row loading

**Files:**
- Modify: `internal/library/query.go`, `internal/library/query_test.go`

**Interfaces:**
- Produces:

```go
var ErrTooManyRows = errors.New("too many rows to aggregate")
const maxStatsRows = 20000
func (l *Library) StatsRows(ctx context.Context, q Query, lang string) ([]Row, error)
// internal: func (l *Library) statsRows(ctx context.Context, q Query, lang string, limit int) ([]Row, error)
```

- [ ] **Step 1: Write the failing tests**

Add `TestStatsRowsReturnsAllMatching` (seed more than one page of rows; all returned), `TestStatsRowsAppliesFilters` (status/genre), and `TestStatsRowsRefusesOverLimit` (call `statsRows` with limit 2 after seeding 3; expect `ErrTooManyRows`).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `scripts/go.sh test ./internal/library/`
Expected: FAIL — `StatsRows` undefined.

- [ ] **Step 3: Implement**

Compile the query, count, refuse above the limit, then `loadRows` with `LIMIT -1 OFFSET 0` in item-id order. `StatsRows` wraps `statsRows(..., maxStatsRows)`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `scripts/go.sh test ./internal/library/`
Expected: PASS.

- [ ] **Step 5: Full verification and commit**

Run the full suite, vet and fmt. Commit: `feat: load rows for statistics`

---

### Task 8: FillMissingFields and the item Update from source action

**Files:**
- Modify: `internal/library/details.go`, `internal/library/details_test.go`
- Modify: `internal/web/item.go`, `internal/web/item_view.go`
- Modify: `internal/web/templates/item.html`
- Modify: `internal/web/item_test.go`

**Interfaces:**
- Consumes: `detailsMeta`, `normalizeFields`, `insertFields`, `Source.Details`.
- Produces:

```go
func (l *Library) FillMissingFields(ctx context.Context, itemID int64, fields map[string]plugin.FieldValue) ([]string, error)
// web: POST /{source}/{id}/refresh in coreRoutes
// itemView gains UpdateURL, UpdateLabel; getItem reads ?updated=N
```

- [ ] **Step 1: Write the failing library tests**

Add `TestFillMissingFieldsAddsOnlyMissing` (existing `runtime` 120; source `runtime` 100 + `duration` 24; returns `["duration"]`, runtime stays 120), `TestFillMissingFieldsValidates` (unknown/wrong-shape field), `TestFillMissingFieldsNoChange` (returns nil), `TestFillMissingFieldsUnknownItem`.

- [ ] **Step 2: Run the library tests to verify they fail**

Run: `scripts/go.sh test ./internal/library/`
Expected: FAIL — `FillMissingFields` undefined.

- [ ] **Step 3: Implement the library method**

In one write-locking transaction, read `detailsMeta`, load the kind, `normalizeFields` the source fields, drop rows whose key is in the stored set, insert the rest, and bump `items.updated_at` only when something was added. Return the added keys sorted.

- [ ] **Step 4: Write the failing web tests**

Add `TestItemRefreshAddsMissingFields` (saved anime, no duration; `e.fp.anime.Duration = 24`; POST `/anime/anilist/1535/refresh` -> 303 to `?updated=1`; item has 24; page says a field was added), `TestItemRefreshKeepsExistingFields`, `TestItemRefreshManualItemNotFound`, `TestItemRefreshSourceError` (`ErrNotConfigured` -> 503 with the Settings hint), `TestItemShowsRefreshActionOnlyForSourcedItems`.

- [ ] **Step 5: Run the web tests to verify they fail**

Run: `scripts/go.sh test ./internal/web/`
Expected: FAIL — no refresh route/form.

- [ ] **Step 6: Implement the web action**

Add the route, a `postRefresh` handler that loads the saved item, resolves the source, calls `Details`, calls `FillMissingFields`, and redirects to the item page with `updated=N`. Use `itemFail` for source errors. Add `UpdateURL`/`UpdateLabel` to the item view for saved items with a non-manual implemented source. Add the form under Edit details and render the `updated` notice. A manual/unknown/unsaved item returns 404 on the POST.

- [ ] **Step 7: Run the tests to verify they pass**

Run: `scripts/go.sh test ./internal/library/ ./internal/web/`
Expected: PASS.

- [ ] **Step 8: Full verification and commit**

Run the full suite, vet and fmt. Commit: `feat: refresh missing item metadata`

---

### Task 9: Statistics aggregation package

**Files:**
- Create: `internal/stats/stats.go`
- Create: `internal/stats/stats_test.go`

**Interfaces:**
- Consumes: Task 1's `StatsDef`; `library.Row`, `library.Item`, `library.Entry`.
- Produces:

```go
type BucketMode string
const (
    BucketAuto  BucketMode = "auto"
    BucketMonth BucketMode = "month"
    BucketYear  BucketMode = "year"
)
type Input struct {
    PluginID string
    Kind     plugin.KindDef
    Rows     []library.Row
}
type Options struct {
    From, To *time.Time
    Bucket   BucketMode
}
type Totals struct {
    Entries, Completed, Rated, RatingSum, TimeEntries, MissingTime, UnitEntries, UndatedCompleted int
    Statuses map[plugin.StatusClass]int
    RatingAverage float64
    RatingDistribution [11]int
    TimeSeconds, Units float64
    TimeLabel, UnitLabel string
}
type Bucket struct { Label string; Seconds float64 }
type Dimension struct {
    Value string
    Entries, Rated, RatingSum int
    TimeSeconds, Units float64
}
type DimensionGroup struct { Label string; Values []Dimension }
type KindReport struct {
    PluginID, Kind, Label string
    Totals
    Buckets    []Bucket
    Dimensions []DimensionGroup
}
type Report struct {
    Kinds  []KindReport
    All    Totals
    Buckets []Bucket
    Genres, Tags []Dimension
}
func Build(inputs []Input, opts Options) Report
```

- [ ] **Step 1: Write the failing tests**

Table-drive the time rules: movie completed runtime 120 -> 7200; movie position 1800 -> 1800; movie completed with runtime missing and position 3000 -> 3000; movie position 9000 with runtime 120 -> 7200; TV episodes 10 x runtime 30 -> 18000; anime episodes 12 x duration 24 -> 17280; game playtime 3600 -> 3600; completed TV with episode 0 and total 12 -> 21600; missing runtime/duration -> not known; inactive key -> not known. Unit rules: pages/chapters, completed-zero fallback to total, inactive ignored. Add period inclusivity, ratings (0 vs nil), averages/missing counts, continuous buckets, dimensions/case-insensitive merge/duplicate once, all-media unit omission, Series-without-Stats.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `scripts/go.sh test ./internal/stats/`
Expected: FAIL — package/types missing.

- [ ] **Step 3: Implement**

Filter rows by `FinishedOn` when From/To are set (inclusive; nil dates excluded). Compute per-kind totals, buckets (continuous, month/year, undated excluded), dimensions (genres, tags, every `Filterable` non-date field; list values separately; text verbatim; duplicate values once), and the All totals. When one of From/To is nil, the bucket range uses the min/max `FinishedOn` of the selected rows on the open side. `BucketAuto` chooses month when the resolved span is <= 24 months, else year; All time (both nil) uses the rows' min/max and the caller's yearly mode. Use only active progress keys. Time order: completed `CompletedField` minutes -> active `DurationKey` seconds (movie position capped by runtime) -> `UnitsKey` x `PerUnitField` minutes -> unknown. Units: active key, completed-zero total fallback. Do not sum units across kinds in `All`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `scripts/go.sh test ./internal/stats/`
Expected: PASS.

- [ ] **Step 5: Full verification and commit**

Run the full suite, vet and fmt. Commit: `feat: aggregate statistics`

---

### Task 10: Statistics page core

**Files:**
- Create: `internal/web/stats.go`, `internal/web/stats_test.go`
- Create: `internal/web/templates/stats.html`
- Modify: `internal/web/server.go` (`pageNames`, `GET /stats`)
- Modify: `internal/web/plugins.go`, `internal/web/templates/nav.html`
- Modify: `internal/web/layout_test.go`

**Interfaces:**
- Consumes: Task 7's `StatsRows`; Task 9's `stats.Build`; Task 5's `ActivityDays`/`FirstActivityDay` (heatmap fields prepared here, rendered in Task 11); Task 1's `StatsDef`; `library.Terms`.
- Produces:

```go
type statsKind struct {
    PluginID string
    Manifest plugin.Manifest
    Kind     plugin.KindDef
}
type statsQuery struct {
    Media, Period string
    From, To      *time.Time
    Status        *library.Status
    Genres, Tags  []string
    GenresAll, TagsAll bool
    Fields        []fieldFilter
    Notices       []string
}
type statsPage struct {
    // summary cards, per-kind rows, dimension tables, notices and empty state;
    // Task 11 adds the time-chart, rating and heatmap fields to this struct.
}
func (s *server) getStats(w http.ResponseWriter, r *http.Request)
func (s *server) statsScope(ctx context.Context) []statsKind
func (s *server) parseStatsQuery(r *http.Request, now time.Time, scope []statsKind) statsQuery
func (s *server) buildStatsPage(ctx context.Context, q statsQuery) (*statsPage, error)
// navView gains Stats navItem; navItems sets Label "Statistics", Href "/stats", Icon "bar_chart".
```

- [ ] **Step 1: Write the failing page tests**

Add `TestStatsPageRendersSummary` (entries/completed/time/average/rating cards), `TestStatsPageMediaFilter`, `TestStatsPagePeriodFilter` (2025 vs 2026 with the env clock at 2026-01-01), `TestStatsPageStatusFilter`, `TestStatsPageGenreAndTagFilters`, `TestStatsPageFieldFilter`, `TestStatsPageAllMediaComparison`, `TestStatsPageDimensions`, `TestStatsPageInvalidParamsNotice`, `TestStatsPageAllMediaIgnoresFieldFilters`, `TestStatsPageEmpty`, and `TestStatsNavRow`. Update `layout_test.go` nav counts/order and add `/stats` to the aria-current cases.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `scripts/go.sh test ./internal/web/`
Expected: FAIL — `/stats` 404 and nav row missing.

- [ ] **Step 3: Implement the handler, parser and template core**

Add `stats` to `pageNames`; mount `GET /stats`. `statsScope` lists enabled kinds in registry order. Parse `media` (empty/all or `plugin/kind`; a bare plugin for one-kind plugins), `period` (`all/year/12m/custom`), `from`/`to` (YYYY-MM-DD; present dates imply custom; invalid or reversed ranges are notices), `status` (core status), `genre`/`tag` with modes, and `f.<field>` only for a specific kind. Build one `library.Query` per kind, call `StatsRows`, and `stats.Build` with the period and bucket mode (month for year/12m, auto for custom, year for all time). Build the filter view (media/period/status/genre/tag/fields), summary cards (entries, completed with completion rate, time/units, average time/units, average rating, rated/unrated; hide time/unit cards when the kind has no such metric), per-kind comparison with kind-name links to the media filter, dimension tables with top 10 values and a Show all disclosure, the missing-time notice pointing at the item Update from source action, notices, and empty states. The status labels are the kind's for a specific kind and neutral core labels for All media. Add the nav row in the bottom list before Calendar. The page is a plain GET form; no htmx.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `scripts/go.sh test ./internal/web/`
Expected: PASS.

- [ ] **Step 5: Full verification and commit**

Run the full suite, vet and fmt. Commit: `feat: add the statistics page`

---

### Task 11: Time chart, rating distribution and activity heatmap

**Files:**
- Create: `internal/web/stats_chart.go`, `internal/web/stats_chart_test.go`
- Modify: `internal/web/stats.go`, `internal/web/stats_test.go`
- Modify: `internal/web/templates/stats.html`

**Interfaces:**
- Consumes: Task 10's `statsPage`; Task 9's `stats.Bucket`; Task 5's `ActivityDays`/`FirstActivityDay`.
- Produces: pure builders used by the page:

```go
type timeChartView struct { Bars []chartBar; MaxLabel string; Total string; Table []bucketRow }
type chartBar struct { Label string; Seconds float64; Height int; Y int; Title string }
type ratingRow struct { Score, Count int; BarWidth int; Unrated bool }
type heatmapView struct { Weeks [][]heatmapDay; Months []heatmapMonth; Since string; Empty bool }
type heatmapDay struct { Date string; Count, Level int; Title string }
func buildTimeChart(buckets []stats.Bucket) *timeChartView
func buildRatingRows(dist [11]int, unrated int) []ratingRow
func buildHeatmap(counts map[string]int, from, to time.Time, since string) heatmapView
```

- [ ] **Step 1: Write the failing tests**

Add pure tests: `TestTimeChartBars` (bucket heights, empty -> nil), `TestRatingRows` (0-10 plus unrated), `TestHeatmapGrid` (53x7 grid, two active cells, level/title, Monday start), `TestHeatmapEmpty`. Add page tests: `TestStatsPageShowsTimeChart`, `TestStatsPageHidesChartWithoutTime`, `TestStatsPageShowsRatingDistribution`, `TestStatsPageHeatmapShowsTrackedSince`, `TestStatsPageHeatmapEmptyState`, `TestStatsPageHeatmapFollowsMediaFilter`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `scripts/go.sh test ./internal/web/`
Expected: FAIL — builders and page sections missing.

- [ ] **Step 3: Implement**

Build the chart as inline SVG with attribute geometry (no `style`), `fill="currentColor"` under `text-indigo-400`, a `<title>`/`<desc>`, and a `<details>` table. Rating rows use a small SVG bar per row. The heatmap is a 53-column x 7-row SVG, Monday-first, month labels, weekday labels, five fill-opacity levels, `<title>` per cell, a legend, and a visually hidden active-day table. The handler fetches `ActivityDays` for the last 12 months (`from` inclusive, `to` exclusive; pass the day after the window end) and `FirstActivityDay`, applies the media scope only, and fills `statsPage`. Hide the chart when the scope has no time or all buckets are zero.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `scripts/go.sh test ./internal/web/`
Expected: PASS.

- [ ] **Step 5: Full verification and commit**

Run the full suite, vet and fmt. Commit: `feat: chart statistics and activity`

---

### Task 12: Documentation

**Files:**
- Modify: `README.md`, `docs/usage.md`, `docs/plugins.md`, `docs/handoff.md`, `AGENTS.md`

- [ ] **Step 1: Update the docs**

README: one sentence for the Statistics page/heatmap and the update action; keep <= 150 lines by trimming an older sentence. `docs/usage.md`: `/stats` parameters, completion-based periods, All-time vs period, ratings 0 vs unrated, heatmap tracked-since/UTC/import exclusion, backup event cap, anime duration and Update from source. `docs/plugins.md`: `StatsDef`/`StatsTimeDef`/`StatsUnitsDef`, validation, time order, generic example. `docs/handoff.md`: page, migration 00017, event log, anime duration, per-item refresh, Series deferral. `AGENTS.md`: next migration 00018 and a line for the stats page/event log.

- [ ] **Step 2: Verify**

Run: `scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./... && wc -l README.md`
Expected: all packages pass; README <= 150.

- [ ] **Step 3: Commit**

Commit: `docs: statistics page and activity heatmap`

---

### Task 13: Preview and final verification

**Files:** none (verification only; fix any issue found).

- [ ] **Step 1: Build and open the preview**

`PORT=18083 docker compose -p allister-preview up -d --build`; enable the relevant plugins; use the existing preview test data plus a few entries with known finished dates, ratings and progress. Verify:
- `/stats` renders for All media and each kind.
- Period/year/12m/custom filters, status, genre/tag and per-kind field filters; invalid params show the notice.
- Summary cards, per-kind table, dimension tables, time chart, rating distribution.
- Heatmap starts empty; a manual `+1` and a completion add cells; an import/restore does not.
- An existing anime item's Update from source adds `duration` (or shows the source error page when AniList is unavailable); an existing non-empty field is unchanged.
- Backup download includes `events`; a fresh restore creates them.

- [ ] **Step 2: Final suite**

Run: `scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...`
Expected: 24+ packages pass; vet/fmt silent; `git status` clean except the ledger.

- [ ] **Step 3: Record the verification**

Append the commands, observations and any accepted limitations to the SDD progress ledger. The controller then runs the whole-branch review and fix wave before merge.

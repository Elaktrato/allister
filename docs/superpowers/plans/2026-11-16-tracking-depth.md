# Tracking Depth Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Runs (repeat viewings with their own dates, score, review and progress), a read-only timeline, entry notes / reviews / unit notes with safe formatting, own tags and cross-plugin custom lists.

**Architecture:** Migration 00019 moves status, score, dates and progress from `entries` into `entry_runs` / `run_progress`; `entries` keeps membership, notes and a maintained `display_run_id`. Two SQL views (`entry_state`, `display_progress`) present the display run in the old column shape, so the ~34 read sites in `internal/library` move by renaming a table, and `library.Entry` stays the Go view of "the display run plus entry notes". Everything new is generic core (no media words); plugins only add `KindDef.Repeat` labels.

**Tech Stack:** Go 1.27, SQLite (modernc), goose migrations, html/template + htmx, Tailwind theme tokens. No new module dependency.

**Spec:** `docs/superpowers/specs/2026-11-16-tracking-depth-design.md` (approved). Read it before any task; this plan only adds decisions.

## Global Constraints

- One migration, `internal/db/migrations/00019_runs.sql`; never edit 00001-00018; 00008 stays unused; `latestVersion` in `internal/db/sync_jobs_test.go` becomes 19.
- Core Go/HTML files must not contain media words (`internal/guard`); "Rewatch", "Reread", "Replay", "Rewatching" etc. live only in plugin manifests.
- Contract types stay serialisable (JSON round-trip tests in `internal/plugin`); only `cmd/allister` imports plugins.
- No inline scripts or `style`; handlers in `internal/web/static/app.js`; colour classes only from `internal/appearance/tailwind-map.json`.
- Limits: entry notes 10,000 runes, run review 10,000 runes, unit note 2,000 runes, tag name 1-40 runes single line, list name 1-80 runes single line (Claude's call, same rules as series titles).
- Statuses stay the five core values; NULL score != 0; dates are calendar days.
- Every commit green: `scripts/go.sh test ./...`, `scripts/go.sh vet ./...`, `scripts/go.sh fmt ./...` (no diff); run `scripts/ci.sh all` before the last commit. RED run shown before each implementation.
- Commit prefixes `feat:`/`fix:`/`test:`/`docs:`; breaking changes use `feat!:` (the Tasks 1+2 commit and Task 12).
- Agents never push or tag.

## Review Focus

1. **+1 or the dialog while the latest run is an abandoned repeat**: must change the display (completed) run only, never the dropped run. Test in Task 4.
2. **An import or AniList daily repeat import on an entry with several runs**: writes the display run, never creates a run, never touches past runs. Test in Task 2.
3. **Restoring the same v2 backup twice, and a v1 backup into a migrated library**: second restore skips items and adds no duplicate list items or tags. Test in Task 12.
4. **Formatting input that tries to escape**: `[x](javascript:alert(1))`, `<script>`, unclosed `**`/`||`, `||` inside a link, HTML entities. Rendered as escaped text, never a tag outside the allowed set. Test + fuzz in Task 8.
5. **Tag and list names differing only by case or Unicode folding** (`Comfort` / `comfort` / `COMFORT`, `Ä`/`ä`): one tag / list, stored as first typed. Test in Tasks 10 and 11.

---

### Task 1: Migration 00019 (schema, conversion, views)

**Files:**
- Create: `internal/db/migrations/00019_runs.sql`
- Create: `internal/db/runs_migration_test.go`
- Modify: `internal/db/sync_jobs_test.go` (`latestVersion = 19`)

**Interfaces:**
- Produces tables (spec "Data model"): `entries(item_id, notes, added_at, updated_at, display_run_id)`, `entry_runs(id, item_id, number, status, rating, review, started_on, finished_on, created_at, updated_at)`, `run_progress(run_id, key, value)`, `entry_events.run_id` (nullable, ON DELETE SET NULL), `unit_notes`, `user_tags`, `entry_user_tags`, `lists`, `list_items`.
- Produces views: `entry_state(item_id, status, rating, started_on, finished_on, updated_at, notes, run_id, run_number, review, repeats)` where status..finished_on and review come from the display run, `updated_at` is `MAX(entries.updated_at, run.updated_at)`, `repeats` = count of completed runs; `display_progress(item_id, key, value)` = `run_progress` of the display run.
- Indexes: `entry_runs(item_id, number)` unique, `entry_runs(status)`, `entry_events(run_id)`, `list_items(item_id)`, `entry_user_tags(tag_id)`.
- `added_at` for converted entries = their old `updated_at` (no better data; Claude's call).

- [ ] **Step 1: Write the failing test** `TestRunsMigrationConvertsEntries`: `openAt(t, path, 18)`, seed 4 items + entries: completed rating 8 with both dates and comment "c1"; active rating NULL; dropped rating 0; planned with no dates. Seed `entry_progress` with an active key and an inactive key (value kept), and 3 `entry_events`. Open at latest. Assert per item: exactly one `entry_runs` row with `number = 1` and the same status/rating (NULL vs 0 kept)/dates/updated_at; `entries.notes = 'c1'`; `display_run_id` = that run; `run_progress` holds both keys with their values; every event has that `run_id`; `entry_state` returns the old values; `display_progress` returns both keys. `TestRunsMigrationDown`: `goose.DownTo(d, "migrations", 18)` restores `entries` columns and `entry_progress` from the display run (comment from notes).
- [ ] **Step 2: Run** `scripts/go.sh test ./internal/db/ -run RunsMigration -count=1` — Expected: FAIL (no 00019; version 18).
- [ ] **Step 3: Write `00019_runs.sql`** — Up: create new tables, `INSERT ... SELECT` from `entries`/`entry_progress`, rebuild `entries` (create `entries_new`, copy, drop, rename; keep FK and the cascade from items), add `entry_events.run_id` and fill it, drop `entry_progress`, create the views. Use `-- +goose StatementBegin/End` where needed; `PRAGMA foreign_keys` handling as goose runs in a transaction (check how 00007 did a rebuild). Down: the reverse from display runs.
- [ ] **Step 4: Run** the test and `scripts/go.sh test ./internal/db/ -count=1` — Expected: PASS. (Other packages fail now; that is Task 2's RED.)
- [ ] **Step 5: Commit** together with Task 2 (the tree is not green between them; commit message `feat!: runs replace the single entry state (migration 00019)`).

### Task 2: Library moves to runs (no new features yet)

**Files:**
- Modify: `internal/library/entries.go`, `progress.go`, `events.go`, `query.go`, `series.go`, `schedule.go`, `convert.go`, `export.go`, `backup.go` (read/write SQL only; backup *format* changes are Task 12)
- Create: `internal/library/runs.go` (display-run maintenance)
- Test: `internal/library/runs_test.go`; existing tests updated only for the `Comment` -> `Notes` rename

**Interfaces:**
- `Entry` gains `Notes string` (replaces `Comment`), `Review string`, `RunID int64`, `RunNumber int`, `Repeats int` (completed runs). `MaxCommentRunes` is replaced by `MaxNotesRunes = 10000` and `MaxReviewRunes = 10000`.
- `func refreshDisplayRun(ctx context.Context, tx *sql.Tx, itemID int64) error` in `runs.go`: applies the spec's Display run rule and writes `entries.display_run_id`. Called by every run write in the same transaction.
- `func displayRunTx(ctx context.Context, tx *sql.Tx, itemID int64) (int64, error)`.
- Reads use `entry_state` / `display_progress` in place of `entries` / `entry_progress`. Writes: entry-level fields (notes, updated_at) to `entries`; status/rating/dates/review/progress to the **display run** (`entry_runs` / `run_progress`); creating an entry creates run 1. `insertEvent` gains a `runID int64` argument (written to `entry_events.run_id`).
- Web/templates/importers/plugins: rename `Comment` -> `Notes` where they use `library.Entry` (`plugin.ExportEntry.Comment` stays until Task 12).

- [ ] **Step 1: Write failing tests** in `runs_test.go`: `TestNewEntryCreatesRunOne` (AddToList -> one run number 1, display_run_id set); `TestUpsertWritesDisplayRun` (seed two runs via SQL: run 1 completed, run 2 active; UpsertEntry changes run 2 only); `TestDisplayRunSkipsDroppedRepeat` (run 1 completed 8, run 2 dropped -> Entry shows completed, 8, run 1 dates; Increment changes run 1's progress only, run 2 untouched — Review Focus 1); `TestApplyImportNeverAddsRun` (entry with 2 runs; `ApplyImport` with new status -> still 2 runs, run 1 unchanged — Review Focus 2); `TestDisplayRunInvariant` (after a mixed sequence of writes, for every entry `display_run_id` equals the rule recomputed in Go).
- [ ] **Step 2: Run** `scripts/go.sh test ./internal/library/ -count=1` — Expected: compile/test FAIL.
- [ ] **Step 3: Implement** the moves above. Keep every exported signature except the `Comment` rename.
- [ ] **Step 4: Run** `scripts/go.sh test ./... -count=1`, `vet`, `fmt` — Expected: all PASS, including the untouched list/stats/home/series/import tests (they prove nothing regressed).
- [ ] **Step 5: Commit** Tasks 1+2: `git add internal/db internal/library internal/web internal/plugins ... && git commit -m "feat!: runs replace the single entry state (migration 00019)"`.

### Task 3: Contract: `RepeatDef`, plugin labels, reserved id

**Files:**
- Modify: `internal/plugin/contract.go` (KindDef), `internal/plugin/validate.go`, `internal/plugin/contract_json_test.go`, `internal/plugin/registry.go` (reserved ids)
- Modify: the six plugin manifests under `internal/plugins/{anime,manga,book,tv,movie,game}/`
- Modify: `docs/plugins.md`

**Interfaces:**
- `type RepeatDef struct { Action string \`json:"action"\`; Marker string \`json:"marker"\` }`; `KindDef.Repeat *RepeatDef \`json:"repeat,omitempty"\``.
- `func (k KindDef) RepeatAction() string` (default `"Start again"`), `func (k KindDef) RepeatMarker() string` (default `"Again"`).
- Labels: anime/tv Show/movie: `Rewatch`/`Rewatching`; manga, book (both kinds): `Reread`/`Rereading`; game: `Replay`/`Replaying`.
- Validation: both non-empty single line <= 40 runes when Repeat is set.
- Reserved plugin ids: add `"lists"` next to `"version"`.

- [ ] **Step 1: Failing tests**: JSON round trip with Repeat set and nil; validation rejects empty Action; defaults; registry refuses plugin id `lists`; each shipped manifest declares Repeat (a test in each plugin package or one in `cmd/allister`).
- [ ] **Step 2: Run** `scripts/go.sh test ./internal/plugin/... ./internal/plugins/... -count=1` — FAIL.
- [ ] **Step 3: Implement**; document `repeat` in `docs/plugins.md`.
- [ ] **Step 4: Run** full suite — PASS.
- [ ] **Step 5: Commit** `feat: repeat labels in the plugin contract`.

### Task 4: Library run API

**Files:**
- Modify: `internal/library/runs.go`; Test: `internal/library/runs_test.go`

**Interfaces:**
- `type Run struct { ID int64; Number int; Status Status; Rating *int; Review string; StartedOn, FinishedOn *time.Time; Progress map[string]float64; CreatedAt, UpdatedAt time.Time }`
- `func (l *Library) Runs(ctx context.Context, itemID int64) ([]Run, error)` — newest first.
- `func (l *Library) StartRun(ctx context.Context, itemID int64, today time.Time) (Run, error)` — refuses (ErrInvalid) unless the display run is completed; new run: active, started today, rating copied from the display run, progress empty, review empty; status event completed->active with origin manual on the new run.
- `type RunUpdate struct { Status Status; Rating *int; Review string; StartedOn, FinishedOn *time.Time }`
- `func (l *Library) UpdateRun(ctx context.Context, runID int64, u RunUpdate) error` — for runs that are not the display run only completed or dropped allowed; records status/rating events with that run_id.
- `func (l *Library) DeleteRun(ctx context.Context, runID int64) error` — refuses when it is the entry's only run; renumbers densely; events keep (run_id NULL); refreshes display run.
- `var ErrNoRun = errors.New("library: no such run")`.

- [ ] **Step 1: Failing tests**: `TestStartRun` (fields as above; second StartRun while active -> ErrInvalid); `TestStartRunAfterDroppedRepeat` allowed; `TestDeleteLatestRunUndoesStart` (display back to run 1, events still counted by `ActivityDays`); `TestDeleteOnlyRunRefused`; `TestDeleteRenumbers` (delete run 2 of 3 -> numbers 1,2); `TestUpdatePastRunStatusLimited` (active on a past run -> ErrInvalid); stale id -> ErrNoRun.
- [ ] **Step 2: Run** `scripts/go.sh test ./internal/library/ -run Run -count=1` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** full suite — PASS.
- [ ] **Step 5: Commit** `feat: start, edit and delete runs`.

### Task 5: Web: start again, the dialog question, markers, run history

**Files:**
- Modify: `internal/web/entries.go`, `internal/web/plugins.go` (routes), `internal/web/item_view.go`, `internal/web/list.go` (row view), templates `entry_dialog.html`, `item.html`, `list_row.html`, `list_cards.html`, `home.html`
- Create: `internal/web/runs.go`, `internal/web/templates/run_history.html`; Test: `internal/web/runs_test.go`

**Interfaces:**
- Routes under each plugin: `POST /list/{id}/runs` (start again), `GET|POST /list/{id}/runs/{run}/edit`, `POST /list/{id}/runs/{run}/delete`.
- `rowView` gains `RepeatMarker string` (set when the display run is active and `RunNumber > 1`) and `Repeats int` (shown as `x<n>` when >= 2).
- Dialog question (owner decision A): `postEdit` with a status change from completed to planned/active/paused and no `run_choice` form value answers 200 with the dialog in a confirm state offering two submit buttons, `run_choice=new` ("<RepeatAction> instead") and `run_choice=correct` ("No, correct this one"). `new` = `StartRun` then apply the dialog's other values to the new run; `correct` = today's update.
- Item page: "Start again" button (label `RepeatAction()`) when the display run is completed; run history from `Runs` with edit/delete (delete confirms, not offered with one run).

- [ ] **Step 1: Failing tests**: start-again POST creates run 2 and redirects/partials like +1; card and row show marker and `x2`; dialog completed->active without choice shows both buttons and changes nothing; `run_choice=new` -> 2 runs; `run_choice=correct` -> 1 run, status active; history lists runs newest first; delete of only run not offered and refused (400); stale run id -> 404.
- [ ] **Step 2: Run** `scripts/go.sh test ./internal/web/ -run 'Run|Repeat' -count=1` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** full suite (guard and colour tests included) — PASS.
- [ ] **Step 5: Commit** `feat: start again, run history and repeat markers`.

### Task 6: Statistics per run

**Files:**
- Modify: `internal/library/query.go` (`StatsRows`), `internal/stats/stats.go`; Test: `internal/stats/stats_test.go`, `internal/web/stats` tests if numbers are asserted there

**Interfaces:**
- `Row` gains `PastRuns []Run` — filled only by `StatsRows` (every run except the display run).
- `stats.Build`: time/units per run as the spec says (display run as today, plus each past run filtered and bucketed by its own `FinishedOn` and narrowed by the status filter on its own status); entry counts, statuses, ratings once per entry from the display run.

- [ ] **Step 1: Failing tests**: a library without repeats gives exactly today's report (golden from the current code before changing it: capture the report of the existing seeded fixtures first); an entry completed in 2025 and rewatched (completed) in 2026 adds its time to both years and counts once as an entry; status filter `completed` excludes an active repeat run's time.
- [ ] **Step 2: Run** `scripts/go.sh test ./internal/stats/ -count=1` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** full suite — PASS.
- [ ] **Step 5: Commit** `feat: statistics count every run`.

### Task 7: Read-only timeline

**Files:**
- Create: `internal/library/timeline.go`, `internal/web/timeline.go`, `internal/web/templates/timeline.html`; Tests alongside
- Modify: `internal/plugin/contract.go` (`ProgressDef.LabelPlural string \`json:"label_plural,omitempty"\``, default label+"s"), plugin manifests where the plural differs, `item.html`

**Interfaces:**
- `type TimelineEntry struct { At time.Time; Type string /* progress|status|rating|note */; Key string; From, To *float64; OldText, NewText string; Origin EventOrigin; Note *UnitNote }`
- `func (l *Library) Timeline(ctx context.Context, runID int64) ([]TimelineEntry, error)` — events of the run oldest first, with unit notes merged at their unit (a note with no matching event gets the note's `created_at`).
- Web: `GET /{plugin}/list/{id}/runs/{run}/timeline` returns the fragment, loaded with htmx when a run's history row expands (Claude's call: lazy). Count/pages steps read "<Label> 5" / "<LabelPlural> 5-7"; times in the configured time zone; import/restore marked; empty: "Nothing was logged for this run".

- [ ] **Step 1: Failing tests**: step 4->5 and jump 4->7 wording; status and rating rows; origin import marked; empty run text; time-zone rendering uses the setting.
- [ ] **Step 2: Run** the new tests — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** full suite — PASS.
- [ ] **Step 5: Commit** `feat: read-only timeline per run`.

### Task 8: Formatting package

**Files:**
- Create: `internal/textfmt/textfmt.go`, `internal/textfmt/textfmt_test.go`, `internal/textfmt/fuzz_test.go`
- Modify: `internal/web/static/app.js` (spoiler reveal: click toggles a class on `.spoiler`), `internal/web/funcs.go` (template func `richtext`)

**Interfaces:**
- `func Render(s string) template.HTML` — syntax exactly as the spec's list; allowed output tags: `p`, `br`, `strong`, `em`, `ul`, `ol`, `li`, `a` (attributes `href` https only, `rel="noopener noreferrer nofollow"`), `span` (attribute `class="spoiler"`, `tabindex="0"`). Spoiler CSS uses theme-token classes only.

- [ ] **Step 1: Failing tests**: table tests for each construct, nested bold-in-spoiler, unclosed markers rendered literally, `javascript:`/`http:` links rendered as text, `<script>` escaped, entities escaped (Review Focus 4); `FuzzRender`: a small tag scanner in the test (no new dependency) asserts every `<` in the output opens or closes one of the allowed tags with only the allowed attributes and values.
- [ ] **Step 2: Run** `scripts/go.sh test ./internal/textfmt/ -count=1` — FAIL.
- [ ] **Step 3: Implement** a line-based block pass (paragraphs, lists) then an inline pass with a small stack; escape with `html.EscapeString` before emitting.
- [ ] **Step 4: Run** tests plus `scripts/go.sh test ./internal/textfmt/ -run x -fuzz FuzzRender -fuzztime 30s` — PASS, no crashers.
- [ ] **Step 5: Commit** `feat: safe text formatting for notes`.

### Task 9: Notes, reviews and unit notes

**Files:**
- Create: `internal/library/notes.go` + test; Modify: `internal/web/entries.go`, `runs.go`, `timeline.go`, templates `entry_dialog.html`, `item.html`, `run_history.html`, `timeline.html`, `list_row.html`/`list_cards.html` (+1 answer link)

**Interfaces:**
- `type UnitNote struct { RunID int64; Key string; Unit int; Body string; CreatedAt, UpdatedAt time.Time }`; `const MaxUnitNoteRunes = 2000`.
- `func (l *Library) SetUnitNote(ctx context.Context, runID int64, key string, unit int, body string) error` — empty body deletes; refuses keys whose track type is not count/pages, unit < 1, or unit above a bounded total.
- `func (l *Library) UnitNotes(ctx context.Context, runID int64) ([]UnitNote, error)`.
- Dialog: notes and review textareas (review edits the display run); rendered with `richtext` on the item page.
- Routes: `GET|POST /{plugin}/list/{id}/runs/{run}/notes/{key}/{unit}`. After +1 on a count/pages primary track the partial includes a "Note on <Label> <n>" link to that route.

- [ ] **Step 1: Failing tests**: limits (10,000 / 10,000 / 2,000 runes) and control characters rejected; percent track note refused; unit above total refused; empty body deletes; +1 partial contains the note link with the new unit; item page renders formatted notes, spoilers hidden by class.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** full suite — PASS.
- [ ] **Step 5: Commit** `feat: entry notes, run reviews and unit notes`.

### Task 10: Own tags

**Files:**
- Create: `internal/library/usertags.go` + test, `internal/web/usertags.go`, `internal/web/templates/settings_tags.html` + test
- Modify: `internal/library/query.go` (`Query.UserTags []string`, `Query.UserTagsAll bool`), `internal/web/filters.go`, `internal/web/columns.go`, `entry_dialog.html`, `filter_bar.html`, `settings_tabs.html`, `docs/usage.md`

**Interfaces:**
- `type UserTag struct { ID int64; Name string; Count int }`
- `func (l *Library) SetEntryTags(ctx context.Context, itemID int64, names []string) error` (creates missing tags; case-insensitive Unicode fold via the same folding the term filters use)
- `func (l *Library) EntryTags(ctx context.Context, itemIDs []int64) (map[int64][]string, error)`
- `func (l *Library) UserTags(ctx context.Context) ([]UserTag, error)`; `RenameUserTag(ctx, id, name)` (onto an existing name merges); `DeleteUserTag(ctx, id)`.
- URL parameters `ut` (repeatable) and `ut_all=1`; column key `user_tags`; the item page lists the entry's tags. Settings route `GET /settings/tags`, `POST /settings/tags/{id}` (rename), `POST /settings/tags/{id}/delete`.

- [ ] **Step 1: Failing tests**: `Comfort`/`comfort`/`COMFORT` one tag stored as first typed, `Ä`/`ä` one tag (Review Focus 5); 41 runes refused; any/all filters; rename-onto merges entries without duplicates; delete removes from entries; dialog datalist lists existing names; filter round trip in the URL.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** full suite — PASS.
- [ ] **Step 5: Commit** `feat: own tags across plugins`.

### Task 11: Custom lists

**Files:**
- Create: `internal/library/lists.go` + test, `internal/web/lists.go` + test, templates `lists.html`, `list_page.html`, `list_picker.html`
- Modify: `internal/web/server.go` (routes), `nav.html` (Lists group), `item.html` ("Add to list"), the add-from-search return flow used by series (`internal/web/series.go`, `add.go`), `docs/usage.md`

**Interfaces:**
- `type List struct { ID int64; Name string; ManualOrder bool; Items, Done int }`
- `CreateList(ctx, name) (List, error)`, `RenameList(ctx, id, name)`, `SetListOrder(ctx, id, manual bool)`, `DeleteList(ctx, id)`, `Lists(ctx) ([]List, error)`, `AddToCustomList(ctx, listID, itemID)` (idempotent), `RemoveFromCustomList(ctx, listID, itemID)`, `MoveInList(ctx, listID, itemID, delta int)`, `ListRows(ctx, listID int64, sort string, lang string) ([]Row, error)` with sort `position|title|added|status`, `ListsOfItem(ctx, itemID) ([]List, error)`, `SearchLibrary(ctx, text, lang string, limit int) ([]Row, error)` (across enabled plugins).
- Routes: `GET /lists`, `POST /lists`, `GET /lists/{id}`, `POST /lists/{id}` (rename/order), `POST /lists/{id}/delete`, `POST /lists/{id}/items`, `POST /lists/{id}/items/{item}/delete`, `POST /lists/{id}/items/{item}/move`, `GET /lists/{id}/search`. Add from search reuses the series return-param pattern with `list=<id>` and adds the new entry as planned.
- Progress line: "`Done` of `Items` done" where Done = display status completed.

- [ ] **Step 1: Failing tests**: create/rename with case-insensitive uniqueness (Review Focus 5); mixed plugins on one page with each plugin's status labels; manual move; sorted modes; removing an entry from the library removes it from lists; deleting a list keeps entries; add from search lands as planned and in the list; progress line counts; disabled plugin's entries are hidden from the page but kept.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** full suite — PASS.
- [ ] **Step 5: Commit** `feat: custom lists across plugins`.

### Task 12: Backup v2, restore v1+v2, JSON export

**Files:**
- Modify: `internal/library/backup.go`, `internal/backup/backup.go`, `internal/library/export.go`, `internal/plugin/data.go` (`ExportEntry`), `internal/plugins/jsonexport/exporter.go`, tests in each; `docs/usage.md`

**Interfaces:**
- `BackupEntry` becomes `{ Notes string; Runs []BackupRun; UserTags []string; UpdatedAt string; AddedAt string }`; `BackupRun{ Number int; Status Status; Rating *int; Review string; StartedOn, FinishedOn *string; Progress map[string]float64; UnitNotes []BackupUnitNote; Events []BackupEvent; EventsTruncated bool; CreatedAt, UpdatedAt string }`; top-level `lists` after `items` (and after `series`): `{name, manual_order, created_at, items:[{plugin_id, kind, source, source_id, position, added_at}]}`.
- `format_version` 2; reader accepts 1 (old `BackupEntry` fields -> run 1, comment -> notes, events -> run 1) and 2. `func (l *Library) RestoreList(ctx context.Context, bl BackupList) (added int, leftOut int, err error)` merges by case-insensitive name.
- `ExportEntry` gains `Notes`, `Runs []ExportRun`, `UserTags []string`, `Lists []string` (all omitempty); `Comment` is removed (export stays readable through the display-run top-level fields). JSON round-trip tests.

- [ ] **Step 1: Failing tests**: v2 round trip of an entry with 3 runs, unit notes, tags and two lists; a checked-in v1 fixture archive restores into runs; restoring the same v2 twice adds nothing the second time (Review Focus 3); export JSON contains runs.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** full suite — PASS.
- [ ] **Step 5: Commit** `feat!: backup format 2 and export with runs, tags and lists`.

### Task 13: Docs and release notes

**Files:**
- Modify: `CHANGELOG.md` (Unreleased: Added / Changed, breaking note on backup format 2 and export `comment` -> `notes`), `docs/usage.md` (runs, start again, timeline, formatting syntax, tags, lists, URL params), `README.md` (one line each, stay <= 150 lines), `docs/handoff.md` (state + migration 00019, next number 00020), `AGENTS.md` (next migration number 00020)

- [ ] **Step 1: Run** the docs tests `scripts/go.sh test ./... -run 'README|Docs' -count=1` after editing — PASS (README cap).
- [ ] **Step 2: Run** `scripts/ci.sh all` — all checks PASS, golangci-lint 0 issues.
- [ ] **Step 3: Commit** `docs: runs, notes, tags and lists`.

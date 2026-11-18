# Managing Entries Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Update from source with a field-by-field merge view, bulk edit across a filter, and a duplicate finder with a merge that keeps every run, note, tag and membership.

**Architecture:** One library compare-and-apply core (`ItemChanges` / `ApplyChanges`) feeds a single comparison page used by both Update from source and Merge. Bulk actions are library functions over a resolved id set (explicit ids or a `Query` re-run at apply time). Migration 00020 adds dismissed duplicate pairs, item aliases and the `bulk` event origin.

**Tech Stack:** Go 1.27, SQLite (modernc), goose, html/template + htmx, Tailwind theme tokens. No new dependency.

**Spec:** `docs/superpowers/specs/2026-11-16-managing-entries-design.md` (approved). Read it before any task.

## Global Constraints

- One migration `internal/db/migrations/00020_managing.sql`; never edit 00001-00019; `latestVersion` in `internal/db/sync_jobs_test.go` becomes 20; AGENTS.md / handoff next number becomes 00021 (Task 10).
- No media words in core files; no plugin HTML/JS; no inline scripts or `style`; colour classes only from `internal/appearance/tailwind-map.json`; htmx rules from AGENTS.md.
- Every write goes through library validation; runs rules hold (older runs only completed or dropped, display-run rule, imports never start runs).
- A missing or empty incoming value is never a difference.
- Bulk writes log events with origin `bulk`; the heatmap counts only `manual` (unchanged predicate).
- Bulk Completed: finish date = today (configured zone) where empty, progress = total where known; bulk Active: start date = today where empty.
- Docker is unavailable: `CGO_ENABLED=0 go test ./... -count=1`, `go vet ./...`, `gofmt -l .`, and golangci-lint v2.15.0 built with `GOTOOLCHAIN=go1.27.4 GOBIN=/tmp/claude-0/gl go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0` (0 issues). Every commit green; RED run shown first.
- Commit prefixes `feat:` / `fix:` / `test:` / `docs:`; trailer lines as in earlier commits. Agents push only to `claude/allister-feature-ideas-mezsjh`.

## Review Focus

1. **The source changes between opening the comparison page and pressing Apply** (or a duplicate is edited in another tab): apply re-computes differences and writes only ticked keys that still differ; never a stale value from the form. Test in Task 2 and Task 3.
2. **A filter-based bulk action whose match count changed since the page loaded** (an entry was added or edited in between): the confirm step shows the new count before anything is written. Test in Task 5.
3. **Merging two items that both have an active run, or where the other item's progress exceeds the survivor's total**: no half-written merge, no older open run, values kept. Test in Task 8.
4. **Adding/importing the merged-away source item later**: finds the survivor via the alias, no duplicate recreated (including the AniList account import's repeat run). Test in Task 8.
5. **A source answer with blank fields, an empty genre list or a failed People call**: no row offers clearing stored data. Test in Task 2.

---

### Task 1: Migration 00020

**Files:** Create `internal/db/migrations/00020_managing.sql`, `internal/db/managing_migration_test.go`; Modify `internal/db/sync_jobs_test.go`, `internal/library/events.go` (`OriginBulk`).

**Interfaces — Produces:** tables `dismissed_duplicates(item_a, item_b, dismissed_at)` (PK (item_a, item_b), CHECK item_a < item_b, both FK items cascade), `item_aliases(item_id FK items cascade, plugin_id, kind, source, source_id, PRIMARY KEY (plugin_id, kind, source, source_id))`; `entry_events.origin` CHECK accepts `bulk` (table rebuild keeping every column, index and row, `run_id` FK ON DELETE SET NULL); `library.OriginBulk EventOrigin = "bulk"` accepted by `validOrigin`.

- [ ] Step 1: Failing test `TestManagingMigration`: open at 19 with seeded events (all three origins, run_id set and NULL), migrate to latest; assert every event row and both indexes survive, an insert with origin `bulk` works, an unknown origin is refused, the two new tables exist with their constraints (a < b refused otherwise; alias uniqueness); `TestManagingMigrationDown` back to 19 drops the new tables and refuses `bulk` again (bulk rows are deleted on down).
- [ ] Step 2: Run `CGO_ENABLED=0 go test ./internal/db/ -run Managing -count=1` — FAIL.
- [ ] Step 3: Write the migration (rebuild pattern as in 00019's entry_events down step) and `OriginBulk`.
- [ ] Step 4: Full suite, vet, fmt, lint — PASS.
- [ ] Step 5: Commit `feat: migration 00020 for aliases, dismissed duplicates and bulk events`.

### Task 2: Compare-and-apply core

**Files:** Create `internal/library/changes.go`, `internal/library/changes_test.go`; Modify `internal/library/details.go` (remove `FillMissingFields` once Task 3 moved its caller — keep it until then).

**Interfaces — Produces:**
- `type Change struct { Key, Label string; Stored, Incoming string; StoredEmpty bool; Added, Removed []string }` — `Stored`/`Incoming` are display strings; `Added`/`Removed` filled for list parts.
- Keys: `field.<key>`, `titles`, `genres`, `tags`, `cover`, `relations`, `credits`.
- `func (l *Library) ItemChanges(ctx context.Context, itemID int64, in plugin.ItemData, credits []plugin.Credit, withCredits bool) ([]Change, error)` — ordered: titles, fields in manifest order, genres, tags, cover, relations, credits.
- `func (l *Library) ApplyChanges(ctx context.Context, itemID int64, in plugin.ItemData, credits []plugin.Credit, withCredits bool, keys []string) (int, error)` — one transaction; recomputes; writes selected keys still differing; returns the number written; unknown key -> ErrInvalid.

- [ ] Step 1: Failing tests: a changed field, an unchanged field (same after normalisation) and a field missing from `in` (no Change); an empty incoming genre list / nil credits with `withCredits=false` (no Change, Review Focus 5); titles/terms/relations/credits whole-list diffs with Added/Removed; `StoredEmpty` true only where stored has no value; Apply writes exactly the selected keys and leaves the rest byte-identical (compare `Item` before/after); Apply after the stored value was changed to equal `in` skips that key and counts it not (Review Focus 1); a source value violating a field bound -> ErrInvalid and nothing written; cover Change stores the URL and sets sync pending.
- [ ] Step 2: Run `CGO_ENABLED=0 go test ./internal/library/ -run Changes -count=1` — FAIL.
- [ ] Step 3: Implement using the existing normalisers (`prepare`, `normalizeTerms`, `normalizeRelations`, `checkCredits`) and write helpers (`insertTitles`, `insertFields`, `insertTerms`, relation insert, `insertCredits`); no second normalisation.
- [ ] Step 4: Full suite etc. — PASS.
- [ ] Step 5: Commit `feat: compare and apply an item's incoming data`.

### Task 3: Update from source page

**Files:** Modify `internal/web/item.go` (routes, `postRefresh`), templates `item.html`; Create `internal/web/changes.go`, `internal/web/templates/changes.html`, `internal/web/changes_test.go`; Modify `internal/library/details.go` (delete `FillMissingFields`), docs/usage.md row.

**Interfaces — Consumes:** Task 2. **Produces:** `GET|POST /{plugin}/{source}/{id}/refresh`; template `changes` with a view `changesView{ Title, Action, Rows []changeRow, Empty bool, Return string }` reused by Task 9; `changeRow{ Key, Label, Stored, Incoming string; Checked bool; Added, Removed []string }`.

- [ ] Step 1: Failing tests (fake source): GET shows only differing rows, empty-stored rows pre-ticked; POST with two keys applies exactly those (overview shows "Updated 2"); POST re-asks the source and ignores any value in the form; nothing differs -> "up to date" text, no form; manual item -> 404; source error -> existing error mapping; People failure while Details works -> no credits row, page still renders.
- [ ] Step 2: Run — FAIL.
- [ ] Step 3: Implement; item page button becomes a link to the GET.
- [ ] Step 4: Full suite etc. — PASS.
- [ ] Step 5: Commit `feat: update from source shows what changed and applies what you pick`.

### Task 4: Bulk library operations

**Files:** Create `internal/library/bulk.go`, `internal/library/bulk_test.go`.

**Interfaces — Produces:**
- `type BulkResult struct { Changed int; Skipped []BulkSkip }`, `type BulkSkip struct { ItemID int64; Reason string }`.
- `func (l *Library) MatchingIDs(ctx context.Context, q Query) ([]int64, error)` — every id the list query matches (no paging), same order as the list.
- `func (l *Library) BulkSetStatus(ctx context.Context, ids []int64, st Status, today time.Time) (BulkResult, error)`
- `func (l *Library) BulkAddToList(ctx context.Context, ids []int64, listID int64) (BulkResult, error)`
- `func (l *Library) BulkTags(ctx context.Context, ids []int64, add, remove []string) (BulkResult, error)`
- `func (l *Library) BulkAddToSeries(ctx context.Context, ids []int64, seriesID int64) (BulkResult, error)` — appended in `ids` order.
- `func (l *Library) BulkRemove(ctx context.Context, ids []int64) (BulkResult, error)`
- Each is one transaction; events with `OriginBulk`.

- [ ] Step 1: Failing tests: `MatchingIDs` equals the union of all list pages for a filter; BulkSetStatus Completed fills finish date (today) only where empty and progress to a known total, leaves unbounded progress, Active fills empty start date; a past display run behind a dropped repeat is skipped with a reason; events origin `bulk` and `ActivityDays` unchanged; BulkAddToSeries skips entries of another plugin/kind or already in a series; BulkTags add/remove by key (case/Unicode); BulkRemove removes entries; ids not in the library are skipped.
- [ ] Step 2: Run — FAIL.
- [ ] Step 3: Implement on the existing entry, list, tag and series write helpers inside one tx each.
- [ ] Step 4: Full suite etc. — PASS.
- [ ] Step 5: Commit `feat: bulk status, tags, lists, series and removal in the library`.

### Task 5: Bulk edit UI

**Files:** Create `internal/web/bulk.go`, `internal/web/templates/bulk_bar.html`, `internal/web/templates/bulk_confirm.html`, `internal/web/bulk_test.go`; Modify `list.html`, `list_row.html`, `list_cards.html`, `internal/web/plugins.go` (route), `internal/web/static/app.js`, docs/usage.md.

**Interfaces — Consumes:** Task 4. **Produces:** `POST /{plugin}/bulk` (fields: `action` = status|list|tags|series|remove, the action's value, `ids` repeated OR `scope=filter` plus the list's filter parameters, `expect` = the count the user saw, `confirm=1`).

- [ ] Step 1: Failing tests: checkboxes on rows and cards; the bar appears via app.js (no inline script; test the markup and the data attributes); `scope=filter` without `confirm` answers the confirm page with the current count; with `confirm=1` and `expect` != current count answers the confirm page again with the new count and writes nothing (Review Focus 2); a confirmed status change applies and the list reloads with "Changed N, skipped M"; remove confirm names the count; series select lists only series of this plugin and kind.
- [ ] Step 2: Run — FAIL.
- [ ] Step 3: Implement; app.js handles select-all and "Select all N matching".
- [ ] Step 4: Full suite etc. — PASS.
- [ ] Step 5: Commit `feat: bulk edit in every list`.

### Task 6: Duplicate finder (library)

**Files:** Create `internal/library/duplicates.go`, `internal/library/duplicates_test.go`.

**Interfaces — Produces:** `type DuplicatePair struct { A, B int64; Reason string /* "id:<type>" or "title" */ }`; `func (l *Library) Duplicates(ctx context.Context, pluginID string) ([]DuplicatePair, error)` (A < B, at least one in the library, dismissed pairs excluded, one pair listed once even with several reasons; external-id reasons first); `func (l *Library) DismissDuplicate(ctx context.Context, a, b int64) error`; `func normalTitle(s string) string` (fold case and Unicode with the same folding as terms, collapse punctuation and whitespace).

- [ ] Step 1: Failing tests: shared ISBN of the same kind is a pair; same external id across kinds is not; same normalised title + same year is a pair; one without a year never matches by title; kind without a year field matches by title alone; neither in the library -> excluded; dismissed -> excluded both ways (b,a); 3,000 items run under 300 ms in the test (a loose guard, `testing.Short` skips).
- [ ] Step 2: Run — FAIL.
- [ ] Step 3: Implement (one external-id self-join query, title grouping in Go over the plugin's items).
- [ ] Step 4: Full suite etc. — PASS.
- [ ] Step 5: Commit `feat: find likely duplicates`.

### Task 7: Duplicates page

**Files:** Create `internal/web/duplicates.go`, `internal/web/templates/duplicates.html`, `internal/web/duplicates_test.go`; Modify `internal/web/plugins.go`, `list.html` (link when pairs exist), docs/usage.md.

**Interfaces — Produces:** `GET /{plugin}/duplicates`, `POST /{plugin}/duplicates/dismiss` (`a`, `b`), link target `GET /{plugin}/merge?keep=<id>&other=<id>` (Task 9).

- [ ] Step 1: Failing tests: page lists pairs with both titles, sources, statuses and the reason text ("same ISBN" from the external id type label, "same title and year"); dismiss hides the pair; list shows the link only when pairs exist; no pairs -> friendly empty text.
- [ ] Step 2-4: RED, implement, GREEN.
- [ ] Step 5: Commit `feat: possible duplicates page`.

### Task 8: Aliases and merge (library)

**Files:** Create `internal/library/merge.go`, `internal/library/merge_test.go`; Modify `internal/library/items.go` (`ItemBySource`, `ItemByPluginSource`, the SaveItem/AddToList duplicate check consult `item_aliases`), `internal/library/convert.go` if the source-link takeover reuses it.

**Interfaces — Consumes:** Task 2 (`ApplyChanges`). **Produces:** `func (l *Library) MergeItems(ctx context.Context, keepID, otherID int64, keys []string) error` — one transaction: ApplyChanges(keep, other's data, other's credits, keys); move entry data per spec (runs ordered by started date else created, renumbered, all but the latest open run dropped; progress kept, bounded only by the absolute max; unit notes, events, orphan events move; notes appended after a divider; tags and lists unioned; series per spec; missing external ids added; other's identity stored as alias of keep; manual keep over sourced other takes over the source link); delete other; refresh display run.

- [ ] Step 1: Failing tests: both items with runs -> combined, renumbered, one open run at most (Review Focus 3); other's progress above keep's total survives; notes joined with the divider; tags/lists union, list position rule; series rule; alias makes `ItemBySource(other's source id)` return keep and AddToList of that source item answer AlreadySavedError{keep} (Review Focus 4); importer dedupe via `ItemBySource` finds keep; manual keep takes the other's source link; any failure mid-way (force one) leaves both items untouched; ActivityDays unchanged.
- [ ] Step 2-4: RED, implement, GREEN.
- [ ] Step 5: Commit `feat: merge two items into one`.

### Task 9: Merge flow (web)

**Files:** Create `internal/web/merge.go`, `internal/web/merge_test.go`; Modify `item.html` ("Merge with…" opening a library search of this plugin and kind), `changes.html` (merge variant), docs/usage.md.

**Interfaces — Consumes:** Tasks 3, 7, 8. **Produces:** `GET|POST /{plugin}/merge?keep=&other=` (swap link to change the survivor; default survivor: sourced over manual, else older), POST applies `MergeItems` with the ticked keys and redirects to the survivor's overview with a notice.

- [ ] Step 1: Failing tests: the page shows the comparison with the other item as incoming and a summary of what moves (runs, notes, tags, lists); POST merges and the other item's URL is 404 afterwards while its source URL redirects to the survivor; items of different kinds/plugins -> 404; stale ids -> 404.
- [ ] Step 2-4: RED, implement, GREEN.
- [ ] Step 5: Commit `feat: merge duplicates from the item page and the duplicates page`.

### Task 10: Backup, docs, changelog

**Files:** Modify `internal/library/backup.go`, `internal/backup/backup.go` (+ tests), `CHANGELOG.md`, `docs/usage.md`, `docs/handoff.md`, `AGENTS.md` (next migration 00021), `README.md` (one line, <= 150 lines).

- [ ] Step 1: Failing tests: `item_aliases` and `dismissed_duplicates` round-trip through a backup (pairs and aliases by item identity) and merge on restore (restoring twice adds nothing); an older backup without them restores.
- [ ] Step 2-4: RED, implement, GREEN; docs and CHANGELOG Unreleased (Added: merge view, bulk edit, duplicates and merge; Changed: Update from source no longer fills fields silently, it shows a comparison).
- [ ] Step 5: Commit `feat: backups keep aliases and dismissed duplicates; docs`.

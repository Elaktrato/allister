# Finding what's next Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Sharpen the home page's active cards into "Up next", extend the calendar (week view, plugin filter, mark watched), add a "Pick one" randomiser with a data-declared length filter, and refuse repeat runs of still-airing items.

**Architecture:** Kinds declare length measures and the grouping progress track as optional manifest data; `internal/library` gains pure and query helpers for "aired beyond progress"; `internal/web` renders them (home cards, calendar, `/{plugin}/pick`). No migration.

**Tech Stack:** Go (`scripts/go.sh` or the local pinned toolchain, see Global Constraints), html/template, htmx, Tailwind theme tokens.

**Spec:** `docs/superpowers/specs/2026-11-17-finding-whats-next-design.md`

## Global Constraints

- Follow `AGENTS.md`. No Docker here: `CGO_ENABLED=0 go test ./... -count=1`, `CGO_ENABLED=0 go vet ./...`, `gofmt -l .` (must print nothing); golangci-lint v2.15.0 at 0 issues (`/tmp/gl/golangci-lint run ./...`).
- `internal/guard`: no media words (anime, episode, manga, book titles...) in core Go/HTML (test files and `plugintest` exempt). Use "unit", "release", the kind's own labels.
- Plugins declare data only; every new contract field is optional and `omitempty`, JSON round trip tested; `ContractVersion` is **not** bumped.
- Colours only from `internal/appearance/tailwind-map.json` classes; no inline `style`, no inline scripts (handlers in `internal/web/static/app.js`). Styling is unchecked until CI.
- htmx: `Vary: HX-Request, HX-Target, HX-History-Restore-Request` where a URL answers both partial and full page.
- No migration expected; if one is needed it is 00021.
- Every commit green; commit style `feat:`/`fix:`/`docs:`/`test:`; work on branch `feat/finding-whats-next`; agents never push.
- Test first: show the real RED run, then implement.

## Review Focus

1. Pick with `len=` for a kind that declares no length, an unknown measure key, `len_min > len_max`, or a non-number bound: ignored with a visible notice, never a 500 (Task 2).
2. Entries whose length field is unset or 0 are excluded when a bound is set; one planned match rolls the same entry again; zero matches shows an empty state naming the filters (Task 2).
3. Mark watched with a forged or stale release (not in the item's stored airings, or already at/behind progress), a double submit, a disabled plugin, a progress total below the release: idempotent or refused, never lowers, never exceeds the total (Task 6).
4. Calendar week/month params that are garbage, a week across a DST change or the year boundary, an unknown `plugin` id (Tasks 5).
5. A repeat run already open when new airings appear: no aired hint, no Mark watched; `StartRun` of a still-airing item refused with a 422 message in both web paths (Tasks 3, 4, 6).

---

### Task 1: Contract data (`Length`, `GroupKey`) and manifest declarations

**Files:**
- Modify: `internal/plugin/contract.go` (add `LengthDef`, `KindDef.Length`, `ProgressSpec.GroupKey`), `internal/plugin/validate.go`
- Modify: `internal/plugins/game/manifest.go`, `movie/manifest.go`, `tv/manifest.go`, `anime/manifest.go`
- Modify: `docs/plugins.md`
- Test: `internal/plugin/validate_test.go`, `internal/plugin/contract_test.go` (JSON round trip), each plugin's `manifest_test.go`

**Interfaces:**
- Produces:
  ```go
  type LengthDef struct {
      Key, Label string // Key a slug, unique per kind
      Field string      // number field in minutes
      UnitsField string // number field counting units
      PerUnitField string // number field, minutes per unit (with UnitsField)
  }
  func (d LengthDef) InMinutes() bool // Field set, or UnitsField with PerUnitField
  // KindDef.Length []LengthDef  `json:"length,omitempty"`
  // ProgressSpec.GroupKey string `json:"group_key,omitempty"` — an eligible count track that groups the primary track's units (a release's Season maps to it); "" for none
  ```
- Valid combinations only: `Field`; `UnitsField`+`PerUnitField`; `UnitsField` alone. Referenced fields must exist in the kind and be `FieldNumber`; `GroupKey` must name an eligible `Count` track different from `PrimaryKey`.

- [ ] **Step 1: Failing tests** — `TestValidateLength` (table: valid three shapes; unknown field, non-number field, `Field` with `UnitsField`, `PerUnitField` without `UnitsField`, duplicate key, empty label each give a problem naming `kinds[0].length[i]`), `TestValidateGroupKey` (unknown track, same as primary, non-count), `TestLengthJSONRoundTrip`, and per plugin `TestManifestLength`: game = `time_main` "Main story", `time_extra` "Main + extras", `time_complete` "Completionist" (all `Field`); movie = `runtime` "Runtime"; tv = `total` (`episodes` x `runtime`, "Total time") and `episodes` ("Episodes"); anime = `total` (`episodes` x `duration`) and `episodes`; manga/book/light-novel declare none; tv `GroupKey == "season"`.
- [ ] **Step 2: Run** `CGO_ENABLED=0 go test ./internal/plugin/... ./internal/plugins/... -count=1`; expect FAIL (undefined `LengthDef`).
- [ ] **Step 3: Implement** the types and `validateKind` checks; declare the data in the four manifests; document `Length` and `GroupKey` in `docs/plugins.md` (note: optional, no contract bump).
- [ ] **Step 4: Run** the same command plus `go test ./internal/guard/ -count=1`; expect PASS.
- [ ] **Step 5: Commit** `feat: kinds declare length measures and a progress group track`.

### Task 2: Backlog randomiser (`/{plugin}/pick`)

**Files:**
- Create: `internal/web/pick.go`, `internal/web/templates/pick_result.html` (partial), `internal/web/pick_test.go`
- Modify: `internal/web/server.go` (`rand` field, constructor and test env `newFixtureEnv` seam, `pageNames`/partials as needed), `internal/web/plugins.go` (route `GET /pick` in the plugin route set), `internal/web/list.go` / `templates/list_page.html` + `filter_bar.html` (button "Pick one", length controls, result panel), `internal/web/static/app.js` (none unless needed), `docs/usage.md`
- Modify (start action): `internal/web/plugins.go` entry routes `POST /list/{id}/start`, handler in `internal/web/entries.go`

**Interfaces:**
- Consumes: `parseListQuery`, the list's `library.Query` builder, `Library.Rows`, `KindDef.Length` (Task 1).
- Produces:
  ```go
  // server.rand: func(n int) int (default rand.IntN from math/rand/v2); tests set a fixed one.
  type lengthFilter struct{ Measure plugin.LengthDef; Min, Max *float64 }
  func parseLengthFilter(k plugin.KindDef, q url.Values) (lengthFilter, []string /*rejected params*/)
  func entryLength(m plugin.LengthDef, fields map[string]float64) (float64, bool)
  func (s *server) getPick(w http.ResponseWriter, r *http.Request)
  func (s *server) postStart(w http.ResponseWriter, r *http.Request) // POST /list/{id}/start: planned -> active via library.UpdateEntry (reads the entry first), 303 to the item
  ```
- Parameters: `len=<measure key>`, `len_min`, `len_max`; status default planned only when no `status` parameter, explicit status incl. `all` honoured; the list's "Pick one" link carries the list's explicit status or `status=planned`.

- [ ] **Step 1: Failing tests** in `pick_test.go` (fixture plugins from `plugintest`; add a `Length` to a fixture kind there if none): `TestParseLengthFilter` (valid minutes, valid count, unknown measure, `min>max`, non-number, kind without length: each rejected key named), `TestEntryLength` (Field, units x per-unit, units alone, missing and 0 give `false`), `TestPickDrawsFromMatches` (seeded rand: three planned entries, length 30..90 matches two, the drawn one is the rand-chosen of those two), `TestPickExcludesUnsetLength`, `TestPickEmptyState` (names the filters), `TestPickRollAgainSameURL`, `TestPickDefaultsToPlanned` and `TestPickHonoursExplicitStatus`, `TestPickHtmxPartialAndFullPage` (Vary header; full page contains the list page with the panel), `TestPickStartButtonOnlyForPlanned`, `TestPostStartActivates`, `TestPickGenreAndOwnTagFilters`, `TestPickBadLengthParamsNeverFail` (200 with a notice).
- [ ] **Step 2: Run** `CGO_ENABLED=0 go test ./internal/web/ -run 'Pick|Length|Start' -count=1`; expect FAIL.
- [ ] **Step 3: Implement** as above. Load the matching rows with the list's own query (page size capped at 20,000 with a notice above it), filter by length in Go, draw with `s.rand`, render title, cover, length, genres, entry link, "Roll again" (same URL, `hx-get`, target the result panel) and "Start" (planned only). Show the length controls in the filter panel only when the kind declares `Length`; they do not change the table/card list.
- [ ] **Step 4: Run** the full `./internal/web/` suite; expect PASS. Update `docs/usage.md` (route, parameters, behaviour).
- [ ] **Step 5: Commit** `feat: pick one entry at random from a list`.

### Task 3: Refuse a repeat run of a still-airing item

**Files:**
- Modify: `internal/library/runs.go` (`StartRun`), `internal/web/entries.go`, `internal/web/runs.go`, `internal/web/templates/entry_dialog.html` and the card/overview "Start again" controls (find with `grep -rn RepeatAction internal/web/templates`), `docs/usage.md`
- Test: `internal/library/runs_test.go`, `internal/web/runs_test.go`

**Interfaces:**
- Produces: `library.ErrStillAiring` (wraps `ErrInvalid`, message "still airing: it has upcoming releases") returned by `StartRun`; `func (l *Library) StillAiring(ctx context.Context, itemID int64) (bool, error)` (true when a stored airing has `airs_at` after now, compared in SQL like `nowSQL`); `rowView`/dialog data gain `StillAiring bool` that hides the control and shows the note "Still airing: it can be started again once it has finished airing."

- [ ] **Step 1: Failing tests:** library `TestStartRunRefusedWhileAiring` (completed item with an airing in year 2999: `errors.Is(err, ErrStillAiring)` and `ErrInvalid`, no run created), `TestStartRunAllowedAfterAirings` (only past airings), `TestStillAiring`; web `TestStartRunStillAiring422` for `POST /list/{id}/runs` and for the dialog's `run_choice=new`, and `TestStartAgainHiddenWhileAiring`.
- [ ] **Step 2: Run** `CGO_ENABLED=0 go test ./internal/library/ ./internal/web/ -run 'StillAiring|StartRun' -count=1`; expect FAIL.
- [ ] **Step 3: Implement** the check inside `StartRun`'s transaction (before the insert), map the error to the existing 422 path in both web callers with the message, and hide the controls.
- [ ] **Step 4: Run** both packages in full; expect PASS. Document in `docs/usage.md` (Runs section).
- [ ] **Step 5: Commit** `feat: a still-airing item cannot be started again`.

### Task 4: "Aired beyond progress" in the library

**Files:**
- Modify: `internal/library/schedule.go`
- Test: `internal/library/schedule_test.go`

**Interfaces:**
- Consumes: `KindDef.Progress.PrimaryKey/GroupKey` (Task 1).
- Produces:
  ```go
  // ScheduledEntry gains: RunNumber int; Progress map[string]float64 (the display run's progress), filled by scheduleQuery.
  func Beyond(def plugin.KindDef, progress map[string]float64, season, number int) bool
  func (l *Library) AiredCounts(ctx context.Context, itemIDs []int64, now time.Time) (map[int64]int, error)
  ```
- `Beyond`: with `GroupKey` set and a release `season > 0`, compare `(season, number) > (progress[GroupKey], progress[PrimaryKey])` lexicographically; otherwise `number > progress[PrimaryKey]`. `AiredCounts` counts airings with `airs_at <= now` that are `Beyond` the display run's progress, only for entries whose display run number is 1 and status is active; others are absent from the map (no hint).

- [ ] **Step 1: Failing tests:** `TestBeyond` (single track; season+unit lexicographic: progress s1 e10 vs releases s1e11 yes, s2e1 yes, s1e10 no, s1e9 no; release season 0 with a group key compares the number only; missing progress means 0), `TestAiredCounts` (counts only aired, none for future, repeat run 2 absent, completed entry absent, planned entry absent), `TestScheduleEntriesCarryProgress`.
- [ ] **Step 2: Run** `CGO_ENABLED=0 go test ./internal/library/ -run 'Beyond|AiredCounts|ScheduleEntries' -count=1`; expect FAIL.
- [ ] **Step 3: Implement** with one extra query per call (display run progress for the item ids, `IN` placeholders like the title query).
- [ ] **Step 4: Run** `go test ./internal/library/ -count=1`; expect PASS.
- [ ] **Step 5: Commit** `feat: count aired releases beyond an entry's progress`.

### Task 5: Calendar week view and plugin filter

**Files:**
- Modify: `internal/web/calendar_page.go`, `internal/web/templates/calendar.html`, `docs/usage.md` (Calendar section)
- Test: `internal/web/calendar_test.go`

**Interfaces:**
- Consumes: `fillCalendar` helpers; `sourceSchedules`.
- Produces: `?view=week&week=YYYY-MM-DD` (any day, Monday start, local zone), `?plugin=<id>` on both views; `calendarView` gains `View string`, `Plugin string`, `Plugins []calendarPluginOption`, `Week []calendarDay`, links keep `plugin` and toggle view; `func parseCalendarWeek(raw string, now time.Time) time.Time` (Monday 00:00 local; garbage means the current week).

- [ ] **Step 1: Failing tests:** `TestParseCalendarWeek` (mid-week date returns its Monday; garbage returns the current week; DST week keeps midnight; year boundary), `TestCalendarWeekView` (seven days Monday first, releases land on their local day, prev/next links ±7 days), `TestCalendarPluginFilter` (only that plugin's releases in month and week), `TestCalendarUnknownPluginIgnored`, `TestCalendarPluginSelectListsScheduleCapable`, `TestCalendarHtmxGridPartial` still passes for both views.
- [ ] **Step 2: Run** `CGO_ENABLED=0 go test ./internal/web/ -run Calendar -count=1`; expect FAIL.
- [ ] **Step 3: Implement**; reuse one release-collecting helper for month and week; the grid partial (`calendar_grid`) renders either view.
- [ ] **Step 4: Run** `go test ./internal/web/ -count=1`; expect PASS.
- [ ] **Step 5: Commit** `feat: calendar week view and plugin filter`.

### Task 6: Mark an aired release watched

**Files:**
- Modify: `internal/library/progress.go` (new method), `internal/web/calendar_page.go`, `internal/web/plugins.go` (route), `internal/web/templates/calendar.html`, `docs/usage.md`
- Test: `internal/library/progress_test.go`, `internal/web/calendar_test.go`

**Interfaces:**
- Consumes: `Beyond`, `ScheduledEntry.Progress/RunNumber` (Task 4); `Increment`'s transaction pattern.
- Produces:
  ```go
  func (l *Library) MarkReleaseWatched(ctx context.Context, itemID int64, season, number int, now time.Time) (changed bool, err error)
  // POST /calendar/watched  form: item (id), season, number; 303 back to the calendar (the Referer's view) 
  ```
- `MarkReleaseWatched`: one transaction; `ErrNoEntry` without an entry; `ErrInvalid` when the (season, number) is not a stored airing of the item, has not aired (`airs_at > now`), the display run is a repeat (number above 1) or the entry is not active; a no-op (`changed=false`) when not `Beyond`; otherwise sets the primary track to `number` (clamped to the total) and, when `GroupKey` is active and `season > 0`, the group track to `season`; resets `ResetOnPrimary` tracks; writes `entry_events` with origin `manual` like `Increment`; never lowers. The button is offered only when every track it sets is active for the item.

- [ ] **Step 1: Failing tests:** library `TestMarkReleaseWatchedSets`, `...Idempotent` (second call `changed=false`, one event total), `...NeverLowers`, `...ClampsToTotal`, `...RejectsUnknownRelease`, `...RejectsFuture`, `...RejectsRepeatRun`, `...SeasonAndUnit` (group track set), `...EventOriginManual`; web `TestCalendarMarkWatchedButton` (shown only aired+beyond+active tracks), `TestPostCalendarWatched` (303; forged item 404/422; disabled plugin 404 via the guard; double post leaves one change).
- [ ] **Step 2: Run** `CGO_ENABLED=0 go test ./internal/library/ ./internal/web/ -run 'MarkRelease|CalendarWatched|MarkWatched' -count=1`; expect FAIL.
- [ ] **Step 3: Implement**, register `POST /calendar/watched` as a core route behind the item's plugin guard (`itemPluginGuard`, add the pattern to `coreGuarded` the way entry routes are).
- [ ] **Step 4: Run** both packages in full; expect PASS.
- [ ] **Step 5: Commit** `feat: mark an aired release watched from the calendar`.

### Task 7: Up next on the home page

**Files:**
- Modify: `internal/web/home.go`, `internal/web/list.go` (`rowView` fields `NextLabel string`, `Aired int`), `internal/web/templates/home.html`, `internal/web/templates/list_cards.html` (card), `docs/usage.md` (route table row for `/`)
- Test: `internal/web/home_test.go`

**Interfaces:**
- Consumes: `Library.AiredCounts` (Task 4), `progressLabel`.
- Produces: section heading "Up next" (id `home-active` unchanged for anchors; heading text changed), per card `NextLabel` = `"<primary label> <current+1>"` for count/pages primary tracks that are active and not at the total, `""` otherwise (duration, percent, no primary, finished); `Aired` from `AiredCounts` for the cards shown (one call), rendered as "N aired" only when `N >= 1`; both set only by the home handler.

- [ ] **Step 1: Failing tests:** `TestHomeUpNextHeading` (the old "In progress" text is gone), `TestHomeCardsShowNextUnit`, `TestHomeNextHiddenForDurationAndAtTotal`, `TestHomeAiredHint` (two aired beyond progress shows "2 aired"; none shows nothing; repeat run shows nothing), `TestHomeRewatchMarkerKept`, `TestListCardsUnchanged` (the plugin list's cards carry no Next/aired text).
- [ ] **Step 2: Run** `CGO_ENABLED=0 go test ./internal/web/ -run Home -count=1`; expect FAIL.
- [ ] **Step 3: Implement**; update the existing home tests that match the old heading.
- [ ] **Step 4: Run** `go test ./internal/web/ -count=1`; expect PASS.
- [ ] **Step 5: Commit** `feat: the home page's active cards become Up next`.

### Task 8: Docs, changelog, full checks and a browser smoke test

**Files:**
- Modify: `docs/handoff.md` (a "Finding what's next" paragraph, next migration unchanged, new follow-ups), `CHANGELOG.md` (Unreleased: user-visible changes), `docs/superpowers/specs/2026-11-17-finding-whats-next-design.md` (only if reality differed), `scripts/ci.sh` `MUTATION_PACKAGES` thresholds (raise only if the measured score is higher; never lower)
- Test: README stays at most 150 lines (docs test)

- [ ] **Step 1:** Run `CGO_ENABLED=0 go test ./... -count=1`, `CGO_ENABLED=0 go vet ./...`, `gofmt -l .` (empty), `/tmp/gl/golangci-lint run ./...` (0 issues); report any that cannot run (CSS build and `css-smoke.sh` only run in CI).
- [ ] **Step 2:** Start the app locally (`PORT=<private> go run ./cmd/allister` with a temp `DATA_DIR`), seed entries through the UI or the fixture, and with Playwright/Chromium check: Up next cards (Next line, aired hint), calendar month/week/plugin filter and "Mark watched" (progress changes once, button disappears), "Pick one" with a length filter and "Roll again", and that "Start again" is hidden for a still-airing item. Save screenshots in the scratchpad; stop the server.
- [ ] **Step 3:** Update docs and changelog; run the docs tests.
- [ ] **Step 4: Commit** `docs: finding what's next in the handoff and changelog`.

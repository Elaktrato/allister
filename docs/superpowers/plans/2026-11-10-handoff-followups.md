# Handoff Follow-ups Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Clear every fix/cleanup follow-up in `docs/handoff.md` (the "not built" features stay out: each needs its own design session).

**Architecture:** Twelve independent small fixes across covers, plugin URL matching, the library sort, the jobs queue (with migration 00012), web templates (CSP-safe dialogs, sort links, safeReturn), and the pin tooling. Each task is one red-green cycle on its own branch, merged to `main` when green.

**Tech Stack:** Go 1.27 (pinned Docker image via `scripts/go.sh`), SQLite (goose migrations), htmx 2.0.4, bash for `scripts/update-pins.sh`.

**Spec:** `docs/handoff.md` "Known follow-ups" (2026-11-10) and `docs/superpowers/specs/2026-11-10-import-pause-design.md` style: small changes argued from the handoff.

## Global Constraints

- Go runs only through `scripts/go.sh` (pinned Docker image; Go is not on the host).
- Full suite, `vet` and `fmt` must be pristine before every commit; work on a branch, merge locally with `--no-ff`.
- TDD: watch each test fail before implementing.
- Migrations strictly increasing; **00008 is unused (guard test asserts it); the next number is 00012**; never edit applied SQL.
- No media-specific words in core code/templates (`internal/guard` enforces); no new inline scripts in templates.
- htmx contract: out-of-band swaps update only option lists and links, never focused inputs; `hx-history="false"` on `#list-rows` stays.
- README stays under ~150 lines; long reference material goes to `docs/usage.md`.

## Review Focus

1. A hostile ftyp box claiming a huge size or truncated brands must not panic or read past the buffer — clamp to `len(head)`.
2. Non-numeric ids must not let path tricks in: `{id}` stays one path segment; no `/`, `?`, `#`, `%`, control chars, or leading/trailing punctuation.
3. The title sort must pick exactly the title `ResolveTitle` displays (same fallback chain), or rows order visibly disagrees with their titles.
4. Pruning must never delete a pending/running job or one whose `finished_at` is recent; `Recover` still resets running → pending first.
5. Removing `hx-on` must keep the exact dialog behavior for every status in `dialogSwapStatuses` and the cancel/submit handling — the existing web dialog tests are the proof.

---

### Task 1: AVIF compatible brands (internal/covers)

**Files:** Modify `internal/covers/covers.go:151-159` (`isAVIF`); test `internal/covers/covers_test.go`.

**Interfaces:** `isAVIF(head []byte) bool` stays unexported with the same signature; `SaveRestored` unchanged.

- [ ] **Step 1: Write the failing test** — cases: major `mif1` + compatible brands `mif1 miaf avif` → accepted; major `mif1` + `mif1 miaf` only → rejected; major `avif` → accepted (regression); box size smaller than 16 or claiming fewer than 4 brands → handled without panic; box size larger than `len(head)` → brands clamped to the buffer.
- [ ] **Step 2: Run** `scripts/go.sh test -count=1 ./internal/covers/ -run AVIF` → FAIL (mif1+avif rejected).
- [ ] **Step 3: Implement** — parse the box size (uint32 BE at head[0:4]); accept when the major brand (head[8:12]) or any compatible brand (offsets 16..min(boxSize, len(head)) step 4) equals `avif` or `avis`; require ≥16 bytes before reading compatible brands.
- [ ] **Step 4: Run** the covers package → PASS. Full suite, vet, fmt.
- [ ] **Step 5:** Branch `fix/avif-compat-brand`, commit `fix: accept AVIF covers by compatible brand`, merge.

### Task 2: Icon extractor hardening (internal/web)

**Files:** Modify `internal/web/icons_test.go` (extractor + docs); no production code.

**Interfaces:** `templateIconNames(t)` keeps its signature; add a second return or a sibling function listing dynamic (action) icon sources.

- [ ] **Step 1: Write the failing test** — (a) a reordered-attributes span (`<span aria-hidden="true" class="icon">name</span>` in a fixture string) is extracted; (b) every action found inside an icon span must be one of a pinned allow-list (today `{{.Icon}}`, tied to the manifest-icon check) — a new unchecked `{{.Whatever}}` fails the test.
- [ ] **Step 2: Run** → FAIL (reordered span missed by `iconContent` regex).
- [ ] **Step 3: Implement** — replace the fixed-order regex with: find `<span` tags, parse their attributes as `name="value"` pairs in any order, take those with `class="icon"` (class list may contain more classes), capture text to `</span>`, strip `{{...}}` actions, and record the actions separately for the allow-list assertion. Keep `tabIcon` as is. Note the codepoints-proxy limitation (font has 4012 ligatures, list has 4304 names) in the test comment.
- [ ] **Step 4:** Full suite, vet, fmt. Branch `test/icon-extractor`, commit `test: make the icon extractor attribute-order proof`, merge.

### Task 3: General source id grammar (internal/plugin)

**Files:** Modify `internal/plugin/matchurl.go` (`normalizeID`, `maxIDDigits`, `MatchRef`); test `internal/plugin/matchurl_test.go`.

**Interfaces:** `MatchRef`/`MatchURL` signatures unchanged; the accepted grammar widens.

- [ ] **Step 1: Write the failing test** — bare ids `tt0944947`, `OL717W`, `a-b_c.d` accepted; URL forms `imdb.example/title/tt0944947` and `books.example/work/OL717W` reduce to the id via patterns; all-digit behavior unchanged (`0053`→`53`, `0` rejected, 13 digits rejected); ids with `/`, `?`, `%`, spaces, leading `-` or `.` rejected; 65-char id rejected.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** — keep the all-digits path exactly as today (1-12 digits, strip zeros, reject zero); otherwise accept 1-64 bytes of `[A-Za-z0-9._-]` that starts and ends with an ASCII alphanumeric. `{id}` in patterns uses the same grammar.
- [ ] **Step 4:** Full suite, vet, fmt (anime behavior unchanged — its tests must stay green untouched). Branch `feat/match-id-grammar`, commit `feat: match non-numeric source ids`, merge.

### Task 4: Title sort follows the fallback order (internal/library)

**Files:** Modify `internal/library/query.go:676-692` (`orderBy`, `SortTitle` case); test `internal/library/query_test.go`.

**Interfaces:** `orderBy(s Sort, lang string)` signature unchanged; `titleFallback` reused.

- [ ] **Step 1: Read `ResolveTitle`** (query.go ~175-230) and write the failing test: items whose first-stored title (rowid) differs from the resolved title (preferred lang missing, fallback lang present) sort by the resolved title, matching the displayed order.
- [ ] **Step 2: Run** → FAIL (sort uses the first-stored title).
- [ ] **Step 3: Implement** — build the COALESCE chain: the preferred `lang` (when non-empty), then each of `c.def.TitleFallback` in order, then any title by rowid, then `''`; dedupe repeated langs is unnecessary (harmless).
- [ ] **Step 4:** Full suite, vet, fmt. Branch `fix/title-sort-fallback`, commit `fix: sort titles by the fallback order they display with`, merge.

### Task 5: Prune done jobs (internal/jobs + migration 00012)

**Files:** Create `internal/db/migrations/00012_jobs_finished_at.sql`; modify `internal/jobs/jobs.go` (`finish`, `Recover`); test `internal/jobs/jobs_test.go`.

**Interfaces:** `finish()` sets `finished_at` (the queue's stamp) on done and failed; `Recover` deletes `state='done' AND finished_at < now-30d` after resetting running jobs.

- [ ] **Step 1: Write the failing test** — a done job row gets `finished_at` set; `Recover` deletes a done job whose `finished_at` is 31 days old and keeps one from today, and never touches pending/running/failed rows.
- [ ] **Step 2: Run** → FAIL (no column → SQL error).
- [ ] **Step 3: Implement** — migration 00012 `ALTER TABLE jobs ADD COLUMN finished_at TEXT` (+ Down); set it in the done and failed branches of `finish`; prune in `Recover` with the 30-day cutoff constant. Check the migration-number guard test passes (00008 still unused, sequence increasing).
- [ ] **Step 4:** Full suite, vet, fmt. Branch `feat/jobs-pruning`, commit `feat: prune done jobs after 30 days`, merge.

### Task 6: Sort links carry the typing (internal/web)

**Files:** Modify `internal/web/templates/list.html:73` (sort link); test `internal/web/templates_test.go` (or the file that asserts template markup).

**Interfaces:** Template-only: add `hx-include="#list-filter"` to the sort links so a click sends the form's current values instead of aborting them.

- [ ] **Step 1: Write the failing test** — the rendered sort link markup includes `hx-include="#list-filter"` (the typing travels with the sort request; the `hx-sync` abort then loses nothing).
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** the attribute (plus its twin in the compact sort control if it is a link too — check `list.html`/`sort.html` fragments).
- [ ] **Step 4:** Full suite, vet, fmt. Branch with Task 7 (`fix/list-ui-nits`), commit `fix: sort links keep the typing in flight`, merge with Task 7's commit.

### Task 7: safeReturn rejects dot segments (internal/web)

**Files:** Modify `internal/web/form.go:40-59` (`safeReturn`); test `internal/web/form_test.go`.

- [ ] **Step 1: Write the failing test** — `/{p}/../settings`, `/{p}/a/../b` → default `/{p}`; normal paths unaffected.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** — reject when any path segment (before the query) equals `..` or `.`.
- [ ] **Step 4:** Full suite, vet, fmt. Same branch as Task 6, commit `fix: refuse dot segments in return URLs`, merge.

### Task 8: No inline scripts (CSP-ready dialogs)

**Files:** Create `internal/web/static/app.js`; modify `internal/web/templates/{add_dialog,entry_dialog,item,list,match_dialog}.html` (6 `hx-on` sites), `internal/web/templates/base.html` (script tag), `internal/web/funcs.go` (`dialogSwap` becomes data), embed list if static files are enumerated anywhere.

**Interfaces:** `app.js` registers: `htmx:beforeSwap` on document (reads `data-swap-on` statuses from the target `#dialog-slot`), a delegated click handler for `[data-dialog-close]`, a delegated submit handler for `[data-dialog-form]`. Templates replace `hx-on` attributes with those data attributes.

- [ ] **Step 1: Write the failing test** — no template contains `hx-on`; `app.js` is served from `/static/app.js` with a JS content type; `base.html` references it.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** the script and template changes, keeping `dialogSwapStatuses` as the single source (render it into the data attribute via `funcs.go`).
- [ ] **Step 4:** The existing dialog web tests (add, match, cancel, error statuses) must pass unchanged — they are the behavioral proof. Full suite, vet, fmt.
- [ ] **Step 5:** Branch `refactor/no-inline-scripts`, commit `refactor: move inline hx-on handlers to app.js`, merge.

### Task 9: update-pins atomic write and exit code (scripts)

**Files:** Modify `scripts/update-pins.sh` (write path, dry-run exit, usage comment).

- [ ] **Step 1: Implement** — `--write` copies each new file to `<file>.tmp` beside it and `mv`s into place (never a partial file); a dry run with a diff exits 1 (message: pins are stale) so callers/CI can detect it; usage comment updated.
- [ ] **Step 2: Verify live** — pins current: dry run exits 0; temporarily stale a digest in the working tree: dry run prints the diff and exits 1, then `--write` restores it atomically; revert. Full suite (guard pin tests).
- [ ] **Step 3:** Branch with Tasks 10-11 (`build/pins-hardening`), commit `build: atomic update-pins write and a failing dry run`, merge with those.

### Task 10: Tailwind pin test by architecture (internal/guard)

**Files:** Modify `internal/guard/pins_test.go`.

- [ ] **Step 1: Write the failing test** — parse PINS.md's Tailwind table rows and assert the amd64 row equals `ARG TAILWIND_SHA256_AMD64` and arm64 equals `ARM64` (containment today cannot catch a swapped pair).
- [ ] **Step 2: Verify RED** — temporarily swap the two sums in PINS.md, run, see FAIL, revert.
- [ ] **Step 3:** Full suite, vet, fmt. Same branch as Task 9, commit `test: pin the Tailwind sums by architecture`, merge with those.

### Task 11: distroless documentation (PINS.md)

**Files:** Modify `PINS.md` (runtime image row note).

- [ ] **Step 1: Implement** — note that distroless publishes no version tags (named tags are `latest`/`debug`/`nonroot` plus commit hashes), so `latest` + digest is the canonical pin; record the pin date; note `nonroot` would change the runtime user and volume permissions (a deliberate future decision, not taken now).
- [ ] **Step 2:** Full suite (pin tests). Same branch, commit `docs: why distroless is pinned under latest`, merge.

### Task 12: Historical docs note (docs/superpowers)

**Files:** Modify the spec/plan files that describe the global `title_lang` setting (at least `plans/2026-11-09-allister-anime.md`; grep for others).

- [ ] **Step 1: Implement** — one-line note at the top of each affected file: historical, written before the plugin era; the global `title_lang` became per-plugin (`plugin.<id>.title_lang`, migration 00011). Do not rewrite the bodies.
- [ ] **Step 2:** Full suite (docs tests). Branch `docs/historical-notes`, commit `docs: mark historical specs and plans`, merge.

### Closing

- [ ] Update `docs/handoff.md`: drop the completed bullets; "Known follow-ups" keeps only the "not built" features line. Commit `docs: handoff follow-ups cleared`.

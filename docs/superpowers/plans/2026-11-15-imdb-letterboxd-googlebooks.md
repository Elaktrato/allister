# IMDb and Letterboxd imports, Google Books key, manga magazine — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Import IMDb (movies, TV) and Letterboxd (movies) history, set the Google Books key in Settings, and fill a manga "Magazine" field from the official MyAnimeList API.

**Architecture:** One new core capability (the import runner resolves id-less rows by an exact title + year search, declared per importer as `ResolveByTitle`) and one new contract flag (`SourceInfo.KeyOptional`); everything else is plugin data and plugin code: a shared IMDb parser package used by the movies and TV plugins, a Letterboxd ZIP importer in the movies plugin, per-request key resolution for Google Books and MyAnimeList.

**Tech Stack:** Go 1.27 (standard library only: `encoding/csv`, `archive/zip`), existing plugin/importer contract, TMDB/TVmaze/Google Books/MAL APIs; everything through `scripts/go.sh`.

**Spec:** `docs/superpowers/specs/2026-11-15-imdb-letterboxd-googlebooks-design.md`

## Global Constraints

- Go only through `scripts/go.sh` (test -count=1 ./..., vet, fmt) and `scripts/ci.sh lint` (0 issues) before each commit; output pristine. TDD with a real RED run.
- No new Go dependencies.
- Plugins declare data only; no plugin HTML/JS; no media words in core files (`internal/guard`).
- Contract changes only: `ImporterInfo.ResolveByTitle` (`json:"resolve_by_title,omitempty"`), `SourceInfo.KeyOptional` (`json:"key_optional,omitempty"`); JSON round-trip tested; `ContractVersion` stays 1.
- Title match = exact: normalised title (lower case; every rune that is not a letter or digit dropped; "&" read as "and") of one candidate title equals the entry's, **and** candidate `year` == entry `Fields["year"]`; exactly one match → resolve, otherwise unmatched manual entry; a row without a year is never searched.
- Uploads keep the 20 MiB cap; ZIP read in memory only, only `watched.csv`, `diary.csv`, `ratings.csv`, `watchlist.csv`, `reviews.csv` at the root, each ≤ 10 MiB uncompressed.
- Secrets (Google Books key, MAL Client ID) never logged and never in a URL for MAL (header `X-MAL-CLIENT-ID`); Google Books keeps its existing `key=` query parameter behaviour.
- Owner exports are never committed (`imdb_*.csv`, `letterboxd_*.zip` are git-ignored); tests use synthetic fixtures.
- README ≤ 150 lines. Next migration (if any) 00019 — none is expected.
- Work on branch `feat/imdb-letterboxd`; commits `feat:`/`fix:`/`test:`/`docs:` with the trailer `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`; merged by rebase PR with CI green.

## Review Focus

1. **Two films with the same title and year** (remakes, "Hamlet" 1990 twice) → neither is guessed; the row becomes unmatched manual. *(Task 2: `TestTitleResolveAmbiguousIsUnmatched`.)*
2. **A title that differs only in punctuation or case** ("Spider-Man: Into the Spider-Verse" vs "Spider-Man Into the Spider Verse") → matches. A title that differs in diacritics ("Amélie" vs "Amelie") → unmatched, never wrong. *(Task 2: `TestNormaliseTitle` table.)*
3. **No TMDB key** while importing Letterboxd → every row is an unmatched manual entry with its year and Letterboxd link; the import does not fail or retry forever. *(Task 2: `TestTitleResolveNotConfiguredIsManual`.)*
4. **A ZIP bomb or an archive with `../` names or huge members** → refused or ignored without reading past the caps; nothing written to disk. *(Task 5: `TestLetterboxdRefusesOversizeAndPaths`.)*
5. **An IMDb export in another language** (an unknown `Title Type`) → each such row is skipped with the value named in the reason; known episode/game types are ignored, not skipped. *(Task 4: `TestIMDbUnknownTypeIsSkippedWithValue`.)*

---

### Task 1: Contract flags

**Files:** Modify `internal/plugin/contract.go` (ImporterInfo, SourceInfo), `internal/plugin/validate.go`, `docs/plugins.md`. Test `internal/plugin/contract_json_test.go`, `internal/plugin/validate_test.go`.

**Interfaces — Produces:** `ImporterInfo.ResolveByTitle bool`, `SourceInfo.KeyOptional bool`.

- [ ] **Step 1: Failing tests**
  - `TestImporterResolveByTitleJSON`: `ResolveByTitle:true` → `"resolve_by_title":true`; false → omitted; round trip; add it to the full-manifest round trip.
  - `TestSourceKeyOptionalJSON`: same for `"key_optional":true`.
  - `TestValidateResolveByTitle`: refused without `ResolveVia` ("resolve_by_title needs resolve_via"); accepted with a declared `ResolveVia` source of the importer's kinds (a source that does not `SupportsLookup` is fine for title-only importers when `ExternalIDType` is empty — see below).
  - `TestValidateKeyOptional`: `KeyOptional` without `KeyLabel` or `KeyFields` → refused ("key_optional needs a key").
- [ ] **Step 2: Run** `scripts/go.sh test -count=1 ./internal/plugin/` → FAIL (undefined fields).
- [ ] **Step 3: Implement** the two fields and checks. Keep the existing rule "ResolveVia must SupportsLookup and list ExternalIDType" for importers that have an `ExternalIDType`; an importer may have both (Letterboxd: `ExternalIDType` empty or `letterboxd`; the rule applies only when the type is one the source lists). Document both fields in `docs/plugins.md` (ImporterInfo and SourceInfo tables).
- [ ] **Step 4: Run** full suite, vet, fmt, lint → PASS.
- [ ] **Step 5: Commit** `feat: resolve_by_title and key_optional in the plugin contract`

### Task 2: Runner title resolution

**Files:** Modify `internal/importer/runner.go` (the `process` path for rows without an id value). Create `internal/importer/title.go` (`normaliseTitle`, candidate matching). Test `internal/importer/title_test.go`, `internal/importer/runner_test.go` (plugintest fixtures with a fake `Search`).

**Interfaces:** Consumes Task 1. Produces `func normaliseTitle(s string) string`; behaviour only (no new exported API).

- [ ] **Step 1: Failing tests**
  - `TestNormaliseTitle` table: `"Spider-Man: Into the Spider-Verse"` == `"spider man into the spider verse"`; `"Fast & Furious"` == `"Fast and Furious"`; `"Amélie"` != `"Amelie"`; `"  "` → `""`.
  - `TestTitleResolveExactMatch`: fake source Search returns two candidates, one with the same normalised title and year → that one is fetched (`Details`) and saved as a source item; the row's `letterboxd` id is kept in ExternalIDs.
  - `TestTitleResolveAmbiguousIsUnmatched`: two candidates both matching → unmatched manual entry, note contains `no unique match`. (Review Focus 1)
  - `TestTitleResolveNoYearNotSearched`: entry without `Fields["year"]` → no Search call, unmatched manual.
  - `TestTitleResolveNotConfiguredIsManual`: Search answers `ErrNotConfigured` → unmatched manual, import completes. (Review Focus 3)
  - `TestTitleResolveRateLimitedRetries`: `ErrRateLimited` once, then a match → resolved (same retry/pause path as lookups).
  - `TestTitleResolveExistingItemUpdated`: the resolved source id already in the library → the existing entry handling (skip/newer/always) applies; no duplicate item.
- [ ] **Step 2: Run** `scripts/go.sh test -count=1 ./internal/importer/` → FAIL.
- [ ] **Step 3: Implement** in `process`: when the row has no value for `ExternalIDType` (or the importer has none) and `ref.info.ResolveByTitle` and the entry has a title and a numeric `year` field → `Search(kind, title)` on `ResolveVia` inside the existing per-source pacing/retry loop (reuse it; do not copy it), pick the unique exact match, `Details` it, then continue as a found item (check `ItemByPluginSource`/existing first). Miss/ambiguous/no year/not configured → `saveUnmatched` with the importer's `UnmatchedNote` (`{title}`, `{year}` substituted; `{id}` stays for id importers).
- [ ] **Step 4: Run** full suite, vet, fmt, lint → PASS.
- [ ] **Step 5: Commit** `feat: import rows resolve by exact title and year`

### Task 3: Google Books key in the API keys card

**Files:** Modify `internal/plugins/book/manifest.go` (Google Books SourceInfo), `internal/plugins/book/plugin.go` + `googlebooks.go` (key resolver), `cmd/allister/main.go` (wiring, env fallback), `internal/web/settings.go` / `search.go` / `match.go` wherever "needs key" is derived (`sourceConfigured`), `docs/usage.md`.

**Interfaces:** Consumes Task 1. Produces `book.Options.GoogleBooksKey func(context.Context) (string, error)` (replaces the string).

- [ ] **Step 1: Failing tests**
  - book plugin: `TestGoogleBooksKeyResolvedPerRequest` — resolver returns "k1" then "k2"; two searches send `key=k1` then `key=k2`; an empty key sends no `key=` parameter.
  - manifest: Google Books has `KeyLabel: "API key"`, `KeyOptional: true`, non-empty `KeyHelp` mentioning that it is optional.
  - web: `TestOptionalKeySourceNeverNeedsKey` — a keyless optional source shows no "(needs key)" label, no plugin notice, and stays eligible as the default source; the API keys card lists it with "optional".
  - cmd: `TestGoogleBooksKeyFallback` — stored `source.googlebooks.key` wins over `GOOGLE_BOOKS_API_KEY`; env used when nothing is stored.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement.** `sourceConfigured` treats `KeyOptional` sources as configured. Card copy: label "API key", help "Optional. Without a key Google limits how many searches you can make."
- [ ] **Step 4: Run** full suite, vet, fmt, lint → PASS.
- [ ] **Step 5: Commit** `feat: Google Books key in Settings (optional)`

### Task 4: IMDb CSV import (movies and TV)

**Files:** Create `internal/plugins/imdb/parse.go` + `parse_test.go` (shared parser, like `internal/plugins/yamtrack`). Create `internal/plugins/movie/imdb.go`, `internal/plugins/tv/imdb.go` (+ tests). Modify both manifests and `plugin.go` (`Importer(id)`), `cmd/allister/main.go` only if needed (it imports plugins), `docs/usage.md`, README import sentence.

**Interfaces:** Produces `package imdb`: `type Row struct { Line int; Const, Title string; TypeKey string /* normalised Title Type */; TypeRaw string; Year int; Rating *int; RatedOn *time.Time }`; `func Parse(r io.Reader) ([]Row, error)` (`ErrBadFormat` wrapped); `const ( TypeMovie = "movie"; TypeTV = "tv"; TypeIgnored = "ignored"; TypeUnknown = "unknown")` and `func Classify(typeKey string) string` with the alias table from the spec (EN + DE).

- [ ] **Step 1: Failing tests** (synthetic CSVs with the two exact headers from the spec)
  - `TestParseRatingsLayout` and `TestParseWatchlistLayout`: both headers parse by name; `Const` validated (`tt` + 7–8 digits, else skipped row "invalid IMDb id"); `Your Rating` 1–10 → `*int`; empty → nil; `Date Rated` `YYYY-MM-DD` → UTC day.
  - `TestParseMissingRequiredColumn`: no `Title Type` → `ErrBadFormat`.
  - `TestClassify`: `Film`/`Movie`/`Video`/`TV Movie`/`Fernsehfilm`/`Short`/`Kurzfilm`/`TV Special` → movie; `Fernsehserie`/`TV Series`/`Miniserie`/`TV Mini Series` → tv; `Fernsehepisode`/`TV Episode`/`Video Game`/`Videospiel`/`Podcast Series`/`Music Video` → ignored; `Serie animée` → unknown.
  - movies `TestIMDbMovieImporter`: one file with film, series, episode, unknown → 1 entry (film, `imdb` id, completed with rating and `FinishedOn` = Date Rated; a watchlist row → planned), series and episode counted Ignored, unknown → Skipped with `Serie animée` in the reason. TV `TestIMDbTVImporter`: the series → entry, film ignored.
  - `TestIMDbUnknownTypeIsSkippedWithValue` (Review Focus 5) and `TestIMDbDuplicateConstReportedOnce`.
  - manifests: movie importer `{ID:"imdb-csv", Name:"IMDb CSV", Accept:[".csv"], ExternalIDType:"imdb", ExternalIDLabel:"IMDb", ResolveVia:"tmdb", ManualRows:true, FallbackTitleLang:"en", UnmatchedNote:"Imported from IMDb; TMDB had no match for {id}."}`; TV importer same with `ResolveVia:"tvmaze"`, `ResolveViaFallback:["tmdb"]`, note "…TVmaze and TMDB had no match for {id}."; plus `FileHint` "Upload your IMDb ratings or watchlist export (CSV). Ratings become Seen/Watched, watchlist entries Want to watch." (TV: "…Watched…").
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement.** Rows carry `Fields["year"]`. Episodes are not imported anywhere.
- [ ] **Step 4: Run** full suite, vet, fmt, lint; also run the parser once (a throwaway test or `go run` in the scratchpad, never committed) against the owner's `imdb_ratings.csv` and `imdb_watchlist.csv` and record in the report only the counts per classified type and skipped reasons — no titles. Expected for the owner's files: ratings 102 Film + 3 Video → movies, 4 Fernsehserie → tv, 9 Fernsehepisode ignored; watchlist 195 Film → movies, 18 Fernsehserie + 3 Miniserie → tv.
- [ ] **Step 5: Commit** `feat: IMDb CSV import for movies and TV`

### Task 5: Letterboxd export import (movies)

**Files:** Create `internal/plugins/movie/letterboxd.go` + `letterboxd_test.go`. Modify `internal/plugins/movie/manifest.go`, `plugin.go`, `internal/web/import.go` only if `.zip` needs the multipart path changed (it should not: `Parse` gets the body), `docs/usage.md`, README.

**Interfaces:** Consumes Tasks 1–2 (`ResolveByTitle`). Produces importer `{ID:"letterboxd-zip", Name:"Letterboxd export", Accept:[".zip"], ExternalIDType:"letterboxd", ExternalIDLabel:"Letterboxd", ResolveVia:"tmdb", ResolveByTitle:true, ManualRows:true, FallbackTitleLang:"en", UnmatchedNote:"Imported from Letterboxd; TMDB had no unique match for \"{title}\" ({year})."}`, `FileHint` "Upload the ZIP Letterboxd sends from Settings > Data > Export. Films are matched on TMDB by exact title and year; others stay as manual entries you can match." — and the `tmdb` movie source does not list `letterboxd`, so validation (Task 1) must accept this pairing; if it cannot cleanly, set `ExternalIDType` empty and store the link in `ExternalIDs["letterboxd"]` anyway (record which in the report).

- [ ] **Step 1: Failing tests** (ZIPs built in the test with `archive/zip`)
  - `TestLetterboxdMerge`: watched + diary (two watches, one `Rewatch: Yes`) + ratings (4.5) + reviews + watchlist for one film → one entry: completed, rating 9, `FinishedOn` = latest Watched Date, comment = latest review, note "watched 2 times", `Fields["year"]`, `ExternalIDs["letterboxd"]` = the URI path; a watchlist-only film → planned; a film on the watchlist and watched → completed.
  - `TestLetterboxdHalfStars`: 0.5 → 1, 5 → 10.
  - `TestLetterboxdBadInput`: not a ZIP → `ErrBadInput`; a ZIP without watched/diary/watchlist → `ErrBadInput`.
  - `TestLetterboxdRefusesOversizeAndPaths` (Review Focus 4): a member over 10 MiB uncompressed → `ErrBadInput` (read through `io.LimitReader`, not by trusting the header); `../watched.csv` and `sub/watched.csv` are ignored.
  - Columns read by name, case-insensitive: `Date`, `Name`, `Year`, `Letterboxd URI`, `Rating`, `Rewatch`, `Tags`, `Watched Date`, `Review`.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** full suite, vet, fmt, lint. If the owner put `letterboxd_*.zip` in the repo root, run the parser on it once (scratch, not committed) and report header names and counts only.
- [ ] **Step 5: Commit** `feat: Letterboxd export import for movies`

### Task 6: Manga magazine from MyAnimeList

**Files:** Create `internal/plugins/manga/mal.go` + `mal_test.go` (small client). Modify `internal/plugins/manga/manifest.go` (field `magazine`, source `mal`), `mapping.go`/`plugin.go` (Details enrichment), `cmd/allister/main.go` (resolver, `MAL_CLIENT_ID` fallback), `internal/config` if env vars live there, `docs/usage.md`, `docs/handoff.md`, README (manga sentence).

**Interfaces:** Produces `manga.Options.MALClientID func(context.Context) (string, error)`; `func (c *malClient) Serialization(ctx context.Context, malID string) ([]string, error)`.

- [ ] **Step 0: Terms check.** Read https://myanimelist.net/static/apiagreement.html (and the API docs' usage notes). Record in the report anything that constrains this use (attribution, caching, rate). If the terms forbid it, stop and report BLOCKED.
- [ ] **Step 1: Failing tests** (httptest server)
  - `TestMALSerialization`: `GET /v2/manga/2?fields=serialization` with header `X-MAL-CLIENT-ID: id1` → `["Young Animal"]`; two serializations joined ", " in the field.
  - `TestMALNeverInURL`: the client id never appears in the request URL or in any returned error string.
  - `TestMagazineOnDetails`: AniList details of a manga with MAL id and a configured id → `Fields["magazine"]`; not configured / 404 / 500 / bad JSON → no magazine and the AniList details still succeed.
  - manifest: field `{Key:"magazine", Label:"Magazine", Type:FieldText, Show:true, Editable:true, Filterable:true}`, column hidden by default; the `mal` source with `KeyLabel:"Client ID"`, `KeyOptional:true`, `KeyHelp` naming https://myanimelist.net/apiconfig. If a key-only source (no kinds/search/lookup) cannot validate without bending the contract, declare the key as a named field on the AniList source instead and record why.
  - cmd: stored `source.mal.key` wins over `MAL_CLIENT_ID`.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement.** Pace at most 1 request per second; a hand-edited `magazine` is not overwritten by sync (existing edited-field rule).
- [ ] **Step 4: Run** full suite, vet, fmt, lint → PASS.
- [ ] **Step 5: Commit** `feat: manga magazine from the MyAnimeList API`

### Task 7: Docs, preview, PR

- [ ] **Step 1:** `docs/handoff.md`: the four features; "Not built, by decision": MangaDex calendar (only scheduled scanlation chapters, would need a second schedule source) and magazine calendars (no official Weekly Shōnen Jump/Jump+ data, MANGA Plus only unofficial, VIZ volumes only). README ≤ 150 lines.
- [ ] **Step 2:** Preview on a free private port (not 18080–18083) with its own compose project: import the owner's two IMDb files into Movies and TV and check the counts match Task 4's expected numbers; check the API keys card (Google Books optional, MAL Client ID); tear down with `docker compose -p <project> down -v`.
- [ ] **Step 3:** `scripts/ci.sh all` green; PR (labels `feat`), CI green, rebase-merge.

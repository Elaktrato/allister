# Allister Anime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Dockerised, single-user Go + htmx + Tailwind app that searches, saves and tracks anime (AniList as the only metadata source, manual entries, Yamtrack import).

**Architecture:** One Go binary serving server-rendered `html/template` pages with htmx for partial updates. SQLite (pure-Go driver, no CGO) holds saved anime, titles, people and list entries. Metadata providers sit behind one `Provider` interface; saving an entry persists core details immediately and runs cover and cast sync as retried background jobs.

**Tech Stack:** Go 1.23 (`net/http` ServeMux patterns, `html/template`, `embed`), `modernc.org/sqlite`, `github.com/pressly/goose/v3` (embedded SQL migrations), htmx (vendored file), Tailwind standalone CLI (built in a Docker stage), Docker + compose.

**Spec:** `docs/superpowers/specs/2026-11-09-allister-anime-design.md`

## Global Constraints

- Docker: single container plus one volume (SQLite file and cover images both under `/data`); one configurable port (`PORT`, default `8080`); no authentication.
- No API keys required to start.
- Titles are stored per language tag; display language is a setting (default `ja`).
- Fallback chain when the preferred language is missing: native (`ja`), romaji (`ja-latn`), English (`en`), then any available title.
- Entry identity is `(source, source_id)` with source in `anilist`, `manual`; people are identified by `(source, source_id)` and never merged across sources.
- Statuses: `watching`, `plan_to_watch`, `paused`, `completed`, `dropped`.
- Cover images are stored locally under `/data/covers`.
- Responsive layout via Tailwind; no dashboards, no personalised recommendations feed.

## Review Focus

- Empty or whitespace-only search query: no provider request, neutral empty state.
- Provider returns 429, 5xx or times out: the page shows a readable error with a retry, never a crash or blank page.
- AniList synopses contain HTML (`<br>`, `<i>`): shown as safe plain text with line breaks, never as raw tags or unescaped markup.
- Progress above the known episode total, rating outside 0 to 10 (blank means unrated, 0 is a valid rating), finish date before start date: rejected with a validation message, nothing saved.
- Yamtrack CSV with a UTF-8 BOM, unknown status values, non-anime rows, or re-imported twice: no crash, unknown rows reported, no duplicates created.
- Matching with an AniList URL that is malformed, points at manga, or at an anime already in the list: clear message, nothing changed.

## File Structure

```
cmd/allister/main.go                  wiring, graceful shutdown
internal/config/config.go             env config (PORT, DATA_DIR)
internal/db/db.go                     Open(path) + goose migrations
internal/db/migrations/*.sql          schema
internal/titles/titles.go             Resolve(titles, preferred)
internal/settings/settings.go         key/value settings store
internal/provider/provider.go         shared types, errors, Provider interface
internal/provider/anilist/            AniList GraphQL client
internal/store/                       anime, list entries, people persistence
internal/covers/covers.go             cover download to DATA_DIR
internal/jobs/jobs.go                 persistent retried background jobs
internal/importer/yamtrack/           CSV parse + import job
internal/web/                         handlers, templates/, static/
Dockerfile, docker-compose.yml, tailwind.config.js, README.md
```

---

### Task 1: Scaffold, Docker and base layout

**Files:**
- Create: `go.mod`, `cmd/allister/main.go`, `internal/config/config.go`, `internal/web/server.go`, `internal/web/templates/base.html`, `internal/web/static/htmx.min.js`, `internal/web/static/input.css`, `tailwind.config.js`, `Dockerfile`, `docker-compose.yml`, `.dockerignore`
- Test: `internal/web/server_test.go`, `internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Load() Config` with `Config{Port string; DataDir string}`; `web.NewServer(deps Deps) http.Handler` where `Deps` starts empty and grows in later tasks; routes `GET /healthz` returns `200 ok`; `base.html` defines blocks `title` and `content` and a nav with Anime, Settings (other media tabs are not rendered until built).

- [ ] **Step 1: Write failing tests** `TestHealthz` (GET `/healthz` is 200 with body `ok`), `TestBasePageHasNav` (GET `/` contains `Anime` and a link to `/settings`), `TestLoadDefaults` (no env gives `Port == "8080"`, `DataDir == "/data"`; env `PORT=9000` gives `"9000"`).
- [ ] **Step 2:** Run `go test ./...`. Expected: FAIL (packages missing).
- [ ] **Step 3:** Implement the packages above. Tailwind CSS is built to `internal/web/static/app.css` and embedded with `embed`. The Dockerfile is multi-stage: Tailwind standalone CLI stage, Go build stage (`CGO_ENABLED=0`), minimal runtime image; compose maps `PORT` and mounts a named volume at `/data`.
- [ ] **Step 4:** Run `go test ./...` (PASS) and `docker compose up --build -d && curl -fsS localhost:8080/healthz` (prints `ok`), then `docker compose down`.
- [ ] **Step 5: Commit** `feat: scaffold server, docker and base layout`.

### Task 2: Database and migrations

**Files:**
- Create: `internal/db/db.go`, `internal/db/migrations/00001_init.sql`
- Test: `internal/db/db_test.go`

**Interfaces:**
- Produces: `db.Open(path string) (*sql.DB, error)` (runs migrations, enables `PRAGMA foreign_keys=ON` and WAL). Tables: `settings(key PK, value)`, `anime(id PK, source, source_id, type, episodes NULL, year NULL, studio, synopsis, cover_path, external_url, source_material, started_on NULL, ended_on NULL, mal_id NULL, sync_state, UNIQUE(source, source_id))`, `anime_titles(anime_id, lang, text)`, `anime_relations(anime_id, kind, rel_source, rel_source_id, titles_json, type)`, `people(id PK, source, source_id, name, image_path, external_url, UNIQUE(source, source_id))`, `anime_cast(anime_id, character_person_id, actor_person_id NULL, role)`, `list_entries(anime_id PK, status, rating NULL, progress, started_on NULL, finished_on NULL, comment, updated_at)`, `jobs(id PK, kind, payload_json, state, attempts, run_after, last_error)`.

- [ ] **Step 1: Write failing tests** `TestOpenCreatesSchema` (open `t.TempDir()/a.db`, all tables above exist), `TestUniqueSourceID` (inserting the same `(source, source_id)` twice into `anime` errors), `TestReopenIsIdempotent`.
- [ ] **Step 2:** `go test ./internal/db/...` Expected: FAIL.
- [ ] **Step 3:** Implement with `modernc.org/sqlite` and goose embedded migrations.
- [ ] **Step 4:** Run the tests. Expected: PASS.
- [ ] **Step 5: Commit** `feat: sqlite schema and migrations`.

### Task 3: Title resolution and settings

**Files:**
- Create: `internal/titles/titles.go`, `internal/settings/settings.go`, `internal/web/settings.go`, `internal/web/templates/settings.html`
- Test: `internal/titles/titles_test.go`, `internal/settings/settings_test.go`, `internal/web/settings_test.go`

**Interfaces:**
- Consumes: `db.Open`.
- Produces: `type Title struct{ Lang, Text string }` (in `internal/provider`, imported here; see Task 4); `titles.Resolve(ts []provider.Title, preferred string) string` (returns "" only for an empty slice); `titles.Languages(ts []provider.Title) []string`; `settings.Store` with `Get(ctx, key string) (string, bool, error)`, `Set(ctx, key, value string) error`, and `TitleLang(ctx) string` (default `"ja"`); routes `GET /settings` and `POST /settings` (field `title_lang`, options `ja`, `ja-latn`, `en` plus languages found in stored titles).

- [ ] **Step 1: Write failing tests**: `TestResolvePreferred` (`en` present, preferred `en` returns the English text), `TestResolveFallbackChain` (preferred `de` missing, with `ja-latn` and `en` present, returns the romaji; with only `en` returns English; with only `fr` returns the French text), `TestResolveEmpty`, `TestTitleLangDefault`, `TestPostSettingsPersists`.
- [ ] **Step 2:** Run the package tests. Expected: FAIL.
- [ ] **Step 3:** Implement. Create `internal/provider/provider.go` with only the `Title` type here if Task 4 has not yet run; Task 4 extends the same file.
- [ ] **Step 4:** Run the tests. Expected: PASS.
- [ ] **Step 5: Commit** `feat: title resolution and language setting`.

### Task 4: Provider types and AniList client

**Files:**
- Create: `internal/provider/provider.go`, `internal/provider/text.go`, `internal/provider/anilist/client.go`, `internal/provider/anilist/testdata/*.json`
- Test: `internal/provider/text_test.go`, `internal/provider/anilist/client_test.go`

**Interfaces:**
- Produces in `provider`:
  - `type Source string` with constants `AniList = "anilist"`, `Manual = "manual"`.
  - `type Title struct{ Lang, Text string }`; `type Person struct{ Source Source; ID, Name, ImageURL, URL string }`; `type CastMember struct{ Character Person; Actor *Person; Role string }`.
  - `type Relation struct{ Kind string; Source Source; ID string; Titles []Title; Type string }` (`Kind` is `sequel`, `prequel`, `source`, `other`).
  - `type Recommendation struct{ Source Source; ID string; Titles []Title; CoverURL string }`.
  - `type SearchResult struct{ Source Source; ID string; Titles []Title; Type string; Episodes *int; Year *int; Studio, Synopsis, CoverURL string }`.
  - `type Anime struct{ SearchResult; StartDate, EndDate *time.Time; SourceMaterial, ExternalURL string; MALID string; Relations []Relation; Recommendations []Recommendation }`.
  - `type Provider interface{ Name() Source; Search(ctx context.Context, q string) ([]SearchResult, error); Anime(ctx context.Context, id string) (Anime, error); Cast(ctx context.Context, id string) ([]CastMember, error) }`.
  - Errors `ErrNotFound`, `ErrRateLimited`, `ErrUnavailable`; `provider.PlainText(s string) string` (turns `<br>` into newlines, strips other tags, unescapes entities).
  - `anilist.New(baseURL string, hc *http.Client) *Client` implementing `Provider`; Language tags: native `ja`, romaji `ja-latn`, English `en`.

- [ ] **Step 1: Write failing tests** with `httptest` servers returning recorded JSON: `TestSearchMapsResults`, `TestAnimeMapsTitlesRelationsRecsMALID`, `TestCastMapsVoiceActors` (Japanese voice actors only), `TestRateLimitedMapsError` (429 gives `ErrRateLimited`), `TestServerErrorMapsUnavailable` (500), `TestPlainTextStripsHTML` (`"a<br>b<i>c</i>&amp;"` gives `"a\nbc&"`).
- [ ] **Step 2:** Run `go test ./internal/provider/...`. Expected: FAIL.
- [ ] **Step 3:** Implement against the AniList GraphQL endpoint (`https://graphql.anilist.co`), passing the descriptions through `PlainText`. The fixtures are trimmed copies of real responses, fetched once while writing the tests.
- [ ] **Step 4:** Run the tests. Expected: PASS.
- [ ] **Step 5: Commit** `feat: provider types and anilist client`.

### Task 5: (dropped) Jikan client

Dropped by the owner: AniList is the only source. Nothing to implement; skip to Task 6.

### Task 6: Store for saved anime and list entries

**Files:**
- Create: `internal/store/anime.go`, `internal/store/list.go`, `internal/store/people.go`
- Test: `internal/store/anime_test.go`, `internal/store/list_test.go`, `internal/store/people_test.go`

**Interfaces:**
- Consumes: `db.Open`, `provider` types.
- Produces:
  - `store.New(db *sql.DB) *Store`.
  - `type Status string` with the five constants from Global Constraints; `type ListEntry struct{ AnimeID int64; Status Status; Rating *int; Progress int; StartedOn, FinishedOn *time.Time; Comment string }`.
  - `(*Store) SaveAnime(ctx, a provider.Anime) (int64, error)` (stores anime, all titles, relations; sets `sync_state='pending'`; returns `ErrAlreadySaved` for the same `(source, source_id)`).
  - `(*Store) FindBySource(ctx, source provider.Source, id string) (*Saved, error)` returning `nil, nil` when absent; `(*Store) FindByMALID(ctx, malID string) (*Saved, error)` (matches `anime.mal_id`; returns `nil, nil` when absent).
  - `(*Store) SaveCast(ctx, animeID int64, cast []provider.CastMember) error`; `(*Store) PersonAnime(ctx, personID int64) ([]PersonAppearance, error)`.
  - `(*Store) UpsertListEntry(ctx, e ListEntry) error` returning `ErrInvalid` (wrapping a message) when: rating outside 0 to 10 (nil means unrated; 0 is valid), progress below 0 or above a known episode total, `FinishedOn` before `StartedOn`.
  - `(*Store) List(ctx, status *Status) ([]ListRow, error)`; `(*Store) IncrementProgress(ctx, animeID int64) error` (stops at the known total, no change when already there).

- [ ] **Step 1: Write failing tests**: `TestSaveAnimeBlocksDuplicate`, `TestSameIDDifferentSourceAllowed` (the same `source_id` under `anilist` and `manual` are different entries), `TestFindByMALID`, `TestUpsertRejectsRatingOutOfRange` (-1 and 11), `TestUpsertAcceptsZeroRating`, `TestUpsertAcceptsNilRating`, `TestUpsertRejectsProgressAboveTotal`, `TestUpsertRejectsFinishBeforeStart`, `TestIncrementStopsAtTotal`, `TestIncrementUnknownTotalKeepsCounting`, `TestSaveCastLinksVoiceActors`, `TestPersonAnimeOnlyReturnsSavedAnime`.
- [ ] **Step 2:** Run `go test ./internal/store/...`. Expected: FAIL.
- [ ] **Step 3:** Implement with plain `database/sql`.
- [ ] **Step 4:** Run the tests. Expected: PASS.
- [ ] **Step 5: Commit** `feat: store for anime, cast and list entries`.

### Task 7: Search page

**Files:**
- Create: `internal/web/search.go`, `internal/web/templates/search.html`, `internal/web/templates/search_results.html`
- Modify: `internal/web/server.go` (add `Providers map[provider.Source]provider.Provider`, `Titles`, `Settings` to `Deps`)
- Test: `internal/web/search_test.go`

**Interfaces:**
- Consumes: `provider.Provider`, `titles.Resolve`, `settings.Store.TitleLang`.
- Produces: `GET /anime/search?q=`; htmx requests (`HX-Request` header) return only the `search_results.html` partial; results show cover, resolved title, type, episodes, year, studio, truncated synopsis, and an "Add" link to `/anime/anilist/{id}`; page includes a link "Can't find it? Add manually" to `/anime/manual/new`.

- [ ] **Step 1: Write failing tests** with a fake `Provider`: `TestSearchUsesAniList`, `TestSearchEmptyQueryMakesNoRequest` (fake counts calls: zero), `TestSearchShowsPreferredTitle` (setting `en`), `TestSearchProviderRateLimitedShowsMessage` (200 with a visible "try again" message), `TestSearchPartialOnHTMX`.
- [ ] **Step 2:** Run `go test ./internal/web/...`. Expected: FAIL.
- [ ] **Step 3:** Implement; the query box uses `hx-get` with `hx-trigger="keyup changed delay:400ms, submit"` and `hx-target` set to the results container.
- [ ] **Step 4:** Run the tests. Expected: PASS.
- [ ] **Step 5: Commit** `feat: anime search`.

### Task 8: Overview page and add to list

**Files:**
- Create: `internal/web/anime.go`, `internal/web/templates/anime.html`, `internal/web/templates/add_dialog.html`
- Test: `internal/web/anime_test.go`

**Interfaces:**
- Consumes: Tasks 4 to 7. 
- Produces: `GET /anime/{source}/{id}` (saved entry rendered from the database, otherwise live from the provider; recommendations and cast fetched live only when not saved); `GET /anime/{source}/{id}/add` (dialog partial); `POST /anime/{source}/{id}/add` (fields `status`, `rating` (blank = unrated, 0 to 10), `progress`, `started_on`, `finished_on`, `comment`) which saves the anime, enqueues job kind `sync_anime` with payload `{"anime_id": int}`, upserts the list entry, then redirects to `/anime`; validation errors re-render the dialog with the message and status 422. Person rows link to the provider page (`Person.URL`) and, when saved locally, also `/people/{id}`.

- [ ] **Step 1: Write failing tests**: `TestOverviewLiveForUnsaved`, `TestOverviewFromDatabaseForSaved`, `TestOverviewShowsAllTitleLanguages`, `TestOverviewSynopsisHasNoRawHTML`, `TestAddCreatesEntryAndJob`, `TestAddRejectsInvalidRating` (422, nothing saved), `TestAddDuplicateIsBlocked`, `TestAddWithProviderDownUsesAlreadyFetchedDetails` (the overview data was fetched, the add succeeds without another provider call), `TestPersonLinksExternalOnlyWhenUnsaved`.
- [ ] **Step 2:** Run `go test ./internal/web/...`. Expected: FAIL.
- [ ] **Step 3:** Implement. The add form posts only the provider ID; the handler re-fetches the details once through the provider and saves them (if that fetch fails, show the provider error and keep the form).
- [ ] **Step 4:** Run the tests. Expected: PASS.
- [ ] **Step 5: Commit** `feat: overview page and add to list`.

### Task 9: Covers and background jobs

**Files:**
- Create: `internal/covers/covers.go`, `internal/jobs/jobs.go`, `internal/jobs/sync_anime.go`
- Modify: `cmd/allister/main.go` (start worker), `internal/web/server.go` (serve `/covers/` from `DATA_DIR/covers`)
- Test: `internal/covers/covers_test.go`, `internal/jobs/jobs_test.go`, `internal/jobs/sync_anime_test.go`

**Interfaces:**
- Consumes: `store`, `provider`.
- Produces: `covers.Download(ctx, hc *http.Client, dir, url string) (relPath string, err error)` (content-hash filename, rejects non-image content types); `jobs.Queue` with `Enqueue(ctx, kind string, payload any) error` and `Run(ctx)` polling the `jobs` table, failed jobs retried with backoff (`attempts` up to 5, then `state='failed'` and `last_error` set); `jobs.SyncAnime` handler: downloads the anime cover, fetches cast through the AniList provider, downloads person images, calls `SaveCast`, sets `anime.sync_state` to `done`; on partial failure sets `partial` and re-queues. List and overview pages show a "partially synced" marker while `sync_state != 'done'`.

- [ ] **Step 1: Write failing tests**: `TestDownloadStoresFileByHash`, `TestDownloadRejectsNonImage`, `TestJobRetriesWithBackoff`, `TestJobGivesUpAfterFiveAttempts`, `TestSyncAnimeCompletes` (state `done`, cast stored), `TestSyncAnimeCastFailureLeavesPartial`, `TestPendingJobsSurviveRestart`.
- [ ] **Step 2:** Run the package tests. Expected: FAIL.
- [ ] **Step 3:** Implement. One worker goroutine is enough (and keeps AniList within its limit).
- [ ] **Step 4:** Run the tests. Expected: PASS.
- [ ] **Step 5: Commit** `feat: cover download and background sync jobs`.

### Task 10: List page

**Files:**
- Create: `internal/web/list.go`, `internal/web/templates/list.html`, `internal/web/templates/list_rows.html`
- Test: `internal/web/list_test.go`

**Interfaces:**
- Consumes: `store.List`, `store.IncrementProgress`, `store.UpsertListEntry`.
- Produces: `GET /anime` (all) and `GET /anime?status=watching|...` (tabs with counts); `POST /anime/{id}/increment` (htmx, returns the updated row); `GET /anime/{id}/edit` and `POST /anime/{id}/edit` (the same dialog fields as Task 8, for editing; 422 on validation errors); `POST /anime/{id}/remove`; extend `store.ListRow` with `Source provider.Source` and `MALID string`; a row shows a small "unmatched" badge when `Source == manual && MALID != ""`.

- [ ] **Step 1: Write failing tests**: `TestListFiltersByStatus`, `TestListShowsPreferredTitle`, `TestIncrementReturnsUpdatedRow`, `TestIncrementAtTotalDoesNotExceed`, `TestEditValidationReturns422`, `TestRemoveDeletesEntry`, `TestEmptyListShowsHint`, `TestListShowsUnmatchedBadge` (a manual entry with a `mal_id` shows the badge; a manual entry without one and an AniList entry do not).
- [ ] **Step 2:** Run `go test ./internal/web/...`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run the tests. Expected: PASS.
- [ ] **Step 5: Commit** `feat: anime list page`.

### Task 11: Local person pages

**Files:**
- Create: `internal/web/people.go`, `internal/web/templates/person.html`
- Test: `internal/web/people_test.go`

**Interfaces:**
- Consumes: `store.PersonAnime`.
- Produces: `GET /people/{id}` showing photo, name, an external link, and the saved anime they appear in (voice actors also show the character played); 404 for an unknown ID.

- [ ] **Step 1: Write failing tests**: `TestPersonPageListsSavedAnimeOnly`, `TestVoiceActorShowsCharacter`, `TestPersonPageHasExternalLink`, `TestUnknownPerson404`.
- [ ] **Step 2:** Run `go test ./internal/web/...`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run the tests. Expected: PASS.
- [ ] **Step 5: Commit** `feat: local character and voice actor pages`.

### Task 12: Manual entries

**Files:**
- Create: `internal/web/manual.go`, `internal/web/templates/manual_form.html`
- Modify: `internal/store/anime.go` (accept `Manual` source IDs generated as UUIDs)
- Test: `internal/web/manual_test.go`

**Interfaces:**
- Produces: `GET /anime/manual/new` and `POST /anime/manual/new` with fields `title`, `title_lang` (default the preferred language), `type`, `episodes`, `year`, `studio`, `synopsis`, `cover` (file upload, optional, image content type only) plus the list fields from Task 8; saves with `Source=provider.Manual`, `sync_state='done'`, no cast; the overview of a manual entry renders from the database only and offers a "Match to AniList" button (built in Task 15).

- [ ] **Step 1: Write failing tests**: `TestManualRequiresTitleAndType`, `TestManualSavesWithOptionalFieldsEmpty`, `TestManualCoverRejectsNonImage`, `TestManualEntryAppearsInList`, `TestManualOverviewHasNoCast`.
- [ ] **Step 2:** Run `go test ./internal/web/...`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run the tests. Expected: PASS.
- [ ] **Step 5: Commit** `feat: manual anime entries`.

### Task 13: Yamtrack import

**Files:**
- Create: `internal/importer/yamtrack/parse.go`, `internal/importer/yamtrack/import.go`, `internal/importer/yamtrack/testdata/sample.csv`, `internal/web/import.go`, `internal/web/templates/import.html`
- Modify: `internal/provider/anilist/client.go` (add `AnimeByMALID`), `internal/provider/anilist/testdata/*.json`
- Test: `internal/importer/yamtrack/parse_test.go`, `internal/importer/yamtrack/import_test.go`, `internal/web/import_test.go`, `internal/provider/anilist/client_test.go`

**Interfaces:**
- Consumes: `anilist.Client`, `store`, `jobs.Queue`.
- Produces: `(*anilist.Client).AnimeByMALID(ctx, malID string) (provider.Anime, error)` (AniList `Media(idMal:, type: ANIME)`; returns `provider.ErrNotFound` when AniList has no match); `yamtrack.Parse(r io.Reader) (rows []Row, skipped []SkippedRow, err error)` with `Row{MALID, Title, ImageURL string; Status store.Status; Rating *int; Progress int; StartedOn, FinishedOn *time.Time; Comment string}`; non-anime rows and unknown statuses go to `skipped` with a reason; a UTF-8 BOM is tolerated; `POST /settings/import` (multipart `file`) enqueues job kind `import_yamtrack`; `GET /settings/import` shows a progress page (total, done, failed, skipped list, unmatched list) that polls with `hx-trigger="every 3s"`; the job skips rows whose `mal_id` is already saved (`store.FindByMALID`) before making any request, resolves each remaining row with `AnimeByMALID`, saves it with source `anilist`, upserts the list entry, then enqueues `sync_anime`; rows AniList cannot match (`ErrNotFound`) are not dropped: they are saved as `manual` entries (source id a generated UUID) carrying the Yamtrack data (the CSV `title` as the entry's title with language tag `ja-latn`, the CSV `image` URL kept as the remote cover URL, status, score, progress, start and end dates, `notes` as comment) with `mal_id` set to the Yamtrack `media_id`, with `sync_state='done'` and no cast; they are recorded in the result as `unmatched`, counted, and listed on the progress page, each with a link to its entry (an entry is unmatched when source is `manual` and `mal_id` is not null; re-runs skip them via `FindByMALID`); requests are paced to AniList's rate limit (a constant of 30 requests per minute, the currently degraded limit), so ~830 entries take on the order of an hour or two; the job is resumable and a re-run creates no duplicates.
- **Column mapping (from the owner's sample export, see the ledger):** columns `media_id`, `source`, `media_type`, `title`, `image`, `season_number`, `episode_number`, `score`, `status`, `notes`, `start_date`, `end_date`, `progress`, `created_at`, `progressed_at`; anime rows only (by `media_type`); `media_id` is the MyAnimeList ID. Statuses: `Completed` to `completed`, `Planning` to `plan_to_watch`, `Dropped` to `dropped`, `Paused` to `paused`, `In progress` to `watching`; anything else is skipped as an unknown status. Score `""` or `"0.0"` is unrated, otherwise the rounded integer. Dates use the layout `2006-01-02 15:04:05-07:00`. `notes` maps to `comment`. Tests use a trimmed hand-made fixture `testdata/sample.csv`; the real export is git-ignored.

- [x] **Step 0:** Obtain the sample export from the owner (done; mapping above).
- [ ] **Step 1: Write failing tests**: `TestAnimeByMALIDMapsAnime`, `TestAnimeByMALIDNotFound` (fixture with no match gives `provider.ErrNotFound`), `TestParseTakesAnimeRowsOnly`, `TestParseToleratesBOM`, `TestParseReportsUnknownStatus`, `TestImportSavesEntriesAndEnqueuesSync`, `TestImportTwiceCreatesNoDuplicates`, `TestImportResumesAfterInterruption` (second run only fetches the unfinished rows), `TestImportSkipsAlreadySavedByMALID` (no provider request for rows whose `mal_id` is saved), `TestImportSavesUnmatchedAsManualEntry` (an `ErrNotFound` row is saved with source `manual`, `mal_id` set to the `media_id`, status, score and progress preserved, and the cover URL kept), `TestImportUnmatchedStillCountedAndListed` (the row is counted and listed as `unmatched` with a link to its entry, and a re-run does not duplicate it), `TestImportContinuesAfterOneRowFails`, `TestImportPageShowsProgress`.
- [ ] **Step 2:** Run the package tests. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run the tests. Expected: PASS.
- [ ] **Step 5: Commit** `feat: yamtrack csv import`.

### Task 14: README and end-to-end check

**Files:**
- Create: `README.md`, `internal/web/e2e_test.go`
- Modify: `docker-compose.yml` (final volume and port docs)

**Interfaces:**
- Consumes: all previous tasks.

- [ ] **Step 1: Write failing test** `TestSearchAddListFlow`: with a fake AniList provider, search, open overview, POST add with status `watching` and progress 3, then GET `/anime` shows the title and `3 /`; POST increment and see `4`.
- [ ] **Step 2:** Run `go test ./...`. Expected: FAIL until all wiring in `main.go` is complete.
- [ ] **Step 3:** Finish wiring in `cmd/allister/main.go` (providers, store, queue, server). Write the README: what it is, `docker compose up`, port and volume, title language setting, import instructions, matching manual and unmatched entries to AniList, source rate-limit note.
- [ ] **Step 4:** Run `go test ./...` (PASS), `docker compose up --build -d`, then in a browser search for an anime on AniList, add it, and check the list, the settings language switch and a person page. Expected: all work; then `docker compose down`.
- [ ] **Step 5: Commit** `docs: readme and end-to-end test`.

### Task 15: Match a manual entry to AniList

Built after the first working version (Tasks 1 to 14).

**Files:**
- Create: `internal/provider/anilist/url.go`, `internal/store/match.go`, `internal/web/match.go`, `internal/web/templates/match_dialog.html`
- Modify: `internal/web/templates/anime.html` ("Match to AniList" button, shown for manual entries only), `internal/web/server.go` (register routes)
- Test: `internal/provider/anilist/url_test.go`, `internal/store/match_test.go`, `internal/web/match_test.go`

**Interfaces:**
- Consumes: `anilist.Client` (`Search`, `Anime`), `store.FindBySource`, `jobs.Queue`, `titles.Resolve`, the dialog pattern from Task 8.
- Produces:
  - `anilist.ParseAnimeURL(s string) (id string, ok bool)`: accepts `https://anilist.co/anime/154587`, `anilist.co/anime/154587/Sousou-no-Frieren`, a trailing slash and a bare number; rejects other paths (`anilist.co/manga/1`), other hosts and garbage.
  - `(*Store) ConvertToSource(ctx context.Context, animeID int64, a provider.Anime) error`: in one transaction converts the manual entry in place (same `anime.id`, `list_entries` row untouched): source `manual` to `a.Source`, `source_id` to `a.ID`; replaces titles, relations, synopsis, studio, episodes, year, dates, source material, external URL and cover (custom uploaded cover replaced); sets `mal_id` to `a.MALID` if non-empty, else keeps it; sets `sync_state='pending'`. Returns `ErrAlreadySaved` when `(a.Source, a.ID)` belongs to another entry, as `type AlreadySavedError struct{ ExistingID int64 }` that satisfies `errors.Is(err, ErrAlreadySaved)`; returns `ErrInvalid` when the target entry is not a manual entry. Any failure rolls everything back.
  - Routes: `GET /anime/{source}/{id}/match` (dialog partial with the URL/id field and a search box; 404 unless the entry is saved and manual); `GET /anime/{source}/{id}/match/search?q=` (htmx partial of AniList results to pick from, using the existing search; empty query makes no request); `POST /anime/{source}/{id}/match` (field `anilist` for a URL or id, or `pick` for a chosen AniList id): fetches the details through the provider, calls `ConvertToSource`, enqueues `sync_anime` for the entry, and redirects 303 to `/anime/anilist/{newid}`. Otherwise it re-renders the dialog with status 422 and a message: invalid URL, unknown id (`ErrNotFound`), or the already-saved refusal with a link to the existing entry; a provider outage or rate limit (`ErrUnavailable`, `ErrRateLimited`) shows a retry message and changes nothing.

- [ ] **Step 1: Write failing tests**:
  - `TestParseAnimeURL` (table: full URL, URL with slug, no scheme, trailing slash, bare number, `anilist.co/manga/1` rejected, garbage rejected, `https://example.com/anime/5` rejected).
  - Store: `TestConvertKeepsListEntry` (status, rating, progress, dates, comment and `anime.id` unchanged), `TestConvertReplacesTitlesAndSource` (source `anilist`, new `source_id`, old titles gone, new titles, relations and `mal_id` rule applied, `sync_state='pending'`), `TestConvertRefusesDuplicate` (`errors.Is(err, ErrAlreadySaved)`, existing id carried, nothing changed), `TestConvertRefusesNonManual` (`ErrInvalid`), `TestConvertIsAtomicOnFailure` (a forced failure midway leaves the entry exactly as before).
  - Web, with a fake provider: `TestMatchDialogOnlyForManual` (200 for manual, 404 for AniList entries), `TestMatchByURLConverts` (303 to `/anime/anilist/{newid}`), `TestMatchByPickConverts`, `TestMatchUnknownIDShowsError` (422, message, nothing changed), `TestMatchProviderDownChangesNothing` (retry message, entry unchanged), `TestMatchEnqueuesSync` (one `sync_anime` job for the converted entry).
- [ ] **Step 2:** Run `go test ./internal/provider/... ./internal/store/... ./internal/web/...`. Expected: FAIL.
- [ ] **Step 3:** Implement. The handler fetches the AniList details first and only then opens the transaction, so a provider failure never touches the database; the URL field and the pick both reduce to an AniList id before the fetch.
- [ ] **Step 4:** Run the tests. Expected: PASS. Then in a browser, match an unmatched imported entry by URL and by search, and check that its list data is unchanged and the cast arrives.
- [ ] **Step 5: Commit** `feat: match manual entries to anilist`.

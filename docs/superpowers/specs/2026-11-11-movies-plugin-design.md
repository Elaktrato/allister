# Movies plugin and source API keys (design, 2026-11-11)

## Intent

Add the movies plugin in the shape of the existing media plugins, and make the
TMDB API key a first-class setting the owner can enter in the web interface.
TV and Movies share one TMDB key. The key takes effect immediately: no restart,
no environment edit.

The owner's Yamtrack export has 9 movie rows, all TMDB ids, all completed with
scores; importing them is the first real data. Movies have no calendar (TMDB
has release dates but no air times), so the calendar stays as it is.

## Goals

- One **Movie** kind: Want to watch / Watching / Paused / Seen / Dropped, an
  optional playback position, director, studio, country, language, collection,
  release status, runtime, year, synopsis, release date, genres and tags.
- TMDB as the source: search, details, cast, director/writer credits,
  recommendations, external ids (TMDB, IMDb), cover and link.
- **API keys** card in Settings > Plugins: one TMDB field shared by TV and
  Movies. Save, replace, remove; the stored key is never echoed. The
  `TMDB_API_KEY` environment variable remains a fallback.
- The key is read per request, so saving it makes TMDB usable at once.
- A missing or refused key is a clear, actionable message, not a retry loop.
- Yamtrack movie CSV import: status, score, dates, notes, cover, TMDB id;
  TMDB resolves automatically when a key is configured, otherwise the row is
  kept as a manual movie.
- JSON export, universal backup, docs, and the usual tests.

## Non-goals

- Other movie sources (Letterboxd, OMDb, IMDb scraping, Wikipedia). TMDB only.
- Other movie imports (Letterboxd, IMDb, Trakt). Yamtrack only.
- Calendar events for movie releases.
- A movie collection as a separate kind or child items. `collection` is a text
  field.
- A general secrets vault, authentication, or encryption at rest. Only source
  API keys, stored as a setting.
- Rebuilding or replacing the plugin registry on save (approach B), or
  requiring a restart (approach C).
- Rewriting TV's behavior beyond moving its TMDB HTTP plumbing to the shared
  client and always declaring the TMDB source (gated by the key at call time).

## Approaches considered

- **A. Key in settings, source reads it live (chosen).** The manifest always
  declares TMDB and says it needs a key; the source asks a key provider per
  request; the settings table stores the key. One field serves TV and Movies;
  no restart; no mutable registry. A small contract addition makes the UI
  generic.
- **B. Rebuild the registry when the key is saved.** Keeps conditional
  manifests, needs a replace/reload operation and mutable plugin snapshots.
  More moving parts for the same user-visible result; rejected.
- **C. Store the key, apply it at startup.** Smallest, but the owner asked for
  the key in the interface, which should mean immediate effect; rejected.

## Constraints

- Plugin contract data only: no plugin HTML/JS; core guard; no hard-coded
  colours. The API keys UI is core, so it says "source", "API key", not
  "movie" or "TMDB".
- No new Go dependencies; HTTP via the standard library.
- No database migration: the `settings` table exists. (AGENTS.md's "next free
  migration" note is stale at 00014; the real next number is 00017. This work
  fixes that line.)
- TMDB rate limit: declare 30 requests/minute and rely on the core's pacing.
- The key is never logged, never rendered, never put in an export or backup,
  and never included in an error message (including the request URL).
- README stays at or below 150 lines.
- Preview only on port 18083 / project `allister-preview`; the owner's
  18080-18082 containers stay untouched. The owner pushes.

## Source credentials

### Contract

`plugin.SourceInfo` gains two optional fields:

```go
// KeyLabel, when set, means the source needs a credential the user stores
// in Settings; the core shows this label ("API key", "Access token").
KeyLabel string `json:"key_label,omitempty"`
// KeyHelp is one optional sentence telling the user where to get it.
KeyHelp  string `json:"key_help,omitempty"`
```

Both are single lines. `KeyLabel` is at most 40 characters, `KeyHelp` at most
200, neither has control characters. `KeyHelp` without `KeyLabel` is invalid.
The fields are optional, so the contract version stays 1.

The key itself is not in the manifest. A source ID is the credential's
identity: `source.<source-id>.key` in the settings table. TV and Movies both
declare `tmdb`, so they share `source.tmdb.key`. Source ids are global by
convention.

### Storage and resolution

`internal/settings` gains:

```go
func (s *Store) SourceKey(ctx context.Context, sourceID string) (string, error)
func (s *Store) SetSourceKey(ctx context.Context, sourceID, key string) error
func (s *Store) ClearSourceKey(ctx context.Context, sourceID string) error
```

The stored value is trimmed. A missing row is `""` with no error.

`cmd/allister` builds one resolver:

```go
fallback := map[string]string{"tmdb": cfg.TMDBKey}
sourceKey := func(ctx context.Context, sourceID string) (string, error) {
    k, err := settingsStore.SourceKey(ctx, sourceID)
    if err != nil || k != "" {
        return k, err
    }
    return fallback[sourceID], nil
}
```

- The stored key wins; an empty store falls back to the environment.
- TV and Movies receive `func(ctx context.Context) (string, error)` closures
  for TMDB (`return sourceKey(ctx, "tmdb")`).
- `web.Deps` gains `SourceKey func(ctx context.Context, sourceID string)
  (string, error)` for the keys card and for "needs key" labels. When nil,
  the web reads only `Settings.SourceKey` (tests).
- Saving or removing a key needs no plugin reconstruction: the next source
  call reads the new value.

### Missing or refused key

`plugin` gains a sentinel:

```go
// ErrNotConfigured: the source cannot answer because the credential it
// needs is missing or was refused.
ErrNotConfigured = errors.New("plugin: source is not configured")
```

The TMDB client returns it when the resolver yields an empty key and on HTTP
401/403. It is not retryable. `ErrNotConfigured` joins the documented source
errors in `docs/plugins.md` and AGENTS.md.

Web behaviour:

- `sourceMessage` answers: "<Source> needs a valid <key label>. Add or replace
  it in Settings > Plugins." (`KeyLabel` lower-cased; a neutral sentence when
  the source declares none).
- Search and match keep keyed sources visible in their source pickers (so the
  user can see what the plugin offers) but append " (needs key)" to the label
  when no effective key is set. Selecting one shows the message above.
- The source picker still defaults to the first source. For Movies that is
  TMDB, so a search without a key explains the setting instead of failing
  silently.
- An item already saved from TMDB keeps rendering from the database when the
  key is removed; only live calls need the key.

Web status matrix (all use the single `sourceMessage` sentence):

| Surface | Status | Behaviour |
|---|---|---|
| Search | 503 | results page, message, retry link |
| Live overview (`/{plugin}/{source}/{id}`) | 503 | error page, message, retry link |
| Add dialog (GET/POST) | 503 | dialog re-rendered, form kept, message |
| Match search/POST | 503 | match dialog re-rendered, nothing changed, message |
| Recommendations fragment | 200 | failed block with the message (no 500, no auto-retry) |

`itemFail` and `addFailure` add `ErrNotConfigured` to their 503 branches; the
recommendations view gains an optional message, keeping its generic text when
unset.

Background sync: `jobs.SyncItem` treats `ErrNotConfigured` from
`Source.People` as "no credits this time": it keeps the stored credits, skips
person-image downloads, still finishes the cover/schedule steps, and ends the
sync `done`. It does not return a retryable error and does not leave the item
`partial`, so `SweepSync` does not requeue it at every start. Adding the key
later does not automatically re-sync an existing item; saving or editing it
enqueues the usual sync.

Import behaviour:

- The importer runner treats `ErrNotConfigured` like `ErrNotFound` for
  resolution: try the next source, then import a manual row when the importer
  allows manual rows. It remembers the last lookup error; the import report
  row says the source is not configured when that was the last miss
  (otherwise its usual "not found on ...").
- The item's stored badge is the static `UnmatchedNote`. The movie importer's
  note is "Imported from Yamtrack; TMDB could not resolve #{id}.", true both
  for a real miss and for a missing key. TV's existing note stays.
- The TV and Movie Yamtrack importers therefore always declare
  `ResolveVia: "tmdb"`; a keyless import yields manual rows, and a re-import
  after adding the key keeps the existing items (the user can Match them).

### API keys card

`GET /settings/plugins` gains an **API keys** card above the plugin list. It
lists every distinct source with `KeyLabel` across installed manifests
(enabled or not), in registration/manifest order. Each block shows the source
name, `KeyLabel`, `KeyHelp`, and a form:

- No effective key: password input + Save.
- Effective key configured: "A key is configured." + password input to replace
  + Save; a Remove button only when a stored key exists (an environment key
  cannot be removed here).
- The stored value is never placed in HTML, not even masked. The input has
  `type="password"`, `autocomplete="new-password"`, `spellcheck="false"`, and
  no value.
- `POST /settings/plugins/keys/{source}`:
  - `remove=1` clears the stored key and redirects.
  - Otherwise the `key` field is trimmed and validated: 1-500 printable ASCII
    characters (0x21-0x7E), so no spaces or control characters. Empty is an
    error ("Enter a key, or use Remove...").
  - Unknown or unkeyed source ids answer 404.
  - Success redirects 303 to `/settings/plugins#source-keys`.
- Each plugin settings page whose manifest has an unconfigured keyed source
  shows a short notice with a link to that card. TV's wording notes that
  TVmaze works without a key.
- The card is hidden when no installed source needs a key.

TMDB accepts both credential forms: a v3 API key (query `api_key`) and a v4
API Read Access Token (JWT, sent as `Authorization: Bearer`). The client
detects the `eyJ` prefix. The UI help says either works.

### Security

- The key is a setting in SQLite, plaintext, like the rest of the database;
  the DB is the trust boundary. No encryption at rest in this work.
- Backups copy items, covers and fonts, not general settings, so the key is
  not in a backup or a JSON export. Restoring on a new machine needs the key
  re-entered.
- Errors and logs never include the key or the request URL. The TMDB client
  does not wrap `http.Client.Do` errors directly (they can contain the URL).
  Tests assert the key does not appear in returned errors.

## Kind: Movie

Plugin `movie`, name "Movies", page `/movie`, icon `movie`,
`DefaultEnabled: false`. Kind key `movie`, labels Movie/Movies.

Fields:

| Key | Type | Notes |
|---|---|---|
| `year` | number | 1900-2100; shown, editable, filterable, sortable |
| `runtime` | number | 1-10000, unit `min`; shown, editable |
| `director` | text | first director; shown, editable, filterable |
| `studio` | text | first production company; shown, editable, filterable |
| `country` | text | first production country; shown, editable, filterable |
| `language` | text | spoken language of the original; shown, editable, filterable |
| `collection` | text | TMDB collection name; shown, editable, filterable |
| `release_status` | enum | `released`, `upcoming`, `other`; shown, editable, filterable |
| `synopsis` | text | editable; the Summary section |
| `released_on` | date | shown, editable |

Genres and tags enabled, editable and filterable.

Statuses: Want to watch, Watching, Paused, Seen, Dropped.

Progress:

- `position` (duration, label Position). Defaults `[position]`, combinations
  `[[position]]`, no primary key (so no +1). No `TotalField`: runtime is in
  minutes, a duration is in seconds, and the position may run past a wrong
  runtime. The position is optional and edited in the entry dialog; it is not
  used as the watched marker. Seen is the watched state.

Sections: Info (Details), Text (Summary, `synopsis`), Titles, People (roles
`actor`, `director`, `writer`), Genres, Tags, Recommendations, Links.

Titles: `en`, `original`; fallback `en`.

Columns: Year (from sm), Score, Status, Updated (from lg) by default;
Progress, Runtime, Director, Studio, Country, Language, Collection and
Released optional. Progress is not a default column for movies: the position
is a convenience, not the main state.

## TMDB movie source

`SourceInfo`:

- ID `tmdb`, name "TMDB", 30 requests/minute, `Kinds: [movie]`,
  `ExternalIDTypes: [tmdb, imdb]`, `SupportsLookup: true`, no schedule.
- `KeyLabel: "API key"`, `KeyHelp`: "Create a free key at themoviedb.org under
  Settings → API." (one line).
- Link "View on TMDB", URL pattern `themoviedb.org/movie/{id}`.

Endpoints:

- Search: `GET /search/movie?query=...&include_adult=false`; map id, title,
  original_title, release_date year, poster. No status/runtime in results.
- Details: `GET /movie/{id}?append_to_response=credits,recommendations,external_ids,keywords`;
  map title/original, overview, status, release_date, runtime, genres,
  production companies/countries, spoken languages, belongs_to_collection,
  poster, keywords, credits, recommendations, external ids (IMDB; TMDB from
  the id).
- People: the details call, mapping cast and crew.
- Recommendations: `recommendations.results`.
- Lookup: `tmdb` → details; `imdb` → `/find/{id}?external_source=imdb_id`,
  first `movie_results` entry, then details. Other id types → `ErrNotFound`.

Mapping details:

- `released_on`: `release_date`; `year` from it.
- `release_status`: TMDB `Released` → `released`; `Planned`, `In Production`,
  `Post Production`, `Rumored` → `upcoming`; `Canceled` and anything else →
  `other`.
- `country`: first `production_countries[].name`; `language`: the
  `spoken_languages[]` entry whose `iso_639_1` is `original_language`, else the
  first English name, else the code; `studio`: first
  `production_companies[].name`; `collection`: `belongs_to_collection.name`.
- Cast: character as `Person` (`character-<credit_id>`), actor as `With`
  (`person-<id>`), role `actor`, `Detail` MAIN for the first 10 by order, else
  SUPPORTING.
- Crew: `job == "Director"` → role `director`; jobs `Screenplay`, `Writer`,
  `Story` → role `writer`. Person id `person-<id>`, `Detail` the job. Dedupe
  by person id + role.
- Keywords: tolerate both `keywords.keywords` and `keywords.results` shapes
  (the movie append response differs from TV's).
- Recommendations: same summary mapping as search; cap 20.
- All text goes through the sanitising helpers and the TV mapping caps (title
  300, text 20k, term 60, name 300, id 100, genres 50, tags 100, credits 500).

## Shared TMDB client and TV changes

New package `internal/plugins/tmdb`:

```go
type KeyFunc func(context.Context) (string, error)

func New(base string, key KeyFunc, hc *http.Client) *Client
func (c *Client) Get(ctx context.Context, path string, values url.Values, out any) error
func (c *Client) Find(ctx context.Context, externalID, source string) (FindResult, error)
func Image(path, size string) string
```

- `Get` resolves the key per request, clones the query values, sends the key as
  `api_key` or a bearer token, limits the body to 8 MiB, and maps:
  404 → `ErrNotFound`, 429 → `ErrRateLimited`, 401/403 → `ErrNotConfigured`,
  5xx/timeout → `ErrUnavailable`, other non-200 → `ErrBadResponse`.
- Empty key → `ErrNotConfigured`; a resolver error → `ErrUnavailable`.
- `Find` returns `MovieResults`/`TVResults` ids.
- No key in errors; no request URL in wrapped errors.

TV's `internal/plugins/tv/tmdb.go` keeps its structs/mapping but uses
`*tmdb.Client`; its tests use the shared client with a fake key func. Its
manifest **always** declares the TMDB source (with `KeyLabel`/`KeyHelp`) and
always sets the Yamtrack importer's `ExternalIDType`, `ExternalIDLabel`,
`ResolveVia` and `UnmatchedNote`. `tv.Options.TMDBKey` becomes a
`tmdb.KeyFunc`; `tv.New` always builds the source. Existing TV tests are
updated for the always-present source and the no-key `ErrNotConfigured`
behaviour.

## Yamtrack movie import

Importer `yamtrack-movie`, name "Yamtrack Movie CSV", accept `.csv`,
`ExternalIDType: "tmdb"`, `ExternalIDLabel: "TMDB"`,
`ResolveVia: "tmdb"`, `ManualRows: true`, `FallbackTitleLang: "en"`,
`UnmatchedNote: "Imported from Yamtrack; TMDB could not resolve #{id}."`.

Parse with the shared `yamtrack.ParseRaw`:

- Keep `media_type == "movie"` case-insensitively; every other row counts as
  ignored.
- Group by `source|media_id`; title-only rows group by `source|title`.
  Duplicate groups are skipped with a reason.
- A row with no title and no id is skipped. A title with a valid numeric
  `media_id` **and** `source == "tmdb"` keeps `ExternalIDs["tmdb"]`; every
  other source (including a numeric id from elsewhere) stays manual with a
  note. The importer's single external id type is TMDB, so IMDb rows are not
  resolved through it.
- Use the mapped status; unknown/blank status is skipped. Score, start/end
  dates, comment and cover URL are mapped as in the TV importer.
- Yamtrack `progress` is a watch count, not a playback position; it is not
  mapped. A `Completed` row with `progress: 1` becomes a Seen entry with no
  position.
- Other media types are ignored; no movie rows makes the runner answer its
  usual "no importable movies rows".

The owner's 9 rows should import as 9 entries (all Completed, their scores and
dates); with no key they are manual with TMDB ids, with a key they resolve to
TMDB items. A re-import under the skip rule keeps the existing items.

## Web routes

| Route | Purpose |
|---|---|
| `/movie` | Movie list (generic list/search/filter/view parameters) |
| `/movie/search?q=...&source=tmdb` | TMDB movie search |
| `/movie/tmdb/{id}` | Movie overview; `/edit`, `/match`, `/add` as usual |
| `/movie/manual/new`, `/movie/manual/{id}` | Manual movies |
| `/movie/export/json` | JSON export |
| `POST /settings/plugins/keys/{source}` | Save/remove a source key |
| `/settings/plugins` | API keys card + plugin list |

No calendar routes or schedule capability.

## Plugin wiring

`internal/plugins/movie`:

- `manifest.go`: kind, TMDB source, Yamtrack importer, JSON exporter.
- `mapping.go`: movie record → `plugin.ItemData`/credits/titles/terms.
- `tmdb.go`: movie endpoints and mapping on the shared client.
- `yamtrack.go`: movie CSV aggregation.
- `plugin.go`: `Options{HTTP *http.Client; TMDBURL string; TMDBKey tmdb.KeyFunc}`,
  sources/importers/exporter maps. `New` always provides the TMDB source.
- `cmd/allister`: build the source-key resolver, pass it to TV and Movies and
  to `web.Deps`, register Movies after TV.
- `internal/config`: `TMDBKey` stays as the env fallback.
- `docker-compose.yml`: unchanged.

## Docs

- README: Movies in the plugin list, one sentence on the API key setting and
  the env fallback; 150-line cap.
- `docs/usage.md`: `/movie` routes, the API keys card, the no-key/manual import
  behaviour, no calendar, key storage/backup caveat.
- `docs/plugins.md`: `SourceInfo.KeyLabel`/`KeyHelp`, `ErrNotConfigured`, the
  `source.<id>.key` setting, and that the host passes the key to the plugin
  (the manifest never holds it).
- `docs/handoff.md`: Movies features, the API keys card, the shared TMDB
  package, TV's always-declared TMDB source; remove movies from the
  not-built list.
- AGENTS.md: the next free migration is 00017 (fix the stale 00014), and the
  settings line mentions source keys. Toast "movies are planned" lines in
  README/handoff become "games are planned".

## Testing and verification

- Contract: JSON round trip and validation for `KeyLabel`/`KeyHelp`
  (lengths, control characters, help without label).
- Settings: set/get/clear `source.tmdb.key`; empty means unset.
- Web: the API keys card lists TMDB once for TV+Movies; save/replace/remove;
  the key is never in the response body; invalid/empty/too long keys are
  refused; the plugin settings notice appears when unconfigured; keyed source
  labels say "(needs key)"; `ErrNotConfigured` maps to the actionable message
  on search (503), live overview (503), add dialog (503), match (503) and the
  recommendations fragment (200, message, no retry).
- Sync job: `ErrNotConfigured` keeps stored credits, skips people images,
  finishes `done` and does not leave a `partial` item for `SweepSync`.
- Shared TMDB client: v3 query, v4 bearer, empty key, 401/403, 404, 429, 5xx,
  body limit, no key in errors, `Find` movie/TV ids.
- TV: manifest always declares TMDB and always resolves the importer via it;
  no-key calls return `ErrNotConfigured`; with a key the existing behaviour
  holds.
- Movie: manifest validation; field/term/credit mapping and caps; search,
  details, cast/crew, recommendations, lookup, errors; no key; key refused.
- Import: synthetic movie CSV with statuses, scores, dates, a duplicate, an
  unknown status, a no-id row, a numeric id with a non-TMDB source, another
  media type and a no-key manual path; a local-only check of the owner's 9
  real rows (never committed).
- Docs tests, full suite, vet, fmt.
- Preview on 18083: enable Movies; check the API keys card, save a dummy key
  and see the immediate "valid key" response, remove it; import a small CSV
  without a key and check the manual movie; JSON export; no calendar entries.
  A real TMDB search/details check needs the owner's key entered in the
  preview UI.

## Decisions left to the builder

- Exact helper names and whether the movie mapping shares a cleaning helper
  with TV or duplicates the few lines.
- Whether the API keys card groups sources by plugin or shows the flat
  deduplicated list (the flat list is intended).
- The password input placeholder and the wording of the "needs key" label.
- Whether saving a key validates it against TMDB (it does not: the first use
  reports a refused key; no extra network call on save).
- The exact test fixture content.

# TV plugin (design, 2026-11-11)

## Intent

A TV show tracker as an opt-in plugin, using anime/manga/books as the pattern:
one **Show** kind, TVmaze as the free primary source, optional TMDB with an API
key, a Yamtrack TV CSV import (the owner's existing data), JSON export and the
generic backup. The calendar gains TV episodes from TVmaze.

MyDramaList is deferred to issue #2; Trakt and TV Time imports are deferred to
issue #3.

## Goals

- Search and add TV shows; TVmaze needs no key, TMDB is optional.
- Track a show with season + episode + optional position, episode primary.
- Import the `tv`/`season`/`episode` rows of a Yamtrack CSV into one show entry.
- Show cast, genres (and TMDB keywords as tags), recommendations and links.
- Calendar: TVmaze episode air times, with the season in the label.
- Enable in Settings > Plugins; JSON export; backup.

## Non-goals

- Movies, games or other media types (later plugins).
- Deferred sources/importers: MyDramaList (issue #2), Trakt and TV Time (issue #3).
- Scraping, undocumented app APIs, or write-back to a source.
- A per-season or per-episode item hierarchy. One show entry, season and
  episode progress; the calendar is the only per-episode metadata.
- Automatic TMDB/TVmaze resolution for every Yamtrack row; rows without a
  usable id stay manual and can be matched later.

## Approaches considered

- **A. TVmaze primary + optional TMDB + Yamtrack CSV (chosen).** TVmaze is
  free, has episode air dates and cast; TMDB adds richer data but needs a key.
  The owner's Yamtrack export is the first import. The calendar works for
  TVmaze shows; TMDB shows have no schedule.
- **B. TVmaze only.** Simplest, but cannot resolve Yamtrack's TMDB ids or give
  richer metadata. Rejected as the only source; kept as the no-key baseline.
- **C. TMDB primary + TVmaze schedule.** Requires a key for every user and a
  cross-source schedule bridge (a TMDB item has no TVmaze schedule). More
  moving parts; rejected for the first version.
- **D. Add Trakt/TV Time now.** OAuth and ZIP parsing; deferred to issue #3.

## Constraints

- Plugin contract data only: no plugin HTML/JS; core guard; no hard-coded
  colours.
- No new Go dependencies. HTTP via the standard library.
- TVmaze rate limit is roughly 20 requests per 10 seconds; use 30/minute and
  rely on the core's pacing. TMDB uses `TMDB_API_KEY`; never log the key.
- TVmaze summaries are HTML; sanitise with the shared plain-text helper and
  `plugin.MultiLine`. All titles/names/URLs use the usual sanitising helpers.
- No new database migration for the plugin. **One core migration** is needed
  for `Airing.Season` (below): the next free number is 00016.
- The README hard cap (150 lines) still applies.

## Kind: Show

Key `show`, labels Show/Shows.

Fields:

| Key | Type | Notes |
|---|---|---|
| `type` | enum | series, miniseries, reality, documentary, other; shown, editable, filterable |
| `year` | number | 1900-2100; shown, editable, filterable, sortable |
| `seasons` | number | 1-1000; total seasons, shown, editable |
| `episodes` | number | 1-100000; total episodes, shown, editable |
| `runtime` | number | 1-10000 minutes; shown, editable |
| `network` | text | shown, editable, filterable |
| `country` | text | shown, editable, filterable |
| `air_status` | enum | running, ended, upcoming, other; shown, editable, filterable |
| `synopsis` | text | editable; the Summary section |
| `started_on` | date | shown, editable |
| `ended_on` | date | shown, editable |

Genres and tags enabled (labels Genres/Tags), editable and filterable.

Statuses: Plan to watch, Watching, Paused, Watched, Dropped.

Progress:

- `season` (count, label Season, total field `seasons`)
- `episode` (count, label Episode, total field `episodes`)
- `position` (duration, label Position)
- Defaults `[season, episode]`; combinations `[[season, episode], [episode], [episode, position]]`; primary `episode`.

Sections: Info (Details), Text (Summary, `synopsis`), Titles, People
(Cast, roles `actor`), Genres, Tags, Recommendations, Links. No Related
section: neither source has a stable related-shows list worth a section.

Titles: `en`, `original`; fallback `en`. TVmaze `name` is `en`; an
`original_name` from TMDB is `original`. The title-language setting can pick
either.

Columns: Type, Year (from md), Progress, Score, Status, Updated (from lg) by
default; Seasons, Episodes, Network, Country, Started and Ended optional.

## Sources

### TVmaze (`tvmaze`)

- Name "TVmaze", 30 requests/minute, `Kinds: [show]`, external id types
  `tvmaze`, `tvdb`, `imdb`, `SupportsLookup: true`, `SupportsSchedule: true`,
  link "View on TVmaze", URL pattern `tvmaze.com/shows/{id}`.
- Base `https://api.tvmaze.com`; no key; descriptive User-Agent.
- Search: `GET /search/shows?q=...`; map the `show` object.
- Details: `GET /shows/{id}?embed[]=cast` and `GET /shows/{id}/episodes`.
  Show fields: name, type, language, genres, status, runtime, premiered,
  ended, officialSite, image, network/webChannel, externals, summary.
  Episodes supply total episodes and total seasons (distinct season numbers,
  season 0 specials excluded from the totals). Cast supplies character and
  actor.
- Lookup: `/shows/{id}` for `tvmaze`; `/lookup/shows?thetvdb=...` for `tvdb`;
  `/lookup/shows?imdb=...` for `imdb`.
- Fields: `year`/`started_on`/`ended_on` from premiered/ended, `type` mapped,
  `seasons`, `episodes`, `runtime`, `network` (network name else web channel),
  `country` (network country), `air_status` (Running -> running, Ended ->
  ended, To Be Determined/In Development -> upcoming, else other), synopsis,
  genres. No tags from TVmaze.
- Cast: `Person` is the character, `With` the actor, `WithRole: actor`,
  `Role: actor`; Detail empty or the source role if it has one. This matches
  the TV fixture and the People section. TVmaze character ids and person ids
  are separate numeric namespaces, so prefix them: `character-<id>` for the
  character, `person-<id>` for the actor. TMDB has no character id; use
  `character-<credit_id>` (unique per cast credit) and `person-<id>` for the
  actor. Never let a character id collide with an actor id in the people
  table.
- Cover: `image.original` (or `medium` when original is absent); external URL
  `https://www.tvmaze.com/shows/{id}`; external IDs `tvmaze`, `tvdb`, `imdb`.
- Schedule: `GET /shows/{id}/episodes`; return episodes with season >= 1,
  a numeric episode number and an `airstamp` (or airdate at 00:00 only if
  airstamp is absent? Prefer airstamp; skip date-only entries to avoid fake
  times). `Airing{Season, Number, AirsAt}`. The source declares schedule.
- Recommendations: none.
- Errors: 404 -> ErrNotFound, 429 -> ErrRateLimited, 5xx/network ->
  ErrUnavailable, malformed JSON -> ErrBadResponse.

### TMDB (`tmdb`, optional)

- Only declared when `TMDB_API_KEY` is set; no dead source in the UI.
- Name "TMDB", 30 requests/minute, `Kinds: [show]`, external id types `tmdb`,
  `imdb`, `tvdb`, `SupportsLookup: true`, `SupportsSchedule: false`, link
  "View on TMDB", URL pattern `themoviedb.org/tv/{id}`.
- Base `https://api.themoviedb.org/3`; key in the `api_key` query parameter;
  redact the URL in every error. Images use `https://image.tmdb.org/t/p/w500`.
- Search: `/search/tv?query=...`; details:
  `/tv/{id}?append_to_response=credits,recommendations,external_ids,keywords`.
  Fields: name/original_name, first/last air date, number_of_seasons,
  number_of_episodes, episode_run_time[0], networks[0], origin_country[0],
  status, type, overview, genres, keywords, credits.cast, recommendations,
  external_ids, homepage.
- Lookup: `tmdb` via details; `imdb`/`tvdb` via `/find/{id}?external_source=...`.
- Cast: `Person` character, `With` actor, role actor; TMDB `order` can fill
  `Detail` (MAIN for the first 10, else SUPPORTING) if desired. Use
  `character-<credit_id>` (or a stable hash of the character name when the
  credit id is absent) and `person-<id>`; never a bare numeric id for either.
- Recommendations: map the first 20 result summaries.
- Tags: TMDB keywords; genres from genres.
- No schedule: TMDB has dates but no air time; calendar events would be fake.
  A show added from TMDB has no calendar events; TVmaze is the calendar source.
- Errors and key redaction as above; 401/403 with an invalid key is
  ErrBadResponse (not a rate limit), 429 is ErrRateLimited.

### MyDramaList

Not part of this plugin. Issue #2 records the WIP official API, OAuth/app
tokens, Cloudflare and the missing episode schedule.

## Yamtrack TV import

### Parser

Extract the raw CSV reader/row parsing from
`internal/plugins/anime/yamtrack` into a shared `internal/plugins/yamtrack`
package used by both the anime and TV importers. The shared parser returns
generic rows (source, media id, media type, title, image, season number,
episode number, raw status plus the mapped status when known, rating,
progress, dates, notes, skipped rows). It does **not** filter a media type and
does **not** reject a blank status: callers decide what a blank status means.
The anime importer filters `anime`/`mal`, rejects unknown statuses as it does
today and keeps its existing output; its tests must stay green. The TV importer
filters `tv`, `season`, `episode` and ignores the other media types. The
generic parser keeps the CSV header/quoting/BOM handling and the score/date/
progress parsing in one place; the TV importer does not duplicate it.

### Aggregation

- Group rows by `(source, media_id)`. The `tv` row is the show; `season` and
  `episode` rows attach to it. Season rows must precede their episodes, as
  Yamtrack requires; a season/episode without a matching `tv` row is skipped
  with a reason.
- Show title/cover/status/rating/dates/notes come from the `tv` row. If the
  `tv` row lacks a title, use the first season/episode title.
- **An episode row is watched by being present**, not by its status. In the
  owner's actual export all 312 episode rows have an empty status and a
  non-empty end_date; treating blank status as unknown would import zero
  episode progress. The presence of the row is the watched marker; end_date
  is useful for dates but not required. Blank-status episode rows are never
  skipped.
- **Season rows carry the per-season watched count in `progress`.** The `tv`
  row's `progress` is the total watched episodes. Aggregate:
  - `episode` progress = the `tv` row's progress when non-zero, else the sum
    of the season rows' progress, else the count of distinct episode rows.
  - `season` progress = the highest season number whose season row has
    progress > 0 (or which has a watched episode row); if none, 0.
  - For a season row with no progress, fall back to the distinct episode rows
    in that season. A completed season with neither progress nor episode rows
    contributes its season number only, with a note.
  - The season/TV progress and the episode rows should agree; when they do
    not, the higher of the two per season wins, and the row gets a note.
- The show's `seasons`/`episodes` totals are not in the export; leave them
  empty for manual rows. If TMDB resolves the row, the source fills them.
- External IDs: `tmdb` when the row source is `tmdb` and the id is numeric;
  `tvdb` when the source is `tvdb`; `manual` has none. Store all available ids.
- A `tv` row with a title but no external id (including a `manual` source) is
  imported as a manual show; only a TV row without a title is skipped. A
  season/episode row without a parent `tv` row is skipped with a reason.
- `ImporterInfo`: id `yamtrack-tv`, name "Yamtrack TV CSV", `.csv`,
  `ExternalIDType: "tmdb"`, `ExternalIDLabel: "TMDB"`, `ManualRows: true`,
  fallback title language `en`. `ResolveVia: "tmdb"` only when the TMDB source
  is configured; otherwise `ResolveVia: ""` (manual-only). A row without a
  `tmdb` id becomes a manual show with its other ids; a row with a `tmdb` id
  resolves through TMDB when configured, otherwise it is a manual show with
  the TMDB id kept for later matching.
- Import modes are generic; the CSV has no reliable updated timestamp, so
  "newer" never updates (document this).
- `Ignored` counts anime/manga/game/movie/book/comic rows; `Skipped` lists
  TV rows with no title, no id, an unknown status or a bad structure. A file
  with no TV rows is `ErrBadInput`/NoRows, not a successful empty import.
- Trakt and TV Time are issue #3.

## Calendar: `Airing.Season`

TV needs season + episode, but `plugin.Airing` only has `Number` and `AirsAt`.

- Add `Season int` to `plugin.Airing` (JSON `season,omitempty`; 0 means no
  season, preserving anime/manga).
- Add `Season int` to `library.Airing`; migration **00016** rebuilds `airings`
  with `season INTEGER NOT NULL DEFAULT 0` and primary key
  `(item_id, season, number)` so the same episode number in two seasons does
  not collide. Existing rows become season 0. Down migration restores the old
  table. The `sync_schedule` job copies the season; `ScheduleEntries` selects
  it.
- Anime returns season 0, so its calendar UIDs and labels stay unchanged:
  `UID:item-number@allister`, summary "… — Episode 12".
- TV returns season > 0: UID includes `s<season>`, summary/page chip uses
  "Season 2, Episode 5" (ICS) / "S2E5" (chip). Skip specials with season 0
  from the TV source; they are not part of the show's progress.
- TMDB does not implement Schedule, so TMDB items have no events. The source
  declares `SupportsSchedule: false`.

## Export and backup

Shared `jsonexport` with format `allister.tv.export`; no plugin-specific
backup code. The backup contains both the item data and the list entries.

## UI and settings

- Sidebar "TV" with `live_tv`; route `/tv`; `/tv/search`; `/tv/{source}/{id}`;
  `/tv/manual/...`; `/tv/export/json`.
- Search source selector: TVmaze always; TMDB only when configured. One kind,
  so no kind selector.
- Match dialog sources: TVmaze and TMDB (when configured); TVmaze URL or id,
  TMDB URL or id.
- Plugin settings: title language (en/original), home view/status, columns,
  Import section with the Yamtrack TV CSV. No schedule/account importer.
- Calendar: TV episodes from TVmaze; no events from TMDB. The month page and
  ICS show the season label.
- DefaultEnabled false; enable in Settings > Plugins.

## Text, caps and errors

- Reuse the usual caps: titles 300, text 20000, terms 60, names 300, ids 100,
  genres 50, tags 100, credits 500. TVmaze summaries are HTML; use
  `anilist.PlainText` (or a shared equivalent) before `plugin.MultiLine`.
- Cast caps; drop empty names/ids; character/actor references sanitised.
- Source errors follow the standard sentinels; the web 502/503 and import
  retry rules are unchanged. TMDB key never appears in errors or logs.

## Tests

- Manifest: one show kind, fields, progress, sections, columns, sources,
  importers, exporter, default disabled, icon.
- TVmaze: search, details, cast, episodes/seasons totals, lookup by tvmaze/
  tvdb/imdb, schedule (season/number/airstamp), errors.
- TMDB: conditional source presence with/without key; search/details/cast/
  recommendations/keywords; lookup tmdb/imdb/tvdb; key redaction; errors.
- Yamtrack: generic parser extraction keeps anime tests green; TV aggregation
  (tv/season/episode, multiple seasons, current season, completed/in-progress
  episodes, no-id manual, ignored media, skipped rows, no TV rows).
- Calendar: `Airing.Season` round trip, migration/save/query, UID stability
  for season 0, season-aware label, two seasons with the same episode number.
- Web: plugin enable/list/search/add/match/import; TVmaze and TMDB source
  selection; calendar page/ICS.
- Export round trip; docs tests; full suite, vet, fmt.

## Builder-check calls

The builder check on the first draft raised four questions; settled as follows:

1. **Watched episodes.** Episode-row presence is the watched marker. The
   owner's actual export has blank statuses on all episode rows, so status is
   not the marker; blank status is accepted. Season/TV `progress` supplies the
   per-season/total counts, with distinct episode rows as the fallback.
2. **TV rows without an id.** A titled TV row with no TMDB/TVDB/manual id is a
   manual show, not skipped. Only a TV row without a title is skipped.
3. **Character/actor ids.** TVmaze characters use `character-<id>` and actors
   `person-<id>`; TMDB characters use `character-<credit_id>` (or a stable
   name hash when absent) and actors `person-<id>`. This keeps the separate
   namespaces from colliding in the people table.
4. **Shared parser boundary.** The shared Yamtrack parser owns CSV reading and
   score/date/progress parsing, but returns raw media type/source/id, season/
   episode numbers and raw status plus a mapped status when known. It does not
   filter a media type and does not reject blank statuses. The anime and TV
   importers decide how to filter, validate and aggregate; the existing anime
   tests stay green.

## Rollout

1. Core `Airing.Season` + migration 00016 + calendar tests.
2. Shared Yamtrack raw parser refactor; anime tests stay green.
3. TV plugin manifest/mapping/TVmaze/TMDB/Yamtrack/TMDB conditional.
4. Register in `cmd/allister` with `TMDB_API_KEY` config; docs.
5. Preview: enable TV, search/add a TVmaze show, verify cast/calendar, import
   a small Yamtrack TV CSV (tv + seasons + episodes), check progress aggregation
   and a re-import; confirm no Trakt/TV Time/MDL UI.
6. Full suite, vet, fmt; report; do not push (owner pushes).

## Follow-ups

- Trakt and TV Time imports: issue #3.
- MyDramaList metadata source: issue #2.
- Movies, games: later plugins.
- Automatic per-source schedule for TMDB items (e.g., a cross-source schedule
  bridge) if it becomes desirable.

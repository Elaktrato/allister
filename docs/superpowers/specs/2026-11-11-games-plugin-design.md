# Games plugin and multi-field source credentials (design, 2026-11-11)

## Intent

Add the games plugin, the last media type: one **Game** kind, a **Steam** account
import for the owned library and playtime, **IGDB** for rich metadata and the
existing 1,019 Yamtrack IGDB rows, and a Yamtrack game CSV import that keeps the
owner's statuses, scores and playtime.

The source-key UI needs one core extension: IGDB authenticates with a Twitch
Client ID + Secret, while the current API keys card stores one value per
source. The card grows to named fields. TMDB and Steam remain single-key
sources.

GOG and HowLongToBeat are out: GOG has no official library API, and HLTB has no
official API or export. IGDB's `game_time_to_beats` endpoint supplies the
completion-time estimates instead (hastily / normally / completely), so the
Game kind still shows "how long to beat" data.

## Goals

- One **Game** kind: Want to play / Playing / Paused / Played / Dropped;
  **Playtime** duration (`h:mm:ss`, no +1); platforms, developer, publisher,
  year, release date, genres, themes/tags, summary, cover, the item's own
  source link (IGDB or Steam), and three IGDB completion-time estimates.
- **Steam** source: official Web API `GetOwnedGames` for the account library
  and playtime (Steam Web API key + SteamID64), plus the Steam store
  `storesearch`/`appdetails` endpoints for search and metadata.
- **IGDB** source: official API v4 with Twitch Client ID + Secret; search,
  details, similar games, the Steam external-id lookup, and time-to-beat.
- **Importers**: Yamtrack game CSV (statuses, scores, playtime; resolves via
  IGDB) and Steam account (library + playtime; resolves via IGDB, falling back
  to the Steam store).
- The source-key card supports named fields (IGDB Client ID/Secret) and keeps
  the existing single-key sources working.
- JSON export, backup, docs, tests, preview.

## Non-goals

- GOG (no official owned-library API; the Galaxy client DB is a separate
  project) and HowLongToBeat (no official API/export). No GitHub issues for
  them; explicitly forgotten.
- Scraping HLTB, GOG, or Steam HTML pages.
- A per-platform library sync beyond Steam. GOG/Epic/etc. are out.
- IGDB/GOG/Steam webhooks or write-back.
- A calendar (games have no release schedule; the kind has no schedule source).
- A separate Time-to-beat source or progress type; the three IGDB estimates are
  number fields in minutes.
- Importing Steam achievements or screenshots.

## Approaches considered

- **A. Source keys only (chosen).** IGDB is a two-field source key
  (`client_id`/`client_secret`); Steam is a single source key. The Steam key is
  used by the account import and gates the Steam source; SteamID64 stays the
  importer's account field. One credential surface; the card is the only place
  keys live. Cost: Steam store search/details are hidden until the Steam key is
  set, although the store endpoints themselves need no key.
- **B. Separate importer credentials.** Steam source keyless; the Steam key on
  the Steam account import form. More accurate for the store endpoints, but a
  second credential surface and a larger importer UI/core change. Rejected for
  this version; the owner has the Steam key.
- **C. Steam-only first.** Use the Steam store for everything and add IGDB
  later. The 1,019 Yamtrack IGDB ids could not resolve, and the statuses/scores
  would be lost or manual. Rejected.

## Constraints

- No new Go dependencies; HTTP via the standard library.
- No database migration: source key fields use the existing `settings` table.
  The next migration stays 00017 (docs only).
- Plugin contract data only; no plugin HTML/JS; core guard; theme-token colours.
- IGDB: Twitch OAuth client credentials; 4 requests/second; declare 60
  requests/minute for the importer's row pace (2-3 requests per row). The
  client caches the token and refreshes it; refresh on a 401 once.
- Steam: the official Web API key is needed for `GetOwnedGames`; the Steam
  store `storesearch`/`appdetails` endpoints are unofficial but widely used.
  Declare 60 requests/minute; they may change, so unexpected shapes are
  `ErrBadResponse`.
- Keys and tokens are never logged, rendered, exported or backed up, and never
  appear in an error (including request URLs). The IGDB token URL contains the
  client secret; do not wrap transport errors.
- The Steam key limit is 100,000 Web API calls/day; one account import is one
  call, then store metadata calls. Fine for a personal library.
- README stays at or below 150 lines.
- Preview only on port 18083 / project `allister-preview`; the owner's
  18080-18082 containers stay untouched. The owner pushes.

## Source credentials

### Contract

`plugin.SourceInfo` gains:

```go
// KeyFields lists the named credential fields of a multi-field source. Empty
// means a single key named by KeyLabel.
KeyFields []KeyField `json:"key_fields,omitempty"`

type KeyField struct {
    Name   string `json:"name"`   // stable settings field ("client_id")
    Label  string `json:"label"`  // "Client ID"
    Secret bool   `json:"secret,omitempty"` // password input
}
```

- A source uses either `KeyLabel` alone (single key, e.g. TMDB/Steam) or
  `KeyLabel` as a group label plus `KeyFields` (IGDB). `KeyLabel` remains at
  most 40 runes, `KeyHelp` 200, both single-line.
- `KeyFields` has at most 4 entries; each `Name` is a lower-case slug (letters,
  digits, `_`, 1-32), unique within the source; each `Label` is a single line of
  at most 40 runes. At least one field is required when `KeyFields` is set.
- The existing optional-field JSON rule applies; the contract version stays 1.

### Storage

`internal/settings`:

```go
SourceKeyField(ctx, sourceID, field string) (string, error)
SetSourceKeyField(ctx, sourceID, field, value string) error
ClearSourceKeys(ctx, sourceID string) error
```

- The setting key is `source.<source-id>.<field>`. Single-key sources use the
  field `key`, so `source.tmdb.key` and the existing `SourceKey`/`SetSourceKey`
  behavior stay byte-compatible; `SourceKey` delegates to field `key`.
- `ClearSourceKeys` deletes every `source.<id>.*` row (single and multi).
  `ClearSourceKey` (single) stays for compatibility and deletes `key` only.
- Values are trimmed. A missing field is `""`; read errors are returned (the
  resolver needs to distinguish a store failure from an unset key).

### Resolution

`cmd/allister` builds one credential resolver:

```go
fallback := map[string]map[string]string{
    "tmdb":  {"key": cfg.TMDBKey},
    "igdb":  {"client_id": cfg.IGDBClientID, "client_secret": cfg.IGDBClientSecret},
    "steam": {"key": cfg.SteamAPIKey},
}
credential := func(ctx context.Context, sourceID, field string) (string, error) {
    v, err := settingsStore.SourceKeyField(ctx, sourceID, field)
    if err != nil || v != "" {
        return v, err
    }
    return fallback[sourceID][field], nil
}
```

- Stored fields beat environment values; an empty store falls back to the env.
- TMDB/TV/Movies use `credential(ctx,"tmdb","key")`; IGDB uses the two fields;
  Steam uses `credential(ctx,"steam","key")`.
- `web.Deps` gains `SourceKeyField func(ctx, sourceID, field string)
  (string, error)`; `SourceKey` remains for single-key sources (TMDB/Steam).
  When both are nil, the web reads the settings table only (tests).
- Config/env: `TMDB_API_KEY` (existing), `IGDB_CLIENT_ID`,
  `IGDB_CLIENT_SECRET`, `STEAM_API_KEY`; `docker-compose.yml` passes all four
  through (empty allowed). `config.Config` gains the three new fields.
- Saving a field is live: the plugin's resolver reads it on the next request
  (IGDB token refresh or Steam call), no restart.

### API keys card

The card on `GET /settings/plugins` renders:

- A single-key source: the current one password input (`name="key"`), Save,
  and Remove when a stored key exists.
- A multi-field source: the group `KeyLabel`/`KeyHelp`, then one input per
  `KeyField` (`name="field.<name>"`; `type="password"` when `Secret`, text
  otherwise), all with no value, `autocomplete="new-password"`, Save, and
  Remove when any field is stored. "A key is configured." only when every
  effective field is non-empty. Saving requires every field; a missing one
  re-renders with 400 and the message. Remove clears every field.
- The existing no-echo rules apply to every field. The card dedupes by source
  id as before (TV/Movies share TMDB; IGDB/Steam are one block each).
- `POST /settings/plugins/{source}/key` handles both shapes; unknown/unkeyed
  source ids 404; success redirects to `/settings/plugins#source-keys`.
- The plugin settings notice and source pickers use the same "configured"
  helper: single key non-empty, or every multi-field non-empty. A source with
  no key fields is always configured. Unconfigured keyed sources keep their
  `" (needs key)"` label.
- The account import form (`postPluginImport`) maps `ErrNotConfigured` to the
  same actionable message: "<Source> needs a valid <key label>. Add or replace
  it in Settings > Plugins." The Steam importer lets the sentinel through; the
  form never shows the raw "plugin: source is not configured".
- The search/match default source becomes server-aware: the first source
  serving the kind that is configured, else the first as today. This matters
  for games (IGDB first, but Steam when only the Steam key is set).

### Missing or refused credentials

`plugin.ErrNotConfigured` is unchanged. IGDB returns it for an empty
ID/secret, a token request 401/403, and a data request 401 after one refresh.
Steam returns it for an empty key and a Web API 401/403. The web message uses
the source name and the group label: "IGDB needs valid Twitch app credentials.
Add or replace them in Settings > Plugins." The existing 503/200 status matrix
and the sync/import fallbacks apply unchanged.

## Kind: Game

Plugin `game`, Name "Games", page `/game`, icon `sports_esports`,
`DefaultEnabled: false`. Kind key `game`, labels Game/Games.

Fields:

| Key | Type | Notes |
|---|---|---|
| `year` | number | 1950-2100; shown, editable, filterable, sortable |
| `released_on` | date | shown, editable |
| `platforms` | text | comma-separated names; shown, editable, filterable |
| `developer` | text | shown, editable, filterable |
| `publisher` | text | shown, editable, filterable |
| `time_main` | number | IGDB "hastily", minutes; unit `min`; shown, editable, sortable |
| `time_extra` | number | IGDB "normally", minutes; unit `min`; shown, editable, sortable |
| `time_complete` | number | IGDB "completely", minutes; unit `min`; shown, editable, sortable |
| `summary` | text | editable; the Summary section |

Genres and tags enabled, editable and filterable.

Statuses: Want to play, Playing, Paused, Played, Dropped.

Progress:

- `playtime` (duration, label Playtime). Defaults `[playtime]`, combinations
  `[[playtime]]`, no primary (no +1), no TotalField. The value is the user's
  total playtime in seconds; the Steam/Yamtrack imports set it; the entry
  dialog edits it as `h:mm:ss`. It is not the completion estimates.

Sections: Info (Details), Text (Summary, `summary`), Titles, Genres, Tags,
Recommendations (IGDB similar games), Links. No People.

Titles: `en`; fallback `en`.

Columns: Year (from sm), Progress, Score, Status, Updated (from lg) by default;
Platforms, Developer, Publisher, Released, Main story, Main + extras and
Completionist optional. The Progress column shows Playtime.

## Source: IGDB

`SourceInfo`:

- ID `igdb`, name "IGDB", 60 requests/minute, `Kinds: [game]`,
  `ExternalIDTypes: [igdb, steam]`, `SupportsLookup: true`, no schedule.
- `KeyLabel: "Twitch app credentials"`,
  `KeyHelp: "Create a free Twitch application at dev.twitch.tv/console/apps."`,
  `KeyFields: [{client_id,"Client ID",false},{client_secret,"Client Secret",true}]`.
- Link "View on IGDB", URL pattern `igdb.com/games/{id}` (the `{id}` may be the
  slug; `MatchRef` extracts it and `Lookup` handles both numeric ids and slugs).

Authentication:

- `POST https://id.twitch.tv/oauth2/token?client_id=...&client_secret=...&grant_type=client_credentials`;
  `{access_token, expires_in}`. Cache the token with the client ID and expiry;
  refresh 60 seconds early; if a data request returns 401, refresh once and
  retry it. The token URL contains the secret; never wrap transport errors or
  include the URL in an error.
- Requests: `POST https://api.igdb.com/v4/{endpoint}` with `Client-ID` and
  `Authorization: Bearer <token>`, `Accept: application/json`, and an
  Apicalypse body. Body cap 8 MiB.
- Status mapping: 400/422 -> `ErrBadResponse`; 401 -> refresh/retry once then
  `ErrNotConfigured`; 404 -> `ErrNotFound`; 429 -> `ErrRateLimited`; 5xx/timeout
  -> `ErrUnavailable`; other -> `ErrBadResponse`.
- Empty credentials -> `ErrNotConfigured`; resolver errors -> `ErrUnavailable`.

Queries:

- Search: `/v4/games`, body `search "<q>"; fields name,slug,first_release_date,cover.url;
  where version_parent = null; limit 20;`. Map id/name/year/cover.
- Details: `/v4/games`, body `fields name,slug,summary,first_release_date,
  platforms.name,genres.name,themes.name,keywords.name,
  involved_companies.company.name,involved_companies.developer,
  involved_companies.publisher,cover.url,similar_games.id,similar_games.name,
  similar_games.first_release_date,similar_games.cover.url,external_games.uid,
  external_games.external_game_source;
  where id = <id>; limit 1;`. A slug lookup uses `where slug = "<slug>"`.
- Time to beat: `/v4/game_time_to_beats`, body `fields hastily,normally,completely,count;
  where game_id = <id>; limit 1;`. Map seconds to whole minutes:
  `time_main` = hastily, `time_extra` = normally, `time_complete` = completely;
  omit empty/zero values. No data is not an error. This call is **best-effort**:
  any time-to-beat error (429/5xx/400) is ignored, the game details are still
  returned, and the three fields are omitted. A flaky time-to-beat endpoint
  must never fail the overview, the sync, or an import row.
- Steam app id lookup: `/v4/external_games`, body `fields game;
  where uid = "<appid>" & external_game_source = 1; limit 1;` (Steam is 1).
  Then the details for `game`. No result -> `ErrNotFound`.
- Recommendations: the details' `similar_games`; cap 20; summaries use
  `Kind: game`, source IGDB, id/name/cover/year.
- People: nil (the kind has no People section).

Mapping:

- `first_release_date` is Unix seconds -> `released_on` (UTC date) and `year`.
- `platforms`: join names with `", "`; `genres` terms; `themes` + `keywords`
  tags; `involved_companies` with `developer`/`publisher` true -> first
  developer/publisher name (join if several, max 3).
- `cover.url` starts `//images.igdb.com/.../t_thumb/...`; prefix `https:` and
  replace `t_thumb` with `t_cover_big`.
- External ids: `igdb` from the id; `steam` from the first external game with
  source 1. A Steam lookup sets the `steam` id too. The source link is the IGDB
  slug URL.
- All text through `plugin.SingleLine/MultiLine/HTTPSURL`; the game caps:
  title 300, text 20k, term 60, id 100, genres 50, tags 100, recommendations 20.

## Source: Steam

`SourceInfo`:

- ID `steam`, name "Steam", 60 requests/minute, `Kinds: [game]`,
  `ExternalIDTypes: [steam]`, `SupportsLookup: true`, no schedule.
- `KeyLabel: "Steam Web API key"`,
  `KeyHelp: "Create one at steamcommunity.com/dev/apikey; used for the Steam
  account import."`. Link "View on Steam",
  URL pattern `store.steampowered.com/app/{id}`.

The key is required for the source's calls (the chosen source-key model): the
account import uses it; the store search/details check it and return
`ErrNotConfigured` when empty, although the store endpoints themselves are
keyless. The key is never sent to the store endpoints.

### Account library (official Web API)

- `GET https://api.steampowered.com/IPlayerService/GetOwnedGames/v1/?key=...
  &steamid=<SteamID64>&include_appinfo=1&include_played_free_games=1&format=json`.
- `response.games[]`: `appid`, `name`, `playtime_forever` (minutes),
  `playtime_2weeks`, `img_icon_url`. `game_count`.
- An empty/private response (no `games`) is an actionable bad input: "Steam
  returned no games; make your profile and game details public."
- 401/403 -> `ErrNotConfigured` (bad/refused key); 429 -> `ErrRateLimited`;
  5xx/timeout -> `ErrUnavailable`. No key in errors.

### Store metadata (unofficial but stable)

- Search: `GET https://store.steampowered.com/api/storesearch/?term=<q>
  &l=english&cc=us`; items `type` ("app"), `id`, `name`, `tiny_image`,
  `platforms`. Map id/name/cover.
- Details: `GET https://store.steampowered.com/api/appdetails?appids=<id>
  &l=english`; the response is `{"<id>":{"success":bool,"data":{...}}}`.
  `success:false` -> `ErrNotFound`. `type` must be `game`; DLC/software/demo
  -> `ErrNotFound`.
  Map: `name`, `short_description`/`about_the_game` (plain text; strip HTML
  with the shared plain-text helper), `header_image`, `developers[0]`/
  `publishers[0]` (join up to 3), `genres[].description`, `categories[].description`
  as tags, `release_date.date`, `platforms` (windows/mac/linux),
  `steam_appid`, `website`.
- Release date layouts: `2 Jan, 2006`, `Jan 2, 2006`, `2006`; `coming_soon`
  true -> no date.
- No People, no recommendations.
- Link and `ExternalURL`: `https://store.steampowered.com/app/<appid>`.

## Importers

### Yamtrack game CSV (`yamtrack-game`)

- Accept `.csv`, `ExternalIDType: "igdb"`, `ExternalIDLabel: "IGDB"`,
  `ResolveVia: "igdb"`, `ManualRows: true`, `FallbackTitleLang: "en"`,
  `UnmatchedNote: "Imported from Yamtrack; IGDB had no match for #{id}."`.
- Parse with `yamtrack.ParseRaw`; keep `media_type == "game"` case-insensitively;
  every other row is ignored.
- Group by `(source, media_id)`, else `(source, title)`; duplicates are skipped.
- `source == "igdb"` and a numeric id keep `ExternalIDs["igdb"]`; every other
  source stays manual with a note. A numeric id with no title becomes
  `IGDB #<id>`; no title and no id is skipped.
- Status mapping: Completed -> Completed (Played), In progress -> Active,
  Planning -> Planned, Paused -> Paused, Dropped -> Dropped; unknown/blank is
  skipped.
- Playtime: the shared parser gains `RawRow.ProgressRaw`; the game importer
  parses `Xh Ymin`/`Xmin` (and plain minutes) to seconds and sets
  `Progress["playtime"]`. `0min` sets 0. The shared parser stops adding an
  "invalid progress" note for a duration-shaped value; an integer still fills
  `Progress` for anime/TV; a truly invalid value keeps the note.
- Rating, start/end dates, comment and cover map as in the TV/movie importers.
  The exact note `"Imported from Steam"` is dropped (it describes Yamtrack's
  import, not the game); other notes are kept.
- The owner's 1,019 rows: 556 Planned, 418 Paused, 39 Completed, 3 Playing, 3
  Dropped, all IGDB ids, playtime in minutes/hours.

### Steam account (`steam-account`)

- `AccountLabel: "SteamID64"`, `ExternalIDType: "steam"`,
  `ExternalIDLabel: "Steam AppID"`, `ResolveVia: "igdb"`,
  `ResolveViaFallback: ["steam"]`, `ManualRows: true`,
  `FallbackTitleLang: "en"`,
  `UnmatchedNote: "Imported from Steam; IGDB and the Steam store had no match
  for #{id}."`.
- `ImportAccount(ctx, steamID)`:
  - Validate 17 ASCII digits; empty/bad -> `ErrBadInput` ("Enter the 17-digit
    SteamID64").
  - Fetch `GetOwnedGames`; a keyless source returns `ErrNotConfigured`; a
    private/empty library is an actionable error.
  - One entry per game: `Kind: game`, title, `ExternalIDs{"steam": appid}`,
    `Progress{"playtime": playtime_forever*60}`; status `Planned` when playtime
    is 0, else `Active`; cover the Steam header image. Steam has no statuses or
    scores; the user adjusts them. No `UpdatedAt` (the "newer" mode never
    updates; use "always" for a repeat schedule).
  - The entries resolve through IGDB first (details + time-to-beat + the
    `steam` external id), then the Steam store fallback. The IGDB source's
    `steam` lookup is the bridge; the IGDB details map the Steam external id
    so a later Yamtrack IGDB import/Steam import converges on one item.
  - The owned list can include tools/software (SteamVR, redistributables). The
    importer does not pre-filter them: the IGDB/Steam lookups return
    `ErrNotFound` for a non-game type, so those rows stay manual with their
    Steam appid and the unmatched note. The user deletes them; the alternative
    (an unpaced per-app `appdetails` type check at submit time) is not worth it.
- The `Parse` method refuses a file (an account importer takes the ID).

## Mapping and caps

- `gameRecord` neutral shape: source/id, titles, fields, cover/external URL,
  external ids, genres, tags, recommendations.
- `gameData` maps `KindGame`, sanitises titles (300), text (20k, summary
  multi-line), fields (text/number/date), external ids, cover, genres (50,
  deduped), tags (100).
- `gameRecommendations`: cap 20, drop summaries without source/id/title.
- The movie/TV sanitising helpers may be duplicated in the game package (the
  same accepted ruling as movies); no shared package.

## Web routes and behavior

| Route | Purpose |
|---|---|
| `/game` | Game list (generic list/search/filter/view parameters) |
| `/game/search?q=...&source=igdb` | IGDB search (Steam also available) |
| `/game/{source}/{id}` | Game overview; `/edit`, `/match`, `/add` as usual |
| `/game/manual/new`, `/game/manual/{id}` | Manual games |
| `/game/export/json` | JSON export |
| `POST /settings/plugins/{source}/key` | Save/remove one or all credential fields |

No calendar routes or schedule capability. The existing `ErrNotConfigured`
web matrix and sync/import fallbacks apply. The default source for a kind is
the first configured source (IGDB when configured, else Steam; the first
source when none is configured).

Small core change in the match flow: `postMatch` currently overwrites the
source data's id with the string extracted from the pasted URL. That would
store an IGDB slug as the item's source id while a search-add stores the
numeric id, creating duplicates. `postMatch` keeps the source's canonical
`d.ID` when the source returns one, falling back to the matched input; the
redirect uses the canonical id. The IGDB `Details`/`Lookup` for a slug returns
the numeric `ID` (the slug stays the external URL). The match tests cover a
source that returns a different canonical id from the input.

## Plugin wiring

`internal/plugins/game`:

- `manifest.go`: the Game kind, IGDB/Steam sources, Yamtrack + Steam importers,
  JSON exporter.
- `mapping.go`: the neutral record and data/recommendation mapping.
- `igdb.go`: OAuth/token cache, Apicalypse requests, search/details/time-to-beat,
  Steam external-id lookup, mapping.
- `steam.go`: Web API owned games, store search/details, mapping.
- `yamtrack.go`: the Yamtrack game importer.
- `plugin.go`: `Options{HTTP; IGDBURL, IGDBTokenURL, SteamAPIURL, SteamStoreURL
  string; IGDBClientID, IGDBClientSecret, SteamKey func(context.Context) (string,error)}`;
  `New` builds both sources, both importers, the exporter. Nil key funcs
  become `""`.
- `cmd/allister`: build the credential resolver, pass it to the game plugin,
  TV/Movies (TMDB), and `web.Deps`; register Games after Movies.
- `internal/config`: `IGDBClientID`, `IGDBClientSecret`, `SteamAPIKey`; env
  `IGDB_CLIENT_ID`, `IGDB_CLIENT_SECRET`, `STEAM_API_KEY`; compose passes them.
- `docker-compose.yml`: add the three env vars (empty allowed).
- No migration.

## Docs

- README: Games in the plugin paragraph, the three env vars / API keys card,
  remove games from the planned list, 150-line cap (fold and trim).
- `docs/usage.md`: `/game` routes; IGDB/Steam sources; the SteamID64 account
  import; the Steam key in the API keys card; playtime vs time-to-beat; the
  Yamtrack game import; no calendar; GOG/HLTB out; key/backup caveats.
- `docs/plugins.md`: `SourceInfo.KeyFields`/`KeyField`; multi-field settings
  `source.<id>.<field>`; the single-key compatibility; `ErrNotConfigured`;
  the host-provided key resolvers; the store endpoints are unofficial.
- `docs/handoff.md`: games features, IGDB/Steam, the multi-field card, the
  IGDB time-to-beat data, no GOG/HLTB; remove games from the not-built list.
- `AGENTS.md`: the settings line already mentions source keys; note multi-field
  source credentials; keep the next migration 00017.

## Testing and verification

- Contract: `KeyFields` JSON round trip; validation (fields unique, names,
  labels, max 4, `KeyFields` without `KeyLabel` invalid, help/label lengths);
  the existing single-key sources still validate.
- Settings: field round trip, trim, single `SourceKey` compatibility,
  `ClearSourceKeys` removes every field of one source and no other source.
- Web: multi-field card renders all inputs and no values; Save all succeeds;
  a missing field is 400 and stores nothing; Remove clears all; env-only shows
  configured and no remove; the single TMDB/Steam card still works; source
  choice labels/default source; the plugin settings notice; no key echoed.
- Config/cmd: stored field beats env; empty falls back; store error returned;
  the resolver table test (as in the movies work).
- Shared parser: `ProgressRaw`; duration parsing; integer progress unchanged;
  invalid values still noted.
- Game: manifest validation; kind fields/statuses/progress/columns/sections;
  mapping/caps; recommendations.
- IGDB: token request/caching/refresh, 401 retry, 429/5xx/400 mapping; search,
  details, time-to-beat, steam external-id lookup, slug lookup; no key leak;
  time values in minutes; external ids igdb+steam.
- Steam: owned games, store search/details, release date parsing, non-game
  types -> not found, private/empty library, key refusal, no key leak; account
  importer entries (playtime, status, appids, cover).
- Yamtrack game import: the owner's 1,019-row shape (synthetic fixture; a
  local-only check of the real export), statuses, playtime, notes, duplicates,
  non-game rows, no-IGDB key manual fallback.
- Docs tests; full suite, vet, fmt.
- Preview on 18083: enable Games; the multi-field IGDB card and single Steam
  card; no-key messages; manual game add with playtime; if the owner enters
  the Steam key + SteamID64, a small Steam account import; JSON export; no
  calendar; TMDB/TV regression. Real IGDB live calls need the owner's Twitch
  credentials (or fixtures only).

## Decisions left to the builder

- Exact helper names, field label wording, and whether the time-to-beat fields
  show in the default Info block.
- The Steam owned-game status default (`Planned` for 0 minutes, `Active`
  otherwise) and the exact private-library error wording.
- Whether the IGDB details query fetches time-to-beat in the same request
  (multi-query) or a second request (the default: a second request).
- The exact Steam release-date layouts and HTML-stripping helper reuse.
- The test fixture content and the preview script.

## Calls made after the builder check

1. **IGDB slug identity** — a small core `postMatch` change keeps the source's
   canonical `d.ID`; the IGDB source returns the numeric id for a slug URL, so
   a matched slug and a search-add converge on one item. The IGDB URL pattern
   stays slug-based, and real IGDB URLs remain pasteable.
2. **Time-to-beat failures** — best-effort: the game details succeed and the
   three time fields are omitted. No overview/sync/import failure comes from
   the time-to-beat endpoint.
3. **Steam non-game owned apps** — no pre-filter; they stay manual rows with
   their Steam appid and the unmatched note, and the user deletes them. The
   alternative (an unpaced per-app type check at submit time) is rejected.
4. **Steam key on the account form** — the web import form maps
   `ErrNotConfigured` to the same actionable key message; the importer lets the
   sentinel through.
5. **Links** — one link per item, the item's own source link; an IGDB item
   shows IGDB, a Steam item shows Steam. The stored `steam` external id is not
   a second link; no URL field or core Links change.

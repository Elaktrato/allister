# Manga plugin (design, 2026-11-10)

## Intent

A second media type as a plugin, using the anime plugin as the baseline:
AniList as the source, manual entries, editing, matching, covers, genres and
tags, recommendations, an AniList-username import and a MyAnimeList XML
export, per-plugin export, and the shared list/卡片/calendar infrastructure.
The plugin is opt-in (DefaultEnabled false), enabled in Settings > Plugins.

**Calendar:** AniList has no chapter release schedule (its schedule data is
anime-only), so the manga plugin does not declare the schedule capability and
contributes no calendar events. A future source with chapter timestamps (for
example MangaDex) could add the capability per source.

## What is shared, what is new

- **Shared AniList client**: the anime plugin's `anilist` package moves to
  `internal/plugins/anilist` (plugins may share code with each other; the
  core-imports-plugins guard is untouched) and gains a media-type parameter:
  queries take `type: $type` (ANIME or MANGA), `MediaByMALID` and
  `MediaList` take the type too, and the media struct gains `chapters`,
  `volumes` and `staff`. The exported `Anime` type is renamed `Media`; the
  anime plugin's mapping is updated mechanically.
- **Shared JSON exporter**: the anime plugin's generic exporter moves to
  `internal/plugins/jsonexport` and both plugins use it.
- **New plugin** `internal/plugins/manga`: manifest, source (Search, Details,
  Recommendations, People = staff, Lookup), mapping and both importers.

## Manifest

- Plugin `manga`, name "Manga", icon `menu_book`, DefaultEnabled **false**.
- One kind `manga` ("Manga"/"Manga"): fields `type` (MANGA/ONE_SHOT/NOVEL,
  required), `chapters` (number, the progress total), `volumes` (number),
  `year`, `author` (text), `started_on`/`ended_on`, `synopsis`, `links`.
- Genres and tags enabled; statuses Reading/Completed/On hold/Dropped/Plan to
  read; sections Info, Synopsis, Titles, Genres, Tags, Related, Staff, Links,
  Recommendations.
- Titles: `ja`, `ja-latn`, `en`; fallback in that order; the usual default
  columns.
- Progress: two count tracks, `chapter` (primary, TotalField `chapters`) and
  `volume` (TotalField `volumes`); defaults `[chapter]`; allowed
  combinations `[[chapter], [volume]]` (one active track at a time).
- Source `anilist`: type MANGA, external ids `mal` and `anilist`, lookup and
  the account importer; **no** schedule capability.
- Importers: `anilist-user` (account, MediaList type MANGA) and `mal-xml`
  (the `<manga>` entries of the export, `my_read_chapters`/`my_read_volumes`
  → the progress map, `my_score` → rating, status codes 1/2/3/4/6).
- Exporter: `json` (shared).

## Mapping notes

- Search/Details/Recommendations use the AniList MANGA fields; `chapters`
  and `volumes` fill the two totals; `format` fills the type; `staff(edges)`
  fills the author field (first Story/Story & Art name) and the People credits
  (each staff edge → a credit with its role; the person page reuses the
  generic layout).
- Lookup: MAL ids via Media(idMal:, MANGA), AniList ids via Details; same
  error contract (not found / rate limited / unavailable / bad answer).
- The account import keys rows by the AniList id; the XML import by the MAL
  id; both reuse the import modes and the scheduler as they are.

## UI

No new core UI: the plugin appears in the sidebar once enabled, the list
works in cards and table, the plugin settings page offers titles, home, import
and columns, and the calendar simply has no events from it.

## Tests

Manifest baseline and freshness; client type parameterisation (ANIME vs
MANGA queries, MAL lookup, MediaList type); source mapping (chapters/volumes,
format, staff as People, relations); both importers (status/score/progress
mapping, manga XML fixture); exporter round trip; web flow through the
fixtures is already generic, but one end-to-end enable/add/list check for the
real plugin; guard (plugin words stay in the plugin).

## Non-goals

Chapter release calendar events (no AniList data), extra sources (MangaDex),
volume/chapter combinations mixed in one entry (one active track like anime).

# Source extras: release status, AniList data, banners, trailers, bulk update (design, 2026-11-27, approved)

## Intent

The owner asked whether Allister imports a title's own status (publishing,
finished, hiatus, ...) and wants it imported "wherever possible" and usable
as a filter. Asked what else AniList offers, the owner wants the extras too:
several as filters, and the banner and trailer shown on the item page. Items
already in the library must get the new data as well, through a bulk
"Update from source" button in each plugin's settings.

## Goals

- **Release status** stored and filterable wherever a source has one:
  - AniList for anime, manga and light novels (book plugin, series kind):
    `FINISHED`, `RELEASING`, `NOT_YET_RELEASED`, `HIATUS`, `CANCELLED`.
  - IGDB for games (its game status, plus "upcoming" for an unreleased game
    with a future first release date).
  - TV (`air_status`) and movies (`release_status`) already have it; Open
    Library and Google Books have none, so books stay without one.
- **AniList extras** (anime, manga, light novels unless stated):

  | Extra | Where |
  |---|---|
  | Season (Winter/Spring/Summer/Fall), anime only | filter (with the existing year filter) |
  | Country of origin (JP, KR, CN, TW, ...) | filter |
  | Adult flag | filter, plus a per-plugin setting to hide adult items |
  | All animation studios, anime only (no producers or licensors: owner decision) | the studio field becomes a list, filterable; existing values kept |
  | Community score (AniList average, 0-100) | shown on the item page, sortable; never mixed with the owner's score |
  | Popularity | sortable (and shown on the item page) |
  | Alternative titles (synonyms) | stored as titles, so search finds them; never the displayed title |
  | Streaming links | the item page's Links section |
  | Banner | a wide image at the top of the item page, downloaded into the local image store like covers |
  | Trailer | the item page: a locally stored thumbnail with a play button linking out to the video site |

- **Bulk "Update from source"** on each plugin's settings page: for every
  sourced item of that plugin in the library, ask the source and **fill
  only what is empty** on the stored item (owner decision, option 1);
  for lists, **add what is missing, never remove or change an element**
  (owner decision after the builder check). Nothing already set changes,
  owner edits included. It runs in the
  background at the source's pace, survives a restart, shows its progress
  and result on the settings page, and can be run again at any time.
- The single-item "Update from source" comparison page shows the new data
  (banner, trailer, links and the new fields) as rows like any other.

## Non-goals

- Characters (a new kind of people data with images: a project of its own).
- An embedded video player (owner chose the thumbnail and link-out, A).
- Release status or extras for sources that do not have them (Open Library,
  Google Books, Goodreads, Steam, files).
- A bulk update that overwrites stored values (owner chose fill-only; a
  changed title or synopsis still comes through the single-item comparison
  page).
- Automatic or scheduled refreshes; a run starting on upgrade without the
  owner pressing the button (approach B was rejected).

## Anti-goals

- Any page loading something from a third-party host (images, players,
  scripts): banners and trailer thumbnails are downloaded and served
  locally, the trailer is a plain outgoing link.
- A bulk update that changes a value that was already set.
- Media words in core code, plugin HTML/JS, inline scripts or styles; the
  htmx, colour-token and CSP rules in AGENTS.md stay.
- The community score sorting, filtering or showing as if it were the
  owner's own score (stats, cards, the rating filter keep using the owner's
  score only).
- Silently losing the stored studio value when the field becomes a list.

## Constraints

- One new migration, **00023**, for whatever storage the banner, the trailer
  and the item links need (Claude's call: item columns for the banner and
  trailer, a table for links; the builder may shape it). Never edit an
  applied migration.
- The contract stays data-only and JSON-serialisable (round trip tested):
  a kind declares that it shows a banner and/or a trailer; `plugin.ItemData`
  carries a banner URL, a trailer (video URL plus thumbnail URL) and a list
  of links (label, URL). Sources sanitise them with `plugin.SingleLine` and
  `plugin.HTTPSURL`; the trailer link is built by the plugin from AniList's
  `trailer { id site }` (YouTube and Dailymotion only; others dropped).
- New fields are plain manifest fields (enum, text, number, list), so
  filters, sorts, the edit form and the comparison page need no new core
  code for them. AniList's values are mapped to lowercase enum values in the
  plugin, as TV's and movies' statuses are.
- Backup carries the new data (banner and trailer thumbnail archived like
  covers, links like credits) and becomes **format 5** (Claude's call, same
  reasoning as format 4: an older Allister refuses a backup it would
  partly drop; formats 1-4 still restore).
- Image downloads reuse `internal/covers` (its SSRF, size and type checks);
  the existing cover cleanup must treat banner and thumbnail files as in use.
- Docker may be unavailable to the builder: tests run with
  `CGO_ENABLED=0 go test`; golangci-lint from the pinned v2.15.0 with
  `GOTOOLCHAIN=go1.27.4`.

## Approaches

- **A. Declared image slots plus a refresh the owner starts (chosen by the
  owner), with a bulk "Update from source" button in plugin settings.**
  New data is plain fields where it can be; banner, trailer and links are
  small declared contract additions; the owner decides when the bulk run
  sends its requests, and the button stays useful whenever a source gains
  data later.
- **B. The same data, filled in by an automatic one-off run after the
  update.** Nothing to click, but hundreds of source requests on first start
  without the owner's say, and every future addition needs another run.
- **C. One free-form "more info" block per item.** Quickest, but nothing in
  it can be filtered or sorted, which is the point.

Bulk behaviour, owner choice: (1) fill only what is empty, over (2) pick
fields to overwrite, then fill the rest, and (3) take everything (loses
edits).

## How it works (decided parts)

### Fields per kind

- Anime: `release_status` (enum: finished, releasing, upcoming, hiatus,
  cancelled), `season` (enum: winter, spring, summer, fall), `country`
  (text, filterable), `adult` (a yes/no value, filterable: the builder
  picks the field shape, e.g. an enum yes/no, since there is no boolean
  field type), `community_score` (number 0-100, sortable, shown),
  `popularity` (number, sortable, shown); `studio` becomes a list of the
  media's animation studios (`isAnimationStudio`; producers and licensors
  left out, owner decision), main studios first, filterable, keeping its key so
  columns, filters and stored values carry over (an existing single value
  becomes a one-element list).
- Manga and light novels: the same minus `season` and studios.
- Games: `release_status` (enum, from IGDB's game status; the builder maps
  IGDB's values onto a short list such as released, early access,
  upcoming, cancelled, delisted, other).
- Synonyms become titles of a dedicated alternative-title language the
  kinds declare separately from `TitleLanguages` (Claude's call, a new
  data-only contract declaration, validated and JSON round-trip tested):
  shown in the Titles section and matched by the text search, but never
  offered as a display language, never part of the fallback order and
  never used as the displayed title, not even as "first stored title".

### Hiding adult items

A per-plugin setting "Hide adult items" (`plugin.<id>.hide_adult`, default
off so nothing disappears after the update), shown only for plugins whose
kinds declare the adult flag (the contract names the field, data-only).
When on, adult items leave the lists, cards, home page, random pick and
calendar, other items' Related and Recommendations sections and the
add-from-source search results (Claude's call); the item page still opens
by link, statistics still count them
(Claude's call: hiding is about what is on screen, not what is counted).

### Item page

- Banner: full width above the header, from the local store; nothing shown
  without one.
- Trailer: the local thumbnail with a play symbol, linking to the video page
  (`rel="noopener noreferrer"`, new tab), labelled "Trailer".
- Links: the existing Links section lists the source's links (streaming
  sites) next to the source link.
- Community score and popularity as info rows.

### Bulk "Update from source"

- A button on the plugin's settings page, for plugins with a source; a
  confirmation names the number of sourced items it will ask about.
- The run asks each item's source (Details, and People when the kind shows
  credits), computes the differences with the existing compare core
  (`library.ItemChanges`) and applies, in one transaction per item:
  - single values (fields, the cover, the banner, the trailer) only where
    the stored side is empty (`StoredEmpty`);
  - lists (titles including synonyms, list fields such as studios,
    genres, tags, links, relations, credits) as an **additive merge**:
    elements the source has and the item lacks are appended, nothing is
    removed, reordered or changed (an element that exists with different
    details, e.g. a tag's rank, is left as stored). This needs its own
    compare/apply path beside `ApplyChanges`, which replaces a part as a
    whole; the single-item comparison page keeps its whole-part rows. Changes are not
  entry events (item data only).
- Paced and retried through the jobs queue (one job per item, the plugin
  gate pauses it while the plugin is disabled, rate limits retry later);
  `ErrNotConfigured` stops the run with the settings' "credential missing"
  notice; `ErrNotFound` and `ErrBadResponse` count the item as failed and
  go on.
- The settings page shows the state: running (done/total), or the last
  result ("Updated 412, nothing new 80, failed 3"), with the failed titles.
  A second press while one runs is refused. Newly downloaded images
  (banners, thumbnails, covers that were empty) follow through the existing
  sync path.

## Left to the builder

- Exact migration shape, contract field names and JSON keys.
- How the run's progress is stored (a run record like the importer's, or
  counted from the jobs table) as long as it survives a restart.
- The adult field's shape, enum labels and the IGDB value mapping.
- Markup and wording within the existing styles and colour tokens.
- Splitting into reviewable, green commits. Suggested order: contract and
  migration; AniList fields and synonyms; banner, trailer and links on the
  item page and comparison page; IGDB status; hide adult; bulk update;
  backup format 5; docs (usage.md, plugins.md) and CHANGELOG Unreleased.

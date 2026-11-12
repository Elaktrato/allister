# Using Allister: reference

Details behind the short sections of the [README](../README.md).

## Searching your list

The search bar above the list (`q`) and the Filters panel next to it (every other parameter except the sort) search only your own entries and update the list as you type. The panel works without JavaScript too (the Apply button submits it); the Filters button shows how many of its filters are set (the score range counts once), and a list opened with such filters starts with the panel open. The column headings of the table set `sort` and `dir`; only the columns you show (see [Plugin settings](#plugin-settings)) have a heading. Below 1024 px wide the Updated column is hidden, and below 768 px the Year column (any plugin's sortable field column) too; the Filters panel then shows a Sort by control with every sort key (the hidden columns and the columns you switched off included) and the direction, which sets the same `sort` and `dir` (on wider screens it is hidden and the headings sort). Everything is in the URL, so a filtered list can be bookmarked. Paging and the status tabs keep the filters, and "Clear filters" removes them. Parameters:

| Parameter | Meaning |
|---|---|
| `q` | Text matched against every stored title in any language (substring; case-insensitive for ASCII letters only, so `é` does not match `É`). Genres and tags are not searched. |
| `status` | Status tab: `planned`, `active`, `paused`, `completed`, `dropped` (empty: all). |
| `f.type` | Anime type (`TV`, `MOVIE`, `OVA`, ...). Repeat it to match any of several. |
| `f.year.from`, `f.year.to` | Year range (canonical form `f.year=1990..2000`). |
| `f.studio` | Studio contains the text. |
| `genre`, `tag` | One or more genres or tags (repeat the parameter). `genre_mode=all` or `tag_mode=all` requires all of them; the default is any. The filter bar shows counts from your library. |
| `progress` | `not_started`, `in_progress` or `finished`. |
| `rmin`, `rmax` | Score range 0 to 10. `unrated=1` and `zero=1` add unrated entries and entries scored 0 (rows matching any checked score choice are shown). |
| `unmatched=1`, `partial=1` | Only unmatched entries, or only entries whose cast or images are still syncing. |
| `sort`, `dir` | `updated` (recently updated, the default), `title`, `rating`, `year` or `progress`, and `asc` or `desc` (title defaults to ascending, the others to descending). Set by clicking a column heading of the table (Title, Year, Progress, Score, Updated): the first click sorts by that column in its default direction, a second click reverses it. The sorted column shows an arrow. Sorting keeps the filters and goes back to page 1. |

Unknown or invalid parameters are ignored, and a notice names them.

## Statistics

The Statistics page (`/stats`, in the sidebar) sums your list by the day each entry was finished. Each media kind declares how its time and units are counted (see [Statistics](plugins.md#statistics) in the plugin guide); a kind without a declaration only adds to the entry counts. The page is a plain GET form: every choice is in the URL, so a view can be bookmarked, and it works without JavaScript. Parameters:

| Parameter | Meaning |
|---|---|
| `media` | `all` (empty: every enabled kind), `<plugin>/<kind>` (for example `anime/anime`), or a bare `<plugin>` when it has one kind. A single kind also unlocks its `f.<field>` filters and its unit totals. |
| `period` | `all` (empty: all time), `year` (the current calendar year), `12m` (the last twelve calendar months, this one included) or `custom` (uses `from` and `to`). |
| `from`, `to` | Calendar days `YYYY-MM-DD`, both inclusive, used with `custom`. A date without `period` implies `custom`. A range that starts after it ends is refused. |
| `status` | A core status class: `planned`, `active`, `paused`, `completed` or `dropped` (empty: all). |
| `genre`, `tag` | One or more genres or tags (repeat the parameter), for the kinds that enable them as filters. `genre_mode=all` or `tag_mode=all` requires all of them; the default is any. At most 50 distinct values. |
| `f.<field>` | The list's field filters (the same syntax as [Searching your list](#searching-your-list)), for the one kind selected by `media`. Number and date fields take `f.<field>.from` and `f.<field>.to`. |

Unknown or invalid parameters are ignored, and a notice names them.

- **Completion-based periods.** A period counts an entry by its finished date. An entry without a finished date is left out of every period, and a completed entry without one is counted only in All time (the page notes how many there are in a period).
- **All time vs period.** With no period, every entry counts, the chart uses years, and an entry without a finished date still contributes its totals. With a period, the chart uses months for up to 24 months and years beyond that.
- **Ratings: 0 vs unrated.** A score of 0 is a real rating: it counts in the rated entries and in the average. An unrated entry has no score; it is left out of the average and shown as "unrated" in the distribution.
- **Time and units.** Time comes from the kind's declaration: for anime, the episodes times the episode duration. An entry whose time cannot be computed (for example an anime without an episode duration) is noted on the page, with a link to its list when one kind is selected. Use **Update from source** on its overview to fill the missing data. It fills in only the fields the item does not have yet, so your edits and earlier values stay. The anime episode duration (minutes per episode) is set in Edit details and filled from AniList when the source has it. Units (episodes, chapters, pages) are never added up across kinds, so All media shows time and entry counts only.
- **No batch backfill.** The heatmap knows only the activity recorded since the event log was added (migration 00017). Older history is not reconstructed, and the tracked-since note says from which day the record starts.
- **Size limit.** One page loads at most 20,000 entries. Beyond that the page shows a notice and the filter form, but no numbers.

**Activity heatmap.** The grid beside the figures shows the last 365 days, today included, with weeks starting on Monday. Days are UTC calendar days. It counts only what you did in the app: a progress increase or a status change to completed. Changes made by an import or a restore are not counted, so a large import does not paint the calendar. The heatmap is narrowed by `media` only; the period, status, genre, tag and field filters do not change it. "Tracked since" is the day of the earliest recorded change of yours.

**Backups.** The universal backup carries each entry's change log, newest first, up to 5,000 events per entry. Older events stay in the database; the archive marks an entry whose log was cut (`events_truncated`). A restore accepts no more than 5,000 events per entry.

## Plugin settings

Every installed plugin (enabled or not) has a settings page at `/settings/plugins/{id}`, linked from Settings > Plugins and Settings > General. Both forms are plain forms (no JavaScript needed) and, like every change, refuse requests from other sites.

- **Title language** (stored as the setting `plugin.<id>.title_lang`). The choices are the languages the plugin declares, followed by other language tags found in that plugin's saved titles; nothing else is accepted. Before you choose, the plugin's first language is used (anime: Japanese). When an entry has no title in the chosen language, the plugin's declared fallback order is tried (anime: Japanese, romaji, English), then the entry's first stored title. It applies to the list, the +1 row, overviews (and their titles block and related entries), source search, recommendations, person pages and the match dialog. List search matches every language, and exports and backups keep every title.
- **Table columns**, one form per kind (stored as `plugin.<id>.columns.<kind>`, a JSON list of column keys in order). The form lists every column the kind offers: the ones shown first, in their order, then the others. Tick or untick, use the arrows to move a shown column (the arrows save the ticks too), "Save columns", or "Reset to defaults" to remove the choice. Cover, title and actions are always there, so an empty choice shows the title only. Columns keep their screen-width hint (for anime, Type from 640 px, Year and Episodes from 768 px, Updated, Studio, Source, Started and Ended from 1024 px). The table head, its sort links (only for shown columns that sort), the rows and the htmx updates follow the choice; the compact Sort by control keeps every sort key. Columns the plugin no longer offers are dropped from a stored choice. The +1 button sits in the Progress column, so without that column the list has no +1 (the Edit dialog still sets progress). A +1 on an entry that is still planned moves it to active — bumping progress means you started it; no other status changes. For TV, a +1 also clears the playback position of the episode it advances.
- **Home** (`plugin.<id>.home.view`, `plugin.<id>.home.statuses`). The view a plugin's list opens in — **cards** (the default) or the table — and the status it opens with (every status, or one of the core status classes). The card view shows each entry as a cover card: the title on a dark overlay, the primary progress (the +1 button included, with a thin progress bar at the card's bottom edge in the status colour) and the status as a colored corner (slate planned, indigo active, amber paused, emerald completed, rose dropped). A list URL that names `view` or `status` always wins over these settings; the toggle in the list header switches the view for that visit. The status tabs sit on a strip; the current one carries its status colour.

Upgrading: the earlier global title language (Settings > General) is moved to the anime plugin's setting by a database migration, so nothing changes on screen.

## API keys

Settings > Plugins (`/settings/plugins`) has an **API keys** card for every installed source that needs a credential, whether its plugin is enabled or not. Today that is TMDB, which the TV and Movies plugins share as one key; IGDB, which needs a Twitch Client ID and Client Secret; and Steam, which needs one Steam Web API key. Each block shows the source, its key label and an optional hint where to get the credential. A single-key source (TMDB, Steam) has one password input. A multi-field source (IGDB) has one input per named field, all empty on every load; saving requires every field, and Remove clears them all. The forms are plain forms (no JavaScript needed) and refuse requests from other sites. They post to `POST /settings/plugins/{source}/key` with the field `key` for a single-key source, `field.<name>` for each field of a multi-field source (`field.client_id`, `field.client_secret`), or `remove=1` to clear the stored values; a source that is unknown or needs no key answers 404. The card is hidden when no installed source needs a key.

Credentials are stored as `source.<source-id>.<field>`, one setting per field. A single-key source uses the field name `key`, so `source.tmdb.key` and every older installation keep working unchanged (the single-key helpers read and write that `key` field). Remove deletes every stored field of the source; a credential that only comes from the environment cannot be removed there. A credential is read per request, so a saved value takes effect at once, without a restart. The stored value wins over the environment: `TMDB_API_KEY`, `IGDB_CLIENT_ID`, `IGDB_CLIENT_SECRET` and `STEAM_API_KEY` remain fallbacks. TMDB accepts both credential forms: a v3 API key (sent as the `api_key` query parameter) and a v4 API Read Access Token (a JWT starting `eyJ`, sent as a bearer token). The IGDB pair comes from a free Twitch application (`dev.twitch.tv/console/apps`); the Steam key from `steamcommunity.com/dev/apikey`.

A stored credential is never echoed: the page only says "A key is configured." when every effective value of the source is set, and every input is always empty. A value is 1 to 500 printable ASCII characters without spaces. Without a credential, a keyed source stays in search and match pickers marked "(needs key)"; selecting it explains the setting and points to Settings > Plugins, and items already saved from the source keep rendering from the database. The Steam store search and metadata endpoints themselves need no key, but the Steam source and its account import are gated by the Steam Web API key like every keyed source.

Credentials are plaintext settings in the database, like every other setting. They are not part of the universal backup or a JSON export (a restore on another machine needs them again), and errors and logs never include them.

## Appearance

Settings > Appearance (`/settings/appearance`) changes the look of every page for the whole installation (there is one user). Every form is a plain form (no JavaScript needed) and refuses requests from other sites; a change applies on the next page.

- **Colours.** A preset: Midnight (the default, the original look), Graphite, Forest, Rose, High contrast, Sand and Paper (light), or System, which follows the device's light or dark setting (Midnight or Paper). Every shipped preset is tested for WCAG contrast: 4.5:1 for text, faint labels included (7:1 in High contrast), 3:1 for icons, focus rings, input borders and the hovered accent button; Midnight keeps the original look and its two documented shortfalls (see Known limitations). Stored as `appearance.preset`.
- **Advanced** edits every colour (27 tokens: surfaces, text, borders, accent, status), starting from the colours in use (for System, the dark preset). Each has a colour picker and a `#rrggbb` field (`#rgb` is accepted); a typed value wins over the picker, and with JavaScript the two follow each other and the page previews the colour. Pairs below their contrast minimum are marked next to the colour, but can be saved. The palette is stored complete as JSON (`appearance.palette`); applying a preset or "Reset to preset" removes it. An edited palette is one fixed scheme (no light/dark switch).
- **Font and text size.** System font stacks (sans-serif, serif, monospace, humanist, rounded; nothing is downloaded), the bundled Inter, Source Serif 4 and JetBrains Mono (OFL, Latin and Latin Extended, see `PINS.md`), or an uploaded font, each shown with a sample line. Text size Small, Default or Large scales the whole layout. Stored as `appearance.font` and `appearance.font_scale`. Icons keep their own font.
- **Your fonts.** Upload woff2, woff, ttf or otf files, at most 2 MiB each and 8 in all, each with a name of up to 40 characters. The type is decided by the file's first bytes (anything else, SVG fonts and HTML included, is refused with 422; a larger file with 413), the file is stored in `DATA_DIR/fonts/` under its content hash (the same file uploaded twice is stored once), and the font is never parsed beyond that. Deleting the font in use returns the app font to the default. Only fonts you may use belong here.

Invalid stored values (for example after editing the database) fall back to the defaults instead of breaking a page. The colours reach the pages as CSS custom properties from `/appearance.css`, a generated stylesheet whose link carries a hash of its content, so it is cached for good and a change shows at once.

The universal backup includes these settings and the uploaded fonts. A restore stores the fonts like uploads (same checks and limits) and applies the settings only when the installation has no appearance settings of its own; everything refused is listed in the restore summary.

## URLs

| URL | Page |
|---|---|
| `/anime` | Your anime list (with the search and filter parameters above; `view=cards` or `view=table` picks the view, `status=all` explicitly shows every status) |
| `/anime/search?q=...` | Search AniList |
| `/anime/anilist/{id}` | Overview of an AniList anime; `/anime/anilist/{id}/edit` edits its details |
| `/anime/manual/new`, `/anime/manual/{id}` | Create and open manual entries; `/anime/manual/{id}/edit` edits details, `/anime/manual/{id}/match` matches to AniList |
| `/anime/export/json` | The JSON export |
| `/manga` | Your manga list (same search, filter and view parameters as `/anime`) |
| `/manga/search?q=...` | Search AniList for manga |
| `/manga/anilist/{id}` | Overview of an AniList manga; `/manga/anilist/{id}/edit` edits its details |
| `/manga/manual/new`, `/manga/manual/{id}` | Create and open manual manga entries; `/manga/manual/{id}/edit` edits details, `/manga/manual/{id}/match` matches to AniList |
| `/manga/export/json` | The manga JSON export |
| `/book` | Your books list (same search, filter and view parameters; `kind=series` shows light-novel series) |
| `/book/search?q=...&kind=book&source=openlibrary` | Search Open Library or Google Books for books, or AniList for light novels (`kind=series&source=anilist`) |
| `/book/{source}/{id}` | Overview of a book or series; add `?kind=book` or `?kind=series` when needed; `/edit` edits its details |
| `/book/manual/new`, `/book/manual/{id}` | Create and open manual books; `/book/manual/{id}/edit` edits details, `/book/manual/{id}/match` matches a source |
| `/book/export/json` | The books JSON export |
| `/tv` | Your TV list (same search, filter and view parameters) |
| `/tv/search?q=...&source=tvmaze` | Search TVmaze; TMDB needs a configured key (see [API keys](#api-keys)) |
| `/tv/{source}/{id}` | Overview of a TV show; `/edit` edits its details |
| `/tv/manual/new`, `/tv/manual/{id}` | Create and open manual shows; `/edit` edits details, `/match` matches a source |
| `/tv/export/json` | The TV JSON export |
| `/movie` | Your movies list (same search, filter and view parameters) |
| `/movie/search?q=...&source=tmdb` | Search TMDB (needs a configured key) |
| `/movie/{source}/{id}` | Overview of a movie; `/edit` edits its details |
| `/movie/manual/new`, `/movie/manual/{id}` | Create and open manual movies; `/edit` edits details, `/match` matches a source |
| `/movie/export/json` | The movies JSON export |
| `/game` | Your games list (same search, filter and view parameters) |
| `/game/search?q=...&source=igdb` | Search IGDB (needs Twitch app credentials); Steam (`source=steam`) is available too (needs a Steam Web API key) |
| `/game/{source}/{id}` | Overview of a game; `/edit` edits its details |
| `/game/manual/new`, `/game/manual/{id}` | Create and open manual games; `/edit` edits details, `/match` matches a source |
| `/game/export/json` | The games JSON export |
| `POST /settings/plugins/{source}/key` | Save a source's API key (`key`, or `field.<name>` for each named field) or remove the stored values (`remove=1`) and redirect back (see [API keys](#api-keys)) |
| `/list/{id}/edit` | The list entry's Edit dialog (status, rating, progress, dates, comment) |
| `/people/{id}` | Person page |
| `/settings`, `/settings/appearance`, `/settings/plugins`, `/settings/import`, `/settings/backup` | Settings sections: General (links to each plugin's settings), Appearance (see [Appearance](#appearance)), Plugins (API keys, switching plugins on and off), Import, Backup and restore |
| `/settings/plugins/{id}` | A plugin's settings: title language, home view and status, and table columns (see [Plugin settings](#plugin-settings)) |
| `/appearance.css?v=...` | The generated stylesheet of the appearance settings (linked by every page) |
| `/fonts/{file}` | An uploaded font file (only fonts listed on the Appearance page) |
| `/healthz` | Answers `ok`, for monitoring |
| `/calendar` | The month view of upcoming releases (see [Calendar](#calendar)) |
| `/calendar.ics` | The iCalendar feed of upcoming releases (same page) |
| `/stats` | The Statistics page with its filters (see [Statistics](#statistics)) |

Changes from the first version: a data volume from the first (pre-plugin) preview build starts empty after the upgrade (its anime tables are dropped, not copied); make a cold backup of the volume first if it holds data you want (see the README). List statuses are the core's status classes, so a bookmark like `/anime?status=watching` now opens the All tab with a notice (use `status=active`). The list's search and filter parameters are new. Edit details (`/anime/{source}/{id}/edit`) and the recommendations fragment (`/anime/{source}/{id}/recommendations`, loaded by the overview) are new routes.

## Imports

The anime and manga plugins each have their own **Import** section on their settings page, with two importers: the **AniList account** (enter the username; its public list is imported with status, score and progress) and the **MyAnimeList XML export** (upload the file MyAnimeList offers from your list page). The manga importer reads the export's manga rows and maps chapters and volumes to the chapter and volume progress. Books have their own **Import** section: the **Goodreads CSV** export (ISBNs resolve through Open Library, then Google Books; rows without an ISBN become manual entries) and the **AniList account** importer, which keeps only light novels and maps chapter and volume progress. The **Yamtrack Novel CSV** importer takes the general Yamtrack CSV (upload the whole export): only `manga` rows with a MyAnimeList id that AniList reports as `NOVEL` become Series entries, keeping chapters, status, score, dates and notes. The CSV has no volume progress, so none is set. Every other row is ignored; the report's ignored count covers the whole export. The real export has 539 manga rows, of which 11 are novels, and 2,827 ignored rows, 528 of them the non-novel manga rows. TV has a **Yamtrack TV CSV** importer: it groups the `tv`, `season` and `episode` rows into one show, accepts blank episode statuses (the row means watched), reads season/TV progress, and keeps the show's status, score and dates. Movies have a **Yamtrack Movie CSV** importer: only movie rows are used (rows of other media types are counted as ignored), duplicates by source and id are reported once, and status, score, dates, comment and cover are kept; a numeric TMDB id resolves through TMDB, while a row without one (or with another source) becomes a manual entry with a note. Without a TMDB key the TV and movie imports are manual-only; with a key, TMDB ids resolve automatically and other ids stay manual. The anime/manga Yamtrack CSV and the generic queue/history live on Settings > Import.

Games have two importers on the plugin's settings page. The **Yamtrack Game CSV** importer uses only `game` rows (every other row is counted as ignored), resolves IGDB ids through IGDB and keeps the row's status, score, dates, comment and playtime; rows with another source or an unusable IGDB id become manual entries with a note. A real Yamtrack export with 1,019 game rows yields 1,017 entries because two duplicate `(source, id)` pairs are skipped. The **Steam account** importer takes a 17-digit SteamID64 instead of a file and fetches your owned games from Steam: one entry per game with its playtime (Want to play when it has none, Playing otherwise). Steam has no statuses or scores, so adjust those yourself. Your Steam profile and its game details must be public; otherwise Steam answers with no games. Each owned game resolves through IGDB (details, time-to-beat and the Steam id), then the Steam store. Tools and software in the library (SteamVR, redistributables) stay manual rows with their Steam appid and the unmatched note, because neither source reports them as games; delete them. Without an IGDB key the Yamtrack game rows stay manual; without a Steam key the Steam account import explains the missing key.

**Playtime** is the game kind's list progress: a duration track (`h:mm:ss`, no +1), set by the Steam and Yamtrack imports or by you in the Edit dialog. It is your own total playtime. IGDB's **Main story**, **Main + extras** and **Completionist** estimates are metadata fields in minutes, not progress; the IGDB overview fills them when the time-to-beat endpoint answers and leaves them empty otherwise (that call is best effort and never blocks a game).

Each importer has a rule for entries already in your list: **Skip** leaves them alone (the default); **Update when the source is newer** applies the source's status, score and progress only when its entry was modified after your local one — formats without modification times (like the XML export) never update under this rule; **Always update** applies them unconditionally. Updates touch only status, score and progress of the list entry — never comments, dates, or item data like covers and credits.

The AniList import can repeat: **off, daily or weekly**. Saving a schedule starts the first import right away; the following rounds run in the background with the same rule (at most one import runs at a time, and a pending round survives a restart).

## Calendar

The app has its own month view at `/calendar` (in the sidebar): the current month's stored releases as chips, one per day, with previous/next/Today links and the air time shown in your timezone. The same data feeds `/calendar.ics`, an iCalendar feed for any calendar app on your network (Thunderbird, KOrganizer and friends: add a calendar "from URL", no account needed). It lists one timed 30-minute event per upcoming release of every entry you have marked as watching, for sources that declare schedules (the anime plugin does: AniList's air dates). The manga plugin has no chapter schedule (AniList has none), books have no release-schedule source, games have no release schedule, and movies have release dates but no air times, so none of them appears in the calendar. The TV plugin uses TVmaze episode air times; the calendar shows the season and episode (`Season 2, Episode 5`). Shows added from TMDB have no calendar events because TMDB has no air time. The summary is the entry's title in your chosen title language plus the kind's progress label — "Frieren — Episode 12" — and the description links back to the item. Each event starts at the source's air time, so your calendar shows it in your timezone.

Schedules are not fetched while a calendar app polls: the database answers, and entries whose schedule is missing or older than 12 hours are refreshed in the background through the normal paced job queue (at most 20 per request). An entry added as watching gets its schedule with its first sync, so it appears in the feed after the next poll. Air dates only cover what the source knows (a finished show has none; a newly announced episode appears after the next refresh). The feed is unauthenticated like the rest of the app — subscribe to it over your private network or tailnet, not from the internet.

## Known limitations

- Only the anime, manga, books, TV, movies and games plugins exist. Plugins are compiled in, and there is no marketplace or runtime installation. Manga, books, TV, movies and games are off after a fresh install; enable them in Settings > Plugins.
- TV sources: TVmaze is free and has schedules; TMDB needs a key (Settings > Plugins, or `TMDB_API_KEY` as a fallback), has no air times and adds cast, genres and details where TVmaze lacks them. MyDramaList, Trakt and TV Time are deferred (GitHub issues #2 and #3). Without a TMDB key the Yamtrack TV import is manual; with a key, TMDB ids resolve automatically.
- Movies: TMDB only, so live search, overviews and import resolution need a key. Without one the plugin is manual-only and has no calendar (TMDB has release dates, not air times). Letterboxd, IMDb and other movie imports are not built.
- Games: IGDB needs a Twitch Client ID and Client Secret; Steam needs a Steam Web API key, and its store search and metadata endpoints are unofficial (they may change, and an unexpected shape is reported as an unusable answer). Without IGDB the Yamtrack game import stays manual; without the Steam key the Steam account import and the Steam source explain the missing key. The Steam account import covers the owned library and playtime only (no achievements or screenshots), needs a public profile, and can leave non-game tools as unmatched manual rows. GOG and HowLongToBeat are deliberately out: neither offers an official library API or export, and IGDB's completion-time estimates replace HowLongToBeat.
- Google Books without `GOOGLE_BOOKS_API_KEY` is limited by the public quota; the source then reports rate limiting. The Goodreads import resolves through Open Library first and only falls back to Google Books when Open Library has no match. Mixing sources can create duplicate items for the same ISBN; the Goodreads import skips IDs it already knows.
- Book series are flat: one series entry tracks volumes and cumulative chapters, without per-volume child items. General book series use the optional `series` and `series number` fields.
- The title language is one per plugin, not per kind; the column choice is per kind.
- "Match to a source" accepts the source's declared URLs and ids; Google Books URLs carry the id in a query string, so paste the volume id or use search there.
- A live overview of an item that is not saved makes two AniList requests (details, then recommendations). Both are cached for 10 minutes.
- The JSON export builds the whole list in memory (fine for a few thousand entries).
- A restored AVIF cover is recognised by the file's main brand only. A rare AVIF file that declares another main brand is restored without its cover.
- Files of removed or re-matched entries are not deleted from `covers/` (see Disk use in the README).
- When AniList answers with unusable data, the search page says "unavailable" instead of naming the cause.
- An edited colour palette is one scheme; System's light/dark switch applies to the presets only. Contrast is checked for the listed pairs, not for every combination a page shows. Midnight keeps the original look's two known shortfalls, faint text (4.24:1 on the page, 3.75:1 on panels) and input borders (below 3:1); every other preset meets 4.5:1 for text and 3:1 for input borders.
- The import progress bar sets its width with an inline style, the one thing on the way to a strict Content-Security-Policy without `style-src 'unsafe-inline'`.
- Only the amd64 image build has been tried. The Debian packages of the CSS build stage are not pinned, and the distroless runtime image is pinned by digest under its `latest` tag.

## Code layout

- `cmd/allister`: wiring and startup
- `internal/web`: HTTP handlers, templates, static assets
- `internal/plugin`: the plugin contract (manifest, source, importer, exporter) and the registry
- `internal/library`: the generic library (items, list entries, people) over SQLite
- `internal/db`: SQLite access and migrations
- `internal/jobs`: background queue and item sync
- `internal/backup`: the universal backup archive (download and merge restore)
- `internal/importer`: the generic import runner (every plugin's importers)
- `internal/plugins/anime`: the anime plugin
- `internal/plugins/manga`: the manga plugin (AniList source, AniList-account and MyAnimeList-XML imports, chapters and volumes)
- `internal/plugins/book`: the books plugin (Open Library, Google Books and AniList sources; book and series kinds; Goodreads and AniList imports)
- `internal/plugins/tv`: the TV plugin (TVmaze and TMDB sources; Show kind; Yamtrack TV import)
- `internal/plugins/movie`: the movies plugin (TMDB source; Movie kind; Yamtrack movie import)
- `internal/plugins/game`: the games plugin (IGDB and Steam sources; Game kind; Yamtrack game and Steam account imports)
- `internal/plugins/tmdb`: the TMDB client shared by TV and movies, with the per-request key
- `internal/plugins/yamtrack`: the shared Yamtrack CSV parser used by anime, TV, movies and games
- `internal/plugins/anilist`: the AniList client shared by anime, manga and books (parameterised by media type and format search)
- `internal/plugins/jsonexport`: the JSON exporter shared by the plugins
- `internal/plugin/plugintest`: test-only book and TV fixture plugins that prove the contract
- `internal/guard`: tests that keep the core free of media-specific code and plugin imports
- `internal/appearance`: colour tokens and presets, fonts, the generated `/appearance.css`, uploaded fonts
- `internal/covers`, `internal/settings`, `internal/config`, `internal/ids`: small helpers

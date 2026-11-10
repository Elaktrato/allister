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

## Plugin settings

Every installed plugin (enabled or not) has a settings page at `/settings/plugins/{id}`, linked from Settings > Plugins and Settings > General. Both forms are plain forms (no JavaScript needed) and, like every change, refuse requests from other sites.

- **Title language** (stored as the setting `plugin.<id>.title_lang`). The choices are the languages the plugin declares, followed by other language tags found in that plugin's saved titles; nothing else is accepted. Before you choose, the plugin's first language is used (anime: Japanese). When an entry has no title in the chosen language, the plugin's declared fallback order is tried (anime: Japanese, romaji, English), then the entry's first stored title. It applies to the list, the +1 row, overviews (and their titles block and related entries), source search, recommendations, person pages and the match dialog. List search matches every language, and exports and backups keep every title.
- **Table columns**, one form per kind (stored as `plugin.<id>.columns.<kind>`, a JSON list of column keys in order). The form lists every column the kind offers: the ones shown first, in their order, then the others. Tick or untick, use the arrows to move a shown column (the arrows save the ticks too), "Save columns", or "Reset to defaults" to remove the choice. Cover, title and actions are always there, so an empty choice shows the title only. Columns keep their screen-width hint (for anime, Type from 640 px, Year and Episodes from 768 px, Updated, Studio, Source, Started and Ended from 1024 px). The table head, its sort links (only for shown columns that sort), the rows and the htmx updates follow the choice; the compact Sort by control keeps every sort key. Columns the plugin no longer offers are dropped from a stored choice. The +1 button sits in the Progress column, so without that column the list has no +1 (the Edit dialog still sets progress). A +1 on an entry that is still planned moves it to active — bumping progress means you started it; no other status changes.
- **Home** (`plugin.<id>.home.view`, `plugin.<id>.home.statuses`). The view a plugin's list opens in — **cards** (the default) or the table — and the status it opens with (every status, or one of the core status classes). The card view shows each entry as a cover card: the title on a dark overlay, the primary progress (the +1 button included, with a thin progress bar at the card's bottom edge in the status colour) and the status as a colored corner (slate planned, indigo active, amber paused, emerald completed, rose dropped). A list URL that names `view` or `status` always wins over these settings; the toggle in the list header switches the view for that visit. The status tabs sit on a strip; the current one carries its status colour.

Upgrading: the earlier global title language (Settings > General) is moved to the anime plugin's setting by a database migration, so nothing changes on screen.

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
| `/list/{id}/edit` | The list entry's Edit dialog (status, rating, progress, dates, comment) |
| `/people/{id}` | Person page |
| `/settings`, `/settings/appearance`, `/settings/plugins`, `/settings/import`, `/settings/backup` | Settings sections: General (links to each plugin's settings), Appearance (see [Appearance](#appearance)), Plugins (switch plugins on and off), Import, Backup and restore |
| `/settings/plugins/{id}` | A plugin's settings: title language, home view and status, and table columns (see [Plugin settings](#plugin-settings)) |
| `/appearance.css?v=...` | The generated stylesheet of the appearance settings (linked by every page) |
| `/fonts/{file}` | An uploaded font file (only fonts listed on the Appearance page) |
| `/healthz` | Answers `ok`, for monitoring |
| `/calendar` | The month view of upcoming releases (see [Calendar](#calendar)) |
| `/calendar.ics` | The iCalendar feed of upcoming releases (same page) |

Changes from the first version: a data volume from the first (pre-plugin) preview build starts empty after the upgrade (its anime tables are dropped, not copied); make a cold backup of the volume first if it holds data you want (see the README). List statuses are the core's status classes, so a bookmark like `/anime?status=watching` now opens the All tab with a notice (use `status=active`). The list's search and filter parameters are new. Edit details (`/anime/{source}/{id}/edit`) and the recommendations fragment (`/anime/{source}/{id}/recommendations`, loaded by the overview) are new routes.

## Imports

The anime plugin's settings page has its own **Import** section with two importers: the **AniList account** (enter the username; its public list is imported with status, score and progress) and the **MyAnimeList XML export** (upload the file MyAnimeList offers from your list page). The Yamtrack CSV import lives on Settings > Import, together with the queue and the history of every import.

Each importer has a rule for entries already in your list: **Skip** leaves them alone (the default); **Update when the source is newer** applies the source's status, score and progress only when its entry was modified after your local one — formats without modification times (like the XML export) never update under this rule; **Always update** applies them unconditionally. Updates touch only status, score and progress of the list entry — never comments, dates, or item data like covers and credits.

The AniList import can repeat: **off, daily or weekly**. Saving a schedule starts the first import right away; the following rounds run in the background with the same rule (at most one import runs at a time, and a pending round survives a restart).

## Calendar

The app has its own month view at `/calendar` (in the sidebar): the current month's stored releases as chips, one per day, with previous/next/Today links and the air time shown in your timezone. The same data feeds `/calendar.ics`, an iCalendar feed for any calendar app on your network (Thunderbird, KOrganizer and friends: add a calendar "from URL", no account needed). It lists one timed 30-minute event per upcoming release of every entry you have marked as watching, for sources that declare schedules (the anime plugin does: AniList's air dates). The summary is the entry's title in your chosen title language plus the kind's progress label — "Frieren — Episode 12" — and the description links back to the item. Each event starts at the source's air time, so your calendar shows it in your timezone.

Schedules are not fetched while a calendar app polls: the database answers, and entries whose schedule is missing or older than 12 hours are refreshed in the background through the normal paced job queue (at most 20 per request). An entry added as watching gets its schedule with its first sync, so it appears in the feed after the next poll. Air dates only cover what the source knows (a finished show has none; a newly announced episode appears after the next refresh). The feed is unauthenticated like the rest of the app — subscribe to it over your private network or tailnet, not from the internet.

## Known limitations

- Only the anime plugin exists. Plugins are compiled in, and there is no marketplace or runtime installation.
- The title language is one per plugin, not per kind; the column choice is per kind.
- "Match to AniList" accepts AniList URLs and numeric ids only.
- A live overview of an anime that is not saved makes two AniList requests (details, then recommendations). Both are cached for 10 minutes.
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
- `internal/plugins/anime`: the anime plugin; `anilist`: its AniList client, `yamtrack`: its Yamtrack CSV parser
- `internal/plugin/plugintest`: test-only book and TV fixture plugins that prove the contract
- `internal/guard`: tests that keep the core free of media-specific code and plugin imports
- `internal/appearance`: colour tokens and presets, fonts, the generated `/appearance.css`, uploaded fonts
- `internal/covers`, `internal/settings`, `internal/config`, `internal/ids`: small helpers

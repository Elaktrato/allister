# Allister: personal media tracker, foundation + anime

Date: 2026-11-09
Status: draft for approval

## Intent and why

Allister is a self-hosted, single-user tracker for everything the owner consumes: anime, manga, movies, TV shows, video games and books. It is a self-hostable "MyAnimeList / IMDb / Goodreads in one place". Nothing about existing sites annoyed the owner; the goal is owning the data and having all media types under one roof. A previous tool, Yamtrack, was disliked mainly because the title language could not be chosen (the owner wants Japanese titles).

This document covers the shared foundation and the first media type, **anime**. The other types come later, one at a time, each with its own design round. The foundation must make adding a media type cheap, but must not build speculative abstractions for types that are not yet designed.

## Goals

- Runs in Docker (single container plus a volume), stack: **Go + htmx + Tailwind**.
- Anime flow modelled on MyAnimeList: search, overview page, add to list with status/rating/progress/dates/comment, a list page.
- Preferred **title language is a setting** (Japanese native, romaji, English, other languages where the source has them). Default for the owner: Japanese. It must be a real setting, since the project may be open-sourced.
- One metadata source: **AniList**. Plus **manual entry** when AniList does not have the title.
- Local copy of everything needed to render a saved entry, so the list works offline or when a source is down.
- Local person pages for characters and voice actors, showing which anime *in the user's list* they appear in.
- **Import from Yamtrack** CSV export (anime rows only).
- **Match to AniList**: a manual entry (including an unmatched import row) can later be matched by hand to an AniList page, by pasting its URL or by searching in a dialog.

## Non-goals (first version)

- No accounts, authentication, multi-user, or sharing. Private use only.
- No dashboards or statistics pages.
- No personalised "recommended for you" feed.
- No merging or de-duplication across sources.
- Other media types (manga, movies, TV, games, books).
- MyAnimeList / Jikan source (dropped by the owner; can be added later behind the same provider interface).

## Anti-goals

- Importing large amounts of metadata the user never asked for (recommendations must not turn into bulk imports).
- A design where adding the second media type needs a rewrite of the first.

## Screens (agreed in mockups)

1. **Search**: query box, results with cover, title, type, episodes, year, studio, short synopsis, and an Add button. A "Can't find it? Add manually" entry point.
2. **Overview page**: cover and "Add to list" button; info (type, episodes, aired dates, studio, source material); titles in multiple languages; synopsis; related entries (source material, prequels, sequels) ; characters with voice actors; a **Recommendations** row. For a manual entry the page has a **Match to AniList** button (see "Matching an entry" below).
3. **Add to list dialog** (over the overview): status (watching, plan to watch, paused, completed, dropped), rating (optional: empty means unrated; 0 to 10, where 0 is a valid rating the user can set), current episode (of total), start date, finish date, optional comment.
4. **List page**: status tabs, rows with title, progress (with +1 episode button), score, status. Imported rows that AniList could not match carry a small "unmatched" badge.
5. **Character / voice actor local pages**: photo, name, and the anime in the user's list they appear in (voice actors also show the character played). Every character and voice actor shown on an overview also links to their AniList page; both links are shown (local and external).
6. **Settings**: title language, import.
7. **Import**: upload Yamtrack CSV, progress page. The page counts and lists unmatched rows, each with a link to its saved entry.

## Decisions made by the owner

- AniList is the only metadata source.
- Manual entry when AniList does not have the title.
- Cover images are stored locally at add time.
- Characters/voice actors get a local page and an external link.
- Single user, no accounts.
- Dashboards excluded; recommendations wanted (as light, on-demand references).
- Ratings: unrated (no value) is distinct from a rating of 0; 0 to 10 are all valid ratings. Yamtrack scores of 0.0 import as unrated.
- Yamtrack import wanted: roughly 800-900 anime, the general Yamtrack CSV export (contains other media types too; non-anime rows are skipped).
- Unmatched imports are acceptable, but the owner wants to be able to match such an entry (or any manual entry) to AniList by hand later, by providing the AniList URL or by searching in a dialog.

## Decisions made by me (the builder-side calls, to be checked)

- **Storage**: SQLite in a Docker volume (simple to back up).
- **Recommendations** are fetched live from the source when an overview is opened, shown as title/cover/ID references, never stored unless the user adds one.
- **Characters and voice actors are persisted when an entry is added**, not when merely viewed; the local person pages therefore only know about anime in the list.
- **Overview pages for entries not in the list** are fetched live (not stored).
- **Responsive layout** (Tailwind), not a separate mobile design.
- **Yamtrack import** is a resumable background job with a progress page. Yamtrack rows are keyed by MyAnimeList ID; the importer resolves each MAL ID through AniList (`Media(idMal:, type: ANIME)`) and saves entries with source `anilist`. Rows already saved (matched by the stored `mal_id`) are skipped before any request is made, so a resume or re-run redoes nothing. Rows AniList cannot match are not dropped: they are saved as manual entries (see "Import" under Further calls) and listed as "unmatched" in the import result. The import is paced to AniList's rate limit (assume 30 requests per minute, the currently degraded limit, as a constant); an import of ~830 entries therefore takes on the order of an hour or two (about 2 requests per anime for details plus cast).

### Further calls (from the builder check; mine, not the owner's)

- **Identity**: an entry is identified by `(source, source_id)`, where source is `anilist` or `manual`. A saved entry belongs to the source it was saved from; its recommendations, related entries and character links are fetched from that source. Adding the same `(source, source_id)` twice is blocked.
- **Titles**: all titles an entry has are stored with a language tag. The settings dropdown offers Japanese (native), romaji and English, plus any other languages found in stored data. Fallback chain when the preferred language is missing: native, romaji, English, then any available title. Manual entries record the language of the title typed in (default: the preferred language).
- **People**: characters and voice actors are identified by `(source, source_id)` and are not merged across sources.
- **Links on overview pages**: for an entry not in the list (live-fetched), people link only to the external AniList page. Once the entry is saved and its people are stored, each person also gets the local link. This corrects "both links are shown" in the screens list: both links appear wherever the person exists locally.
- **Import depth**: imported entries are stored like any added entry (details, titles, related entries, cover, characters and voice actors), because the local person pages need the cast. With AniList's detail and character queries that is about two requests per anime, so roughly one to two hours for ~830 entries at 30 requests per minute (an estimate). The import mapping can be planned now; the CSV mapping step waits for the sample export.
- **Manual entries**: required fields are title and type; optional are episode count, release year, studio, synopsis, and a cover image upload. They have an overview page with those fields, no characters, and can later be matched to AniList (see "Matching an entry"). Entries created by the Yamtrack import for unmatched rows are manual entries too.
- **Import, unmatched rows**: a row AniList cannot match (`ErrNotFound`) is saved as a `manual` entry carrying the Yamtrack data: the Yamtrack title as the entry's title with language tag `ja-latn`, the remote cover URL from the CSV `image` column, status, score, progress, start and end dates, and notes as comment, with `mal_id` set to the Yamtrack `media_id`. An entry is "unmatched" when its source is `manual` and `mal_id` is not null (no schema change). Such rows are still counted and listed as "unmatched" on the import progress page, each with a link to its entry, and the list page shows an "unmatched" badge on them.
- **Matching an entry**: the overview page of any manual entry has a "Match to AniList" button that opens a dialog with (a) a field accepting an AniList URL (`https://anilist.co/anime/154587`, `anilist.co/anime/154587/Sousou-no-Frieren`) or a bare number, and (b) a search box using the existing AniList search, with results to pick from. Choosing one converts the entry in place: same `anime.id`; the list entry (status, rating, progress, dates, comment) is untouched; source changes `manual` to `anilist` and `source_id` becomes the AniList id; titles, relations, synopsis, studio, episodes, year, dates, source material and external URL are replaced by the AniList data (a custom uploaded cover is replaced by the AniList cover); `mal_id` is set to AniList's `idMal` if present, otherwise kept; `sync_state` becomes `pending` and a `sync_anime` job is enqueued (cast and cover download). If `(anilist, id)` is already saved as another entry, the match is refused with a visible message linking to that entry and nothing changes. An invalid URL or an unknown AniList id shows an error in the dialog; a provider outage shows a retry message and changes nothing (the conversion is one transaction).
- **Adding while a source is flaky**: adding saves the core details already fetched for the overview right away. Cover download and cast fetch run as background jobs, are retried, and the entry shows a "partially synced" marker until they finish. The list works throughout.
- **Deployment**: assumed private network only (no authentication), one configurable port, and a single volume holding the SQLite file and cover images.

## Constraints

- Self-hosters should need no accounts or API keys to get started.
- Respect source rate limits (AniList); handle source errors gracefully in the UI.
- Titles must be stored per language so the display language can change without refetching.

## Left to the builder

- Project layout, Go packages, router and template library, migration tooling, SQL layer.
- Data model details, including how media types share a table or not.
- Exact Tailwind build setup (standalone CLI vs Node) and htmx usage patterns.
- Caching and retry strategy for sources, within the rate-limit constraint.
- Visual design details beyond the wireframes.
- Test strategy.
- Mapping of Yamtrack's CSV columns and status values onto the list model (a sample export will be provided later by the owner).

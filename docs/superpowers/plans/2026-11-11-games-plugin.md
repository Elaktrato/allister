# Games Plugin and Multi-Field Source Credentials Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the opt-in Games plugin (one Game kind, Steam account/library + IGDB metadata, Yamtrack game import, playtime + IGDB time-to-beat) and extend source credentials to named multi-field keys.

**Architecture:** `SourceInfo.KeyFields` names several credentials per source; `settings` stores `source.<id>.<field>`; the API keys card renders one input per field. IGDB (Client ID/Secret) and Steam (single key) serve one Game kind; playtime is a duration progress track, and IGDB's `game_time_to_beats` supplies the three completion estimates. Steam imports resolve through IGDB first (IGDB maps Steam app ids), then the Steam store.

**Tech Stack:** Go via `scripts/go.sh` (Docker, CGO off), SQLite, `net/http`, `html/template`, htmx, Tailwind, existing plugin/library/importer/web layers.

**Spec:** `docs/superpowers/specs/2026-11-11-games-plugin-design.md`

## Global Constraints

- No new Go dependencies; HTTP via the standard library.
- No database migration; source key fields use the existing `settings` table. The next migration stays **00017**.
- Plugin contract data only; no plugin HTML/JS; the core guard; no hard-coded colours.
- Keys and tokens are never logged, rendered, exported or backed up, and never appear in an error (including request URLs). Do not wrap transport errors from keyed requests.
- IGDB: Twitch OAuth client credentials; 4 requests/second; declare 60 requests/minute for the importer row pace (2-3 requests per row). Cache the token; refresh 60s early; on a data 401 refresh once and retry. Time-to-beat is best-effort and never fails the game details.
- Steam: Web API key for `GetOwnedGames`; the store `storesearch`/`appdetails` endpoints are unofficial, may change, and are treated as `ErrBadResponse` on unexpected shapes. Declare 60 requests/minute.
- SteamID64 is an account field, not a key. A missing/refused Steam key on the account import becomes `ErrBadInput` with an actionable message, never the raw `ErrNotConfigured`.
- One link per item: its own source link. No second IGDB/Steam link.
- README stays at or below 150 lines.
- Preview only on port 18083 / `allister-preview`; the owner's 18080-18082 containers stay untouched. The owner pushes.
- Full suite, `vet`, `fmt` before each commit; output pristine.

## Review Focus

1. **IGDB key/token path** — an empty or refused key, a token expiry, and a 401 during a data request must produce the actionable 503, refresh once, never leak the secret or token, and never retry a bad key forever. (Task 9)
2. **Steam account import** — a missing/refused Steam key, a private/empty library, and non-game owned apps must not break the import or show a raw sentinel; non-games become labeled manual rows. (Tasks 10, 11)
3. **The owner's 1,019 Yamtrack rows** — `1h 16min` becomes Playtime seconds, statuses/scores are preserved, duplicates/non-game rows are handled, and a keyless IGDB import falls back to manual. (Tasks 6, 12)
4. **Multi-field key card** — every field required, no field echoed, a partial save stores nothing, Remove clears all fields, env-only is configured but not removable, and the single TMDB/Steam key still works. (Task 4)
5. **IGDB slug identity and flaky time-to-beat** — a slug match and a search-add converge on the numeric item, and a time-to-beat failure still returns the game. (Tasks 3, 9)

---

### Task 1: Multi-field source credential contract

**Files:**
- Modify: `internal/plugin/contract.go`
- Modify: `internal/plugin/validate.go`
- Modify: `internal/plugin/contract_json_test.go`
- Modify: `internal/plugin/validate_test.go`
- Modify: `internal/plugin/plugintest/plugintest.go`

**Interfaces:**
- Consumes: the existing `SourceInfo.KeyLabel`/`KeyHelp`.
- Produces: `plugin.KeyField{Name, Label string; Secret bool}`; `SourceInfo.KeyFields []KeyField` (`key_fields,omitempty`); a `plugintest.WithSourceKeyFields(fields ...plugin.KeyField) Option` that sets the first fixture source's fields.

- [ ] **Step 1: Write the failing tests**

Add a `KeyFields` slice to the `full` manifest's source in `contract_json_test.go`:

```go
KeyFields: []plugin.KeyField{{Name: "client_id", Label: "Client ID"}, {Name: "client_secret", Label: "Client Secret", Secret: true}},
```

Add a `plugintest` option and a valid fixture test. Add these cases to `TestValidateRejects`:

```go
{"key fields without group label", tv, func(m *plugin.Manifest) {
    m.Sources[0].KeyLabel = ""
    m.Sources[0].KeyFields = []plugin.KeyField{{Name: "client_id", Label: "Client ID"}}
}, `sources[0].key_fields: key_label is the group label`},
{"key field bad name", tv, func(m *plugin.Manifest) {
    m.Sources[0].KeyFields = []plugin.KeyField{{Name: "Client ID", Label: "Client ID"}}
}, `sources[0].key_fields[0].name`},
{"key field duplicate", tv, func(m *plugin.Manifest) {
    m.Sources[0].KeyFields = []plugin.KeyField{{Name: "id", Label: "A"}, {Name: "id", Label: "B"}}
}, `sources[0].key_fields[1].name: duplicate`},
{"key field label too long", tv, func(m *plugin.Manifest) {
    m.Sources[0].KeyFields = []plugin.KeyField{{Name: "id", Label: strings.Repeat("L", 41)}}
}, `sources[0].key_fields[0].label: longer than 40 characters`},
{"too many key fields", tv, func(m *plugin.Manifest) {
    m.Sources[0].KeyFields = []plugin.KeyField{
        {Name: "a", Label: "A"}, {Name: "b", Label: "B"}, {Name: "c", Label: "C"},
        {Name: "d", Label: "D"}, {Name: "e", Label: "E"},
    }
}, `sources[0].key_fields: at most 4 fields`},
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `scripts/go.sh test -count=1 ./internal/plugin/`
Expected: FAIL — `undefined: plugin.KeyField` / missing validation.

- [ ] **Step 3: Implement**

Add `KeyField` and `KeyFields` to `contract.go`. In `validate.go` check: `KeyFields` without `KeyLabel` invalid; at most 4; each name is a lower-case slug (`keyRe`, 1-32, letters/digits/`_`), unique; each label is single-line ≤40. Keep the existing single-key validation. Add the `plugintest` option.

- [ ] **Step 4: Run the test to verify it passes**

Run: `scripts/go.sh test -count=1 ./internal/plugin/`
Expected: PASS.

- [ ] **Step 5: Full suite, vet, fmt, commit**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add internal/plugin
git commit -m "feat: support named source credential fields"
```

---

### Task 2: Settings storage for named source fields

**Files:**
- Modify: `internal/settings/settings.go`
- Modify: `internal/settings/settings_test.go`

**Interfaces:**
- Consumes: the existing `settings.Get/Set`.
- Produces: `SourceKeyField(ctx, sourceID, field string) (string, error)`, `SetSourceKeyField(ctx, sourceID, field, value string) error`, `ClearSourceKeys(ctx, sourceID string) error`; `SourceKey`/`SetSourceKey` delegate to field `key`; the setting key is `source.<id>.<field>`.

- [ ] **Step 1: Write the failing test**

```go
func TestSourceKeyFields(t *testing.T) {
    s, _ := newStore(t)
    ctx := context.Background()
    if got, err := s.SourceKeyField(ctx, "igdb", "client_id"); got != "" || err != nil {
        t.Fatalf("unset: %q %v", got, err)
    }
    if err := s.SetSourceKeyField(ctx, "igdb", "client_id", " id "); err != nil { t.Fatal(err) }
    if err := s.SetSourceKeyField(ctx, "igdb", "client_secret", "secret"); err != nil { t.Fatal(err) }
    if v, ok, _ := s.Get(ctx, "source.igdb.client_id"); !ok || v != "id" { t.Fatalf("stored %q %v", v, ok) }
    if err := s.SetSourceKeyField(ctx, "steam", "key", "steam-key"); err != nil { t.Fatal(err) }
    if err := s.SetSourceKeyField(ctx, "tmdb", "key", "tmdb-key"); err != nil { t.Fatal(err) }
    if err := s.ClearSourceKeys(ctx, "igdb"); err != nil { t.Fatal(err) }
    for _, f := range []string{"client_id", "client_secret"} {
        if got, _ := s.SourceKeyField(ctx, "igdb", f); got != "" { t.Fatalf("%s survived: %q", f, got) }
    }
    if got, _ := s.SourceKeyField(ctx, "steam", "key"); got != "steam-key" { t.Fatalf("other source: %q", got) }
    // the single-key API still reads and writes field "key"
    if got, _ := s.SourceKey(ctx, "tmdb"); got != "tmdb-key" { t.Fatalf("single: %q", got) }
}
```

Add a closed-DB error test for `SourceKeyField`/`SetSourceKeyField`/`ClearSourceKeys` (mirror the existing enabled-store error test).

- [ ] **Step 2: Run the test to verify it fails**

Run: `scripts/go.sh test -count=1 ./internal/settings/`
Expected: FAIL — undefined methods.

- [ ] **Step 3: Implement**

Add the three methods; `SetSourceKeyField` trims. Make `SourceKey` call `SourceKeyField(...,"key")`, `SetSourceKey` call `SetSourceKeyField(...,"key")`; `ClearSourceKey` deletes only the `key` field. `ClearSourceKeys` deletes every `source.<id>.%` row (source ids are slugs; no LIKE metacharacters).

- [ ] **Step 4: Run the test to verify it passes**

Run: `scripts/go.sh test -count=1 ./internal/settings/`
Expected: PASS.

- [ ] **Step 5: Full suite, vet, fmt, commit**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add internal/settings
git commit -m "feat: store named source credentials"
```

---

### Task 3: Match keeps the source's canonical id

**Files:**
- Modify: `internal/web/match.go`
- Modify: `internal/web/match_test.go`

**Interfaces:**
- Consumes: `plugin.Source.Details` / `plugin.ItemData`.
- Produces: `postMatch` sets `d.Source = si.ID`, `d.Kind = t.kind.Key`, and keeps the source's `d.ID` when non-empty (falling back to the matched input); the redirect and converted item use that canonical id.

- [ ] **Step 1: Write the failing test**

Use the existing match fixture, but a source whose `Details` returns `ItemData{ID: "canonical-slug", Titles: ...}` for the matched id `input-id`. POST the match and assert:

```go
// Location is the item URL with the canonical source id, not the input.
if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/tv/tvdata/canonical-slug" {
    t.Fatalf("%d %q", rec.Code, rec.Header().Get("Location"))
}
// the saved item's source id is canonical
```

Add a second case where `Details` returns an empty `ID`; the input id is kept.

- [ ] **Step 2: Run the test to verify it fails**

Run: `scripts/go.sh test -count=1 -run 'Match.*Canonical' ./internal/web/`
Expected: FAIL — the item/redirect uses the input id.

- [ ] **Step 3: Implement**

In `postMatch`, replace the overwrite with:

```go
d.Source, d.Kind = si.ID, t.kind.Key
if d.ID == "" {
    d.ID = id
}
```

Use `d.ID` in the final `itemURL` redirect. The source data's ID must be a valid path id; the library validation catches it.

- [ ] **Step 4: Run the test to verify it passes**

Run: `scripts/go.sh test -count=1 ./internal/web/`
Expected: PASS.

- [ ] **Step 5: Full suite, vet, fmt, commit**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add internal/web/match.go internal/web/match_test.go
git commit -m "fix: keep a source's canonical id on match"
```

---

### Task 4: Multi-field API keys card and configured-source helper

**Files:**
- Modify: `internal/web/server.go` (Deps)
- Modify: `internal/web/settings.go` (card, handler, helpers)
- Modify: `internal/web/plugin_settings.go` (notice/message)
- Modify: `internal/web/search.go` (`sourceChoiceLabel`/`sourceConfigured`)
- Modify: `internal/web/templates/settings_plugins_page.html`
- Modify: `internal/web/source_keys_test.go`
- Create: `internal/web/source_keys_fields_test.go`

**Interfaces:**
- Consumes: `SourceInfo.KeyFields` (Task 1), settings field methods (Task 2).
- Produces: `web.Deps.SourceKeyField func(ctx, sourceID, field string) (string, error)`; `sourceKeyField`/`sourceConfigured` helpers; `sourceKeyView.Fields []sourceKeyFieldView`; the multi-field form (`name="field.<name>"`); a configured source helper used by labels, the notice, and the card.

- [ ] **Step 1: Write the failing tests**

Use `plugintest.TVPlugin(plugintest.WithSourceKeyFields(plugin.KeyField{Name: "client_id", Label: "Client ID"}, plugin.KeyField{Name: "client_secret", Label: "Client Secret", Secret: true}))` in a new `newFieldFixtureEnv` (or extend `newFixtureEnv`). Test:

```go
func TestSourceKeyFieldsCardSavesAllOrNothing(t *testing.T) {
    // block shows Client ID/Client Secret, no values, both action names
    // POST only client_id -> 400, nothing stored, no echo
    // POST both -> 303, sourceConfigured true, card "A key is configured."
    // GET shows no field values; Remove shown
    // POST remove=1 -> 303; both fields empty; no Remove
}
func TestSourceKeyFieldsEnvironmentAndSingleCompatibility(t *testing.T) {
    // env both fields -> configured, no Remove
    // the existing single tvdata key tests still pass
}
func TestSourceKeyFieldsLabelsAndNotice(t *testing.T) {
    // one field missing -> "Fake TV (needs key)" in a source switch
    // the plugin page notice links to #source-keys
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `scripts/go.sh test -count=1 -run 'SourceKeyFields' ./internal/web/`
Expected: FAIL — no multi-field card/helpers.

- [ ] **Step 3: Implement**

- Add `Deps.SourceKeyField`; server helpers `sourceKeyField(ctx,id,field)` (dep, else `Settings.SourceKeyField`, else "") and `sourceConfigured(ctx, si)` (single: key non-empty; multi: every field non-empty; no key label: true).
- `sourceKeyView` gains `Fields []sourceKeyFieldView{Name, Label, Secret}`; `sourceKeyViews` reads every field, sets `Configured`/`Stored` (any stored for multi, the single key for single).
- `postSourceKey` looks up the source info; for multi fields read `field.<name>`, trim and validate each (same 1-500 printable ASCII rule), require all, store each; `remove=1` calls `ClearSourceKeys`; single sources keep `key`/`SetSourceKey`.
- The template renders the multi fields (password only when `Secret`), labels, Save/Remove; the group `KeyLabel`/`KeyHelp`; the existing single branch.
- `pluginSettingsData`'s `MissingKeys` and `sourceChoiceLabel` use `sourceConfigured`; the `sourceMessage` for a multi source uses `KeyLabel` as the group label.
- Keep the single-key compatibility and no-echo tests green.

- [ ] **Step 4: Run the test to verify it passes**

Run: `scripts/go.sh test -count=1 ./internal/web/`
Expected: PASS.

- [ ] **Step 5: Full suite, vet, fmt, commit**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add internal/web
git commit -m "feat: edit named source credentials in the web UI"
```

---

### Task 5: Configured default source for a kind

**Files:**
- Modify: `internal/web/search.go`
- Modify: `internal/web/match.go`
- Modify: `internal/web/search_test.go` / `internal/web/match_test.go` (or a new focused test)

**Interfaces:**
- Consumes: `sourceConfigured` (Task 4).
- Produces: `(*server).defaultSourceForKind(ctx, m, kind)` returns the first serving source that is configured (preferring `SupportsLookup`), else the current first source; `(*server).chooseSourceForKind(ctx, m, kind, id)` uses it for an empty or non-serving id. `search` and the three match call sites use the server versions.

- [ ] **Step 1: Write the failing test**

A fixture plugin with two keyed sources serving the kind: IGDB-like multi-field (unconfigured) first, Steam-like single key configured second. Assert `GET /tv/search?q=x` selects the Steam source (its heading/placeholder/source option), while with both configured the first source is selected; with neither, the first.

- [ ] **Step 2: Run the test to verify it fails**

Run: `scripts/go.sh test -count=1 -run 'DefaultSource' ./internal/web/`
Expected: FAIL — the first source is always selected.

- [ ] **Step 3: Implement**

Add the server methods; update `search`, `getMatch`, `getMatchSearch`, `postMatch` and the match fallback. The current pure `defaultSourceForKind`/`chooseSourceForKind` may stay as the fallback.

- [ ] **Step 4: Run the test to verify it passes**

Run: `scripts/go.sh test -count=1 ./internal/web/`
Expected: PASS.

- [ ] **Step 5: Full suite, vet, fmt, commit**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add internal/web
git commit -m "feat: choose a configured source by default"
```

---

### Task 6: Yamtrack raw playtime

**Files:**
- Modify: `internal/plugins/yamtrack/raw.go`
- Modify: `internal/plugins/yamtrack/raw_test.go`
- Modify: `internal/plugins/yamtrack/parse_test.go` (if the progress note changes)

**Interfaces:**
- Consumes: the existing parser.
- Produces: `RawRow.ProgressRaw string` (the raw `progress` cell); `func ParsePlaytime(s string) (seconds int, ok bool)` handling `Xh Ymin`, `Xh`, `Xmin`, and plain minutes; the parser fills `ProgressRaw`, sets `Progress` for an integer, and adds the invalid-progress note only when the value is neither an integer nor a playtime.

- [ ] **Step 1: Write the failing test**

```go
func TestParsePlaytime(t *testing.T) {
    for _, c := range []struct{ in string; sec int; ok bool }{
        {"0min", 0, true}, {"1min", 60, true}, {"16min", 960, true},
        {"1h 16min", 4560, true}, {"2h", 7200, true}, {"45", 2700, true},
        {"1h16min", 4560, true}, {"", 0, false}, {"abc", 0, false},
    } { ... }
}
```

Add a parser test: a game row with `progress=1h 16min` has `ProgressRaw == "1h 16min"`, `Progress == 0`, and no invalid-progress note; an anime row with `progress=12` still has `Progress == 12`; `progress=abc` still notes invalid.

- [ ] **Step 2: Run the test to verify it fails**

Run: `scripts/go.sh test -count=1 ./internal/plugins/yamtrack/`
Expected: FAIL — no `ProgressRaw`/`ParsePlaytime`.

- [ ] **Step 3: Implement**

Set `ProgressRaw` from the cell. Parse an integer as today. If not an integer, try `ParsePlaytime`; when it succeeds, leave `Progress` 0 without a note; otherwise add the existing note. Keep the function in the yamtrack package so the game importer can use it.

- [ ] **Step 4: Run the test to verify it passes**

Run: `scripts/go.sh test -count=1 ./internal/plugins/yamtrack/`
Expected: PASS.

- [ ] **Step 5: Full suite, vet, fmt, commit**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add internal/plugins/yamtrack
git commit -m "feat: parse raw playtime in Yamtrack exports"
```

---

### Task 7: Games manifest and kind

**Files:**
- Create: `internal/plugins/game/manifest.go`
- Create: `internal/plugins/game/manifest_test.go`

**Interfaces:**
- Consumes: `plugin` contract (Task 1).
- Produces: `game.PluginID = "game"`, `KindGame = "game"`, `SourceIGDB = "igdb"`, `SourceSteam = "steam"`, `ImporterYamtrack = "yamtrack-game"`, `ImporterSteam = "steam-account"`, `ExporterJSON = "json"`, `exportFormat = "allister.game.export"`, `manifest()`, `gameKind()`.

- [ ] **Step 1: Write the failing test**

Assert `plugin.Validate(manifest())`; the Game kind fields (year, released_on, platforms, developer, publisher, time_main/time_extra/time_complete, summary), statuses, `playtime` progress (Defaults `[playtime]`, combinations `[[playtime]]`, no primary/total), 7 sections (no People), titles `en`/fallback `en`, columns; the two sources (IGDB with `KeyFields` client_id/client_secret + `ExternalIDTypes` igdb/steam; Steam single key + external id steam; both 60 rpm, kind game, no schedule); the two importers (Yamtrack IGDB; Steam account with SteamID64, ResolveVia igdb, fallback steam, UnmatchedNote); the JSON exporter.

- [ ] **Step 2: Run the test to verify it fails**

Run: `scripts/go.sh test -count=1 ./internal/plugins/game/`
Expected: FAIL — package/function undefined.

- [ ] **Step 3: Implement**

Write the constants, `num`, statuses, `gameKind()` and `manifest()` with the exact values from the spec. The sources/importers are data only; no `New` yet.

- [ ] **Step 4: Run the test to verify it passes**

Run: `scripts/go.sh test -count=1 ./internal/plugins/game/`
Expected: PASS.

- [ ] **Step 5: Full suite, vet, fmt, commit**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add internal/plugins/game/manifest.go internal/plugins/game/manifest_test.go
git commit -m "feat: games plugin manifest"
```

---

### Task 8: Game mapping

**Files:**
- Create: `internal/plugins/game/mapping.go`
- Create: `internal/plugins/game/mapping_test.go`

**Interfaces:**
- Consumes: `KindGame` (Task 7).
- Produces: `gameRecord` (source/id, titles, fields, cover/external URL, external ids, genres, tags, recommendations); `gameData(gameRecord) plugin.ItemData`; `gameRecommendations([]plugin.ItemSummary) []plugin.ItemSummary`; `gameTitles`, `cleanTerms`, `cleanTags`; caps title 300, text 20k, term 60, id 100, genres 50, tags 100, recs 20.

- [ ] **Step 1: Write the failing tests**

Fields (year, platforms, developer, publisher, time_main/time_extra/time_complete as `Num`), titles, external ids, cover, genres deduped, tags, recommendations cap/drop; the caps; empty values. The mapping duplicates the TV sanitising helpers (accepted; no shared package).

- [ ] **Step 2: Run the test to verify it fails**

Run: `scripts/go.sh test -count=1 ./internal/plugins/game/`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

Copy the TV mapping shape with game names and `KindGame`; no credits/people; preserve the three number fields; `gameRecommendations` caps and filters.

- [ ] **Step 4: Run the test to verify it passes**

Run: `scripts/go.sh test -count=1 ./internal/plugins/game/`
Expected: PASS.

- [ ] **Step 5: Full suite, vet, fmt, commit**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add internal/plugins/game/mapping.go internal/plugins/game/mapping_test.go
git commit -m "feat: game item mapping"
```

---

### Task 9: IGDB client and source

**Files:**
- Create: `internal/plugins/game/igdb.go`
- Create: `internal/plugins/game/igdb_test.go`

**Interfaces:**
- Consumes: `gameData`/`gameRecommendations` (Task 8), the `game` constants (Task 7), `plugin.ErrNotConfigured`.
- Produces: `type igdbKeyFunc func(context.Context) (string, error)`; `newIGDBClient(base, tokenURL string, clientID, clientSecret igdbKeyFunc, hc *http.Client) *igdbClient`; source `igdbSource{c *igdbClient}` implementing `plugin.Source` with search/details/people(nil)/recommendations/lookup (igdb id, slug, steam).

- [ ] **Step 1: Write the failing tests**

A fake token server + API server. Cover: token request query/headers; token caching (one token request for two calls); 401 refresh once then retry; 429/5xx/400/404 mapping; no secret/token in errors; search query/fields/limit; details query and mapping (name/summary/platforms/genres/themes/keywords/developer/publisher/cover/external games/time-to-beat); time-to-beat seconds -> minutes and a 500 time-to-beat still returns the game with empty time fields; external_games steam lookup -> details; slug lookup returns the numeric ID; people nil; recommendations.

- [ ] **Step 2: Run the test to verify it fails**

Run: `scripts/go.sh test -count=1 -run 'IGDB' ./internal/plugins/game/`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

`igdbClient` with `mu`, `token`, `tokenClientID`, `expires`. `Get`/`Post` resolves the client ID/secret (empty -> `ErrNotConfigured`; resolver errors -> `ErrUnavailable`), fetches/reuses the token, sends `Client-ID`/`Authorization`/body, maps statuses, caps the body at 8 MiB, decodes JSON. `Details` fetches the game + time-to-beat (best-effort) + maps to `gameRecord`; `Lookup` handles numeric id, slug, and steam (external_games). `Search` maps summaries. `People` returns nil. `Recommendations` maps `similar_games`. Use the cover URL normalization and the game caps.

- [ ] **Step 4: Run the test to verify it passes**

Run: `scripts/go.sh test -count=1 ./internal/plugins/game/`
Expected: PASS.

- [ ] **Step 5: Full suite, vet, fmt, commit**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add internal/plugins/game/igdb.go internal/plugins/game/igdb_test.go
git commit -m "feat: IGDB game source"
```

---

### Task 10: Steam client and source

**Files:**
- Create: `internal/plugins/game/steam.go`
- Create: `internal/plugins/game/steam_test.go`

**Interfaces:**
- Consumes: `gameData` (Task 8), `game` constants (Task 7), `plugin.ErrNotConfigured`.
- Produces: `newSteamClient(apiBase, storeBase string, key func(context.Context) (string, error), hc *http.Client) *steamClient` with `ownedGames(ctx, steamID)`, `search(ctx,q)`, `details(ctx,appid)`; `steamSource{c *steamClient}` implementing `plugin.Source` (search/details/people(nil)/recommendations(nil)/lookup); a `steamGame` record.

- [ ] **Step 1: Write the failing tests**

Owned games query/response (appid/name/playtime/minutes, empty/private); store search; store details (success/type game/non-game/not found/release date layouts/HTML description); key gating (empty -> ErrNotConfigured, no request; 401/403 -> ErrNotConfigured; no key in errors); 429/5xx mapping; search/details mapping; people/recommendations nil.

- [ ] **Step 2: Run the test to verify it fails**

Run: `scripts/go.sh test -count=1 -run 'Steam' ./internal/plugins/game/`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

The client resolves the key per request; `ownedGames` sends the key in the query and never wraps transport errors; the store calls are keyless but the source checks the key presence first. Map the store JSON into `gameRecord`; `type != game` -> `ErrNotFound`; parse the release date; strip HTML with the shared plain-text helper; `People`/`Recommendations` nil.

- [ ] **Step 4: Run the test to verify it passes**

Run: `scripts/go.sh test -count=1 ./internal/plugins/game/`
Expected: PASS.

- [ ] **Step 5: Full suite, vet, fmt, commit**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add internal/plugins/game/steam.go internal/plugins/game/steam_test.go
git commit -m "feat: Steam game source"
```

---

### Task 11: Steam account importer

**Files:**
- Create: `internal/plugins/game/steam_account.go`
- Create: `internal/plugins/game/steam_account_test.go`

**Interfaces:**
- Consumes: `steamClient.ownedGames` (Task 10), the manifest importer (Task 7).
- Produces: `steamAccountImporter{c *steamClient}` implementing `plugin.AccountImporter`; `ImportAccount` validates the 17-digit SteamID64, fetches the library, and returns one `ParsedEntry` per game with `ExternalIDs{"steam": appid}`, `Progress{"playtime": minutes*60}`, status Planned for 0 else Active, and a Steam header cover; a missing/refused Steam key becomes `plugin.ErrBadInput` with the actionable "Steam Web API key" message.

- [ ] **Step 1: Write the failing test**

- Invalid/empty/non-17-digit SteamID64 -> `ErrBadInput` with a readable reason.
- Keyless -> `ErrBadInput` containing "Steam Web API key" and "Settings > Plugins" (not `ErrNotConfigured`).
- Successful library -> entries with appid, playtime seconds, statuses, cover, title; a `0`-playtime game is Planned, a played one Active.
- Private/empty response -> `ErrBadInput` with "make your profile and game details public".
- `Parse` returns `ErrBadInput` ("takes a SteamID64, not a file").

- [ ] **Step 2: Run the test to verify it fails**

Run: `scripts/go.sh test -count=1 -run 'SteamAccount' ./internal/plugins/game/`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

The importer; map the client's `ErrNotConfigured`/private errors to `ErrBadInput` messages; the entries. The manifest's `ResolveVia: igdb`, fallback `steam`; the importer does not resolve (the runner does).

- [ ] **Step 4: Run the test to verify it passes**

Run: `scripts/go.sh test -count=1 ./internal/plugins/game/`
Expected: PASS.

- [ ] **Step 5: Full suite, vet, fmt, commit**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add internal/plugins/game/steam_account.go internal/plugins/game/steam_account_test.go
git commit -m "feat: Steam account import"
```

---

### Task 12: Yamtrack game import

**Files:**
- Create: `internal/plugins/game/yamtrack.go`
- Create: `internal/plugins/game/yamtrack_test.go`

**Interfaces:**
- Consumes: `yamtrack.ParseRaw`/`ParsePlaytime` (Task 6), the manifest importer (Task 7).
- Produces: `yamtrackGameImporter{}`; group by `(source, media_id)` or `(source, title)`; keep only `media_type == game`; map status/score/dates/comment/playtime; IGDB external id only for `source == "igdb"` + numeric id; drop the exact "Imported from Steam" note; skip duplicates/invalid status/title-less rows.

- [ ] **Step 1: Write the failing test**

A synthetic CSV with: a Completed IGDB game with `8.0`, dates and `1h 16min`; a Paused `16min`; a Planning `0min`; a duplicate; a manual/no-id game; a non-IGDB numeric id (no external id + note); a blank/unknown status; a game row; a TV row (ignored); a no-title+no-id row; an IGDB id with no title (`IGDB #<id>`). Assert entries/skipped/ignored, playtime seconds, statuses, score, dates, notes, external ids.

- [ ] **Step 2: Run the test to verify it fails**

Run: `scripts/go.sh test -count=1 -run 'YamtrackGame' ./internal/plugins/game/`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

The importer; use `yamtrack.ParsePlaytime(row.ProgressRaw)`; ignore the exact "Imported from Steam" note; the manifest's `ResolveVia: igdb`.

- [ ] **Step 4: Run the test to verify it passes**

Run: `scripts/go.sh test -count=1 ./internal/plugins/game/`
Expected: PASS.

- [ ] **Step 5: Local-only owner export check**

Run a temporary test/program against `yamtrack_2026-11-09 15_33_37.419384+01_00.csv` (never commit it) and confirm 1,019 entries, all IGDB ids, the status split, and playtime parsing. Remove the temporary file.

- [ ] **Step 6: Full suite, vet, fmt, commit**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add internal/plugins/game/yamtrack.go internal/plugins/game/yamtrack_test.go
git commit -m "feat: Yamtrack game import"
```

---

### Task 13: Game plugin wiring, config and registration

**Files:**
- Create: `internal/plugins/game/plugin.go`
- Create: `internal/plugins/game/plugin_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `cmd/allister/main.go`
- Modify: `docker-compose.yml`

**Interfaces:**
- Consumes: the IGDB/Steam clients/sources (Tasks 9/10), the importers (Tasks 11/12), `manifest()` (Task 7), `SourceKeyField` (Task 2), `web.Deps.SourceKeyField` (Task 4).
- Produces: `game.Options{HTTP, IGDBURL, IGDBTokenURL, SteamAPIURL, SteamStoreURL string; IGDBClientID, IGDBClientSecret, SteamKey func(context.Context) (string,error)}`; `game.New(opts) plugin.Plugin`; `config.Config` fields `IGDBClientID`, `IGDBClientSecret`, `SteamAPIKey`; the cmd credential resolver; the plugin registration.

- [ ] **Step 1: Write the failing tests**

- `New(Options{})` provides both sources, both importers, the exporter; the manifest validates; a second registration is refused.
- A mutable key provider: no key -> IGDB `Details` ErrNotConfigured; set the keys + a fake IGDB server -> data. The Steam path similarly.
- Config: `Load()` reads `IGDB_CLIENT_ID`, `IGDB_CLIENT_SECRET`, `STEAM_API_KEY`.
- The `cmd` resolver (extract/reuse the `resolveSourceKey` style helper or add a field-aware `resolveSourceKeyField`) tests stored beats env, empty falls back, error returned.

- [ ] **Step 2: Run the test to verify it fails**

Run: `scripts/go.sh test -count=1 ./internal/plugins/game/ ./internal/config/ ./cmd/allister/`
Expected: FAIL — `New`/Options/config fields undefined.

- [ ] **Step 3: Implement**

`plugin.go`: options, default HTTP client (20s), nil key funcs -> "", the IGDB/Steam clients, sources, importers, exporter. `config`: env fields. `cmd`: build the per-field credential resolver (fallback map `tmdb.key`, `igdb.client_id/client_secret`, `steam.key`), use it for TV/Movies TMDB, the game keys, and `web.Deps.SourceKeyField`/`SourceKey`; register Games after Movies. Compose: add the three env vars.

- [ ] **Step 4: Run the test to verify it passes**

Run: `scripts/go.sh test -count=1 ./internal/plugins/game/ ./internal/config/ ./cmd/allister/`
Expected: PASS.

- [ ] **Step 5: Full suite, vet, fmt, commit**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add internal/plugins/game/plugin.go internal/plugins/game/plugin_test.go internal/config cmd/allister/main.go docker-compose.yml
git commit -m "feat: register the games plugin"
```

---

### Task 14: Docs

**Files:**
- Modify: `README.md`
- Modify: `docs/usage.md`
- Modify: `docs/plugins.md`
- Modify: `docs/handoff.md`
- Modify: `AGENTS.md`

**Interfaces:**
- Consumes: everything above.
- Produces: user/author docs; README ≤150 lines.

- [ ] **Step 1: Update the docs**

- README: Games in the plugin paragraph; the three env vars / API keys card; remove games from the planned list; fold/trim to ≤150 lines.
- `docs/usage.md`: `/game`; IGDB/Steam; the SteamID64 account import; the multi-field API keys card and single-key compatibility; Playtime vs time-to-beat; the Yamtrack game import; no calendar; GOG/HLTB out; key/backup caveats.
- `docs/plugins.md`: `SourceInfo.KeyFields`/`KeyField`; settings `source.<id>.<field>`; the single-key compatibility; `ErrNotConfigured`; host-provided resolvers; the store endpoints unofficial.
- `docs/handoff.md`: games features; IGDB/Steam; multi-field card; time-to-beat; no GOG/HLTB; remove games from the not-built list.
- `AGENTS.md`: note named source credential fields; next migration 00017.

- [ ] **Step 2: Run the docs/full suite**

Run: `scripts/go.sh test -count=1 ./...`; `wc -l README.md` ≤150.
Expected: PASS.

- [ ] **Step 3: Vet, fmt, commit**

```bash
scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add README.md docs/usage.md docs/plugins.md docs/handoff.md AGENTS.md
git commit -m "docs: games plugin and named source credentials"
```

---

### Task 15: Preview and final verification

**Files:** none (verification only).

- [ ] **Step 1: Rebuild the preview**

```bash
PORT=18083 docker compose -p allister-preview up -d --build
curl -sS http://localhost:18083/healthz
```

- [ ] **Step 2: Enable Games and inspect the cards**

```bash
curl -sS -o /dev/null -w '%{http_code}\n' -X POST -H 'Sec-Fetch-Site: same-origin' -d 'enabled=1' http://localhost:18083/settings/plugins/game
curl -sS http://localhost:18083/settings/plugins | grep -o 'IGDB\|Steam\|Client ID\|Client Secret\|API key' | sort -u
```

Expected: IGDB has two fields, Steam one; no stored values.

- [ ] **Step 3: Check the no-key messages and manual playtime**

`GET /game/search?q=test` with no keys: 503 with the key message; a manual game with Playtime `12:34:56` saves and displays; the Game list shows the playtime.

- [ ] **Step 4: Optional live import**

If the owner enters the Steam key + SteamID64 in the preview, run a small Steam account import and check the playtime/statuses; if IGDB credentials are entered, check the details + time-to-beat. Otherwise rely on the fixture tests and record that live keys were not available.

- [ ] **Step 5: Export/calendar/regression**

`/game/export/json`; no game calendar events; TV/Movies/TMDB paths still work; the multi-field card remove/restore works.

- [ ] **Step 6: Final suite, vet, fmt**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git status --short
```

Expected: 25+ packages pass; vet/fmt clean; working tree clean. Commit preview fixes if any, then report.

# Movies Plugin and Source API Keys Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the opt-in Movies plugin (one Movie kind, TMDB source, Yamtrack movie import, JSON export) and a web UI for source API keys, shared by TV and Movies.

**Architecture:** `SourceInfo` gains optional key labels; `settings` stores `source.<id>.key`; the TMDB client resolves the key per request, so the UI takes effect without a restart. A shared `internal/plugins/tmdb` client serves TV and Movies. The Movie kind is one flat entry with an optional position; TMDB never supplies calendar events.

**Tech Stack:** Go via `scripts/go.sh` (Docker, CGO off), SQLite, `net/http`, `html/template`, htmx, Tailwind, existing plugin/library/importer/web layers.

**Spec:** `docs/superpowers/specs/2026-11-11-movies-plugin-design.md`

## Global Constraints

- No new Go dependencies; HTTP via the standard library.
- No database migration; the `settings` table exists. AGENTS.md's next migration is **00017** (fix the stale 00014 line in docs).
- Plugin contract data only: no plugin HTML/JS; the core guard; no hard-coded colours.
- The API key is never logged, rendered, exported or backed up, and never included in an error (including request URLs). Tests assert this.
- TMDB: 30 requests/minute, 8 MiB response cap, v3 `api_key` query or v4 bearer JWT (`eyJ` prefix).
- `ErrNotConfigured` is not retryable. Missing/refused keys must not create retry loops.
- README hard cap 150 lines.
- Preview only on port 18083 / project `allister-preview`; the owner's 18080-18082 containers stay untouched. The owner pushes.
- Run the full suite, `vet` and `fmt` before each commit; output must be pristine.

## Review Focus

1. **Missing/refused key during sync** — stored credits must survive, the item must end `done`, and `SweepSync` must not requeue it forever. (Task 5)
2. **Keyless Yamtrack import** — rows must become manual items with their TMDB ids and a truthful note, not failed rows or a misleading “no match”. (Tasks 6, 12)
3. **Pasted key variants** — surrounding whitespace is trimmed, a v4 JWT goes in `Authorization`, a v3 key in `api_key`, and the key never appears in HTML or errors. (Tasks 3, 7)
4. **Key removed after items exist** — saved items still render; live search/overview/add/match explain the setting with 503, and recommendations explain it with 200, never a 500. (Task 8)
5. **Two plugins, one keyed source** — the API keys card lists TMDB once; the shared key is used by TV and Movies; an unknown source id cannot be written. (Task 7)

---

### Task 1: Source credential contract and error sentinel

**Files:**
- Modify: `internal/plugin/contract.go`
- Modify: `internal/plugin/interfaces.go`
- Modify: `internal/plugin/validate.go`
- Modify: `internal/plugin/contract_json_test.go`
- Modify: `internal/plugin/validate_test.go`
- Modify: `internal/plugin/plugintest/plugintest.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `plugin.SourceInfo.KeyLabel string` (`key_label,omitempty`), `plugin.SourceInfo.KeyHelp string` (`key_help,omitempty`), `plugin.ErrNotConfigured error`; validation limits 40/200 runes.

- [ ] **Step 1: Write the failing tests**

Add `KeyLabel: "API key", KeyHelp: "Get one at example.test."` to the `full` manifest's `Sources[0]` in `contract_json_test.go`, and to the TV fixture's `Sources[0]` in `plugintest.go`.

Add these cases to `TestValidateRejects` in `validate_test.go`:

```go
{"key help without label", tv, func(m *plugin.Manifest) {
    m.Sources[0].KeyLabel = ""
    m.Sources[0].KeyHelp = "Get one at example.test."
}, `sources[0].key_help: requires key_label`},
{"key label with newline", tv, func(m *plugin.Manifest) {
    m.Sources[0].KeyLabel = "API\nkey"
}, `sources[0].key_label: must be a single line without control characters`},
{"key label too long", tv, func(m *plugin.Manifest) {
    m.Sources[0].KeyLabel = strings.Repeat("k", 41)
}, `sources[0].key_label: longer than 40 characters`},
{"key help too long", tv, func(m *plugin.Manifest) {
    m.Sources[0].KeyLabel = "API key"
    m.Sources[0].KeyHelp = strings.Repeat("h", 201)
}, `sources[0].key_help: longer than 200 characters`},
```

Add a test that the sentinel is distinct and wraps nothing:

```go
func TestErrNotConfigured(t *testing.T) {
    if plugin.ErrNotConfigured == nil || errors.Is(plugin.ErrNotConfigured, plugin.ErrNotFound) {
        t.Fatal("bad sentinel")
    }
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `scripts/go.sh test -count=1 ./internal/plugin/`
Expected: FAIL — `unknown field KeyLabel`, `undefined: plugin.ErrNotConfigured`, and the validation cases missing.

- [ ] **Step 3: Implement**

In `contract.go` add to `SourceInfo`:

```go
// KeyLabel, when set, means the source needs a credential the user stores
// in Settings; the core shows this label ("API key", "Access token").
KeyLabel string `json:"key_label,omitempty"`
// KeyHelp is one optional sentence telling the user where to get it.
KeyHelp string `json:"key_help,omitempty"`
```

In `interfaces.go` add:

```go
// ErrNotConfigured: the source cannot answer because the credential it
// needs is missing or was refused.
ErrNotConfigured = errors.New("plugin: source is not configured")
```

In `validate.go` add constants and source checks (single line, no control characters, lengths; `KeyHelp` requires `KeyLabel`). Use the existing `isControl` helper.

- [ ] **Step 4: Run the test to verify it passes**

Run: `scripts/go.sh test -count=1 ./internal/plugin/`
Expected: PASS.

- [ ] **Step 5: Full suite, vet, fmt, commit**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add internal/plugin
git commit -m "feat: declare source credentials in the contract"
```

---

### Task 2: Settings storage for source keys

**Files:**
- Modify: `internal/settings/settings.go`
- Modify: `internal/settings/settings_test.go`

**Interfaces:**
- Consumes: `settings.Store.Get/Set` (existing).
- Produces: `func (s *Store) SourceKey(ctx context.Context, sourceID string) (string, error)`, `SetSourceKey(ctx, sourceID, key string) error`, `ClearSourceKey(ctx, sourceID string) error`; key format `source.<id>.key`.

- [ ] **Step 1: Write the failing test**

```go
func TestSourceKeyRoundTrip(t *testing.T) {
    s, _ := newStore(t)
    ctx := context.Background()
    if got, err := s.SourceKey(ctx, "tmdb"); got != "" || err != nil {
        t.Fatalf("unset: %q %v", got, err)
    }
    if err := s.SetSourceKey(ctx, "tmdb", "  secret-key  "); err != nil {
        t.Fatal(err)
    }
    if v, ok, _ := s.Get(ctx, "source.tmdb.key"); !ok || v != "secret-key" {
        t.Fatalf("stored %q %v", v, ok)
    }
    if got, _ := s.SourceKey(ctx, "tmdb"); got != "secret-key" {
        t.Fatalf("got %q", got)
    }
    if got, _ := s.SourceKey(ctx, "other"); got != "" {
        t.Fatalf("source ids share a key: %q", got)
    }
    if err := s.ClearSourceKey(ctx, "tmdb"); err != nil {
        t.Fatal(err)
    }
    if got, _ := s.SourceKey(ctx, "tmdb"); got != "" {
        t.Fatalf("after clear: %q", got)
    }
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `scripts/go.sh test -count=1 ./internal/settings/`
Expected: FAIL — `s.SourceKey undefined`.

- [ ] **Step 3: Implement**

Add the three methods; `SetSourceKey` trims with `strings.TrimSpace`; `ClearSourceKey` deletes the row. `SourceKey` returns the stored value and the error (no logging/swallowing).

- [ ] **Step 4: Run the test to verify it passes**

Run: `scripts/go.sh test -count=1 ./internal/settings/`
Expected: PASS.

- [ ] **Step 5: Full suite, vet, fmt, commit**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add internal/settings
git commit -m "feat: store source API keys in settings"
```

---

### Task 3: Shared TMDB client

**Files:**
- Create: `internal/plugins/tmdb/client.go`
- Create: `internal/plugins/tmdb/client_test.go`

**Interfaces:**
- Consumes: `plugin.ErrNotFound`, `ErrRateLimited`, `ErrUnavailable`, `ErrBadResponse`, `ErrNotConfigured`.
- Produces:
  - `type KeyFunc func(context.Context) (string, error)`
  - `func New(base string, key KeyFunc, hc *http.Client) *Client`
  - `func (c *Client) Get(ctx context.Context, path string, values url.Values, out any) error`
  - `func (c *Client) Find(ctx context.Context, externalID, source string) (FindResult, error)`
  - `type FindItem struct { ID int }`
  - `type FindResult struct { MovieResults, TVResults []FindItem }`
  - `func Image(path, size string) string`
  - `const DefaultBase = "https://api.themoviedb.org/3"`, `const MaxBody = 8 << 20`

- [ ] **Step 1: Write the failing tests**

Cover:

```go
func TestGetSendsV3KeyAndV4Bearer(t *testing.T) // query api_key for "abc"; Authorization: Bearer for "eyJ..."
func TestGetWithoutKeyNeedsConfiguration(t *testing.T) // key func "" -> ErrNotConfigured, zero requests
func TestGetKeyResolverErrorIsUnavailable(t *testing.T) // key func error -> ErrUnavailable, no request
func TestGetMapsStatuses(t *testing.T) // 401/403 -> ErrNotConfigured; 404; 429; 500; 400 -> ErrBadResponse
func TestGetDoesNotLeakKey(t *testing.T) // 500 body/err must not contain "secret"
func TestFindReturnsMovieAndTVIds(t *testing.T)
func TestGetBodyLimit(t *testing.T) // a body over MaxBody is ErrBadResponse
func TestImage(t *testing.T) // "" -> ""; "/p.jpg" + "w500" -> https://image.tmdb.org/t/p/w500/p.jpg
```

Use an `httptest.Server` that records the request count, query and `Authorization` header.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `scripts/go.sh test -count=1 ./internal/plugins/tmdb/`
Expected: FAIL — package/type/function undefined.

- [ ] **Step 3: Implement**

`Get` resolves the key before building the request; a nil `KeyFunc` or one returning `""` becomes `ErrNotConfigured`; a resolver error becomes `ErrUnavailable`. Clone `values`, then either set `api_key` or the bearer header. Set `Accept: application/json` and a descriptive `User-Agent`. Do not wrap `hc.Do`'s error text (it can include the URL): return `ctx.Err()` or `ErrUnavailable`. Map statuses exactly as in the spec; cap the body at 8 MiB; decode JSON into `out`.

`Find` calls `/find/{escaped id}` with `external_source` and returns the two result lists. `Image` joins the base with `size` and the path (default size `w500`).

- [ ] **Step 4: Run the test to verify it passes**

Run: `scripts/go.sh test -count=1 ./internal/plugins/tmdb/`
Expected: PASS.

- [ ] **Step 5: Full suite, vet, fmt, commit**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add internal/plugins/tmdb
git commit -m "feat: shared TMDB client"
```

---

### Task 4: TV uses the shared client and always declares TMDB

**Files:**
- Modify: `internal/plugins/tv/plugin.go`
- Modify: `internal/plugins/tv/manifest.go`
- Modify: `internal/plugins/tv/tmdb.go`
- Modify: `internal/plugins/tv/manifest_test.go`
- Modify: `internal/plugins/tv/plugin_test.go`
- Modify: `internal/plugins/tv/tmdb_test.go`
- Modify: `cmd/allister/main.go` (temporary env key func, replaced in Task 13)

**Interfaces:**
- Consumes: `tmdb.New`, `tmdb.KeyFunc`, `tmdb.Client.Get/Find/Image` (Task 3).
- Produces: `tv.Options.TMDBKey tmdb.KeyFunc`; `tv.New` always provides `SourceTMDB`; `manifest()` has no key parameter and always declares TMDB with `KeyLabel`/`KeyHelp`; the Yamtrack importer always sets `ExternalIDType: "tmdb"`, `ExternalIDLabel: "TMDB"`, `ResolveVia: "tmdb"`, and `UnmatchedNote`.

- [ ] **Step 1: Write the failing tests**

Update `manifest_test.go`:

```go
func TestManifestAlwaysDeclaresTMDB(t *testing.T) {
    m := New(Options{}).Manifest()
    if err := plugin.Validate(m); err != nil { t.Fatal(err) }
    if len(m.Sources) != 2 || m.Sources[1].ID != SourceTMDB || m.Sources[1].KeyLabel != "API key" || m.Sources[1].SupportsSchedule {
        t.Fatalf("sources = %+v", m.Sources)
    }
    if m.Importers[0].ResolveVia != SourceTMDB || m.Importers[0].ExternalIDType != "tmdb" || m.Importers[0].UnmatchedNote == "" {
        t.Fatalf("importer = %+v", m.Importers[0])
    }
    if got := New(Options{TMDBKey: func(context.Context) (string, error) { return "k", nil }}).Manifest(); !reflect.DeepEqual(got, m) {
        t.Fatalf("the key must not change the manifest")
    }
}
```

Update `plugin_test.go`: `New(Options{})` provides both sources and `New(Options{TMDBKey: func(context.Context) (string, error) { return "k", nil }})` does too; a no-key `Details` on `SourceTMDB` returns `ErrNotConfigured`. Update `tmdb_test.go` to construct the source with `&tmdbSource{c: tmdb.New(srv.URL, keyFunc("key"), srv.Client())}`; the 401 expectation becomes `plugin.ErrNotConfigured`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `scripts/go.sh test -count=1 ./internal/plugins/tv/`
Expected: FAIL — `Options.TMDBKey` is not a function, the no-key manifest has one source, and the 401 maps to `ErrBadResponse`.

- [ ] **Step 3: Implement**

Change the options, always build the source (nil `TMDBKey` becomes a func returning `""`), use `*tmdb.Client` in `tmdbSource`, and call `c.Get`/`c.Find`. Rewrite `manifest()` without the key conditional (remove the duplicate `ExternalIDType`/`UnmatchedNote` assignments). Set `KeyLabel: "API key"` and `KeyHelp: "Create a free key at themoviedb.org under Settings → API (v3 key or v4 read token)."`.

Keep `cmd/allister` compiling for now: replace `TMDBKey: cfg.TMDBKey` in the `tv.New` call with `TMDBKey: func(context.Context) (string, error) { return cfg.TMDBKey, nil }`. Task 13 replaces it with the settings-aware resolver.

- [ ] **Step 4: Run the test to verify it passes**

Run: `scripts/go.sh test -count=1 ./internal/plugins/tv/`
Expected: PASS.

- [ ] **Step 5: Full suite, vet, fmt, commit**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add internal/plugins/tv cmd/allister/main.go
git commit -m "refactor: TV uses the shared TMDB client"
```

---

### Task 5: Sync job handles a missing credential

**Files:**
- Modify: `internal/jobs/sync_item.go`
- Modify: `internal/jobs/sync_item_test.go`

**Interfaces:**
- Consumes: `plugin.ErrNotConfigured` (Task 1).
- Produces: on `Source.People` returning `ErrNotConfigured`, the sync keeps stored credits, skips the people-image loop, refreshes the schedule, and sets `sync_state = "done"`; no `ErrRetryLater`/`partial`.

- [ ] **Step 1: Write the failing test**

```go
func TestSyncItemNotConfiguredKeepsCreditsAndFinishes(t *testing.T) {
    e := newSyncEnv(t)
    id := e.save(t, "tvdata", "")
    ctx := context.Background()
    if err := e.lib.SaveCredits(ctx, id, []plugin.Credit{{Role: "actor", Person: plugin.PersonRef{ID: "p1", Name: "P1"}}}); err != nil {
        t.Fatal(err)
    }
    e.src.Errs = map[string]error{plugintest.OpPeople: plugin.ErrNotConfigured}
    if err := SyncItem(e.deps)(ctx, payload(id, "tv")); err != nil {
        t.Fatal(err)
    }
    it, err := e.lib.Item(ctx, id)
    if err != nil || it.SyncState != "done" {
        t.Fatalf("item = %+v, %v", it, err)
    }
    people, err := e.lib.ItemPeople(ctx, id)
    if err != nil || len(people) != 1 {
        t.Fatalf("credits = %+v, %v", people, err)
    }
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `scripts/go.sh test -count=1 -run TestSyncItemNotConfigured ./internal/jobs/`
Expected: FAIL — the handler returns an error and the item is `partial`.

- [ ] **Step 3: Implement**

In the `People` switch add `case errors.Is(err, plugin.ErrNotConfigured):` setting a `skipCredits` flag (keep `credits` untouched). Wrap `SaveCredits`, `ItemPeople` and the image loop in `if !skipCredits { ... }`. The schedule refresh and the final `done` stay outside. Do not log the key.

- [ ] **Step 4: Run the test to verify it passes**

Run: `scripts/go.sh test -count=1 -run TestSyncItemNotConfigured ./internal/jobs/`
Expected: PASS.

- [ ] **Step 5: Full suite, vet, fmt, commit**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add internal/jobs
git commit -m "fix: finish sync when a source needs configuration"
```

---

### Task 6: Importer treats a missing credential as unmatched

**Files:**
- Modify: `internal/importer/runner.go`
- Modify: `internal/importer/runner_test.go`

**Interfaces:**
- Consumes: `plugin.ErrNotConfigured` (Task 1).
- Produces: `Lookup` returning `ErrNotConfigured` breaks the source loop like `ErrNotFound`; the unmatched reason is `"not configured: <source names>"` when the last miss was `ErrNotConfigured`, otherwise the existing `"not found on <source names>"`.

- [ ] **Step 1: Write the failing test**

```go
func TestRunnerUnmatchedWhenSourceNeedsConfiguration(t *testing.T) {
    e := newGenEnv(t)
    e.bookSrc.Fn = func(context.Context, string, string, string, int) (plugin.ItemData, error) {
        return plugin.ItemData{}, plugin.ErrNotConfigured
    }
    e.bookImp.Result = plugin.ParseResult{Entries: []plugin.ParsedEntry{bookEntry("1", "One", plugin.Planned, 0)}}
    id := e.submit("book", "books-csv")
    if err := e.run("book", id); err != nil {
        t.Fatal(err)
    }
    r := e.report()
    if r.Unmatched != 1 || r.UnmatchedRows[0].Message != "not configured: Fake Books" {
        t.Fatalf("report = %+v", r)
    }
    if it, _ := e.item("book", "book", "isbn", "1"); it.Source != "manual" {
        t.Fatalf("item = %+v", it)
    }
}
```

Keep the existing `TestRunner...` case expecting `"not found on Fake Books"` for `ErrNotFound` green.

- [ ] **Step 2: Run the test to verify it fails**

Run: `scripts/go.sh test -count=1 -run 'Unmatched|NeedsConfiguration' ./internal/importer/`
Expected: FAIL — the row is recorded as failed/to-retry instead of unmatched.

- [ ] **Step 3: Implement**

Track `lastMiss error` in the lookup loop. On `ErrNotFound` or `ErrNotConfigured`, set it and break. After the loop, choose the reason:

```go
reason := "not found on " + ref.sourceNames()
if errors.Is(lastMiss, plugin.ErrNotConfigured) {
    reason = "not configured: " + ref.sourceNames()
}
return r.saveUnmatched(dctx, rc, reason)
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `scripts/go.sh test -count=1 ./internal/importer/`
Expected: PASS.

- [ ] **Step 5: Full suite, vet, fmt, commit**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add internal/importer
git commit -m "fix: import unmatched rows when a source needs configuration"
```

---

### Task 7: API keys card and source key form

**Files:**
- Modify: `internal/web/server.go` (Deps)
- Modify: `internal/web/settings.go`
- Modify: `internal/web/plugin_settings.go`
- Modify: `internal/web/templates/settings_plugins_page.html`
- Modify: `internal/web/templates/settings_plugin.html`
- Create: `internal/web/source_keys_test.go`

**Interfaces:**
- Consumes: `settings.SourceKey/SetSourceKey/ClearSourceKey` (Task 2); `plugin.SourceInfo.KeyLabel/KeyHelp` (Task 1).
- Produces: `web.Deps.SourceKey func(ctx context.Context, sourceID string) (string, error)`; route `POST /settings/plugins/{source}/key`; `pluginSettingsView.MissingKeys []string`; source key view data on the plugins page.

- [ ] **Step 1: Write the failing tests**

`source_keys_test.go` builds a server over a fixture plugin whose `Sources[0].KeyLabel = "API key"` and `KeyHelp = "Get one at example.test."`. Test:

```go
func TestSourceKeysCardSavesAndClearsWithoutEchoing(t *testing.T) {
    // GET /settings/plugins contains the source name, "API key", the help,
    // and action="/settings/plugins/tvdata/key"; it has name="key".
    // POST key=secret -> 303 /settings/plugins#source-keys; store is "secret".
    // GET no longer contains "secret"; contains "A key is configured." and name="remove".
    // POST remove=1 -> 303; store is empty; GET has no "remove".
}
func TestSourceKeyValidation(t *testing.T) // empty, "a b", 501 chars -> 400, nothing stored; unknown source -> 404
func TestSourceKeysCardHiddenWithoutKeyedSources(t *testing.T)
func TestSourceKeyFromEnvironmentShowsConfiguredWithoutRemove(t *testing.T)
func TestSourceKeysCardDeduplicatesSharedSource(t *testing.T) // two plugins declaring source id "tvdata" -> one form action
func TestPluginSettingsNoticeForMissingKey(t *testing.T) // GET /settings/plugins/tv contains the link to /settings/plugins#source-keys; after save it does not
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `scripts/go.sh test -count=1 -run 'SourceKey|MissingKey' ./internal/web/`
Expected: FAIL — the card and route are missing.

- [ ] **Step 3: Implement**

Add `Deps.SourceKey`. Add a server helper `sourceKey(ctx, id)` that uses the dep, else `Settings.SourceKey`, else `""`.

Build `sourceKeyView{ID, Name, Label, Help, Action, Configured, Stored}` by walking `Registry.Manifests()` and deduplicating by source ID; only sources with `KeyLabel != ""`. `Configured` uses the effective key; `Stored` uses `Settings.SourceKey`. Pass `SourceKeys` and an optional `KeyError` to `settings_plugins_page`.

`postSourceKey` validates the source is declared with a `KeyLabel` by some installed manifest; `remove=1` clears; otherwise trim and validate 1-500 printable ASCII (0x21-0x7E). On error re-render the page with `KeyError` and status 400; on success redirect 303 to `/settings/plugins#source-keys`.

Template: a `source-keys` section before the plugins list, rendered only when `SourceKeys` is non-empty. Each block shows Name, Label, Help; when configured, “A key is configured.”; a password input (`name="key"`, no value, `autocomplete="new-password"`, `spellcheck="false"`); Save; and Remove (`name="remove" value="1"`) only when `Stored`.

The card adds forms whose action ends with `/key`. If an existing test helper matches forms by the `/settings/plugins/` prefix (for example `columns_test.go`), tighten it to the plugin id so it cannot pick a key form.

`pluginSettingsData` adds `MissingKeys` (names of the plugin's sources with `KeyLabel` and no effective key). The plugin template shows a notice with `href="/settings/plugins#source-keys"`; when the plugin also has a source without `KeyLabel`, the notice adds that the other sources work without a key (TV's TVmaze).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `scripts/go.sh test -count=1 -run 'SourceKey|MissingKey' ./internal/web/`
Expected: PASS.

- [ ] **Step 5: Full suite, vet, fmt, commit**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add internal/web
git commit -m "feat: edit source API keys in the web UI"
```

---

### Task 8: Web explains a missing or refused credential

**Files:**
- Modify: `internal/web/item.go`
- Modify: `internal/web/add.go`
- Modify: `internal/web/match.go`
- Modify: `internal/web/recommendations.go`
- Modify: `internal/web/search.go`
- Modify: `internal/web/templates/recommendations.html`
- Create: `internal/web/source_errors_test.go`

**Interfaces:**
- Consumes: `plugin.ErrNotConfigured` (Task 1); `SourceInfo.KeyLabel` (Task 1); the source-key helper (Task 7).
- Produces: `sourceMessage` config text; 503 on search/overview/add/match; recommendations `recsView.Message`; source choice labels gain `" (needs key)"` when unconfigured.

- [ ] **Step 1: Write the failing tests**

Use a fixture source with `Errs` set per operation. Test:

```go
func TestSearchNeedsConfiguration(t *testing.T)      // 503 full page; 200 htmx partial with the same message
func TestLiveOverviewNeedsConfiguration(t *testing.T) // GET /book/books/1 -> 503, same message
func TestAddDialogNeedsConfiguration(t *testing.T)    // htmx GET /book/books/1/add -> 503, dialog keeps the form
func TestMatchNeedsConfiguration(t *testing.T)        // POST match -> 503, "Nothing was changed."
func TestRecommendationsNeedConfiguration(t *testing.T) // 200, message, no generic "unavailable"
func TestSourceChoiceLabelsNeedsKey(t *testing.T)     // two sources, one keyed/unconfigured -> "Fake X (needs key)"
func TestSavedItemStillRendersWithoutKey(t *testing.T) // saved book item, no key -> 200 from the database, no source call
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `scripts/go.sh test -count=1 -run 'NeedsConfiguration|NeedsKey' ./internal/web/`
Expected: FAIL — 500s/generic messages, no labels.

- [ ] **Step 3: Implement**

`sourceMessage`: on `ErrNotConfigured`, return `"<Source> needs a valid <key label>. Add or replace it in Settings > Plugins."` (`KeyLabel` lower-cased; neutral text without one).

Search: add `Status int` to `searchView`. In `runSearch`, on `ErrNotConfigured` set the message, the retry flag and `Status = http.StatusServiceUnavailable`; keep the existing 200-with-message behaviour for other errors. In `search`, render the full page with 503 when `Status != 0`; render the htmx partial with 200 and the same message (htmx does not swap 5xx outside the dialog slot). In `getMatchSearch`, if `v.Search.Status != 0`, pass it to `matchResponse` (which already uses `renderPartialStatus` for htmx).

`itemFail`: add `ErrNotConfigured` to the 503 branch (title “Not configured”, message, retry). `addFailure`: include it in the branch that re-renders the dialog with 503. `postMatch`: return 503 with the message plus “Nothing was changed.”.

Recommendations: add `Message string` to `recsView`; on `ErrNotConfigured`, set `Failed` and `Message`; the template renders `{{if .Message}}{{.Message}}{{else}}Recommendations are unavailable right now.{{end}}`.

Add `func (s *server) sourceChoiceLabel(ctx, si) string`: the name plus `" (needs key)"` when `si.KeyLabel != ""` and the effective key is empty. Use it for the search and match source choices (not headings or result badges).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `scripts/go.sh test -count=1 -run 'NeedsConfiguration|NeedsKey' ./internal/web/`
Expected: PASS.

- [ ] **Step 5: Full suite, vet, fmt, commit**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add internal/web
git commit -m "fix: explain missing source credentials in the web"
```

---

### Task 9: Movies manifest and kind

**Files:**
- Create: `internal/plugins/movie/manifest.go`
- Create: `internal/plugins/movie/manifest_test.go`

**Interfaces:**
- Consumes: `plugin` contract (Task 1).
- Produces: `movie.PluginID = "movie"`, `KindMovie = "movie"`, `SourceTMDB = "tmdb"`, `ImporterYamtrack = "yamtrack-movie"`, `ExporterJSON = "json"`, `exportFormat = "allister.movie.export"`, `manifest() plugin.Manifest`, `movieKind() plugin.KindDef`.

- [ ] **Step 1: Write the failing test**

```go
func TestManifest(t *testing.T) {
    m := manifest()
    if err := plugin.Validate(m); err != nil { t.Fatal(err) }
    if m.ID != PluginID || m.Name != "Movies" || m.DefaultEnabled || m.Icon != "movie" || len(m.Kinds) != 1 || m.Kinds[0].Key != KindMovie {
        t.Fatalf("%+v", m)
    }
    k := m.Kinds[0]
    if !k.Genres.Enabled || !k.Tags.Enabled || len(k.Sections) != 8 { t.Fatalf("%+v", k) }
    if !slices.Equal(k.Progress.Defaults, []string{"position"}) || k.Progress.PrimaryKey != "" {
        t.Fatalf("progress = %+v", k.Progress)
    }
    if len(m.Sources) != 1 || m.Sources[0].ID != SourceTMDB || m.Sources[0].KeyLabel == "" || m.Sources[0].SupportsSchedule {
        t.Fatalf("sources = %+v", m.Sources)
    }
    if m.Importers[0].ID != ImporterYamtrack || m.Importers[0].ResolveVia != SourceTMDB || !strings.Contains(m.Importers[0].UnmatchedNote, "could not resolve") {
        t.Fatalf("importer = %+v", m.Importers[0])
    }
    if len(m.Exporters) != 1 || m.Exporters[0].ID != ExporterJSON { t.Fatalf("exporters = %+v", m.Exporters) }
}
```

Also assert the fields/statuses/columns/roles by key (table-driven or direct lookups).

- [ ] **Step 2: Run the test to verify it fails**

Run: `scripts/go.sh test -count=1 ./internal/plugins/movie/`
Expected: FAIL — package/function undefined.

- [ ] **Step 3: Implement**

Write `manifest.go` with the constants, `num`, statuses, `movieKind()` (fields, terms, progress, sections, titles, columns), and `manifest()` (TMDB source with `KeyLabel: "API key"` and `KeyHelp: "Create a free key at themoviedb.org under Settings → API (v3 key or v4 read token)."`, the Yamtrack importer, JSON exporter).

- [ ] **Step 4: Run the test to verify it passes**

Run: `scripts/go.sh test -count=1 ./internal/plugins/movie/`
Expected: PASS.

- [ ] **Step 5: Full suite, vet, fmt, commit**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add internal/plugins/movie/manifest.go internal/plugins/movie/manifest_test.go
git commit -m "feat: movie plugin manifest"
```

---

### Task 10: Movie item mapping

**Files:**
- Create: `internal/plugins/movie/mapping.go`
- Create: `internal/plugins/movie/mapping_test.go`

**Interfaces:**
- Consumes: `KindMovie` (Task 9).
- Produces: `movieRecord` neutral source shape; `movieData(movieRecord) plugin.ItemData`; `movieCredits([]plugin.Credit) []plugin.Credit`; `movieTitles`, `movieRecommendations`, `cleanTerms`, `cleanTags`; caps title 300, text 20k, term 60, name 300, id 100, genres 50, tags 100, credits 500, recs 20.

- [ ] **Step 1: Write the failing tests**

```go
func TestMovieDataAndCredits(t *testing.T) // fields, titles, external ids, cover, genres deduped, tags, cast character/actor IDs, crew person IDs
func TestMovieCaps(t *testing.T) // long title/term/name/text, >50 genres, >100 tags, >500 credits, >20 recs
func TestMovieDropsEmptyRecommendations(t *testing.T)
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `scripts/go.sh test -count=1 ./internal/plugins/movie/`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

Copy the TV mapping shape with movie names (the packages stay independent). `movieData` maps `KindMovie`, sanitises fields (synopsis multiline), external ids, cover, terms. `movieCredits` cleans the capped credits. `movieRecommendations` caps and drops summaries without source/id/title.

- [ ] **Step 4: Run the test to verify it passes**

Run: `scripts/go.sh test -count=1 ./internal/plugins/movie/`
Expected: PASS.

- [ ] **Step 5: Full suite, vet, fmt, commit**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add internal/plugins/movie/mapping.go internal/plugins/movie/mapping_test.go
git commit -m "feat: movie item mapping"
```

---

### Task 11: TMDB movie source

**Files:**
- Create: `internal/plugins/movie/tmdb.go`
- Create: `internal/plugins/movie/tmdb_test.go`

**Interfaces:**
- Consumes: `tmdb.Client` (Task 3), `movieData`/`movieCredits`/`movieRecommendations` (Task 10), `KindMovie`/`SourceTMDB` (Task 9).
- Produces: `tmdbSource{c *tmdb.Client}` implementing `plugin.Source` for `KindMovie`; `tmdbMovie`/`tmdbCast`/`tmdbCrew`; search/details/people/recommendations/lookup; `tmdbURL`, `tmdbCover`, `tmdbProfile`.

- [ ] **Step 1: Write the failing tests**

A fake server serves `/search/movie`, `/movie/1`, `/find/tt1`. The details body includes title/original/overview/status/release_date/runtime/genres/production companies/countries/spoken languages/belongs_to_collection/poster/credits cast+crew/keywords/recommendations/external_ids. Test:

```go
func TestTMDBMovieSearchDetailsCreditsAndRecommendations(t *testing.T)
func TestTMDBMovieLookupTMDBAndIMDB(t *testing.T)
func TestTMDBMovieNeedsKey(t *testing.T) // empty key -> ErrNotConfigured, no request
func TestTMDBMovieKeyRefused(t *testing.T) // 401 -> ErrNotConfigured, no key in error
func TestTMDBMovieKeywordsBothShapes(t *testing.T) // keywords.keywords and keywords.results
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `scripts/go.sh test -count=1 -run TestTMDBMovie ./internal/plugins/movie/`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

Write the structs and source. Details calls `Get("/movie/"+id, append_to_response=credits,recommendations,external_ids,keywords)`. Map fields as the spec says (release status, country/language/studio/collection, director). Cast/crew mapping: cast `character-<credit_id>` + `person-<id>`, crew `person-<id>` with role director/writer. Lookup tmdb/imdb; unsupported → `ErrNotFound`. Recommendations from the results. Use `tmdb.Image` for covers/profiles.

- [ ] **Step 4: Run the test to verify it passes**

Run: `scripts/go.sh test -count=1 ./internal/plugins/movie/`
Expected: PASS.

- [ ] **Step 5: Full suite, vet, fmt, commit**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add internal/plugins/movie/tmdb.go internal/plugins/movie/tmdb_test.go
git commit -m "feat: TMDB movie source"
```

---

### Task 12: Yamtrack movie import

**Files:**
- Create: `internal/plugins/movie/yamtrack.go`
- Create: `internal/plugins/movie/yamtrack_test.go`

**Interfaces:**
- Consumes: `yamtrack.ParseRaw` (existing shared parser), `KindMovie`/`ImporterYamtrack` (Task 9).
- Produces: `yamtrackMovieImporter{}` implementing `plugin.Importer`; `Parse` groups movie rows by `(source, media_id)` or `(source, title)`.

- [ ] **Step 1: Write the failing test**

Use an inline CSV with the standard Yamtrack header and rows:

```text
101,tmdb,movie,The Movie,,,,,8.0,Completed,,2026-01-01 00:00:00+00:00,2026-02-01 00:00:00+00:00,1
102,tmdb,movie,Planned Movie,,,,,7.0,Planning,,,,1
103,manual,movie,Manual Movie,,,,,6.0,Completed,,,,1
104,tvdb,movie,Wrong Source,,,,,5.0,Completed,,,,1
105,tmdb,movie,Bad Status,,, ,,Sometimes,,,,1
101,tmdb,movie,The Movie,,,,,8.0,Completed,,,,1
2001,igdb,game,Game,,,,,Completed,,,,
```

Assert 4 entries (The Movie, Planned Movie, Manual Movie, Wrong Source), 1 ignored (game), and skipped reasons for the duplicate and the bad status. Assert The Movie has `ExternalIDs["tmdb"]="101"`, Completed, rating 8, dates, cover; Wrong Source has no external id and a note; a blank title + no id is skipped. A valid tmdb id with no title becomes `TMDB #<id>`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `scripts/go.sh test -count=1 -run TestYamtrackMovie ./internal/plugins/movie/`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

Parse raw rows; non-movie rows increment `Ignored`; group movies; dedupe; skip blank status; keep a TMDB external id only for `source == "tmdb"` and a numeric id; add a note for other sources/invalid ids; map status, rating, dates, comment, cover; no progress. Return `plugin.ParseResult`.

- [ ] **Step 4: Run the test to verify it passes**

Run: `scripts/go.sh test -count=1 ./internal/plugins/movie/`
Expected: PASS.

- [ ] **Step 5: Local-only owner export check**

Run a temporary test or small Go program against `yamtrack_2026-11-09 15_33_37.419384+01_00.csv` (do not commit it) to confirm 9 movie entries with the expected titles/scores. Remove the temporary file afterwards.

- [ ] **Step 6: Full suite, vet, fmt, commit**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add internal/plugins/movie/yamtrack.go internal/plugins/movie/yamtrack_test.go
git commit -m "feat: Yamtrack movie import"
```

---

### Task 13: Movie plugin wiring and registration

**Files:**
- Create: `internal/plugins/movie/plugin.go`
- Create: `internal/plugins/movie/plugin_test.go`
- Modify: `cmd/allister/main.go`

**Interfaces:**
- Consumes: `tmdb.KeyFunc`/`Client` (Task 3), `tmdbSource` (Task 11), `yamtrackMovieImporter` (Task 12), `manifest()` (Task 9), `web.Deps.SourceKey` (Task 7).
- Produces: `movie.Options{HTTP *http.Client; TMDBURL string; TMDBKey tmdb.KeyFunc}`; `movie.New(opts) plugin.Plugin`; the plugin registered in `cmd`; the key resolver passed to TV, Movies and the web.

- [ ] **Step 1: Write the failing tests**

```go
func TestNewWiresDeclaredParts(t *testing.T) {
    p := New(Options{})
    if p.Source(SourceTMDB) == nil || p.Importer(ImporterYamtrack) == nil || p.Exporter(ExporterJSON) == nil || p.Source("nope") != nil {
        t.Fatal("wiring")
    }
    if err := plugin.Validate(p.Manifest()); err != nil { t.Fatal(err) }
    reg := plugin.NewRegistry(plugintest.NewMemEnabled())
    if err := reg.Register(p); err != nil { t.Fatal(err) }
    if err := reg.Register(New(Options{})); err == nil { t.Fatal("duplicate registration") }
}
func TestNewKeyProviderIsLive(t *testing.T) // a mutable key variable: no key -> Details ErrNotConfigured; set -> fake server data
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `scripts/go.sh test -count=1 ./internal/plugins/movie/`
Expected: FAIL — `New` undefined.

- [ ] **Step 3: Implement**

Add `plugin.go` with the options, default HTTP client, key fallback (nil → empty), sources/importers/exporter maps. In `cmd/allister/main.go`:

```go
fallback := map[string]string{"tmdb": cfg.TMDBKey}
sourceKey := func(ctx context.Context, sourceID string) (string, error) {
    k, err := settingsStore.SourceKey(ctx, sourceID)
    if err != nil || k != "" {
        return k, err
    }
    return fallback[sourceID], nil
}
tmdbKey := func(ctx context.Context) (string, error) { return sourceKey(ctx, "tmdb") }
```

Pass `TMDBKey: tmdbKey` to `tv.New` (replacing Task 4's temporary env func) and `movie.New`; pass `SourceKey: sourceKey` to `web.Deps`; register `movie.New` after TV.

- [ ] **Step 4: Run the test to verify it passes**

Run: `scripts/go.sh test -count=1 ./internal/plugins/movie/ ./cmd/allister/`
Expected: PASS.

- [ ] **Step 5: Full suite, vet, fmt, commit**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add internal/plugins/movie/plugin.go internal/plugins/movie/plugin_test.go cmd/allister/main.go
git commit -m "feat: register the movies plugin"
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
- Produces: user and plugin-author documentation; README ≤150 lines.

- [ ] **Step 1: Write the failing docs check (where one exists)**

Run: `scripts/go.sh test -count=1 ./internal/guard/ ./internal/web/`
Expected: still PASS before the edits (the docs tests have no movie expectations yet). If a docs test asserts the old plugin list, it fails; update the test expectations only where they encode the new docs.

- [ ] **Step 2: Update the docs**

- README: add the Movies plugin (one Movie kind, TMDB, Yamtrack import, JSON export, no calendar); explain the API keys card and the `TMDB_API_KEY` fallback; remove “movies” from the planned list; keep ≤150 lines. The file is already at 150, so fold the Movies sentence into the existing plugin paragraph and trim an older sentence rather than exceeding the cap.
- `docs/usage.md`: `/movie` routes; the API keys card (Settings > Plugins); key save/replace/remove, no echo; keyless import → manual; v3/v4 credentials; no calendar; key not in backup/export.
- `docs/plugins.md`: `SourceInfo.KeyLabel`/`KeyHelp`; `ErrNotConfigured`; `source.<id>.key`; the host passes the key to the plugin, the manifest never holds it.
- `docs/handoff.md`: Movies features; API keys; shared TMDB client; TV always declares TMDB; remove movies from the not-built list.
- `AGENTS.md`: next migration 00017; the settings line mentions source keys.

- [ ] **Step 3: Run the docs tests and full suite**

Run: `scripts/go.sh test -count=1 ./...`
Expected: PASS; `wc -l README.md` ≤ 150.

- [ ] **Step 4: Vet, fmt, commit**

```bash
scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git add README.md docs/usage.md docs/plugins.md docs/handoff.md AGENTS.md
git commit -m "docs: movies plugin and source API keys"
```

---

### Task 15: Preview and final verification

**Files:** none (verification only).

- [ ] **Step 1: Rebuild the preview**

```bash
PORT=18083 docker compose -p allister-preview up -d --build
curl -sS http://localhost:18083/healthz
```

- [ ] **Step 2: Enable Movies and check the key card**

```bash
curl -sS -o /dev/null -w '%{http_code}\n' -X POST -H 'Sec-Fetch-Site: same-origin' -d 'enabled=1' http://localhost:18083/settings/plugins/movie
curl -sS http://localhost:18083/settings/plugins | grep -o 'API keys\|TMDB\|API key' | sort -u
```

- [ ] **Step 3: Check the no-key behaviour**

```bash
curl -sS -o /tmp/opencode/movie-search.html -w '%{http_code}\n' 'http://localhost:18083/movie/search?q=test&source=tmdb'
grep -o 'valid API key\|Settings > Plugins\|needs key' /tmp/opencode/movie-search.html | sort -u
```

Expected: 503 with the actionable message; no 500.

- [ ] **Step 4: Save a dummy key, see the immediate refusal, remove it**

Use the API keys form with a well-formed dummy key; search again; expect “valid API key” (TMDB 401), no restart needed. Remove the key.

- [ ] **Step 5: Import a small movie CSV without a key**

Use a 2-row synthetic CSV (one TMDB row, one manual row) through the Movies import section; check the manual item, status/score/date, the unmatched note, and the JSON export. Re-import under skip and verify it does not duplicate.

- [ ] **Step 6: Calendar and TV regression**

`/calendar.ics` still lists TV episodes and no movie events; TVmaze search still works; TV's TMDB source shows “(needs key)” without a key.

- [ ] **Step 7: Final suite, vet, fmt**

```bash
scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...
git status --short
```

Expected: 22+ packages pass; vet/fmt clean; working tree clean. If preview fixes were needed, commit them with a `fix:` message before the final run.

- [ ] **Step 8: Report**

Report the plugin, the API keys UI, the import result, the preview checks, and the remaining owner step (paste a real TMDB key and verify live search/details). The owner pushes.

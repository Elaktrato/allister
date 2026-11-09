package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anime/animedef"
	"github.com/allister/allister/internal/provider"
)

// The mapping between the legacy anime types and the generic library.
//
//	anime row            -> item (plugin "anime", kind "anime")
//	Type, Episodes, Year,
//	Studio, Synopsis,
//	SourceMaterial,
//	StartDate, EndDate   -> fields type, episodes, year, studio, synopsis,
//	                        source_material, started_on, ended_on
//	MALID                -> external id "mal" (canonical decimal form)
//	Relations            -> relations (Type in the related item's field "type")
//	cast member          -> credit: role "character", Detail = cast role
//	                        (MAIN ...), the voice actor as the counterpart
//	                        with role "Voice actor"
//	list entry Progress  -> progress key "episode"
//	Status watching,
//	plan_to_watch        -> core classes active, planned (paused, completed,
//	                        dropped are identical); the database stores the
//	                        core classes, the legacy strings exist only here.
//
// The anime plugin's manifest comes from animedef because the anime plugin
// package imports this package (through the legacy Yamtrack importer), so
// importing it here would be a cycle. The registry holds a manifest-only
// plugin: it never offers a source, importer or exporter.

const (
	animePlugin = animedef.PluginID
	animeKind   = animedef.KindAnime

	fieldType           = "type"
	fieldEpisodes       = "episodes"
	fieldYear           = "year"
	fieldStudio         = "studio"
	fieldSynopsis       = "synopsis"
	fieldSourceMaterial = "source_material"
	fieldStartedOn      = "started_on"
	fieldEndedOn        = "ended_on"

	extMAL          = "mal"
	progressEpisode = "episode"
	roleCharacter   = "character"
	roleVoiceActor  = "Voice actor"
)

// manifestPlugin is a plugin.Plugin that only supplies a manifest.
type manifestPlugin struct{ m plugin.Manifest }

func (p manifestPlugin) Manifest() plugin.Manifest     { return p.m }
func (manifestPlugin) Source(string) plugin.Source     { return nil }
func (manifestPlugin) Importer(string) plugin.Importer { return nil }
func (manifestPlugin) Exporter(string) plugin.Exporter { return nil }

// memEnabled is an in-memory EnabledStore that keeps nothing: every plugin
// has its manifest default (the library does not look at the flag anyway).
type memEnabled struct{}

func (memEnabled) IsEnabled(_ context.Context, _ string, def bool) (bool, error) { return def, nil }
func (memEnabled) SetEnabled(context.Context, string, bool) error                { return nil }

var (
	animeRegistry = newAnimeRegistry()
	// animeKindDef is the validated KindDef the library validates against.
	animeKindDef = mustKindDef()
	// validator runs the library's entry rules for ValidateListEntry, which
	// has no database (ValidateEntry never touches it).
	validator = library.New(nil, animeRegistry)
)

func newAnimeRegistry() *plugin.Registry {
	reg := plugin.NewRegistry(memEnabled{})
	if err := reg.Register(manifestPlugin{animedef.Manifest()}); err != nil {
		panic("store: anime manifest refused: " + err.Error())
	}
	return reg
}

func mustKindDef() plugin.KindDef {
	m, _ := animeRegistry.Manifest(animePlugin)
	for _, k := range m.Kinds {
		if k.Key == animeKind {
			return k
		}
	}
	panic("store: anime manifest has no anime kind")
}

// --- status ---

var toCore = map[Status]library.Status{
	Watching:    library.Active,
	PlanToWatch: library.Planned,
	Paused:      library.Paused,
	Completed:   library.Completed,
	Dropped:     library.Dropped,
}

func fromCore(s library.Status) Status {
	for legacy, core := range toCore {
		if core == s {
			return legacy
		}
	}
	return Status(s) // not one of the five classes: show it as stored
}

// --- errors ---

// mapErr translates library errors into this package's sentinels: ErrInvalid
// (keeping the reason), ErrAlreadySaved. Other errors pass through.
func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, library.ErrInvalid):
		return invalid("%s", strings.TrimPrefix(err.Error(), library.ErrInvalid.Error()+": "))
	case errors.Is(err, library.ErrAlreadySaved):
		return ErrAlreadySaved
	}
	return err
}

// ignoreMissing turns the library's "no such row" into nil: the legacy setters
// were plain UPDATEs that did nothing for an unknown id.
func ignoreMissing(err error) error {
	if errors.Is(err, library.ErrNoEntry) {
		return nil
	}
	return mapErr(err)
}

// --- anime <-> item ---

func pluginTitles(ts []provider.Title) []plugin.Title {
	var out []plugin.Title
	for _, t := range ts {
		out = append(out, plugin.Title{Lang: t.Lang, Text: t.Text})
	}
	return out
}

func providerTitles(ts []plugin.Title) []provider.Title {
	var out []provider.Title
	for _, t := range ts {
		out = append(out, provider.Title{Lang: t.Lang, Text: t.Text})
	}
	return out
}

func numValue(p *int) (plugin.FieldValue, bool) {
	if p == nil {
		return plugin.FieldValue{}, false
	}
	f := float64(*p)
	return plugin.FieldValue{Num: &f}, true
}

func dateValue(t *time.Time) (plugin.FieldValue, bool) {
	if t == nil {
		return plugin.FieldValue{}, false
	}
	d := *t
	return plugin.FieldValue{Date: &d}, true
}

// canonicalMAL returns the decimal form of a MAL id (the legacy schema stored
// an integer), or ok=false when id is not a number.
func canonicalMAL(id string) (string, bool) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return "", false
	}
	return strconv.FormatInt(n, 10), true
}

// relatedKind is the kind of a related item: "source" relations point at the
// source material (a manga), everything else at an anime (the anime plugin's
// convention).
func relatedKind(relation string) string {
	if relation == "source" {
		return "manga"
	}
	return animeKind
}

// itemData maps a legacy anime onto the plugin data the library stores. A
// non-numeric MAL id is rejected, as the legacy schema did.
func itemData(a provider.Anime) (plugin.ItemData, error) {
	d := plugin.ItemData{
		ItemSummary: plugin.ItemSummary{
			Source: string(a.Source), ID: a.ID, Kind: animeKind,
			Titles: pluginTitles(a.Titles), Fields: map[string]plugin.FieldValue{}, CoverURL: a.CoverURL,
		},
		ExternalURL: a.ExternalURL,
		Genres:      a.Genres,
	}
	if a.MALID != "" {
		mal, ok := canonicalMAL(a.MALID)
		if !ok {
			return d, invalid("MAL id %q is not a number", a.MALID)
		}
		d.ExternalIDs = map[string]string{extMAL: mal}
	}
	for k, v := range map[string]string{fieldType: a.Type, fieldStudio: a.Studio, fieldSynopsis: a.Synopsis, fieldSourceMaterial: a.SourceMaterial} {
		if v != "" {
			d.Fields[k] = plugin.FieldValue{Text: v}
		}
	}
	for k, p := range map[string]*int{fieldEpisodes: a.Episodes, fieldYear: a.Year} {
		if v, ok := numValue(p); ok {
			d.Fields[k] = v
		}
	}
	for k, t := range map[string]*time.Time{fieldStartedOn: a.StartDate, fieldEndedOn: a.EndDate} {
		if v, ok := dateValue(t); ok {
			d.Fields[k] = v
		}
	}
	for _, r := range a.Relations {
		item := plugin.ItemSummary{Source: string(r.Source), ID: r.ID, Kind: relatedKind(r.Kind), Titles: pluginTitles(r.Titles)}
		if r.Type != "" {
			item.Fields = map[string]plugin.FieldValue{fieldType: {Text: r.Type}}
		}
		d.Relations = append(d.Relations, plugin.Relation{Kind: r.Kind, Item: item})
	}
	for _, t := range a.Tags {
		d.Tags = append(d.Tags, plugin.Tag{Name: t.Name, Rank: t.Rank, Spoiler: t.Spoiler})
	}
	return d, nil
}

func textField(f map[string]plugin.FieldValue, key string) string { return f[key].Text }

func intField(f map[string]plugin.FieldValue, key string) *int {
	if v, ok := f[key]; ok && v.Num != nil {
		n := int(*v.Num)
		return &n
	}
	return nil
}

func dateField(f map[string]plugin.FieldValue, key string) *time.Time {
	if v, ok := f[key]; ok && v.Date != nil {
		d := *v.Date
		return &d
	}
	return nil
}

// saved maps a library item back to the legacy Saved (nil for nil or an item
// of another plugin). Titles is never nil; Recommendations stays empty.
func saved(it *library.Item) *Saved {
	if it == nil || it.PluginID != animePlugin || it.Kind != animeKind {
		return nil
	}
	sv := &Saved{ID: it.ID, CoverPath: it.CoverPath, SyncState: it.SyncState}
	sv.Source, sv.Anime.ID = provider.Source(it.Source), it.SourceID
	sv.Titles = providerTitles(it.Titles)
	if sv.Titles == nil {
		sv.Titles = []provider.Title{}
	}
	f := it.Fields
	sv.Type, sv.Studio, sv.Synopsis = textField(f, fieldType), textField(f, fieldStudio), textField(f, fieldSynopsis)
	sv.SourceMaterial = textField(f, fieldSourceMaterial)
	sv.Episodes, sv.Year = intField(f, fieldEpisodes), intField(f, fieldYear)
	sv.StartDate, sv.EndDate = dateField(f, fieldStartedOn), dateField(f, fieldEndedOn)
	sv.CoverURL, sv.ExternalURL, sv.MALID = it.CoverPath, it.ExternalURL, it.ExternalIDs[extMAL]
	for _, r := range it.Relations {
		sv.Relations = append(sv.Relations, provider.Relation{
			Kind: r.Kind, Source: provider.Source(r.Item.Source), ID: r.Item.ID,
			Titles: providerTitles(r.Item.Titles), Type: r.Item.Fields[fieldType].Text,
		})
	}
	sv.Genres = it.Genres
	for _, t := range it.Tags {
		sv.Tags = append(sv.Tags, provider.Tag{Name: t.Name, Rank: t.Rank, Spoiler: t.Spoiler})
	}
	return sv
}

// --- list entry <-> entry ---

func entry(e ListEntry) library.Entry {
	return library.Entry{
		ItemID: e.AnimeID, Status: toCore[e.Status], Rating: e.Rating, Comment: e.Comment,
		StartedOn: e.StartedOn, FinishedOn: e.FinishedOn,
		Progress: map[string]float64{progressEpisode: float64(e.Progress)},
	}
}

func listEntry(e library.Entry) ListEntry {
	return ListEntry{
		AnimeID: e.ItemID, Status: fromCore(e.Status), Rating: e.Rating, Progress: int(e.Progress[progressEpisode]),
		StartedOn: e.StartedOn, FinishedOn: e.FinishedOn, Comment: e.Comment,
	}
}

func listRow(r library.Row) ListRow {
	f := r.Item.Fields
	return ListRow{
		AnimeID: r.Item.ID, Titles: providerTitles(r.Item.Titles), CoverPath: r.Item.CoverPath,
		Type: textField(f, fieldType), Episodes: intField(f, fieldEpisodes), Entry: listEntry(r.Entry),
		SyncState: r.Item.SyncState, Source: provider.Source(r.Item.Source), SourceID: r.Item.SourceID,
		MALID: r.Item.ExternalIDs[extMAL],
	}
}

// animeItem is the not yet saved item that legacy entry validation checks
// against: the default active progress key and the episode total.
func animeItem(episodes *int) *library.Item {
	it := &library.Item{ProgressKeys: animeKindDef.Progress.Defaults}
	if v, ok := numValue(episodes); ok {
		it.Fields = map[string]plugin.FieldValue{fieldEpisodes: v}
	}
	return it
}

// legacyEntryError reports an entry the library rejected with the legacy
// message, chosen in the legacy order of checks; a reason the legacy rules did
// not know (for example a control character in the comment) keeps the
// library's message. The library decides validity, this only words it.
func legacyEntryError(e ListEntry, episodes *int, libErr error) error {
	switch {
	case e.Rating != nil && (*e.Rating < 0 || *e.Rating > 10):
		return invalid("rating must be between 0 and 10, got %d", *e.Rating)
	case e.Progress < 0:
		return invalid("progress must not be negative, got %d", e.Progress)
	case e.StartedOn != nil && e.FinishedOn != nil &&
		plugin.DateOnly(*e.FinishedOn).Before(plugin.DateOnly(*e.StartedOn)):
		return invalid("finish date is before start date")
	case episodes != nil && e.Progress > *episodes:
		return invalid("progress %d is above the episode total %d", e.Progress, *episodes)
	}
	return mapErr(libErr)
}

// --- people ---

func person(p library.Person) Person {
	return Person{ID: p.ID, Source: provider.Source(p.Source), SourceID: p.SourceID, Name: p.Name, ImagePath: p.ImagePath, ExternalURL: p.ExternalURL}
}

// personRef maps a provider person. The legacy store kept any URL and the web
// layer refused to link or load a bad one; the library refuses to store it,
// so a URL or image the library would reject is dropped here instead of
// failing the whole cast.
func personRef(p provider.Person) plugin.PersonRef {
	r := plugin.PersonRef{ID: p.ID, Name: p.Name, ImageURL: p.ImageURL, URL: p.URL}
	if !library.AcceptsURL(r.URL) {
		r.URL = ""
	}
	if !library.AcceptsImageRef(r.ImageURL) {
		r.ImageURL = ""
	}
	return r
}

func credits(cast []provider.CastMember) []plugin.Credit {
	out := make([]plugin.Credit, 0, len(cast))
	for _, m := range cast {
		c := plugin.Credit{Role: roleCharacter, Detail: m.Role, Person: personRef(m.Character)}
		if m.Actor != nil {
			a := personRef(*m.Actor)
			c.With, c.WithRole = &a, roleVoiceActor
		}
		out = append(out, c)
	}
	return out
}

func animeQuery(status *library.Status) library.Query {
	return library.Query{PluginID: animePlugin, Kind: animeKind, Status: status}
}

// wrapf maps err (see mapErr) and prefixes it with what, except for this
// package's sentinel errors, whose text callers show as is.
func wrapf(what string, err error) error {
	err = mapErr(err)
	if err == nil || errors.Is(err, ErrInvalid) || errors.Is(err, ErrAlreadySaved) {
		return err
	}
	return fmt.Errorf("%s: %w", what, err)
}

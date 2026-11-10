package anime

import "github.com/allister/allister/internal/plugin"

// Plugin, source, importer and exporter ids and the one kind key of the anime
// plugin.
const (
	PluginID   = "anime"
	KindAnime  = "anime"
	SourceID   = "anilist"
	ImporterID = "yamtrack"
	ExporterID = "json"
)

func num(f float64) *float64 { return &f }

// typeValues are the AniList formats of an anime, the enum of the type field.
var typeValues = []string{"TV", "TV_SHORT", "MOVIE", "SPECIAL", "OVA", "ONA", "MUSIC"}

// manifest returns the anime plugin's manifest (a fresh value on every call).
func manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:              PluginID,
		Name:            "Anime",
		Version:         "1.0.0",
		ContractVersion: plugin.ContractVersion,
		DefaultEnabled:  true,
		Icon:            "live_tv",
		Kinds: []plugin.KindDef{{
			Key:         KindAnime,
			Label:       "Anime",
			LabelPlural: "Anime",
			Fields: []plugin.FieldDef{
				{Key: "type", Label: "Type", Type: plugin.FieldEnum, EnumValues: append([]string(nil), typeValues...), Show: true, Editable: true, Filterable: true, Required: true},
				{Key: "episodes", Label: "Episodes", Type: plugin.FieldNumber, Unit: "eps", Min: num(1), Max: num(10000), Show: true, Editable: true},
				{Key: "year", Label: "Year", Type: plugin.FieldNumber, Min: num(1900), Max: num(2100), Show: true, Editable: true, Filterable: true, Sortable: true},
				{Key: "studio", Label: "Studio", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true},
				{Key: "synopsis", Label: "Synopsis", Type: plugin.FieldText, Editable: true},
				{Key: "source_material", Label: "Source", Type: plugin.FieldText, Show: true, Editable: true},
				{Key: "started_on", Label: "Started", Type: plugin.FieldDate, Show: true, Editable: true},
				{Key: "ended_on", Label: "Ended", Type: plugin.FieldDate, Show: true, Editable: true},
			},
			Genres: plugin.TermDef{Enabled: true, Label: "Genres", Editable: true, Filterable: true},
			Tags:   plugin.TermDef{Enabled: true, Label: "Tags", Editable: true, Filterable: true},
			Statuses: plugin.StatusLabels{
				Planned: "Plan to watch", Active: "Watching", Paused: "Paused", Completed: "Completed", Dropped: "Dropped",
			},
			Progress: plugin.ProgressSpec{
				Eligible:     []plugin.ProgressDef{{Key: "episode", Type: plugin.Count, Label: "Episode", TotalField: "episodes"}},
				Defaults:     []string{"episode"},
				Combinations: [][]string{{"episode"}},
				PrimaryKey:   "episode",
			},
			Sections: []plugin.SectionDef{
				{Type: plugin.SectionInfo, Label: "Information"},
				{Type: plugin.SectionText, Field: "synopsis", Label: "Synopsis"},
				{Type: plugin.SectionTitles, Label: "Titles"},
				{Type: plugin.SectionGenres, Label: "Genres"},
				{Type: plugin.SectionTags, Label: "Tags"},
				{Type: plugin.SectionRelated, Label: "Related"},
				{Type: plugin.SectionPeople, Label: "Characters", Roles: []string{"character"}},
				{Type: plugin.SectionRecommendations, Label: "Recommendations"},
				{Type: plugin.SectionLinks},
			},
			TitleLanguages: []string{"ja", "ja-latn", "en"},
			// native, romaji, English: the order the core used to hard-code
			TitleFallback: []string{"ja", "ja-latn", "en"},
		}},
		Sources: []plugin.SourceInfo{{
			ID:                SourceID,
			Name:              "AniList",
			RequestsPerMinute: 30,
			ExternalIDTypes:   []string{"mal"},
			SupportsLookup:    true,
			LinkLabel:         "View on AniList",
			URLPatterns:       []string{"anilist.co/anime/{id}"},
		}},
		Importers: []plugin.ImporterInfo{{
			ID:                ImporterID,
			Name:              "Yamtrack CSV",
			FileHint:          "Export your list from Yamtrack (Settings, Export data) and upload the CSV file. Only anime rows with MyAnimeList ids are imported.",
			Accept:            []string{".csv"},
			ExternalIDType:    "mal",
			ExternalIDLabel:   "MyAnimeList",
			ResolveVia:        SourceID,
			FallbackTitleLang: "ja-latn",
			UnmatchedNote:     "Imported from Yamtrack; AniList had no match for MyAnimeList #{id}.",
		}},
		Exporters: []plugin.ExporterInfo{{
			ID: ExporterID, Name: "JSON", Extension: ".json", MIME: "application/json",
		}},
	}
}

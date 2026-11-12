package manga

import (
	"github.com/allister/allister/internal/plugin"
)

// Plugin, source, importer and exporter ids and the one kind key.
const (
	PluginID   = "manga"
	KindManga  = "manga"
	SourceID   = "anilist"
	AccountImp = "anilist-user"
	MALImp     = "mal-xml"
	ExporterID = "json"
)

func num(f float64) *float64 { return &f }

// typeValues are the AniList formats of a manga, the enum of the type field.
var typeValues = []string{"MANGA", "ONE_SHOT", "NOVEL"}

// statusLabels are the core status labels of the manga plugin.
var statusLabels = plugin.StatusLabels{
	Planned: "Plan to read", Active: "Reading", Paused: "On hold", Completed: "Completed", Dropped: "Dropped",
}

func manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:              PluginID,
		Name:            "Manga",
		Version:         "1.0.0",
		ContractVersion: plugin.ContractVersion,
		DefaultEnabled:  false,
		Icon:            "menu_book",
		Kinds: []plugin.KindDef{{
			Key:         KindManga,
			Label:       "Manga",
			LabelPlural: "Manga",
			Fields: []plugin.FieldDef{
				{Key: "type", Label: "Type", Type: plugin.FieldEnum, EnumValues: append([]string(nil), typeValues...), Show: true, Editable: true, Filterable: true, Required: true},
				{Key: "chapters", Label: "Chapters", Type: plugin.FieldNumber, Unit: "ch", Min: num(1), Max: num(100000), Show: true, Editable: true},
				{Key: "volumes", Label: "Volumes", Type: plugin.FieldNumber, Min: num(1), Max: num(10000), Show: true, Editable: true},
				{Key: "year", Label: "Year", Type: plugin.FieldNumber, Min: num(1900), Max: num(2100), Show: true, Editable: true, Filterable: true, Sortable: true},
				{Key: "author", Label: "Author", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true},
				{Key: "synopsis", Label: "Synopsis", Type: plugin.FieldText, Editable: true},
				{Key: "started_on", Label: "Started", Type: plugin.FieldDate, Show: true, Editable: true},
				{Key: "ended_on", Label: "Ended", Type: plugin.FieldDate, Show: true, Editable: true},
			},
			Genres:   plugin.TermDef{Enabled: true, Label: "Genres", Editable: true, Filterable: true},
			Tags:     plugin.TermDef{Enabled: true, Label: "Tags", Editable: true, Filterable: true},
			Statuses: statusLabels,
			Progress: plugin.ProgressSpec{
				Eligible: []plugin.ProgressDef{
					{Key: "chapter", Type: plugin.Count, Label: "Chapter", TotalField: "chapters"},
					{Key: "volume", Type: plugin.Count, Label: "Volume", TotalField: "volumes"},
				},
				Defaults:     []string{"chapter"},
				Combinations: [][]string{{"chapter"}, {"volume"}},
				PrimaryKey:   "chapter",
			},
			Stats: &plugin.StatsDef{
				Units: &plugin.StatsUnitsDef{Key: "chapter", LabelPlural: "Chapters"},
			},
			Sections: []plugin.SectionDef{
				{Type: plugin.SectionInfo, Label: "Information"},
				{Type: plugin.SectionText, Field: "synopsis", Label: "Synopsis"},
				{Type: plugin.SectionTitles, Label: "Titles"},
				{Type: plugin.SectionGenres, Label: "Genres"},
				{Type: plugin.SectionTags, Label: "Tags"},
				{Type: plugin.SectionRelated, Label: "Related"},
				{Type: plugin.SectionPeople, Label: "Staff"},
				{Type: plugin.SectionRecommendations, Label: "Recommendations"},
				{Type: plugin.SectionLinks},
			},
			TitleLanguages: []string{"ja", "ja-latn", "en"},
			Columns: []plugin.ColumnDef{
				{Key: "type", Default: true, Hide: "sm"},
				{Key: "year", Default: true, Hide: "md"},
				{Key: plugin.ColumnProgress, Default: true},
				{Key: plugin.ColumnRating, Label: "Score", Default: true},
				{Key: plugin.ColumnStatus, Default: true},
				{Key: plugin.ColumnUpdated, Default: true, Hide: "lg"},
				{Key: "chapters", Hide: "md"},
				{Key: "volumes", Hide: "md"},
				{Key: "author", Hide: "lg"},
				{Key: "started_on", Hide: "lg"},
				{Key: "ended_on", Hide: "lg"},
			},
			TitleFallback: []string{"ja", "ja-latn", "en"},
		}},
		Sources: []plugin.SourceInfo{{
			ID:                SourceID,
			Name:              "AniList",
			RequestsPerMinute: 30,
			ExternalIDTypes:   []string{"mal", "anilist"},
			SupportsLookup:    true,
			LinkLabel:         "View on AniList",
			URLPatterns:       []string{"anilist.co/manga/{id}"},
		}},
		Importers: []plugin.ImporterInfo{{
			ID:                AccountImp,
			Name:              "AniList account",
			AccountLabel:      "AniList username",
			ExternalIDType:    "anilist",
			ExternalIDLabel:   "AniList",
			ResolveVia:        SourceID,
			FallbackTitleLang: "ja-latn",
			UnmatchedNote:     "Imported from AniList; AniList had no match for #{id}.",
		}, {
			ID:                MALImp,
			Name:              "MyAnimeList XML export",
			FileHint:          "Export your manga list on MyAnimeList (List, then Export) and upload the XML file.",
			Accept:            []string{".xml"},
			ExternalIDType:    "mal",
			ExternalIDLabel:   "MyAnimeList",
			ResolveVia:        SourceID,
			FallbackTitleLang: "ja-latn",
			UnmatchedNote:     "Imported from MyAnimeList; AniList had no match for MyAnimeList #{id}.",
		}},
		Exporters: []plugin.ExporterInfo{{
			ID: ExporterID, Name: "JSON", Extension: ".json", MIME: "application/json",
		}},
	}
}

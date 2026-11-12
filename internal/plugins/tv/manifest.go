package tv

import "github.com/allister/allister/internal/plugin"

const (
	PluginID = "tv"
	KindShow = "show"

	SourceTVmaze = "tvmaze"
	SourceTMDB   = "tmdb"

	ImporterYamtrack = "yamtrack-tv"
	ExporterJSON     = "json"
	exportFormat     = "allister.tv.export"
)

func num(f float64) *float64 { return &f }

var showStatuses = plugin.StatusLabels{
	Planned: "Plan to watch", Active: "Watching", Paused: "Paused", Completed: "Watched", Dropped: "Dropped",
}

func showKind() plugin.KindDef {
	return plugin.KindDef{
		Key:         KindShow,
		Label:       "Show",
		LabelPlural: "Shows",
		Fields: []plugin.FieldDef{
			{Key: "type", Label: "Type", Type: plugin.FieldEnum, EnumValues: []string{"series", "miniseries", "reality", "documentary", "other"}, Show: true, Editable: true, Filterable: true},
			{Key: "year", Label: "Year", Type: plugin.FieldNumber, Min: num(1900), Max: num(2100), Show: true, Editable: true, Filterable: true, Sortable: true},
			{Key: "seasons", Label: "Seasons", Type: plugin.FieldNumber, Min: num(1), Max: num(1000), Show: true, Editable: true},
			{Key: "episodes", Label: "Episodes", Type: plugin.FieldNumber, Min: num(1), Max: num(100000), Show: true, Editable: true},
			{Key: "runtime", Label: "Runtime", Type: plugin.FieldNumber, Min: num(1), Max: num(10000), Unit: "min", Show: true, Editable: true},
			{Key: "network", Label: "Network", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true},
			{Key: "country", Label: "Country", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true},
			{Key: "air_status", Label: "Air status", Type: plugin.FieldEnum, EnumValues: []string{"running", "ended", "upcoming", "other"}, Show: true, Editable: true, Filterable: true},
			{Key: "synopsis", Label: "Synopsis", Type: plugin.FieldText, Editable: true},
			{Key: "started_on", Label: "Started", Type: plugin.FieldDate, Show: true, Editable: true},
			{Key: "ended_on", Label: "Ended", Type: plugin.FieldDate, Show: true, Editable: true},
		},
		Genres:   plugin.TermDef{Enabled: true, Label: "Genres", Editable: true, Filterable: true},
		Tags:     plugin.TermDef{Enabled: true, Label: "Tags", Editable: true, Filterable: true},
		Statuses: showStatuses,
		Progress: plugin.ProgressSpec{
			Eligible: []plugin.ProgressDef{
				{Key: "season", Type: plugin.Count, Label: "Season", TotalField: "seasons"},
				{Key: "episode", Type: plugin.Count, Label: "Episode", TotalField: "episodes"},
				{Key: "position", Type: plugin.Duration, Label: "Position", ResetOnPrimary: true},
			},
			Defaults:     []string{"season", "episode", "position"},
			Combinations: [][]string{{"season", "episode", "position"}, {"season", "episode"}, {"episode"}, {"episode", "position"}},
			PrimaryKey:   "episode",
		},
		Stats: &plugin.StatsDef{
			Time:  &plugin.StatsTimeDef{Label: "Watchtime", UnitsKey: "episode", PerUnitField: "runtime"},
			Units: &plugin.StatsUnitsDef{Key: "episode", LabelPlural: "Episodes"},
		},
		Sections: []plugin.SectionDef{
			{Type: plugin.SectionInfo, Label: "Details"},
			{Type: plugin.SectionText, Field: "synopsis", Label: "Summary"},
			{Type: plugin.SectionTitles, Label: "Titles"},
			{Type: plugin.SectionPeople, Label: "Cast", Roles: []string{"actor"}},
			{Type: plugin.SectionGenres, Label: "Genres"},
			{Type: plugin.SectionTags, Label: "Tags"},
			{Type: plugin.SectionRecommendations, Label: "Recommendations"},
			{Type: plugin.SectionLinks, Label: "Links"},
		},
		TitleLanguages: []string{"en", "original"},
		TitleFallback:  []string{"en"},
		Columns: []plugin.ColumnDef{
			{Key: "type", Default: true, Hide: "sm"},
			{Key: "year", Default: true, Hide: "md"},
			{Key: plugin.ColumnProgress, Default: true},
			{Key: plugin.ColumnRating, Label: "Score", Default: true},
			{Key: plugin.ColumnStatus, Default: true},
			{Key: plugin.ColumnUpdated, Default: true, Hide: "lg"},
			{Key: "seasons", Hide: "md"},
			{Key: "episodes", Hide: "md"},
			{Key: "network", Hide: "lg"},
			{Key: "country", Hide: "lg"},
			{Key: "started_on", Hide: "lg"},
			{Key: "ended_on", Hide: "lg"},
		},
	}
}

func manifest() plugin.Manifest {
	return plugin.Manifest{
		ID: PluginID, Name: "TV", Version: "1.0.0", ContractVersion: plugin.ContractVersion,
		DefaultEnabled: false, Icon: "live_tv",
		Kinds: []plugin.KindDef{showKind()},
		Sources: []plugin.SourceInfo{{
			ID: SourceTVmaze, Name: "TVmaze", RequestsPerMinute: 30,
			ExternalIDTypes: []string{"tvmaze", "tvdb", "imdb"}, SupportsLookup: true, SupportsSchedule: true,
			Kinds: []string{KindShow}, LinkLabel: "View on TVmaze", URLPatterns: []string{"tvmaze.com/shows/{id}"},
		}, {
			ID: SourceTMDB, Name: "TMDB", RequestsPerMinute: 30,
			ExternalIDTypes: []string{"tmdb", "imdb", "tvdb"}, SupportsLookup: true,
			Kinds: []string{KindShow}, LinkLabel: "View on TMDB", URLPatterns: []string{"themoviedb.org/tv/{id}"},
			KeyLabel: "API key",
			KeyHelp:  "Create a free key at themoviedb.org under Settings → API (v3 key or v4 read token).",
		}},
		Importers: []plugin.ImporterInfo{{
			ID: ImporterYamtrack, Name: "Yamtrack TV CSV",
			FileHint: "Export your TV data from Yamtrack as CSV and upload it here.",
			Accept:   []string{".csv"}, ManualRows: true, FallbackTitleLang: "en",
			ExternalIDType: "tmdb", ExternalIDLabel: "TMDB",
			ResolveVia:    SourceTMDB,
			UnmatchedNote: "Imported from Yamtrack; TMDB had no match for #{id}.",
		}},
		Exporters: []plugin.ExporterInfo{{ID: ExporterJSON, Name: "JSON", Extension: ".json", MIME: "application/json"}},
	}
}

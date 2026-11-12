package movie

import "github.com/allister/allister/internal/plugin"

// Plugin, kind, source, importer and exporter ids.
const (
	PluginID  = "movie"
	KindMovie = "movie"

	SourceTMDB = "tmdb"

	ImporterYamtrack = "yamtrack-movie"
	ExporterJSON     = "json"
	exportFormat     = "allister.movie.export"
)

func num(f float64) *float64 { return &f }

var movieStatuses = plugin.StatusLabels{
	Planned: "Want to watch", Active: "Watching", Paused: "Paused", Completed: "Seen", Dropped: "Dropped",
}

func movieKind() plugin.KindDef {
	return plugin.KindDef{
		Key:         KindMovie,
		Label:       "Movie",
		LabelPlural: "Movies",
		Fields: []plugin.FieldDef{
			{Key: "year", Label: "Year", Type: plugin.FieldNumber, Min: num(1900), Max: num(2100), Show: true, Editable: true, Filterable: true, Sortable: true},
			{Key: "runtime", Label: "Runtime", Type: plugin.FieldNumber, Min: num(1), Max: num(10000), Unit: "min", Show: true, Editable: true},
			{Key: "director", Label: "Director", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true},
			{Key: "studio", Label: "Studio", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true},
			{Key: "country", Label: "Country", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true},
			{Key: "language", Label: "Language", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true},
			{Key: "collection", Label: "Collection", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true},
			{Key: "release_status", Label: "Release status", Type: plugin.FieldEnum, EnumValues: []string{"released", "upcoming", "other"}, Show: true, Editable: true, Filterable: true},
			{Key: "synopsis", Label: "Synopsis", Type: plugin.FieldText, Editable: true},
			{Key: "released_on", Label: "Released", Type: plugin.FieldDate, Show: true, Editable: true},
		},
		Genres:   plugin.TermDef{Enabled: true, Label: "Genres", Editable: true, Filterable: true},
		Tags:     plugin.TermDef{Enabled: true, Label: "Tags", Editable: true, Filterable: true},
		Statuses: movieStatuses,
		Progress: plugin.ProgressSpec{
			Eligible:     []plugin.ProgressDef{{Key: "position", Type: plugin.Duration, Label: "Position"}},
			Defaults:     []string{"position"},
			Combinations: [][]string{{"position"}},
		},
		Stats: &plugin.StatsDef{
			Time: &plugin.StatsTimeDef{Label: "Watchtime", CompletedField: "runtime", DurationKey: "position"},
		},
		Sections: []plugin.SectionDef{
			{Type: plugin.SectionInfo, Label: "Details"},
			{Type: plugin.SectionText, Field: "synopsis", Label: "Summary"},
			{Type: plugin.SectionTitles, Label: "Titles"},
			{Type: plugin.SectionPeople, Label: "Cast & crew", Roles: []string{"actor", "director", "writer"}},
			{Type: plugin.SectionGenres, Label: "Genres"},
			{Type: plugin.SectionTags, Label: "Tags"},
			{Type: plugin.SectionRecommendations, Label: "Recommendations"},
			{Type: plugin.SectionLinks, Label: "Links"},
		},
		TitleLanguages: []string{"en", "original"},
		TitleFallback:  []string{"en"},
		Columns: []plugin.ColumnDef{
			{Key: "year", Default: true, Hide: "sm"},
			{Key: plugin.ColumnRating, Label: "Score", Default: true},
			{Key: plugin.ColumnStatus, Default: true},
			{Key: plugin.ColumnUpdated, Default: true, Hide: "lg"},
			{Key: plugin.ColumnProgress, Hide: "md"},
			{Key: "runtime", Hide: "md"},
			{Key: "director", Hide: "md"},
			{Key: "studio", Hide: "lg"},
			{Key: "country", Hide: "lg"},
			{Key: "language", Hide: "lg"},
			{Key: "collection", Hide: "lg"},
			{Key: "released_on", Hide: "lg"},
		},
	}
}

func manifest() plugin.Manifest {
	return plugin.Manifest{
		ID: PluginID, Name: "Movies", Version: "1.0.0", ContractVersion: plugin.ContractVersion,
		DefaultEnabled: false, Icon: "movie",
		Kinds: []plugin.KindDef{movieKind()},
		Sources: []plugin.SourceInfo{{
			ID: SourceTMDB, Name: "TMDB", RequestsPerMinute: 30,
			ExternalIDTypes: []string{"tmdb", "imdb"}, SupportsLookup: true,
			Kinds: []string{KindMovie}, LinkLabel: "View on TMDB", URLPatterns: []string{"themoviedb.org/movie/{id}"},
			KeyLabel: "API key",
			KeyHelp:  "Create a free key at themoviedb.org under Settings → API (v3 key or v4 read token).",
		}},
		Importers: []plugin.ImporterInfo{{
			ID: ImporterYamtrack, Name: "Yamtrack Movie CSV",
			FileHint: "Export your movie data from Yamtrack as CSV and upload it here.",
			Accept:   []string{".csv"}, ManualRows: true, FallbackTitleLang: "en",
			ExternalIDType: "tmdb", ExternalIDLabel: "TMDB",
			ResolveVia:    SourceTMDB,
			UnmatchedNote: "Imported from Yamtrack; TMDB could not resolve #{id}.",
		}},
		Exporters: []plugin.ExporterInfo{{ID: ExporterJSON, Name: "JSON", Extension: ".json", MIME: "application/json"}},
	}
}

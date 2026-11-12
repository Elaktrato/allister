package game

import "github.com/allister/allister/internal/plugin"

// Plugin, kind, source, importer and exporter ids.
const (
	PluginID = "game"
	KindGame = "game"

	SourceIGDB  = "igdb"
	SourceSteam = "steam"

	ImporterYamtrack = "yamtrack-game"
	ImporterSteam    = "steam-account"
	ExporterJSON     = "json"
	exportFormat     = "allister.game.export"
)

func num(f float64) *float64 { return &f }

var gameStatuses = plugin.StatusLabels{
	Planned: "Want to play", Active: "Playing", Paused: "Paused", Completed: "Played", Dropped: "Dropped",
}

func gameKind() plugin.KindDef {
	return plugin.KindDef{
		Key:         KindGame,
		Label:       "Game",
		LabelPlural: "Games",
		Fields: []plugin.FieldDef{
			{Key: "year", Label: "Year", Type: plugin.FieldNumber, Min: num(1950), Max: num(2100), Show: true, Editable: true, Filterable: true, Sortable: true},
			{Key: "released_on", Label: "Released", Type: plugin.FieldDate, Show: true, Editable: true},
			{Key: "platforms", Label: "Platforms", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true},
			{Key: "developer", Label: "Developer", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true},
			{Key: "publisher", Label: "Publisher", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true},
			{Key: "time_main", Label: "Main story", Type: plugin.FieldNumber, Unit: "min", Show: true, Editable: true, Sortable: true},
			{Key: "time_extra", Label: "Main + extras", Type: plugin.FieldNumber, Unit: "min", Show: true, Editable: true, Sortable: true},
			{Key: "time_complete", Label: "Completionist", Type: plugin.FieldNumber, Unit: "min", Show: true, Editable: true, Sortable: true},
			{Key: "summary", Label: "Summary", Type: plugin.FieldText, Editable: true},
		},
		Genres:   plugin.TermDef{Enabled: true, Label: "Genres", Editable: true, Filterable: true},
		Tags:     plugin.TermDef{Enabled: true, Label: "Tags", Editable: true, Filterable: true},
		Statuses: gameStatuses,
		Progress: plugin.ProgressSpec{
			Eligible:     []plugin.ProgressDef{{Key: "playtime", Type: plugin.Duration, Label: "Playtime"}},
			Defaults:     []string{"playtime"},
			Combinations: [][]string{{"playtime"}},
		},
		Stats: &plugin.StatsDef{
			Time: &plugin.StatsTimeDef{Label: "Playtime", DurationKey: "playtime"},
		},
		Sections: []plugin.SectionDef{
			{Type: plugin.SectionInfo, Label: "Details"},
			{Type: plugin.SectionText, Field: "summary", Label: "Summary"},
			{Type: plugin.SectionTitles, Label: "Titles"},
			{Type: plugin.SectionGenres, Label: "Genres"},
			{Type: plugin.SectionTags, Label: "Tags"},
			{Type: plugin.SectionRecommendations, Label: "Recommendations"},
			{Type: plugin.SectionLinks, Label: "Links"},
		},
		TitleLanguages: []string{"en"},
		TitleFallback:  []string{"en"},
		Columns: []plugin.ColumnDef{
			{Key: "year", Default: true, Hide: "sm"},
			{Key: plugin.ColumnProgress, Default: true},
			{Key: plugin.ColumnRating, Label: "Score", Default: true},
			{Key: plugin.ColumnStatus, Default: true},
			{Key: plugin.ColumnUpdated, Default: true, Hide: "lg"},
			{Key: "platforms", Hide: "md"},
			{Key: "developer", Hide: "md"},
			{Key: "publisher", Hide: "lg"},
			{Key: "released_on", Hide: "lg"},
			{Key: "time_main", Hide: "lg"},
			{Key: "time_extra", Hide: "lg"},
			{Key: "time_complete", Hide: "lg"},
		},
	}
}

func manifest() plugin.Manifest {
	return plugin.Manifest{
		ID: PluginID, Name: "Games", Version: "1.0.0", ContractVersion: plugin.ContractVersion,
		DefaultEnabled: false, Icon: "sports_esports",
		Kinds: []plugin.KindDef{gameKind()},
		Sources: []plugin.SourceInfo{{
			ID: SourceIGDB, Name: "IGDB", RequestsPerMinute: 60,
			ExternalIDTypes: []string{"igdb", "steam"}, SupportsLookup: true,
			Kinds: []string{KindGame}, LinkLabel: "View on IGDB", URLPatterns: []string{"igdb.com/games/{id}"},
			KeyLabel: "Twitch app credentials",
			KeyHelp:  "Create a free Twitch application at dev.twitch.tv/console/apps.",
			KeyFields: []plugin.KeyField{
				{Name: "client_id", Label: "Client ID"},
				{Name: "client_secret", Label: "Client Secret", Secret: true},
			},
		}, {
			ID: SourceSteam, Name: "Steam", RequestsPerMinute: 60,
			ExternalIDTypes: []string{"steam"}, SupportsLookup: true,
			Kinds: []string{KindGame}, LinkLabel: "View on Steam", URLPatterns: []string{"store.steampowered.com/app/{id}"},
			KeyLabel: "Steam Web API key",
			KeyHelp:  "Create one at steamcommunity.com/dev/apikey; used for the Steam account import.",
		}},
		Importers: []plugin.ImporterInfo{{
			ID: ImporterYamtrack, Name: "Yamtrack Game CSV",
			FileHint: "Export your game data from Yamtrack as CSV and upload it here.",
			Accept:   []string{".csv"}, ManualRows: true, FallbackTitleLang: "en",
			ExternalIDType: "igdb", ExternalIDLabel: "IGDB",
			ResolveVia:    SourceIGDB,
			UnmatchedNote: "Imported from Yamtrack; IGDB had no match for #{id}.",
		}, {
			ID: ImporterSteam, Name: "Steam account",
			AccountLabel:   "SteamID64",
			ExternalIDType: "steam", ExternalIDLabel: "Steam AppID",
			ResolveVia:         SourceIGDB,
			ResolveViaFallback: []string{SourceSteam},
			ManualRows:         true, FallbackTitleLang: "en",
			UnmatchedNote: "Imported from Steam; IGDB and the Steam store had no match for #{id}.",
		}},
		Exporters: []plugin.ExporterInfo{{ID: ExporterJSON, Name: "JSON", Extension: ".json", MIME: "application/json"}},
	}
}

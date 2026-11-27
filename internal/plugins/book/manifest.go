package book

import (
	"slices"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anilist"
)

// Plugin, kind, source, importer and exporter ids.
const (
	PluginID = "book"

	KindBook   = "book"
	KindSeries = "series"

	SourceOpenLibrary = "openlibrary"
	SourceGoogleBooks = "googlebooks"
	SourceAniList     = "anilist"
	ImporterGoodreads = "goodreads-csv"
	ImporterAniList   = "anilist-account"
	ImporterYamtrack  = "yamtrack-novel"
	ExporterJSON      = "json"
	exportFormat      = "allister.book.export"
)

func num(f float64) *float64 { return &f }

var (
	bookStatuses = plugin.StatusLabels{
		Planned: "Want to read", Active: "Reading", Paused: "Paused", Completed: "Read", Dropped: "Dropped",
	}
	seriesStatuses = plugin.StatusLabels{
		Planned: "Plan to read", Active: "Reading", Paused: "On hold", Completed: "Completed", Dropped: "Dropped",
	}
	titleLanguages = []string{"en", "de", "ja", "ja-latn"}
	titleFallback  = []string{"en", "de", "ja-latn"}
)

func manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:              PluginID,
		Name:            "Books",
		Version:         "1.0.0",
		ContractVersion: plugin.ContractVersion,
		DefaultEnabled:  false,
		Icon:            "auto_stories",
		Kinds: []plugin.KindDef{
			bookKind(),
			seriesKind(),
		},
		Sources: []plugin.SourceInfo{
			{
				ID:                SourceOpenLibrary,
				Name:              "Open Library",
				RequestsPerMinute: 30,
				ExternalIDTypes:   []string{"isbn"},
				SupportsLookup:    true,
				Kinds:             []string{KindBook},
				LinkLabel:         "View on Open Library",
				URLPatterns:       []string{"openlibrary.org/works/{id}"},
			},
			{
				ID:                SourceGoogleBooks,
				Name:              "Google Books",
				RequestsPerMinute: 30,
				ExternalIDTypes:   []string{"isbn"},
				SupportsLookup:    true,
				Kinds:             []string{KindBook},
				LinkLabel:         "View on Google Books",
				KeyLabel:          "API key",
				KeyHelp:           "Optional. Without a key Google limits how many searches you can make.",
				KeyOptional:       true,
			},
			{
				ID:                SourceAniList,
				Name:              "AniList",
				RequestsPerMinute: 30,
				ExternalIDTypes:   []string{"anilist", "mal"},
				SupportsLookup:    true,
				Kinds:             []string{KindSeries},
				LinkLabel:         "View on AniList",
				URLPatterns:       []string{"anilist.co/manga/{id}"},
			},
		},
		Importers: []plugin.ImporterInfo{
			{
				ID:                 ImporterGoodreads,
				Name:               "Goodreads CSV",
				FileHint:           "Export your books on Goodreads (My Books, Import and export) and upload the CSV.",
				Accept:             []string{".csv"},
				ExternalIDType:     "isbn",
				ExternalIDLabel:    "ISBN",
				ResolveVia:         SourceOpenLibrary,
				ResolveViaFallback: []string{SourceGoogleBooks},
				ManualRows:         true,
				FallbackTitleLang:  "en",
				UnmatchedNote:      "Imported from Goodreads; no ISBN match on Open Library or Google Books.",
			},
			{
				ID:                ImporterAniList,
				Name:              "AniList account",
				AccountLabel:      "AniList username",
				ExternalIDType:    "anilist",
				ExternalIDLabel:   "AniList",
				ResolveVia:        SourceAniList,
				FallbackTitleLang: "ja-latn",
				UnmatchedNote:     "Imported from AniList; AniList had no match for #{id}.",
			},
			{
				ID:                ImporterYamtrack,
				Name:              "Yamtrack Novel CSV",
				FileHint:          "Upload the Yamtrack general CSV. Only manga rows that AniList reports as novels are imported into Light novels.",
				Accept:            []string{".csv"},
				ExternalIDType:    "mal",
				ExternalIDLabel:   "MyAnimeList",
				ResolveVia:        SourceAniList,
				ManualRows:        false,
				FallbackTitleLang: "ja-latn",
				UnmatchedNote:     "Imported from Yamtrack; AniList had no match for MyAnimeList #{id}.",
			},
		},
		Exporters: []plugin.ExporterInfo{
			{ID: ExporterJSON, Name: "JSON", Extension: ".json", MIME: "application/json"},
		},
	}
}

func bookKind() plugin.KindDef {
	return plugin.KindDef{
		Key:         KindBook,
		Label:       "Book",
		LabelPlural: "Books",
		SeriesHint:  &plugin.SeriesHintDef{TitleField: "series", PositionField: "series_number", ReleaseField: "published_on"},
		SeriesFields: []plugin.FieldDef{
			{Key: "volumes", Label: "Volumes", Type: plugin.FieldNumber, Min: num(1), Max: num(10000), Show: true, Editable: true},
			{Key: "chapters", Label: "Chapters", Type: plugin.FieldNumber, Min: num(1), Max: num(100000), Show: true, Editable: true},
			{Key: "author", Label: "Author", Type: plugin.FieldText, Show: true, Editable: true},
		},
		SeriesLink: &plugin.SeriesLinkDef{Kind: KindSeries, DescriptionField: "synopsis", StartField: "published_on", EndField: "ended_on", PartsField: "volumes"},
		Fields: []plugin.FieldDef{
			{Key: "author", Label: "Author", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true},
			{Key: "pages", Label: "Pages", Type: plugin.FieldNumber, Min: num(1), Max: num(100000), Show: true, Editable: true},
			{Key: "year", Label: "Year", Type: plugin.FieldNumber, Min: num(1000), Max: num(2100), Show: true, Editable: true, Filterable: true, Sortable: true},
			{Key: "publisher", Label: "Publisher", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true},
			{Key: "isbn", Label: "ISBN", Type: plugin.FieldText, Show: true, Editable: true},
			{Key: "series", Label: "Series", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true},
			{Key: "series_number", Label: "Series number", Type: plugin.FieldNumber, Min: num(1), Max: num(10000), Editable: true, Sortable: true},
			{Key: "synopsis", Label: "Synopsis", Type: plugin.FieldText, Editable: true},
			{Key: "published_on", Label: "Published", Type: plugin.FieldDate, Show: true, Editable: true},
		},
		Genres:   plugin.TermDef{Enabled: true, Label: "Genres", Editable: true, Filterable: true},
		Tags:     plugin.TermDef{Enabled: true, Label: "Tags", Editable: true, Filterable: true},
		Repeat:   &plugin.RepeatDef{Action: "Reread", Marker: "Rereading"},
		Statuses: bookStatuses,
		Progress: plugin.ProgressSpec{
			Eligible: []plugin.ProgressDef{
				{Key: "pages", Type: plugin.Pages, Label: "Page", TotalField: "pages"},
				{Key: "percent", Type: plugin.Percent, Label: "Percent"},
			},
			Defaults:     []string{"pages"},
			Combinations: [][]string{{"pages"}, {"percent"}},
			PrimaryKey:   "pages",
		},
		Stats: &plugin.StatsDef{
			Units: &plugin.StatsUnitsDef{Key: "pages", LabelPlural: "Pages"},
		},
		Sections: []plugin.SectionDef{
			{Type: plugin.SectionInfo, Label: "Details"},
			{Type: plugin.SectionText, Field: "synopsis", Label: "Summary"},
			{Type: plugin.SectionTitles, Label: "Titles"},
			{Type: plugin.SectionPeople, Label: "Authors", Roles: []string{"author", "illustrator"}},
			{Type: plugin.SectionGenres, Label: "Genres"},
			{Type: plugin.SectionTags, Label: "Tags"},
			{Type: plugin.SectionLinks, Label: "Links"},
		},
		TitleLanguages: titleLanguages,
		TitleFallback:  titleFallback,
		Columns: []plugin.ColumnDef{
			{Key: "author", Default: true},
			{Key: "year", Default: true, Hide: "md"},
			{Key: plugin.ColumnProgress, Default: true},
			{Key: plugin.ColumnRating, Label: "Score", Default: true},
			{Key: plugin.ColumnStatus, Default: true},
			{Key: plugin.ColumnUpdated, Default: true, Hide: "lg"},
			{Key: "series", Hide: "md"},
			{Key: "publisher", Hide: "lg"},
			{Key: "isbn", Hide: "lg"},
			{Key: "published_on", Hide: "lg"},
			{Key: "series_number", Hide: "lg"},
		},
	}
}

func seriesKind() plugin.KindDef {
	return plugin.KindDef{
		Key:         KindSeries,
		Label:       "Light novel",
		LabelPlural: "Light novels",
		Fields: []plugin.FieldDef{
			{Key: "author", Label: "Author", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true},
			{Key: "volumes", Label: "Volumes", Type: plugin.FieldNumber, Min: num(1), Max: num(10000), Show: true, Editable: true},
			{Key: "chapters", Label: "Chapters", Type: plugin.FieldNumber, Min: num(1), Max: num(100000), Show: true, Editable: true},
			{Key: "year", Label: "Year", Type: plugin.FieldNumber, Min: num(1000), Max: num(2100), Show: true, Editable: true, Filterable: true, Sortable: true},
			{Key: "synopsis", Label: "Synopsis", Type: plugin.FieldText, Editable: true},
			{Key: "published_on", Label: "Published", Type: plugin.FieldDate, Show: true, Editable: true},
			{Key: "ended_on", Label: "Ended", Type: plugin.FieldDate, Show: true, Editable: true},
			// the AniList extras (light novels from other sources leave them empty)
			{Key: "release_status", Label: "Release status", Type: plugin.FieldEnum, EnumValues: slices.Clone(anilist.ReleaseStatusValues), Show: true, Editable: true, Filterable: true},
			{Key: "country", Label: "Country", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true},
			{Key: "adult", Label: "Adult", Type: plugin.FieldEnum, EnumValues: slices.Clone(anilist.AdultValues), Show: true, Editable: true, Filterable: true},
			{Key: "community_score", Label: "Community score", Type: plugin.FieldNumber, Unit: "%", Min: num(0), Max: num(100), Show: true, Sortable: true, SourceRefresh: true},
			{Key: "popularity", Label: "Popularity", Type: plugin.FieldNumber, Min: num(0), Max: num(100000000), Show: true, Sortable: true, SourceRefresh: true},
		},
		AltTitles:  true,
		Banner:     true,
		Trailer:    true,
		AdultField: "adult",
		Genres:     plugin.TermDef{Enabled: true, Label: "Genres", Editable: true, Filterable: true},
		Tags:       plugin.TermDef{Enabled: true, Label: "Tags", Editable: true, Filterable: true},
		Repeat:     &plugin.RepeatDef{Action: "Reread", Marker: "Rereading"},
		Statuses:   seriesStatuses,
		Progress: plugin.ProgressSpec{
			Eligible: []plugin.ProgressDef{
				{Key: "volume", Type: plugin.Count, Label: "Volume", TotalField: "volumes"},
				{Key: "chapter", Type: plugin.Count, Label: "Chapter", TotalField: "chapters"},
			},
			Defaults:     []string{"volume", "chapter"},
			Combinations: [][]string{{"volume", "chapter"}, {"chapter"}, {"volume"}},
			PrimaryKey:   "chapter",
		},
		Sections: []plugin.SectionDef{
			{Type: plugin.SectionInfo, Label: "Details"},
			{Type: plugin.SectionText, Field: "synopsis", Label: "Summary"},
			{Type: plugin.SectionTitles, Label: "Titles"},
			{Type: plugin.SectionPeople, Label: "Authors", Roles: []string{"author", "illustrator"}},
			{Type: plugin.SectionGenres, Label: "Genres"},
			{Type: plugin.SectionTags, Label: "Tags"},
			{Type: plugin.SectionLinks, Label: "Links"},
		},
		TitleLanguages: titleLanguages,
		TitleFallback:  titleFallback,
		Columns: []plugin.ColumnDef{
			{Key: "author", Default: true},
			{Key: "year", Default: true, Hide: "md"},
			{Key: plugin.ColumnProgress, Default: true},
			{Key: plugin.ColumnRating, Label: "Score", Default: true},
			{Key: plugin.ColumnStatus, Default: true},
			{Key: plugin.ColumnUpdated, Default: true, Hide: "lg"},
			{Key: "volumes", Hide: "md"},
			{Key: "chapters", Hide: "md"},
			{Key: "published_on", Hide: "lg"},
			{Key: "release_status", Hide: "lg"},
		},
	}
}

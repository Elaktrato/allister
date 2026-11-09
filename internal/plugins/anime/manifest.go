package anime

import (
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anime/animedef"
)

// typeValues are the AniList formats of an anime, the enum of the type field.
var typeValues = animedef.TypeValues()

// manifest is the plugin's data; it lives in animedef (see that package).
func manifest() plugin.Manifest { return animedef.Manifest() }

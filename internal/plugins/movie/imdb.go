package movie

import (
	"context"
	"fmt"
	"io"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/imdb"
)

// imdbMovieImporter reads an IMDb ratings or watchlist CSV and keeps the movie
// types (films, TV movies, shorts, videos, specials). Series and episodes are
// counted as ignored.
type imdbMovieImporter struct{}

func (imdbMovieImporter) Parse(ctx context.Context, r io.Reader) (plugin.ParseResult, error) {
	if err := ctx.Err(); err != nil {
		return plugin.ParseResult{}, err
	}
	rows, err := imdb.Parse(r)
	if err != nil {
		return plugin.ParseResult{}, fmt.Errorf("%w: %w", plugin.ErrBadInput, err)
	}
	return imdb.Entries(rows, KindMovie, imdb.TypeMovie), nil
}

var _ plugin.Importer = imdbMovieImporter{}

package tv

import (
	"context"
	"fmt"
	"io"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/imdb"
)

// imdbTVImporter reads an IMDb ratings or watchlist CSV and keeps the series
// types (TV series and miniseries). Films and episodes are counted as ignored.
type imdbTVImporter struct{}

func (imdbTVImporter) Parse(ctx context.Context, r io.Reader) (plugin.ParseResult, error) {
	if err := ctx.Err(); err != nil {
		return plugin.ParseResult{}, err
	}
	rows, err := imdb.Parse(r)
	if err != nil {
		return plugin.ParseResult{}, fmt.Errorf("%w: %w", plugin.ErrBadInput, err)
	}
	return imdb.Entries(rows, KindShow, imdb.TypeTV), nil
}

var _ plugin.Importer = imdbTVImporter{}

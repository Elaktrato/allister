package library

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/allister/allister/internal/plugin"
)

// Appearance is one place a person shows up: one row per (item, role,
// counterpart) pair. For a credit "character C, voiced by X" the row of C has
// Role "character", With X and WithRole "actor"; the row of X is the mirror
// image. A person who is both credited and a counterpart gets both kinds of
// rows. People belong to one plugin, so items of other plugins never appear.
type Appearance struct {
	ItemID                                      int64
	PluginID, Kind, Source, SourceID, CoverPath string
	Titles                                      []plugin.Title
	Role                                        string
	With                                        *Person
	WithRole                                    string
}

// Appearances returns the person's appearances ordered by the lower-cased
// first stored title, then item id, then credit position. An unknown person
// has none.
func (l *Library) Appearances(ctx context.Context, personID int64) ([]Appearance, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT i.id, i.plugin_id, i.kind, i.source, i.source_id, i.cover_path, a.role, a.with_role,
		w.id, w.plugin_id, w.source, w.source_id, w.name, w.image_path, w.external_url
		FROM (
		  SELECT c.item_id, c.seq, c.role AS role, c.counterpart_role AS with_role, c.counterpart_person_id AS other
		    FROM credits c WHERE c.person_id = ?1
		  UNION ALL
		  SELECT c.item_id, c.seq, c.counterpart_role, c.role, c.person_id
		    FROM credits c WHERE c.counterpart_person_id = ?1
		) a
		JOIN items i ON i.id = a.item_id
		LEFT JOIN `+tablePeople+` w ON w.id = a.other
		ORDER BY lower(COALESCE((SELECT t.text FROM item_titles t WHERE t.item_id = i.id ORDER BY t.rowid LIMIT 1), '')), i.id, a.seq`, personID)
	if err != nil {
		return nil, fmt.Errorf("appearances: %w", err)
	}
	var out []Appearance
	for rows.Next() {
		var a Appearance
		var wid sql.NullInt64
		var wp, wsrc, wsid, wname, wimg, wurl sql.NullString
		if err := rows.Scan(&a.ItemID, &a.PluginID, &a.Kind, &a.Source, &a.SourceID, &a.CoverPath, &a.Role, &a.WithRole,
			&wid, &wp, &wsrc, &wsid, &wname, &wimg, &wurl); err != nil {
			rows.Close()
			return nil, fmt.Errorf("appearances: %w", err)
		}
		if wid.Valid {
			a.With = &Person{ID: wid.Int64, PluginID: wp.String, Source: wsrc.String, SourceID: wsid.String,
				Name: wname.String, ImagePath: wimg.String, ExternalURL: wurl.String}
		}
		out = append(out, a)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("appearances: %w", err)
	}
	if len(out) == 0 {
		return nil, nil
	}
	titles := map[int64][]plugin.Title{}
	var ids []any
	for _, a := range out {
		if _, ok := titles[a.ItemID]; !ok {
			titles[a.ItemID] = nil
			ids = append(ids, a.ItemID)
		}
	}
	trows, err := l.db.QueryContext(ctx, `SELECT item_id, lang, text FROM item_titles WHERE item_id IN (`+placeholders(len(ids))+`) ORDER BY rowid`, ids...)
	if err != nil {
		return nil, fmt.Errorf("appearances: titles: %w", err)
	}
	for trows.Next() {
		var id int64
		var t plugin.Title
		if err := trows.Scan(&id, &t.Lang, &t.Text); err != nil {
			trows.Close()
			return nil, fmt.Errorf("appearances: titles: %w", err)
		}
		titles[id] = append(titles[id], t)
	}
	if err := closeRows(trows); err != nil {
		return nil, fmt.Errorf("appearances: titles: %w", err)
	}
	for i := range out {
		out[i].Titles = titles[out[i].ItemID]
	}
	return out, nil
}

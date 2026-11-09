package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/allister/allister/internal/plugin"
)

const maxCredits = 500

// SaveCredits replaces the credits of the item in one transaction (existing
// credits are deleted first, so a re-sync is idempotent). People are upserted
// by (plugin_id, source, source_id) with the item's plugin and source: name
// and URL are updated; the incoming image URL replaces the stored path only
// when that is empty or itself a remote URL, so a downloaded local file
// survives a re-sync. Counterparts (Credit.With) are upserted as people too.
func (l *Library) SaveCredits(ctx context.Context, itemID int64, credits []plugin.Credit) error {
	if len(credits) > maxCredits {
		return invalid("more than %d credits", maxCredits)
	}
	for _, c := range credits {
		if strings.TrimSpace(c.Person.ID) == "" {
			return invalid("a credit needs a person id")
		}
		if c.With != nil && strings.TrimSpace(c.With.ID) == "" {
			return invalid("a credit counterpart needs a person id")
		}
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("save credits: %w", err)
	}
	defer tx.Rollback()
	var pluginID, source string
	err = tx.QueryRowContext(ctx, `SELECT plugin_id, source FROM items WHERE id = ?`, itemID).Scan(&pluginID, &source)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoEntry
	}
	if err != nil {
		return fmt.Errorf("save credits: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM credits WHERE item_id = ?`, itemID); err != nil {
		return fmt.Errorf("save credits: %w", err)
	}
	for i, c := range credits {
		pid, err := upsertPerson(ctx, tx, pluginID, source, c.Person)
		if err != nil {
			return err
		}
		var with any
		if c.With != nil {
			id, err := upsertPerson(ctx, tx, pluginID, source, *c.With)
			if err != nil {
				return err
			}
			with = id
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO credits(item_id, seq, role, person_id, counterpart_person_id, counterpart_role)
			VALUES (?,?,?,?,?,?)`, itemID, i, c.Role, pid, with, c.WithRole); err != nil {
			return fmt.Errorf("save credits: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("save credits: %w", err)
	}
	return nil
}

func upsertPerson(ctx context.Context, tx *sql.Tx, pluginID, source string, p plugin.PersonRef) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO `+tablePeople+`(plugin_id, source, source_id, name, image_path, external_url)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(plugin_id, source, source_id) DO UPDATE SET name = excluded.name, external_url = excluded.external_url,
		  image_path = CASE
		    WHEN `+tablePeople+`.image_path <> '' AND `+tablePeople+`.image_path NOT LIKE 'http%' THEN `+tablePeople+`.image_path
		    WHEN excluded.image_path <> '' THEN excluded.image_path
		    ELSE `+tablePeople+`.image_path END
		RETURNING id`, pluginID, source, strings.TrimSpace(p.ID), p.Name, p.ImageURL, p.URL).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("save person: %w", err)
	}
	return id, nil
}

// Credits returns the item's credits ordered by seq with people resolved.
func (l *Library) Credits(ctx context.Context, itemID int64) ([]SavedCredit, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT c.seq, c.role, c.counterpart_role,
		p.id, p.plugin_id, p.source, p.source_id, p.name, p.image_path, p.external_url,
		w.id, w.plugin_id, w.source, w.source_id, w.name, w.image_path, w.external_url
		FROM credits c
		JOIN `+tablePeople+` p ON p.id = c.person_id
		LEFT JOIN `+tablePeople+` w ON w.id = c.counterpart_person_id
		WHERE c.item_id = ? ORDER BY c.seq`, itemID)
	if err != nil {
		return nil, fmt.Errorf("load credits: %w", err)
	}
	var out []SavedCredit
	for rows.Next() {
		var c SavedCredit
		var wid sql.NullInt64
		var wp, wsrc, wsid, wname, wimg, wurl sql.NullString
		if err := rows.Scan(&c.Seq, &c.Role, &c.WithRole,
			&c.Person.ID, &c.Person.PluginID, &c.Person.Source, &c.Person.SourceID, &c.Person.Name, &c.Person.ImagePath, &c.Person.ExternalURL,
			&wid, &wp, &wsrc, &wsid, &wname, &wimg, &wurl); err != nil {
			rows.Close()
			return nil, fmt.Errorf("load credits: %w", err)
		}
		if wid.Valid {
			c.With = &Person{ID: wid.Int64, PluginID: wp.String, Source: wsrc.String, SourceID: wsid.String,
				Name: wname.String, ImagePath: wimg.String, ExternalURL: wurl.String}
		}
		out = append(out, c)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("load credits: %w", err)
	}
	return out, nil
}

// Person returns the person or nil, nil when absent.
func (l *Library) Person(ctx context.Context, id int64) (*Person, error) {
	var p Person
	err := l.db.QueryRowContext(ctx, `SELECT id, plugin_id, source, source_id, name, image_path, external_url
		FROM `+tablePeople+` WHERE id = ?`, id).Scan(&p.ID, &p.PluginID, &p.Source, &p.SourceID, &p.Name, &p.ImagePath, &p.ExternalURL)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load person: %w", err)
	}
	return &p, nil
}

// ItemPeople returns the distinct people credited on the item (people and
// counterparts) in order of first appearance.
func (l *Library) ItemPeople(ctx context.Context, itemID int64) ([]Person, error) {
	cs, err := l.Credits(ctx, itemID)
	if err != nil {
		return nil, err
	}
	var out []Person
	seen := map[int64]bool{}
	add := func(p *Person) {
		if p != nil && !seen[p.ID] {
			seen[p.ID] = true
			out = append(out, *p)
		}
	}
	for i := range cs {
		add(&cs[i].Person)
		add(cs[i].With)
	}
	return out, nil
}

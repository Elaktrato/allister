package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/allister/allister/internal/plugin"
)

const (
	maxCredits     = 500
	maxDetailRunes = 100
)

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
		if err := cleanText("credit role", c.Role, singleLine); err != nil {
			return err
		}
		if err := cleanText("credit detail", c.Detail, singleLine); err != nil {
			return err
		}
		if utf8.RuneCountInString(c.Detail) > maxDetailRunes {
			return invalid("credit detail is longer than %d characters", maxDetailRunes)
		}
		if err := cleanText("credit counterpart role", c.WithRole, singleLine); err != nil {
			return err
		}
		if err := checkPerson("credit person", c.Person); err != nil {
			return err
		}
		if c.With != nil {
			if err := checkPerson("credit counterpart person", *c.With); err != nil {
				return err
			}
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
		if _, err := tx.ExecContext(ctx, `INSERT INTO credits(item_id, seq, role, detail, person_id, counterpart_person_id, counterpart_role)
			VALUES (?,?,?,?,?,?,?)`, itemID, i, c.Role, c.Detail, pid, with, c.WithRole); err != nil {
			return fmt.Errorf("save credits: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("save credits: %w", err)
	}
	return nil
}

func checkPerson(path string, p plugin.PersonRef) error {
	if strings.TrimSpace(p.ID) == "" {
		return invalid("%s needs a person id", path)
	}
	if err := cleanText(path+" id", p.ID, singleLine); err != nil {
		return err
	}
	if err := cleanText(path+" name", p.Name, singleLine); err != nil {
		return err
	}
	if err := checkHTTPURL(path+" url", p.URL); err != nil {
		return err
	}
	return checkImageRef(path+" image", p.ImageURL)
}

func upsertPerson(ctx context.Context, tx *sql.Tx, pluginID, source string, p plugin.PersonRef) (int64, error) {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		// a new person needs a name; a known one keeps the stored name
		var one int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM `+tablePeople+` WHERE plugin_id = ? AND source = ? AND source_id = ?`,
			pluginID, source, strings.TrimSpace(p.ID)).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, invalid("person %q needs a name", p.ID)
		}
		if err != nil {
			return 0, fmt.Errorf("save person: %w", err)
		}
	}
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO `+tablePeople+`(plugin_id, source, source_id, name, image_path, external_url)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(plugin_id, source, source_id) DO UPDATE SET name = CASE WHEN excluded.name <> '' THEN excluded.name ELSE `+tablePeople+`.name END, external_url = excluded.external_url,
		  image_path = CASE
		    WHEN `+tablePeople+`.image_path <> '' AND `+tablePeople+`.image_path NOT LIKE 'http%' THEN `+tablePeople+`.image_path
		    WHEN excluded.image_path <> '' THEN excluded.image_path
		    ELSE `+tablePeople+`.image_path END
		RETURNING id`, pluginID, source, strings.TrimSpace(p.ID), name, p.ImageURL, p.URL).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("save person: %w", err)
	}
	return id, nil
}

// Credits returns the item's credits ordered by seq with people resolved.
func (l *Library) Credits(ctx context.Context, itemID int64) ([]SavedCredit, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT c.seq, c.role, c.detail, c.counterpart_role,
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
		if err := rows.Scan(&c.Seq, &c.Role, &c.Detail, &c.WithRole,
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

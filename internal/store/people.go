package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/allister/allister/internal/provider"
)

// SaveCast replaces the cast of animeID in one transaction (existing cast
// rows are deleted first, so a re-sync is idempotent). People are upserted by
// (source, source_id): name and URL are updated; the remote ImageURL is
// stored in image_path unless a local path (not starting with "http") is
// already known.
func (s *Store) SaveCast(ctx context.Context, animeID int64, cast []provider.CastMember) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("save cast: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM anime_cast WHERE anime_id = ?`, animeID); err != nil {
		return fmt.Errorf("save cast: %w", err)
	}
	for _, m := range cast {
		charID, err := upsertPerson(ctx, tx, m.Character)
		if err != nil {
			return err
		}
		var actor any
		if m.Actor != nil {
			id, err := upsertPerson(ctx, tx, *m.Actor)
			if err != nil {
				return err
			}
			actor = id
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO anime_cast(anime_id, character_person_id, actor_person_id, role)
			VALUES (?,?,?,?)`, animeID, charID, actor, m.Role); err != nil {
			return fmt.Errorf("save cast: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("save cast: %w", err)
	}
	return nil
}

func upsertPerson(ctx context.Context, tx *sql.Tx, p provider.Person) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO people(source, source_id, name, image_path, external_url)
		VALUES (?,?,?,?,?)
		ON CONFLICT(source, source_id) DO UPDATE SET name = excluded.name, external_url = excluded.external_url,
		  image_path = CASE
		    WHEN people.image_path <> '' AND people.image_path NOT LIKE 'http%' THEN people.image_path
		    WHEN excluded.image_path <> '' THEN excluded.image_path
		    ELSE people.image_path END
		RETURNING id`, string(p.Source), p.ID, p.Name, p.ImageURL, p.URL).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("save person: %w", err)
	}
	return id, nil
}

// SetPersonImagePath replaces the person's image (remote URL or local path).
func (s *Store) SetPersonImagePath(ctx context.Context, personID int64, path string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE people SET image_path = ? WHERE id = ?`, path, personID)
	return err
}

// Person returns the person or nil, nil when absent.
func (s *Store) Person(ctx context.Context, id int64) (*Person, error) {
	var p Person
	var src string
	err := s.db.QueryRowContext(ctx, `SELECT id, source, source_id, name, image_path, external_url
		FROM people WHERE id = ?`, id).Scan(&p.ID, &src, &p.SourceID, &p.Name, &p.ImagePath, &p.ExternalURL)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("person: %w", err)
	}
	p.Source = provider.Source(src)
	return &p, nil
}

// PersonAnime returns the saved anime the person appears in, as voice actor
// or as character, ordered by anime id. Character is always the name of the
// character in that role (for a character page: the character itself). An
// anime appears once per distinct (anime, character) pair.
func (s *Store) PersonAnime(ctx context.Context, personID int64) ([]PersonAppearance, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT a.id, a.cover_path, c.name
		FROM anime_cast ac
		JOIN anime a ON a.id = ac.anime_id
		JOIN people c ON c.id = ac.character_person_id
		WHERE ac.actor_person_id = ?1 OR ac.character_person_id = ?1
		ORDER BY a.id, c.name`, personID)
	if err != nil {
		return nil, fmt.Errorf("person anime: %w", err)
	}
	var out []PersonAppearance
	for rows.Next() {
		var pa PersonAppearance
		if err := rows.Scan(&pa.AnimeID, &pa.CoverPath, &pa.Character); err != nil {
			rows.Close()
			return nil, fmt.Errorf("person anime: %w", err)
		}
		out = append(out, pa)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("person anime: %w", err)
	}
	for i := range out {
		if out[i].Titles, err = s.titles(ctx, out[i].AnimeID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

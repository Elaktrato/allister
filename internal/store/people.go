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

// AnimePeople returns the distinct people (characters and voice actors) in
// the cast of animeID, ordered by person id.
func (s *Store) AnimePeople(ctx context.Context, animeID int64) ([]Person, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.id, p.source, p.source_id, p.name, p.image_path, p.external_url
		FROM people p WHERE p.id IN (
		  SELECT character_person_id FROM anime_cast WHERE anime_id = ?1
		  UNION
		  SELECT actor_person_id FROM anime_cast WHERE anime_id = ?1 AND actor_person_id IS NOT NULL)
		ORDER BY p.id`, animeID)
	if err != nil {
		return nil, fmt.Errorf("anime people: %w", err)
	}
	defer rows.Close()
	var out []Person
	for rows.Next() {
		var p Person
		var src string
		if err := rows.Scan(&p.ID, &src, &p.SourceID, &p.Name, &p.ImagePath, &p.ExternalURL); err != nil {
			return nil, fmt.Errorf("anime people: %w", err)
		}
		p.Source = provider.Source(src)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("anime people: %w", err)
	}
	return out, nil
}

// SavedCast is one cast row with local person ids.
type SavedCast struct {
	Character Person
	Actor     *Person
	Role      string
}

// Cast returns the stored cast of animeID in insertion order.
func (s *Store) Cast(ctx context.Context, animeID int64) ([]SavedCast, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT ac.role,
		c.id, c.source, c.source_id, c.name, c.image_path, c.external_url,
		a.id, a.source, a.source_id, a.name, a.image_path, a.external_url
		FROM anime_cast ac
		JOIN people c ON c.id = ac.character_person_id
		LEFT JOIN people a ON a.id = ac.actor_person_id
		WHERE ac.anime_id = ? ORDER BY ac.rowid`, animeID)
	if err != nil {
		return nil, fmt.Errorf("cast: %w", err)
	}
	defer rows.Close()
	var out []SavedCast
	for rows.Next() {
		var (
			sc          SavedCast
			cs          string
			aid         sql.NullInt64
			asrc, asid  sql.NullString
			aname, aimg sql.NullString
			aurl        sql.NullString
		)
		if err := rows.Scan(&sc.Role, &sc.Character.ID, &cs, &sc.Character.SourceID, &sc.Character.Name,
			&sc.Character.ImagePath, &sc.Character.ExternalURL, &aid, &asrc, &asid, &aname, &aimg, &aurl); err != nil {
			return nil, fmt.Errorf("cast: %w", err)
		}
		sc.Character.Source = provider.Source(cs)
		if aid.Valid {
			sc.Actor = &Person{ID: aid.Int64, Source: provider.Source(asrc.String), SourceID: asid.String,
				Name: aname.String, ImagePath: aimg.String, ExternalURL: aurl.String}
		}
		out = append(out, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cast: %w", err)
	}
	return out, nil
}

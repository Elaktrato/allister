package store

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugins/anime/animemap"
	"github.com/allister/allister/internal/provider"
)

// SaveCast replaces the cast of animeID in one transaction (existing cast
// rows are deleted first, so a re-sync is idempotent). The cast is mapped and
// sanitised by animemap: a character without an id or name is skipped, a
// voice actor without one only loses the link, bad URLs are dropped. People are upserted by
// (source, source_id): name and URL are updated; the remote ImageURL is
// stored in image_path unless a local path (not starting with "http") is
// already known.
func (s *Store) SaveCast(ctx context.Context, animeID int64, cast []provider.CastMember) error {
	err := s.lib.SaveCredits(ctx, animeID, animemap.Credits(cast))
	if errors.Is(err, library.ErrNoEntry) {
		return fmt.Errorf("save cast: anime %d does not exist", animeID)
	}
	return wrapf("save cast", err)
}

// SetPersonImagePath replaces the person's image (remote URL or local file
// name). An unknown id is a no-op.
func (s *Store) SetPersonImagePath(ctx context.Context, personID int64, path string) error {
	return ignoreMissing(s.lib.SetPersonImagePath(ctx, personID, path))
}

// Person returns the person or nil, nil when absent.
func (s *Store) Person(ctx context.Context, id int64) (*Person, error) {
	p, err := s.lib.Person(ctx, id)
	if err != nil || p == nil {
		return nil, wrapf("person", err)
	}
	out := person(*p)
	return &out, nil
}

// PersonAnime returns the saved anime the person appears in, ordered by anime
// id, then character name, with voice-actor rows before character rows.
// When the person is the voice actor of a character, the row has AsActor set
// and Character is the voiced character's name. When the person is itself a
// character, AsActor is false and Character is the person's own name. An
// anime appears once per distinct (anime, character, role) triple; a person
// who is both actor and character in the data gets both kinds of rows.
func (s *Store) PersonAnime(ctx context.Context, personID int64) ([]PersonAppearance, error) {
	p, err := s.lib.Person(ctx, personID)
	if err != nil || p == nil {
		return nil, wrapf("person anime", err)
	}
	apps, err := s.lib.Appearances(ctx, personID)
	if err != nil {
		return nil, wrapf("person anime", err)
	}
	type key struct {
		id        int64
		character string
		asActor   bool
	}
	seen := map[key]bool{}
	var out []PersonAppearance
	for _, a := range apps {
		if a.PluginID != animePlugin {
			continue
		}
		pa := PersonAppearance{AnimeID: a.ItemID, Source: provider.Source(a.Source), SourceID: a.SourceID,
			Titles: providerTitles(a.Titles), CoverPath: a.CoverPath}
		switch {
		case a.Role == roleCharacter: // the person is the credited character
			pa.Character = p.Name
		case a.With != nil: // the person is the counterpart (voice actor) of a character
			pa.Character, pa.AsActor = a.With.Name, true
		default:
			continue
		}
		k := key{pa.AnimeID, pa.Character, pa.AsActor}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, pa)
	}
	slices.SortStableFunc(out, func(x, y PersonAppearance) int {
		if c := cmp.Compare(x.AnimeID, y.AnimeID); c != 0 {
			return c
		}
		if c := cmp.Compare(x.Character, y.Character); c != 0 {
			return c
		}
		switch { // actor rows first
		case x.AsActor == y.AsActor:
			return 0
		case x.AsActor:
			return -1
		}
		return 1
	})
	return out, nil
}

// AnimePeople returns the distinct people (characters and voice actors) in
// the cast of animeID, ordered by person id.
func (s *Store) AnimePeople(ctx context.Context, animeID int64) ([]Person, error) {
	ps, err := s.lib.ItemPeople(ctx, animeID)
	if err != nil {
		return nil, wrapf("anime people", err)
	}
	var out []Person
	for _, p := range ps {
		out = append(out, person(p))
	}
	slices.SortFunc(out, func(x, y Person) int { return cmp.Compare(x.ID, y.ID) })
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
	cs, err := s.lib.Credits(ctx, animeID)
	if err != nil {
		return nil, wrapf("cast", err)
	}
	var out []SavedCast
	for _, c := range cs {
		sc := SavedCast{Character: person(c.Person), Role: c.Detail}
		if c.With != nil {
			a := person(*c.With)
			sc.Actor = &a
		}
		out = append(out, sc)
	}
	return out, nil
}

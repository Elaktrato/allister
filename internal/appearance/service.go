package appearance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
)

// Settings keys (global: the app has one user).
const (
	KeyPreset  = "appearance.preset"
	KeyPalette = "appearance.palette" // JSON object token -> #rrggbb, "" for none
	KeyFont    = "appearance.font"
	KeyScale   = "appearance.font_scale"
)

// Store is the part of settings.Store the service needs.
type Store interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, value string) error
}

// ErrInvalid wraps a refused setting; its message is meant for the user.
var ErrInvalid = errors.New("invalid")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Service reads and writes the appearance settings and keeps the rendered
// stylesheet in memory until a setting changes.
type Service struct {
	set   Store
	fonts *FontStore

	mu      sync.Mutex
	css     []byte
	version string
}

// NewService returns a service over the settings store; fonts may be nil
// (no uploaded fonts). A nil store serves the defaults and refuses changes.
func NewService(set Store, fonts *FontStore) *Service {
	return &Service{set: set, fonts: fonts}
}

// Fonts returns the uploaded font store (nil when uploads are off).
func (s *Service) Fonts() *FontStore { return s.fonts }

// Config reads the stored settings as they are; Resolve applies the
// fallbacks. A read error is logged and the value treated as unset.
func (s *Service) Config(ctx context.Context) Config {
	var c Config
	if s.set == nil {
		return c
	}
	get := func(key string) string {
		v, _, err := s.set.Get(ctx, key)
		if err != nil {
			log.Printf("appearance: read %s: %v (using the default)", key, err)
		}
		return v
	}
	c.Preset, c.Font, c.Scale = get(KeyPreset), get(KeyFont), get(KeyScale)
	if raw := get(KeyPalette); raw != "" {
		p, err := DecodePalette(raw)
		if err != nil {
			log.Printf("appearance: stored palette ignored: %v", err)
		} else {
			c.Palette = p
		}
	}
	return c
}

// Uploads lists the uploaded fonts (none when uploads are off or the list
// cannot be read, which is logged).
func (s *Service) Uploads(ctx context.Context) []Font {
	if s.fonts == nil {
		return nil
	}
	fs, err := s.fonts.List(ctx)
	if err != nil {
		log.Printf("appearance: list fonts: %v", err)
		return nil
	}
	return fs
}

// AddFont stores an uploaded font (see FontStore.Add).
func (s *Service) AddFont(ctx context.Context, name string, r io.Reader) (Font, bool, error) {
	if s.fonts == nil {
		return Font{}, false, errors.New("appearance: font uploads are off")
	}
	defer s.Invalidate()
	return s.fonts.Add(ctx, name, r)
}

// DeleteFont removes an uploaded font; when it is the app font, the app
// font goes back to the default.
func (s *Service) DeleteFont(ctx context.Context, id int64) error {
	if s.fonts == nil {
		return ErrNoFont
	}
	defer s.Invalidate()
	if err := s.fonts.Delete(ctx, id); err != nil {
		return err
	}
	if s.set != nil {
		if cur, _, err := s.set.Get(ctx, KeyFont); err == nil && cur == uploadedID(id) {
			return s.set.Set(ctx, KeyFont, "")
		}
	}
	return nil
}

// Resolved is the effective appearance.
func (s *Service) Resolved(ctx context.Context) Resolved {
	return Resolve(s.Config(ctx), s.Uploads(ctx))
}

// Stylesheet returns the CSS and its version (a hash of the content, so
// the versioned URL changes exactly when the CSS does).
func (s *Service) Stylesheet(ctx context.Context) (css []byte, version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.css == nil {
		s.css = CSS(s.Config(ctx), s.Uploads(ctx))
		sum := sha256.Sum256(s.css)
		s.version = hex.EncodeToString(sum[:8])
	}
	return s.css, s.version
}

// Invalidate drops the cached stylesheet (after any change, also one made
// behind the service's back such as a restore).
func (s *Service) Invalidate() {
	s.mu.Lock()
	s.css, s.version = nil, ""
	s.mu.Unlock()
}

func (s *Service) write(ctx context.Context, kv ...string) error {
	if s.set == nil {
		return errors.New("appearance: no settings store")
	}
	defer s.Invalidate()
	for i := 0; i+1 < len(kv); i += 2 {
		if err := s.set.Set(ctx, kv[i], kv[i+1]); err != nil {
			return err
		}
	}
	return nil
}

// SetPreset chooses a preset and drops the edited palette.
func (s *Service) SetPreset(ctx context.Context, id string) error {
	if !ValidPreset(id) {
		return invalid("there is no colour preset %q", clip(id))
	}
	return s.write(ctx, KeyPreset, id, KeyPalette, "")
}

// SetPalette stores an edited palette (validated again here).
func (s *Service) SetPalette(ctx context.Context, p Palette) error {
	m, err := ParsePalette(p)
	if err != nil {
		return invalid("%v", err)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return s.write(ctx, KeyPalette, string(b))
}

// ResetPalette drops the edited palette (back to the preset's colours).
func (s *Service) ResetPalette(ctx context.Context) error {
	return s.write(ctx, KeyPalette, "")
}

// SetFont chooses the app font: a system stack, a bundled or an uploaded
// font.
func (s *Service) SetFont(ctx context.Context, id string) error {
	if _, ok := FontByID(id, s.Uploads(ctx)); !ok {
		return invalid("there is no font %q", clip(id))
	}
	return s.write(ctx, KeyFont, id)
}

// SetScale chooses the size step.
func (s *Service) SetScale(ctx context.Context, id string) error {
	if !ValidScale(id) {
		return invalid("there is no text size %q", clip(id))
	}
	return s.write(ctx, KeyScale, id)
}

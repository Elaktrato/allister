package appearance

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// mapStore is an in-memory settings store.
type mapStore struct {
	m    map[string]string
	fail error
}

func (s *mapStore) Get(_ context.Context, k string) (string, bool, error) {
	if s.fail != nil {
		return "", false, s.fail
	}
	v, ok := s.m[k]
	return v, ok, nil
}

func (s *mapStore) Set(_ context.Context, k, v string) error {
	if s.fail != nil {
		return s.fail
	}
	s.m[k] = v
	return nil
}

func TestServiceDefaultsAndCache(t *testing.T) {
	ctx := context.Background()
	st := &mapStore{m: map[string]string{}}
	s := NewService(st, nil)
	css, v := s.Stylesheet(ctx)
	if string(css) != string(CSS(Config{}, nil)) || len(v) != 16 {
		t.Fatalf("default stylesheet %q version %q", css, v)
	}
	// cached: a change behind the service's back is not seen until Invalidate
	st.m[KeyPreset] = "paper"
	if _, v2 := s.Stylesheet(ctx); v2 != v {
		t.Fatal("not cached")
	}
	s.Invalidate()
	if _, v2 := s.Stylesheet(ctx); v2 == v {
		t.Fatal("Invalidate kept the old stylesheet")
	}
}

func TestServiceSettersValidateAndInvalidate(t *testing.T) {
	ctx := context.Background()
	st := &mapStore{m: map[string]string{}}
	s := NewService(st, nil)
	_, v0 := s.Stylesheet(ctx)

	if err := s.SetPreset(ctx, "forest"); err != nil {
		t.Fatal(err)
	}
	_, v1 := s.Stylesheet(ctx)
	if v1 == v0 || st.m[KeyPreset] != "forest" || st.m[KeyPalette] != "" {
		t.Fatalf("preset: %v %q", st.m, v1)
	}
	m := full()
	m["accent"] = "#123456"
	if err := s.SetPalette(ctx, m); err != nil {
		t.Fatal(err)
	}
	css, _ := s.Stylesheet(ctx)
	if !strings.Contains(string(css), "--al-accent: 18 52 86;") || !strings.HasPrefix(st.m[KeyPalette], "{") {
		t.Fatalf("palette not applied: %s", st.m[KeyPalette])
	}
	// choosing a preset again drops the edited palette
	if err := s.SetPreset(ctx, "forest"); err != nil || st.m[KeyPalette] != "" {
		t.Fatalf("preset kept the palette: %v", err)
	}
	if err := s.SetPalette(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetPalette(ctx); err != nil || st.m[KeyPalette] != "" {
		t.Fatal("reset")
	}
	if err := s.SetFont(ctx, "system-serif"); err != nil || st.m[KeyFont] != "system-serif" {
		t.Fatal("font")
	}
	if err := s.SetScale(ctx, "large"); err != nil || st.m[KeyScale] != "large" {
		t.Fatal("scale")
	}

	before := len(st.m)
	for name, err := range map[string]error{
		"preset":  s.SetPreset(ctx, "nope"),
		"preset2": s.SetPreset(ctx, "midnight;}"),
		"palette": s.SetPalette(ctx, Palette{"bg": "red"}),
		"font":    s.SetFont(ctx, "comic-sans"),
		"upload":  s.SetFont(ctx, "upload-1"),
		"scale":   s.SetScale(ctx, "huge"),
	} {
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
	if len(st.m) != before || st.m[KeyFont] != "system-serif" {
		t.Errorf("a refused value was stored: %v", st.m)
	}
}

// TestServiceInvalidStoredValuesFallBack: whatever the database holds, the
// stylesheet renders with the defaults for the broken values.
func TestServiceInvalidStoredValuesFallBack(t *testing.T) {
	ctx := context.Background()
	st := &mapStore{m: map[string]string{
		KeyPreset: "gone", KeyPalette: `{"bg":"red;}"}`, KeyFont: "x;}", KeyScale: "</style>",
	}}
	css, _ := NewService(st, nil).Stylesheet(ctx)
	if string(css) != string(CSS(Config{}, nil)) {
		t.Errorf("broken settings did not fall back to the defaults:\n%s", css)
	}
	st.m[KeyPalette] = "not json"
	if c := NewService(st, nil).Config(ctx); c.Palette != nil {
		t.Error("a broken palette was returned")
	}
	// a failing store serves the defaults and refuses writes
	bad := &mapStore{fail: errors.New("disk")}
	s := NewService(bad, nil)
	if css, _ := s.Stylesheet(ctx); string(css) != string(CSS(Config{}, nil)) {
		t.Error("read error did not serve the defaults")
	}
	if err := s.SetPreset(ctx, "paper"); err == nil {
		t.Error("write error swallowed")
	}
	if css, _ := NewService(nil, nil).Stylesheet(ctx); string(css) != string(CSS(Config{}, nil)) {
		t.Error("nil store")
	}
	if err := NewService(nil, nil).SetScale(ctx, "large"); err == nil {
		t.Error("nil store accepted a write")
	}
}

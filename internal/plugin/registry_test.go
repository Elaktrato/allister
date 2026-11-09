package plugin_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

func ids(ps []plugin.Plugin) []string {
	out := []string{}
	for _, p := range ps {
		out = append(out, p.Manifest().ID)
	}
	return out
}

func eq(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }

func TestRegisterRejectsReservedAndDuplicateIDs(t *testing.T) {
	r := plugin.NewRegistry(plugintest.NewMemEnabled())
	for _, id := range []string{"list", "people", "settings", "covers", "static", "healthz", "api"} {
		err := r.Register(plugintest.BookPlugin(plugintest.WithID(id)))
		if err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("id %q: want reserved error, got %v", id, err)
		}
	}
	if err := r.Register(plugintest.BookPlugin()); err != nil {
		t.Fatal(err)
	}
	err := r.Register(plugintest.BookPlugin())
	if err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("duplicate: %v", err)
	}
	if got := ids(r.Plugins()); !eq(got, []string{"book"}) {
		t.Fatalf("plugins: %v", got)
	}
	if len(r.Problems()) != 8 {
		t.Fatalf("problems: %v", r.Problems())
	}
}

func TestRegistryEnabledPersistence(t *testing.T) {
	ctx := context.Background()
	store := plugintest.NewMemEnabled()
	r := plugin.NewRegistry(store)
	on := plugintest.BookPlugin(plugintest.WithID("on"))
	off := plugintest.TVPlugin(plugintest.WithID("off"), plugintest.WithDefaultEnabled(false))
	for _, p := range []plugin.Plugin{on, off} {
		if err := r.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	if !r.IsEnabled("on") || r.IsEnabled("off") || r.IsEnabled("ghost") {
		t.Fatal("default flags not respected")
	}
	if got := ids(r.Enabled()); !eq(got, []string{"on"}) {
		t.Fatalf("enabled: %v", got)
	}
	if err := r.SetEnabled(ctx, "off", true); err != nil {
		t.Fatal(err)
	}
	if err := r.SetEnabled(ctx, "on", false); err != nil {
		t.Fatal(err)
	}
	if v, _ := store.IsEnabled(ctx, "off", false); !v {
		t.Fatal("not persisted through the store")
	}
	if got := ids(r.Enabled()); !eq(got, []string{"off"}) {
		t.Fatalf("enabled: %v", got)
	}
	if got := ids(r.Plugins()); !eq(got, []string{"on", "off"}) {
		t.Fatalf("plugins must keep registration order and include disabled: %v", got)
	}
	// a fresh registry on the same store sees the persisted choice
	r2 := plugin.NewRegistry(store)
	_ = r2.Register(plugintest.BookPlugin(plugintest.WithID("on")))
	if r2.IsEnabled("on") {
		t.Fatal("persisted disable lost")
	}
	if err := r.SetEnabled(ctx, "ghost", true); err == nil {
		t.Fatal("unknown id must error")
	}
	if _, ok := r.Get("on"); !ok {
		t.Fatal("Get on")
	}
	if _, ok := r.Get("ghost"); ok {
		t.Fatal("Get ghost")
	}
	store.SetErr(errors.New("disk full"))
	if err := r.SetEnabled(ctx, "on", true); err == nil {
		t.Fatal("store error must surface")
	}
	// unreadable store falls back to the manifest default (true for "on")
	if !r.IsEnabled("on") {
		t.Fatal("broken store must fall back to the default")
	}
}

func TestRegistryKeepsGoodPluginsWhenOneIsRefused(t *testing.T) {
	r := plugin.NewRegistry(plugintest.NewMemEnabled())
	bad := plugintest.TVPlugin(plugintest.WithID("badtv"))
	bad.M.Kinds[0].TitleLanguages = nil
	old := plugintest.BookPlugin(plugintest.WithID("oldbook"))
	old.M.ContractVersion = 2
	for _, p := range []plugin.Plugin{plugintest.BookPlugin(), bad, old, plugintest.TVPlugin()} {
		_ = r.Register(p)
	}
	if got := ids(r.Plugins()); !eq(got, []string{"book", "tv"}) {
		t.Fatalf("plugins: %v", got)
	}
	probs := r.Problems()
	if len(probs) != 2 {
		t.Fatalf("problems: %v", probs)
	}
	if !strings.Contains(probs[0], `"badtv"`) || !strings.Contains(probs[0], "title_languages") {
		t.Errorf("problem 0: %s", probs[0])
	}
	if !strings.Contains(probs[1], `"oldbook"`) || !strings.Contains(probs[1], "contract 2") {
		t.Errorf("problem 1: %s", probs[1])
	}
	probs[0] = "mutated"
	if r.Problems()[0] == "mutated" {
		t.Error("Problems must return a copy")
	}
}

type panicPlugin struct{ plugin.Plugin }

func (panicPlugin) Manifest() plugin.Manifest { panic("boom") }

func TestRegisterSurvivesNilAndPanickingPlugins(t *testing.T) {
	r := plugin.NewRegistry(plugintest.NewMemEnabled())
	if err := r.Register(nil); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("nil: %v", err)
	}
	var typedNil *plugintest.Plugin
	if err := r.Register(typedNil); err == nil {
		t.Fatal("typed nil must be refused")
	}
	err := r.Register(panicPlugin{})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("panic: %v", err)
	}
	if err := r.Register(plugintest.BookPlugin()); err != nil {
		t.Fatal(err)
	}
	if len(r.Problems()) != 3 || len(r.Plugins()) != 1 {
		t.Fatalf("problems %v plugins %v", r.Problems(), ids(r.Plugins()))
	}
}

func TestRegistryConcurrentUse(t *testing.T) {
	ctx := context.Background()
	r := plugin.NewRegistry(plugintest.NewMemEnabled())
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		id := "p" + string(rune('a'+i))
		wg.Add(3)
		go func() { defer wg.Done(); _ = r.Register(plugintest.BookPlugin(plugintest.WithID(id))) }()
		go func() { defer wg.Done(); _ = r.SetEnabled(ctx, id, false) }()
		go func() { defer wg.Done(); _ = r.Enabled(); _ = r.Plugins(); _ = r.Problems(); r.IsEnabled(id) }()
	}
	wg.Wait()
	if n := len(r.Plugins()); n != 20 {
		t.Fatalf("plugins: %d", n)
	}
}

func TestManifestSnapshotIsImmutable(t *testing.T) {
	r := plugin.NewRegistry(plugintest.NewMemEnabled())
	p := plugintest.BookPlugin()
	if err := r.Register(p); err != nil {
		t.Fatal(err)
	}
	want, ok := r.Manifest("book")
	if !ok {
		t.Fatal("manifest missing")
	}
	// mutate the plugin's own manifest after registration, including nested data
	p.M.Name = "changed"
	p.M.Kinds[0].Label = "changed"
	p.M.Kinds[0].TitleLanguages[0] = "xx"
	*p.M.Kinds[0].Fields[1].Min = 99
	p.M.Kinds[0].Progress.Eligible[0].TotalField = "changed"
	p.M.Kinds[0].Progress.Combinations[0][0] = "changed"
	p.M.Sources[0].URLPatterns[0] = "changed"
	p.M.DefaultEnabled = false
	if got, _ := r.Manifest("book"); !reflect.DeepEqual(got, want) {
		t.Fatalf("plugin mutation leaked into the registry:\n got %+v\nwant %+v", got, want)
	}
	// mutate a manifest handed out by the registry
	got, _ := r.Manifest("book")
	got.Name = "changed"
	got.Kinds[0].Label = "changed"
	got.Kinds[0].TitleLanguages[0] = "xx"
	*got.Kinds[0].Fields[1].Min = 77
	got.Kinds[0].Progress.Defaults[0] = "changed"
	got.Sources[0].URLPatterns[0] = "changed"
	if again, _ := r.Manifest("book"); !reflect.DeepEqual(again, want) {
		t.Fatalf("returned manifest shares state with the registry:\n got %+v\nwant %+v", again, want)
	}
	all := r.Manifests()
	all[0].Kinds[0].Fields[1].Label = "changed"
	if again, _ := r.Manifest("book"); !reflect.DeepEqual(again, want) {
		t.Fatal("Manifests() shares state with the registry")
	}
	if err := plugin.Validate(want); err != nil {
		t.Fatalf("snapshot must be valid: %v", err)
	}
}

func TestRegistryManifestsInRegistrationOrder(t *testing.T) {
	r := plugin.NewRegistry(plugintest.NewMemEnabled())
	for _, id := range []string{"zeta", "alpha", "mid"} {
		if err := r.Register(plugintest.TVPlugin(plugintest.WithID(id))); err != nil {
			t.Fatal(err)
		}
	}
	_ = r.Register(plugintest.TVPlugin(plugintest.WithID("api"))) // refused, absent
	var got []string
	for _, m := range r.Manifests() {
		got = append(got, m.ID)
	}
	if !eq(got, []string{"zeta", "alpha", "mid"}) {
		t.Fatalf("got %v", got)
	}
}

func TestManifestUnknownID(t *testing.T) {
	r := plugin.NewRegistry(plugintest.NewMemEnabled())
	_ = r.Register(plugintest.BookPlugin())
	if m, ok := r.Manifest("ghost"); ok || m.ID != "" {
		t.Fatalf("got %+v %v", m, ok)
	}
}

func TestDefaultEnabledComesFromSnapshot(t *testing.T) {
	r := plugin.NewRegistry(plugintest.NewMemEnabled())
	p := plugintest.BookPlugin()
	_ = r.Register(p)
	p.M.DefaultEnabled = false // after registration: must have no effect
	if !r.IsEnabled("book") || len(r.Enabled()) != 1 {
		t.Fatal("default flag was read from the live plugin")
	}
	q := plugintest.BookPlugin(plugintest.WithID("late"), plugintest.WithDefaultEnabled(false))
	_ = r.Register(q)
	q.M.DefaultEnabled = true
	if r.IsEnabled("late") {
		t.Fatal("default flag was read from the live plugin")
	}
}

func TestMemEnabledZeroValue(t *testing.T) {
	var s plugintest.MemEnabled
	ctx := context.Background()
	if v, err := s.IsEnabled(ctx, "x", true); err != nil || !v {
		t.Fatal(v, err)
	}
	if err := s.SetEnabled(ctx, "x", false); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.IsEnabled(ctx, "x", true); v {
		t.Fatal("not stored")
	}
}

func TestDateOnlyNormalisesToUTCMidnight(t *testing.T) {
	loc := time.FixedZone("x", 5*3600)
	got := plugin.DateOnly(time.Date(2024, 3, 5, 23, 30, 0, 0, loc))
	if want := time.Date(2024, 3, 5, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

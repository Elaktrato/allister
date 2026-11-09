package plugin_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

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

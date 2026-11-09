package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// EnabledStore persists whether a plugin is switched on. def is the manifest's
// DefaultEnabled, returned when nothing is stored.
type EnabledStore interface {
	IsEnabled(ctx context.Context, id string, def bool) (bool, error)
	SetEnabled(ctx context.Context, id string, on bool) error
}

type entry struct {
	p Plugin
	m Manifest // validated deep copy taken at registration; the only manifest data the registry reads
}

// cloneManifest deep-copies m through its JSON form, which also exercises the
// contract's serialisable guarantee. Observable effects: empty non-nil slices
// come back nil, and a manifest that cannot be marshalled (for example a NaN or
// Inf bound) yields an error, which Register reports as "not serialisable".
func cloneManifest(m Manifest) (Manifest, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return Manifest{}, err
	}
	var out Manifest
	if err := json.Unmarshal(b, &out); err != nil {
		return Manifest{}, err
	}
	return out, nil
}

// Registry holds the installed plugins in registration order. It is safe for
// concurrent use. A plugin that is refused never prevents the others from
// loading; the reasons are listed by Problems.
type Registry struct {
	store EnabledStore

	mu       sync.RWMutex
	entries  []entry
	byID     map[string]entry
	problems []string
}

// NewRegistry returns an empty registry that persists enable flags in store.
func NewRegistry(store EnabledStore) *Registry {
	return &Registry{store: store, byID: map[string]entry{}}
}

// Register validates p and adds it. It calls p.Manifest() exactly once, deep
// copies the result, validates the copy and keeps only that snapshot: later
// changes to the plugin's manifest have no effect on the registry. It refuses nil or panicking
// implementations, invalid manifests (see Validate), reserved ids and ids that
// are already registered. Every refusal is recorded in Problems and returned.
func (r *Registry) Register(p Plugin) (err error) {
	id := "<unknown>"
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("plugin %q: refused, implementation panicked: %v", id, rec)
		}
		if err != nil {
			r.mu.Lock()
			r.problems = append(r.problems, err.Error())
			r.mu.Unlock()
		}
	}()
	if p == nil {
		return fmt.Errorf("plugin %q: refused, implementation is nil", id)
	}
	raw := p.Manifest() // called once; a nil pointer implementation panics here and is recovered
	id = raw.ID
	m, cerr := cloneManifest(raw)
	if cerr != nil {
		// prefer the precise validation reasons when there are any
		if verr := Validate(raw); verr != nil {
			return fmt.Errorf("plugin %q: refused, invalid manifest: %s", id, strings.ReplaceAll(verr.Error(), "\n", "; "))
		}
		return fmt.Errorf("plugin %q: refused, manifest is not serialisable: %v", id, cerr)
	}
	if verr := Validate(m); verr != nil {
		return fmt.Errorf("plugin %q: refused, invalid manifest: %s", id, strings.ReplaceAll(verr.Error(), "\n", "; "))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.byID[m.ID]; dup {
		return fmt.Errorf("plugin %q: refused, id already registered", id)
	}
	e := entry{p: p, m: m}
	r.entries = append(r.entries, e)
	r.byID[m.ID] = e
	return nil
}

// Manifest returns a deep copy of the validated manifest snapshot of the
// installed plugin (enabled or not); callers cannot modify registry state.
func (r *Registry) Manifest(id string) (Manifest, bool) {
	r.mu.RLock()
	e, ok := r.byID[id]
	r.mu.RUnlock()
	if !ok {
		return Manifest{}, false
	}
	return mustClone(e.m), true
}

// Manifests returns deep copies of every installed manifest in registration order.
func (r *Registry) Manifests() []Manifest {
	r.mu.RLock()
	entries := append([]entry(nil), r.entries...)
	r.mu.RUnlock()
	out := make([]Manifest, len(entries))
	for i, e := range entries {
		out[i] = mustClone(e.m)
	}
	return out
}

// mustClone copies a manifest that already survived cloneManifest once, so it
// cannot fail.
func mustClone(m Manifest) Manifest {
	c, err := cloneManifest(m)
	if err != nil {
		panic("plugin: snapshot no longer serialisable: " + err.Error())
	}
	return c
}

// Problems lists the refused plugins with their reasons, in the order they
// were refused. The slice is a copy.
func (r *Registry) Problems() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.problems...)
}

// Plugins returns every installed plugin, enabled or not, in registration order.
func (r *Registry) Plugins() []Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Plugin, len(r.entries))
	for i, e := range r.entries {
		out[i] = e.p
	}
	return out
}

// Enabled returns the installed plugins that are switched on, in registration order.
func (r *Registry) Enabled() []Plugin {
	r.mu.RLock()
	entries := append([]entry(nil), r.entries...)
	r.mu.RUnlock()
	var out []Plugin
	for _, e := range entries {
		if r.enabled(e) {
			out = append(out, e.p)
		}
	}
	return out
}

// Get returns the installed plugin with the given id (enabled or not).
func (r *Registry) Get(id string) (Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.byID[id]
	return e.p, ok
}

// IsEnabled reports whether the plugin is installed and switched on. With no
// stored choice (or an unreadable store) it falls back to the manifest's
// DefaultEnabled. Unknown ids are not enabled.
func (r *Registry) IsEnabled(id string) bool {
	r.mu.RLock()
	e, ok := r.byID[id]
	r.mu.RUnlock()
	return ok && r.enabled(e)
}

func (r *Registry) enabled(e entry) bool {
	on, err := r.store.IsEnabled(context.Background(), e.m.ID, e.m.DefaultEnabled)
	if err != nil {
		return e.m.DefaultEnabled
	}
	return on
}

// SetEnabled persists the choice. It fails for unknown ids and when the store fails.
func (r *Registry) SetEnabled(ctx context.Context, id string, on bool) error {
	r.mu.RLock()
	_, ok := r.byID[id]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("plugin %q: not installed", id)
	}
	return r.store.SetEnabled(ctx, id, on)
}

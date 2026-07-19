package toolschema

import (
	"fmt"
	"sync"
)

// Registry is a thread-safe, in-memory holder for the set of registered
// apps, loaded from YAML files on disk (see LoadDir) — this is the
// document-storage sibling of the original Postgres-backed Registry (see
// git history for the old database-backed version). This POC's client-tools
// mechanism (server/internal/clienttools) has no database of its own, so
// there is nothing to persist: an app's tool set is exactly what's on disk
// under its directory at process start (or the last Reload).
//
// All reads go through an in-memory snapshot refreshed by Reload/NewRegistry
// — a session resolving its tool set on "hello" shouldn't pay a filesystem
// walk per connection.
type Registry struct {
	dir string
	mu  sync.RWMutex
	// apps and order together preserve the dependency this POC needs: Get/All
	// callers see a stable set keyed by AppID, but tool declaration order
	// within an app (which affects nothing functionally, but matters for
	// reproducible codegen/debug output) follows LoadDir's file-then-YAML
	// order, not Go map iteration order.
	apps map[string]*App
}

// NewRegistry loads every *.yaml/*.yml App definition under dir once and
// returns a Registry serving that snapshot. dir is created if missing (an
// empty tools directory is a valid, if unusual, starting state — not an
// error) so a fresh checkout doesn't need a manual mkdir before first run.
func NewRegistry(dir string) (*Registry, error) {
	r := &Registry{dir: dir}
	if err := r.Reload(); err != nil {
		return nil, err
	}
	return r, nil
}

// Get returns the app for id, and whether it was found.
func (r *Registry) Get(id string) (*App, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	app, ok := r.apps[id]
	return app, ok
}

// All returns a snapshot copy of every loaded app, keyed by appId. Safe to
// range over without holding the Registry's lock — callers get their own
// map, not a reference into internal state.
func (r *Registry) All() map[string]*App {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]*App, len(r.apps))
	for k, v := range r.apps {
		out[k] = v
	}
	return out
}

// Reload re-reads every *.yaml/*.yml file under dir and atomically swaps
// the result into memory. On error (a malformed file, a duplicate appId),
// the previous in-memory set is left untouched — a bad edit to one YAML
// file must not take down every already-loaded app.
func (r *Registry) Reload() error {
	apps, err := LoadDir(r.dir)
	if err != nil {
		return fmt.Errorf("toolschema: reload from %s: %w", r.dir, err)
	}
	r.mu.Lock()
	r.apps = apps
	r.mu.Unlock()
	return nil
}

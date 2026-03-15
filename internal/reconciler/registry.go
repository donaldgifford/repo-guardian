package reconciler

import (
	"fmt"

	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// Factory creates a Reconciler from the given configuration.
type Factory func(config policy.ReconcilerConfig) (Reconciler, error)

// Registry holds reconciler factories keyed by type name.
type Registry struct {
	factories map[string]Factory
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]Factory)}
}

// Register adds a factory for the given reconciler type name.
func (r *Registry) Register(name string, factory Factory) {
	r.factories[name] = factory
}

// Build creates a Reconciler from the given config using the registered factory.
// Returns an error if no factory is registered for the config's type.
func (r *Registry) Build(config policy.ReconcilerConfig) (Reconciler, error) {
	factory, ok := r.factories[config.Type]
	if !ok {
		return nil, fmt.Errorf("unknown reconciler type: %q", config.Type)
	}

	return factory(config)
}

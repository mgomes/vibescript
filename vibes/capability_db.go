package vibes

import (
	"github.com/mgomes/vibescript/internal/runtime"
	"github.com/mgomes/vibescript/vibes/capability/db"
)

// NewDBCapability constructs a database CapabilityAdapter bound to the
// provided script-facing name. The adapter wraps a *db.Capability built
// from impl and dispatches the db.find/query/update/sum/each builtins.
// Use db.NewCapability directly when you only need the per-method
// dispatchers and intend to build a custom adapter.
func NewDBCapability(name string, impl db.Database) (CapabilityAdapter, error) {
	return runtime.NewDBCapability(name, impl)
}

// MustNewDBCapability constructs a database CapabilityAdapter or panics
// when name is empty or impl is a nil implementation.
func MustNewDBCapability(name string, impl db.Database) CapabilityAdapter {
	cap, err := NewDBCapability(name, impl)
	if err != nil {
		panic(err)
	}
	return cap
}

// Package db provides the host-side database capability adapter for
// Vibescript. It exposes a thin Database interface plus the request
// structs that scripts can call via the db.* methods bound on a script
// invocation. The vibes package re-exports the types via aliases and
// wraps the capability behind a CapabilityAdapter implementation so
// existing embedders continue to compile while new code can depend on
// the carved subpackage directly.
package db

import (
	"context"
	"fmt"

	"github.com/mgomes/vibescript/vibes/internal/capabilitycontract"
	"github.com/mgomes/vibescript/vibes/value"
)

// Database exposes data access capability methods to scripts.
type Database interface {
	Find(ctx context.Context, req DBFindRequest) (value.Value, error)
	Query(ctx context.Context, req DBQueryRequest) (value.Value, error)
	Update(ctx context.Context, req DBUpdateRequest) (value.Value, error)
	Sum(ctx context.Context, req DBSumRequest) (value.Value, error)
	Each(ctx context.Context, req DBEachRequest) ([]value.Value, error)
}

// DBFindRequest captures db.find calls.
type DBFindRequest struct {
	Collection string
	ID         value.Value
	Options    map[string]value.Value
}

// DBQueryRequest captures db.query calls.
type DBQueryRequest struct {
	Collection string
	Options    map[string]value.Value
}

// DBUpdateRequest captures db.update calls.
type DBUpdateRequest struct {
	Collection string
	ID         value.Value
	Attributes map[string]value.Value
	Options    map[string]value.Value
}

// DBSumRequest captures db.sum calls.
type DBSumRequest struct {
	Collection string
	Field      string
	Options    map[string]value.Value
}

// DBEachRequest captures db.each calls.
type DBEachRequest struct {
	Collection string
	Options    map[string]value.Value
}

// ExecutionContext describes the slice of the vibes runtime the db
// capability calls into. *vibes.Execution satisfies it structurally.
// Defining the interface here keeps the db package free of an import
// of vibes and so prevents the import cycle the alias bridge would
// otherwise create.
type ExecutionContext interface {
	Context() context.Context
	Step() error
	CallBlock(block value.Value, args []value.Value) (value.Value, error)
}

// Contract pairs the boundary validators registered for a single
// capability method. The vibes alias bridge converts this into
// vibes.CapabilityMethodContract before handing it to the runtime.
type Contract struct {
	ValidateArgs   func(args []value.Value, kwargs map[string]value.Value, block value.Value) error
	ValidateReturn func(result value.Value) error
}

// NewCapability constructs a database capability adapter bound to the
// provided script-facing name. The returned *Capability holds the
// per-call dispatchers; package vibes wraps it in a CapabilityAdapter
// for installation on a script invocation.
func NewCapability(name string, db Database) (*Capability, error) {
	if name == "" {
		return nil, fmt.Errorf("vibes: database capability name must be non-empty")
	}
	if capabilitycontract.IsNilImplementation(db) {
		return nil, fmt.Errorf("vibes: database capability requires a non-nil implementation")
	}
	return &Capability{name: name, db: db}, nil
}

// MustNewCapability constructs a database capability adapter or panics
// when name is empty or db is a nil implementation.
func MustNewCapability(name string, db Database) *Capability {
	cap, err := NewCapability(name, db)
	if err != nil {
		panic(err)
	}
	return cap
}

// Capability is the concrete adapter returned by NewCapability. The
// per-method Call* functions accept an ExecutionContext so the package
// stays free of a vibes runtime dependency.
type Capability struct {
	name string
	db   Database
}

// Name returns the script-facing name the capability was bound under.
func (c *Capability) Name() string { return c.name }

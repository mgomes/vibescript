package vibes

import (
	"github.com/mgomes/vibescript/internal/runtime"
	"github.com/mgomes/vibescript/vibes/capability/db"
)

// Type aliases for the database capability types that moved to
// vibes/capability/db. They keep the vibes package surface
// source-compatible with embedders. Scheduled for removal in v0.29.0
// per PR-3.5 in the Effective-Go sweep.
type (
	Database        = db.Database
	DBFindRequest   = db.DBFindRequest
	DBQueryRequest  = db.DBQueryRequest
	DBUpdateRequest = db.DBUpdateRequest
	DBSumRequest    = db.DBSumRequest
	DBEachRequest   = db.DBEachRequest
)

// NewDBCapability constructs a database capability adapter bound to
// the provided script-facing name. Forwards to db.NewCapability and
// wraps the result behind a CapabilityAdapter.
func NewDBCapability(name string, impl Database) (CapabilityAdapter, error) {
	return runtime.NewDBCapability(name, impl)
}

// MustNewDBCapability constructs a database capability adapter or
// panics when name is empty or impl is a nil implementation.
func MustNewDBCapability(name string, impl Database) CapabilityAdapter {
	cap, err := NewDBCapability(name, impl)
	if err != nil {
		panic(err)
	}
	return cap
}

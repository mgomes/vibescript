package vibes

import (
	"context"
	"fmt"
)

// Database exposes data access capability methods to scripts.
type Database interface {
	Find(ctx context.Context, req DBFindRequest) (Value, error)
	Query(ctx context.Context, req DBQueryRequest) (Value, error)
	Update(ctx context.Context, req DBUpdateRequest) (Value, error)
	Sum(ctx context.Context, req DBSumRequest) (Value, error)
	Each(ctx context.Context, req DBEachRequest) ([]Value, error)
}

// DBFindRequest captures db.find calls.
type DBFindRequest struct {
	Collection string
	ID         Value
	Options    map[string]Value
}

// DBQueryRequest captures db.query calls.
type DBQueryRequest struct {
	Collection string
	Options    map[string]Value
}

// DBUpdateRequest captures db.update calls.
type DBUpdateRequest struct {
	Collection string
	ID         Value
	Attributes map[string]Value
	Options    map[string]Value
}

// DBSumRequest captures db.sum calls.
type DBSumRequest struct {
	Collection string
	Field      string
	Options    map[string]Value
}

// DBEachRequest captures db.each calls.
type DBEachRequest struct {
	Collection string
	Options    map[string]Value
}

// NewDBCapability constructs a capability adapter bound to the provided name.
func NewDBCapability(name string, db Database) (CapabilityAdapter, error) {
	if name == "" {
		return nil, fmt.Errorf("vibes: database capability name must be non-empty")
	}
	if isNilCapabilityImplementation(db) {
		return nil, fmt.Errorf("vibes: database capability requires a non-nil implementation")
	}
	return &dbCapability{name: name, db: db}, nil
}

// MustNewDBCapability constructs a capability adapter or panics on invalid arguments.
func MustNewDBCapability(name string, db Database) CapabilityAdapter {
	cap, err := NewDBCapability(name, db)
	if err != nil {
		panic(err)
	}
	return cap
}

type dbCapability struct {
	name string
	db   Database
}

// Package contextcap provides a data-only capability adapter that resolves
// call-scoped context values into a script-visible hash or object.
//
// Resolvers receive the request context.Context and return a value.Value of
// kind Hash or Object. Returned values are validated to be data-only (no
// callables, no cycles) and are deep-cloned before being handed to the
// script runtime so subsequent mutations cannot leak across calls.
package contextcap

import (
	"context"
	"fmt"
	"reflect"
	"slices"

	"github.com/mgomes/vibescript/vibes/value"
)

// Resolver resolves call-scoped context data for script access.
type Resolver func(ctx context.Context) (value.Value, error)

// Capability is the data-only context capability implementation. Its Bind
// method takes a context.Context so the package stays free of vibes imports;
// the vibes facade wraps it to satisfy vibes.CapabilityAdapter.
type Capability struct {
	name     string
	resolver Resolver
}

// NewCapability constructs a data-only context capability.
func NewCapability(name string, resolver Resolver) (*Capability, error) {
	if name == "" {
		return nil, fmt.Errorf("vibes: context capability name must be non-empty")
	}
	if resolver == nil {
		return nil, fmt.Errorf("vibes: context capability requires a resolver")
	}
	return &Capability{name: name, resolver: resolver}, nil
}

// MustNewCapability constructs a capability or panics on invalid arguments.
func MustNewCapability(name string, resolver Resolver) *Capability {
	cap, err := NewCapability(name, resolver)
	if err != nil {
		panic(err)
	}
	return cap
}

// Name returns the script-visible binding name.
func (c *Capability) Name() string { return c.name }

// Bind resolves the underlying value, validates that it is data-only and
// non-cyclic, and returns a deep-cloned copy keyed by the capability name.
func (c *Capability) Bind(ctx context.Context) (map[string]value.Value, error) {
	val, err := c.resolver(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve %s capability: %w", c.name, err)
	}
	if val.Kind() != value.KindHash && val.Kind() != value.KindObject {
		return nil, fmt.Errorf("%s capability resolver must return hash/object", c.name)
	}
	label := c.name + " capability value"
	if err := validateDataOnly(label, val); err != nil {
		return nil, err
	}
	return map[string]value.Value{c.name: deepClone(val)}, nil
}

// validateDataOnly rejects values that embed callables or cyclic references.
// The contextcap package intentionally inlines this check rather than
// depending on the parent vibes package: only data-shaped kinds (Array,
// Hash, Object) require traversal, so a self-contained scanner suffices.
func validateDataOnly(label string, val value.Value) error {
	if newCallableScanner().containsCallable(val) {
		return fmt.Errorf("%s must be data-only", label)
	}
	if newCycleScanner().containsCycle(val) {
		return fmt.Errorf("%s must not contain cyclic references", label)
	}
	return nil
}

type callableScanner struct {
	seenArrays map[sliceID]struct{}
	seenMaps   map[uintptr]struct{}
}

func newCallableScanner() *callableScanner {
	return &callableScanner{
		seenArrays: make(map[sliceID]struct{}),
		seenMaps:   make(map[uintptr]struct{}),
	}
}

func (s *callableScanner) containsCallable(val value.Value) bool {
	switch val.Kind() {
	case value.KindFunction, value.KindBuiltin, value.KindBlock, value.KindClass, value.KindInstance:
		return true
	case value.KindArray:
		values := val.Array()
		id := identityOf(values)
		if _, seen := s.seenArrays[id]; seen {
			return false
		}
		s.seenArrays[id] = struct{}{}
		return slices.ContainsFunc(values, s.containsCallable)
	case value.KindHash, value.KindObject:
		entries := val.Hash()
		ptr := reflect.ValueOf(entries).Pointer()
		if _, seen := s.seenMaps[ptr]; seen {
			return false
		}
		s.seenMaps[ptr] = struct{}{}
		for _, item := range entries {
			if s.containsCallable(item) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

type cycleScanner struct {
	visitingArrays map[sliceID]struct{}
	visitingMaps   map[uintptr]struct{}
	seenArrays     map[sliceID]struct{}
	seenMaps       map[uintptr]struct{}
}

func newCycleScanner() *cycleScanner {
	return &cycleScanner{
		visitingArrays: make(map[sliceID]struct{}),
		visitingMaps:   make(map[uintptr]struct{}),
		seenArrays:     make(map[sliceID]struct{}),
		seenMaps:       make(map[uintptr]struct{}),
	}
}

func (s *cycleScanner) containsCycle(val value.Value) bool {
	switch val.Kind() {
	case value.KindArray:
		values := val.Array()
		id := identityOf(values)
		if _, seen := s.seenArrays[id]; seen {
			return false
		}
		if _, visiting := s.visitingArrays[id]; visiting {
			return true
		}
		s.visitingArrays[id] = struct{}{}
		if slices.ContainsFunc(values, s.containsCycle) {
			return true
		}
		delete(s.visitingArrays, id)
		s.seenArrays[id] = struct{}{}
		return false
	case value.KindHash, value.KindObject:
		entries := val.Hash()
		ptr := reflect.ValueOf(entries).Pointer()
		if _, seen := s.seenMaps[ptr]; seen {
			return false
		}
		if _, visiting := s.visitingMaps[ptr]; visiting {
			return true
		}
		s.visitingMaps[ptr] = struct{}{}
		for _, item := range entries {
			if s.containsCycle(item) {
				return true
			}
		}
		delete(s.visitingMaps, ptr)
		s.seenMaps[ptr] = struct{}{}
		return false
	default:
		return false
	}
}

type sliceID struct {
	Ptr uintptr
	Len int
	Cap int
}

func identityOf(values []value.Value) sliceID {
	return sliceID{
		Ptr: reflect.ValueOf(values).Pointer(),
		Len: len(values),
		Cap: cap(values),
	}
}

func deepClone(val value.Value) value.Value {
	switch val.Kind() {
	case value.KindArray:
		arr := val.Array()
		cloned := make([]value.Value, len(arr))
		for i, elem := range arr {
			cloned[i] = deepClone(elem)
		}
		return value.NewArray(cloned)
	case value.KindHash:
		hash := val.Hash()
		cloned := make(map[string]value.Value, len(hash))
		for k, v := range hash {
			cloned[k] = deepClone(v)
		}
		return value.NewHash(cloned)
	case value.KindObject:
		obj := val.Hash()
		cloned := make(map[string]value.Value, len(obj))
		for k, v := range obj {
			cloned[k] = deepClone(v)
		}
		return value.NewObject(cloned)
	default:
		return val
	}
}

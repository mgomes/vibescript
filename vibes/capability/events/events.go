// Package events defines the host-facing contract for the events capability
// that Vibescript exposes to scripts. The runtime wraps a *Capability with a
// script-visible adapter; embedders implement Publisher to back events.publish.
package events

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/mgomes/vibescript/vibes/value"
)

// Publisher exposes event publication capability methods to scripts.
type Publisher interface {
	Publish(ctx context.Context, req PublishRequest) (value.Value, error)
}

// PublishRequest captures events.publish calls from script code.
type PublishRequest struct {
	Topic   string
	Payload map[string]value.Value
	Options map[string]value.Value
}

// Capability binds a host Publisher implementation under a script-visible
// name. The vibes package wraps it in a CapabilityAdapter; embedders
// construct one via NewCapability.
type Capability struct {
	Name      string
	Publisher Publisher
}

// NewCapability validates the inputs and returns a bound Capability.
func NewCapability(name string, publisher Publisher) (*Capability, error) {
	if name == "" {
		return nil, fmt.Errorf("vibes: events capability name must be non-empty")
	}
	if isNilImpl(publisher) {
		return nil, fmt.Errorf("vibes: events capability requires a non-nil implementation")
	}
	return &Capability{Name: name, Publisher: publisher}, nil
}

// MustNewCapability is the panicking variant of NewCapability.
func MustNewCapability(name string, publisher Publisher) *Capability {
	cap, err := NewCapability(name, publisher)
	if err != nil {
		panic(err)
	}
	return cap
}

// PublishMethodName returns the dotted script-visible method name (for example
// "events.publish") for use in error messages and contract keys.
func (c *Capability) PublishMethodName() string { return c.Name + ".publish" }

// ValidatePublishArgs enforces the events.publish contract on script-supplied
// arguments. The vibes-side adapter wires this into the runtime contract and
// Publish calls it when embedders invoke the capability directly.
func (c *Capability) ValidatePublishArgs(args []value.Value, kwargs map[string]value.Value, blockProvided bool) error {
	method := c.PublishMethodName()
	if len(args) != 2 {
		return fmt.Errorf("%s expects topic and payload", method)
	}
	if blockProvided {
		return fmt.Errorf("%s does not accept blocks", method)
	}
	if _, err := nameArg(method, "topic", args[0]); err != nil {
		return err
	}
	if err := validateHashValue(method+" payload", args[1]); err != nil {
		return err
	}
	return validateKwargsDataOnly(method, kwargs)
}

// ValidatePublishReturn enforces the data-only contract on host return values.
// The vibes-side adapter wires this into CapabilityMethodContract.ValidateReturn.
func (c *Capability) ValidatePublishReturn(result value.Value) error {
	return validateAnyValue(c.PublishMethodName()+" return value", result)
}

// Publish runs the full publish path: validates args, builds the
// PublishRequest, delegates to the host Publisher, validates the return value,
// and deep-clones it so the host can't share mutable state with scripts.
func (c *Capability) Publish(ctx context.Context, args []value.Value, kwargs map[string]value.Value, blockProvided bool) (value.Value, error) {
	if err := c.ValidatePublishArgs(args, kwargs, blockProvided); err != nil {
		return value.NewNil(), err
	}
	return c.PublishValidated(ctx, args, kwargs, blockProvided)
}

// PublishValidated runs events.publish after the runtime has already enforced
// ValidatePublishArgs. Direct embedders should call Publish so invalid script
// arguments are still rejected before the host publisher runs.
func (c *Capability) PublishValidated(ctx context.Context, args []value.Value, kwargs map[string]value.Value, blockProvided bool) (value.Value, error) {
	req := PublishRequest{
		Topic:   args[0].String(),
		Payload: cloneHash(args[1].Hash()),
		Options: cloneKwargs(kwargs),
	}
	result, err := c.Publisher.Publish(ctx, req)
	if err != nil {
		return value.NewNil(), err
	}
	if err := c.ValidatePublishReturn(result); err != nil {
		return value.NewNil(), err
	}
	return deepClone(result), nil
}

// nameArg coerces a string or symbol argument into its underlying name,
// rejecting empty values and other kinds.
func nameArg(method, label string, val value.Value) (string, error) {
	switch val.Kind() {
	case value.KindString, value.KindSymbol:
		name := val.String()
		if strings.TrimSpace(name) == "" {
			return "", fmt.Errorf("%s expects %s as non-empty string or symbol", method, label)
		}
		return name, nil
	default:
		return "", fmt.Errorf("%s expects %s as string or symbol", method, label)
	}
}

// validateHashValue ensures val is hash-like (hash or object) whose
// contents are data-only. The pre-carve validateCapabilityHashValue
// accepted both KindHash and KindObject, and Value.Hash() resolves both,
// so callers that forward host objects as event payloads must continue
// to work.
func validateHashValue(label string, val value.Value) error {
	if val.Kind() != value.KindHash && val.Kind() != value.KindObject {
		return fmt.Errorf("%s expected hash, got %s", label, val.Kind())
	}
	return validateDataOnly(label, val)
}

// validateAnyValue accepts any kind so long as it is data-only and acyclic.
func validateAnyValue(label string, val value.Value) error {
	return validateDataOnly(label, val)
}

// validateKwargsDataOnly applies validateAnyValue to every kwarg entry.
func validateKwargsDataOnly(method string, kwargs map[string]value.Value) error {
	for key, val := range kwargs {
		if err := validateAnyValue(fmt.Sprintf("%s keyword %s", method, key), val); err != nil {
			return err
		}
	}
	return nil
}

// cloneKwargs returns nil for empty input; otherwise a deep clone so the host
// cannot mutate the script-side kwargs map.
func cloneKwargs(kwargs map[string]value.Value) map[string]value.Value {
	if len(kwargs) == 0 {
		return nil
	}
	return cloneHash(kwargs)
}

// isNilImpl reports whether impl is either an untyped nil or a typed-nil
// pointer/interface/etc. value.
func isNilImpl(impl any) bool {
	if impl == nil {
		return true
	}
	val := reflect.ValueOf(impl)
	switch val.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return val.IsNil()
	default:
		return false
	}
}

// validateDataOnly rejects values that embed callables or cyclic references.
// The events package inlines this check rather than depending on the parent
// vibes package: only data-shaped kinds (Array, Hash, Object) require
// traversal, so a self-contained scanner suffices.
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
		// A KindHash's default metadata lives outside its entry map, so two
		// wrappers can share one map yet carry different defaults. Key the
		// seen-set on the whole hash wrapper (falling back to the entry-map
		// pointer for objects, which never carry defaults) so a second wrapper's
		// callable default is not hidden by an earlier plain wrapper marking the
		// shared map seen.
		ptr := value.HashIdentity(val)
		if ptr == 0 {
			ptr = reflect.ValueOf(entries).Pointer()
		}
		if _, seen := s.seenMaps[ptr]; seen {
			return false
		}
		s.seenMaps[ptr] = struct{}{}
		for _, item := range entries {
			if s.containsCallable(item) {
				return true
			}
		}
		// A KindHash may carry Ruby-style default metadata outside its entry
		// map: a default value (itself possibly a callable or a collection of
		// callables) and a default proc (a KindBlock, always a callable). Scan
		// both so Hash.new { ... } or Hash.new(some_proc) cannot smuggle a
		// script callable past this data-only boundary.
		if s.containsCallable(value.HashDefaultValue(val)) {
			return true
		}
		return s.containsCallable(value.HashDefaultProc(val))
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
		// Key on the whole hash wrapper (falling back to the entry-map pointer for
		// objects) so default metadata is visited per wrapper, matching the
		// callable scan above.
		ptr := value.HashIdentity(val)
		if ptr == 0 {
			ptr = reflect.ValueOf(entries).Pointer()
		}
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
		// A KindHash's default value/proc are reachable hash state and may
		// themselves nest collections, so walk them for cycles too. They share
		// the same visiting set, so a default that references its own hash is
		// detected as a cycle like any other back-edge.
		if s.containsCycle(value.HashDefaultValue(val)) || s.containsCycle(value.HashDefaultProc(val)) {
			return true
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

// cloneHash deep-clones a string-keyed map of values, returning an empty map
// for an empty input (matching the existing vibes capability behavior).
func cloneHash(src map[string]value.Value) map[string]value.Value {
	if len(src) == 0 {
		return map[string]value.Value{}
	}
	out := make(map[string]value.Value, len(src))
	for k, v := range src {
		out[k] = deepClone(v)
	}
	return out
}

// deepClone returns a deep copy of val so the host cannot mutate state shared
// with a running script. Non-collection kinds are returned unchanged.
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
		// Preserve the hash's Ruby-style default metadata so the isolated copy
		// keeps the same missing-key behavior. The default value is deep-cloned
		// like an entry; the default proc is a runtime-only block, rejected by
		// validateDataOnly before reaching this clone, so it is copied by
		// reference rather than dropped.
		defaultProc := value.HashDefaultProc(val)
		defaultValue := value.HashDefaultValue(val)
		if defaultProc.Kind() == value.KindNil && defaultValue.Kind() == value.KindNil {
			return value.NewHash(cloned)
		}
		return value.NewHashWithDefault(cloned, deepClone(defaultValue), defaultProc)
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

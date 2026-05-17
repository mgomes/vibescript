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
// arguments. The vibes-side adapter calls this both at bind-time (as the
// CapabilityMethodContract.ValidateArgs hook) and at call-time before invoking
// the host publisher.
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
	topic, err := nameArg(c.PublishMethodName(), "topic", args[0])
	if err != nil {
		return value.NewNil(), err
	}
	req := PublishRequest{
		Topic:   topic,
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

// validateHashValue ensures val is a hash whose contents are data-only.
func validateHashValue(label string, val value.Value) error {
	if val.Kind() != value.KindHash {
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

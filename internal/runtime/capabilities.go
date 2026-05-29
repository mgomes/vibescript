package runtime

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
)

// CapabilityAdapter binds host capabilities into a script invocation.
type CapabilityAdapter interface {
	Bind(binding CapabilityBinding) (map[string]Value, error)
}

// CapabilityMethodContract validates capability method calls at the boundary.
// These contracts run before and after a capability builtin executes.
type CapabilityMethodContract struct {
	ValidateArgs   func(args []Value, kwargs map[string]Value, block Value) error
	ValidateReturn func(result Value) error
}

// CapabilityContractProvider exposes per-method contracts for capability adapters.
// Contract keys must match builtin method names exposed to scripts (for example "jobs.enqueue").
type CapabilityContractProvider interface {
	CapabilityContracts() map[string]CapabilityMethodContract
}

// CapabilityBinding provides execution context for adapters during binding.
type CapabilityBinding struct {
	Context context.Context
	Engine  *Engine
}

func cloneHash(src map[string]Value) map[string]Value {
	if len(src) == 0 {
		return map[string]Value{}
	}
	out := make(map[string]Value, len(src))
	for k, v := range src {
		out[k] = deepCloneValue(v)
	}
	return out
}

func deepCloneValue(val Value) Value {
	switch val.Kind() {
	case KindArray:
		arr := val.Array()
		cloned := make([]Value, len(arr))
		for i, elem := range arr {
			cloned[i] = deepCloneValue(elem)
		}
		return NewArray(cloned)
	case KindHash:
		hash := val.Hash()
		cloned := make(map[string]Value, len(hash))
		for k, v := range hash {
			cloned[k] = deepCloneValue(v)
		}
		return NewHash(cloned)
	case KindObject:
		obj := val.Hash()
		cloned := make(map[string]Value, len(obj))
		for k, v := range obj {
			cloned[k] = deepCloneValue(v)
		}
		return NewObject(cloned)
	default:
		return val
	}
}

func mergeHash(dest, src map[string]Value) map[string]Value {
	if len(src) == 0 {
		return dest
	}
	if dest == nil {
		dest = make(map[string]Value, len(src))
	}
	maps.Copy(dest, src)
	return dest
}

var (
	capabilityTypeAny = &TypeExpr{
		Name: "any",
		Kind: TypeAny,
	}
	capabilityTypeHash = &TypeExpr{
		Name: "hash",
		Kind: TypeHash,
	}
)

func cloneCapabilityKwargs(kwargs map[string]Value) map[string]Value {
	if len(kwargs) == 0 {
		return nil
	}
	return cloneHash(kwargs)
}

func validateCapabilityKwargsDataOnly(method string, kwargs map[string]Value) error {
	for key, val := range kwargs {
		if err := validateCapabilityTypedValue(fmt.Sprintf("%s keyword %s", method, key), val, capabilityTypeAny); err != nil {
			return err
		}
	}
	return nil
}

func validateCapabilityTypedValue(label string, val Value, ty *TypeExpr) error {
	if err := validateCapabilityDataOnlyValue(label, val); err != nil {
		return err
	}
	if err := checkValueType(val, ty); err != nil {
		var mismatch *typeMismatchError
		if errors.As(err, &mismatch) {
			return fmt.Errorf("%s expected %s, got %s", label, mismatch.Expected, mismatch.Actual)
		}
		return err
	}
	return nil
}

func validateCapabilityHashValue(label string, val Value) error {
	return validateCapabilityTypedValue(label, val, capabilityTypeHash)
}

func capabilityValidateAnyReturn(method string) func(result Value) error {
	return func(result Value) error {
		return validateCapabilityTypedValue(method+" return value", result, capabilityTypeAny)
	}
}

func cloneCapabilityMethodResult(method string, result Value) (Value, error) {
	if err := validateCapabilityTypedValue(method+" return value", result, capabilityTypeAny); err != nil {
		return NewNil(), err
	}
	return deepCloneValue(result), nil
}

type capabilityContractScanner struct {
	seenArrays    map[sliceIdentity]struct{}
	seenMaps      map[uintptr]struct{}
	seenClasses   map[*ClassDef]struct{}
	seenInstances map[*Instance]struct{}
	seenEnvs      map[*Env]struct{}
	// ambientEnvs are environments whose bindings are pre-existing ambient
	// globals (the execution root and its ancestors), NOT values a capability
	// freshly exposed. When walking a closure's captured environment we skip
	// these, so an unrelated global builtin whose name happens to match a
	// capability contract method is never bound to that scope through a
	// script-supplied closure. nil means "scan every env" (used by callers
	// that have no root context, e.g. binding adapter globals at setup).
	ambientEnvs map[*Env]struct{}
	excluded    map[*Builtin]struct{}
}

func newCapabilityContractScanner() *capabilityContractScanner {
	return &capabilityContractScanner{
		seenArrays:    make(map[sliceIdentity]struct{}),
		seenMaps:      make(map[uintptr]struct{}),
		seenClasses:   make(map[*ClassDef]struct{}),
		seenInstances: make(map[*Instance]struct{}),
		seenEnvs:      make(map[*Env]struct{}),
	}
}

// ambientEnvSet returns the set of environments reachable from root via the
// parent chain. Builtins bound in these envs are ambient globals, not
// capability-exposed values, and must not be contract-bound when encountered
// while walking a script-supplied closure's captured environment.
func ambientEnvSet(root *Env) map[*Env]struct{} {
	if root == nil {
		return nil
	}
	set := make(map[*Env]struct{})
	for env := root; env != nil; env = env.parent {
		if _, seen := set[env]; seen {
			break
		}
		set[env] = struct{}{}
	}
	return set
}

func validateCapabilityDataOnlyValue(label string, val Value) error {
	callableScanner := newCapabilityContractScanner()
	if callableScanner.containsCallable(val) {
		return fmt.Errorf("%s must be data-only", label)
	}
	cycleScanner := newCapabilityCycleScanner()
	if cycleScanner.containsCycle(val) {
		return fmt.Errorf("%s must not contain cyclic references", label)
	}
	return nil
}

func bindCapabilityContracts(
	val Value,
	scope *capabilityContractScope,
	target map[*Builtin]CapabilityMethodContract,
	scopes map[*Builtin]*capabilityContractScope,
) {
	bindCapabilityContractsExcluding(val, scope, target, scopes, nil)
}

func bindCapabilityContractsExcluding(
	val Value,
	scope *capabilityContractScope,
	target map[*Builtin]CapabilityMethodContract,
	scopes map[*Builtin]*capabilityContractScope,
	excluded map[*Builtin]struct{},
) {
	if scope == nil {
		return
	}
	scanner := newCapabilityContractScanner()
	scanner.excluded = excluded
	scanner.bindContracts(val, scope, target, scopes)
}

type capabilityCycleScanner struct {
	visitingArrays map[sliceIdentity]struct{}
	visitingMaps   map[uintptr]struct{}
	seenArrays     map[sliceIdentity]struct{}
	seenMaps       map[uintptr]struct{}
}

func newCapabilityCycleScanner() *capabilityCycleScanner {
	return &capabilityCycleScanner{
		visitingArrays: make(map[sliceIdentity]struct{}),
		visitingMaps:   make(map[uintptr]struct{}),
		seenArrays:     make(map[sliceIdentity]struct{}),
		seenMaps:       make(map[uintptr]struct{}),
	}
}

func (s *capabilityCycleScanner) containsCycle(val Value) bool {
	switch val.Kind() {
	case KindArray:
		values := val.Array()
		id := sliceIdentity{
			Ptr: reflect.ValueOf(values).Pointer(),
			Len: len(values),
			Cap: cap(values),
		}
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
	case KindHash, KindObject:
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

func (s *capabilityContractScanner) containsCallable(val Value) bool {
	switch val.Kind() {
	case KindFunction, KindBuiltin, KindBlock, KindClass, KindInstance:
		return true
	case KindArray:
		values := val.Array()
		id := sliceIdentity{
			Ptr: reflect.ValueOf(values).Pointer(),
			Len: len(values),
			Cap: cap(values),
		}
		if _, seen := s.seenArrays[id]; seen {
			return false
		}
		s.seenArrays[id] = struct{}{}
		return slices.ContainsFunc(values, s.containsCallable)
	case KindHash, KindObject:
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

func (s *capabilityContractScanner) bindContracts(
	val Value,
	scope *capabilityContractScope,
	target map[*Builtin]CapabilityMethodContract,
	scopes map[*Builtin]*capabilityContractScope,
) {
	switch val.Kind() {
	case KindBuiltin:
		builtin := valueBuiltin(val)
		if _, skip := s.excluded[builtin]; skip {
			return
		}
		if scope != nil && scope.knownBuiltins != nil {
			scope.knownBuiltins[builtin] = struct{}{}
		}
		ownerScope, seen := scopes[builtin]
		if !seen {
			scopes[builtin] = scope
			ownerScope = scope
		}
		if ownerScope != scope {
			return
		}
		if contract, ok := scope.contracts[builtin.Name]; ok {
			if _, seen := target[builtin]; !seen {
				target[builtin] = contract
			}
		}
	case KindArray:
		values := val.Array()
		id := sliceIdentity{
			Ptr: reflect.ValueOf(values).Pointer(),
			Len: len(values),
			Cap: cap(values),
		}
		if _, seen := s.seenArrays[id]; seen {
			return
		}
		s.seenArrays[id] = struct{}{}
		for _, item := range values {
			s.bindContracts(item, scope, target, scopes)
		}
	case KindHash, KindObject:
		entries := val.Hash()
		ptr := reflect.ValueOf(entries).Pointer()
		if _, seen := s.seenMaps[ptr]; seen {
			return
		}
		s.seenMaps[ptr] = struct{}{}
		for _, item := range entries {
			s.bindContracts(item, scope, target, scopes)
		}
	case KindClass:
		classDef := valueClass(val)
		if classDef == nil {
			return
		}
		if _, seen := s.seenClasses[classDef]; seen {
			return
		}
		s.seenClasses[classDef] = struct{}{}
		for _, item := range classDef.ClassVars {
			s.bindContracts(item, scope, target, scopes)
		}
	case KindInstance:
		instance := valueInstance(val)
		if instance == nil {
			return
		}
		if _, seen := s.seenInstances[instance]; seen {
			return
		}
		s.seenInstances[instance] = struct{}{}
		for _, item := range instance.Ivars {
			s.bindContracts(item, scope, target, scopes)
		}
		if instance.Class != nil {
			s.bindContracts(NewClass(instance.Class), scope, target, scopes)
		}
	case KindFunction:
		fn := valueFunction(val)
		if fn == nil {
			return
		}
		for env := fn.Env; env != nil; env = env.parent {
			// Stop at the ambient global chain: builtins bound there are
			// pre-existing globals, not values this capability exposed.
			// Binding them through a script-supplied closure would let an
			// unrelated global builtin whose name matches a contract method
			// be attached to this scope (CWE-862 regression). The remaining
			// ancestors are all ambient too, so stop the walk entirely.
			if _, ambient := s.ambientEnvs[env]; ambient {
				return
			}
			if _, seen := s.seenEnvs[env]; seen {
				return
			}
			s.seenEnvs[env] = struct{}{}
			for _, item := range env.values {
				s.bindContracts(item, scope, target, scopes)
			}
		}
	}
}

func (s *capabilityContractScanner) collectBuiltins(val Value, out map[*Builtin]struct{}) {
	switch val.Kind() {
	case KindBuiltin:
		out[valueBuiltin(val)] = struct{}{}
	case KindArray:
		values := val.Array()
		id := sliceIdentity{
			Ptr: reflect.ValueOf(values).Pointer(),
			Len: len(values),
			Cap: cap(values),
		}
		if _, seen := s.seenArrays[id]; seen {
			return
		}
		s.seenArrays[id] = struct{}{}
		for _, item := range values {
			s.collectBuiltins(item, out)
		}
	case KindHash, KindObject:
		entries := val.Hash()
		ptr := reflect.ValueOf(entries).Pointer()
		if _, seen := s.seenMaps[ptr]; seen {
			return
		}
		s.seenMaps[ptr] = struct{}{}
		for _, item := range entries {
			s.collectBuiltins(item, out)
		}
	case KindClass:
		classDef := valueClass(val)
		if classDef == nil {
			return
		}
		if _, seen := s.seenClasses[classDef]; seen {
			return
		}
		s.seenClasses[classDef] = struct{}{}
		for _, item := range classDef.ClassVars {
			s.collectBuiltins(item, out)
		}
	case KindInstance:
		instance := valueInstance(val)
		if instance == nil {
			return
		}
		if _, seen := s.seenInstances[instance]; seen {
			return
		}
		s.seenInstances[instance] = struct{}{}
		for _, item := range instance.Ivars {
			s.collectBuiltins(item, out)
		}
		if instance.Class != nil {
			s.collectBuiltins(NewClass(instance.Class), out)
		}
	case KindFunction:
		fn := valueFunction(val)
		if fn == nil {
			return
		}
		for env := fn.Env; env != nil; env = env.parent {
			// Skip the ambient global chain (see bindContracts for rationale):
			// its builtins are pre-existing globals, not capability-exposed
			// values, and the remaining ancestors are ambient too.
			if _, ambient := s.ambientEnvs[env]; ambient {
				return
			}
			if _, seen := s.seenEnvs[env]; seen {
				return
			}
			s.seenEnvs[env] = struct{}{}
			for _, item := range env.values {
				s.collectBuiltins(item, out)
			}
		}
	}
}

type strictGlobalsScanner struct {
	seenArrays map[sliceIdentity]struct{}
	seenMaps   map[uintptr]struct{}
}

func validateStrictGlobals(globals map[string]Value) error {
	if len(globals) == 0 {
		return nil
	}
	scanner := strictGlobalsScanner{
		seenArrays: make(map[sliceIdentity]struct{}),
		seenMaps:   make(map[uintptr]struct{}),
	}
	for name, val := range globals {
		if scanner.containsCallable(val) {
			return fmt.Errorf("strict effects: global %s must be data-only; use CallOptions.Capabilities for side effects", name)
		}
	}
	return nil
}

func (s *strictGlobalsScanner) containsCallable(val Value) bool {
	switch val.Kind() {
	case KindFunction, KindBuiltin, KindBlock, KindClass, KindInstance:
		return true
	case KindArray:
		values := val.Array()
		id := sliceIdentity{
			Ptr: reflect.ValueOf(values).Pointer(),
			Len: len(values),
			Cap: cap(values),
		}
		if _, seen := s.seenArrays[id]; seen {
			return false
		}
		s.seenArrays[id] = struct{}{}
		return slices.ContainsFunc(values, s.containsCallable)
	case KindHash, KindObject:
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

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
//
// ValidateReturn always runs after the builtin returns. The only exception is
// runtime-internal: a first-party builtin that has already validated and
// isolated its result records that fact through an unexported per-call proof
// on the Execution (markValidatedCapabilityReturn), which the dispatcher
// consumes to avoid validating the same value twice. Adapters outside this
// package cannot record that proof, so a host-supplied contract can never
// skip its declared return validation.
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

// cloneHash copies a capability's argument or option hash. This is boundary
// isolation -- the runtime handing the adapter its own copy -- not the
// script-visible dup, so a bag the runtime built keeps its provenance.
func cloneHash(src map[string]Value) map[string]Value {
	if len(src) == 0 {
		return map[string]Value{}
	}
	out := make(map[string]Value, len(src))
	for k, v := range src {
		out[k] = deepCloneValueForContainment(v)
	}
	return out
}

const deepCloneSmallSeenLimit = 8

type deepClonePtrEntry struct {
	id    uintptr
	value Value
}

type deepCloneState struct {
	arrays  map[uintptr]Value
	hashes  map[uintptr]Value
	objects map[uintptr]Value

	smallArrays  [deepCloneSmallSeenLimit]deepClonePtrEntry
	smallHashes  [deepCloneSmallSeenLimit]deepClonePtrEntry
	smallObjects [deepCloneSmallSeenLimit]deepClonePtrEntry
	arrayCount   int
	hashCount    int
	objectCount  int

	// preserveTags carries an attribute bag's provenance into the clone. It is
	// set only for the runtime's own containment clones, never for the
	// script-visible dup and clone, which share this helper: a bag script code
	// duplicates becomes an ordinary object, so the tag cannot be laundered
	// onto content the script then edits.
	preserveTags bool
}

// deepCloneValue is the script-visible duplication. A cloned attribute bag
// loses its provenance tag and renders as an ordinary object.
func deepCloneValue(val Value) Value {
	var state deepCloneState
	return deepCloneValueWithState(val, &state)
}

// deepCloneValueForContainment is the runtime copying a value to isolate it --
// into a task, across a boundary. The copy stands for the same value, so a
// recognized provenance tag survives.
func deepCloneValueForContainment(val Value) Value {
	state := deepCloneState{preserveTags: true}
	return deepCloneValueWithState(val, &state)
}

func deepCloneValueWithState(val Value, state *deepCloneState) Value {
	switch val.Kind() {
	case KindArray:
		// Key on the array wrapper identity so aliases of one mutable array
		// clone to one shared object and a cyclic array terminates here.
		arr := val.Array()
		id := arrayIdentity(val)
		if id != 0 {
			if cloned, ok := state.clonedArray(id); ok {
				return cloned
			}
		}
		cloned := make([]Value, len(arr))
		clonedValue := NewArray(cloned)
		state.rememberArray(id, clonedValue)
		for i, elem := range arr {
			cloned[i] = deepCloneValueWithState(elem, state)
		}
		return clonedValue
	case KindHash:
		// Preserve the hash's Ruby-style default metadata so the clone keeps the
		// same missing-key behavior. The default value is deep-cloned like an
		// entry; the default proc is a runtime-only block, copied by reference.
		id := hashIdentity(val)
		if id != 0 {
			if cloned, ok := state.clonedHash(id); ok {
				return cloned
			}
		}
		defaultProc := hashDefaultProc(val)
		defaultValue := hashDefaultValue(val)
		hasDefault := !defaultProc.IsNil() || !defaultValue.IsNil()
		clonedEntries := make(map[string]Value, val.HashLen())
		cloned := NewHash(clonedEntries)
		state.rememberHash(id, cloned)
		if hasDefault {
			cloned.SetHashDefaults(deepCloneValueWithState(defaultValue, state), defaultProc)
		}
		if hashHasTypedEntries(val) {
			var entryBuf [smallHashKeyBufferSize]HashEntry
			for _, entry := range val.HashEntriesInto(entryBuf[:]) {
				setClonedHashEntry(cloned, deepCloneValueWithState(entry.Key, state), deepCloneValueWithState(entry.Value, state))
			}
			return cloned
		}
		for key, item := range val.Hash() {
			clonedEntries[key] = deepCloneValueWithState(item, state)
		}
		return cloned
	case KindObject:
		obj := val.Hash()
		id := reflect.ValueOf(obj).Pointer()
		if id != 0 {
			// A wrapper whose tag differs from the cached clone's gets its own
			// copy: a tagged bag is immutable, and sharing entries with an
			// untagged alias would let a write through the alias change what
			// the tagged one renders.
			if cloned, ok := state.clonedObject(state.objectCacheID(id, val)); ok {
				return state.wrapCloned(val, cloned)
			}
		}
		cloned := make(map[string]Value, len(obj))
		clonedValue := state.wrapCloned(val, NewObject(cloned))
		state.rememberObject(state.objectCacheID(id, val), clonedValue)
		for k, v := range obj {
			cloned[k] = deepCloneValueWithState(v, state)
		}
		return clonedValue
	default:
		return val
	}
}

func (state *deepCloneState) clonedArray(id uintptr) (Value, bool) {
	if id == 0 {
		return NewNil(), false
	}
	if state.arrays != nil {
		cloned, ok := state.arrays[id]
		return cloned, ok
	}
	for i := range state.arrayCount {
		entry := state.smallArrays[i]
		if entry.id == id {
			return entry.value, true
		}
	}
	return NewNil(), false
}

func (state *deepCloneState) rememberArray(id uintptr, cloned Value) {
	if id == 0 {
		return
	}
	if state.arrays != nil {
		state.arrays[id] = cloned
		return
	}
	if state.arrayCount < len(state.smallArrays) {
		state.smallArrays[state.arrayCount] = deepClonePtrEntry{id: id, value: cloned}
		state.arrayCount++
		return
	}
	state.arrays = make(map[uintptr]Value, state.arrayCount+1)
	for i := range state.arrayCount {
		entry := state.smallArrays[i]
		state.arrays[entry.id] = entry.value
	}
	state.arrays[id] = cloned
}

func (state *deepCloneState) clonedHash(id uintptr) (Value, bool) {
	return state.clonedPtr(id, state.hashes, state.smallHashes[:], state.hashCount)
}

func (state *deepCloneState) rememberHash(id uintptr, cloned Value) {
	if id == 0 {
		return
	}
	if state.hashes != nil {
		state.hashes[id] = cloned
		return
	}
	if state.hashCount < len(state.smallHashes) {
		state.smallHashes[state.hashCount] = deepClonePtrEntry{id: id, value: cloned}
		state.hashCount++
		return
	}
	state.hashes = make(map[uintptr]Value, state.hashCount+1)
	for i := range state.hashCount {
		entry := state.smallHashes[i]
		state.hashes[entry.id] = entry.value
	}
	state.hashes[id] = cloned
}

// clonedTagFor is the tag wrapCloned would give this wrapper's clone, used to
// decide whether the cached clone may be shared with it.
func (state *deepCloneState) clonedTagFor(src Value) ObjectTag {
	if !state.preserveTags {
		return ObjectTagNone
	}
	return src.ObjectTag()
}

// wrapCloned gives the shared clone the provenance of the wrapper being
// cloned. Without preserveTags every clone is an ordinary bag, which is the
// script-visible dup behavior.
func (state *deepCloneState) wrapCloned(src, cloned Value) Value {
	if !state.preserveTags {
		if cloned.ObjectTag() == ObjectTagNone {
			return cloned
		}
		return NewObject(cloned.Hash())
	}
	if cloned.ObjectTag() == src.ObjectTag() {
		return cloned
	}
	return retagClonedObject(src, cloned.Hash())
}

// objectCacheID folds the wrapper's provenance into the cache id, so a tagged
// and an untagged wrapper over one entry map get independent clones and each
// still terminates a cycle. Caching only the first wrapper left the other
// uncached, and a cyclic map reachable through both recursed without end.
func (state *deepCloneState) objectCacheID(id uintptr, src Value) uintptr {
	if state.clonedTagFor(src) == ObjectTagNone {
		return id
	}
	// A tag-qualified id must not collide with a plain one. Rotating keeps
	// distinct maps distinct while separating the tagged view of each.
	return id<<1 ^ uintptr(state.clonedTagFor(src))
}

func (state *deepCloneState) clonedObject(id uintptr) (Value, bool) {
	return state.clonedPtr(id, state.objects, state.smallObjects[:], state.objectCount)
}

func (state *deepCloneState) rememberObject(id uintptr, cloned Value) {
	if id == 0 {
		return
	}
	if state.objects != nil {
		state.objects[id] = cloned
		return
	}
	if state.objectCount < len(state.smallObjects) {
		state.smallObjects[state.objectCount] = deepClonePtrEntry{id: id, value: cloned}
		state.objectCount++
		return
	}
	state.objects = make(map[uintptr]Value, state.objectCount+1)
	for i := range state.objectCount {
		entry := state.smallObjects[i]
		state.objects[entry.id] = entry.value
	}
	state.objects[id] = cloned
}

func (state *deepCloneState) clonedPtr(id uintptr, spilled map[uintptr]Value, small []deepClonePtrEntry, count int) (Value, bool) {
	if id == 0 {
		return NewNil(), false
	}
	if spilled != nil {
		cloned, ok := spilled[id]
		return cloned, ok
	}
	for i := range count {
		entry := small[i]
		if entry.id == id {
			return entry.value, true
		}
	}
	return NewNil(), false
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

const maxCapabilityDataOnlyDepth = 256

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
		if mismatch, ok := errors.AsType[*typeMismatchError](err); ok {
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
	return cloneCapabilityDataOnlyValue(method+" return value", result)
}

type capabilityDataCloneScanner struct {
	label          string
	clonedArrays   map[uintptr]Value
	clonedMaps     map[uintptr]Value
	clonedObjects  map[objectCloneKey]Value
	visitingArrays map[uintptr]struct{}
	visitingMaps   map[uintptr]struct{}
}

func cloneCapabilityDataOnlyValue(label string, val Value) (Value, error) {
	if err := validateCapabilityTraversalDepth(label, val); err != nil {
		return NewNil(), err
	}
	scanner := &capabilityDataCloneScanner{
		label:          label,
		clonedArrays:   make(map[uintptr]Value),
		clonedMaps:     make(map[uintptr]Value),
		clonedObjects:  make(map[objectCloneKey]Value),
		visitingArrays: make(map[uintptr]struct{}),
		visitingMaps:   make(map[uintptr]struct{}),
	}
	return scanner.clone(val)
}

func (s *capabilityDataCloneScanner) clone(val Value) (Value, error) {
	switch val.Kind() {
	case KindFunction, KindBuiltin, KindBlock, KindClass, KindInstance, KindShape:
		return NewNil(), fmt.Errorf("%s must be data-only", s.label)
	case KindArray:
		return s.cloneArray(val)
	case KindHash:
		return s.cloneHash(val)
	case KindObject:
		return s.cloneObject(val)
	default:
		return val, nil
	}
}

func (s *capabilityDataCloneScanner) cloneArray(val Value) (Value, error) {
	// Key on the array wrapper identity so aliases of one mutable array clone
	// to one shared object (and distinct empties stay distinct), and so a
	// cyclic array is detected by object rather than by backing slice.
	values := val.Array()
	id := arrayIdentity(val)
	if id != 0 {
		if _, visiting := s.visitingArrays[id]; visiting {
			return NewNil(), fmt.Errorf("%s must not contain cyclic references", s.label)
		}
		if cloned, ok := s.clonedArrays[id]; ok {
			return cloned, nil
		}
		s.visitingArrays[id] = struct{}{}
	}
	clonedValues := make([]Value, len(values))
	cloned := NewArray(clonedValues)
	if id != 0 {
		s.clonedArrays[id] = cloned
	}
	for i, item := range values {
		clonedItem, err := s.clone(item)
		if err != nil {
			return NewNil(), err
		}
		clonedValues[i] = clonedItem
	}
	if id != 0 {
		delete(s.visitingArrays, id)
	}
	return cloned, nil
}

func (s *capabilityDataCloneScanner) cloneHash(val Value) (Value, error) {
	// A typed hash is walked through its entries, so its lossy display-key
	// view is never materialized here — building it would allocate a map and
	// bump the mutation epoch for a view this clone does not read.
	typed := hashHasTypedEntries(val)
	var entries map[string]Value
	ptr := hashIdentity(val)
	if !typed {
		entries = val.Hash()
		if ptr == 0 {
			ptr = reflect.ValueOf(entries).Pointer()
		}
	}
	if ptr != 0 {
		if _, visiting := s.visitingMaps[ptr]; visiting {
			return NewNil(), fmt.Errorf("%s must not contain cyclic references", s.label)
		}
		if cloned, ok := s.clonedMaps[ptr]; ok {
			return cloned, nil
		}
		s.visitingMaps[ptr] = struct{}{}
	}
	clonedEntries := make(map[string]Value, val.HashLen())
	cloned := NewHash(clonedEntries)
	if ptr != 0 {
		s.clonedMaps[ptr] = cloned
	}
	// A typed hash clones entry by entry with its original keys: rebuilding
	// from the display-key view would drop every entry whose key renders like
	// another's (`:x` beside `"x"`), silently changing the data that crosses
	// the boundary.
	if typed {
		var entryBuf [smallHashKeyBufferSize]HashEntry
		for _, entry := range val.HashEntriesInto(entryBuf[:]) {
			clonedKey, err := s.clone(entry.Key)
			if err != nil {
				return NewNil(), err
			}
			clonedItem, err := s.clone(entry.Value)
			if err != nil {
				return NewNil(), err
			}
			setClonedHashEntry(cloned, clonedKey, clonedItem)
		}
	} else {
		for key, item := range entries {
			clonedItem, err := s.clone(item)
			if err != nil {
				return NewNil(), err
			}
			clonedEntries[key] = clonedItem
		}
	}
	defaultValue, err := s.clone(hashDefaultValue(val))
	if err != nil {
		return NewNil(), err
	}
	defaultProc, err := s.clone(hashDefaultProc(val))
	if err != nil {
		return NewNil(), err
	}
	if defaultValue.IsNil() && defaultProc.IsNil() {
		if ptr != 0 {
			delete(s.visitingMaps, ptr)
		}
		return cloned, nil
	}
	cloned = NewHashWithDefault(clonedEntries, defaultValue, defaultProc)
	if ptr != 0 {
		s.clonedMaps[ptr] = cloned
		delete(s.visitingMaps, ptr)
	}
	return cloned, nil
}

func (s *capabilityDataCloneScanner) cloneObject(val Value) (Value, error) {
	entries := val.Hash()
	ptr := reflect.ValueOf(entries).Pointer()
	if ptr != 0 {
		if _, visiting := s.visitingMaps[ptr]; visiting {
			return NewNil(), fmt.Errorf("%s must not contain cyclic references", s.label)
		}
		// Keyed by provenance as well as entry map: a host can return both a
		// tagged bag and NewObject over its live Hash(), and sharing one clone
		// would give the plain wrapper the tag or strip the tagged one's
		// published rendering, depending on which was cloned first.
		key := objectCloneKey{ptr: ptr, tag: val.ObjectTag()}
		if cloned, ok := s.clonedObjects[key]; ok {
			return cloned, nil
		}
		s.visitingMaps[ptr] = struct{}{}
	}
	clonedEntries := make(map[string]Value, len(entries))
	cloned := retagClonedObject(val, clonedEntries)
	if ptr != 0 {
		s.clonedObjects[objectCloneKey{ptr: ptr, tag: val.ObjectTag()}] = cloned
	}
	for key, item := range entries {
		clonedItem, err := s.clone(item)
		if err != nil {
			return NewNil(), err
		}
		clonedEntries[key] = clonedItem
	}
	if ptr != 0 {
		delete(s.visitingMaps, ptr)
	}
	return cloned, nil
}

type capabilityContractScanner struct {
	seenArrays    map[sliceIdentity]struct{}
	seenMaps      map[uintptr]struct{}
	seenClasses   map[*ClassDef]struct{}
	seenInstances map[*Instance]struct{}
	seenEnvs      map[*Env]struct{}

	// collectBounded marks a walk that must stop after collectBudget nodes.
	// It is a separate flag rather than a sentinel budget value: treating zero
	// as "unbounded" meant an exhausted walk silently became unbounded again
	// on the very next node.
	collectBounded bool
	collectBudget  int
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
	if err := validateCapabilityTraversalDepth(label, val); err != nil {
		return err
	}
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

type capabilityTraversalDepthScanner struct {
	visitingArrays map[sliceIdentity]struct{}
	seenArrays     map[sliceIdentity]int
	visitingMaps   map[uintptr]struct{}
	seenMaps       map[uintptr]int
}

func newCapabilityTraversalDepthScanner() *capabilityTraversalDepthScanner {
	return &capabilityTraversalDepthScanner{
		visitingArrays: make(map[sliceIdentity]struct{}),
		seenArrays:     make(map[sliceIdentity]int),
		visitingMaps:   make(map[uintptr]struct{}),
		seenMaps:       make(map[uintptr]int),
	}
}

func validateCapabilityTraversalDepth(label string, val Value) error {
	return newCapabilityTraversalDepthScanner().check(label, val, 0)
}

func (s *capabilityTraversalDepthScanner) check(label string, val Value, depth int) error {
	if depth > maxCapabilityDataOnlyDepth {
		return guardLimitErrorf("%s exceeds maximum depth %d", label, maxCapabilityDataOnlyDepth)
	}
	remainingDepth := maxCapabilityDataOnlyDepth - depth
	switch val.Kind() {
	case KindArray:
		values := val.Array()
		id := sliceIdentity{
			Ptr: reflect.ValueOf(values).Pointer(),
			Len: len(values),
			Cap: cap(values),
		}
		if seenRemaining, seen := s.seenArrays[id]; seen && seenRemaining <= remainingDepth {
			return nil
		}
		if _, visiting := s.visitingArrays[id]; visiting {
			return nil
		}
		s.visitingArrays[id] = struct{}{}
		for _, item := range values {
			if err := s.check(label, item, depth+1); err != nil {
				return err
			}
		}
		delete(s.visitingArrays, id)
		if seenRemaining, seen := s.seenArrays[id]; !seen || remainingDepth < seenRemaining {
			s.seenArrays[id] = remainingDepth
		}
	case KindHash, KindObject:
		ptr := hashScanIdentity(val)
		if seenRemaining, seen := s.seenMaps[ptr]; seen && seenRemaining <= remainingDepth {
			return nil
		}
		if _, visiting := s.visitingMaps[ptr]; visiting {
			return nil
		}
		s.visitingMaps[ptr] = struct{}{}
		var entryErr error
		anyHashValue(val, func(item Value) bool {
			entryErr = s.check(label, item, depth+1)
			return entryErr != nil
		})
		if entryErr != nil {
			return entryErr
		}
		if err := s.check(label, hashDefaultValue(val), depth+1); err != nil {
			return err
		}
		if err := s.check(label, hashDefaultProc(val), depth+1); err != nil {
			return err
		}
		delete(s.visitingMaps, ptr)
		if seenRemaining, seen := s.seenMaps[ptr]; !seen || remainingDepth < seenRemaining {
			s.seenMaps[ptr] = remainingDepth
		}
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
		// Key on the whole hash wrapper (or the entry-map pointer for objects,
		// which never carry defaults) so two wrappers sharing one entry map but
		// carrying distinct defaults are each walked: a second wrapper's default
		// is not skipped at the seen check, and a data-only diamond of shared-map
		// wrappers is not mistaken for a cycle.
		ptr := hashScanIdentity(val)
		if _, seen := s.seenMaps[ptr]; seen {
			return false
		}
		if _, visiting := s.visitingMaps[ptr]; visiting {
			return true
		}
		s.visitingMaps[ptr] = struct{}{}
		if anyHashValue(val, s.containsCycle) {
			return true
		}
		// A KindHash's default value/proc are reachable hash state and may
		// themselves nest collections, so walk them for cycles too. They share
		// the same visiting set, so a default that references its own hash is
		// detected as a cycle like any other back-edge.
		if s.containsCycle(hashDefaultValue(val)) || s.containsCycle(hashDefaultProc(val)) {
			return true
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
	case KindFunction, KindBuiltin, KindBlock, KindClass, KindInstance, KindShape:
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
		// A KindHash's default metadata lives outside its entry map, so two
		// wrappers can share one map yet carry different defaults. Key the
		// seen-set on the whole hash wrapper (falling back to the entry-map
		// pointer for objects, which never carry defaults) so a second wrapper's
		// callable default is not hidden by an earlier plain wrapper marking the
		// shared map seen.
		ptr := hashScanIdentity(val)
		if _, seen := s.seenMaps[ptr]; seen {
			return false
		}
		s.seenMaps[ptr] = struct{}{}
		if anyHashValue(val, s.containsCallable) {
			return true
		}
		// A KindHash may carry Ruby-style default metadata outside its entry
		// map: a default value (itself possibly a callable or a collection of
		// callables) and a default proc (a KindBlock, always a callable). Scan
		// both so Hash.new { ... } or Hash.new(some_proc) cannot smuggle a
		// script callable past a data-only boundary.
		if s.containsCallable(hashDefaultValue(val)) {
			return true
		}
		return s.containsCallable(hashDefaultProc(val))
	default:
		return false
	}
}

// scanClosureEnv walks a closure's captured environment chain (the Env of a
// script function or a block) and applies visit to every value bound in each
// frame. It stops at the ambient global chain: builtins bound there are
// pre-existing globals, not values this capability exposed. Binding them
// through a script-supplied closure would let an unrelated global builtin whose
// name matches a contract method be attached to this scope (CWE-862
// regression). The remaining ancestors are all ambient too, so the walk stops
// entirely. seenEnvs gives cycle-safe termination for self- or mutually
// referencing closure environments.
// ambientCollectNodeBudget bounds the ambient snapshot's traversal. It is
// generous next to any realistic set of globals while keeping the per-call
// cost independent of how large a script's globals grow.
const ambientCollectNodeBudget = 4096

// collectExhausted reports that a bounded walk has spent its allowance.
// Container loops consult it so traversal stops rather than making a no-op
// recursive call for every remaining element -- which left the walk O(graph)
// per call despite the cap.
func (s *capabilityContractScanner) collectExhausted() bool {
	return s.collectBounded && s.collectBudget <= 0
}

// collectAmbientBuiltins gathers the builtins bound directly in the ambient
// environments -- the script's own globals and the engine scopes above them.
//
// Only each environment's own bindings are enumerated; the values then go
// through the ordinary scanner, which still stops when a nested function or
// block reaches an ambient environment. That keeps this from turning into a
// recursive walk of every closure chain.
//
// The ordinary pre-call scan never reaches globals, so a block that breaks
// with one would make it the call's result and the post-call scan would bind
// the capability's contract to something the caller has owned all along.
func (s *capabilityContractScanner) collectAmbientBuiltins(root *Env, out map[*Builtin]struct{}) {
	// The walk charges no steps, so it is bounded rather than metered: a
	// script could otherwise hold a large global structure and call a
	// contracted capability with an empty block in a loop, buying O(global
	// graph) host work per metered step. Truncating only means fewer
	// exclusions, which costs precision on caller-owned break values and
	// never lets a genuinely published builtin through.
	s.collectBounded, s.collectBudget = true, ambientCollectNodeBudget
	defer func() { s.collectBounded, s.collectBudget = false, 0 }()
	for env := root; env != nil; env = env.parent {
		if s.collectExhausted() {
			return
		}
		env.rangeDynamicBindingsWhile(func(_ string, item Value) bool {
			s.collectBuiltins(item, out)
			return !s.collectExhausted()
		})
		if s.collectExhausted() {
			return
		}
		env.rangeRawStaticBindingsWhile(func(_ string, item Value) bool {
			s.collectBuiltins(item, out)
			return !s.collectExhausted()
		})
	}
}

func (s *capabilityContractScanner) scanClosureEnv(env *Env, visit func(Value)) {
	for ; env != nil; env = env.parent {
		if _, ambient := s.ambientEnvs[env]; ambient {
			return
		}
		if _, seen := s.seenEnvs[env]; seen {
			return
		}
		// A bounded walk stops here too. A captured frame can hold many
		// bindings, and a no-op visitor per binding still costs O(frame) --
		// and O(ancestors) as the loop climbs -- which is exactly the
		// unmetered work the budget exists to bound.
		if s.collectExhausted() {
			return
		}
		s.seenEnvs[env] = struct{}{}
		env.rangeDynamicBindingsWhile(func(_ string, item Value) bool {
			visit(item)
			return !s.collectExhausted()
		})
		if s.collectExhausted() {
			return
		}
		env.rangeRawStaticBindingsWhile(func(_ string, item Value) bool {
			visit(item)
			return !s.collectExhausted()
		})
		if s.collectExhausted() {
			return
		}
		if env.hasCallBlock {
			visit(env.callBlock)
		}
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
		// Key on the whole hash wrapper (or the entry-map pointer for objects) so
		// a second wrapper sharing one entry map but carrying distinct defaults
		// still has those defaults scanned for exposed builtins.
		ptr := hashIdentity(val)
		if ptr == 0 {
			ptr = reflect.ValueOf(entries).Pointer()
		}
		if _, seen := s.seenMaps[ptr]; seen {
			return
		}
		s.seenMaps[ptr] = struct{}{}
		for _, item := range entries {
			s.bindContracts(item, scope, target, scopes)
		}
		// A KindHash's default value/proc are reachable hash state, so contracts
		// must bind to any builtins they expose just as they do for entries.
		s.bindContracts(hashDefaultValue(val), scope, target, scopes)
		s.bindContracts(hashDefaultProc(val), scope, target, scopes)
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
			if s.collectExhausted() {
				return
			}
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
			if s.collectExhausted() {
				return
			}
			s.bindContracts(item, scope, target, scopes)
		}
		if instance.Class != nil {
			s.bindContracts(NewClass(instance.Class), scope, target, scopes)
		}
	case KindFunction:
		if fn := valueFunction(val); fn != nil {
			s.scanClosureEnv(fn.Env, func(item Value) {
				s.bindContracts(item, scope, target, scopes)
			})
		}
	case KindBlock:
		if blk := valueBlock(val); blk != nil {
			s.scanClosureEnv(blk.Env, func(item Value) {
				s.bindContracts(item, scope, target, scopes)
			})
			s.bindContracts(blk.forward, scope, target, scopes)
		}
	}
}

func (s *capabilityContractScanner) collectBuiltins(val Value, out map[*Builtin]struct{}) {
	// collectBudget bounds an otherwise unmetered walk. It is only set for the
	// ambient snapshot, whose size is the caller's globals rather than
	// anything the call supplies; every other caller leaves it zero and is
	// unaffected.
	if s.collectExhausted() {
		return
	}
	if s.collectBounded {
		s.collectBudget--
	}
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
			if s.collectExhausted() {
				return
			}
			s.collectBuiltins(item, out)
		}
	case KindHash, KindObject:
		entries := val.Hash()
		// Key on the whole hash wrapper (or the entry-map pointer for objects) so
		// a second wrapper sharing one entry map but carrying distinct defaults
		// still has those defaults scanned for exposed builtins.
		ptr := hashIdentity(val)
		if ptr == 0 {
			ptr = reflect.ValueOf(entries).Pointer()
		}
		if _, seen := s.seenMaps[ptr]; seen {
			return
		}
		s.seenMaps[ptr] = struct{}{}
		for _, item := range entries {
			if s.collectExhausted() {
				return
			}
			s.collectBuiltins(item, out)
		}
		// A KindHash's default value/proc are reachable hash state, so any
		// builtins they expose must be collected like entry builtins. The proc
		// is a KindBlock whose captured env is walked by the KindBlock case.
		s.collectBuiltins(hashDefaultValue(val), out)
		s.collectBuiltins(hashDefaultProc(val), out)
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
			if s.collectExhausted() {
				return
			}
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
			if s.collectExhausted() {
				return
			}
			s.collectBuiltins(item, out)
		}
		if instance.Class != nil {
			s.collectBuiltins(NewClass(instance.Class), out)
		}
	case KindFunction:
		if fn := valueFunction(val); fn != nil {
			s.scanClosureEnv(fn.Env, func(item Value) {
				s.collectBuiltins(item, out)
			})
		}
	case KindBlock:
		if blk := valueBlock(val); blk != nil {
			s.scanClosureEnv(blk.Env, func(item Value) {
				s.collectBuiltins(item, out)
			})
			s.collectBuiltins(blk.forward, out)
		}
	}
}

// markCapabilityBuiltins flags every builtin reachable from a capability
// adapter's bound globals as a per-call capability grant. The set is gathered
// through the shared cycle-safe traversal so nested objects, hashes, arrays, and
// closure environments an adapter may expose are all covered.
func markCapabilityBuiltins(val Value) {
	builtins := make(map[*Builtin]struct{})
	scanner := newCapabilityContractScanner()
	scanner.collectBuiltins(val, builtins)
	for builtin := range builtins {
		builtin.Capability = true
	}
}

type strictGlobalsScanner struct {
	seenArrays  map[sliceIdentity]struct{}
	seenMaps    map[uintptr]struct{}
	stackArrays map[sliceIdentity]struct{}
	stackMaps   map[uintptr]struct{}
}

func validateStrictGlobals(globals map[string]Value) error {
	if len(globals) == 0 {
		return nil
	}
	scanner := strictGlobalsScanner{
		seenArrays:  make(map[sliceIdentity]struct{}),
		seenMaps:    make(map[uintptr]struct{}),
		stackArrays: make(map[sliceIdentity]struct{}),
		stackMaps:   make(map[uintptr]struct{}),
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
	case KindFunction, KindBuiltin, KindBlock, KindClass, KindInstance, KindShape:
		return true
	case KindArray:
		values := val.Array()
		id := sliceIdentity{
			Ptr: reflect.ValueOf(values).Pointer(),
			Len: len(values),
			Cap: cap(values),
		}
		if _, seen := s.seenArrays[id]; seen {
			if _, cyclic := s.stackArrays[id]; cyclic {
				return true
			}
			return false
		}
		s.seenArrays[id] = struct{}{}
		s.stackArrays[id] = struct{}{}
		defer delete(s.stackArrays, id)
		return slices.ContainsFunc(values, s.containsCallable)
	case KindHash, KindObject:
		// Key the seen-set on the whole hash wrapper (or the entry-map pointer for
		// objects, which never carry defaults) so a second wrapper sharing the
		// same entry map but carrying a callable default is still scanned rather
		// than skipped at the seen check.
		ptr := hashScanIdentity(val)
		if _, seen := s.seenMaps[ptr]; seen {
			if _, cyclic := s.stackMaps[ptr]; cyclic {
				return true
			}
			return false
		}
		s.seenMaps[ptr] = struct{}{}
		s.stackMaps[ptr] = struct{}{}
		defer delete(s.stackMaps, ptr)
		if anyHashValue(val, s.containsCallable) {
			return true
		}
		// A KindHash may carry Ruby-style default metadata outside its entry
		// map: a default value and a default proc (a KindBlock callable). A
		// strict global must be data-only, so scan both rather than admitting a
		// Hash.new { ... } as an empty, callable-free hash.
		if s.containsCallable(hashDefaultValue(val)) {
			return true
		}
		return s.containsCallable(hashDefaultProc(val))
	default:
		return false
	}
}

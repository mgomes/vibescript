package runtime

import (
	"maps"
	"reflect"

	"github.com/mgomes/vibescript/vibes/value"
)

const inlineEnvBindingCapacity = 3

type envBinding struct {
	name  string
	value Value
}

// Env represents a lexical scope that maps variable names to values.
//
// Bindings live in two stores: inline holds small normal script scopes without
// a map allocation, values holds larger normal script scopes, and
// statics holds bindings whose deep size never changes after definition
// (builtins, per-call function clones). Statics are stored separately so
// memory-quota estimation can account for them in O(1) through the
// staticBytes counter instead of re-walking every binding on each check
// -- the root env's builtin set dominated estimation cost otherwise.
type Env struct {
	parent             *Env
	inline             [inlineEnvBindingCapacity]envBinding
	inlineLen          uint8
	values             map[string]Value
	statics            map[string]Value
	staticBytes        int32
	arrayAppendBuffers map[string][]Value
	assignBoundary     bool

	// frozen marks engine-shared scopes (the builtin proto). Their
	// bindings are readable through the chain but never written:
	// assignments to names found here rebind in the nearest call-local
	// scope instead, exactly as if the binding lived in the call root.
	frozen bool

	// callRoot marks the per-call ambient root: the scope that holds a
	// Script.Call's globals, capabilities, and per-call function clones,
	// chained beneath the engine-shared builtin proto. The inbound rebinder
	// uses this marker to re-root an escaped closure's captured environment
	// onto the live call. The marker is preserved when a closure is cloned
	// across the host boundary so it survives re-entry.
	callRoot bool
}

func newEnv(parent *Env) *Env {
	return newEnvWithCapacity(parent, 0)
}

func newEnvWithCapacity(parent *Env, capacity int) *Env {
	if capacity < 0 {
		capacity = 0
	}
	env := &Env{parent: parent}
	if capacity > inlineEnvBindingCapacity {
		env.values = make(map[string]Value, capacity)
	}
	return env
}

// newAssignmentBoundaryEnv can read parent bindings, but missing-name writes
// stop in this scope instead of escaping into an outer mutable scope.
func newAssignmentBoundaryEnv(parent *Env) *Env {
	env := newEnv(parent)
	env.assignBoundary = true
	return env
}

func (e *Env) resetForBlockCall(parent *Env) {
	e.parent = parent
	for i := range int(e.inlineLen) {
		e.inline[i] = envBinding{}
	}
	e.inlineLen = 0
	clear(e.values)
	e.statics = nil
	e.staticBytes = 0
	e.arrayAppendBuffers = nil
	e.assignBoundary = false
	e.frozen = false
	e.callRoot = false
}

// Get looks up a variable by name, traversing parent scopes if needed.
func (e *Env) Get(name string) (Value, bool) {
	var lastMutable *Env
	for scope := e; scope != nil; scope = scope.parent {
		if !scope.frozen {
			lastMutable = scope
		}
		if idx, ok := scope.inlineIndex(name); ok {
			val := scope.inline[idx].value
			if lazy, ok := lazyValue(val); ok {
				val = lazy.materialize()
				scope.inline[idx].value = val
				scope.dropArrayAppendBuffer(name)
			}
			return val, true
		}
		if val, ok := scope.values[name]; ok {
			if lazy, ok := lazyValue(val); ok {
				val = lazy.materialize()
				scope.values[name] = val
				scope.dropArrayAppendBuffer(name)
			}
			return val, true
		}
		if val, ok := scope.statics[name]; ok {
			if scope.frozen && lastMutable != nil && builtinNeedsCallClone(val) {
				cloned := cloneBuiltinValueForCall(val)
				lastMutable.DefineStatic(name, cloned)
				return cloned, true
			}
			return val, true
		}
	}
	return Value{}, false
}

// Define binds a new variable in the current scope.
func (e *Env) Define(name string, val Value) {
	e.setDynamic(name, val)
	e.dropStatic(name)
	e.dropArrayAppendBuffer(name)
}

// growStatics pre-sizes the statics map for n upcoming DefineStatic
// calls so bulk binding (builtins, per-call function clones) does not
// rehash repeatedly.
func (e *Env) growStatics(n int) {
	if e.statics == nil {
		e.statics = make(map[string]Value, n)
	}
}

// DefineStatic binds a variable whose deep size is fixed at definition
// time, keeping it out of the per-check estimation walk.
func (e *Env) DefineStatic(name string, val Value) {
	e.deleteDynamic(name)
	if e.statics == nil {
		e.statics = make(map[string]Value)
	}
	if _, exists := e.statics[name]; !exists {
		e.staticBytes += int32(staticEntryBytes(name))
	}
	e.statics[name] = val
}

// Assign updates an existing variable in the nearest enclosing scope.
// Names not bound anywhere are defined in the outermost mutable scope,
// and names found in a frozen scope rebind in the nearest mutable scope
// below it, so engine-shared bindings are never written.
func (e *Env) Assign(name string, val Value) bool {
	e.assignValue(name, val)
	return true
}

func (e *Env) assignArrayAppendBuffer(name string, val Value, buffer []Value) bool {
	scope := e.assignValue(name, val)
	scope.setArrayAppendBuffer(name, buffer)
	return true
}

func (e *Env) assignValue(name string, val Value) *Env {
	last := e
	for scope := e; scope != nil; scope = scope.parent {
		if scope.frozen {
			inValues := scope.hasDynamic(name)
			_, inStatics := scope.statics[name]
			if inValues || inStatics {
				last.setDynamic(name, val)
				last.dropStatic(name)
				last.dropArrayAppendBuffer(name)
				return last
			}
			continue
		}
		if scope.setExistingDynamic(name, val) {
			scope.dropArrayAppendBuffer(name)
			return scope
		}
		if _, ok := scope.statics[name]; ok {
			// The binding is no longer immutable-by-binding; demote it
			// so estimation starts walking its (now mutable) value.
			scope.dropStatic(name)
			scope.setDynamic(name, val)
			scope.dropArrayAppendBuffer(name)
			return scope
		}
		if scope.assignBoundary {
			scope.setDynamic(name, val)
			scope.dropStatic(name)
			scope.dropArrayAppendBuffer(name)
			return scope
		}
		last = scope
	}
	last.setDynamic(name, val)
	last.dropStatic(name)
	last.dropArrayAppendBuffer(name)
	return last
}

func (e *Env) arrayAppendBuffer(name string) ([]Value, bool) {
	scope, ok := e.lookupBindingScope(name)
	if !ok || scope.arrayAppendBuffers == nil {
		return nil, false
	}
	buffer, ok := scope.arrayAppendBuffers[name]
	return buffer, ok
}

func (e *Env) clearArrayAppendBuffer(name string) {
	if scope, ok := e.lookupBindingScope(name); ok {
		scope.dropArrayAppendBuffer(name)
	}
}

func (e *Env) lookupBindingScope(name string) (*Env, bool) {
	for scope := e; scope != nil; scope = scope.parent {
		if scope.hasDynamic(name) {
			return scope, true
		}
		if _, ok := scope.statics[name]; ok {
			return scope, true
		}
	}
	return nil, false
}

func (e *Env) setArrayAppendBuffer(name string, buffer []Value) {
	if e.arrayAppendBuffers == nil {
		e.arrayAppendBuffers = make(map[string][]Value)
	}
	e.arrayAppendBuffers[name] = buffer
}

func (e *Env) detachArrayAppendResult(val Value) Value {
	if val.Kind() != KindArray {
		return val
	}
	items := val.Array()
	if len(items) == 0 {
		return val
	}
	ptr := reflect.ValueOf(items).Pointer()
	if ptr == 0 {
		return val
	}
	for scope := e; scope != nil; scope = scope.parent {
		for _, buffer := range scope.arrayAppendBuffers {
			if len(buffer) != len(items) || reflect.ValueOf(buffer).Pointer() != ptr {
				continue
			}
			detached := make([]Value, len(items))
			copy(detached, items)
			return NewArray(detached)
		}
	}
	return val
}

// visibleNames returns every name bound in this scope or any enclosing
// scope, reporting shadowed names once. It is intended for error-path
// suggestions and is never called on successful lookups.
func (e *Env) visibleNames() []string {
	seen := make(map[string]struct{})
	names := make([]string, 0, e.dynamicLen())
	add := func(name string) {
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	for scope := e; scope != nil; scope = scope.parent {
		scope.rangeDynamicBindings(func(name string, _ Value) {
			add(name)
		})
		for name := range scope.statics {
			add(name)
		}
	}
	return names
}

// CloneShallow returns a copy of the environment with the same parent and a shallow copy of its bindings.
func (e *Env) CloneShallow() *Env {
	clone := newEnvWithCapacity(e.parent, e.dynamicLen())
	e.rangeDynamicBindings(func(name string, val Value) {
		clone.setDynamic(name, val)
	})
	if len(e.statics) > 0 {
		clone.statics = make(map[string]Value, len(e.statics))
		maps.Copy(clone.statics, e.statics)
		clone.staticBytes = e.staticBytes
	}
	return clone
}

type lazyEnvValue interface {
	materialize() Value
}

func (e *Env) defineLazy(name string, lazy lazyEnvValue) {
	e.setDynamic(name, newLazyValue(lazy))
	e.dropStatic(name)
	e.dropArrayAppendBuffer(name)
}

func (e *Env) dynamicLen() int {
	return int(e.inlineLen) + len(e.values)
}

func (e *Env) inlineIndex(name string) (int, bool) {
	for i := range int(e.inlineLen) {
		if e.inline[i].name == name {
			return i, true
		}
	}
	return 0, false
}

func (e *Env) hasDynamic(name string) bool {
	if _, ok := e.inlineIndex(name); ok {
		return true
	}
	_, ok := e.values[name]
	return ok
}

func (e *Env) getOwn(name string) (Value, bool) {
	if idx, ok := e.inlineIndex(name); ok {
		return e.inline[idx].value, true
	}
	if val, ok := e.values[name]; ok {
		return val, true
	}
	if val, ok := e.statics[name]; ok {
		return val, true
	}
	return Value{}, false
}

func (e *Env) setExistingDynamic(name string, val Value) bool {
	if idx, ok := e.inlineIndex(name); ok {
		e.inline[idx].value = val
		return true
	}
	if _, ok := e.values[name]; ok {
		e.values[name] = val
		return true
	}
	return false
}

func (e *Env) setDynamic(name string, val Value) {
	if e.setExistingDynamic(name, val) {
		return
	}
	if e.values != nil {
		e.values[name] = val
		return
	}
	if int(e.inlineLen) < len(e.inline) {
		e.inline[e.inlineLen] = envBinding{name: name, value: val}
		e.inlineLen++
		return
	}
	e.promoteInlineBindings(int(e.inlineLen) + 1)
	e.values[name] = val
}

func (e *Env) deleteDynamic(name string) {
	if idx, ok := e.inlineIndex(name); ok {
		last := int(e.inlineLen) - 1
		copy(e.inline[idx:last], e.inline[idx+1:int(e.inlineLen)])
		e.inline[last] = envBinding{}
		e.inlineLen--
		return
	}
	delete(e.values, name)
}

func (e *Env) promoteInlineBindings(capacity int) {
	if capacity < int(e.inlineLen) {
		capacity = int(e.inlineLen)
	}
	if e.values == nil {
		e.values = make(map[string]Value, capacity)
	}
	for i := range int(e.inlineLen) {
		binding := e.inline[i]
		e.values[binding.name] = binding.value
		e.inline[i] = envBinding{}
	}
	e.inlineLen = 0
}

func (e *Env) rangeDynamicBindings(visit func(string, Value)) {
	for i := range int(e.inlineLen) {
		binding := e.inline[i]
		visit(binding.name, binding.value)
	}
	for name, val := range e.values {
		visit(name, val)
	}
}

func (e *Env) rangeStaticBindings(visit func(string, Value)) {
	for name, val := range e.statics {
		visit(name, val)
	}
}

func (e *Env) dropStatic(name string) {
	if e.statics == nil {
		return
	}
	if _, ok := e.statics[name]; !ok {
		return
	}
	delete(e.statics, name)
	e.staticBytes -= int32(staticEntryBytes(name))
	if len(e.statics) == 0 {
		e.statics = nil
	}
}

func (e *Env) dropArrayAppendBuffer(name string) {
	if e.arrayAppendBuffers == nil {
		return
	}
	delete(e.arrayAppendBuffers, name)
	if len(e.arrayAppendBuffers) == 0 {
		e.arrayAppendBuffers = nil
	}
}

// staticEntryBytes is the estimation cost of one static binding: its map
// entry, name header and bytes, and the value header. Deep value size is
// intentionally excluded — static bindings are compile-time artifacts
// whose payloads do not count against script memory quotas.
func staticEntryBytes(name string) int {
	return estimatedMapEntryBytes + estimatedStringHeaderBytes + len(name) + estimatedValueBytes
}

func newLazyValue(lazy lazyEnvValue) Value {
	return value.NewValue(KindBuiltin, lazy)
}

func lazyValue(val Value) (lazyEnvValue, bool) {
	if val.Kind() != KindBuiltin {
		return nil, false
	}
	lazy, ok := val.Data().(lazyEnvValue)
	return lazy, ok
}

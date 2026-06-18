package runtime

import "maps"

// Env represents a lexical scope that maps variable names to values.
//
// Bindings live in two maps: values holds normal script bindings, while
// statics holds bindings whose deep size never changes after definition
// (builtins, per-call function clones). Statics are stored separately so
// memory-quota estimation can account for them in O(1) through the
// staticBytes counter instead of re-walking every binding on each check
// — the root env's builtin set dominated estimation cost otherwise.
type Env struct {
	parent             *Env
	values             map[string]Value
	statics            map[string]Value
	staticBytes        int32
	arrayAppendBuffers map[string][]Value

	// frozen marks engine-shared scopes (the builtin proto). Their
	// bindings are readable through the chain but never written:
	// assignments to names found here rebind in the nearest call-local
	// scope instead, exactly as if the binding lived in the call root.
	frozen bool
}

func newEnv(parent *Env) *Env {
	return newEnvWithCapacity(parent, 0)
}

func newEnvWithCapacity(parent *Env, capacity int) *Env {
	if capacity < 0 {
		capacity = 0
	}
	return &Env{parent: parent, values: make(map[string]Value, capacity)}
}

// Get looks up a variable by name, traversing parent scopes if needed.
func (e *Env) Get(name string) (Value, bool) {
	if val, ok := e.values[name]; ok {
		return val, true
	}
	if val, ok := e.statics[name]; ok {
		return val, true
	}
	if e.parent != nil {
		return e.parent.Get(name)
	}
	return Value{}, false
}

// Define binds a new variable in the current scope.
func (e *Env) Define(name string, val Value) {
	e.values[name] = val
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
	delete(e.values, name)
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
			_, inValues := scope.values[name]
			_, inStatics := scope.statics[name]
			if inValues || inStatics {
				last.values[name] = val
				last.dropStatic(name)
				last.dropArrayAppendBuffer(name)
				return last
			}
			continue
		}
		if _, ok := scope.values[name]; ok {
			scope.values[name] = val
			scope.dropArrayAppendBuffer(name)
			return scope
		}
		if _, ok := scope.statics[name]; ok {
			// The binding is no longer immutable-by-binding; demote it
			// so estimation starts walking its (now mutable) value.
			scope.dropStatic(name)
			scope.values[name] = val
			scope.dropArrayAppendBuffer(name)
			return scope
		}
		last = scope
	}
	last.values[name] = val
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
		if _, ok := scope.values[name]; ok {
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

// visibleNames returns every name bound in this scope or any enclosing
// scope, reporting shadowed names once. It is intended for error-path
// suggestions and is never called on successful lookups.
func (e *Env) visibleNames() []string {
	seen := make(map[string]struct{})
	names := make([]string, 0, len(e.values))
	add := func(name string) {
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	for scope := e; scope != nil; scope = scope.parent {
		for name := range scope.values {
			add(name)
		}
		for name := range scope.statics {
			add(name)
		}
	}
	return names
}

// CloneShallow returns a copy of the environment with the same parent and a shallow copy of its bindings.
func (e *Env) CloneShallow() *Env {
	clone := newEnv(e.parent)
	maps.Copy(clone.values, e.values)
	if len(e.statics) > 0 {
		clone.statics = make(map[string]Value, len(e.statics))
		maps.Copy(clone.statics, e.statics)
		clone.staticBytes = e.staticBytes
	}
	return clone
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

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
	parent      *Env
	values      map[string]Value
	statics     map[string]Value
	staticBytes int
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
		e.staticBytes += staticEntryBytes(name)
	}
	e.statics[name] = val
}

// Assign updates an existing variable in the nearest enclosing scope, or defines it in the current scope.
func (e *Env) Assign(name string, val Value) bool {
	if _, ok := e.values[name]; ok {
		e.values[name] = val
		return true
	}
	if _, ok := e.statics[name]; ok {
		// The binding is no longer immutable-by-binding; demote it so
		// estimation starts walking its (now mutable) value.
		e.dropStatic(name)
		e.values[name] = val
		return true
	}
	if e.parent != nil {
		if e.parent.Assign(name, val) {
			return true
		}
	}
	e.values[name] = val
	e.dropStatic(name)
	return true
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
	e.staticBytes -= staticEntryBytes(name)
}

// staticEntryBytes is the estimation cost of one static binding: its map
// entry, name header and bytes, and the value header. Deep value size is
// intentionally excluded — static bindings are compile-time artifacts
// whose payloads do not count against script memory quotas.
func staticEntryBytes(name string) int {
	return estimatedMapEntryBytes + estimatedStringHeaderBytes + len(name) + estimatedValueBytes
}

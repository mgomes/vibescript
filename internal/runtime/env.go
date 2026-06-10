package runtime

import "maps"

// Env represents a lexical scope that maps variable names to values.
type Env struct {
	parent       *Env
	values       map[string]Value
	staticValues map[string]struct{}
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
	if e.parent != nil {
		return e.parent.Get(name)
	}
	return Value{}, false
}

// Define binds a new variable in the current scope.
func (e *Env) Define(name string, val Value) {
	e.values[name] = val
	if e.staticValues != nil {
		delete(e.staticValues, name)
	}
}

func (e *Env) DefineStatic(name string, val Value) {
	e.values[name] = val
	if e.staticValues == nil {
		e.staticValues = make(map[string]struct{})
	}
	e.staticValues[name] = struct{}{}
}

// Assign updates an existing variable in the nearest enclosing scope, or defines it in the current scope.
func (e *Env) Assign(name string, val Value) bool {
	if _, ok := e.values[name]; ok {
		e.values[name] = val
		if e.staticValues != nil {
			delete(e.staticValues, name)
		}
		return true
	}
	if e.parent != nil {
		if e.parent.Assign(name, val) {
			return true
		}
	}
	e.values[name] = val
	if e.staticValues != nil {
		delete(e.staticValues, name)
	}
	return true
}

// visibleNames returns every name bound in this scope or any enclosing
// scope, reporting shadowed names once. It is intended for error-path
// suggestions and is never called on successful lookups.
func (e *Env) visibleNames() []string {
	seen := make(map[string]struct{})
	names := make([]string, 0, len(e.values))
	for scope := e; scope != nil; scope = scope.parent {
		for name := range scope.values {
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}
	return names
}

// CloneShallow returns a copy of the environment with the same parent and a shallow copy of its bindings.
func (e *Env) CloneShallow() *Env {
	clone := newEnv(e.parent)
	maps.Copy(clone.values, e.values)
	if len(e.staticValues) > 0 {
		clone.staticValues = make(map[string]struct{}, len(e.staticValues))
		maps.Copy(clone.staticValues, e.staticValues)
	}
	return clone
}

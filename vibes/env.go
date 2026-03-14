package vibes

import "maps"

// Env represents a lexical scope that maps variable names to values.
type Env struct {
	parent *Env
	values map[string]Value
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
}

// Assign updates an existing variable in the nearest enclosing scope, or defines it in the current scope.
func (e *Env) Assign(name string, val Value) bool {
	if _, ok := e.values[name]; ok {
		e.values[name] = val
		return true
	}
	if e.parent != nil {
		if e.parent.Assign(name, val) {
			return true
		}
	}
	e.values[name] = val
	return true
}

// CloneShallow returns a copy of the environment with the same parent and a shallow copy of its bindings.
func (e *Env) CloneShallow() *Env {
	clone := newEnv(e.parent)
	maps.Copy(clone.values, e.values)
	return clone
}

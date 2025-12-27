package vibes

import "sync"

type Env struct {
	parent *Env
	values map[string]Value
}

var envPool = sync.Pool{
	New: func() any {
		return &Env{values: make(map[string]Value)}
	},
}

func newEnv(parent *Env) *Env {
	return &Env{parent: parent, values: make(map[string]Value)}
}

func borrowEnv(parent *Env) *Env {
	env := envPool.Get().(*Env)
	env.parent = parent
	clear(env.values)
	return env
}

func releaseEnv(env *Env) {
	env.parent = nil
	envPool.Put(env)
}

func (e *Env) Get(name string) (Value, bool) {
	if val, ok := e.values[name]; ok {
		return val, true
	}
	if e.parent != nil {
		return e.parent.Get(name)
	}
	return Value{}, false
}

func (e *Env) Define(name string, val Value) {
	e.values[name] = val
}

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

func (e *Env) CloneShallow() *Env {
	clone := newEnv(e.parent)
	for k, v := range e.values {
		clone.values[k] = v
	}
	return clone
}

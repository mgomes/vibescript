package vibes

import "reflect"

type callFunctionRebinder struct {
	root          *Env
	seenFunctions map[*ScriptFunction]*ScriptFunction
	seenArrays    map[sliceIdentity]struct{}
	seenMaps      map[uintptr]struct{}
}

func newCallFunctionRebinder(root *Env) *callFunctionRebinder {
	return &callFunctionRebinder{
		root:          root,
		seenFunctions: make(map[*ScriptFunction]*ScriptFunction),
		seenArrays:    make(map[sliceIdentity]struct{}),
		seenMaps:      make(map[uintptr]struct{}),
	}
}

func (r *callFunctionRebinder) rebindValue(val Value) Value {
	switch val.Kind() {
	case KindFunction:
		fn := val.Function()
		if fn == nil || fn.Env == r.root {
			return val
		}
		if clone, ok := r.seenFunctions[fn]; ok {
			return NewFunction(clone)
		}
		clone := cloneFunctionForEnv(fn, r.root)
		r.seenFunctions[fn] = clone
		return NewFunction(clone)
	case KindArray:
		items := val.Array()
		id := sliceIdentity{
			ptr: reflect.ValueOf(items).Pointer(),
			len: len(items),
			cap: cap(items),
		}
		if _, seen := r.seenArrays[id]; seen {
			return val
		}
		r.seenArrays[id] = struct{}{}
		for i := range items {
			items[i] = r.rebindValue(items[i])
		}
		return val
	case KindHash, KindObject:
		entries := val.Hash()
		ptr := reflect.ValueOf(entries).Pointer()
		if _, seen := r.seenMaps[ptr]; seen {
			return val
		}
		r.seenMaps[ptr] = struct{}{}
		for key, item := range entries {
			entries[key] = r.rebindValue(item)
		}
		return val
	default:
		return val
	}
}

func (r *callFunctionRebinder) rebindValues(values []Value) []Value {
	if len(values) == 0 {
		return values
	}
	out := make([]Value, len(values))
	for i, val := range values {
		out[i] = r.rebindValue(val)
	}
	return out
}

func (r *callFunctionRebinder) rebindKeywords(kwargs map[string]Value) map[string]Value {
	if len(kwargs) == 0 {
		return kwargs
	}
	out := make(map[string]Value, len(kwargs))
	for name, val := range kwargs {
		out[name] = r.rebindValue(val)
	}
	return out
}

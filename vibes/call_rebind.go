package vibes

import "reflect"

type callFunctionRebinder struct {
	root          *Env
	seenFunctions map[*ScriptFunction]*ScriptFunction
	seenArrays    map[sliceIdentity]Value
	seenMaps      map[uintptr]Value
}

func newCallFunctionRebinder(root *Env) *callFunctionRebinder {
	return &callFunctionRebinder{
		root:          root,
		seenFunctions: make(map[*ScriptFunction]*ScriptFunction),
		seenArrays:    make(map[sliceIdentity]Value),
		seenMaps:      make(map[uintptr]Value),
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
		if clone, seen := r.seenArrays[id]; seen {
			return clone
		}
		clonedItems := make([]Value, len(items))
		clonedArray := NewArray(clonedItems)
		r.seenArrays[id] = clonedArray
		for i := range items {
			clonedItems[i] = r.rebindValue(items[i])
		}
		return clonedArray
	case KindHash:
		entries := val.Hash()
		ptr := reflect.ValueOf(entries).Pointer()
		if clone, seen := r.seenMaps[ptr]; seen {
			return clone
		}
		clonedEntries := make(map[string]Value, len(entries))
		clonedHash := NewHash(clonedEntries)
		r.seenMaps[ptr] = clonedHash
		for key, item := range entries {
			clonedEntries[key] = r.rebindValue(item)
		}
		return clonedHash
	case KindObject:
		entries := val.Hash()
		ptr := reflect.ValueOf(entries).Pointer()
		if clone, seen := r.seenMaps[ptr]; seen {
			return clone
		}
		clonedEntries := make(map[string]Value, len(entries))
		clonedObject := NewObject(clonedEntries)
		r.seenMaps[ptr] = clonedObject
		for key, item := range entries {
			clonedEntries[key] = r.rebindValue(item)
		}
		return clonedObject
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

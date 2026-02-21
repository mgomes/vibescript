package vibes

import "maps"

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

func mergeHash(dest map[string]Value, src map[string]Value) map[string]Value {
	if len(src) == 0 {
		return dest
	}
	if dest == nil {
		dest = make(map[string]Value, len(src))
	}
	maps.Copy(dest, src)
	return dest
}

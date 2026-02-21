package vibes

import (
	"encoding/json"
	"fmt"
	"reflect"
)

func jsonValueToVibeValue(val any) (Value, error) {
	switch v := val.(type) {
	case nil:
		return NewNil(), nil
	case bool:
		return NewBool(v), nil
	case string:
		return NewString(v), nil
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return NewInt(i), nil
		}
		f, err := v.Float64()
		if err != nil {
			return NewNil(), fmt.Errorf("JSON.parse invalid number %q", v.String())
		}
		return NewFloat(f), nil
	case float64:
		return NewFloat(v), nil
	case []any:
		arr := make([]Value, len(v))
		for i, item := range v {
			converted, err := jsonValueToVibeValue(item)
			if err != nil {
				return NewNil(), err
			}
			arr[i] = converted
		}
		return NewArray(arr), nil
	case map[string]any:
		obj := make(map[string]Value, len(v))
		for key, item := range v {
			converted, err := jsonValueToVibeValue(item)
			if err != nil {
				return NewNil(), err
			}
			obj[key] = converted
		}
		return NewHash(obj), nil
	default:
		return NewNil(), fmt.Errorf("JSON.parse unsupported value type %T", val)
	}
}

func vibeValueToJSONValue(val Value, state *jsonStringifyState) (any, error) {
	switch val.Kind() {
	case KindNil:
		return nil, nil
	case KindBool:
		return val.Bool(), nil
	case KindInt:
		return val.Int(), nil
	case KindFloat:
		return val.Float(), nil
	case KindString, KindSymbol:
		return val.String(), nil
	case KindArray:
		arr := val.Array()
		id := reflect.ValueOf(arr).Pointer()
		if id != 0 {
			if _, seen := state.seenArrays[id]; seen {
				return nil, fmt.Errorf("JSON.stringify does not support cyclic arrays")
			}
			state.seenArrays[id] = struct{}{}
			defer delete(state.seenArrays, id)
		}

		out := make([]any, len(arr))
		for i, item := range arr {
			converted, err := vibeValueToJSONValue(item, state)
			if err != nil {
				return nil, err
			}
			out[i] = converted
		}
		return out, nil
	case KindHash, KindObject:
		hash := val.Hash()
		id := reflect.ValueOf(hash).Pointer()
		if id != 0 {
			if _, seen := state.seenHashes[id]; seen {
				return nil, fmt.Errorf("JSON.stringify does not support cyclic objects")
			}
			state.seenHashes[id] = struct{}{}
			defer delete(state.seenHashes, id)
		}

		out := make(map[string]any, len(hash))
		for key, item := range hash {
			converted, err := vibeValueToJSONValue(item, state)
			if err != nil {
				return nil, err
			}
			out[key] = converted
		}
		return out, nil
	default:
		return nil, fmt.Errorf("JSON.stringify unsupported value type %s", val.Kind())
	}
}

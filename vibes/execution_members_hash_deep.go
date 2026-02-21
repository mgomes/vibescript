package vibes

import (
	"fmt"
	"reflect"
)

func deepTransformKeys(exec *Execution, value Value, block Value) (Value, error) {
	return deepTransformKeysWithState(exec, value, block, &deepTransformState{
		seenHashes: make(map[uintptr]struct{}),
		seenArrays: make(map[uintptr]struct{}),
	})
}

type deepTransformState struct {
	seenHashes map[uintptr]struct{}
	seenArrays map[uintptr]struct{}
}

func deepTransformKeysWithState(exec *Execution, value Value, block Value, state *deepTransformState) (Value, error) {
	switch value.Kind() {
	case KindHash, KindObject:
		entries := value.Hash()
		id := reflect.ValueOf(entries).Pointer()
		if id != 0 {
			if _, seen := state.seenHashes[id]; seen {
				return NewNil(), fmt.Errorf("hash.deep_transform_keys does not support cyclic structures")
			}
			state.seenHashes[id] = struct{}{}
			defer delete(state.seenHashes, id)
		}
		out := make(map[string]Value, len(entries))
		for _, key := range sortedHashKeys(entries) {
			nextKeyValue, err := exec.CallBlock(block, []Value{NewSymbol(key)})
			if err != nil {
				return NewNil(), err
			}
			nextKey, err := valueToHashKey(nextKeyValue)
			if err != nil {
				return NewNil(), fmt.Errorf("hash.deep_transform_keys block must return symbol or string")
			}
			nextValue, err := deepTransformKeysWithState(exec, entries[key], block, state)
			if err != nil {
				return NewNil(), err
			}
			out[nextKey] = nextValue
		}
		return NewHash(out), nil
	case KindArray:
		items := value.Array()
		id := reflect.ValueOf(items).Pointer()
		if id != 0 {
			if _, seen := state.seenArrays[id]; seen {
				return NewNil(), fmt.Errorf("hash.deep_transform_keys does not support cyclic structures")
			}
			state.seenArrays[id] = struct{}{}
			defer delete(state.seenArrays, id)
		}
		out := make([]Value, len(items))
		for i, item := range items {
			nextValue, err := deepTransformKeysWithState(exec, item, block, state)
			if err != nil {
				return NewNil(), err
			}
			out[i] = nextValue
		}
		return NewArray(out), nil
	default:
		return value, nil
	}
}

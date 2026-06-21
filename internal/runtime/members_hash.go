package runtime

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sort"
)

// hashMemberNames mirrors the names dispatched by hashMember and feeds
// "did you mean" suggestions on the error path. Keep it in sync with the
// switch below; TestMemberSuggestionCandidatesResolve enforces that every
// listed name resolves.
var hashMemberNames = []string{
	"size", "length", "empty?", "key?", "has_key?", "member?", "include?", "value?", "has_value?", "keys", "values", "fetch", "dig", "each", "each_key", "each_value",
	"merge", "store", "slice", "except", "select", "reject", "transform_keys", "deep_transform_keys", "remap_keys", "transform_values", "compact",
}

var hashBuiltinMembers = newMemberTable(hashMemberNames)

// Most script hashes are small records/options; larger maps fall back to heap.
const smallHashKeyBufferSize = 8

func hashMember(obj Value, property string) (Value, error) {
	if member, ok := hashBuiltinMembers.lookup(property, hashMemberBuiltin); ok {
		return member, nil
	}
	candidates := slices.AppendSeq(slices.Clone(hashMemberNames), maps.Keys(obj.Hash()))
	return NewNil(), fmt.Errorf("unknown hash method %s%s", property, didYouMean(property, candidates))
}

func hashMemberBuiltin(property string) (Value, error) {
	switch property {
	case "size", "length", "empty?", "key?", "has_key?", "member?", "include?", "value?", "has_value?", "keys", "values", "fetch", "dig", "each", "each_key", "each_value":
		return hashMemberQuery(property)
	case "merge", "store", "slice", "except", "select", "reject", "transform_keys", "deep_transform_keys", "remap_keys", "transform_values", "compact":
		return hashMemberTransforms(property)
	default:
		return NewNil(), fmt.Errorf("unknown hash method %s", property)
	}
}

func sortedHashKeysInto(entries map[string]Value, buf []string) []string {
	keys := buf[:0]
	if cap(keys) < len(entries) {
		keys = make([]string, 0, len(entries))
	}
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func deepTransformKeys(exec *Execution, value, block Value) (Value, error) {
	return deepTransformKeysWithState(exec, value, block, &deepTransformState{
		seenHashes: make(map[uintptr]struct{}),
		seenArrays: make(map[uintptr]struct{}),
	})
}

type deepTransformState struct {
	seenHashes map[uintptr]struct{}
	seenArrays map[uintptr]struct{}
}

func deepTransformKeysWithState(exec *Execution, value, block Value, state *deepTransformState) (Value, error) {
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
		var blockArg [1]Value
		var keyBuf [smallHashKeyBufferSize]string
		for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
			blockArg[0] = NewSymbol(key)
			nextKeyValue, err := exec.CallBlock(block, blockArg[:])
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

func hashMemberQuery(property string) (Value, error) {
	switch property {
	case "size":
		return NewAutoBuiltin("hash.size", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.size does not take arguments")
			}
			return NewInt(int64(len(receiver.Hash()))), nil
		}), nil
	case "length":
		return NewAutoBuiltin("hash.length", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.length does not take arguments")
			}
			return NewInt(int64(len(receiver.Hash()))), nil
		}), nil
	case "empty?":
		return NewAutoBuiltin("hash.empty?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.empty? does not take arguments")
			}
			return NewBool(len(receiver.Hash()) == 0), nil
		}), nil
	case "key?", "has_key?", "member?", "include?":
		name := property
		return NewAutoBuiltin("hash."+name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("hash.%s expects exactly one key", name)
			}
			// Ruby's membership predicates accept any object as the candidate
			// key and report false when it is absent. Vibescript only stores
			// symbol/string keys, so an unsupported candidate type can never be
			// present and is reported as a non-member rather than a type error.
			key, err := valueToHashKey(args[0])
			if err != nil {
				return NewBool(false), nil
			}
			_, ok := receiver.Hash()[key]
			return NewBool(ok), nil
		}), nil
	case "value?", "has_value?":
		name := property
		return NewAutoBuiltin("hash."+name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("hash.%s expects exactly one value", name)
			}
			// Ruby compares the candidate against each stored value with ==.
			// Vibescript mirrors this with Value.Equal so deep collection and
			// scalar equality match Ruby's hash value membership semantics.
			for _, stored := range receiver.Hash() {
				if stored.Equal(args[0]) {
					return NewBool(true), nil
				}
			}
			return NewBool(false), nil
		}), nil
	case "keys":
		return NewAutoBuiltin("hash.keys", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.keys does not take arguments")
			}
			entries := receiver.Hash()
			var keyBuf [smallHashKeyBufferSize]string
			keys := sortedHashKeysInto(entries, keyBuf[:])
			values := make([]Value, len(keys))
			for i, k := range keys {
				values[i] = NewSymbol(k)
			}
			return NewArray(values), nil
		}), nil
	case "values":
		return NewAutoBuiltin("hash.values", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.values does not take arguments")
			}
			entries := receiver.Hash()
			var keyBuf [smallHashKeyBufferSize]string
			keys := sortedHashKeysInto(entries, keyBuf[:])
			values := make([]Value, len(keys))
			for i, k := range keys {
				values[i] = entries[k]
			}
			return NewArray(values), nil
		}), nil
	case "fetch":
		return NewAutoBuiltin("hash.fetch", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) < 1 || len(args) > 2 {
				return NewNil(), fmt.Errorf("hash.fetch expects key and optional default")
			}
			key, err := valueToHashKey(args[0])
			if err != nil {
				return NewNil(), fmt.Errorf("hash.fetch key must be symbol or string")
			}
			if value, ok := receiver.Hash()[key]; ok {
				return value, nil
			}
			if len(args) == 2 {
				return args[1], nil
			}
			return NewNil(), nil
		}), nil
	case "dig":
		return NewAutoBuiltin("hash.dig", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) == 0 {
				return NewNil(), fmt.Errorf("hash.dig expects at least one key")
			}
			current := receiver
			for _, arg := range args {
				key, err := valueToHashKey(arg)
				if err != nil {
					return NewNil(), fmt.Errorf("hash.dig path keys must be symbol or string")
				}
				if current.Kind() != KindHash && current.Kind() != KindObject {
					return NewNil(), nil
				}
				next, ok := current.Hash()[key]
				if !ok {
					return NewNil(), nil
				}
				current = next
			}
			return current, nil
		}), nil
	case "each":
		return NewAutoBuiltin("hash.each", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.each does not take arguments")
			}
			runner, err := newBlockCallRunner(exec, block, "hash.each")
			if err != nil {
				return NewNil(), err
			}
			entries := receiver.Hash()
			var blockArgs [2]Value
			var keyBuf [smallHashKeyBufferSize]string
			for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
				blockArgs[0] = NewSymbol(key)
				blockArgs[1] = entries[key]
				if _, err := runner.call(blockArgs[:]); err != nil {
					return NewNil(), err
				}
			}
			return receiver, nil
		}), nil
	case "each_key":
		return NewAutoBuiltin("hash.each_key", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.each_key does not take arguments")
			}
			runner, err := newBlockCallRunner(exec, block, "hash.each_key")
			if err != nil {
				return NewNil(), err
			}
			entries := receiver.Hash()
			var blockArg [1]Value
			var keyBuf [smallHashKeyBufferSize]string
			for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
				blockArg[0] = NewSymbol(key)
				if _, err := runner.call(blockArg[:]); err != nil {
					return NewNil(), err
				}
			}
			return receiver, nil
		}), nil
	case "each_value":
		return NewAutoBuiltin("hash.each_value", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.each_value does not take arguments")
			}
			runner, err := newBlockCallRunner(exec, block, "hash.each_value")
			if err != nil {
				return NewNil(), err
			}
			entries := receiver.Hash()
			var blockArg [1]Value
			var keyBuf [smallHashKeyBufferSize]string
			for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
				blockArg[0] = entries[key]
				if _, err := runner.call(blockArg[:]); err != nil {
					return NewNil(), err
				}
			}
			return receiver, nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown hash method %s", property)
	}
}

func hashMemberTransforms(property string) (Value, error) {
	switch property {
	case "merge":
		return NewBuiltin("hash.merge", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 || (args[0].Kind() != KindHash && args[0].Kind() != KindObject) {
				return NewNil(), fmt.Errorf("hash.merge expects a single hash argument")
			}
			base := receiver.Hash()
			addition := args[0].Hash()
			out := make(map[string]Value, len(base)+len(addition))
			maps.Copy(out, base)
			maps.Copy(out, addition)
			return NewHash(out), nil
		}), nil
	case "store":
		return NewBuiltin("hash.store", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 2 {
				return NewNil(), fmt.Errorf("hash.store expects a key and a value")
			}
			key, err := valueToHashKey(args[0])
			if err != nil {
				return NewNil(), fmt.Errorf("hash.store key must be symbol or string")
			}
			// Vibescript's method-based hash helpers are immutable-style: store
			// returns a new hash with the key assigned rather than mutating the
			// receiver, matching merge and the array collection helpers.
			base := receiver.Hash()
			out := make(map[string]Value, len(base)+1)
			maps.Copy(out, base)
			out[key] = args[1]
			return NewHash(out), nil
		}), nil
	case "slice":
		return NewBuiltin("hash.slice", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			entries := receiver.Hash()
			out := make(map[string]Value, len(args))
			for _, arg := range args {
				key, err := valueToHashKey(arg)
				if err != nil {
					return NewNil(), fmt.Errorf("hash.slice keys must be symbol or string")
				}
				if value, ok := entries[key]; ok {
					out[key] = value
				}
			}
			return NewHash(out), nil
		}), nil
	case "except":
		return NewBuiltin("hash.except", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			excluded := make(map[string]struct{}, len(args))
			for _, arg := range args {
				key, err := valueToHashKey(arg)
				if err != nil {
					return NewNil(), fmt.Errorf("hash.except keys must be symbol or string")
				}
				excluded[key] = struct{}{}
			}
			entries := receiver.Hash()
			out := make(map[string]Value, len(entries))
			for key, value := range entries {
				if _, skip := excluded[key]; skip {
					continue
				}
				out[key] = value
			}
			return NewHash(out), nil
		}), nil
	case "select":
		return NewAutoBuiltin("hash.select", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.select does not take arguments")
			}
			runner, err := newBlockCallRunner(exec, block, "hash.select")
			if err != nil {
				return NewNil(), err
			}
			entries := receiver.Hash()
			out := make(map[string]Value, len(entries))
			var blockArgs [2]Value
			var keyBuf [smallHashKeyBufferSize]string
			for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
				blockArgs[0] = NewSymbol(key)
				blockArgs[1] = entries[key]
				include, err := runner.call(blockArgs[:])
				if err != nil {
					return NewNil(), err
				}
				if include.Truthy() {
					out[key] = entries[key]
				}
			}
			return NewHash(out), nil
		}), nil
	case "reject":
		return NewAutoBuiltin("hash.reject", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.reject does not take arguments")
			}
			runner, err := newBlockCallRunner(exec, block, "hash.reject")
			if err != nil {
				return NewNil(), err
			}
			entries := receiver.Hash()
			out := make(map[string]Value, len(entries))
			var blockArgs [2]Value
			var keyBuf [smallHashKeyBufferSize]string
			for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
				blockArgs[0] = NewSymbol(key)
				blockArgs[1] = entries[key]
				exclude, err := runner.call(blockArgs[:])
				if err != nil {
					return NewNil(), err
				}
				if !exclude.Truthy() {
					out[key] = entries[key]
				}
			}
			return NewHash(out), nil
		}), nil
	case "transform_keys":
		return NewAutoBuiltin("hash.transform_keys", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.transform_keys does not take arguments")
			}
			runner, err := newBlockCallRunner(exec, block, "hash.transform_keys")
			if err != nil {
				return NewNil(), err
			}
			entries := receiver.Hash()
			out := make(map[string]Value, len(entries))
			var blockArg [1]Value
			var keyBuf [smallHashKeyBufferSize]string
			for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
				blockArg[0] = NewSymbol(key)
				nextKey, err := runner.call(blockArg[:])
				if err != nil {
					return NewNil(), err
				}
				resolved, err := valueToHashKey(nextKey)
				if err != nil {
					return NewNil(), fmt.Errorf("hash.transform_keys block must return symbol or string")
				}
				out[resolved] = entries[key]
			}
			return NewHash(out), nil
		}), nil
	case "deep_transform_keys":
		return NewAutoBuiltin("hash.deep_transform_keys", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.deep_transform_keys does not take arguments")
			}
			if err := ensureBlock(block, "hash.deep_transform_keys"); err != nil {
				return NewNil(), err
			}
			return deepTransformKeys(exec, receiver, block)
		}), nil
	case "remap_keys":
		return NewBuiltin("hash.remap_keys", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 || (args[0].Kind() != KindHash && args[0].Kind() != KindObject) {
				return NewNil(), fmt.Errorf("hash.remap_keys expects a key mapping hash")
			}
			entries := receiver.Hash()
			mapping := args[0].Hash()
			out := make(map[string]Value, len(entries))
			var keyBuf [smallHashKeyBufferSize]string
			for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
				value := entries[key]
				if mapped, ok := mapping[key]; ok {
					nextKey, err := valueToHashKey(mapped)
					if err != nil {
						return NewNil(), fmt.Errorf("hash.remap_keys mapping values must be symbol or string")
					}
					out[nextKey] = value
					continue
				}
				out[key] = value
			}
			return NewHash(out), nil
		}), nil
	case "transform_values":
		return NewAutoBuiltin("hash.transform_values", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.transform_values does not take arguments")
			}
			runner, err := newBlockCallRunner(exec, block, "hash.transform_values")
			if err != nil {
				return NewNil(), err
			}
			entries := receiver.Hash()
			out := make(map[string]Value, len(entries))
			var blockArg [1]Value
			var keyBuf [smallHashKeyBufferSize]string
			for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
				blockArg[0] = entries[key]
				nextValue, err := runner.call(blockArg[:])
				if err != nil {
					return NewNil(), err
				}
				out[key] = nextValue
			}
			return NewHash(out), nil
		}), nil
	case "compact":
		return NewAutoBuiltin("hash.compact", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.compact does not take arguments")
			}
			entries := receiver.Hash()
			out := make(map[string]Value, len(entries))
			for k, v := range entries {
				if v.Kind() != KindNil {
					out[k] = v
				}
			}
			return NewHash(out), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown hash method %s", property)
	}
}

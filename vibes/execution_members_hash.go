package vibes

import (
	"fmt"
	"maps"
	"sort"
)

func hashMember(obj Value, property string) (Value, error) {
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
	case "key?", "has_key?", "include?":
		name := property
		return NewAutoBuiltin("hash."+name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("hash.%s expects exactly one key", name)
			}
			key, err := valueToHashKey(args[0])
			if err != nil {
				return NewNil(), fmt.Errorf("hash.%s key must be symbol or string", name)
			}
			_, ok := receiver.Hash()[key]
			return NewBool(ok), nil
		}), nil
	case "keys":
		return NewAutoBuiltin("hash.keys", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.keys does not take arguments")
			}
			keys := sortedHashKeys(receiver.Hash())
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
			keys := sortedHashKeys(entries)
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
			if err := ensureBlock(block, "hash.each"); err != nil {
				return NewNil(), err
			}
			entries := receiver.Hash()
			for _, key := range sortedHashKeys(entries) {
				if _, err := exec.CallBlock(block, []Value{NewSymbol(key), entries[key]}); err != nil {
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
			if err := ensureBlock(block, "hash.each_key"); err != nil {
				return NewNil(), err
			}
			for _, key := range sortedHashKeys(receiver.Hash()) {
				if _, err := exec.CallBlock(block, []Value{NewSymbol(key)}); err != nil {
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
			if err := ensureBlock(block, "hash.each_value"); err != nil {
				return NewNil(), err
			}
			entries := receiver.Hash()
			for _, key := range sortedHashKeys(entries) {
				if _, err := exec.CallBlock(block, []Value{entries[key]}); err != nil {
					return NewNil(), err
				}
			}
			return receiver, nil
		}), nil
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
			if err := ensureBlock(block, "hash.select"); err != nil {
				return NewNil(), err
			}
			entries := receiver.Hash()
			out := make(map[string]Value, len(entries))
			for _, key := range sortedHashKeys(entries) {
				include, err := exec.CallBlock(block, []Value{NewSymbol(key), entries[key]})
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
			if err := ensureBlock(block, "hash.reject"); err != nil {
				return NewNil(), err
			}
			entries := receiver.Hash()
			out := make(map[string]Value, len(entries))
			for _, key := range sortedHashKeys(entries) {
				exclude, err := exec.CallBlock(block, []Value{NewSymbol(key), entries[key]})
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
			if err := ensureBlock(block, "hash.transform_keys"); err != nil {
				return NewNil(), err
			}
			entries := receiver.Hash()
			out := make(map[string]Value, len(entries))
			for _, key := range sortedHashKeys(entries) {
				nextKey, err := exec.CallBlock(block, []Value{NewSymbol(key)})
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
			for _, key := range sortedHashKeys(entries) {
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
			if err := ensureBlock(block, "hash.transform_values"); err != nil {
				return NewNil(), err
			}
			entries := receiver.Hash()
			out := make(map[string]Value, len(entries))
			for _, key := range sortedHashKeys(entries) {
				nextValue, err := exec.CallBlock(block, []Value{entries[key]})
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

func sortedHashKeys(entries map[string]Value) []string {
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

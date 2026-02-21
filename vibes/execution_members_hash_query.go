package vibes

import "fmt"

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
	default:
		return NewNil(), fmt.Errorf("unknown hash method %s", property)
	}
}

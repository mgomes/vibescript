package vibes

import "fmt"

func arrayMemberQuery(property string) (Value, error) {
	switch property {
	case "size":
		return NewAutoBuiltin("array.size", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.size does not take arguments")
			}
			return NewInt(int64(len(receiver.Array()))), nil
		}), nil
	case "each":
		return NewAutoBuiltin("array.each", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if err := ensureBlock(block, "array.each"); err != nil {
				return NewNil(), err
			}
			for _, item := range receiver.Array() {
				if _, err := exec.CallBlock(block, []Value{item}); err != nil {
					return NewNil(), err
				}
			}
			return receiver, nil
		}), nil
	case "map":
		return NewAutoBuiltin("array.map", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if err := ensureBlock(block, "array.map"); err != nil {
				return NewNil(), err
			}
			arr := receiver.Array()
			result := make([]Value, len(arr))
			for i, item := range arr {
				val, err := exec.CallBlock(block, []Value{item})
				if err != nil {
					return NewNil(), err
				}
				result[i] = val
			}
			return NewArray(result), nil
		}), nil
	case "select":
		return NewAutoBuiltin("array.select", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if err := ensureBlock(block, "array.select"); err != nil {
				return NewNil(), err
			}
			arr := receiver.Array()
			out := make([]Value, 0, len(arr))
			for _, item := range arr {
				val, err := exec.CallBlock(block, []Value{item})
				if err != nil {
					return NewNil(), err
				}
				if val.Truthy() {
					out = append(out, item)
				}
			}
			return NewArray(out), nil
		}), nil
	case "find":
		return NewAutoBuiltin("array.find", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.find does not take arguments")
			}
			if err := ensureBlock(block, "array.find"); err != nil {
				return NewNil(), err
			}
			for _, item := range receiver.Array() {
				match, err := exec.CallBlock(block, []Value{item})
				if err != nil {
					return NewNil(), err
				}
				if match.Truthy() {
					return item, nil
				}
			}
			return NewNil(), nil
		}), nil
	case "find_index":
		return NewAutoBuiltin("array.find_index", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.find_index does not take arguments")
			}
			if err := ensureBlock(block, "array.find_index"); err != nil {
				return NewNil(), err
			}
			for idx, item := range receiver.Array() {
				match, err := exec.CallBlock(block, []Value{item})
				if err != nil {
					return NewNil(), err
				}
				if match.Truthy() {
					return NewInt(int64(idx)), nil
				}
			}
			return NewNil(), nil
		}), nil
	case "reduce":
		return NewAutoBuiltin("array.reduce", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if err := ensureBlock(block, "array.reduce"); err != nil {
				return NewNil(), err
			}
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("array.reduce accepts at most one initial value")
			}
			arr := receiver.Array()
			if len(arr) == 0 && len(args) == 0 {
				return NewNil(), fmt.Errorf("array.reduce on empty array requires an initial value")
			}
			var acc Value
			start := 0
			if len(args) == 1 {
				acc = args[0]
			} else {
				acc = arr[0]
				start = 1
			}
			for i := start; i < len(arr); i++ {
				next, err := exec.CallBlock(block, []Value{acc, arr[i]})
				if err != nil {
					return NewNil(), err
				}
				acc = next
			}
			return acc, nil
		}), nil
	case "include?":
		return NewAutoBuiltin("array.include?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("array.include? expects exactly one value")
			}
			for _, item := range receiver.Array() {
				if item.Equal(args[0]) {
					return NewBool(true), nil
				}
			}
			return NewBool(false), nil
		}), nil
	case "index":
		return NewAutoBuiltin("array.index", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) < 1 || len(args) > 2 {
				return NewNil(), fmt.Errorf("array.index expects value and optional offset")
			}
			offset := 0
			if len(args) == 2 {
				n, err := valueToInt(args[1])
				if err != nil || n < 0 {
					return NewNil(), fmt.Errorf("array.index offset must be non-negative integer")
				}
				offset = n
			}
			arr := receiver.Array()
			if offset >= len(arr) {
				return NewNil(), nil
			}
			for idx := offset; idx < len(arr); idx++ {
				if arr[idx].Equal(args[0]) {
					return NewInt(int64(idx)), nil
				}
			}
			return NewNil(), nil
		}), nil
	case "rindex":
		return NewAutoBuiltin("array.rindex", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) < 1 || len(args) > 2 {
				return NewNil(), fmt.Errorf("array.rindex expects value and optional offset")
			}
			offset := -1
			if len(args) == 2 {
				n, err := valueToInt(args[1])
				if err != nil || n < 0 {
					return NewNil(), fmt.Errorf("array.rindex offset must be non-negative integer")
				}
				offset = n
			}
			arr := receiver.Array()
			if len(arr) == 0 {
				return NewNil(), nil
			}
			if offset < 0 {
				offset = len(arr) - 1
			}
			if offset >= len(arr) {
				offset = len(arr) - 1
			}
			for idx := offset; idx >= 0; idx-- {
				if arr[idx].Equal(args[0]) {
					return NewInt(int64(idx)), nil
				}
			}
			return NewNil(), nil
		}), nil
	case "count":
		return NewAutoBuiltin("array.count", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("array.count accepts at most one value argument")
			}
			arr := receiver.Array()
			if len(args) == 1 {
				if block.Block() != nil {
					return NewNil(), fmt.Errorf("array.count does not accept both argument and block")
				}
				total := int64(0)
				for _, item := range arr {
					if item.Equal(args[0]) {
						total++
					}
				}
				return NewInt(total), nil
			}
			if block.Block() == nil {
				return NewInt(int64(len(arr))), nil
			}
			total := int64(0)
			for _, item := range arr {
				include, err := exec.CallBlock(block, []Value{item})
				if err != nil {
					return NewNil(), err
				}
				if include.Truthy() {
					total++
				}
			}
			return NewInt(total), nil
		}), nil
	case "any?":
		return NewAutoBuiltin("array.any?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.any? does not take arguments")
			}
			for _, item := range receiver.Array() {
				if block.Block() != nil {
					val, err := exec.CallBlock(block, []Value{item})
					if err != nil {
						return NewNil(), err
					}
					if val.Truthy() {
						return NewBool(true), nil
					}
					continue
				}
				if item.Truthy() {
					return NewBool(true), nil
				}
			}
			return NewBool(false), nil
		}), nil
	case "all?":
		return NewAutoBuiltin("array.all?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.all? does not take arguments")
			}
			for _, item := range receiver.Array() {
				if block.Block() != nil {
					val, err := exec.CallBlock(block, []Value{item})
					if err != nil {
						return NewNil(), err
					}
					if !val.Truthy() {
						return NewBool(false), nil
					}
					continue
				}
				if !item.Truthy() {
					return NewBool(false), nil
				}
			}
			return NewBool(true), nil
		}), nil
	case "none?":
		return NewAutoBuiltin("array.none?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.none? does not take arguments")
			}
			for _, item := range receiver.Array() {
				if block.Block() != nil {
					val, err := exec.CallBlock(block, []Value{item})
					if err != nil {
						return NewNil(), err
					}
					if val.Truthy() {
						return NewBool(false), nil
					}
					continue
				}
				if item.Truthy() {
					return NewBool(false), nil
				}
			}
			return NewBool(true), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown array method %s", property)
	}
}

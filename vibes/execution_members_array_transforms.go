package vibes

import (
	"fmt"
	"slices"
	"strings"
)

func arrayMemberTransforms(property string) (Value, error) {
	switch property {
	case "push":
		return NewBuiltin("array.push", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) == 0 {
				return NewNil(), fmt.Errorf("array.push expects at least one argument")
			}
			base := receiver.Array()
			out := make([]Value, len(base)+len(args))
			copy(out, base)
			copy(out[len(base):], args)
			return NewArray(out), nil
		}), nil
	case "pop":
		return NewAutoBuiltin("array.pop", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("array.pop accepts at most one argument")
			}
			count := 1
			if len(args) == 1 {
				n, err := valueToInt(args[0])
				if err != nil || n < 0 {
					return NewNil(), fmt.Errorf("array.pop expects non-negative integer")
				}
				count = n
			}
			arr := receiver.Array()
			if count == 0 {
				return NewHash(map[string]Value{
					"array":  NewArray(arr),
					"popped": NewNil(),
				}), nil
			}
			if len(arr) == 0 {
				popped := NewNil()
				if len(args) == 1 {
					popped = NewArray([]Value{})
				}
				return NewHash(map[string]Value{
					"array":  NewArray([]Value{}),
					"popped": popped,
				}), nil
			}
			if count > len(arr) {
				count = len(arr)
			}
			remaining := make([]Value, len(arr)-count)
			copy(remaining, arr[:len(arr)-count])
			removed := make([]Value, count)
			copy(removed, arr[len(arr)-count:])
			result := map[string]Value{
				"array": NewArray(remaining),
			}
			if count == 1 && len(args) == 0 {
				result["popped"] = removed[0]
			} else {
				result["popped"] = NewArray(removed)
			}
			return NewHash(result), nil
		}), nil
	case "uniq":
		return NewAutoBuiltin("array.uniq", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.uniq does not take arguments")
			}
			arr := receiver.Array()
			unique := make([]Value, 0, len(arr))
			for _, item := range arr {
				found := slices.ContainsFunc(unique, item.Equal)
				if !found {
					unique = append(unique, item)
				}
			}
			return NewArray(unique), nil
		}), nil
	case "first":
		return NewAutoBuiltin("array.first", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			arr := receiver.Array()
			if len(args) == 0 {
				if len(arr) == 0 {
					return NewNil(), nil
				}
				return arr[0], nil
			}
			n, err := valueToInt(args[0])
			if err != nil || n < 0 {
				return NewNil(), fmt.Errorf("array.first expects non-negative integer")
			}
			if n > len(arr) {
				n = len(arr)
			}
			out := make([]Value, n)
			copy(out, arr[:n])
			return NewArray(out), nil
		}), nil
	case "last":
		return NewAutoBuiltin("array.last", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			arr := receiver.Array()
			if len(args) == 0 {
				if len(arr) == 0 {
					return NewNil(), nil
				}
				return arr[len(arr)-1], nil
			}
			n, err := valueToInt(args[0])
			if err != nil || n < 0 {
				return NewNil(), fmt.Errorf("array.last expects non-negative integer")
			}
			if n > len(arr) {
				n = len(arr)
			}
			out := make([]Value, n)
			copy(out, arr[len(arr)-n:])
			return NewArray(out), nil
		}), nil
	case "sum":
		return NewAutoBuiltin("array.sum", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			arr := receiver.Array()
			total := NewInt(0)
			for _, item := range arr {
				switch item.Kind() {
				case KindInt, KindFloat:
				default:
					return NewNil(), fmt.Errorf("array.sum supports numeric values")
				}
				sum, err := addValues(total, item)
				if err != nil {
					return NewNil(), fmt.Errorf("array.sum supports numeric values")
				}
				total = sum
			}
			return total, nil
		}), nil
	case "compact":
		return NewAutoBuiltin("array.compact", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.compact does not take arguments")
			}
			arr := receiver.Array()
			out := make([]Value, 0, len(arr))
			for _, item := range arr {
				if item.Kind() != KindNil {
					out = append(out, item)
				}
			}
			return NewArray(out), nil
		}), nil
	case "flatten":
		return NewAutoBuiltin("array.flatten", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			// depth=-1 is a sentinel value meaning "flatten fully" (no depth limit)
			depth := -1
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("array.flatten accepts at most one depth argument")
			}
			if len(args) == 1 {
				n, err := valueToInt(args[0])
				if err != nil || n < 0 {
					return NewNil(), fmt.Errorf("array.flatten depth must be non-negative integer")
				}
				depth = n
			}
			arr := receiver.Array()
			out := flattenValues(arr, depth)
			return NewArray(out), nil
		}), nil
	case "chunk":
		return NewAutoBuiltin("array.chunk", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("array.chunk expects a chunk size")
			}
			sizeValue := args[0]
			maxNativeInt := int64(^uint(0) >> 1)
			if sizeValue.Kind() != KindInt || sizeValue.Int() <= 0 || sizeValue.Int() > maxNativeInt {
				return NewNil(), fmt.Errorf("array.chunk size must be a positive integer")
			}
			size := int(sizeValue.Int())
			arr := receiver.Array()
			if len(arr) == 0 {
				return NewArray([]Value{}), nil
			}
			chunkCapacity := len(arr) / size
			if len(arr)%size != 0 {
				chunkCapacity++
			}
			chunks := make([]Value, 0, chunkCapacity)
			for i := 0; i < len(arr); i += size {
				end := i + size
				if end > len(arr) {
					end = len(arr)
				}
				part := make([]Value, end-i)
				copy(part, arr[i:end])
				chunks = append(chunks, NewArray(part))
			}
			return NewArray(chunks), nil
		}), nil
	case "window":
		return NewAutoBuiltin("array.window", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("array.window expects a window size")
			}
			sizeValue := args[0]
			maxNativeInt := int64(^uint(0) >> 1)
			if sizeValue.Kind() != KindInt || sizeValue.Int() <= 0 || sizeValue.Int() > maxNativeInt {
				return NewNil(), fmt.Errorf("array.window size must be a positive integer")
			}
			size := int(sizeValue.Int())
			arr := receiver.Array()
			if size > len(arr) {
				return NewArray([]Value{}), nil
			}
			windows := make([]Value, 0, len(arr)-size+1)
			for i := 0; i+size <= len(arr); i++ {
				part := make([]Value, size)
				copy(part, arr[i:i+size])
				windows = append(windows, NewArray(part))
			}
			return NewArray(windows), nil
		}), nil
	case "join":
		return NewAutoBuiltin("array.join", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("array.join accepts at most one separator")
			}
			sep := ""
			if len(args) == 1 {
				if args[0].Kind() != KindString {
					return NewNil(), fmt.Errorf("array.join separator must be string")
				}
				sep = args[0].String()
			}
			arr := receiver.Array()
			if len(arr) == 0 {
				return NewString(""), nil
			}
			// Use strings.Builder for efficient concatenation
			var b strings.Builder
			for i, item := range arr {
				if i > 0 {
					b.WriteString(sep)
				}
				b.WriteString(item.String())
			}
			return NewString(b.String()), nil
		}), nil
	case "reverse":
		return NewAutoBuiltin("array.reverse", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.reverse does not take arguments")
			}
			arr := receiver.Array()
			out := make([]Value, len(arr))
			for i, item := range arr {
				out[len(arr)-1-i] = item
			}
			return NewArray(out), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown array method %s", property)
	}
}

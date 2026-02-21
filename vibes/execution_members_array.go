package vibes

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

func arrayMember(array Value, property string) (Value, error) {
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
	case "sort":
		return NewAutoBuiltin("array.sort", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.sort does not take arguments")
			}
			arr := receiver.Array()
			out := make([]Value, len(arr))
			copy(out, arr)
			var sortErr error
			sort.SliceStable(out, func(i, j int) bool {
				if sortErr != nil {
					return false
				}
				if block.Block() != nil {
					cmpValue, err := exec.CallBlock(block, []Value{out[i], out[j]})
					if err != nil {
						sortErr = err
						return false
					}
					cmp, err := sortComparisonResult(cmpValue)
					if err != nil {
						sortErr = fmt.Errorf("array.sort block must return numeric comparator")
						return false
					}
					return cmp < 0
				}
				cmp, err := arraySortCompareValues(out[i], out[j])
				if err != nil {
					sortErr = fmt.Errorf("array.sort values are not comparable")
					return false
				}
				return cmp < 0
			})
			if sortErr != nil {
				return NewNil(), sortErr
			}
			return NewArray(out), nil
		}), nil
	case "sort_by":
		return NewAutoBuiltin("array.sort_by", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.sort_by does not take arguments")
			}
			if err := ensureBlock(block, "array.sort_by"); err != nil {
				return NewNil(), err
			}
			type itemWithSortKey struct {
				item  Value
				key   Value
				index int
			}
			arr := receiver.Array()
			withKeys := make([]itemWithSortKey, len(arr))
			for i, item := range arr {
				sortKey, err := exec.CallBlock(block, []Value{item})
				if err != nil {
					return NewNil(), err
				}
				withKeys[i] = itemWithSortKey{item: item, key: sortKey, index: i}
			}
			var sortErr error
			sort.SliceStable(withKeys, func(i, j int) bool {
				if sortErr != nil {
					return false
				}
				cmp, err := arraySortCompareValues(withKeys[i].key, withKeys[j].key)
				if err != nil {
					sortErr = fmt.Errorf("array.sort_by block values are not comparable")
					return false
				}
				if cmp == 0 {
					return withKeys[i].index < withKeys[j].index
				}
				return cmp < 0
			})
			if sortErr != nil {
				return NewNil(), sortErr
			}
			out := make([]Value, len(withKeys))
			for i, item := range withKeys {
				out[i] = item.item
			}
			return NewArray(out), nil
		}), nil
	case "partition":
		return NewAutoBuiltin("array.partition", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.partition does not take arguments")
			}
			if err := ensureBlock(block, "array.partition"); err != nil {
				return NewNil(), err
			}
			arr := receiver.Array()
			left := make([]Value, 0, len(arr))
			right := make([]Value, 0, len(arr))
			for _, item := range arr {
				match, err := exec.CallBlock(block, []Value{item})
				if err != nil {
					return NewNil(), err
				}
				if match.Truthy() {
					left = append(left, item)
				} else {
					right = append(right, item)
				}
			}
			return NewArray([]Value{NewArray(left), NewArray(right)}), nil
		}), nil
	case "group_by":
		return NewAutoBuiltin("array.group_by", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.group_by does not take arguments")
			}
			if err := ensureBlock(block, "array.group_by"); err != nil {
				return NewNil(), err
			}
			arr := receiver.Array()
			groups := make(map[string][]Value, len(arr))
			for _, item := range arr {
				groupValue, err := exec.CallBlock(block, []Value{item})
				if err != nil {
					return NewNil(), err
				}
				key, err := valueToHashKey(groupValue)
				if err != nil {
					return NewNil(), fmt.Errorf("array.group_by block must return symbol or string")
				}
				groups[key] = append(groups[key], item)
			}
			result := make(map[string]Value, len(groups))
			for key, items := range groups {
				result[key] = NewArray(items)
			}
			return NewHash(result), nil
		}), nil
	case "group_by_stable":
		return NewAutoBuiltin("array.group_by_stable", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.group_by_stable does not take arguments")
			}
			if err := ensureBlock(block, "array.group_by_stable"); err != nil {
				return NewNil(), err
			}
			arr := receiver.Array()
			order := make([]string, 0, len(arr))
			keyValues := make(map[string]Value, len(arr))
			groups := make(map[string][]Value, len(arr))
			for _, item := range arr {
				groupValue, err := exec.CallBlock(block, []Value{item})
				if err != nil {
					return NewNil(), err
				}
				key, err := valueToHashKey(groupValue)
				if err != nil {
					return NewNil(), fmt.Errorf("array.group_by_stable block must return symbol or string")
				}
				if _, exists := groups[key]; !exists {
					order = append(order, key)
					keyValues[key] = groupValue
					groups[key] = []Value{}
				}
				groups[key] = append(groups[key], item)
			}
			result := make([]Value, 0, len(order))
			for _, key := range order {
				result = append(result, NewArray([]Value{
					keyValues[key],
					NewArray(groups[key]),
				}))
			}
			return NewArray(result), nil
		}), nil
	case "tally":
		return NewAutoBuiltin("array.tally", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.tally does not take arguments")
			}
			arr := receiver.Array()
			counts := make(map[string]int64, len(arr))
			for _, item := range arr {
				keyValue := item
				if block.Block() != nil {
					mapped, err := exec.CallBlock(block, []Value{item})
					if err != nil {
						return NewNil(), err
					}
					keyValue = mapped
				}
				key, err := valueToHashKey(keyValue)
				if err != nil {
					return NewNil(), fmt.Errorf("array.tally values must be symbol or string")
				}
				counts[key]++
			}
			result := make(map[string]Value, len(counts))
			for key, count := range counts {
				result[key] = NewInt(count)
			}
			return NewHash(result), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown array method %s", property)
	}
}

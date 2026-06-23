package runtime

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

// arrayMemberNames mirrors the names dispatched by arrayMember and feeds
// "did you mean" suggestions on the error path. Keep it in sync with the
// switch below; TestMemberSuggestionCandidatesResolve enforces that every
// listed name resolves.
var arrayMemberNames = []string{
	"size", "length", "empty?", "each", "each_slice", "each_cons", "reverse_each", "cycle", "map", "select", "reject", "find", "find_index", "reduce", "include?", "index", "rindex", "fetch", "count", "any?", "all?", "none?",
	"take_while", "drop_while", "grep", "grep_v",
	"push", "pop", "uniq", "first", "last", "sum", "compact", "flatten", "fill", "chunk", "window", "join", "reverse",
	"take", "drop", "zip", "transpose", "union", "difference",
	"sort", "sort_by", "partition", "group_by", "group_by_stable", "tally",
	"min", "max", "minmax", "min_by", "max_by",
}

var arrayBuiltinMembers = newMemberTable(arrayMemberNames)

func arrayMember(array Value, property string) (Value, error) {
	if member, ok := arrayBuiltinMembers.lookup(property, arrayMemberBuiltin); ok {
		return member, nil
	}
	return NewNil(), fmt.Errorf("unknown array method %s%s", property, didYouMean(property, arrayMemberNames))
}

func arrayMemberBuiltin(property string) (Value, error) {
	switch property {
	case "size", "length", "empty?", "each", "each_slice", "each_cons", "reverse_each", "cycle", "map", "select", "reject", "find", "find_index", "reduce", "include?", "index", "rindex", "fetch", "count", "any?", "all?", "none?",
		"take_while", "drop_while", "grep", "grep_v":
		return arrayMemberQuery(property)
	case "push", "pop", "uniq", "first", "last", "sum", "compact", "flatten", "fill", "chunk", "window", "join", "reverse", "take", "drop", "zip", "transpose", "union", "difference":
		return arrayMemberTransforms(property)
	case "sort", "sort_by", "partition", "group_by", "group_by_stable", "tally":
		return arrayMemberGrouping(property)
	case "min", "max", "minmax", "min_by", "max_by":
		return arrayMemberExtrema(property)
	default:
		return NewNil(), fmt.Errorf("unknown array method %s", property)
	}
}

func arrayMemberGrouping(property string) (Value, error) {
	switch property {
	case "sort":
		return NewAutoBuiltin("array.sort", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.sort does not take arguments")
			}
			arr := receiver.Array()
			out := make([]Value, len(arr))
			copy(out, arr)
			var runner *blockCallRunner
			if valueBlock(block) != nil {
				var err error
				runner, err = newBlockCallRunner(exec, block, "array.sort")
				if err != nil {
					return NewNil(), err
				}
			}
			var comparatorArgs [2]Value
			var sortErr error
			sort.SliceStable(out, func(i, j int) bool {
				if sortErr != nil {
					return false
				}
				if runner != nil {
					comparatorArgs[0] = out[i]
					comparatorArgs[1] = out[j]
					cmpValue, err := runner.call(comparatorArgs[:])
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
			runner, err := newBlockCallRunner(exec, block, "array.sort_by")
			if err != nil {
				return NewNil(), err
			}
			type itemWithSortKey struct {
				item  Value
				key   Value
				index int
			}
			arr := receiver.Array()
			withKeys := make([]itemWithSortKey, len(arr))
			var blockArg [1]Value
			for i, item := range arr {
				blockArg[0] = item
				sortKey, err := runner.call(blockArg[:])
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
			runner, err := newBlockCallRunner(exec, block, "array.partition")
			if err != nil {
				return NewNil(), err
			}
			arr := receiver.Array()
			initialCapacity := arrayPartitionInitialCapacity(len(arr))
			left := make([]Value, 0, initialCapacity)
			right := make([]Value, 0, initialCapacity)
			var blockArg [1]Value
			for _, item := range arr {
				blockArg[0] = item
				match, err := runner.call(blockArg[:])
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
			runner, err := newBlockCallRunner(exec, block, "array.group_by")
			if err != nil {
				return NewNil(), err
			}
			arr := receiver.Array()
			groups := make(map[string][]Value, arrayGroupingInitialCapacity(len(arr)))
			var blockArg [1]Value
			for _, item := range arr {
				blockArg[0] = item
				groupValue, err := runner.call(blockArg[:])
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
			runner, err := newBlockCallRunner(exec, block, "array.group_by_stable")
			if err != nil {
				return NewNil(), err
			}
			arr := receiver.Array()
			initialCapacity := arrayGroupingInitialCapacity(len(arr))
			order := make([]string, 0, initialCapacity)
			keyValues := make(map[string]Value, initialCapacity)
			groups := make(map[string][]Value, initialCapacity)
			var blockArg [1]Value
			for _, item := range arr {
				blockArg[0] = item
				groupValue, err := runner.call(blockArg[:])
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
			hasBlock := valueBlock(block) != nil
			initialCapacity, err := arrayTallyInitialCapacity(arr, hasBlock)
			if err != nil {
				return NewNil(), fmt.Errorf("array.tally values must be symbol or string")
			}
			counts := make(map[string]int64, initialCapacity)
			var runner *blockCallRunner
			if hasBlock {
				runner, err = newBlockCallRunner(exec, block, "array.tally")
				if err != nil {
					return NewNil(), err
				}
			}
			var blockArg [1]Value
			for _, item := range arr {
				keyValue := item
				if hasBlock {
					blockArg[0] = item
					mapped, err := runner.call(blockArg[:])
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

func arrayMemberExtrema(property string) (Value, error) {
	switch property {
	case "min":
		return arrayMemberMinMax("array.min", false), nil
	case "max":
		return arrayMemberMinMax("array.max", true), nil
	case "minmax":
		minmax := NewAutoBuiltin("array.minmax", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.minmax does not take arguments")
			}
			if valueBlock(block) != nil {
				return NewNil(), fmt.Errorf("array.minmax does not accept a block")
			}
			arr := receiver.Array()
			if len(arr) == 0 {
				return NewArray([]Value{NewNil(), NewNil()}), nil
			}
			minVal := arr[0]
			maxVal := arr[0]
			for _, item := range arr[1:] {
				cmpMin, err := arraySortCompareValues(item, minVal)
				if err != nil {
					return NewNil(), fmt.Errorf("array.minmax values are not comparable")
				}
				if cmpMin < 0 {
					minVal = item
				}
				cmpMax, err := arraySortCompareValues(item, maxVal)
				if err != nil {
					return NewNil(), fmt.Errorf("array.minmax values are not comparable")
				}
				if cmpMax > 0 {
					maxVal = item
				}
			}
			return NewArray([]Value{minVal, maxVal}), nil
		})
		return minmax, nil
	case "min_by":
		return arrayMemberMinMaxBy("array.min_by", false), nil
	case "max_by":
		return arrayMemberMinMaxBy("array.max_by", true), nil
	default:
		return NewNil(), fmt.Errorf("unknown array method %s", property)
	}
}

// arrayMemberMinMax builds the array.min / array.max builtin. When wantMax is
// true it selects the maximum; otherwise the minimum. Ties resolve to the first
// element encountered, matching Ruby's Enumerable#min/#max.
func arrayMemberMinMax(name string, wantMax bool) Value {
	return NewAutoBuiltin(name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(args) > 0 {
			return NewNil(), fmt.Errorf("%s does not take arguments", name)
		}
		if valueBlock(block) != nil {
			return NewNil(), fmt.Errorf("%s does not accept a block; use min_by or max_by for block-based selection", name)
		}
		arr := receiver.Array()
		if len(arr) == 0 {
			return NewNil(), nil
		}
		best := arr[0]
		for _, item := range arr[1:] {
			cmp, err := arraySortCompareValues(item, best)
			if err != nil {
				return NewNil(), fmt.Errorf("%s values are not comparable", name)
			}
			if (wantMax && cmp > 0) || (!wantMax && cmp < 0) {
				best = item
			}
		}
		return best, nil
	})
}

// arrayMemberMinMaxBy builds the array.min_by / array.max_by builtin. The block
// derives a comparison key for each element using the same ordering as
// array.sort_by. Ties resolve to the first element encountered, matching Ruby's
// Enumerable#min_by/#max_by.
func arrayMemberMinMaxBy(name string, wantMax bool) Value {
	return NewAutoBuiltin(name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(args) > 0 {
			return NewNil(), fmt.Errorf("%s does not take arguments", name)
		}
		runner, err := newBlockCallRunner(exec, block, name)
		if err != nil {
			return NewNil(), err
		}
		arr := receiver.Array()
		if len(arr) == 0 {
			return NewNil(), nil
		}
		var blockArg [1]Value
		blockArg[0] = arr[0]
		bestKey, err := runner.call(blockArg[:])
		if err != nil {
			return NewNil(), err
		}
		best := arr[0]
		for _, item := range arr[1:] {
			blockArg[0] = item
			key, err := runner.call(blockArg[:])
			if err != nil {
				return NewNil(), err
			}
			cmp, err := arraySortCompareValues(key, bestKey)
			if err != nil {
				return NewNil(), fmt.Errorf("%s block values are not comparable", name)
			}
			if (wantMax && cmp > 0) || (!wantMax && cmp < 0) {
				best = item
				bestKey = key
			}
		}
		return best, nil
	})
}

func arrayGroupingInitialCapacity(length int) int {
	if length <= 16 {
		return length
	}
	return 16
}

func arrayPartitionInitialCapacity(length int) int {
	if length <= 1 {
		return length
	}
	return (length + 1) / 2
}

func arrayTallyInitialCapacity(arr []Value, hasBlock bool) (int, error) {
	length := len(arr)
	if length <= 256 {
		return length, nil
	}
	if hasBlock {
		return 256, nil
	}

	// Sample direct values only; blocks may be expensive or effectful, so
	// block tallies use the conservative fixed cap above.
	const sampleLimit = 64
	var keys [sampleLimit]string
	distinct := 0
	for _, item := range arr[:sampleLimit] {
		key, err := valueToHashKey(item)
		if err != nil {
			return 0, err
		}
		found := false
		for i := range distinct {
			if keys[i] == key {
				found = true
				break
			}
		}
		if !found {
			keys[distinct] = key
			distinct++
		}
	}
	if distinct == sampleLimit {
		return length, nil
	}
	if distinct <= 8 {
		return 16, nil
	}
	return 256, nil
}

// arrayPositiveSliceSize validates the single argument shared by each_slice as a
// positive native-int size. Ruby raises "invalid slice size" for non-positive
// values, and an out-of-range integer cannot index a Go slice, so both are
// rejected before iteration begins.
func arrayPositiveSliceSize(args []Value, method string) (int, error) {
	if len(args) != 1 {
		return 0, fmt.Errorf("%s expects a slice size", method)
	}
	sizeValue := args[0]
	maxNativeInt := int64(^uint(0) >> 1)
	if sizeValue.Kind() != KindInt || sizeValue.Int() <= 0 || sizeValue.Int() > maxNativeInt {
		return 0, fmt.Errorf("%s invalid slice size", method)
	}
	return int(sizeValue.Int()), nil
}

// arrayPositiveConsSize validates the single argument shared by each_cons as a
// positive native-int size. Ruby raises "invalid size" for non-positive values.
func arrayPositiveConsSize(args []Value, method string) (int, error) {
	if len(args) != 1 {
		return 0, fmt.Errorf("%s expects a window size", method)
	}
	sizeValue := args[0]
	maxNativeInt := int64(^uint(0) >> 1)
	if sizeValue.Kind() != KindInt || sizeValue.Int() <= 0 || sizeValue.Int() > maxNativeInt {
		return 0, fmt.Errorf("%s invalid size", method)
	}
	return int(sizeValue.Int()), nil
}

// arrayArgsToSlices validates that every argument is an array and returns their
// element slices. It backs the variadic set helpers (union, difference), which
// in Ruby raise TypeError when handed a non-array argument and accept no
// keyword arguments.
func arrayArgsToSlices(method string, args []Value, kwargs map[string]Value) ([][]Value, error) {
	if len(kwargs) > 0 {
		return nil, fmt.Errorf("%s does not take keyword arguments", method)
	}
	others := make([][]Value, len(args))
	for i, arg := range args {
		if arg.Kind() != KindArray {
			return nil, fmt.Errorf("%s arguments must be arrays", method)
		}
		others[i] = arg.Array()
	}
	return others, nil
}

// arrayCycleCount validates the optional repetition count for cycle. With no
// argument, or an explicit nil count, the loop is infinite (infinite=true),
// mirroring Ruby's Array#cycle where cycle and cycle(nil) both repeat forever.
// A negative count yields no iterations rather than an error, matching Ruby.
func arrayCycleCount(args []Value, method string) (count int, infinite bool, err error) {
	if len(args) == 0 {
		return 0, true, nil
	}
	if len(args) != 1 {
		return 0, false, fmt.Errorf("%s accepts at most one count", method)
	}
	countValue := args[0]
	if countValue.Kind() == KindNil {
		return 0, true, nil
	}
	if countValue.Kind() != KindInt {
		return 0, false, fmt.Errorf("%s count must be an integer", method)
	}
	if countValue.Int() <= 0 {
		return 0, false, nil
	}
	maxNativeInt := int64(^uint(0) >> 1)
	if countValue.Int() > maxNativeInt {
		return 0, false, fmt.Errorf("%s count is out of range", method)
	}
	return int(countValue.Int()), false, nil
}

func arrayMemberQuery(property string) (Value, error) {
	switch property {
	case "size", "length":
		name := property
		return NewAutoBuiltin("array."+name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.%s does not take arguments", name)
			}
			return NewInt(int64(len(receiver.Array()))), nil
		}), nil
	case "empty?":
		return NewAutoBuiltin("array.empty?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.empty? does not take arguments")
			}
			return NewBool(len(receiver.Array()) == 0), nil
		}), nil
	case "each":
		return NewAutoBuiltin("array.each", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			runner, err := newBlockCallRunner(exec, block, "array.each")
			if err != nil {
				return NewNil(), err
			}
			var blockArg [1]Value
			for _, item := range receiver.Array() {
				blockArg[0] = item
				if _, err := runner.call(blockArg[:]); err != nil {
					return NewNil(), err
				}
			}
			return receiver, nil
		}), nil
	case "each_slice":
		return NewAutoBuiltin("array.each_slice", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			size, err := arrayPositiveSliceSize(args, "array.each_slice")
			if err != nil {
				return NewNil(), err
			}
			runner, err := newBlockCallRunner(exec, block, "array.each_slice")
			if err != nil {
				return NewNil(), err
			}
			arr := receiver.Array()
			var blockArg [1]Value
			for i := 0; i < len(arr); i += size {
				end := min(i+size, len(arr))
				slice := make([]Value, end-i)
				copy(slice, arr[i:end])
				blockArg[0] = NewArray(slice)
				if _, err := runner.call(blockArg[:]); err != nil {
					return NewNil(), err
				}
			}
			return NewNil(), nil
		}), nil
	case "each_cons":
		return NewAutoBuiltin("array.each_cons", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			size, err := arrayPositiveConsSize(args, "array.each_cons")
			if err != nil {
				return NewNil(), err
			}
			runner, err := newBlockCallRunner(exec, block, "array.each_cons")
			if err != nil {
				return NewNil(), err
			}
			arr := receiver.Array()
			var blockArg [1]Value
			for i := 0; i+size <= len(arr); i++ {
				window := make([]Value, size)
				copy(window, arr[i:i+size])
				blockArg[0] = NewArray(window)
				if _, err := runner.call(blockArg[:]); err != nil {
					return NewNil(), err
				}
			}
			return NewNil(), nil
		}), nil
	case "reverse_each":
		return NewAutoBuiltin("array.reverse_each", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.reverse_each does not take arguments")
			}
			runner, err := newBlockCallRunner(exec, block, "array.reverse_each")
			if err != nil {
				return NewNil(), err
			}
			arr := receiver.Array()
			var blockArg [1]Value
			for i := len(arr) - 1; i >= 0; i-- {
				blockArg[0] = arr[i]
				if _, err := runner.call(blockArg[:]); err != nil {
					return NewNil(), err
				}
			}
			return receiver, nil
		}), nil
	case "cycle":
		return NewAutoBuiltin("array.cycle", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			// A nil count cycles forever, mirroring Ruby's Array#cycle. The step
			// quota and context cancellation bound the otherwise unbounded loop.
			count, infinite, err := arrayCycleCount(args, "array.cycle")
			if err != nil {
				return NewNil(), err
			}
			runner, err := newBlockCallRunner(exec, block, "array.cycle")
			if err != nil {
				return NewNil(), err
			}
			arr := receiver.Array()
			if len(arr) == 0 {
				return NewNil(), nil
			}
			var blockArg [1]Value
			for rep := 0; infinite || rep < count; rep++ {
				for _, item := range arr {
					// Charge a step per yield so an empty or trivial block body
					// cannot starve the quota or cancellation checks during a
					// long (or infinite) cycle.
					if err := exec.step(); err != nil {
						return NewNil(), err
					}
					blockArg[0] = item
					if _, err := runner.call(blockArg[:]); err != nil {
						return NewNil(), err
					}
				}
			}
			return NewNil(), nil
		}), nil
	case "map":
		return NewAutoBuiltin("array.map", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			runner, err := newBlockCallRunner(exec, block, "array.map")
			if err != nil {
				return NewNil(), err
			}
			arr := receiver.Array()
			result := make([]Value, len(arr))
			var blockArg [1]Value
			for i, item := range arr {
				blockArg[0] = item
				val, err := runner.call(blockArg[:])
				if err != nil {
					return NewNil(), err
				}
				result[i] = val
			}
			return NewArray(result), nil
		}), nil
	case "select":
		return NewAutoBuiltin("array.select", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			runner, err := newBlockCallRunner(exec, block, "array.select")
			if err != nil {
				return NewNil(), err
			}
			arr := receiver.Array()
			out := make([]Value, 0, len(arr))
			var blockArg [1]Value
			for _, item := range arr {
				blockArg[0] = item
				val, err := runner.call(blockArg[:])
				if err != nil {
					return NewNil(), err
				}
				if val.Truthy() {
					out = append(out, item)
				}
			}
			return NewArray(out), nil
		}), nil
	case "reject":
		return NewAutoBuiltin("array.reject", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.reject does not take arguments")
			}
			runner, err := newBlockCallRunner(exec, block, "array.reject")
			if err != nil {
				return NewNil(), err
			}
			arr := receiver.Array()
			out := make([]Value, 0, len(arr))
			var blockArg [1]Value
			for _, item := range arr {
				blockArg[0] = item
				val, err := runner.call(blockArg[:])
				if err != nil {
					return NewNil(), err
				}
				if !val.Truthy() {
					out = append(out, item)
				}
			}
			// A sparse result should not retain a backing array sized to the
			// whole receiver, so right-size the result.
			if len(out) < cap(out) {
				trimmed := make([]Value, len(out))
				copy(trimmed, out)
				out = trimmed
			}
			return NewArray(out), nil
		}), nil
	case "take_while":
		return NewAutoBuiltin("array.take_while", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.take_while does not take arguments")
			}
			runner, err := newBlockCallRunner(exec, block, "array.take_while")
			if err != nil {
				return NewNil(), err
			}
			arr := receiver.Array()
			out := make([]Value, 0, len(arr))
			var blockArg [1]Value
			for _, item := range arr {
				blockArg[0] = item
				val, err := runner.call(blockArg[:])
				if err != nil {
					return NewNil(), err
				}
				if !val.Truthy() {
					break
				}
				out = append(out, item)
			}
			// A short prefix should not retain a backing array sized to the
			// whole receiver, so right-size the result after an early stop.
			if len(out) < cap(out) {
				trimmed := make([]Value, len(out))
				copy(trimmed, out)
				out = trimmed
			}
			return NewArray(out), nil
		}), nil
	case "drop_while":
		return NewAutoBuiltin("array.drop_while", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.drop_while does not take arguments")
			}
			runner, err := newBlockCallRunner(exec, block, "array.drop_while")
			if err != nil {
				return NewNil(), err
			}
			arr := receiver.Array()
			start := len(arr)
			var blockArg [1]Value
			for idx, item := range arr {
				blockArg[0] = item
				val, err := runner.call(blockArg[:])
				if err != nil {
					return NewNil(), err
				}
				if !val.Truthy() {
					start = idx
					break
				}
			}
			out := make([]Value, len(arr)-start)
			copy(out, arr[start:])
			return NewArray(out), nil
		}), nil
	case "grep", "grep_v":
		return arrayMemberGrep(property)
	case "find":
		return NewAutoBuiltin("array.find", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.find does not take arguments")
			}
			runner, err := newBlockCallRunner(exec, block, "array.find")
			if err != nil {
				return NewNil(), err
			}
			var blockArg [1]Value
			for _, item := range receiver.Array() {
				blockArg[0] = item
				match, err := runner.call(blockArg[:])
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
			runner, err := newBlockCallRunner(exec, block, "array.find_index")
			if err != nil {
				return NewNil(), err
			}
			var blockArg [1]Value
			for idx, item := range receiver.Array() {
				blockArg[0] = item
				match, err := runner.call(blockArg[:])
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
			runner, err := newBlockCallRunner(exec, block, "array.reduce")
			if err != nil {
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
			var blockArgs [2]Value
			for i := start; i < len(arr); i++ {
				blockArgs[0] = acc
				blockArgs[1] = arr[i]
				next, err := runner.call(blockArgs[:])
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
	case "fetch":
		return NewAutoBuiltin("array.fetch", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) < 1 || len(args) > 2 {
				return NewNil(), fmt.Errorf("array.fetch expects index and optional default")
			}
			index, err := valueToInt(args[0])
			if err != nil {
				return NewNil(), fmt.Errorf("array.fetch index must be integer")
			}
			if args[0].Kind() == KindFloat && math.Trunc(args[0].Float()) != args[0].Float() {
				return NewNil(), fmt.Errorf("array.fetch index must be integer")
			}
			arr := receiver.Array()
			if index >= 0 && index < len(arr) {
				return arr[index], nil
			}
			if len(args) == 2 {
				return args[1], nil
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
				if valueBlock(block) != nil {
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
			if valueBlock(block) == nil {
				return NewInt(int64(len(arr))), nil
			}
			runner, err := newBlockCallRunner(exec, block, "array.count")
			if err != nil {
				return NewNil(), err
			}
			total := int64(0)
			var blockArg [1]Value
			for _, item := range arr {
				blockArg[0] = item
				include, err := runner.call(blockArg[:])
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
			var runner *blockCallRunner
			if valueBlock(block) != nil {
				var err error
				runner, err = newBlockCallRunner(exec, block, "array.any?")
				if err != nil {
					return NewNil(), err
				}
			}
			var blockArg [1]Value
			for _, item := range receiver.Array() {
				if runner != nil {
					blockArg[0] = item
					val, err := runner.call(blockArg[:])
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
			var runner *blockCallRunner
			if valueBlock(block) != nil {
				var err error
				runner, err = newBlockCallRunner(exec, block, "array.all?")
				if err != nil {
					return NewNil(), err
				}
			}
			var blockArg [1]Value
			for _, item := range receiver.Array() {
				if runner != nil {
					blockArg[0] = item
					val, err := runner.call(blockArg[:])
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
			var runner *blockCallRunner
			if valueBlock(block) != nil {
				var err error
				runner, err = newBlockCallRunner(exec, block, "array.none?")
				if err != nil {
					return NewNil(), err
				}
			}
			var blockArg [1]Value
			for _, item := range receiver.Array() {
				if runner != nil {
					blockArg[0] = item
					val, err := runner.call(blockArg[:])
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

// arrayMemberGrep builds array.grep and array.grep_v. Both select elements
// against a pattern using Ruby's case-equality direction (pattern === element),
// reusing the same matcher that powers case/when clauses. grep keeps matching
// elements; grep_v keeps the non-matching ones. An optional block transforms
// each kept element, mirroring Ruby's Enumerable#grep.
func arrayMemberGrep(property string) (Value, error) {
	keep := property == "grep"
	name := "array." + property
	return NewAutoBuiltin(name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(args) != 1 {
			return NewNil(), fmt.Errorf("%s expects exactly one pattern argument", name)
		}
		pattern := args[0]
		var runner *blockCallRunner
		if valueBlock(block) != nil {
			var err error
			runner, err = newBlockCallRunner(exec, block, name)
			if err != nil {
				return NewNil(), err
			}
		}
		arr := receiver.Array()
		out := make([]Value, 0, len(arr))
		var blockArg [1]Value
		for _, item := range arr {
			if caseCandidateMatches(item, pattern) != keep {
				continue
			}
			if runner == nil {
				out = append(out, item)
				continue
			}
			blockArg[0] = item
			transformed, err := runner.call(blockArg[:])
			if err != nil {
				return NewNil(), err
			}
			out = append(out, transformed)
		}
		// A sparse match set should not retain a backing array sized to the
		// whole receiver, so right-size the result.
		if len(out) < cap(out) {
			trimmed := make([]Value, len(out))
			copy(trimmed, out)
			out = trimmed
		}
		return NewArray(out), nil
	}), nil
}

// arrayFillSpan describes the half-open destination window [begin, end) that a
// fill writes to, together with finalLength, the length the result array needs
// so the window fits. When the window extends past the receiver, finalLength is
// larger than the receiver and the gap before begin is padded with nil, matching
// Ruby's Array#fill growth behavior.
type arrayFillSpan struct {
	begin       int
	end         int
	finalLength int
}

// arrayFill implements Ruby's Array#fill, returning a new array rather than
// mutating the receiver, consistent with the immutable collection helpers
// alongside it (push, pop, compact, flatten). It accepts the value and block
// forms:
//
//	fill(value)                fill(start) { |i| ... }
//	fill(value, start)         fill(start, length) { |i| ... }
//	fill(value, start, length) fill(range) { |i| ... }
//	fill(value, range)         fill { |i| ... }
//
// The value and block forms are mutually exclusive: supplying both is rejected,
// matching Ruby, which never consults a block when an explicit fill value is
// given.
func arrayFill(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("array.fill does not take keyword arguments")
	}
	arr := receiver.Array()
	hasBlock := valueBlock(block) != nil

	// selectors are the positional arguments that choose the fill window. In the
	// value form the first argument is the fill value, so the selectors follow
	// it; in the block form every argument is a selector.
	var selectors []Value
	if hasBlock {
		selectors = args
	} else {
		if len(args) == 0 {
			return NewNil(), fmt.Errorf("array.fill requires a value or a block")
		}
		selectors = args[1:]
	}

	span, err := arrayFillResolveSpan(selectors, len(arr))
	if err != nil {
		return NewNil(), err
	}

	// Reject an oversized result up front so a window far past the receiver
	// cannot reserve a huge backing array before the per-element checks below
	// observe it, mirroring the range materialization guard.
	if err := exec.checkProjectedIntArrayBytes(span.finalLength); err != nil {
		return NewNil(), err
	}

	out := make([]Value, span.finalLength)
	copy(out, arr)
	// Pad the gap between the receiver's end and the fill window with nil, the
	// same value Ruby inserts when fill grows the array past its old length. The
	// gap only exists when the window actually extends the array, so it is
	// bounded by the result length to skip an empty window whose start sits past
	// the receiver without growing it.
	for i := len(arr); i < span.begin && i < span.finalLength; i++ {
		out[i] = NewNil()
	}

	if hasBlock {
		runner, err := newBlockCallRunner(exec, block, "array.fill")
		if err != nil {
			return NewNil(), err
		}
		var blockArg [1]Value
		for i := span.begin; i < span.end; i++ {
			// Charge a step per produced element so a large window cannot starve
			// the quota or cancellation checks while the block runs.
			if err := exec.step(); err != nil {
				return NewNil(), err
			}
			blockArg[0] = NewInt(int64(i))
			val, err := runner.call(blockArg[:])
			if err != nil {
				return NewNil(), err
			}
			out[i] = val
		}
		return NewArray(out), nil
	}

	value := args[0]
	for i := span.begin; i < span.end; i++ {
		if err := exec.step(); err != nil {
			return NewNil(), err
		}
		out[i] = value
	}
	return NewArray(out), nil
}

// arrayFillResolveSpan parses the window selectors shared by both fill forms and
// returns the destination span. length is the receiver's current length. It
// accepts an empty selector list (whole array), an integer start with optional
// length, or a single range, matching Ruby's Array#fill.
func arrayFillResolveSpan(selectors []Value, length int) (arrayFillSpan, error) {
	switch len(selectors) {
	case 0:
		return arrayFillSpan{begin: 0, end: length, finalLength: length}, nil
	case 1:
		if selectors[0].Kind() == KindRange {
			return arrayFillRangeSpan(selectors[0].Range(), length)
		}
		begin, err := arrayFillStartIndex(selectors[0], length)
		if err != nil {
			return arrayFillSpan{}, err
		}
		return arrayFillSpanFromStart(begin, length, length-begin)
	case 2:
		if selectors[0].Kind() == KindRange {
			return arrayFillSpan{}, fmt.Errorf("array.fill does not accept a length with a range")
		}
		begin, err := arrayFillStartIndex(selectors[0], length)
		if err != nil {
			return arrayFillSpan{}, err
		}
		count, err := arrayFillLength(selectors[1])
		if err != nil {
			return arrayFillSpan{}, err
		}
		return arrayFillSpanFromStart(begin, length, count)
	default:
		return arrayFillSpan{}, fmt.Errorf("array.fill accepts at most a start and length")
	}
}

// arrayFillStartIndex resolves a start argument to a non-negative index.
// Fractional floats truncate toward zero like Ruby's to_int. A negative start
// counts back from the end like Ruby; a start more negative than the receiver
// length clamps to 0 (Ruby's Array#fill does not raise for an out-of-range
// negative integer start, unlike a range bound).
func arrayFillStartIndex(value Value, length int) (int, error) {
	start, err := valueToInt(value)
	if err != nil {
		return 0, fmt.Errorf("array.fill start must be integer")
	}
	if start < 0 {
		start += length
		if start < 0 {
			start = 0
		}
	}
	return start, nil
}

// arrayFillLength resolves an explicit length argument to a count, truncating
// fractional floats toward zero like Ruby's to_int. The sign is preserved: a
// negative count signals an empty window that leaves the array untouched, while
// a zero count still grows the array up to the start (see
// arrayFillSpanFromStart).
func arrayFillLength(value Value) (int, error) {
	count, err := valueToInt(value)
	if err != nil {
		return 0, fmt.Errorf("array.fill length must be integer")
	}
	return count, nil
}

// arrayFillSpanFromStart builds a span for the integer start/length forms. A
// negative count yields an empty window that never grows the array, matching
// Ruby's no-op for fill(value, start, -n) and for a bare start past the end
// (whose computed count is length-begin < 0). A zero or positive count grows
// finalLength up to begin+count, so an explicit zero length whose start sits
// past the receiver still pads the gap with nil up to the start, exactly as
// Ruby's Array#fill does ([1,2,3].fill(0, 5, 0) => [1,2,3,nil,nil]).
func arrayFillSpanFromStart(begin, length, count int) (arrayFillSpan, error) {
	if count < 0 {
		return arrayFillSpan{begin: begin, end: begin, finalLength: length}, nil
	}
	end := begin + count
	if end < begin {
		// begin + count overflowed int; such a window cannot be materialized.
		return arrayFillSpan{}, fmt.Errorf("array.fill window is too large")
	}
	finalLength := length
	if end > finalLength {
		finalLength = end
	}
	return arrayFillSpan{begin: begin, end: end, finalLength: finalLength}, nil
}

// arrayFillRangeSpan resolves a range selector to a span. Negative bounds count
// back from the end; a bound more negative than the receiver length is rejected
// with an out-of-range error, matching Ruby's Array#fill (which raises a
// RangeError for such ranges rather than clamping as it does for integer
// starts). The window grows the result when the range end extends past the
// receiver. Bound arithmetic stays in int64 and an exclusive end beyond the
// native int range is rejected so a near-MaxInt64 inclusive range cannot
// silently overflow into a no-op.
func arrayFillRangeSpan(rng Range, length int) (arrayFillSpan, error) {
	length64 := int64(length)
	begin := rng.Start
	if begin < 0 {
		begin += length64
		if begin < 0 {
			return arrayFillSpan{}, fmt.Errorf("array.fill range %s out of range", NewRange(rng).String())
		}
	}
	end := rng.End
	if end < 0 {
		end += length64
	}
	if !rng.Exclusive {
		// An inclusive range's exclusive end is one past End; guard the increment
		// so End == math.MaxInt64 reports the oversized window rather than wrapping.
		if end == math.MaxInt64 {
			return arrayFillSpan{}, fmt.Errorf("array.fill window is too large")
		}
		end++
	}
	if end < begin {
		end = begin
	}
	if begin > math.MaxInt || end > math.MaxInt {
		return arrayFillSpan{}, fmt.Errorf("array.fill window is too large")
	}
	finalLength := length
	if int(end) > finalLength {
		finalLength = int(end)
	}
	return arrayFillSpan{begin: int(begin), end: int(end), finalLength: finalLength}, nil
}

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
			return NewArray(uniqueValues(arr)), nil
		}), nil
	case "union":
		return NewAutoBuiltin("array.union", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			others, err := arrayArgsToSlices("array.union", args, kwargs)
			if err != nil {
				return NewNil(), err
			}
			return NewArray(unionArrayValues(receiver.Array(), others)), nil
		}), nil
	case "difference":
		return NewAutoBuiltin("array.difference", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			others, err := arrayArgsToSlices("array.difference", args, kwargs)
			if err != nil {
				return NewNil(), err
			}
			return NewArray(differenceArrayValues(receiver.Array(), others)), nil
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
			out, err := flattenValues(arr, depth, "array.flatten")
			if err != nil {
				return NewNil(), err
			}
			return NewArray(out), nil
		}), nil
	case "fill":
		return NewAutoBuiltin("array.fill", arrayFill), nil
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
				end := min(i+size, len(arr))
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
			for i := range len(arr) - size + 1 {
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
	case "take":
		return NewAutoBuiltin("array.take", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("array.take expects exactly one count")
			}
			n, err := valueToCount(args[0])
			if err != nil {
				if errors.Is(err, errNegativeCount) {
					return NewNil(), fmt.Errorf("array.take attempted with negative size")
				}
				return NewNil(), fmt.Errorf("array.take count must be integer")
			}
			arr := receiver.Array()
			if n > len(arr) {
				n = len(arr)
			}
			out := make([]Value, n)
			copy(out, arr[:n])
			return NewArray(out), nil
		}), nil
	case "drop":
		return NewAutoBuiltin("array.drop", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("array.drop expects exactly one count")
			}
			n, err := valueToCount(args[0])
			if err != nil {
				if errors.Is(err, errNegativeCount) {
					return NewNil(), fmt.Errorf("array.drop attempted with negative size")
				}
				return NewNil(), fmt.Errorf("array.drop count must be integer")
			}
			arr := receiver.Array()
			if n > len(arr) {
				n = len(arr)
			}
			out := make([]Value, len(arr)-n)
			copy(out, arr[n:])
			return NewArray(out), nil
		}), nil
	case "zip":
		return NewAutoBuiltin("array.zip", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			others := make([][]Value, len(args))
			for i, arg := range args {
				if arg.Kind() != KindArray {
					return NewNil(), fmt.Errorf("array.zip arguments must be arrays")
				}
				others[i] = arg.Array()
			}
			arr := receiver.Array()
			rows := make([]Value, len(arr))
			for i := range arr {
				row := make([]Value, len(args)+1)
				row[0] = arr[i]
				for j, other := range others {
					if i < len(other) {
						row[j+1] = other[i]
					} else {
						row[j+1] = NewNil()
					}
				}
				rows[i] = NewArray(row)
			}
			return NewArray(rows), nil
		}), nil
	case "transpose":
		return NewAutoBuiltin("array.transpose", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 || len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("array.transpose does not take arguments")
			}
			rows := receiver.Array()
			if len(rows) == 0 {
				return NewArray([]Value{}), nil
			}
			// The first row defines the expected column count; every later row
			// must be an array of the same length, mirroring Ruby's IndexError
			// on ragged input.
			columnCount := -1
			for i, row := range rows {
				if row.Kind() != KindArray {
					return NewNil(), fmt.Errorf("array.transpose requires arrays as elements, but element at index %d is a %s", i, row.Kind())
				}
				got := len(row.Array())
				if columnCount == -1 {
					columnCount = got
					continue
				}
				if got != columnCount {
					return NewNil(), fmt.Errorf("array.transpose requires equal-length rows, but element at index %d has length %d (expected %d)", i, got, columnCount)
				}
			}
			columns := make([]Value, columnCount)
			for col := range columnCount {
				transposed := make([]Value, len(rows))
				for rowIndex, row := range rows {
					transposed[rowIndex] = row.Array()[col]
				}
				columns[col] = NewArray(transposed)
			}
			return NewArray(columns), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown array method %s", property)
	}
}

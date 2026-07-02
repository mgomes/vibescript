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
	"size", "length", "empty?", "each", "each_with_index", "each_slice", "each_cons", "reverse_each", "cycle", "map", "map_with_index", "filter_map", "select", "reject", "find", "find_index", "reduce", "include?", "index", "rindex", "at", "slice", "fetch", "values_at", "dig", "count", "any?", "all?", "none?", "one?",
	"take_while", "drop_while", "grep", "grep_v", "slice_when", "chunk_while",
	"push", "append", "prepend", "unshift", "pop", "shift", "delete", "insert", "clear", "delete_if", "keep_if", "uniq", "uniq!", "first", "last", "sum", "compact", "compact!", "flatten", "fill", "chunk", "window", "join", "reverse", "reverse!", "to_h",
	"take", "drop", "zip", "transpose", "union", "difference",
	"sample", "shuffle", "rotate", "product", "combination", "permutation", "repeated_combination", "repeated_permutation",
	"sort", "sort!", "sort_by", "partition", "group_by", "group_by_stable", "tally",
	"map!", "select!", "reject!",
	"min", "max", "minmax", "min_by", "max_by",
	"inspect",
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
	case "size", "length", "empty?", "each", "each_with_index", "each_slice", "each_cons", "reverse_each", "cycle", "map", "map_with_index", "filter_map", "select", "reject", "find", "find_index", "reduce", "include?", "index", "rindex", "at", "slice", "fetch", "values_at", "dig", "count", "any?", "all?", "none?", "one?",
		"take_while", "drop_while", "grep", "grep_v", "slice_when", "chunk_while":
		return arrayMemberQuery(property)
	case "push", "append", "prepend", "unshift", "pop", "shift", "delete", "insert", "clear", "delete_if", "keep_if", "uniq", "uniq!", "first", "last", "sum", "compact", "compact!", "flatten", "fill", "chunk", "window", "join", "reverse", "reverse!", "to_h", "take", "drop", "zip", "transpose", "union", "difference",
		"sample", "shuffle", "rotate", "product", "combination", "permutation", "repeated_combination", "repeated_permutation", "sort!", "map!", "select!", "reject!":
		return arrayMemberTransforms(property)
	case "sort", "sort_by", "partition", "group_by", "group_by_stable", "tally":
		return arrayMemberGrouping(property)
	case "min", "max", "minmax", "min_by", "max_by":
		return arrayMemberExtrema(property)
	case "inspect":
		return newInspectBuiltin("array"), nil
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
				runner, err = newBlockCallRunner(exec, block, "array.sort", receiver, nil, kwargs)
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
			runner, err := newBlockCallRunner(exec, block, "array.sort_by", receiver, nil, kwargs)
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
			runner, err := newBlockCallRunner(exec, block, "array.partition", receiver, nil, kwargs)
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
			runner, err := newBlockCallRunner(exec, block, "array.group_by", receiver, nil, kwargs)
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
			runner, err := newBlockCallRunner(exec, block, "array.group_by_stable", receiver, nil, kwargs)
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
				runner, err = newBlockCallRunner(exec, block, "array.tally", receiver, nil, kwargs)
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
		runner, err := newBlockCallRunner(exec, block, name, receiver, nil, kwargs)
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

// filterMapInitialCap is the modest capacity filter_map reserves up front. The
// kept count can range from zero (every block result falsy) to the full
// receiver length, so unlike map there is no useful size hint. The local result
// slice is not reachable from the execution's memory roots while it is built, so
// reserving len(receiver) would let a sparse result allocate and then drop large
// transient backing storage that the post-call memory check never charges.
// Seeding a small fixed capacity and letting append grow keeps the peak backing
// allocation proportional to the elements actually kept (at most append's
// doubling factor) rather than to the receiver length.
const filterMapInitialCap = 16

// boundedFilterCap caps a desired capacity at filterMapInitialCap so the
// up-front reservation never scales with the receiver length.
func boundedFilterCap(n int) int {
	if n > filterMapInitialCap {
		return filterMapInitialCap
	}
	return n
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
			runner, err := newBlockCallRunner(exec, block, "array.each", receiver, nil, kwargs)
			if err != nil {
				return NewNil(), err
			}
			var blockArg [1]Value
			for _, item := range receiver.Array() {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				blockArg[0] = item
				if _, err := runner.call(blockArg[:]); err != nil {
					return NewNil(), err
				}
				if err := exec.checkContext(); err != nil {
					return NewNil(), err
				}
			}
			return receiver, nil
		}), nil
	case "each_with_index":
		return NewAutoBuiltin("array.each_with_index", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.each_with_index does not take arguments")
			}
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("array.each_with_index does not take keyword arguments")
			}
			runner, err := newBlockCallRunner(exec, block, "array.each_with_index", receiver, nil, kwargs)
			if err != nil {
				return NewNil(), err
			}
			var blockArgs [2]Value
			for i, item := range receiver.Array() {
				// Charge a step per yield so an empty block body cannot starve
				// the step quota or cancellation checks while traversing a large
				// receiver; runner.call only charges steps for the statements it
				// evaluates, and an empty block evaluates none.
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				blockArgs[0] = item
				blockArgs[1] = NewInt(int64(i))
				if _, err := runner.call(blockArgs[:]); err != nil {
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
			runner, err := newBlockCallRunner(exec, block, "array.each_slice", receiver, nil, kwargs)
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
			runner, err := newBlockCallRunner(exec, block, "array.each_cons", receiver, nil, kwargs)
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
			runner, err := newBlockCallRunner(exec, block, "array.reverse_each", receiver, nil, kwargs)
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
			runner, err := newBlockCallRunner(exec, block, "array.cycle", receiver, nil, kwargs)
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
			runner, err := newBlockCallRunner(exec, block, "array.map", receiver, nil, kwargs)
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
	case "map_with_index":
		return NewAutoBuiltin("array.map_with_index", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.map_with_index does not take arguments")
			}
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("array.map_with_index does not take keyword arguments")
			}
			runner, err := newBlockCallRunner(exec, block, "array.map_with_index", receiver, nil, kwargs)
			if err != nil {
				return NewNil(), err
			}
			arr := receiver.Array()
			// map_with_index keeps an arbitrary block result per element, so charge
			// the growing result incrementally rather than only after the call: a
			// block returning an individually quota-sized value per element could
			// otherwise pile up past MemoryQuotaBytes before the post-call check
			// ran. The accumulator's baseline includes the live receiver and block
			// (held on the Go stack during the call, so invisible to the per-call
			// checkMemoryWith), matching hash.map_with_index and filter_map.
			acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
			// Reject the build before reserving the backing slice when its len(arr)
			// slots would already overflow the quota. map keeps one result per
			// element, so the backing reaches len(arr) Value slots regardless of the
			// block; make would otherwise reserve all of them as a Go-local slice
			// (invisible to the quota) before the first acc.add projected cap(out),
			// letting a large receiver transiently allocate a full result backing
			// that should have been rejected up front, mirroring rangeMaterialize's
			// pre-make reservation.
			if err := acc.reserveSlots(len(arr)); err != nil {
				return NewNil(), err
			}
			out := make([]Value, 0, len(arr))
			var blockArgs [2]Value
			for i, item := range arr {
				// Charge a step per yield so an empty block body cannot starve the
				// step quota or cancellation checks while traversing a large
				// receiver; runner.call only charges steps for the statements it
				// evaluates, and an empty block evaluates none.
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				blockArgs[0] = item
				blockArgs[1] = NewInt(int64(i))
				val, err := runner.call(blockArgs[:])
				if err != nil {
					return NewNil(), err
				}
				out = append(out, val)
				if err := acc.addConservative(val, cap(out)); err != nil {
					return NewNil(), err
				}
			}
			return NewArray(out), nil
		}), nil
	case "filter_map":
		return NewAutoBuiltin("array.filter_map", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.filter_map does not take arguments")
			}
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("array.filter_map does not take keyword arguments")
			}
			runner, err := newBlockCallRunner(exec, block, "array.filter_map", receiver, nil, kwargs)
			if err != nil {
				return NewNil(), err
			}
			arr := receiver.Array()
			// Only reserve a modest initial capacity and let append grow the
			// backing array as truthy results accumulate. Reserving len(arr) up
			// front would charge no quota (the local slice is not reachable from
			// exec's roots) yet could exceed MemoryQuotaBytes in transient
			// backing storage before the post-call check runs, and a sparse
			// result would trim that storage away before it could ever be
			// charged. Bounding the reservation keeps the peak allocation
			// proportional to the elements actually kept.
			out := make([]Value, 0, boundedFilterCap(len(arr)))
			// Charge the accumulating result against the quota on every kept
			// element. out is a local slice, so it is invisible to the step()
			// slow-path checkMemory() and to the post-call checkMemoryWith(result);
			// without this a block returning an individually quota-sized value per
			// element could pile up far beyond MemoryQuotaBytes before the
			// post-call check ever ran. The accumulator walks each kept element
			// once and keeps a running total, so the whole loop costs O(sum of
			// result sizes) rather than re-walking the entire accumulated array per
			// append. Unlike range materialization (whose elements are inlined ints
			// with no payload beyond their slot, letting it use the O(1) cap-based
			// projection), filter_map keeps arbitrary block results, so the
			// accumulator walks each element to account for string and collection
			// payloads. Its baseline includes the live receiver and block (held on
			// the Go stack during the call, so invisible to estimateMemoryUsageBase
			// but charged by the pre-call checkCallMemoryRoots), so a transform
			// whose receiver or captured block already nears the quota cannot
			// accumulate an unbounded result. It dedups shared backings exactly
			// like the post-call check, so it never rejects a result the post-call
			// check would have accepted.
			acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
			var blockArg [1]Value
			for _, item := range arr {
				// Charge a step per yield so an empty or trivial block body
				// cannot starve the step quota or cancellation checks while
				// traversing a large receiver; runner.call only charges steps
				// for the statements it evaluates, and an empty block evaluates
				// none.
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				blockArg[0] = item
				val, err := runner.call(blockArg[:])
				if err != nil {
					return NewNil(), err
				}
				// filter_map fuses map followed by a truthiness filter: keep each
				// truthy block result and drop falsy ones. This uses Vibescript's
				// Truthy model (matching select/reject/take_while), so 0, "", and
				// empty collections are dropped alongside nil and false.
				if val.Truthy() {
					out = append(out, val)
					if err := acc.addConservative(val, cap(out)); err != nil {
						return NewNil(), err
					}
				}
			}
			return NewArray(out), nil
		}), nil
	case "select":
		return NewAutoBuiltin("array.select", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			runner, err := newBlockCallRunner(exec, block, "array.select", receiver, nil, kwargs)
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
			runner, err := newBlockCallRunner(exec, block, "array.reject", receiver, nil, kwargs)
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
			runner, err := newBlockCallRunner(exec, block, "array.take_while", receiver, nil, kwargs)
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
			runner, err := newBlockCallRunner(exec, block, "array.drop_while", receiver, nil, kwargs)
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
	case "slice_when":
		return NewAutoBuiltin("array.slice_when", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return arrayAdjacentSlices(exec, receiver, args, kwargs, block, "array.slice_when", true)
		}), nil
	case "chunk_while":
		return NewAutoBuiltin("array.chunk_while", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return arrayAdjacentSlices(exec, receiver, args, kwargs, block, "array.chunk_while", false)
		}), nil
	case "find":
		return NewAutoBuiltin("array.find", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("array.find expects at most one fallback callable")
			}
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("array.find does not take keyword arguments")
			}
			runner, err := newBlockCallRunner(exec, block, "array.find", receiver, nil, kwargs)
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
			if len(args) == 1 && args[0].Kind() != KindNil {
				if err := exec.checkCallMemoryRootsWithCallee(args[0], receiver, nil, nil, NewNil()); err != nil {
					return NewNil(), err
				}
				result, err := exec.invokeCallable(args[0], NewNil(), nil, nil, NewNil(), Position{})
				if err != nil {
					return NewNil(), err
				}
				if err := exec.checkMemoryWith(receiver, result); err != nil {
					return NewNil(), err
				}
				return result, nil
			}
			return NewNil(), nil
		}), nil
	case "find_index":
		return NewAutoBuiltin("array.find_index", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return arrayForwardIndex(exec, receiver, args, block, "array.find_index")
		}), nil
	case "reduce":
		return NewAutoBuiltin("array.reduce", arrayReduce), nil
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
			return arrayForwardIndex(exec, receiver, args, block, "array.index")
		}), nil
	case "rindex":
		return NewAutoBuiltin("array.rindex", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return arrayReverseIndex(exec, receiver, args, block)
		}), nil
	case "at":
		return NewAutoBuiltin("array.at", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("array.at does not take keyword arguments")
			}
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("array.at expects exactly one index")
			}
			index, err := arraySliceIndex(args[0], "array.at")
			if err != nil {
				return NewNil(), err
			}
			return arrayElementAt(receiver.Array(), index), nil
		}), nil
	case "slice":
		return NewAutoBuiltin("array.slice", arraySlice), nil
	case "fetch":
		return NewAutoBuiltin("array.fetch", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			hasBlock := valueBlock(block) != nil
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
			normalized := index
			if normalized < 0 {
				normalized += len(arr)
			}
			if normalized >= 0 && normalized < len(arr) {
				return arr[normalized], nil
			}
			// A block supersedes a default value argument, matching Ruby's
			// Array#fetch: when both are supplied the block is invoked on a
			// miss and the default argument is ignored.
			if hasBlock {
				blockArg := [1]Value{NewInt(int64(index))}
				return exec.CallBlock(block, blockArg[:])
			}
			if len(args) == 2 {
				return args[1], nil
			}
			return NewNil(), fmt.Errorf("array.fetch index %d outside of array bounds: %d...%d", index, -len(arr), len(arr))
		}), nil
	case "values_at":
		return NewAutoBuiltin("array.values_at", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("array.values_at does not take keyword arguments")
			}
			arr := receiver.Array()

			// The result aliases the receiver's elements, so charge its growth
			// through an arrayBuildAccumulator whose baseline already includes the
			// live call roots (receiver, args, block). When values_at runs on an
			// ephemeral receiver — an array literal or capability result reachable
			// only through the call roots, invisible to estimateMemoryUsageBase — the
			// receiver's payload is already near the quota; without seeding it the
			// per-slot check would let the result backing grow another full quota on
			// top of it, with the excess only caught after materialization. The
			// accumulator dedups elements that alias the receiver, so each selected
			// slot adds only a Value slot while the receiver payload it points into
			// is counted once, in the baseline.
			acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)

			// Grow the result with append from a bounded initial capacity rather
			// than reserving one slot per argument up front. A single range
			// selector can expand to far more positions than there are arguments
			// (values_at(0..1_000_000_000)), so the per-element step() and projected
			// memory check below are what bound the actual allocation; bounding the
			// initial capacity keeps it proportional to what the quotas allow,
			// mirroring rangeMaterialize.
			initialCap := len(args)
			if initialCap > arrayValuesAtInitialCap {
				initialCap = arrayValuesAtInitialCap
			}
			// Reject the build before reserving the backing slice when that capacity
			// alone already overflows the quota. Charging it through the accumulator
			// uses the same call-root baseline emit charges each slot against, mirroring
			// rangeMaterialize's up-front checkProjectedIntArrayBytes. Without it a tight
			// MemoryQuotaBytes paired with many selectors could let make reserve up to
			// arrayValuesAtInitialCap Value slots transiently before the first emit
			// reported the overrun.
			if err := acc.reserveSlots(initialCap); err != nil {
				return NewNil(), err
			}
			out := make([]Value, 0, initialCap)

			// emit appends one selected element, charging a step and re-checking the
			// backing array's growth against the memory quota before the next slot.
			// Every position a range selector expands to (including nil pads past the
			// receiver) flows through here, so a huge padded window cannot
			// materialize without polling cancellation or the memory quota.
			emit := func(val Value) error {
				if err := exec.step(); err != nil {
					return err
				}
				out = append(out, val)
				return acc.add(val, cap(out))
			}

			for _, arg := range args {
				if arg.Kind() == KindRange {
					// Range selectors expand to their selected positions in place,
					// matching Ruby's Array#values_at(0..1) -> [a[0], a[1]] and the
					// mixed form values_at(0..1, -1).
					if err := arrayValuesAtRange(acc, len(out), arr, arg.Range(), emit); err != nil {
						return NewNil(), err
					}
					continue
				}
				// Floats truncate toward zero like Ruby's Array#values_at (1.5 -> 1,
				// -1.9 -> -1); non-numeric arguments are rejected.
				index, err := valueToInt(arg)
				if err != nil {
					return NewNil(), fmt.Errorf("array.values_at index must be integer")
				}
				if index < 0 {
					index += len(arr)
				}
				selected := NewNil()
				if index >= 0 && index < len(arr) {
					selected = arr[index]
				}
				if err := emit(selected); err != nil {
					return NewNil(), err
				}
			}
			return NewArray(out), nil
		}), nil
	case "dig":
		return NewAutoBuiltin("array.dig", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) == 0 {
				return NewNil(), fmt.Errorf("array.dig expects at least one index")
			}
			return exec.digPath("array.dig", receiver, args)
		}), nil
	case "count":
		return NewAutoBuiltin("array.count", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("array.count accepts at most one value argument")
			}
			arr := receiver.Array()
			if len(args) == 1 {
				// A value argument takes precedence and any attached block is
				// ignored, matching Ruby's Array#count(value) { ... }.
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
			runner, err := newBlockCallRunner(exec, block, "array.count", receiver, nil, kwargs)
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
			return arrayPredicate(exec, receiver, args, kwargs, block, arrayPredicateAny)
		}), nil
	case "all?":
		return NewAutoBuiltin("array.all?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return arrayPredicate(exec, receiver, args, kwargs, block, arrayPredicateAll)
		}), nil
	case "none?":
		return NewAutoBuiltin("array.none?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return arrayPredicate(exec, receiver, args, kwargs, block, arrayPredicateNone)
		}), nil
	case "one?":
		return NewAutoBuiltin("array.one?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.one? does not take arguments")
			}
			var runner *blockCallRunner
			if valueBlock(block) != nil {
				var err error
				runner, err = newBlockCallRunner(exec, block, "array.one?", receiver, nil, kwargs)
				if err != nil {
					return NewNil(), err
				}
			}
			matched := false
			var blockArg [1]Value
			for _, item := range receiver.Array() {
				truthy := item.Truthy()
				if runner != nil {
					// Charge a step per yield so an empty or trivial block body
					// cannot starve the step quota or cancellation checks while
					// traversing a large receiver; runner.call only charges steps
					// for the statements it evaluates, and an empty block evaluates
					// none.
					if err := exec.step(); err != nil {
						return NewNil(), err
					}
					blockArg[0] = item
					val, err := runner.call(blockArg[:])
					if err != nil {
						return NewNil(), err
					}
					truthy = val.Truthy()
				}
				if !truthy {
					continue
				}
				if matched {
					return NewBool(false), nil
				}
				matched = true
			}
			return NewBool(matched), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown array method %s", property)
	}
}

// arrayValuesAtInitialCap bounds the capacity Array#values_at reserves up front.
// A single range selector can expand to far more positions than there are
// arguments, so larger results grow the backing array via append while the
// per-element step() and projected memory check bound the actual allocation. It
// mirrors rangeMaterializeInitialCap.
const arrayValuesAtInitialCap = 4096

// arrayValuesAtRange expands a range selector for Array#values_at, passing each
// selected element to emit in order. It matches Ruby's semantics. A negative
// start counts back from the end; a start that remains negative after that
// adjustment is rejected with an out-of-range error, exactly as Ruby raises a
// RangeError for values_at(-4..-1) on a three-element array. A negative end
// likewise counts back from the end but is never rejected: an end at or before
// the start simply selects nothing. The window is not clamped to the receiver
// length, so positions at or past the end pad with nil (values_at(0..5) on
// [10,20,30] yields [10,20,30,nil,nil,nil]). The selected count is derived by
// subtracting the bounds (end - begin) rather than incrementing the inclusive
// end into an exclusive bound, so a range ending at math.MaxInt64 reports its
// true span instead of overflowing: values_at(MaxInt64..MaxInt64) selects a
// single out-of-bounds position and pads it with nil rather than being rejected.
// Only a window whose span genuinely exceeds the native int range, or the memory
// quota, is rejected as too large. The selected count is reserved against the
// memory quota up front so a huge padded window (values_at(0..1_000_000_000))
// fails fast; emit then charges a step and re-checks the quota per position so
// the materialization stays interruptible even when the up-front reservation
// passes under a large memory quota. The reservation runs through the build
// accumulator so it is measured against the same call-root baseline (including an
// ephemeral receiver) that emit charges each appended slot against.
func arrayValuesAtRange(acc *arrayBuildAccumulator, emittedSoFar int, arr []Value, rng Range, emit func(Value) error) error {
	length := int64(len(arr))
	begin := rng.Start
	if begin < 0 {
		begin += length
		if begin < 0 {
			return fmt.Errorf("array.values_at range %s out of range", NewRange(rng).String())
		}
	}
	end := rng.End
	if end < 0 {
		end += length
	}
	if end < begin {
		return nil
	}
	// Derive the selected count from end - begin (then +1 for an inclusive end)
	// rather than incrementing end into an exclusive bound. The subtraction never
	// overflows because begin >= 0 and end <= math.MaxInt64, so an inclusive range
	// ending at math.MaxInt64 reports its real span instead of wrapping to a
	// negative no-op window.
	span := end - begin
	if !rng.Exclusive {
		if span == math.MaxInt64 {
			// begin == 0 && end == math.MaxInt64: the inclusive span is one past the
			// representable int64 maximum and cannot be materialized.
			return guardLimitErrorf("array.values_at window is too large")
		}
		span++
	}
	if span == 0 {
		return nil
	}
	if span > math.MaxInt {
		return guardLimitErrorf("array.values_at window is too large")
	}
	count := int(span)
	// Fail fast when the window clearly exceeds the memory quota before emitting
	// any element; emit re-checks per position as the backing array grows. The
	// reservation projects the positions already emitted by earlier selectors plus
	// this window's count against the call-root baseline, so a mixed
	// values_at(0..big, 0..big) cannot slip a second huge window past the check.
	if err := acc.reserveSlots(saturatingAdd(emittedSoFar, count)); err != nil {
		return err
	}
	// Positions in [begin, begin+inBounds) read from arr; the remainder pad with
	// nil. inBounds is computed against the receiver length so the absolute index
	// never needs begin + offset, which could overflow int64 when begin is near
	// the maximum (a begin at or past length contributes only padding).
	var inBounds int64
	if begin < length {
		inBounds = length - begin
	}
	for offset := range count {
		selected := NewNil()
		if int64(offset) < inBounds {
			selected = arr[begin+int64(offset)]
		}
		if err := emit(selected); err != nil {
			return err
		}
	}
	return nil
}

// arrayPredicateKind selects the quantifier evaluated by arrayPredicate.
type arrayPredicateKind int

const (
	arrayPredicateAny arrayPredicateKind = iota
	arrayPredicateAll
	arrayPredicateNone
)

// arrayPredicate implements the shared scanning logic for Array#any?, Array#all?
// and Array#none?. It mirrors Ruby's three calling conventions:
//
//	any?           # whether any element is truthy
//	any? { |x| } # whether any block result is truthy
//	any?(pattern)  # whether any element matches pattern via ===
//
// As in Ruby, the value form tests each element with case equality (===) via
// caseCandidateMatches, so range patterns such as any?(1..3) test membership
// rather than object identity. An explicit pattern argument takes precedence
// over any attached block, which is then ignored, matching Array#count(value).
// Keyword arguments are unsupported and rejected. The name of the originating
// method is derived from the predicate kind for error messages.
func arrayPredicate(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value, kind arrayPredicateKind) (Value, error) {
	name := arrayPredicateName(kind)
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("%s does not take keyword arguments", name)
	}
	if len(args) > 1 {
		return NewNil(), fmt.Errorf("%s accepts at most one value argument", name)
	}
	arr := receiver.Array()
	if len(args) == 1 {
		pattern := args[0]
		return arrayPredicateResult(kind, arr, func(item Value) (bool, error) {
			return caseCandidateMatches(item, pattern), nil
		})
	}
	if valueBlock(block) != nil {
		runner, err := newBlockCallRunner(exec, block, name, receiver, nil, kwargs)
		if err != nil {
			return NewNil(), err
		}
		var blockArg [1]Value
		return arrayPredicateResult(kind, arr, func(item Value) (bool, error) {
			blockArg[0] = item
			val, err := runner.call(blockArg[:])
			if err != nil {
				return false, err
			}
			return val.Truthy(), nil
		})
	}
	return arrayPredicateResult(kind, arr, func(item Value) (bool, error) {
		return item.Truthy(), nil
	})
}

// arrayPredicateName returns the public method name used in error messages for a
// predicate kind.
func arrayPredicateName(kind arrayPredicateKind) string {
	switch kind {
	case arrayPredicateAll:
		return "array.all?"
	case arrayPredicateNone:
		return "array.none?"
	default:
		return "array.any?"
	}
}

// arrayPredicateResult applies match to every element until the quantifier can
// short-circuit. any? returns true on the first match, all? returns false on the
// first miss, and none? returns false on the first match; each falls through to
// the vacuous result for an empty or fully scanned array.
func arrayPredicateResult(kind arrayPredicateKind, arr []Value, match func(Value) (bool, error)) (Value, error) {
	for _, item := range arr {
		ok, err := match(item)
		if err != nil {
			return NewNil(), err
		}
		switch kind {
		case arrayPredicateAny:
			if ok {
				return NewBool(true), nil
			}
		case arrayPredicateAll:
			if !ok {
				return NewBool(false), nil
			}
		case arrayPredicateNone:
			if ok {
				return NewBool(false), nil
			}
		}
	}
	return NewBool(kind != arrayPredicateAny), nil
}

// arrayForwardIndex implements the shared forward-scanning logic for
// Array#index and Array#find_index. It mirrors Ruby's two calling conventions:
//
//	index(value)        # first index whose element equals value
//	index { |item| ... } # first index whose block result is truthy
//
// The optional second positional argument is a Vibescript extension that starts
// the value search at a non-negative offset. As in Ruby, a value and a block are
// mutually exclusive, and the block form rejects positional arguments entirely.
// A miss returns nil.
func arrayForwardIndex(exec *Execution, receiver Value, args []Value, block Value, name string) (Value, error) {
	if valueBlock(block) != nil {
		if len(args) > 0 {
			return NewNil(), fmt.Errorf("%s takes a value or a block, not both", name)
		}
		runner, err := newBlockCallRunner(exec, block, name, receiver, nil, nil)
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
	}

	if len(args) < 1 || len(args) > 2 {
		return NewNil(), fmt.Errorf("%s expects a value (with optional offset) or a block", name)
	}
	offset := 0
	if len(args) == 2 {
		n, err := valueToInt(args[1])
		if err != nil || n < 0 {
			return NewNil(), fmt.Errorf("%s offset must be non-negative integer", name)
		}
		offset = n
	}
	arr := receiver.Array()
	for idx := offset; idx < len(arr); idx++ {
		if arr[idx].Equal(args[0]) {
			return NewInt(int64(idx)), nil
		}
	}
	return NewNil(), nil
}

// arrayReverseIndex implements Array#rindex, scanning from the end. It mirrors
// Ruby's two calling conventions:
//
//	rindex(value)         # last index whose element equals value
//	rindex { |item| ... } # last index whose block result is truthy
//
// The optional second positional argument is a Vibescript extension that caps
// the value search at a non-negative offset and scans backward from there. As in
// Ruby, a value and a block are mutually exclusive, and the block form rejects
// positional arguments entirely. A miss returns nil.
func arrayReverseIndex(exec *Execution, receiver Value, args []Value, block Value) (Value, error) {
	const name = "array.rindex"
	arr := receiver.Array()
	if valueBlock(block) != nil {
		if len(args) > 0 {
			return NewNil(), fmt.Errorf("%s takes a value or a block, not both", name)
		}
		runner, err := newBlockCallRunner(exec, block, name, receiver, nil, nil)
		if err != nil {
			return NewNil(), err
		}
		var blockArg [1]Value
		for idx := len(arr) - 1; idx >= 0; idx-- {
			blockArg[0] = arr[idx]
			match, err := runner.call(blockArg[:])
			if err != nil {
				return NewNil(), err
			}
			if match.Truthy() {
				return NewInt(int64(idx)), nil
			}
		}
		return NewNil(), nil
	}

	if len(args) < 1 || len(args) > 2 {
		return NewNil(), fmt.Errorf("%s expects a value (with optional offset) or a block", name)
	}
	offset := -1
	if len(args) == 2 {
		n, err := valueToInt(args[1])
		if err != nil || n < 0 {
			return NewNil(), fmt.Errorf("%s offset must be non-negative integer", name)
		}
		offset = n
	}
	if len(arr) == 0 {
		return NewNil(), nil
	}
	if offset < 0 || offset >= len(arr) {
		offset = len(arr) - 1
	}
	for idx := offset; idx >= 0; idx-- {
		if arr[idx].Equal(args[0]) {
			return NewInt(int64(idx)), nil
		}
	}
	return NewNil(), nil
}

// arraySliceIndex validates a single index argument shared by Array#at and the
// single-index form of Array#slice. It accepts integers and fractional floats
// (truncated toward zero like Ruby's to_int), rejecting other kinds and any
// non-finite or out-of-range float with a method-specific error.
func arraySliceIndex(value Value, method string) (int, error) {
	switch value.Kind() {
	case KindInt, KindFloat:
		index, err := valueToInt(value)
		if err != nil {
			return 0, fmt.Errorf("%s index must be integer", method)
		}
		return index, nil
	default:
		return 0, fmt.Errorf("%s index must be integer", method)
	}
}

// arrayElementAt returns the element at index, counting a negative index back
// from the end. An index outside the array (after normalization) yields nil,
// matching Ruby's Array#at and Array#[] single-index access, which never raise
// for out-of-range integer indexes.
func arrayElementAt(arr []Value, index int) Value {
	if index < 0 {
		index += len(arr)
	}
	if index < 0 || index >= len(arr) {
		return NewNil()
	}
	return arr[index]
}

// arraySlice implements Array#slice across the three argument shapes Vibescript
// can represent: a single integer index, an integer start with a length, and a
// range. It mirrors Ruby's extraction semantics, returning nil for selectors
// that fall outside the array rather than raising.
//
//	slice(index)         # the single element at index (negative counts from end)
//	slice(start, length) # a subarray of up to length elements starting at start
//	slice(range)         # a subarray selected by the range bounds
//
// The single-index form returns the element itself (like Array#at and Array#[]),
// while the start/length and range forms return a new subarray. An out-of-range
// single index, a start beyond the array, or a negative length yields nil; a
// start exactly at the length with a non-negative length yields an empty array,
// matching Ruby ([1, 2, 3].slice(3, 1) is [] while [1, 2, 3].slice(3) is nil).
func arraySlice(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("array.slice does not take keyword arguments")
	}
	arr := receiver.Array()
	switch len(args) {
	case 1:
		if args[0].Kind() == KindRange {
			window, ok := arraySliceRangeWindow(len(arr), args[0].Range())
			if !ok {
				return NewNil(), nil
			}
			if err := reserveArraySliceCallSlots(exec, receiver, args, kwargs, block, window.length); err != nil {
				return NewNil(), err
			}
			return NewArray(copyArraySliceWindow(arr, window)), nil
		}
		index, err := arraySliceIndex(args[0], "array.slice")
		if err != nil {
			return NewNil(), err
		}
		return arrayElementAt(arr, index), nil
	case 2:
		start, err := arraySliceIndex(args[0], "array.slice")
		if err != nil {
			return NewNil(), err
		}
		length, err := valueToInt(args[1])
		if err != nil {
			return NewNil(), fmt.Errorf("array.slice length must be integer")
		}
		if args[1].Kind() != KindInt && args[1].Kind() != KindFloat {
			return NewNil(), fmt.Errorf("array.slice length must be integer")
		}
		window, ok := arraySliceStartLengthWindow(len(arr), start, length)
		if !ok {
			return NewNil(), nil
		}
		if err := reserveArraySliceCallSlots(exec, receiver, args, kwargs, block, window.length); err != nil {
			return NewNil(), err
		}
		return NewArray(copyArraySliceWindow(arr, window)), nil
	default:
		return NewNil(), fmt.Errorf("array.slice expects an index, a start and length, or a range")
	}
}

func reserveArraySliceCallSlots(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value, slotCount int) error {
	if exec.memoryQuota <= 0 {
		return nil
	}
	return newArrayBuildAccumulator(exec, receiver, args, kwargs, block).reserveSlots(slotCount)
}

type arraySliceWindow struct {
	start  int
	length int
}

// arraySliceStartLengthWindow normalizes Array#slice(start, length) bounds. A
// negative start counts back from the end, a start exactly at the length yields
// an empty window, and oversized lengths clamp to the remaining suffix.
func arraySliceStartLengthWindow(arrLen, start, length int) (arraySliceWindow, bool) {
	if length < 0 {
		return arraySliceWindow{}, false
	}
	if start < 0 {
		start += arrLen
		if start < 0 {
			return arraySliceWindow{}, false
		}
	}
	if start > arrLen {
		return arraySliceWindow{}, false
	}
	remaining := arrLen - start
	if length > remaining {
		length = remaining
	}
	return arraySliceWindow{start: start, length: length}, true
}

// arraySliceRangeWindow normalizes Array#slice(range) bounds. Negative bounds
// count back from the end, the end is clamped to the array length, and an end
// before begin yields an empty window.
func arraySliceRangeWindow(arrLen int, rng Range) (arraySliceWindow, bool) {
	length := int64(arrLen)
	begin := rng.Start
	if begin < 0 {
		begin += length
	}
	if begin < 0 || begin > length {
		return arraySliceWindow{}, false
	}
	end := rng.End
	if end < 0 {
		end += length
	}
	if !rng.Exclusive {
		// An inclusive range's exclusive end is one past End; guard the increment so
		// End == math.MaxInt64 cannot wrap to a negative no-op window.
		if end == math.MaxInt64 {
			end = length
		} else {
			end++
		}
	}
	if end > length {
		end = length
	}
	if end < begin {
		end = begin
	}
	return arraySliceWindow{start: int(begin), length: int(end - begin)}, true
}

func copyArraySliceWindow(arr []Value, window arraySliceWindow) []Value {
	out := make([]Value, window.length)
	copy(out, arr[window.start:window.start+window.length])
	return out
}

// arrayReduce folds the receiver into a single value. It supports Ruby's three
// calling conventions for Array#reduce / Enumerable#inject:
//
//	reduce { |acc, item| ... }          # fold from the first element
//	reduce(initial) { |acc, item| ... } # fold from an explicit initial value
//	reduce(operation)                   # fold by sending a symbol/string op
//	reduce(initial, operation)          # fold from initial by sending an op
//
// The operation form sends the named operation to the accumulator with each
// element as its argument, matching Ruby's `acc.public_send(op, item)`. Both
// symbol and string operation names are accepted at runtime, mirroring Ruby's
// acceptance of `reduce(:+)` and `reduce("+")`.
// Following Ruby, a block takes precedence: when a block is supplied, a lone
// argument is always treated as the initial value, never as an operation.
func arrayReduce(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("array.reduce does not take keyword arguments")
	}
	if len(args) > 2 {
		return NewNil(), fmt.Errorf("array.reduce accepts at most an initial value and an operation")
	}

	hasBlock := valueBlock(block) != nil
	var initial Value
	hasInitial := false
	operation := ""
	hasOperation := false

	switch {
	case len(args) == 2:
		// reduce(initial, operation): the operation argument must name an op.
		op, ok := reduceOperationName(args[1])
		if !ok {
			return NewNil(), fmt.Errorf("array.reduce operation must be a symbol or string")
		}
		initial, hasInitial = args[0], true
		operation, hasOperation = op, true
	case len(args) == 1 && hasBlock:
		// reduce(initial) { block }: a block always makes the argument the seed.
		initial, hasInitial = args[0], true
	case len(args) == 1:
		// reduce(operation): the sole argument must name an op when no block runs.
		op, ok := reduceOperationName(args[0])
		if !ok {
			return NewNil(), fmt.Errorf("array.reduce operation must be a symbol or string")
		}
		operation, hasOperation = op, true
	case !hasBlock:
		return NewNil(), fmt.Errorf("array.reduce requires a block or an operation")
	}

	var runner *blockCallRunner
	if hasBlock {
		var err error
		runner, err = newBlockCallRunner(exec, block, "array.reduce", receiver, args, kwargs)
		if err != nil {
			return NewNil(), err
		}
	}

	arr := receiver.Array()
	if len(arr) == 0 {
		if hasInitial {
			return initial, nil
		}
		// An empty array with no initial value folds to nil, matching Ruby's
		// `[].reduce(:+)` and `[].reduce { ... }`.
		return NewNil(), nil
	}

	acc := initial
	start := 0
	if !hasInitial {
		acc = arr[0]
		start = 1
	}

	if hasOperation {
		// The operation form has no block runner to account for work, so charge
		// a step per element and bound the accumulator's retained size, honoring
		// the sandbox limits exactly as the block form's runner does.
		for i := start; i < len(arr); i++ {
			if err := exec.step(); err != nil {
				return NewNil(), err
			}
			next, err := exec.reduceSendOperation(acc, operation, arr[i])
			if err != nil {
				return NewNil(), err
			}
			if err := exec.checkMemoryWith(next); err != nil {
				return NewNil(), err
			}
			acc = next
		}
		return acc, nil
	}

	var blockArgs [2]Value
	for i := start; i < len(arr); i++ {
		blockArgs[0] = acc
		blockArgs[1] = arr[i]
		// The accumulator lives only in this Go frame and evolves every call (the
		// seed first, each prior call's result after), so it is not in the runner's
		// one-time baseline and cannot be folded into it like a fixed positional
		// root. Charge it per call so a block that copies its tail into a
		// rest-collecting parameter (reduce(big) do |(head, *tail), item| ... end) is
		// rejected when the real peak (receiver + accumulator + tail) exceeds the
		// quota, not just when receiver + tail does. The charge probes the accumulator
		// against the snapshotted call roots, so a no-seed accumulator that is the
		// receiver's first element deduplicates against the receiver and is charged
		// only its structural slots -- never a second copy of the receiver's data.
		next, err := runner.callWithChargedRoots(blockArgs[:], acc)
		if err != nil {
			return NewNil(), err
		}
		acc = next
	}
	return acc, nil
}

// arraySum totals an array, mirroring Ruby's Array#sum forms:
//
//	values.sum                       # start from 0, add each element
//	values.sum(initial)              # start from initial, add each element
//	values.sum { |item| ... }        # start from 0, add each block result
//	values.sum(initial) { |item| ... } # combine both
//
// The optional positional argument seeds the accumulator and an optional block
// transforms each element before it is added. Like Ruby's `+`, each addition
// must operate on compatible operands, so mixing a string with a non-string (or
// any other unsupported pair) raises instead of silently coercing the operands.
func arraySum(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("array.sum does not take keyword arguments")
	}
	if len(args) > 1 {
		return NewNil(), fmt.Errorf("array.sum accepts at most an initial value")
	}

	total := NewInt(0)
	if len(args) == 1 {
		total = args[0]
	}

	var runner *blockCallRunner
	if valueBlock(block) != nil {
		var err error
		runner, err = newBlockCallRunner(exec, block, "array.sum", receiver, args, kwargs)
		if err != nil {
			return NewNil(), err
		}
	}

	arr := receiver.Array()
	var blockArg [1]Value
	for _, item := range arr {
		if err := exec.step(); err != nil {
			return NewNil(), err
		}
		contribution := item
		if runner != nil {
			blockArg[0] = item
			result, err := runner.call(blockArg[:])
			if err != nil {
				return NewNil(), err
			}
			contribution = result
		}
		next, err := arraySumAdd(total, contribution)
		if err != nil {
			return NewNil(), err
		}
		// Both the prior total and the contribution stay live on the Go stack
		// alongside next: arraySumAdd builds next from a fresh copy (a new string
		// buffer or a new slice backing) of the old total and the contribution, so
		// all three coexist at this step's peak. The prior total matters most when it
		// has grown across iterations into a large string or array that is reachable
		// only from this Go-local — the base walk never sees it, so a quota above the
		// new accumulator alone but below old_total + new accumulator would otherwise
		// admit a step whose true peak exceeds the limit. Charge both as live extras
		// so the step is rejected here rather than only after the builtin returns. The
		// estimator dedups each, so the blockless case (contribution is a receiver
		// element) and an int seed (a scalar prior total) add nothing meaningful.
		if err := exec.checkAccumulatorWithCallRoots(next, receiver, args, kwargs, block, total, contribution); err != nil {
			return NewNil(), err
		}
		total = next
	}
	return total, nil
}

// arraySumAdd adds one contribution into the running total for array.sum. It
// reuses addValues for the actual arithmetic but rejects the asymmetric
// string-coercion addValues allows (e.g. 0 + "a"), matching Ruby's strict `+`
// where a string and a non-string cannot be summed together.
func arraySumAdd(total, contribution Value) (Value, error) {
	isString := func(v Value) bool { return v.Kind() == KindString }
	if isString(total) != isString(contribution) {
		return NewNil(), errArraySumIncompatible
	}
	sum, err := addValues(total, contribution)
	if err != nil {
		return NewNil(), errArraySumIncompatible
	}
	return sum, nil
}

// errArraySumIncompatible is returned when array.sum encounters operands that
// cannot be added together, such as summing a string with a number.
var errArraySumIncompatible = errors.New("array.sum cannot add incompatible values")

// reduceOperationName extracts an operation name from a reduce argument. Ruby
// accepts both symbols and strings here (`reduce(:+)` and `reduce("+")`) and
// raises a TypeError ("not a symbol nor a string") for anything else.
func reduceOperationName(v Value) (string, bool) {
	switch v.Kind() {
	case KindSymbol, KindString:
		return v.String(), true
	default:
		return "", false
	}
}

// reduceArithmeticOps maps the binary operator names accepted by reduce's
// operation form to the runtime helpers that implement them. Ruby exposes these
// as methods on its numeric and collection types; Vibescript implements them as
// operators, so the symbol shorthand routes through the same helpers the `+`,
// `-`, `*`, `/`, `%`, and `**` operators use.
var reduceArithmeticOps = map[string]func(left, right Value) (Value, error){
	"+":  addValues,
	"-":  subtractValues,
	"*":  multiplyValues,
	"/":  divideValues,
	"%":  moduloValues,
	"**": powerValues,
	"<<": shovelValues,
	"&":  intersectValues,
}

// reduceSendOperation applies a single fold step by sending operation to the
// accumulator with item as its argument. Operator names dispatch to the same
// arithmetic helpers the corresponding operators use; any other name is treated
// as a method invoked as `accumulator.operation(item)`, mirroring Ruby's
// `accumulator.public_send(operation, item)`. Resolution is public-only, so an
// accumulator that happens to be the current self cannot reach private methods,
// matching public_send's privacy guarantee.
func (exec *Execution) reduceSendOperation(accumulator Value, operation string, item Value) (Value, error) {
	if op, ok := reduceArithmeticOps[operation]; ok {
		return op(accumulator, item)
	}
	member, err := exec.getPublicMember(accumulator, operation, Position{})
	if err != nil {
		return NewNil(), fmt.Errorf("array.reduce cannot apply %q: %w", operation, err)
	}
	return exec.invokeCallable(member, accumulator, []Value{item}, nil, NewNil(), Position{})
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
			// The pattern argument lives on the Go call stack for the whole grep
			// loop, so pass it as the runner's positional call root: a transform
			// block that copies part of an element into a rest-collecting parameter
			// must be charged against a baseline that counts every live call root.
			runner, err = newBlockCallRunner(exec, block, name, receiver, args, kwargs)
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

func arrayUniq(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value, method string) (Value, bool, error) {
	if len(args) > 0 {
		return NewNil(), false, fmt.Errorf("%s does not take arguments", method)
	}
	if len(kwargs) > 0 {
		return NewNil(), false, fmt.Errorf("%s does not take keyword arguments", method)
	}
	arr := receiver.Array()
	if valueBlock(block) == nil {
		unique, err := uniqueValuesChecked(arr, exec.checkContext)
		if err != nil {
			return NewNil(), false, err
		}
		return NewArray(unique), len(unique) != len(arr), nil
	}
	runner, err := newBlockCallRunner(exec, block, method, receiver, nil, kwargs)
	if err != nil {
		return NewNil(), false, err
	}
	acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
	keyAcc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
	keyScratchReserved := 0
	out := make([]Value, 0, boundedSetCap(len(arr)))
	var seen valueSet
	var blockArg [1]Value
	changed := false
	for _, item := range arr {
		if err := exec.step(); err != nil {
			return NewNil(), false, err
		}
		blockArg[0] = item
		key, err := runner.call(blockArg[:])
		if err != nil {
			return NewNil(), false, err
		}
		if seen.contains(key) {
			changed = true
			continue
		}
		projectedKeyScratch := valueSetScratchBytesForNext(seen, key, len(arr))
		if projectedKeyScratch > keyScratchReserved {
			if err := keyAcc.reserveScratch(projectedKeyScratch - keyScratchReserved); err != nil {
				return NewNil(), false, err
			}
			keyScratchReserved = projectedKeyScratch
		}
		if err := keyAcc.addToReservedBacking(key); err != nil {
			return NewNil(), false, err
		}
		seen.add(key, len(arr))
		out = append(out, item)
		if err := acc.add(item, cap(out)); err != nil {
			return NewNil(), false, err
		}
	}
	return NewArray(out), changed, nil
}

func valueSetScratchBytesForNext(seen valueSet, next Value, hint int) int {
	scalarCount := len(seen.scalars)
	compositeCap := cap(seen.composite)
	if _, ok := scalarValueKey(next); ok {
		scalarCount++
	} else if len(seen.composite) == compositeCap {
		if compositeCap == 0 {
			compositeCap = 1
		} else {
			compositeCap *= 2
		}
		if maxCap := boundedSetCap(hint); compositeCap > maxCap && len(seen.composite) < maxCap {
			compositeCap = maxCap
		}
	}
	return valueSetScratchBytesForCounts(scalarCount, compositeCap)
}

func valueSetScratchBytesForCounts(scalarCount, compositeCap int) int {
	total := 0
	if scalarCount > 0 {
		scalarEntryBytes := estimatedMapEntryBytes + estimatedValueBytes + estimatedStringHeaderBytes + saturatingMul(5, estimatedIntBytes)
		total = saturatingAdd(total, estimatedMapBaseBytes)
		total = saturatingAdd(total, saturatingMul(scalarCount, scalarEntryBytes))
	}
	if compositeCap > 0 {
		total = saturatingAdd(total, estimatedSliceBaseBytes)
		total = saturatingAdd(total, saturatingMul(compositeCap, estimatedValueBytes))
	}
	return total
}

func arrayCompact(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value, method string) (Value, bool, error) {
	if len(args) > 0 {
		return NewNil(), false, fmt.Errorf("%s does not take arguments", method)
	}
	if len(kwargs) > 0 {
		return NewNil(), false, fmt.Errorf("%s does not take keyword arguments", method)
	}
	if valueBlock(block) != nil {
		return NewNil(), false, fmt.Errorf("%s does not accept a block", method)
	}
	arr := receiver.Array()
	out := make([]Value, 0, len(arr))
	changed := false
	for _, item := range arr {
		if item.Kind() != KindNil {
			out = append(out, item)
			continue
		}
		changed = true
	}
	return NewArray(out), changed, nil
}

func arrayFilterByBlock(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value, method string, keepTruthy, bang bool) (Value, error) {
	if len(args) > 0 {
		return NewNil(), fmt.Errorf("%s does not take arguments", method)
	}
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("%s does not take keyword arguments", method)
	}
	runner, err := newBlockCallRunner(exec, block, method, receiver, nil, kwargs)
	if err != nil {
		return NewNil(), err
	}
	arr := receiver.Array()
	out := make([]Value, 0, boundedFilterCap(len(arr)))
	acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
	changed := false
	var blockArg [1]Value
	for _, item := range arr {
		if err := exec.step(); err != nil {
			return NewNil(), err
		}
		blockArg[0] = item
		val, err := runner.call(blockArg[:])
		if err != nil {
			return NewNil(), err
		}
		keep := val.Truthy() == keepTruthy
		if keep {
			out = append(out, item)
			if err := acc.add(item, cap(out)); err != nil {
				return NewNil(), err
			}
			continue
		}
		changed = true
	}
	result := NewArray(out)
	if bang && !changed {
		return NewNil(), nil
	}
	return result, nil
}

type arrayChunkControl int

const (
	arrayChunkNormal arrayChunkControl = iota
	arrayChunkSeparator
	arrayChunkAlone
)

func arrayChunkControlForKey(key Value) arrayChunkControl {
	if key.Kind() == KindNil {
		return arrayChunkSeparator
	}
	if key.Kind() != KindSymbol {
		return arrayChunkNormal
	}
	switch key.String() {
	case "_separator":
		return arrayChunkSeparator
	case "_alone":
		return arrayChunkAlone
	default:
		return arrayChunkNormal
	}
}

func arrayChunkByBlock(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) > 0 {
		return NewNil(), fmt.Errorf("array.chunk does not take arguments when a block is supplied")
	}
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("array.chunk does not take keyword arguments")
	}
	runner, err := newBlockCallRunner(exec, block, "array.chunk", receiver, nil, kwargs)
	if err != nil {
		return NewNil(), err
	}
	arr := receiver.Array()
	acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
	out := make([]Value, 0, boundedFilterCap(len(arr)))
	if len(arr) == 0 {
		return NewArray(out), nil
	}
	var blockArg [1]Value
	var currentKey Value
	active := false
	start := 0
	emit := func(key Value, begin, end int) error {
		group := make([]Value, end-begin)
		copy(group, arr[begin:end])
		pair := NewArray([]Value{key, NewArray(group)})
		out = append(out, pair)
		return acc.add(pair, cap(out))
	}
	for i, item := range arr {
		if err := exec.step(); err != nil {
			return NewNil(), err
		}
		blockArg[0] = item
		key, err := runner.call(blockArg[:])
		if err != nil {
			return NewNil(), err
		}
		switch arrayChunkControlForKey(key) {
		case arrayChunkSeparator:
			if active {
				if err := emit(currentKey, start, i); err != nil {
					return NewNil(), err
				}
				active = false
			}
			start = i + 1
			continue
		case arrayChunkAlone:
			if active {
				if err := emit(currentKey, start, i); err != nil {
					return NewNil(), err
				}
				active = false
			}
			if err := emit(key, i, i+1); err != nil {
				return NewNil(), err
			}
			start = i + 1
			continue
		}
		if !active {
			currentKey = key
			start = i
			active = true
			continue
		}
		if !key.Equal(currentKey) {
			if err := emit(currentKey, start, i); err != nil {
				return NewNil(), err
			}
			currentKey = key
			start = i
		}
	}
	if active {
		if err := emit(currentKey, start, len(arr)); err != nil {
			return NewNil(), err
		}
	}
	return NewArray(out), nil
}

func arrayAdjacentSlices(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value, method string, splitOnTruthy bool) (Value, error) {
	if len(args) > 0 {
		return NewNil(), fmt.Errorf("%s does not take arguments", method)
	}
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("%s does not take keyword arguments", method)
	}
	runner, err := newBlockCallRunner(exec, block, method, receiver, nil, kwargs)
	if err != nil {
		return NewNil(), err
	}
	arr := receiver.Array()
	acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
	out := make([]Value, 0, boundedFilterCap(len(arr)))
	if len(arr) == 0 {
		return NewArray(out), nil
	}
	start := 0
	flush := func(end int) error {
		part := make([]Value, end-start)
		copy(part, arr[start:end])
		out = append(out, NewArray(part))
		return acc.add(out[len(out)-1], cap(out))
	}
	var blockArgs [2]Value
	for i := 1; i < len(arr); i++ {
		if err := exec.step(); err != nil {
			return NewNil(), err
		}
		blockArgs[0] = arr[i-1]
		blockArgs[1] = arr[i]
		val, err := runner.call(blockArgs[:])
		if err != nil {
			return NewNil(), err
		}
		split := val.Truthy() == splitOnTruthy
		if split {
			if err := flush(i); err != nil {
				return NewNil(), err
			}
			start = i
		}
	}
	if err := flush(len(arr)); err != nil {
		return NewNil(), err
	}
	return NewArray(out), nil
}

// arrayToHash implements Ruby's Array#to_h, converting an array of two-element
// [key, value] pairs into a hash. It accepts the bare and block forms:
//
//	[[:a, 1], [:b, 2]].to_h            # => { a: 1, b: 2 }
//	[:a, :b].to_h { |sym| [sym, 0] }   # => { a: 0, b: 0 }
//
// In the block form the block maps each element to the [key, value] pair, so the
// receiver's elements need not themselves be pairs. Either way each pair must be
// a two-element array whose key resolves through the same symbol/string hash-key
// rules used everywhere else; a non-array element, a wrong-length pair, or an
// unsupported key type is rejected, matching Ruby's TypeError/ArgumentError for
// malformed pairs. Duplicate keys keep the last pair encountered, like Ruby.
func arrayToHash(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) > 0 {
		return NewNil(), fmt.Errorf("array.to_h does not take arguments")
	}
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("array.to_h does not take keyword arguments")
	}
	arr := receiver.Array()

	var runner *blockCallRunner
	if valueBlock(block) != nil {
		var err error
		runner, err = newBlockCallRunner(exec, block, "array.to_h", receiver, args, kwargs)
		if err != nil {
			return NewNil(), err
		}
	}

	// Abort before reserving anything when the context is already canceled or the
	// remaining step quota cannot cover the per-element loop. The loop still
	// charges each step, but the make below reserves a map sized to the whole
	// receiver first, so without this up-front check a large receiver could trigger
	// that full allocation even though the conversion is guaranteed to fail.
	if err := exec.checkStepBudgetFor(len(arr)); err != nil {
		return NewNil(), err
	}

	// The output holds at most one entry per element; duplicate keys collapse.
	// Reject the build before reserving the backing map when that capacity alone
	// already overflows the quota, mirroring the map-producing hash transforms.
	if err := exec.checkProjectedHashBytes(len(arr), receiver, args, kwargs, block); err != nil {
		return NewNil(), err
	}

	// In the bare form every pair, key, and value aliases a receiver element, so
	// the structural projection above (sized to the receiver, already a live call
	// root) bounds the build. The block form is different: it can synthesize a
	// fresh key string and a fresh value per element (ids.to_h { |id| [id.to_s,
	// big] }), and those results live only in the Go-local out map until the
	// builtin returns. The structural projection cannot see them, so many
	// individually-under-quota block results could grow the map past the quota
	// before the post-call check observed them. Charge each block-produced key and
	// value incrementally through a build accumulator so accumulated payloads count
	// toward the quota as entries are inserted. The accumulator is only needed for
	// the block form; nil for the bare form skips the per-entry charge.
	var acc *hashBuildAccumulator
	if runner != nil {
		acc = newHashBuildAccumulator(exec, receiver, args, kwargs, block)
		// The output map is preallocated with make(map, len(arr)), so its full
		// backing is live before the first block runs; reserve it so a large early
		// block result is checked against the whole backing, not just the entries
		// inserted so far.
		if err := acc.reserveBacking(len(arr)); err != nil {
			return NewNil(), err
		}
	}

	out := make(map[string]Value, len(arr))
	var blockArg [1]Value
	for _, item := range arr {
		// Charge a step per element so even the bare form, where runner is nil and
		// no block statements run, participates in the step quota and observes
		// cancellation while converting a large receiver. runner.call only charges
		// steps for the statements the block evaluates, and the bare form evaluates
		// none, so without this a huge pairs.to_h could run to completion despite a
		// canceled context or a tiny StepQuota.
		if err := exec.step(); err != nil {
			return NewNil(), err
		}
		pair := item
		if runner != nil {
			blockArg[0] = item
			mapped, err := runner.call(blockArg[:])
			if err != nil {
				return NewNil(), err
			}
			if err := acc.checkTransient(mapped); err != nil {
				return NewNil(), err
			}
			pair = mapped
		}
		if pair.Kind() != KindArray {
			return NewNil(), fmt.Errorf("array.to_h expects an array of two-element pairs")
		}
		elements := pair.Array()
		if len(elements) != 2 {
			return NewNil(), fmt.Errorf("array.to_h pair must have exactly two elements")
		}
		key, err := valueToHashKey(elements[0])
		if err != nil {
			return NewNil(), fmt.Errorf("array.to_h pair key must be symbol or string")
		}
		out[key] = elements[1]
		if acc != nil {
			// The block synthesized both the key and the value, so charge each: the
			// key string via addSynthesizedKey and the value via add. Both route
			// through the accumulator's results-only estimator, so a backing shared
			// across block results is counted once while a result aliasing a receiver
			// element is conservatively counted again rather than dedup'd to nothing.
			if err := acc.addSynthesizedKey(key); err != nil {
				return NewNil(), err
			}
			if err := acc.add(elements[1]); err != nil {
				return NewNil(), err
			}
		}
	}
	return NewHash(out), nil
}

// arrayFillInitialCap bounds the capacity reserved up front when building a
// fill result. Larger fills grow the backing array via append so the per-element
// step() and arrayBuildAccumulator checks bound the actual allocation, rather
// than reserving the full requested length immediately. It mirrors
// rangeMaterializeInitialCap.
const arrayFillInitialCap = 4096

// arrayInsertInitialCap bounds the capacity reserved up front when building an
// insert result. Larger inserts grow by append so step and memory checks run
// before the backing reaches the requested final length.
const arrayInsertInitialCap = arrayFillInitialCap

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

	var runner *blockCallRunner
	if hasBlock {
		runner, err = newBlockCallRunner(exec, block, "array.fill", receiver, nil, kwargs)
		if err != nil {
			return NewNil(), err
		}
	}

	// Grow the result with append from a bounded initial capacity rather than
	// reserving the full finalLength up front. The projected check above only
	// fails fast when the requested window clearly exceeds the memory quota; it
	// passes whenever MemoryQuotaBytes is large, so preallocating finalLength
	// there would let a small StepQuota paired with a generous memory quota
	// still trigger a huge up-front allocation before the per-element step()
	// loop could abort. Bounding the initial capacity and re-checking the
	// backing array's growth per element keeps the actual allocation
	// proportional to what the quotas allow, mirroring rangeMaterialize.
	initialCap := span.finalLength
	if initialCap > arrayFillInitialCap {
		initialCap = arrayFillInitialCap
	}
	out := make([]Value, 0, initialCap)

	// Track accumulated payloads incrementally rather than re-estimating the whole
	// prefix on each append: checkMemoryWith(NewArray(out)) would walk every element
	// per append and make the fill O(n^2). The accumulator snapshots the live base
	// (including this call's receiver/args/block, still live on the Go stack) once,
	// then per kept element walks only that element (O(element size)), so a block
	// returning quota-sized values is charged during the loop instead of slipping
	// past the slot-only backing check until fill returns.
	acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)

	appendValue := func(val Value, conservative bool) error {
		out = append(out, val)
		if conservative {
			return acc.addConservative(val, cap(out))
		}
		return acc.add(val, cap(out))
	}

	var blockArg [1]Value
	for i := range span.finalLength {
		// Charge a step for every position written, not just the ones inside the
		// fill window. A fill that grows the array past its old length pads the
		// nil gap (and copies the existing prefix) one slot at a time, so without
		// a step per slot a window far past the end (e.g. fill(0, 1_000_000, 0))
		// could materialize a huge array under a small step quota without ever
		// polling cancellation. Stepping every iteration keeps total growth
		// bounded by the step quota regardless of where the window sits.
		if err := exec.step(); err != nil {
			return NewNil(), err
		}
		switch {
		case i >= span.begin && i < span.end:
			// Within the fill window: resolve the element from the block or the
			// explicit fill value.
			var val Value
			if runner != nil {
				blockArg[0] = NewInt(int64(i))
				val, err = runner.call(blockArg[:])
				if err != nil {
					return NewNil(), err
				}
			} else {
				val = args[0]
			}
			if err := appendValue(val, runner != nil); err != nil {
				return NewNil(), err
			}
		case i < len(arr):
			// Outside the window but within the receiver: copy the original
			// element unchanged (the prefix before the window or the tail after
			// it).
			if err := appendValue(arr[i], false); err != nil {
				return NewNil(), err
			}
		default:
			// Past the receiver's end but before the window: pad the gap with
			// nil, the value Ruby inserts when fill grows the array past its old
			// length.
			if err := appendValue(NewNil(), false); err != nil {
				return NewNil(), err
			}
		}
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
		// A nil length is treated as omitted, filling from begin to the end,
		// matching Ruby's Array#fill (which reads a nil length as "to the end").
		if selectors[1].Kind() == KindNil {
			return arrayFillSpanFromStart(begin, length, length-begin)
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
// A nil start is treated as 0, matching Ruby's Array#fill, which reads a nil
// start (or omitted start) as the beginning of the array. Fractional floats
// truncate toward zero like Ruby's to_int. A negative start counts back from
// the end like Ruby; a start more negative than the receiver length clamps to 0
// (Ruby's Array#fill does not raise for an out-of-range negative integer start,
// unlike a range bound).
func arrayFillStartIndex(value Value, length int) (int, error) {
	if value.Kind() == KindNil {
		return 0, nil
	}
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
		return arrayFillSpan{}, guardLimitErrorf("array.fill window is too large")
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
			return arrayFillSpan{}, guardLimitErrorf("array.fill window is too large")
		}
		end++
	}
	if end < begin {
		end = begin
	}
	if begin > math.MaxInt || end > math.MaxInt {
		return arrayFillSpan{}, guardLimitErrorf("array.fill window is too large")
	}
	finalLength := length
	if int(end) > finalLength {
		finalLength = int(end)
	}
	return arrayFillSpan{begin: int(begin), end: int(end), finalLength: finalLength}, nil
}

func arrayMemberTransforms(property string) (Value, error) {
	switch property {
	case "push", "append":
		// Ruby exposes Array#append as an alias for Array#push, appending the
		// arguments (in order) to the end of the receiver. Vibescript's
		// collections are non-mutating, so both return a new array.
		name := "array." + property
		return NewAutoBuiltin(name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("%s does not take keyword arguments", name)
			}
			base := receiver.Array()
			out := make([]Value, len(base)+len(args))
			copy(out, base)
			copy(out[len(base):], args)
			return NewArray(out), nil
		}), nil
	case "prepend", "unshift":
		// Ruby exposes Array#unshift as an alias for Array#prepend, inserting the
		// arguments, in order, at the front of the array. Vibescript's collections
		// are non-mutating, so both return a new array.
		name := "array." + property
		return NewAutoBuiltin(name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("%s does not take keyword arguments", name)
			}
			base := receiver.Array()
			out := make([]Value, len(args)+len(base))
			copy(out, args)
			copy(out[len(args):], base)
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
				if err := newArrayBuildAccumulator(exec, receiver, args, kwargs, block).reserveSlotArrays(len(arr)); err != nil {
					return NewNil(), err
				}
				// Return a copy of the receiver rather than wrapping its backing
				// slice, so mutating the returned array through index assignment
				// cannot reach the source.
				remaining := make([]Value, len(arr))
				copy(remaining, arr)
				return NewHash(map[string]Value{
					"array":  NewArray(remaining),
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
			remainingLen := len(arr) - count
			returnsRemovedArray := count != 1 || len(args) != 0
			if returnsRemovedArray {
				if err := newArrayBuildAccumulator(exec, receiver, args, kwargs, block).reserveSlotArrays(remainingLen, count); err != nil {
					return NewNil(), err
				}
			} else if err := newArrayBuildAccumulator(exec, receiver, args, kwargs, block).reserveSlotArrays(remainingLen); err != nil {
				return NewNil(), err
			}
			remaining := make([]Value, remainingLen)
			copy(remaining, arr[:len(arr)-count])
			result := map[string]Value{
				"array": NewArray(remaining),
			}
			if returnsRemovedArray {
				removed := make([]Value, count)
				copy(removed, arr[len(arr)-count:])
				result["popped"] = NewArray(removed)
			} else {
				result["popped"] = arr[len(arr)-1]
			}
			return NewHash(result), nil
		}), nil
	case "shift":
		return NewAutoBuiltin("array.shift", arrayShift), nil
	case "delete":
		return NewAutoBuiltin("array.delete", arrayDelete), nil
	case "insert":
		return NewAutoBuiltin("array.insert", arrayInsert), nil
	case "clear":
		return NewAutoBuiltin("array.clear", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.clear does not take arguments")
			}
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("array.clear does not take keyword arguments")
			}
			if valueBlock(block) != nil {
				return NewNil(), fmt.Errorf("array.clear does not accept a block")
			}
			return NewArray([]Value{}), nil
		}), nil
	case "delete_if", "keep_if":
		name := "array." + property
		keepTruthy := property == "keep_if"
		return NewAutoBuiltin(name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return arrayFilterByBlock(exec, receiver, args, kwargs, block, name, keepTruthy, false)
		}), nil
	case "uniq":
		return NewAutoBuiltin("array.uniq", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			result, _, err := arrayUniq(exec, receiver, args, kwargs, block, "array.uniq")
			return result, err
		}), nil
	case "uniq!":
		return NewAutoBuiltin("array.uniq!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			result, changed, err := arrayUniq(exec, receiver, args, kwargs, block, "array.uniq!")
			if err != nil {
				return NewNil(), err
			}
			if !changed {
				return NewNil(), nil
			}
			return result, nil
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
	case "sample":
		return NewAutoBuiltin("array.sample", arraySample), nil
	case "shuffle":
		return NewAutoBuiltin("array.shuffle", arrayShuffle), nil
	case "rotate":
		return NewAutoBuiltin("array.rotate", arrayRotate), nil
	case "product":
		return NewAutoBuiltin("array.product", arrayProduct), nil
	case "combination":
		return NewAutoBuiltin("array.combination", arrayCombination), nil
	case "permutation":
		return NewAutoBuiltin("array.permutation", arrayPermutation), nil
	case "repeated_combination":
		return NewAutoBuiltin("array.repeated_combination", arrayRepeatedCombination), nil
	case "repeated_permutation":
		return NewAutoBuiltin("array.repeated_permutation", arrayRepeatedPermutation), nil
	case "first":
		return NewAutoBuiltin("array.first", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("array.first does not take keyword arguments")
			}
			arr := receiver.Array()
			if len(args) == 0 {
				if len(arr) == 0 {
					return NewNil(), nil
				}
				return arr[0], nil
			}
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("array.first accepts at most one count")
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
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("array.last does not take keyword arguments")
			}
			arr := receiver.Array()
			if len(args) == 0 {
				if len(arr) == 0 {
					return NewNil(), nil
				}
				return arr[len(arr)-1], nil
			}
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("array.last accepts at most one count")
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
		return NewAutoBuiltin("array.sum", arraySum), nil
	case "compact":
		return NewAutoBuiltin("array.compact", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			result, _, err := arrayCompact(exec, receiver, args, kwargs, block, "array.compact")
			return result, err
		}), nil
	case "compact!":
		return NewAutoBuiltin("array.compact!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			result, changed, err := arrayCompact(exec, receiver, args, kwargs, block, "array.compact!")
			if err != nil {
				return NewNil(), err
			}
			if !changed {
				return NewNil(), nil
			}
			return result, nil
		}), nil
	case "flatten":
		return NewAutoBuiltin("array.flatten", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			// depth=-1 is a sentinel value meaning "flatten fully" (no depth
			// limit). flattenValues treats every negative depth as unlimited, so
			// nil, negative integers, and the no-argument form all flatten fully,
			// matching Ruby's Array#flatten depth semantics.
			depth := -1
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("array.flatten accepts at most one depth argument")
			}
			if len(args) == 1 && args[0].Kind() != KindNil {
				n, err := valueToInt(args[0])
				if err != nil {
					return NewNil(), fmt.Errorf("array.flatten depth must be an integer")
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
	case "to_h":
		return NewAutoBuiltin("array.to_h", arrayToHash), nil
	case "fill":
		return NewAutoBuiltin("array.fill", arrayFill), nil
	case "chunk":
		return NewAutoBuiltin("array.chunk", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if valueBlock(block) != nil {
				return arrayChunkByBlock(exec, receiver, args, kwargs, block)
			}
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
			// arrayJoin recursively joins nested arrays with the active separator,
			// matching Ruby's Array#join, and guards against cyclic or pathologically
			// deep structures the same way array.flatten does.
			var b strings.Builder
			if err := arrayJoin(&b, arr, sep); err != nil {
				return NewNil(), err
			}
			result := NewString(b.String())
			if err := exec.checkMemoryWith(result); err != nil {
				return NewNil(), err
			}
			return result, nil
		}), nil
	case "reverse":
		return NewAutoBuiltin("array.reverse", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return arrayReverseCopy(exec, receiver, args, kwargs, block, "array.reverse")
		}), nil
	case "reverse!":
		return NewAutoBuiltin("array.reverse!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return arrayReverseCopy(exec, receiver, args, kwargs, block, "array.reverse!")
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
	case "sort!":
		return NewAutoBuiltin("array.sort!", arraySortBang), nil
	case "map!":
		return NewAutoBuiltin("array.map!", arrayMapBang), nil
	case "select!":
		return NewAutoBuiltin("array.select!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return arrayFilterByBlock(exec, receiver, args, kwargs, block, "array.select!", true, true)
		}), nil
	case "reject!":
		return NewAutoBuiltin("array.reject!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return arrayFilterByBlock(exec, receiver, args, kwargs, block, "array.reject!", false, true)
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown array method %s", property)
	}
}

// arrayShift implements Ruby's Array#shift, removing element(s) from the front.
// Vibescript collections are non-mutating, so it returns both halves of the
// result as the hash { array:, shifted: }, mirroring Array#pop's
// { array:, popped: } convention. Bare shift removes one element and reports it
// (or nil on an empty array); shift(n) removes up to n elements and reports them
// as an array. n must be a non-negative integer.
func arrayShift(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) > 1 {
		return NewNil(), fmt.Errorf("array.shift accepts at most one argument")
	}
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("array.shift does not take keyword arguments")
	}
	count := 1
	if len(args) == 1 {
		n, err := valueToInt(args[0])
		if err != nil || n < 0 {
			return NewNil(), fmt.Errorf("array.shift expects non-negative integer")
		}
		count = n
	}
	arr := receiver.Array()
	if count == 0 {
		if err := newArrayBuildAccumulator(exec, receiver, args, kwargs, block).reserveSlotArrays(len(arr)); err != nil {
			return NewNil(), err
		}
		// count == 0 is only reachable through the explicit-count form shift(0);
		// bare shift() defaults count to 1. Ruby's [1, 2].shift(0) returns [], so
		// report an empty array, keeping shifted typed as an array for every
		// explicit-count call rather than nil only in the zero case. Return a copy
		// of the receiver rather than wrapping its backing slice, so mutating the
		// returned array through index assignment cannot reach the source.
		remaining := make([]Value, len(arr))
		copy(remaining, arr)
		return NewHash(map[string]Value{
			"array":   NewArray(remaining),
			"shifted": NewArray([]Value{}),
		}), nil
	}
	if len(arr) == 0 {
		shifted := NewNil()
		if len(args) == 1 {
			shifted = NewArray([]Value{})
		}
		return NewHash(map[string]Value{
			"array":   NewArray([]Value{}),
			"shifted": shifted,
		}), nil
	}
	if count > len(arr) {
		count = len(arr)
	}
	remainingLen := len(arr) - count
	returnsShiftedArray := count != 1 || len(args) != 0
	if returnsShiftedArray {
		if err := newArrayBuildAccumulator(exec, receiver, args, kwargs, block).reserveSlotArrays(remainingLen, count); err != nil {
			return NewNil(), err
		}
	} else if err := newArrayBuildAccumulator(exec, receiver, args, kwargs, block).reserveSlotArrays(remainingLen); err != nil {
		return NewNil(), err
	}
	remaining := make([]Value, remainingLen)
	copy(remaining, arr[count:])
	result := map[string]Value{
		"array": NewArray(remaining),
	}
	if returnsShiftedArray {
		removed := make([]Value, count)
		copy(removed, arr[:count])
		result["shifted"] = NewArray(removed)
	} else {
		result["shifted"] = arr[0]
	}
	return NewHash(result), nil
}

// arrayDelete implements Ruby's Array#delete, removing every element equal to the
// given value. Vibescript collections are non-mutating, so it returns both the
// pruned array and the deleted value as the hash { array:, deleted: }, mirroring
// Array#pop's { array:, popped: } convention. Following Ruby, deleted is the last
// removed element itself when at least one match was removed and nil otherwise;
// reporting the stored element (rather than the search argument) lets callers
// recover a removed value that is Equal to but distinct from the argument. An
// attached block is invoked with the searched-for value on a miss and its result
// reported instead, matching `arr.delete(obj) { |o| default }`.
func arrayDelete(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("array.delete does not take keyword arguments")
	}
	if len(args) != 1 {
		return NewNil(), fmt.Errorf("array.delete expects exactly one value")
	}
	target := args[0]
	arr := receiver.Array()
	acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
	out := make([]Value, 0)
	found := false
	var matched Value
	for _, item := range arr {
		if err := exec.step(); err != nil {
			return NewNil(), err
		}
		if item.Equal(target) {
			found = true
			// Track the matched element itself so the result reports the stored
			// object rather than the caller's search argument. Ruby's Array#delete
			// returns the last deleted element, which lets callers recover or
			// mutate a removed value that is Equal to but distinct from target.
			matched = item
			continue
		}
		out = append(out, item)
		if err := acc.add(out[len(out)-1], cap(out)); err != nil {
			return NewNil(), err
		}
	}
	deleted := NewNil()
	if found {
		// Report the actual element removed (the last match), mirroring Ruby's
		// Array#delete which returns the deleted object, not the search argument.
		deleted = matched
	} else if valueBlock(block) != nil {
		// On a miss Ruby invokes the block with the searched-for value and returns
		// its result, matching `arr.delete(obj) { |o| default }`.
		runner, err := newBlockCallRunner(exec, block, "array.delete", receiver, args, kwargs)
		if err != nil {
			return NewNil(), err
		}
		result, err := runner.call([]Value{target})
		if err != nil {
			return NewNil(), err
		}
		deleted = result
	}
	return NewHash(map[string]Value{
		"array":   NewArray(out),
		"deleted": deleted,
	}), nil
}

// arrayInsert implements Ruby's Array#insert, returning a new array with the
// given values inserted before the element at index. Vibescript's collections are
// non-mutating, so it returns the new array rather than the receiver.
//
// A non-negative index inserts before that position, padding with nil when the
// index lies past the end (Ruby's [1].insert(3, "x") yields [1, nil, nil, "x"]).
// A negative index counts back from the end and inserts after that element, so
// insert(-1, x) appends and insert(-2, x) inserts before the last element,
// matching Ruby. A negative index whose magnitude exceeds the length is rejected,
// as Ruby raises IndexError for it. Inserting no values returns the array
// unchanged, mirroring Ruby.
func arrayInsert(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("array.insert does not take keyword arguments")
	}
	if len(args) == 0 {
		return NewNil(), fmt.Errorf("array.insert expects an index")
	}
	index, err := valueToInt(args[0])
	if err != nil {
		return NewNil(), fmt.Errorf("array.insert index must be integer")
	}
	if args[0].Kind() != KindInt && args[0].Kind() != KindFloat {
		return NewNil(), fmt.Errorf("array.insert index must be integer")
	}
	values := args[1:]
	arr := receiver.Array()
	if len(values) == 0 {
		return arrayInsertBuildResult(exec, receiver, args, kwargs, block, len(arr), func(i int) Value {
			return arr[i]
		})
	}
	// Resolve the insertion point. A negative index inserts after the element it
	// names, so it normalizes to (index + len + 1); Ruby rejects a negative index
	// whose magnitude exceeds the length.
	at := index
	if at < 0 {
		at += len(arr) + 1
		if at < 0 {
			return NewNil(), fmt.Errorf("array.insert index %d out of range", index)
		}
	}
	// A non-negative index past the end pads the gap with nil, growing the array
	// so the inserted values land exactly at the requested position.
	pad := 0
	if at > len(arr) {
		pad = at - len(arr)
		at = len(arr)
	}
	finalLength := saturatingAdd(saturatingAdd(len(arr), pad), len(values))
	padEnd := saturatingAdd(at, pad)
	valuesEnd := saturatingAdd(padEnd, len(values))
	return arrayInsertBuildResult(exec, receiver, args, kwargs, block, finalLength, func(i int) Value {
		switch {
		case i < at:
			return arr[i]
		case i < padEnd:
			return NewNil()
		case i < valuesEnd:
			return values[i-padEnd]
		default:
			return arr[at+i-valuesEnd]
		}
	})
}

func arrayInsertBuildResult(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value, finalLength int, valueAt func(int) Value) (Value, error) {
	acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
	if err := acc.reserveSlots(finalLength); err != nil {
		return NewNil(), err
	}

	initialCap := finalLength
	if initialCap > arrayInsertInitialCap {
		initialCap = arrayInsertInitialCap
	}
	out := make([]Value, 0, initialCap)
	for i := range finalLength {
		if err := exec.step(); err != nil {
			return NewNil(), err
		}
		out = append(out, valueAt(i))
		if err := acc.add(out[len(out)-1], cap(out)); err != nil {
			return NewNil(), err
		}
	}
	return NewArray(out), nil
}

func arraySample(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("array.sample does not take keyword arguments")
	}
	if valueBlock(block) != nil {
		return NewNil(), fmt.Errorf("array.sample does not accept a block")
	}
	if len(args) > 1 {
		return NewNil(), fmt.Errorf("array.sample accepts at most one count")
	}
	arr := receiver.Array()
	if len(args) == 0 {
		if len(arr) == 0 {
			return NewNil(), nil
		}
		idx, err := exec.randomInt64n(uint64(len(arr)))
		if err != nil {
			return NewNil(), err
		}
		return arr[int(idx)], nil
	}
	count, err := valueToCount(args[0])
	if err != nil {
		if errors.Is(err, errNegativeCount) {
			return NewNil(), fmt.Errorf("array.sample count must be non-negative")
		}
		return NewNil(), fmt.Errorf("array.sample count must be integer")
	}
	if count > len(arr) {
		count = len(arr)
	}
	scratch := sampledIndexMapBytes(count)
	delta := exec.reserveLoopScratch(scratch)
	defer exec.releaseLoopScratch(delta)
	if err := exec.checkReservedLoopScratch(receiver, args, kwargs, block); err != nil {
		return NewNil(), err
	}
	if err := newArrayBuildAccumulator(exec, receiver, args, kwargs, block).reserveSlots(count); err != nil {
		return NewNil(), err
	}
	swaps := make(map[int]int, boundedSetCap(count))
	out := make([]Value, 0, count)
	for i := range count {
		if err := exec.step(); err != nil {
			return NewNil(), err
		}
		offset, err := exec.randomInt64n(uint64(len(arr) - i))
		if err != nil {
			return NewNil(), err
		}
		j := i + int(offset)
		selected := sampledIndexAt(swaps, j)
		swaps[j] = sampledIndexAt(swaps, i)
		out = append(out, arr[selected])
	}
	return NewArray(out), nil
}

func sampledIndexAt(swaps map[int]int, index int) int {
	if swapped, ok := swaps[index]; ok {
		return swapped
	}
	return index
}

func sampledIndexMapBytes(count int) int {
	if count <= 0 {
		return 0
	}
	return saturatingAdd(estimatedMapBaseBytes, saturatingMul(count, estimatedMapEntryBytes+saturatingMul(2, estimatedIntBytes)))
}

func arrayIntScratchBytes(count int) int {
	if count <= 0 {
		return 0
	}
	return saturatingAdd(estimatedSliceBaseBytes, saturatingMul(count, estimatedIntBytes))
}

func arrayValueScratchBytes(count int) int {
	if count <= 0 {
		return 0
	}
	return saturatingAdd(estimatedSliceBaseBytes, saturatingMul(count, estimatedValueBytes))
}

func arrayShuffle(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) > 0 {
		return NewNil(), fmt.Errorf("array.shuffle does not take arguments")
	}
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("array.shuffle does not take keyword arguments")
	}
	if valueBlock(block) != nil {
		return NewNil(), fmt.Errorf("array.shuffle does not accept a block")
	}
	arr := receiver.Array()
	if err := newArrayBuildAccumulator(exec, receiver, args, kwargs, block).reserveSlots(len(arr)); err != nil {
		return NewNil(), err
	}
	out := make([]Value, len(arr))
	copy(out, arr)
	for offset := range len(out) - 1 {
		i := len(out) - 1 - offset
		if err := exec.step(); err != nil {
			return NewNil(), err
		}
		j, err := exec.randomInt64n(uint64(i + 1))
		if err != nil {
			return NewNil(), err
		}
		out[i], out[int(j)] = out[int(j)], out[i]
	}
	return NewArray(out), nil
}

func arrayRotate(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("array.rotate does not take keyword arguments")
	}
	if valueBlock(block) != nil {
		return NewNil(), fmt.Errorf("array.rotate does not accept a block")
	}
	if len(args) > 1 {
		return NewNil(), fmt.Errorf("array.rotate accepts at most one count")
	}
	offset := 1
	if len(args) == 1 {
		var err error
		offset, err = valueToInt(args[0])
		if err != nil {
			return NewNil(), fmt.Errorf("array.rotate count must be integer")
		}
	}
	return arrayRotateCopy(exec, receiver, args, kwargs, block, offset)
}

func arrayRotateCopy(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value, offset int) (Value, error) {
	arr := receiver.Array()
	if len(arr) == 0 {
		return NewArray([]Value{}), nil
	}
	acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
	if err := acc.reserveSlots(len(arr)); err != nil {
		return NewNil(), err
	}
	shift := offset % len(arr)
	if shift < 0 {
		shift += len(arr)
	}
	out := make([]Value, len(arr))
	for i := range len(arr) {
		if err := exec.step(); err != nil {
			return NewNil(), err
		}
		out[i] = arr[(shift+i)%len(arr)]
		if err := acc.add(out[i], cap(out)); err != nil {
			return NewNil(), err
		}
	}
	return NewArray(out), nil
}

func arrayProduct(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("array.product does not take keyword arguments")
	}
	if valueBlock(block) != nil {
		return NewNil(), fmt.Errorf("array.product does not accept a block")
	}
	dims := make([][]Value, 0, len(args)+1)
	dims = append(dims, receiver.Array())
	for _, arg := range args {
		if arg.Kind() != KindArray {
			return NewNil(), fmt.Errorf("array.product arguments must be arrays")
		}
		dims = append(dims, arg.Array())
	}
	count := 1
	for _, dim := range dims {
		if len(dim) == 0 {
			return NewArray([]Value{}), nil
		}
		var err error
		count, err = checkedArrayMaterializationMul("array.product", count, len(dim))
		if err != nil {
			return NewNil(), err
		}
	}
	acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
	if err := acc.reserveScratch(arrayIntScratchBytes(len(dims))); err != nil {
		return NewNil(), err
	}
	if err := acc.reserveSlots(count); err != nil {
		return NewNil(), err
	}
	out := make([]Value, 0, count)
	indices := make([]int, len(dims))
	for range count {
		if err := exec.step(); err != nil {
			return NewNil(), err
		}
		row := make([]Value, len(dims))
		for i, dim := range dims {
			row[i] = dim[indices[i]]
		}
		out = append(out, NewArray(row))
		if err := acc.add(out[len(out)-1], cap(out)); err != nil {
			return NewNil(), err
		}
		for offset := range len(indices) {
			i := len(indices) - 1 - offset
			indices[i]++
			if indices[i] < len(dims[i]) {
				break
			}
			indices[i] = 0
		}
	}
	return NewArray(out), nil
}

func arrayCombination(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	length, ok, err := arrayCombinationLength("array.combination", args, kwargs, block)
	if err != nil || !ok {
		if err != nil {
			return NewNil(), err
		}
		return NewArray([]Value{}), nil
	}
	arr := receiver.Array()
	if length > len(arr) {
		return NewArray([]Value{}), nil
	}
	count, err := combinationCount("array.combination", len(arr), length)
	if err != nil {
		return NewNil(), err
	}
	return arrayBuildCombinations(exec, receiver, args, kwargs, block, count, length, false)
}

func arrayRepeatedCombination(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	length, ok, err := arrayCombinationLength("array.repeated_combination", args, kwargs, block)
	if err != nil || !ok {
		if err != nil {
			return NewNil(), err
		}
		return NewArray([]Value{}), nil
	}
	arr := receiver.Array()
	if len(arr) == 0 && length > 0 {
		return NewArray([]Value{}), nil
	}
	count := 1
	if length > 0 {
		total := len(arr) + length - 1
		if total < len(arr) {
			return NewNil(), guardLimitErrorf("array.repeated_combination result too large")
		}
		count, err = combinationCount("array.repeated_combination", total, length)
		if err != nil {
			return NewNil(), err
		}
	}
	return arrayBuildCombinations(exec, receiver, args, kwargs, block, count, length, true)
}

func arrayPermutation(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	length, ok, err := arrayPermutationLength("array.permutation", receiver, args, kwargs, block)
	if err != nil || !ok {
		if err != nil {
			return NewNil(), err
		}
		return NewArray([]Value{}), nil
	}
	arr := receiver.Array()
	if length > len(arr) {
		return NewArray([]Value{}), nil
	}
	count := 1
	for offset := range length {
		factor := len(arr) - length + 1 + offset
		count, err = checkedArrayMaterializationMul("array.permutation", count, factor)
		if err != nil {
			return NewNil(), err
		}
	}
	return arrayBuildPermutations(exec, receiver, args, kwargs, block, count, length, false)
}

func arrayRepeatedPermutation(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	length, ok, err := arrayCombinationLength("array.repeated_permutation", args, kwargs, block)
	if err != nil || !ok {
		if err != nil {
			return NewNil(), err
		}
		return NewArray([]Value{}), nil
	}
	arr := receiver.Array()
	if len(arr) == 0 && length > 0 {
		return NewArray([]Value{}), nil
	}
	count := 1
	for range length {
		count, err = checkedArrayMaterializationMul("array.repeated_permutation", count, len(arr))
		if err != nil {
			return NewNil(), err
		}
	}
	return arrayBuildPermutations(exec, receiver, args, kwargs, block, count, length, true)
}

func arrayCombinationLength(method string, args []Value, kwargs map[string]Value, block Value) (int, bool, error) {
	if len(kwargs) > 0 {
		return 0, false, fmt.Errorf("%s does not take keyword arguments", method)
	}
	if valueBlock(block) != nil {
		return 0, false, fmt.Errorf("%s does not accept a block", method)
	}
	if len(args) != 1 {
		return 0, false, fmt.Errorf("%s expects exactly one length", method)
	}
	length, err := valueToInt(args[0])
	if err != nil {
		return 0, false, fmt.Errorf("%s length must be integer", method)
	}
	if length < 0 {
		return 0, false, nil
	}
	return length, true, nil
}

func arrayPermutationLength(method string, receiver Value, args []Value, kwargs map[string]Value, block Value) (int, bool, error) {
	if len(args) == 0 {
		if len(kwargs) > 0 {
			return 0, false, fmt.Errorf("%s does not take keyword arguments", method)
		}
		if valueBlock(block) != nil {
			return 0, false, fmt.Errorf("%s does not accept a block", method)
		}
		return len(receiver.Array()), true, nil
	}
	return arrayCombinationLength(method, args, kwargs, block)
}

func arrayBuildCombinations(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value, count, length int, repeated bool) (Value, error) {
	arr := receiver.Array()
	acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
	if err := acc.reserveScratch(arrayIntScratchBytes(length)); err != nil {
		return NewNil(), err
	}
	if err := acc.reserveSlots(count); err != nil {
		return NewNil(), err
	}
	out := make([]Value, 0, count)
	indices := make([]int, length)
	for i := range length {
		indices[i] = i
	}
	if repeated {
		for i := range length {
			indices[i] = 0
		}
	}
	for emitted := range count {
		if err := exec.step(); err != nil {
			return NewNil(), err
		}
		row := make([]Value, length)
		for i, idx := range indices {
			row[i] = arr[idx]
		}
		out = append(out, NewArray(row))
		if err := acc.add(out[len(out)-1], cap(out)); err != nil {
			return NewNil(), err
		}
		if emitted == count-1 || length == 0 {
			continue
		}
		if repeated {
			advanceRepeatedCombination(indices, len(arr))
		} else {
			advanceCombination(indices, len(arr))
		}
	}
	return NewArray(out), nil
}

func advanceCombination(indices []int, n int) {
	k := len(indices)
	for offset := range k {
		i := k - 1 - offset
		if indices[i] != i+n-k {
			indices[i]++
			for j := i + 1; j < k; j++ {
				indices[j] = indices[j-1] + 1
			}
			return
		}
	}
}

func advanceRepeatedCombination(indices []int, n int) {
	for offset := range len(indices) {
		i := len(indices) - 1 - offset
		if indices[i] < n-1 {
			next := indices[i] + 1
			for j := i; j < len(indices); j++ {
				indices[j] = next
			}
			return
		}
	}
}

func arrayBuildPermutations(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value, count, length int, repeated bool) (Value, error) {
	arr := receiver.Array()
	acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
	scratch := arrayIntScratchBytes(length)
	if !repeated {
		scratch = saturatingAdd(arrayValueScratchBytes(length), arrayValueScratchBytes(len(arr)))
	}
	if err := acc.reserveScratch(scratch); err != nil {
		return NewNil(), err
	}
	if err := acc.reserveSlots(count); err != nil {
		return NewNil(), err
	}
	out := make([]Value, 0, count)
	if repeated {
		indices := make([]int, length)
		for range count {
			if err := exec.step(); err != nil {
				return NewNil(), err
			}
			row := make([]Value, length)
			for i, idx := range indices {
				row[i] = arr[idx]
			}
			out = append(out, NewArray(row))
			if err := acc.add(out[len(out)-1], cap(out)); err != nil {
				return NewNil(), err
			}
			for offset := range len(indices) {
				i := len(indices) - 1 - offset
				indices[i]++
				if indices[i] < len(arr) {
					break
				}
				indices[i] = 0
			}
		}
		return NewArray(out), nil
	}
	used := make([]bool, len(arr))
	row := make([]Value, length)
	var visit func(int) error
	visit = func(depth int) error {
		if depth == length {
			if err := exec.step(); err != nil {
				return err
			}
			copyRow := make([]Value, length)
			copy(copyRow, row)
			out = append(out, NewArray(copyRow))
			return acc.add(out[len(out)-1], cap(out))
		}
		for i, item := range arr {
			if used[i] {
				continue
			}
			used[i] = true
			row[depth] = item
			if err := visit(depth + 1); err != nil {
				return err
			}
			used[i] = false
		}
		return nil
	}
	if err := visit(0); err != nil {
		return NewNil(), err
	}
	return NewArray(out), nil
}

func combinationCount(method string, n, k int) (int, error) {
	if k < 0 || k > n {
		return 0, nil
	}
	if k > n-k {
		k = n - k
	}
	count := 1
	for offset := range k {
		i := offset + 1
		numerator := n - k + i
		denominator := i
		g := gcdInt(numerator, denominator)
		numerator /= g
		denominator /= g
		g = gcdInt(count, denominator)
		count /= g
		denominator /= g
		if denominator != 1 {
			return 0, guardLimitErrorf("%s result too large", method)
		}
		var err error
		count, err = checkedArrayMaterializationMul(method, count, numerator)
		if err != nil {
			return 0, err
		}
	}
	return count, nil
}

func checkedArrayMaterializationMul(method string, left, right int) (int, error) {
	if right != 0 && left > math.MaxInt/right {
		return 0, guardLimitErrorf("%s result too large", method)
	}
	return left * right, nil
}

func gcdInt(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func arrayReverseCopy(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value, method string) (Value, error) {
	if len(args) > 0 {
		return NewNil(), fmt.Errorf("%s does not take arguments", method)
	}
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("%s does not take keyword arguments", method)
	}
	if valueBlock(block) != nil {
		return NewNil(), fmt.Errorf("%s does not accept a block", method)
	}
	arr := receiver.Array()
	acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
	if err := acc.reserveSlots(len(arr)); err != nil {
		return NewNil(), err
	}
	out := make([]Value, len(arr))
	for i, item := range arr {
		if err := exec.step(); err != nil {
			return NewNil(), err
		}
		out[len(arr)-1-i] = item
		if err := acc.add(item, cap(out)); err != nil {
			return NewNil(), err
		}
	}
	return NewArray(out), nil
}

func arraySortBang(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) > 0 {
		return NewNil(), fmt.Errorf("array.sort! does not take arguments")
	}
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("array.sort! does not take keyword arguments")
	}
	arr := receiver.Array()
	if err := newArrayBuildAccumulator(exec, receiver, args, kwargs, block).reserveSlots(len(arr)); err != nil {
		return NewNil(), err
	}
	out := make([]Value, len(arr))
	copy(out, arr)
	var runner *blockCallRunner
	if valueBlock(block) != nil {
		var err error
		runner, err = newBlockCallRunner(exec, block, "array.sort!", receiver, nil, kwargs)
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
				sortErr = fmt.Errorf("array.sort! block must return numeric comparator")
				return false
			}
			return cmp < 0
		}
		cmp, err := arraySortCompareValues(out[i], out[j])
		if err != nil {
			sortErr = fmt.Errorf("array.sort! values are not comparable")
			return false
		}
		return cmp < 0
	})
	if sortErr != nil {
		return NewNil(), sortErr
	}
	return NewArray(out), nil
}

func arrayMapBang(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) > 0 {
		return NewNil(), fmt.Errorf("array.map! does not take arguments")
	}
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("array.map! does not take keyword arguments")
	}
	runner, err := newBlockCallRunner(exec, block, "array.map!", receiver, nil, kwargs)
	if err != nil {
		return NewNil(), err
	}
	arr := receiver.Array()
	acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
	if err := acc.reserveSlots(len(arr)); err != nil {
		return NewNil(), err
	}
	out := make([]Value, 0, len(arr))
	var blockArg [1]Value
	for _, item := range arr {
		if err := exec.step(); err != nil {
			return NewNil(), err
		}
		blockArg[0] = item
		val, err := runner.call(blockArg[:])
		if err != nil {
			return NewNil(), err
		}
		out = append(out, val)
		if err := acc.addConservative(val, cap(out)); err != nil {
			return NewNil(), err
		}
	}
	return NewArray(out), nil
}

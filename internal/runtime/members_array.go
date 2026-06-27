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
	"take_while", "drop_while", "grep", "grep_v",
	"push", "append", "prepend", "pop", "uniq", "first", "last", "sum", "compact", "flatten", "fill", "chunk", "window", "join", "reverse",
	"take", "drop", "zip", "transpose", "union", "difference",
	"sort", "sort_by", "partition", "group_by", "group_by_stable", "tally",
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
		"take_while", "drop_while", "grep", "grep_v":
		return arrayMemberQuery(property)
	case "push", "append", "prepend", "pop", "uniq", "first", "last", "sum", "compact", "flatten", "fill", "chunk", "window", "join", "reverse", "take", "drop", "zip", "transpose", "union", "difference":
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
	case "each_with_index":
		return NewAutoBuiltin("array.each_with_index", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.each_with_index does not take arguments")
			}
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("array.each_with_index does not take keyword arguments")
			}
			runner, err := newBlockCallRunner(exec, block, "array.each_with_index")
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
	case "map_with_index":
		return NewAutoBuiltin("array.map_with_index", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.map_with_index does not take arguments")
			}
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("array.map_with_index does not take keyword arguments")
			}
			runner, err := newBlockCallRunner(exec, block, "array.map_with_index")
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
				if err := acc.add(val, cap(out)); err != nil {
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
			runner, err := newBlockCallRunner(exec, block, "array.filter_map")
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
					if err := acc.add(val, cap(out)); err != nil {
						return NewNil(), err
					}
				}
			}
			return NewArray(out), nil
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
				runner, err = newBlockCallRunner(exec, block, "array.one?")
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
			return fmt.Errorf("array.values_at window is too large")
		}
		span++
	}
	if span == 0 {
		return nil
	}
	if span > math.MaxInt {
		return fmt.Errorf("array.values_at window is too large")
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
		runner, err := newBlockCallRunner(exec, block, name)
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
		runner, err := newBlockCallRunner(exec, block, name)
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
		runner, err := newBlockCallRunner(exec, block, name)
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
			sub, ok := arraySliceRange(arr, args[0].Range())
			if !ok {
				return NewNil(), nil
			}
			return NewArray(sub), nil
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
		sub, ok := arraySliceStartLength(arr, start, length)
		if !ok {
			return NewNil(), nil
		}
		return NewArray(sub), nil
	default:
		return NewNil(), fmt.Errorf("array.slice expects an index, a start and length, or a range")
	}
}

// arraySliceStartLength extracts at most length elements starting at start,
// matching Ruby's Array#slice(start, length). A negative start counts back from
// the end. It returns ok=false when length is negative or when start lands
// outside the array; a start exactly equal to the length is in range and yields
// an empty array (Ruby's [1, 2, 3].slice(3, n) is []). The length is clamped to
// the remaining elements, so an oversized length returns the suffix from start.
// Clamping length to the remaining count before computing end keeps start+length
// from overflowing int when length is near math.MaxInt64, which would otherwise
// wrap to a negative window and panic make. The returned slice is a fresh copy
// so it never aliases the receiver's backing array.
func arraySliceStartLength(arr []Value, start, length int) ([]Value, bool) {
	if length < 0 {
		return nil, false
	}
	if start < 0 {
		start += len(arr)
		if start < 0 {
			return nil, false
		}
	}
	if start > len(arr) {
		return nil, false
	}
	remaining := len(arr) - start
	if length > remaining {
		length = remaining
	}
	out := make([]Value, length)
	copy(out, arr[start:start+length])
	return out, true
}

// arraySliceRange extracts the elements selected by a range, matching Ruby's
// Array#slice(range). Negative bounds count back from the end. A begin bound
// before the start of the array (after normalization) or past its length returns
// ok=false (nil); a begin exactly at the length yields an empty array. The end
// bound is clamped to the array length, and an end before begin yields an empty
// array. Bound arithmetic stays in int64 so a near-MaxInt64 inclusive range
// cannot silently overflow into a no-op. The returned slice is a fresh copy so
// it never aliases the receiver's backing array.
func arraySliceRange(arr []Value, rng Range) ([]Value, bool) {
	length := int64(len(arr))
	begin := rng.Start
	if begin < 0 {
		begin += length
	}
	if begin < 0 || begin > length {
		return nil, false
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
	out := make([]Value, end-begin)
	copy(out, arr[begin:end])
	return out, true
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
// acceptance of `reduce(:+)` and `reduce("+")`. Note that operator-symbol
// literals such as `:+` cannot yet be written in source because the lexer does
// not tokenize them (tracked in #801); reach the operator path with the string
// form (`reduce("+")`) or a symbol naming a method (`reduce(:concat)`).
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
		runner, err = newBlockCallRunner(exec, block, "array.reduce")
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
		next, err := runner.call(blockArgs[:])
		if err != nil {
			return NewNil(), err
		}
		acc = next
	}
	return acc, nil
}

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

// arrayFillInitialCap bounds the capacity reserved up front when building a
// fill result. Larger fills grow the backing array via append so the per-element
// step() and arrayBuildAccumulator checks bound the actual allocation, rather
// than reserving the full requested length immediately. It mirrors
// rangeMaterializeInitialCap.
const arrayFillInitialCap = 4096

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
		runner, err = newBlockCallRunner(exec, block, "array.fill")
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

	appendValue := func(val Value) error {
		out = append(out, val)
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
			if err := appendValue(val); err != nil {
				return NewNil(), err
			}
		case i < len(arr):
			// Outside the window but within the receiver: copy the original
			// element unchanged (the prefix before the window or the tail after
			// it).
			if err := appendValue(arr[i]); err != nil {
				return NewNil(), err
			}
		default:
			// Past the receiver's end but before the window: pad the gap with
			// nil, the value Ruby inserts when fill grows the array past its old
			// length.
			if err := appendValue(NewNil()); err != nil {
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
	case "prepend":
		// Ruby's Array#prepend (alias unshift) inserts the arguments, in order,
		// at the front of the array. Vibescript's collections are non-mutating,
		// so this returns a new array.
		return NewAutoBuiltin("array.prepend", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("array.prepend does not take keyword arguments")
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

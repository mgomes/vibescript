package runtime

import "fmt"

// rangeEachInt walks the range's integer iteration sequence in the same order a
// `for` loop would: ascending when Start <= End, descending otherwise, honoring
// the exclusive end. A step is charged per element so an empty or trivial block
// body cannot starve the step quota or cancellation checks while traversing a
// large range. The callback may return stop=true to end iteration early (used by
// find). Iteration is overflow safe: the final element is yielded and the loop
// breaks before the counter would wrap past the int64 boundary, so an inclusive
// range ending at MaxInt64 (or starting at MinInt64 descending) terminates
// rather than looping forever.
func (exec *Execution) rangeEachInt(rng Range, fn func(value int64) (stop bool, err error)) error {
	// An exclusive range whose endpoints coincide yields nothing; every other
	// range yields at least its start.
	if rng.Exclusive && rng.Start == rng.End {
		return nil
	}

	ascending := rng.Start <= rng.End
	last := rangeLastElement(rng)
	current := rng.Start
	for {
		if err := exec.step(); err != nil {
			return err
		}
		stop, err := fn(current)
		if err != nil {
			return err
		}
		if stop || current == last {
			return nil
		}
		if ascending {
			current++
		} else {
			current--
		}
	}
}

// rangeMemberEach builds Range#each: it yields each integer in iteration order
// and returns the receiver, matching Ruby's Range#each.
func rangeMemberEach() Value {
	return NewAutoBuiltin("range.each", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(args) > 0 {
			return NewNil(), fmt.Errorf("range.each does not take arguments")
		}
		runner, err := newBlockCallRunner(exec, block, "range.each")
		if err != nil {
			return NewNil(), err
		}
		var blockArg [1]Value
		err = exec.rangeEachInt(receiver.Range(), func(value int64) (bool, error) {
			blockArg[0] = NewInt(value)
			if _, callErr := runner.call(blockArg[:]); callErr != nil {
				return false, callErr
			}
			return false, nil
		})
		if err != nil {
			return NewNil(), err
		}
		return receiver, nil
	})
}

// rangeMemberEachWithIndex builds Range#each_with_index: it yields each integer
// together with its zero-based iteration index and returns the receiver,
// matching Ruby's Enumerable#each_with_index.
func rangeMemberEachWithIndex() Value {
	return NewAutoBuiltin("range.each_with_index", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(args) > 0 {
			return NewNil(), fmt.Errorf("range.each_with_index does not take arguments")
		}
		runner, err := newBlockCallRunner(exec, block, "range.each_with_index")
		if err != nil {
			return NewNil(), err
		}
		index := int64(0)
		var blockArgs [2]Value
		err = exec.rangeEachInt(receiver.Range(), func(value int64) (bool, error) {
			blockArgs[0] = NewInt(value)
			blockArgs[1] = NewInt(index)
			if _, callErr := runner.call(blockArgs[:]); callErr != nil {
				return false, callErr
			}
			index++
			return false, nil
		})
		if err != nil {
			return NewNil(), err
		}
		return receiver, nil
	})
}

// rangeMemberMap builds Range#map: it yields each integer and collects the block
// results into a new array, matching Ruby's Enumerable#map. The accumulated
// payloads are charged against the memory quota as the result grows because the
// block may return arbitrarily large values per element.
func rangeMemberMap() Value {
	return NewAutoBuiltin("range.map", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(args) > 0 {
			return NewNil(), fmt.Errorf("range.map does not take arguments")
		}
		runner, err := newBlockCallRunner(exec, block, "range.map")
		if err != nil {
			return NewNil(), err
		}
		// The result holds arbitrary block values whose payloads cannot be bounded
		// up front, so charge them incrementally like Array#map/filter_map rather
		// than the int-only cap projection select/reject can use. The local slice
		// is invisible to exec's roots while it is built, so without this a block
		// returning a quota-sized value per element could pile up far beyond the
		// memory quota before any post-call check ran.
		acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
		out := make([]Value, 0, rangeMapInitialCap)
		var blockArg [1]Value
		err = exec.rangeEachInt(receiver.Range(), func(value int64) (bool, error) {
			blockArg[0] = NewInt(value)
			result, callErr := runner.call(blockArg[:])
			if callErr != nil {
				return false, callErr
			}
			out = append(out, result)
			if addErr := acc.add(result, cap(out)); addErr != nil {
				return false, addErr
			}
			return false, nil
		})
		if err != nil {
			return NewNil(), err
		}
		return NewArray(out), nil
	})
}

// rangeMapInitialCap is the modest capacity Range#map reserves up front. A range
// has no cheap, overflow-safe element count to size the result by, and reserving
// a large capacity would let append grow and then drop transient backing storage
// the per-element accumulator never charges. Seeding a small capacity keeps the
// peak backing allocation proportional to the elements actually produced.
const rangeMapInitialCap = 16

// rangeMemberFilter builds Range#select and Range#reject. Both yield each integer
// and keep the ones whose block result is (select) or is not (reject) truthy,
// returning an array of the kept integers. Because every kept element is an
// inlined integer, the result memory is bounded with the same O(1) cap-based
// projection range materialization uses.
func rangeMemberFilter(property string) Value {
	keepTruthy := property == "select"
	name := "range." + property
	return NewAutoBuiltin(name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(args) > 0 {
			return NewNil(), fmt.Errorf("%s does not take arguments", name)
		}
		runner, err := newBlockCallRunner(exec, block, name)
		if err != nil {
			return NewNil(), err
		}
		out := make([]Value, 0, rangeMapInitialCap)
		var blockArg [1]Value
		err = exec.rangeEachInt(receiver.Range(), func(value int64) (bool, error) {
			blockArg[0] = NewInt(value)
			result, callErr := runner.call(blockArg[:])
			if callErr != nil {
				return false, callErr
			}
			if result.Truthy() == keepTruthy {
				out = append(out, NewInt(value))
				if memErr := exec.checkProjectedIntArrayBytes(cap(out)); memErr != nil {
					return false, memErr
				}
			}
			return false, nil
		})
		if err != nil {
			return NewNil(), err
		}
		return NewArray(out), nil
	})
}

// rangeMemberFind builds Range#find: it yields each integer and returns the first
// one whose block result is truthy, or nil when none match, matching Ruby's
// Enumerable#find/detect.
func rangeMemberFind() Value {
	return NewAutoBuiltin("range.find", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(args) > 0 {
			return NewNil(), fmt.Errorf("range.find does not take arguments")
		}
		runner, err := newBlockCallRunner(exec, block, "range.find")
		if err != nil {
			return NewNil(), err
		}
		found := NewNil()
		var blockArg [1]Value
		err = exec.rangeEachInt(receiver.Range(), func(value int64) (bool, error) {
			blockArg[0] = NewInt(value)
			result, callErr := runner.call(blockArg[:])
			if callErr != nil {
				return false, callErr
			}
			if result.Truthy() {
				found = NewInt(value)
				return true, nil
			}
			return false, nil
		})
		if err != nil {
			return NewNil(), err
		}
		return found, nil
	})
}

// rangeMemberReduce builds Range#reduce. It mirrors Array#reduce's argument
// shapes (an initial value and/or an operation symbol, with or without a block)
// but folds over the range's integer sequence lazily so a huge range never
// materializes. Only the single accumulator is retained, charged against the
// memory quota each step in the operation form exactly as Array#reduce does.
func rangeMemberReduce() Value {
	return NewAutoBuiltin("range.reduce", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(kwargs) > 0 {
			return NewNil(), fmt.Errorf("range.reduce does not take keyword arguments")
		}
		if len(args) > 2 {
			return NewNil(), fmt.Errorf("range.reduce accepts at most an initial value and an operation")
		}

		hasBlock := valueBlock(block) != nil
		var initial Value
		hasInitial := false
		operation := ""
		hasOperation := false

		switch {
		case len(args) == 2:
			op, ok := reduceOperationName(args[1])
			if !ok {
				return NewNil(), fmt.Errorf("range.reduce operation must be a symbol or string")
			}
			initial, hasInitial = args[0], true
			operation, hasOperation = op, true
		case len(args) == 1 && hasBlock:
			initial, hasInitial = args[0], true
		case len(args) == 1:
			op, ok := reduceOperationName(args[0])
			if !ok {
				return NewNil(), fmt.Errorf("range.reduce operation must be a symbol or string")
			}
			operation, hasOperation = op, true
		case !hasBlock:
			return NewNil(), fmt.Errorf("range.reduce requires a block or an operation")
		}

		var runner *blockCallRunner
		if hasBlock {
			r, err := newBlockCallRunner(exec, block, "range.reduce")
			if err != nil {
				return NewNil(), err
			}
			runner = r
		}

		acc := initial
		seeded := hasInitial
		var blockArgs [2]Value
		err := exec.rangeEachInt(receiver.Range(), func(value int64) (bool, error) {
			element := NewInt(value)
			if !seeded {
				acc = element
				seeded = true
				return false, nil
			}
			if hasOperation {
				next, opErr := exec.reduceSendOperation(acc, operation, element)
				if opErr != nil {
					return false, opErr
				}
				if memErr := exec.checkMemoryWith(next); memErr != nil {
					return false, memErr
				}
				acc = next
				return false, nil
			}
			blockArgs[0] = acc
			blockArgs[1] = element
			next, callErr := runner.call(blockArgs[:])
			if callErr != nil {
				return false, callErr
			}
			acc = next
			return false, nil
		})
		if err != nil {
			return NewNil(), err
		}
		if !seeded {
			// An empty range with no initial value folds to nil, matching Ruby's
			// `(1...1).reduce(:+)`.
			return NewNil(), nil
		}
		return acc, nil
	})
}

// rangeMemberSum builds Range#sum. With no block it adds the range's integers to
// an optional initial value (default 0); with a block it adds each block result.
// A step is charged per element by rangeEachInt, and the running total is a
// single value, so the fold stays bounded over a huge range.
func rangeMemberSum() Value {
	return NewAutoBuiltin("range.sum", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(args) > 1 {
			return NewNil(), fmt.Errorf("range.sum accepts at most an initial value")
		}
		if len(kwargs) > 0 {
			return NewNil(), fmt.Errorf("range.sum does not take keyword arguments")
		}
		total := NewInt(0)
		if len(args) == 1 {
			if !isNumericValue(args[0]) {
				return NewNil(), fmt.Errorf("range.sum initial value must be numeric")
			}
			total = args[0]
		}

		var runner *blockCallRunner
		if valueBlock(block) != nil {
			r, err := newBlockCallRunner(exec, block, "range.sum")
			if err != nil {
				return NewNil(), err
			}
			runner = r
		}

		var blockArg [1]Value
		err := exec.rangeEachInt(receiver.Range(), func(value int64) (bool, error) {
			addend := NewInt(value)
			if runner != nil {
				blockArg[0] = NewInt(value)
				result, callErr := runner.call(blockArg[:])
				if callErr != nil {
					return false, callErr
				}
				if !isNumericValue(result) {
					return false, fmt.Errorf("range.sum block must return a numeric value")
				}
				addend = result
			}
			next, addErr := addValues(total, addend)
			if addErr != nil {
				return false, fmt.Errorf("range.sum supports numeric values")
			}
			total = next
			return false, nil
		})
		if err != nil {
			return NewNil(), err
		}
		return total, nil
	})
}

// rangeMemberCount builds Range#count. With no argument it returns the number of
// integers the range iterates over. With a value argument it counts matching
// elements; with a block it counts elements whose block result is truthy. A
// value argument takes precedence over an attached block, matching Ruby's
// Enumerable#count(item) { ... }.
func rangeMemberCount() Value {
	return NewAutoBuiltin("range.count", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(args) > 1 {
			return NewNil(), fmt.Errorf("range.count accepts at most one value argument")
		}
		rng := receiver.Range()

		if len(args) == 1 {
			// Match against an integer target by walking the sequence; a non-integer
			// target can never equal an element, so it counts zero without iterating.
			if args[0].Kind() != KindInt {
				return NewInt(0), nil
			}
			target := args[0].Int()
			total := int64(0)
			err := exec.rangeEachInt(rng, func(value int64) (bool, error) {
				if value == target {
					total++
				}
				return false, nil
			})
			if err != nil {
				return NewNil(), err
			}
			return NewInt(total), nil
		}

		if valueBlock(block) == nil {
			length, overflow := rangeLength(rng)
			if overflow {
				return NewNil(), fmt.Errorf("range.count overflow")
			}
			return NewInt(length), nil
		}

		runner, err := newBlockCallRunner(exec, block, "range.count")
		if err != nil {
			return NewNil(), err
		}
		total := int64(0)
		var blockArg [1]Value
		err = exec.rangeEachInt(rng, func(value int64) (bool, error) {
			blockArg[0] = NewInt(value)
			result, callErr := runner.call(blockArg[:])
			if callErr != nil {
				return false, callErr
			}
			if result.Truthy() {
				total++
			}
			return false, nil
		})
		if err != nil {
			return NewNil(), err
		}
		return NewInt(total), nil
	})
}

// rangeMemberStep builds Range#step(n): it yields the range's start and every nth
// integer thereafter, in iteration order, returning the receiver. The stride
// must be a positive integer (Ruby raises ArgumentError for a zero or negative
// step on an integer range), and it is applied in the range's iteration
// direction so a descending range steps downward.
func rangeMemberStep() Value {
	return NewAutoBuiltin("range.step", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(args) != 1 {
			return NewNil(), fmt.Errorf("range.step expects a step size")
		}
		if args[0].Kind() != KindInt {
			return NewNil(), fmt.Errorf("range.step expects an integer step")
		}
		stride := args[0].Int()
		if stride <= 0 {
			return NewNil(), fmt.Errorf("range.step step must be positive")
		}
		runner, err := newBlockCallRunner(exec, block, "range.step")
		if err != nil {
			return NewNil(), err
		}
		// position counts iteration order; only every stride-th element is yielded.
		position := int64(0)
		var blockArg [1]Value
		err = exec.rangeEachInt(receiver.Range(), func(value int64) (bool, error) {
			if position%stride == 0 {
				blockArg[0] = NewInt(value)
				if _, callErr := runner.call(blockArg[:]); callErr != nil {
					return false, callErr
				}
			}
			position++
			return false, nil
		})
		if err != nil {
			return NewNil(), err
		}
		return receiver, nil
	})
}

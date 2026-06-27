package runtime

import (
	"fmt"
	"math"
)

// rangeMaterializeInitialCap bounds the capacity reserved up front when
// materializing a range. Larger materializations grow the backing array via
// append so the per-element step() and checkMemoryWith calls bound the actual
// allocation, rather than reserving the full requested count immediately.
const rangeMaterializeInitialCap = 4096

// rangeBuildInitialCap bounds the capacity reserved up front when a range
// Enumerable helper (map, select, reject) accumulates a result. The per-element
// arrayBuildAccumulator.add and step() calls bound the actual allocation, so the
// backing array grows via append rather than reserving the full span at once.
const rangeBuildInitialCap = 64

// rangeMemberNames mirrors the names dispatched by rangeMember and feeds
// "did you mean" suggestions on the error path. Keep it in sync with the
// switch below; TestMemberSuggestionCandidatesResolve enforces that every
// listed name resolves.
var rangeMemberNames = []string{
	"cover?", "include?", "member?", "first", "last", "size", "exclude_end?", "to_a",
	"each", "step", "map", "select", "reject", "find", "reduce", "count", "sum", "min", "max",
}

func (exec *Execution) rangeMember(obj Value, property string, pos Position) (Value, error) {
	switch property {
	case "cover?", "include?", "member?":
		return rangeMemberPredicate(property), nil
	case "first":
		return rangeMemberFirst(), nil
	case "last":
		return rangeMemberLast(), nil
	case "size":
		return rangeMemberSize(), nil
	case "exclude_end?":
		return rangeMemberExcludeEnd(), nil
	case "to_a":
		return rangeMemberToArray(), nil
	case "each":
		return rangeMemberEach(), nil
	case "step":
		return rangeMemberStep(), nil
	case "map":
		return rangeMemberMap(), nil
	case "select", "reject":
		return rangeMemberFilter(property), nil
	case "find":
		return rangeMemberFind(), nil
	case "reduce":
		return rangeMemberReduce(), nil
	case "count":
		return rangeMemberCount(), nil
	case "sum":
		return rangeMemberSum(), nil
	case "min", "max":
		return rangeMemberMinMax(property), nil
	default:
		return NewNil(), exec.errorAt(pos, "unknown range method %s%s", property, didYouMean(property, rangeMemberNames))
	}
}

// rangeForEach walks the range's iteration sequence in order — ascending when
// Start <= End, descending otherwise — invoking yield for each integer. It
// charges a step per element so a wide span is bounded by the sandbox step
// quota and honors context cancellation, mirroring the for-loop's range
// iteration. yield may return stop=true to end iteration early (for find and
// similar short-circuiting helpers). The terminal value is detected before the
// next increment so a span ending at MaxInt64/MinInt64 stops cleanly rather
// than wrapping around.
func (exec *Execution) rangeForEach(rng Range, yield func(value int64) (stop bool, err error)) error {
	ascending := rng.Start <= rng.End
	current := rng.Start
	for {
		if ascending {
			if !rangeLoopAscendingContinues(current, rng) {
				return nil
			}
		} else if !rangeLoopDescendingContinues(current, rng) {
			return nil
		}
		if err := exec.step(); err != nil {
			return err
		}
		stop, err := yield(current)
		if err != nil {
			return err
		}
		if stop {
			return nil
		}
		// The endpoints are int64, so an inclusive span reaching MaxInt64 (or a
		// descending one reaching MinInt64) would wrap on the next increment.
		// Detect the terminal value first and stop before incrementing.
		if ascending {
			if current == math.MaxInt64 {
				return nil
			}
			current++
		} else {
			if current == math.MinInt64 {
				return nil
			}
			current--
		}
	}
}

// rangeMemberEach yields each integer in the range to the block and returns the
// range, mirroring Array#each and Ruby's Range#each.
func rangeMemberEach() Value {
	return NewAutoBuiltin("range.each", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(args) > 0 {
			return NewNil(), fmt.Errorf("range.each does not take arguments")
		}
		if len(kwargs) > 0 {
			return NewNil(), fmt.Errorf("range.each does not take keyword arguments")
		}
		runner, err := newBlockCallRunner(exec, block, "range.each")
		if err != nil {
			return NewNil(), err
		}
		var blockArg [1]Value
		err = exec.rangeForEach(receiver.Range(), func(value int64) (bool, error) {
			blockArg[0] = NewInt(value)
			if _, err := runner.call(blockArg[:]); err != nil {
				return false, err
			}
			return false, nil
		})
		if err != nil {
			return NewNil(), err
		}
		return receiver, nil
	})
}

// rangeMemberStep yields every nth integer in the range to the block, starting
// at the range's start, and returns the range. The step must be a positive
// integer, matching Ruby's ArgumentError on a zero or negative Range#step.
//
// Iteration advances the current value by the stride directly rather than
// visiting every intermediate integer, so a sparse step over a wide span only
// charges the sandbox step quota for the values it actually yields. This
// mirrors Integer#step and keeps `(1..1_000_000).step(1_000_000)` usable. The
// stride is applied in the range's iteration direction — ascending when
// Start <= End, descending otherwise — and the next value is computed with
// checked arithmetic so a span reaching MaxInt64/MinInt64 stops cleanly rather
// than wrapping around.
func rangeMemberStep() Value {
	return NewAutoBuiltin("range.step", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(args) != 1 {
			return NewNil(), fmt.Errorf("range.step expects one integer argument")
		}
		if len(kwargs) > 0 {
			return NewNil(), fmt.Errorf("range.step does not take keyword arguments")
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
		rng := receiver.Range()
		ascending := rng.Start <= rng.End
		// Descending ranges advance by the stride in the negative direction; the
		// stride magnitude is identical, so the signed delta is just negated.
		delta := stride
		if !ascending {
			delta = -stride
		}
		current := rng.Start
		var blockArg [1]Value
		for {
			if ascending {
				if !rangeLoopAscendingContinues(current, rng) {
					break
				}
			} else if !rangeLoopDescendingContinues(current, rng) {
				break
			}
			if err := exec.step(); err != nil {
				return NewNil(), err
			}
			blockArg[0] = NewInt(current)
			if _, err := runner.call(blockArg[:]); err != nil {
				return NewNil(), err
			}
			next, ok := addInt64Checked(current, delta)
			if !ok {
				// The next value would wrap past the int64 range, so no further value
				// can be in bounds; stop cleanly rather than wrapping around.
				break
			}
			current = next
		}
		return receiver, nil
	})
}

// rangeMemberMap builds an array of the block's result for each integer in the
// range, mirroring Array#map. The growing result is charged against the memory
// quota per element so a wide range cannot accumulate an unbounded array, and
// the block may return arbitrary values whose payloads are accounted for.
func rangeMemberMap() Value {
	return NewAutoBuiltin("range.map", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(args) > 0 {
			return NewNil(), fmt.Errorf("range.map does not take arguments")
		}
		if len(kwargs) > 0 {
			return NewNil(), fmt.Errorf("range.map does not take keyword arguments")
		}
		runner, err := newBlockCallRunner(exec, block, "range.map")
		if err != nil {
			return NewNil(), err
		}
		acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
		out := make([]Value, 0, rangeBuildInitialCap)
		var blockArg [1]Value
		err = exec.rangeForEach(receiver.Range(), func(value int64) (bool, error) {
			blockArg[0] = NewInt(value)
			val, err := runner.call(blockArg[:])
			if err != nil {
				return false, err
			}
			out = append(out, val)
			if err := acc.add(val, cap(out)); err != nil {
				return false, err
			}
			return false, nil
		})
		if err != nil {
			return NewNil(), err
		}
		return NewArray(out), nil
	})
}

// rangeMemberFilter implements select (keep truthy) and reject (keep falsy):
// it tests each integer in the range with the block and collects the integers
// the predicate keeps into an array. Kept elements are inlined ints, charged
// against the memory quota per element via the backing capacity.
func rangeMemberFilter(property string) Value {
	keepTruthy := property == "select"
	name := "range." + property
	return NewAutoBuiltin(name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(args) > 0 {
			return NewNil(), fmt.Errorf("%s does not take arguments", name)
		}
		if len(kwargs) > 0 {
			return NewNil(), fmt.Errorf("%s does not take keyword arguments", name)
		}
		runner, err := newBlockCallRunner(exec, block, name)
		if err != nil {
			return NewNil(), err
		}
		acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
		out := make([]Value, 0, rangeBuildInitialCap)
		var blockArg [1]Value
		err = exec.rangeForEach(receiver.Range(), func(value int64) (bool, error) {
			element := NewInt(value)
			blockArg[0] = element
			val, err := runner.call(blockArg[:])
			if err != nil {
				return false, err
			}
			if val.Truthy() == keepTruthy {
				out = append(out, element)
				if err := acc.add(element, cap(out)); err != nil {
					return false, err
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

// rangeMemberFind returns the first integer in the range for which the block is
// truthy, or nil when none match, mirroring Array#find. It short-circuits on
// the first match.
func rangeMemberFind() Value {
	return NewAutoBuiltin("range.find", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(args) > 0 {
			return NewNil(), fmt.Errorf("range.find does not take arguments")
		}
		if len(kwargs) > 0 {
			return NewNil(), fmt.Errorf("range.find does not take keyword arguments")
		}
		runner, err := newBlockCallRunner(exec, block, "range.find")
		if err != nil {
			return NewNil(), err
		}
		found := NewNil()
		var blockArg [1]Value
		err = exec.rangeForEach(receiver.Range(), func(value int64) (bool, error) {
			element := NewInt(value)
			blockArg[0] = element
			val, err := runner.call(blockArg[:])
			if err != nil {
				return false, err
			}
			if val.Truthy() {
				found = element
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

// rangeMemberReduce folds the range's integers with the block, mirroring
// Array#reduce in its block forms. With no argument the first integer seeds the
// accumulator; with one argument that value seeds it. An empty range returns
// the seed, or nil when no seed is given.
func rangeMemberReduce() Value {
	return NewAutoBuiltin("range.reduce", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(args) > 1 {
			return NewNil(), fmt.Errorf("range.reduce expects at most one argument")
		}
		if len(kwargs) > 0 {
			return NewNil(), fmt.Errorf("range.reduce does not take keyword arguments")
		}
		runner, err := newBlockCallRunner(exec, block, "range.reduce")
		if err != nil {
			return NewNil(), err
		}
		acc := NewNil()
		hasAcc := false
		if len(args) == 1 {
			acc = args[0]
			hasAcc = true
		}
		var blockArgs [2]Value
		err = exec.rangeForEach(receiver.Range(), func(value int64) (bool, error) {
			element := NewInt(value)
			if !hasAcc {
				acc = element
				hasAcc = true
				return false, nil
			}
			blockArgs[0] = acc
			blockArgs[1] = element
			next, err := runner.call(blockArgs[:])
			if err != nil {
				return false, err
			}
			if err := exec.checkMemoryWith(next); err != nil {
				return false, err
			}
			acc = next
			return false, nil
		})
		if err != nil {
			return NewNil(), err
		}
		return acc, nil
	})
}

// rangeMemberCount returns the number of integers in the range, or — when a
// block is given — the number for which the block is truthy, mirroring
// Array#count.
func rangeMemberCount() Value {
	return NewAutoBuiltin("range.count", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(args) > 0 {
			return NewNil(), fmt.Errorf("range.count does not take arguments")
		}
		if len(kwargs) > 0 {
			return NewNil(), fmt.Errorf("range.count does not take keyword arguments")
		}
		rng := receiver.Range()
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
		count := int64(0)
		var blockArg [1]Value
		err = exec.rangeForEach(rng, func(value int64) (bool, error) {
			blockArg[0] = NewInt(value)
			val, err := runner.call(blockArg[:])
			if err != nil {
				return false, err
			}
			if val.Truthy() {
				count++
			}
			return false, nil
		})
		if err != nil {
			return NewNil(), err
		}
		return NewInt(count), nil
	})
}

// rangeMemberSum returns the sum of the range's integers plus an optional
// initial value (default 0), mirroring Array#sum. It uses checked addition so a
// span whose total overflows int64 raises rather than wrapping.
func rangeMemberSum() Value {
	return NewAutoBuiltin("range.sum", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(args) > 1 {
			return NewNil(), fmt.Errorf("range.sum expects at most one argument")
		}
		if len(kwargs) > 0 {
			return NewNil(), fmt.Errorf("range.sum does not take keyword arguments")
		}
		if valueBlock(block) != nil {
			return NewNil(), fmt.Errorf("range.sum does not take a block")
		}
		total := int64(0)
		if len(args) == 1 {
			if args[0].Kind() != KindInt {
				return NewNil(), fmt.Errorf("range.sum expects an integer initial value")
			}
			total = args[0].Int()
		}
		err := exec.rangeForEach(receiver.Range(), func(value int64) (bool, error) {
			next, ok := addInt64Checked(total, value)
			if !ok {
				return false, fmt.Errorf("range.sum overflow")
			}
			total = next
			return false, nil
		})
		if err != nil {
			return NewNil(), err
		}
		return NewInt(total), nil
	})
}

// rangeMemberMinMax returns the smallest (min) or largest (max) integer the
// range iterates over, or nil for an empty range, mirroring Array#min and
// Array#max without a block.
func rangeMemberMinMax(property string) Value {
	wantMin := property == "min"
	name := "range." + property
	return NewAutoBuiltin(name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(args) > 0 {
			return NewNil(), fmt.Errorf("%s does not take arguments", name)
		}
		if len(kwargs) > 0 {
			return NewNil(), fmt.Errorf("%s does not take keyword arguments", name)
		}
		if valueBlock(block) != nil {
			return NewNil(), fmt.Errorf("%s does not accept a block", name)
		}
		best := int64(0)
		seen := false
		err := exec.rangeForEach(receiver.Range(), func(value int64) (bool, error) {
			if !seen {
				best = value
				seen = true
				return false, nil
			}
			if wantMin {
				if value < best {
					best = value
				}
			} else if value > best {
				best = value
			}
			return false, nil
		})
		if err != nil {
			return NewNil(), err
		}
		if !seen {
			return NewNil(), nil
		}
		return NewInt(best), nil
	})
}

// rangeMemberPredicate builds the membership predicates cover?, include?,
// and member?. For Vibescript's integer ranges these are equivalent, just
// as they are for Ruby's integer ranges, and they share the same direction
// and exclusivity handling used by case/when membership. A non-numeric
// argument is never a member, matching Ruby's silent false rather than an
// error.
func rangeMemberPredicate(property string) Value {
	return NewAutoBuiltin("range."+property, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(args) != 1 {
			return NewNil(), fmt.Errorf("range.%s expects one argument", property)
		}
		if len(kwargs) > 0 {
			return NewNil(), fmt.Errorf("range.%s does not take keyword arguments", property)
		}
		rng := receiver.Range()
		switch args[0].Kind() {
		case KindInt:
			return NewBool(rangeContainsInt(rng, args[0].Int())), nil
		case KindFloat:
			return NewBool(rangeContainsFloat(rng, args[0].Float())), nil
		default:
			return NewBool(false), nil
		}
	})
}

// rangeMemberFirst returns the range's start endpoint with no argument, or
// the first n iterated elements as an array with a non-negative count. The
// endpoint result ignores exclusivity, matching Ruby's Range#first.
func rangeMemberFirst() Value {
	return NewAutoBuiltin("range.first", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(kwargs) > 0 {
			return NewNil(), fmt.Errorf("range.first does not take keyword arguments")
		}
		rng := receiver.Range()
		if len(args) == 0 {
			return NewInt(rng.Start), nil
		}
		if len(args) != 1 {
			return NewNil(), fmt.Errorf("range.first expects at most one argument")
		}
		count, err := rangeCountArg(args[0], "range.first")
		if err != nil {
			return NewNil(), err
		}
		return exec.rangeMaterialize(rng, count, false)
	})
}

// rangeMemberLast returns the range's end endpoint with no argument, or the
// last n iterated elements as an array with a non-negative count. The
// endpoint result ignores exclusivity, matching Ruby's Range#last.
func rangeMemberLast() Value {
	return NewAutoBuiltin("range.last", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(kwargs) > 0 {
			return NewNil(), fmt.Errorf("range.last does not take keyword arguments")
		}
		rng := receiver.Range()
		if len(args) == 0 {
			return NewInt(rng.End), nil
		}
		if len(args) != 1 {
			return NewNil(), fmt.Errorf("range.last expects at most one argument")
		}
		count, err := rangeCountArg(args[0], "range.last")
		if err != nil {
			return NewNil(), err
		}
		return exec.rangeMaterialize(rng, count, true)
	})
}

// rangeMemberSize returns the number of integers the range iterates over.
// Vibescript iterates descending ranges, so a descending range reports its
// span rather than zero; see docs/control-flow.md.
func rangeMemberSize() Value {
	return NewAutoBuiltin("range.size", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(args) > 0 {
			return NewNil(), fmt.Errorf("range.size does not take arguments")
		}
		if len(kwargs) > 0 {
			return NewNil(), fmt.Errorf("range.size does not take keyword arguments")
		}
		length, overflow := rangeLength(receiver.Range())
		if overflow {
			return NewNil(), fmt.Errorf("range.size overflow")
		}
		return NewInt(length), nil
	})
}

// rangeMemberExcludeEnd reports whether the range excludes its end endpoint
// (`...` versus `..`).
func rangeMemberExcludeEnd() Value {
	return NewAutoBuiltin("range.exclude_end?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(args) > 0 {
			return NewNil(), fmt.Errorf("range.exclude_end? does not take arguments")
		}
		if len(kwargs) > 0 {
			return NewNil(), fmt.Errorf("range.exclude_end? does not take keyword arguments")
		}
		return NewBool(receiver.Range().Exclusive), nil
	})
}

// rangeMemberToArray materializes the range's iteration sequence into an
// array, honoring sandbox step and memory quotas while building it.
func rangeMemberToArray() Value {
	return NewAutoBuiltin("range.to_a", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(args) > 0 {
			return NewNil(), fmt.Errorf("range.to_a does not take arguments")
		}
		if len(kwargs) > 0 {
			return NewNil(), fmt.Errorf("range.to_a does not take keyword arguments")
		}
		rng := receiver.Range()
		length, overflow := rangeLength(rng)
		if overflow {
			return NewNil(), fmt.Errorf("range.to_a result too large")
		}
		return exec.rangeMaterialize(rng, length, false)
	})
}

// rangeCountArg validates a first/last count argument: it must be a
// non-negative integer, matching Ruby's ArgumentError on negatives.
func rangeCountArg(arg Value, method string) (int64, error) {
	if arg.Kind() != KindInt {
		return 0, fmt.Errorf("%s expects an integer count", method)
	}
	count := arg.Int()
	if count < 0 {
		return 0, fmt.Errorf("%s count must be non-negative", method)
	}
	return count, nil
}

// rangeLength returns how many integers the range iterates over. The bool
// reports int64 overflow, which only the full 64-bit span can trigger.
func rangeLength(rng Range) (int64, bool) {
	low, high := rng.Start, rng.End
	if low > high {
		low, high = high, low
	}
	span := high - low
	if span < 0 {
		// high - low overflowed int64 (e.g. MinInt64..MaxInt64).
		return 0, true
	}
	if rng.Exclusive {
		return span, false
	}
	if span == math.MaxInt64 {
		return 0, true
	}
	return span + 1, false
}

// rangeLastElement returns the final value the range iterates over: its end
// endpoint for inclusive ranges, or the value one step inside the end for
// exclusive ranges. Callers must only invoke it for non-empty ranges; the
// trailing-window arithmetic in rangeMaterialize guarantees this because it
// has already clamped the requested count to a positive number of in-range
// elements.
func rangeLastElement(rng Range) int64 {
	if !rng.Exclusive {
		return rng.End
	}
	if rng.Start <= rng.End {
		return rng.End - 1
	}
	return rng.End + 1
}

// rangeMaterialize builds up to limit elements of the range's iteration
// sequence. When fromEnd is true it returns the trailing elements (for
// last(n)); otherwise the leading elements (for to_a and first(n)). Step
// and memory quotas are enforced per element so large materializations are
// bounded by the sandbox.
//
// A range whose total element count overflows int64 (the inclusive full
// 64-bit span) is still valid for a bounded first(n)/last(n): the count is
// only clamped to the range length when that length is representable, and the
// window's starting value is derived from the relevant endpoint, so a single
// trailing or leading element never depends on the unrepresentable total.
func (exec *Execution) rangeMaterialize(rng Range, limit int64, fromEnd bool) (Value, error) {
	if limit <= 0 {
		return NewArray([]Value{}), nil
	}
	// Only clamp to the element count when it fits in int64. An overflowing
	// length is strictly greater than any limit (limit <= MaxInt < length), so
	// the requested window always fits without clamping.
	length, overflow := rangeLength(rng)
	if !overflow && limit > length {
		limit = length
	}
	if limit <= 0 {
		return NewArray([]Value{}), nil
	}
	if limit > int64(math.MaxInt) {
		return NewNil(), fmt.Errorf("range materialization result too large")
	}

	ascending := rng.Start <= rng.End

	// Compute the first value of the window directly from the endpoint nearest
	// to the start of the window. For last(n) this avoids both materializing
	// and depending on the (possibly unrepresentable) total element count: the
	// trailing window's elements are all within the range, so deriving its
	// first value from the final iterated element cannot overflow int64.
	current := rng.Start
	if fromEnd {
		last := rangeLastElement(rng)
		if ascending {
			current = last - (limit - 1)
		} else {
			current = last + (limit - 1)
		}
	}

	// Reject the allocation up front so a near-MaxInt64 range cannot reserve a
	// multi-gigabyte backing array before the per-element check below would
	// observe it. limit is already clamped to length and <= math.MaxInt.
	if err := exec.checkProjectedIntArrayBytes(int(limit)); err != nil {
		return NewNil(), err
	}

	// Only reserve a modest initial capacity and let append grow the backing
	// array as elements are produced. The projected check above is an early-out
	// for ranges whose full materialization clearly exceeds the memory quota,
	// but it passes whenever MemoryQuotaBytes is large. Preallocating the full
	// limit there would reserve the entire backing array before the per-element
	// step() and memory check below could reject the call, so a large
	// MemoryQuotaBytes paired with a small StepQuota could still trigger a huge
	// up-front allocation. Bounding the initial capacity keeps the actual
	// allocation proportional to the elements the quotas allow.
	initialCap := limit
	if initialCap > rangeMaterializeInitialCap {
		initialCap = rangeMaterializeInitialCap
	}
	out := make([]Value, 0, int(initialCap))
	for i := int64(0); i < limit; i++ {
		if err := exec.step(); err != nil {
			return NewNil(), err
		}
		out = append(out, NewInt(current))
		// Charge the backing array's current capacity rather than re-estimating
		// the whole array prefix. checkMemoryWith(NewArray(out)) would walk every
		// element on each append, making materialization O(n^2); the projected
		// check is O(1) and keyed on cap so it tracks the actual allocation as
		// append doubles the backing array. Each element is an inlined int, so it
		// adds nothing beyond its slot, making the cap-based bound exact.
		if err := exec.checkProjectedIntArrayBytes(cap(out)); err != nil {
			return NewNil(), err
		}
		if ascending {
			current++
		} else {
			current--
		}
	}
	return NewArray(out), nil
}

package runtime

import (
	"fmt"
	"math"
)

// rangeMemberNames mirrors the names dispatched by rangeMember and feeds
// "did you mean" suggestions on the error path. Keep it in sync with the
// switch below; TestMemberSuggestionCandidatesResolve enforces that every
// listed name resolves.
var rangeMemberNames = []string{
	"cover?", "include?", "member?", "first", "last", "size", "exclude_end?", "to_a",
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
	default:
		return NewNil(), exec.errorAt(pos, "unknown range method %s%s", property, didYouMean(property, rangeMemberNames))
	}
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

// rangeMaterialize builds up to limit elements of the range's iteration
// sequence. When fromEnd is true it returns the trailing elements (for
// last(n)); otherwise the leading elements (for to_a and first(n)). Step
// and memory quotas are enforced per element so large materializations are
// bounded by the sandbox.
func (exec *Execution) rangeMaterialize(rng Range, limit int64, fromEnd bool) (Value, error) {
	length, overflow := rangeLength(rng)
	if overflow {
		return NewNil(), fmt.Errorf("range materialization result too large")
	}
	if limit > length {
		limit = length
	}
	if limit <= 0 {
		return NewArray([]Value{}), nil
	}
	if limit > int64(math.MaxInt) {
		return NewNil(), fmt.Errorf("range materialization result too large")
	}

	ascending := rng.Start <= rng.End
	// skip drops leading elements so last(n) keeps the trailing window.
	skip := int64(0)
	if fromEnd {
		skip = length - limit
	}

	out := make([]Value, 0, int(limit))
	current := rng.Start
	for produced := int64(0); produced < length; produced++ {
		if int64(len(out)) == limit {
			break
		}
		if produced >= skip {
			if err := exec.step(); err != nil {
				return NewNil(), err
			}
			out = append(out, NewInt(current))
			if err := exec.checkMemoryWith(NewArray(out)); err != nil {
				return NewNil(), err
			}
		}
		if ascending {
			current++
		} else {
			current--
		}
	}
	return NewArray(out), nil
}

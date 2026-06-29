package runtime

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/bits"
	"reflect"
	"strings"
	"time"

	"github.com/mgomes/vibescript/vibes/value"
)

const (
	maxFlattenDepth      = 1024
	nanosecondsPerSecond = int64(time.Second)
)

func valueToHashKey(val Value) (string, error) {
	if _, err := value.HashKey(val); err != nil {
		return "", err
	}
	return value.HashDisplayKey(val), nil
}

func canonicalHashKey(val Value) (string, error) {
	return value.HashKey(val)
}

func hashLookupKey(val Value) (HashLookupKey, error) {
	return value.NewHashLookupKey(val)
}

func hashGet(container, key Value) (Value, bool, error) {
	return container.HashGet(key)
}

func hashSet(container, key, val Value) error {
	return container.HashSet(key, val)
}

func hashHasTypedEntries(val Value) bool {
	return val.HashHasTypedEntries()
}

func setClonedHashEntry(hash, key, val Value) {
	if err := hashSet(hash, key, val); err != nil {
		panic(fmt.Sprintf("clone valid hash entry: %v", err))
	}
}

func valueToInt(val Value) (int, error) {
	switch val.Kind() {
	case KindInt:
		return int(val.Int()), nil
	case KindFloat:
		f := val.Float()
		// Reject non-finite and out-of-range floats so the new Infinity/NaN
		// values cannot reach int() (which is implementation-specific for them)
		// and slip into index/count helpers. float64(math.MaxInt) rounds up to
		// 2^63, so reject `>=` it; float64(math.MinInt) is exactly -2^63.
		if math.IsNaN(f) || math.IsInf(f, 0) || f >= float64(math.MaxInt) || f < float64(math.MinInt) {
			return 0, fmt.Errorf("index must be integer")
		}
		return int(f), nil
	default:
		return 0, fmt.Errorf("index must be integer")
	}
}

// digPath walks args as a nested lookup path, descending one level per path
// component. It traverses hashes by Ruby-style hash key, objects by symbol/string
// field key, and arrays by integer index, returning nil as soon as a key is
// absent or an index is out of range. This mirrors Ruby's Hash#dig and Array#dig
// semantics while staying within Vibescript's non-negative array-index model.
//
// Each hash step is a full `[]` access: a missing key consults that hash's
// Ruby-style default (a default value returned without inserting, or a default
// proc invoked with the hash and key, which may store), and dig then descends
// into whatever the default resolves to. This matches MRI, where
// Hash.new(0).dig(:missing) returns 0 and a default proc fires per missing dig
// step. Objects never carry defaults, so a missing object key yields nil.
//
// Two behaviors are deliberate divergences from MRI Ruby:
//
//   - Indexing an array with a non-integer path component is a type error, like
//     Ruby's "no implicit conversion into Integer" TypeError. A hash or object
//     miss returns nil (Ruby returns nil there too).
//   - Continuing a path through a scalar (a non-collection that does not respond
//     to dig) returns nil rather than raising. Vibescript has always done this
//     for Hash#dig, and keeping it avoids surprising scripts that probe deeper
//     than the data nests. MRI instead raises a TypeError once a non-collection
//     default (for example the 0 from Hash.new(0)) is dug into further.
//
// name is the caller's method name (for example "array.dig") used in error
// messages.
func (exec *Execution) digPath(name string, current Value, args []Value) (Value, error) {
	for _, arg := range args {
		switch current.Kind() {
		case KindHash, KindObject:
			next, ok, err := hashGet(current, arg)
			if err != nil {
				if current.Kind() == KindObject {
					return NewNil(), nil
				}
				return NewNil(), err
			}
			if !ok {
				// A missing hash key is a [] access that consults the hash's
				// default (objects carry none, so they stay nil). The resolved
				// default becomes the next value to descend into, exactly as
				// MRI digs into the result of each step's [] access.
				if current.Kind() != KindHash {
					return NewNil(), nil
				}
				resolved, err := exec.hashDefaultForKey(current, arg)
				if err != nil {
					return NewNil(), err
				}
				current = resolved
				continue
			}
			current = next
		case KindArray:
			if arg.Kind() != KindInt && arg.Kind() != KindFloat {
				return NewNil(), fmt.Errorf("%s array index must be integer", name)
			}
			index, err := valueToInt(arg)
			if err != nil {
				return NewNil(), fmt.Errorf("%s array index must be integer", name)
			}
			if arg.Kind() == KindFloat && math.Trunc(arg.Float()) != arg.Float() {
				return NewNil(), fmt.Errorf("%s array index must be integer", name)
			}
			arr := current.Array()
			if index < 0 || index >= len(arr) {
				return NewNil(), nil
			}
			current = arr[index]
		default:
			return NewNil(), nil
		}
	}
	return current, nil
}

// errNegativeCount signals that a count argument was numeric but negative.
// Callers detect it with errors.Is to emit a method-specific message.
var errNegativeCount = errors.New("count must not be negative")

// errWidthNotInteger signals that a width argument was not a numeric value that
// could represent an integer. Callers detect it with errors.Is to emit a
// method-specific message.
var errWidthNotInteger = errors.New("width must be integer")

// errWidthOutOfRange signals that a width argument was a finite Float whose
// truncated value falls outside the native int range, or a non-finite Float
// (NaN/Inf). Callers detect it with errors.Is to emit a method-specific message
// mirroring Ruby's RangeError for such widths.
var errWidthOutOfRange = errors.New("width is out of range")

// errIncomparableOperands signals that two operands of different kinds cannot
// be ordered. The spaceship operator detects it with isIncomparable and yields
// nil, matching Ruby's `1 <=> "a"`, while relational operators surface it.
var errIncomparableOperands = errors.New("unsupported comparison operands")

// errMoneyCompareMismatch signals that two money values cannot be ordered
// because their currencies differ. Its message follows the documented
// comparison convention; the spaceship operator still treats it as
// incomparable via isIncomparable.
var errMoneyCompareMismatch = errors.New("money currency mismatch for comparison")

// isIncomparable reports whether err marks an operand pair that cannot be
// ordered, so the spaceship operator can yield nil instead of raising.
func isIncomparable(err error) bool {
	return errors.Is(err, errIncomparableOperands) || errors.Is(err, errMoneyCompareMismatch)
}

// valueToPadWidth converts a numeric width argument to an int, truncating
// fractional Floats toward zero like Ruby's to_int. Unlike valueToCount it
// permits negative widths because padding helpers treat a width at or below the
// receiver length as a no-op rather than an error. Non-finite Floats and Floats
// whose truncated magnitude exceeds the int range return errWidthOutOfRange so
// callers do not silently wrap a huge width into an in-range int (for example
// 1e20 collapsing to math.MinInt) and bypass the projected-size guard.
// Non-numeric values return errWidthNotInteger.
func valueToPadWidth(val Value) (int, error) {
	switch val.Kind() {
	case KindInt:
		return int(val.Int()), nil
	case KindFloat:
		f := val.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, errWidthOutOfRange
		}
		// Truncate toward zero first, matching Ruby's to_int, then verify the
		// result is representable as an int. float64(math.MaxInt) rounds up to
		// 2^63, so a strict `>` check would let exactly 2^63 through and then
		// int(2^63) overflows to math.MinInt; reject `>= float64(math.MaxInt)`
		// instead. float64(math.MinInt) is exactly -2^63, so `<` is correct.
		t := math.Trunc(f)
		if t >= float64(math.MaxInt) || t < float64(math.MinInt) {
			return 0, errWidthOutOfRange
		}
		return int(t), nil
	default:
		return 0, errWidthNotInteger
	}
}

// valueToCount converts a numeric count argument to a non-negative int,
// truncating positive fractional values toward zero like Ruby's to_int. It
// inspects the original numeric value's sign before truncating so that
// fractional negatives such as -0.5 are rejected rather than silently
// collapsing to 0. Numeric negatives return errNegativeCount; non-numeric
// values, NaN, and values outside the int range return a generic error.
func valueToCount(val Value) (int, error) {
	switch val.Kind() {
	case KindInt:
		if val.Int() < 0 {
			return 0, errNegativeCount
		}
		return int(val.Int()), nil
	case KindFloat:
		f := val.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) || f > math.MaxInt || f < math.MinInt {
			return 0, fmt.Errorf("count must be integer")
		}
		// Truncate toward zero first, matching Ruby's to_int (and Array#first/
		// #last), then reject only a negative integer. A fraction in (-1, 0)
		// therefore becomes 0 rather than an error.
		n := int(f)
		if n < 0 {
			return 0, errNegativeCount
		}
		return n, nil
	default:
		return 0, fmt.Errorf("count must be integer")
	}
}

func sortComparisonResult(val Value) (int, error) {
	switch val.Kind() {
	case KindInt:
		switch {
		case val.Int() < 0:
			return -1, nil
		case val.Int() > 0:
			return 1, nil
		default:
			return 0, nil
		}
	case KindFloat:
		switch {
		case val.Float() < 0:
			return -1, nil
		case val.Float() > 0:
			return 1, nil
		default:
			return 0, nil
		}
	default:
		return 0, fmt.Errorf("comparator must be numeric")
	}
}

func arraySortCompareValues(left, right Value) (int, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt:
		switch {
		case left.Int() < right.Int():
			return -1, nil
		case left.Int() > right.Int():
			return 1, nil
		default:
			return 0, nil
		}
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		lf, rf := left.Float(), right.Float()
		// NaN is unordered: returning 0 (equal) here would let sort/min/max
		// treat NaN as equal to every element. Report it as incomparable so
		// callers fail consistently with the <=> operator (which yields nil).
		if math.IsNaN(lf) || math.IsNaN(rf) {
			return 0, fmt.Errorf("cannot compare NaN")
		}
		switch {
		case lf < rf:
			return -1, nil
		case lf > rf:
			return 1, nil
		default:
			return 0, nil
		}
	case left.Kind() == KindString && right.Kind() == KindString:
		switch {
		case left.String() < right.String():
			return -1, nil
		case left.String() > right.String():
			return 1, nil
		default:
			return 0, nil
		}
	case left.Kind() == KindSymbol && right.Kind() == KindSymbol:
		switch {
		case left.String() < right.String():
			return -1, nil
		case left.String() > right.String():
			return 1, nil
		default:
			return 0, nil
		}
	case left.Kind() == KindBool && right.Kind() == KindBool:
		switch {
		case !left.Bool() && right.Bool():
			return -1, nil
		case left.Bool() && !right.Bool():
			return 1, nil
		default:
			return 0, nil
		}
	case left.Kind() == KindDuration && right.Kind() == KindDuration:
		switch {
		case left.Duration().Seconds() < right.Duration().Seconds():
			return -1, nil
		case left.Duration().Seconds() > right.Duration().Seconds():
			return 1, nil
		default:
			return 0, nil
		}
	case left.Kind() == KindTime && right.Kind() == KindTime:
		switch {
		case left.Time().Before(right.Time()):
			return -1, nil
		case left.Time().After(right.Time()):
			return 1, nil
		default:
			return 0, nil
		}
	case left.Kind() == KindMoney && right.Kind() == KindMoney:
		if left.Money().Currency() != right.Money().Currency() {
			return 0, fmt.Errorf("money currency mismatch for comparison")
		}
		switch {
		case left.Money().Cents() < right.Money().Cents():
			return -1, nil
		case left.Money().Cents() > right.Money().Cents():
			return 1, nil
		default:
			return 0, nil
		}
	case left.Kind() == KindNil && right.Kind() == KindNil:
		return 0, nil
	default:
		return 0, fmt.Errorf("values are not comparable")
	}
}

// flattenValues recursively flattens nested arrays up to the specified depth.
// depth=-1 means flatten completely (no limit).
// depth=0 means don't flatten at all.
// depth=1 means flatten one level, etc.
// method names the caller (e.g. "array.flatten" or "hash.flatten") so the depth
// and cycle errors read in terms of the method the script invoked.
type flattenState struct {
	arrays map[sliceIdentity]struct{}
	depth  int
	method string
}

func flattenValues(values []Value, depth int, method string) ([]Value, error) {
	return flattenValuesWithState(values, depth, &flattenState{
		arrays: make(map[sliceIdentity]struct{}),
		method: method,
	})
}

func flattenValuesWithState(values []Value, depth int, state *flattenState) ([]Value, error) {
	if state.depth >= maxFlattenDepth {
		return nil, guardLimitErrorf("%s exceeded maximum depth", state.method)
	}

	id := sliceIdentity{
		Ptr: reflect.ValueOf(values).Pointer(),
		Len: len(values),
		Cap: cap(values),
	}
	if id.Ptr != 0 {
		if _, visiting := state.arrays[id]; visiting {
			return nil, fmt.Errorf("%s does not support cyclic structures", state.method)
		}
		state.arrays[id] = struct{}{}
		defer delete(state.arrays, id)
	}

	state.depth++
	defer func() {
		state.depth--
	}()

	out := make([]Value, 0, len(values))
	for _, v := range values {
		if v.Kind() == KindArray && depth != 0 {
			nextDepth := depth
			if nextDepth > 0 {
				nextDepth--
			}
			flattened, err := flattenValuesWithState(v.Array(), nextDepth, state)
			if err != nil {
				return nil, err
			}
			out = append(out, flattened...)
		} else {
			out = append(out, v)
		}
	}
	return out, nil
}

// joinState carries the cycle and depth guards for arrayJoin. It mirrors
// flattenState so recursive joins are bounded the same way recursive flattening
// is: a self-referential array fails rather than recursing forever, and an
// array nested deeper than maxFlattenDepth is rejected before it can exhaust the
// goroutine stack.
type joinState struct {
	arrays map[sliceIdentity]struct{}
	depth  int
}

// arrayJoin renders values into b separated by sep, recursively joining nested
// arrays with the same separator. This matches Ruby's Array#join, which flattens
// nested arrays into the output using the active separator rather than rendering
// their inspect form. Scalar elements use their Vibescript string form, so nil
// contributes an empty segment exactly as Ruby's join does.
func arrayJoin(b *strings.Builder, values []Value, sep string) error {
	return arrayJoinWithState(b, values, sep, &joinState{
		arrays: make(map[sliceIdentity]struct{}),
	})
}

func arrayJoinWithState(b *strings.Builder, values []Value, sep string, state *joinState) error {
	if state.depth >= maxFlattenDepth {
		return guardLimitErrorf("array.join exceeded maximum depth")
	}

	id := sliceIdentity{
		Ptr: reflect.ValueOf(values).Pointer(),
		Len: len(values),
		Cap: cap(values),
	}
	if id.Ptr != 0 {
		if _, visiting := state.arrays[id]; visiting {
			return fmt.Errorf("array.join does not support cyclic structures")
		}
		state.arrays[id] = struct{}{}
		defer delete(state.arrays, id)
	}

	state.depth++
	defer func() {
		state.depth--
	}()

	for i, v := range values {
		if i > 0 {
			b.WriteString(sep)
		}
		if v.Kind() == KindArray {
			if err := arrayJoinWithState(b, v.Array(), sep, state); err != nil {
				return err
			}
			continue
		}
		b.WriteString(v.String())
	}
	return nil
}

func floatToInt64Checked(v float64, method string) (int64, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("%s result out of int64 range", method)
	}
	// float64(math.MaxInt64) rounds to 2^63, so use >= 2^63 as the true upper bound.
	if v < float64(math.MinInt64) || v >= math.Exp2(63) {
		return 0, fmt.Errorf("%s result out of int64 range", method)
	}
	return int64(v), nil
}

func int64RangeError(method string) error {
	return fmt.Errorf("%s result out of int64 range", method)
}

func addInt64Checked(left, right int64) (int64, bool) {
	sum := left + right
	if (left > 0 && right > 0 && sum < 0) || (left < 0 && right < 0 && sum >= 0) {
		return 0, false
	}
	return sum, true
}

func subInt64Checked(left, right int64) (int64, bool) {
	diff := left - right
	if (left^right)&(left^diff) < 0 {
		return 0, false
	}
	return diff, true
}

func mulInt64Checked(left, right int64) (int64, bool) {
	if left == 0 || right == 0 {
		return 0, true
	}
	negative := (left < 0) != (right < 0)
	lMag := uint64(left)
	if left < 0 {
		lMag = -lMag
	}
	rMag := uint64(right)
	if right < 0 {
		rMag = -rMag
	}
	hi, lo := bits.Mul64(lMag, rMag)
	if hi != 0 {
		return 0, false
	}
	if negative {
		minMagnitude := uint64(math.MaxInt64) + 1
		if lo > minMagnitude {
			return 0, false
		}
		if lo == minMagnitude {
			return math.MinInt64, true
		}
		return -int64(lo), true
	}
	if lo > uint64(math.MaxInt64) {
		return 0, false
	}
	return int64(lo), true
}

func floorDivIntChecked(left, right int64) (int64, bool) {
	if left == math.MinInt64 && right == -1 {
		return 0, false
	}
	return floorDivInt(left, right), true
}

func divInt64Checked(left, right int64) (int64, bool) {
	if left == math.MinInt64 && right == -1 {
		return 0, false
	}
	return left / right, true
}

func durationSecondsToTimeDuration(seconds int64, method string) (time.Duration, error) {
	if seconds > math.MaxInt64/nanosecondsPerSecond || seconds < math.MinInt64/nanosecondsPerSecond {
		return 0, int64RangeError(method)
	}
	return time.Duration(seconds) * time.Second, nil
}

// numericSecondsToTimeDuration converts a numeric value (interpreted as a
// count of seconds, matching Ruby's Time arithmetic) into a nanosecond
// time.Duration. Integers shift by whole seconds while floats carry
// sub-second precision down to the nanosecond. It reports an error when the
// nanosecond magnitude would overflow int64.
func numericSecondsToTimeDuration(val Value, method string) (time.Duration, error) {
	switch val.Kind() {
	case KindInt:
		return durationSecondsToTimeDuration(val.Int(), method)
	case KindFloat:
		// Ruby floors the scaled nanosecond offset, so negative fractional
		// nanoseconds move further from zero rather than truncating toward it.
		ns, err := floatSecondsToFlooredNanos(val.Float(), false, method)
		if err != nil {
			return 0, err
		}
		return time.Duration(ns), nil
	default:
		return 0, fmt.Errorf("%s expects numeric seconds", method)
	}
}

// floatSecondsToFlooredNanos converts a float count of seconds into a floored
// nanosecond offset, optionally negating the seconds first. Scaling routes
// through math/big so the multiplication by 10^9 stays exact: floating the
// product before flooring (math.Floor(f * 1e9)) can round a value whose exact
// representation sits just below an integer nanosecond up to that integer,
// flipping the floor and diverging from Ruby (e.g. 0.123456789 floors to
// 123456788 ns, not 123456789). It reports an error for non-finite inputs or
// when the floored offset would overflow int64.
func floatSecondsToFlooredNanos(seconds float64, negate bool, method string) (int64, error) {
	rat := new(big.Rat).SetFloat64(seconds)
	if rat == nil {
		return 0, int64RangeError(method)
	}
	if negate {
		rat.Neg(rat)
	}
	rat.Mul(rat, new(big.Rat).SetInt64(nanosecondsPerSecond))
	floor := new(big.Int)
	floor.Div(rat.Num(), rat.Denom()) // Div floors because Rat denominators are positive
	return bigToInt64Checked(floor, method)
}

// negatedNumericSecondsToTimeDuration converts the negation of a numeric
// seconds value into a nanosecond time.Duration. Time subtraction is defined
// as t + (-x), so the negation happens on the numeric value before it becomes
// a duration. This keeps subtraction symmetric with addition and avoids ever
// unary-negating a time.Duration, which would overflow for the most negative
// representable nanosecond offset (time.Duration(math.MinInt64)).
func negatedNumericSecondsToTimeDuration(val Value, method string) (time.Duration, error) {
	switch val.Kind() {
	case KindInt:
		neg, ok := subInt64Checked(0, val.Int())
		if !ok {
			return 0, int64RangeError(method)
		}
		return durationSecondsToTimeDuration(neg, method)
	case KindFloat:
		// Negate first so the floor matches Ruby's t + (-x): subtracting a
		// positive fractional offset floors the negated nanoseconds away from
		// zero, mirroring numericSecondsToTimeDuration's addition path.
		ns, err := floatSecondsToFlooredNanos(val.Float(), true, method)
		if err != nil {
			return 0, err
		}
		return time.Duration(ns), nil
	default:
		return 0, fmt.Errorf("%s expects numeric seconds", method)
	}
}

// timeDifferenceSeconds returns the difference left - right (both Time
// values) as a floating-point number of seconds, matching Ruby's Time#-
// behavior. It computes the whole-second and sub-second parts separately so
// the nanosecond span between two instants cannot silently overflow the way a
// raw time.Duration subtraction would for differences beyond ~292 years.
func timeDifferenceSeconds(left, right time.Time) (float64, error) {
	secDiff, ok := subInt64Checked(left.Unix(), right.Unix())
	if !ok {
		return 0, int64RangeError("time subtraction")
	}
	nsecDiff := int64(left.Nanosecond()) - int64(right.Nanosecond())
	return float64(secDiff) + float64(nsecDiff)/float64(nanosecondsPerSecond), nil
}

func addValues(left, right Value) (Value, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt:
		sum, ok := addInt64Checked(left.Int(), right.Int())
		if !ok {
			return NewNil(), int64RangeError("integer addition")
		}
		return NewInt(sum), nil
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		return NewFloat(left.Float() + right.Float()), nil
	case left.Kind() == KindTime && right.Kind() == KindDuration:
		delta, err := durationSecondsToTimeDuration(right.Duration().Seconds(), "time addition")
		if err != nil {
			return NewNil(), err
		}
		return NewTime(left.Time().Add(delta)), nil
	case right.Kind() == KindTime && left.Kind() == KindDuration:
		delta, err := durationSecondsToTimeDuration(left.Duration().Seconds(), "time addition")
		if err != nil {
			return NewNil(), err
		}
		return NewTime(right.Time().Add(delta)), nil
	case left.Kind() == KindTime && (right.Kind() == KindInt || right.Kind() == KindFloat):
		delta, err := numericSecondsToTimeDuration(right, "time addition")
		if err != nil {
			return NewNil(), err
		}
		return NewTime(left.Time().Add(delta)), nil
	case right.Kind() == KindTime && (left.Kind() == KindInt || left.Kind() == KindFloat):
		delta, err := numericSecondsToTimeDuration(left, "time addition")
		if err != nil {
			return NewNil(), err
		}
		return NewTime(right.Time().Add(delta)), nil
	case left.Kind() == KindDuration && right.Kind() == KindDuration:
		sum, ok := addInt64Checked(left.Duration().Seconds(), right.Duration().Seconds())
		if !ok {
			return NewNil(), int64RangeError("duration addition")
		}
		return NewDuration(durationFromSeconds(sum)), nil
	case left.Kind() == KindDuration && (right.Kind() == KindInt || right.Kind() == KindFloat):
		secs, err := valueToInt64(right)
		if err != nil {
			return NewNil(), err
		}
		sum, ok := addInt64Checked(left.Duration().Seconds(), secs)
		if !ok {
			return NewNil(), int64RangeError("duration addition")
		}
		return NewDuration(durationFromSeconds(sum)), nil
	case right.Kind() == KindDuration && (left.Kind() == KindInt || left.Kind() == KindFloat):
		secs, err := valueToInt64(left)
		if err != nil {
			return NewNil(), err
		}
		sum, ok := addInt64Checked(right.Duration().Seconds(), secs)
		if !ok {
			return NewNil(), int64RangeError("duration addition")
		}
		return NewDuration(durationFromSeconds(sum)), nil
	case left.Kind() == KindArray && right.Kind() == KindArray:
		lArr := left.Array()
		rArr := right.Array()
		out := make([]Value, len(lArr)+len(rArr))
		copy(out, lArr)
		copy(out[len(lArr):], rArr)
		return NewArray(out), nil
	case left.Kind() == KindString || right.Kind() == KindString:
		return NewString(left.String() + right.String()), nil
	case left.Kind() == KindMoney && right.Kind() == KindMoney:
		sum, err := left.Money().Add(right.Money())
		if err != nil {
			return NewNil(), err
		}
		return NewMoney(sum), nil
	default:
		return NewNil(), fmt.Errorf("unsupported addition operands")
	}
}

// shovelValues implements the array shovel operator `array << value`. Ruby
// mutates the receiver in place and returns it, but Vibescript collections are
// non-mutating, so this returns a new array with the single value appended,
// matching how Array#push and `array + [value]` behave. The idiomatic
// accumulator pattern is reassignment (`values = values << x`), which the
// runtime routes through the same backing-buffer fast path as those forms.
func shovelValues(left, right Value) (Value, error) {
	if left.Kind() != KindArray {
		return NewNil(), fmt.Errorf("unsupported shovel operands")
	}
	base := left.Array()
	out := make([]Value, len(base)+1)
	copy(out, base)
	out[len(base)] = right
	return NewArray(out), nil
}

// intersectValues implements the array intersection operator `array & other`,
// returning the elements common to both arrays with duplicates removed and the
// left array's order preserved, mirroring Ruby's Array#&.
func intersectValues(left, right Value) (Value, error) {
	if left.Kind() != KindArray || right.Kind() != KindArray {
		return NewNil(), fmt.Errorf("unsupported intersection operands")
	}
	return NewArray(intersectArrayValues(left.Array(), right.Array())), nil
}

func subtractValues(left, right Value) (Value, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt:
		diff, ok := subInt64Checked(left.Int(), right.Int())
		if !ok {
			return NewNil(), int64RangeError("integer subtraction")
		}
		return NewInt(diff), nil
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		return NewFloat(left.Float() - right.Float()), nil
	case left.Kind() == KindTime && right.Kind() == KindDuration:
		delta, err := durationSecondsToTimeDuration(right.Duration().Seconds(), "time subtraction")
		if err != nil {
			return NewNil(), err
		}
		return NewTime(left.Time().Add(-delta)), nil
	case left.Kind() == KindTime && (right.Kind() == KindInt || right.Kind() == KindFloat):
		delta, err := negatedNumericSecondsToTimeDuration(right, "time subtraction")
		if err != nil {
			return NewNil(), err
		}
		return NewTime(left.Time().Add(delta)), nil
	case left.Kind() == KindTime && right.Kind() == KindTime:
		diff, err := timeDifferenceSeconds(left.Time(), right.Time())
		if err != nil {
			return NewNil(), err
		}
		return NewFloat(diff), nil
	case left.Kind() == KindDuration && right.Kind() == KindDuration:
		diff, ok := subInt64Checked(left.Duration().Seconds(), right.Duration().Seconds())
		if !ok {
			return NewNil(), int64RangeError("duration subtraction")
		}
		return NewDuration(durationFromSeconds(diff)), nil
	case left.Kind() == KindDuration && (right.Kind() == KindInt || right.Kind() == KindFloat):
		secs, err := valueToInt64(right)
		if err != nil {
			return NewNil(), err
		}
		diff, ok := subInt64Checked(left.Duration().Seconds(), secs)
		if !ok {
			return NewNil(), int64RangeError("duration subtraction")
		}
		return NewDuration(durationFromSeconds(diff)), nil
	case left.Kind() == KindArray && right.Kind() == KindArray:
		lArr := left.Array()
		rArr := right.Array()
		return NewArray(subtractArrayValues(lArr, rArr)), nil
	case left.Kind() == KindMoney && right.Kind() == KindMoney:
		diff, err := left.Money().Sub(right.Money())
		if err != nil {
			return NewNil(), err
		}
		return NewMoney(diff), nil
	default:
		return NewNil(), fmt.Errorf("unsupported subtraction operands")
	}
}

func multiplyValues(left, right Value) (Value, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt:
		product, ok := mulInt64Checked(left.Int(), right.Int())
		if !ok {
			return NewNil(), int64RangeError("integer multiplication")
		}
		return NewInt(product), nil
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		return NewFloat(left.Float() * right.Float()), nil
	case left.Kind() == KindDuration && (right.Kind() == KindInt || right.Kind() == KindFloat):
		secs, err := valueToInt64(right)
		if err != nil {
			return NewNil(), err
		}
		product, ok := mulInt64Checked(left.Duration().Seconds(), secs)
		if !ok {
			return NewNil(), int64RangeError("duration multiplication")
		}
		return NewDuration(durationFromSeconds(product)), nil
	case right.Kind() == KindDuration && (left.Kind() == KindInt || left.Kind() == KindFloat):
		secs, err := valueToInt64(left)
		if err != nil {
			return NewNil(), err
		}
		product, ok := mulInt64Checked(right.Duration().Seconds(), secs)
		if !ok {
			return NewNil(), int64RangeError("duration multiplication")
		}
		return NewDuration(durationFromSeconds(product)), nil
	case left.Kind() == KindMoney && right.Kind() == KindInt:
		product, err := left.Money().MulInt(right.Int())
		if err != nil {
			return NewNil(), err
		}
		return NewMoney(product), nil
	case left.Kind() == KindInt && right.Kind() == KindMoney:
		product, err := right.Money().MulInt(left.Int())
		if err != nil {
			return NewNil(), err
		}
		return NewMoney(product), nil
	default:
		return NewNil(), fmt.Errorf("unsupported multiplication operands")
	}
}

func powerValues(left, right Value) (Value, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt && right.Int() >= 0:
		result, ok := powInt64Checked(left.Int(), right.Int())
		if !ok {
			return NewNil(), int64RangeError("integer exponentiation")
		}
		return NewInt(result), nil
	case isNumericValue(left) && isNumericValue(right):
		result := math.Pow(left.Float(), right.Float())
		if math.IsInf(result, 0) || math.IsNaN(result) {
			return NewNil(), errors.New("float exponentiation result is not finite")
		}
		return NewFloat(result), nil
	default:
		return NewNil(), fmt.Errorf("unsupported exponentiation operands")
	}
}

func powInt64Checked(base, exponent int64) (int64, bool) {
	result := int64(1)
	factor := base
	for exponent > 0 {
		if exponent%2 == 1 {
			var ok bool
			result, ok = mulInt64Checked(result, factor)
			if !ok {
				return 0, false
			}
		}
		exponent /= 2
		if exponent == 0 {
			break
		}
		var ok bool
		factor, ok = mulInt64Checked(factor, factor)
		if !ok {
			return 0, false
		}
	}
	return result, true
}

func isNumericValue(val Value) bool {
	return val.Kind() == KindInt || val.Kind() == KindFloat
}

func divideValues(left, right Value) (Value, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt:
		if right.Int() == 0 {
			return NewNil(), newTypedRuntimeError(runtimeErrorTypeZeroDiv, errors.New("division by zero"))
		}
		quotient, ok := floorDivIntChecked(left.Int(), right.Int())
		if !ok {
			return NewNil(), int64RangeError("integer division")
		}
		return NewInt(quotient), nil
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		// Float division by zero follows IEEE 754 and Ruby: a finite nonzero
		// numerator yields +/-Infinity and a zero numerator yields NaN, rather
		// than raising. Integer division by zero is handled by the int/int case
		// above and still errors, matching Ruby's ZeroDivisionError.
		return NewFloat(left.Float() / right.Float()), nil
	case left.Kind() == KindDuration && right.Kind() == KindDuration:
		if right.Duration().Seconds() == 0 {
			return NewNil(), newTypedRuntimeError(runtimeErrorTypeZeroDiv, errors.New("division by zero"))
		}
		return NewFloat(float64(left.Duration().Seconds()) / float64(right.Duration().Seconds())), nil
	case left.Kind() == KindDuration && (right.Kind() == KindInt || right.Kind() == KindFloat):
		secs, err := valueToInt64(right)
		if err != nil {
			return NewNil(), err
		}
		if secs == 0 {
			return NewNil(), newTypedRuntimeError(runtimeErrorTypeZeroDiv, errors.New("division by zero"))
		}
		quotient, ok := divInt64Checked(left.Duration().Seconds(), secs)
		if !ok {
			return NewNil(), int64RangeError("duration division")
		}
		return NewDuration(durationFromSeconds(quotient)), nil
	case left.Kind() == KindMoney && right.Kind() == KindInt:
		res, err := left.Money().DivInt(right.Int())
		if err != nil {
			return NewNil(), err
		}
		return NewMoney(res), nil
	default:
		return NewNil(), fmt.Errorf("unsupported division operands")
	}
}

func moduloValues(left, right Value) (Value, error) {
	if left.Kind() == KindString {
		values := []Value{right}
		if right.Kind() == KindArray {
			values = right.Array()
		}
		return formatStringValues(left.String(), values)
	}
	if left.Kind() == KindInt && right.Kind() == KindInt {
		if right.Int() == 0 {
			return NewNil(), zeroDivisionErrorf("modulo by zero")
		}
		return NewInt(floorModInt(left.Int(), right.Int())), nil
	}
	if left.Kind() == KindDuration && right.Kind() == KindDuration {
		if right.Duration().Seconds() == 0 {
			return NewNil(), zeroDivisionErrorf("modulo by zero")
		}
		return NewDuration(durationFromSeconds(left.Duration().Seconds() % right.Duration().Seconds())), nil
	}
	return NewNil(), fmt.Errorf("unsupported modulo operands")
}

func floorDivInt(left, right int64) int64 {
	quotient := left / right
	remainder := left % right
	if remainder != 0 && ((remainder < 0) != (right < 0)) {
		quotient--
	}
	return quotient
}

func floorModInt(left, right int64) int64 {
	remainder := left % right
	if remainder != 0 && ((remainder < 0) != (right < 0)) {
		remainder += right
	}
	return remainder
}

func compareValues(left, right Value, cmp func(int) bool) (Value, error) {
	order, ordered, err := compareValueOrder(left, right)
	if err != nil {
		return NewNil(), err
	}
	// Unordered operands (a NaN on either side) make every ordered comparison
	// false, matching IEEE 754 and Ruby's `<`, `<=`, `>`, `>=`.
	if !ordered {
		return NewBool(false), nil
	}
	return NewBool(cmp(order)), nil
}

// compareValueOrder reports the relative order of two values as -1, 0, or 1.
// The ordered result is false when the operands are numeric but unordered (a
// NaN on either side); callers translate that into false comparisons and a nil
// spaceship result, matching IEEE 754 and Ruby. A non-nil error for which
// isIncomparable reports true means the operand pair cannot be ordered at all
// (different kinds, or money values in different currencies); the spaceship
// operator turns that into nil while relational operators surface it.
func compareValueOrder(left, right Value) (order int, ordered bool, err error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt:
		switch {
		case left.Int() < right.Int():
			return -1, true, nil
		case left.Int() > right.Int():
			return 1, true, nil
		default:
			return 0, true, nil
		}
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		lf, rf := left.Float(), right.Float()
		switch {
		case math.IsNaN(lf) || math.IsNaN(rf):
			return 0, false, nil
		case lf < rf:
			return -1, true, nil
		case lf > rf:
			return 1, true, nil
		default:
			return 0, true, nil
		}
	case left.Kind() == KindString && right.Kind() == KindString:
		switch {
		case left.String() < right.String():
			return -1, true, nil
		case left.String() > right.String():
			return 1, true, nil
		default:
			return 0, true, nil
		}
	case left.Kind() == KindMoney && right.Kind() == KindMoney:
		if left.Money().Currency() != right.Money().Currency() {
			return 0, false, errMoneyCompareMismatch
		}
		switch {
		case left.Money().Cents() < right.Money().Cents():
			return -1, true, nil
		case left.Money().Cents() > right.Money().Cents():
			return 1, true, nil
		default:
			return 0, true, nil
		}
	case left.Kind() == KindDuration && right.Kind() == KindDuration:
		diff := left.Duration().Seconds() - right.Duration().Seconds()
		switch {
		case diff < 0:
			return -1, true, nil
		case diff > 0:
			return 1, true, nil
		default:
			return 0, true, nil
		}
	case left.Kind() == KindTime && right.Kind() == KindTime:
		switch {
		case left.Time().Before(right.Time()):
			return -1, true, nil
		case left.Time().After(right.Time()):
			return 1, true, nil
		default:
			return 0, true, nil
		}
	default:
		return 0, false, errIncomparableOperands
	}
}

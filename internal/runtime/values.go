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

func hashDisplayKey(val Value) string {
	return value.HashDisplayKey(val)
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

func hashDeleteKey(container, key Value) (Value, bool, error) {
	return container.HashDeleteKey(key)
}

func hashClearEntries(container Value) {
	container.HashClearEntries()
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
		if n, ok := val.CompactInt(); ok {
			return int(n), nil
		}
		// A big integer can never be a valid index; reject it cleanly rather
		// than truncating (Value.Int would silently yield 0).
		return 0, fmt.Errorf("index must fit in a 64-bit integer")
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
			if err := exec.chargeValueKeySteps(arg); err != nil {
				return NewNil(), err
			}
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

// errCompareNaN signals that two numeric operands are unordered because one
// is a NaN. Ordering members surface it; the spaceship operator reports the
// same pair as unordered and yields nil.
var errCompareNaN = errors.New("cannot compare NaN")

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
		if n, ok := val.CompactInt(); ok {
			return int(n), nil
		}
		// A big integer behaves like a float width beyond the int range: out
		// of range rather than silently wrapped.
		return 0, errWidthOutOfRange
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
		n, ok := val.CompactInt()
		if !ok {
			if bi, big := value.BigIntPayload(val); big && bi.Sign() < 0 {
				return 0, errNegativeCount
			}
			return 0, fmt.Errorf("count must fit in a 64-bit integer")
		}
		if n < 0 {
			return 0, errNegativeCount
		}
		return int(n), nil
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
		if bi, ok := value.BigIntPayload(val); ok {
			return bi.Sign(), nil
		}
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

// arraySortCompareValuesWith orders two values for the sort and min/max
// members. It is compareOrderForSort with the unordered case reported as an
// error, because a sort cannot place a value it cannot order, whereas the
// spaceship operator answers nil. It charges the execution for each element a
// recursive array comparison visits.
//
// The state is the caller's, so one sort or extrema pass shares a single memo.
//
// A per-comparison state was the alternative and it made every comparator call
// that saw two arrays allocate a fresh memo and take a fresh reservation --
// whose check walks the whole reachable graph, since builtin dispatch disables
// the base-walk cache. Sorting many small arrays turned into O(n^2 log n)
// graph walking and gigabytes of churn for a memo holding one or two entries.
// Sorting does not mutate its elements, so a pair's result stays valid for the
// whole pass.
func arraySortCompareValuesWith(state *arrayCompareState, left, right Value) (int, error) {
	order, ordered, err := compareOrderForSort(left, right, state)
	if err != nil {
		return 0, err
	}
	if !ordered {
		return 0, errCompareNaN
	}
	return order, nil
}

// flattenState carries the guards and quota hooks for flattenValuesInto, which
// recursively flattens nested arrays up to the specified depth.
// depth=-1 means flatten completely (no limit).
// depth=0 means don't flatten at all.
// depth=1 means flatten one level, etc.
// method names the caller (e.g. "array.flatten" or "hash.flatten") so the depth
// and cycle errors read in terms of the method the script invoked.
type flattenState struct {
	arrays map[sliceIdentity]struct{}
	depth  int
	method string
	// visit, when set, is charged once per element examined, so a flatten over
	// a huge or deeply nested input participates in the step quota and its
	// periodic memory/context checks while the result is still being built.
	visit func() error
	// appendLeaf, when set, appends a leaf element to the shared output slice
	// after charging its slot growth against the memory quota; the hook owns
	// the append so it can meter the backing before it grows.
	appendLeaf func(out []Value, v Value) ([]Value, error)
}

// flattenValuesInto appends the flattened elements of values onto out, sharing
// a single output slice across every recursion level. Building into one shared
// slice (instead of a fresh slice per level merged upward) keeps the transient
// footprint at one backing array, so the quota hooks on flattenState observe
// the build's true peak as it grows. Array#flatten and Hash#flatten drive it
// through arrayFlattenBounded and hashFlattenBounded respectively.
func flattenValuesInto(out, values []Value, depth int, state *flattenState) ([]Value, error) {
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

	for _, v := range values {
		if state.visit != nil {
			if err := state.visit(); err != nil {
				return nil, err
			}
		}
		if v.Kind() == KindArray && depth != 0 {
			nextDepth := depth
			if nextDepth > 0 {
				nextDepth--
			}
			var err error
			out, err = flattenValuesInto(out, v.Array(), nextDepth, state)
			if err != nil {
				return nil, err
			}
			continue
		}
		if state.appendLeaf != nil {
			var err error
			out, err = state.appendLeaf(out, v)
			if err != nil {
				return nil, err
			}
			continue
		}
		out = append(out, v)
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

func arrayJoinByteLenBounded(values []Value, sep string, step func() error) (int, error) {
	return arrayJoinByteLenBoundedWithState(values, sep, step, &joinState{
		arrays: make(map[sliceIdentity]struct{}),
	})
}

func arrayJoinByteLenBoundedWithState(values []Value, sep string, step func() error, state *joinState) (int, error) {
	if state.depth >= maxFlattenDepth {
		return 0, guardLimitErrorf("array.join exceeded maximum depth")
	}

	id := sliceIdentity{
		Ptr: reflect.ValueOf(values).Pointer(),
		Len: len(values),
		Cap: cap(values),
	}
	if id.Ptr != 0 {
		if _, visiting := state.arrays[id]; visiting {
			return 0, fmt.Errorf("array.join does not support cyclic structures")
		}
		state.arrays[id] = struct{}{}
		defer delete(state.arrays, id)
	}

	state.depth++
	defer func() {
		state.depth--
	}()

	total := 0
	for i, v := range values {
		if step != nil {
			if err := step(); err != nil {
				return 0, err
			}
		}
		if i > 0 {
			total = saturatingAdd(total, len(sep))
		}
		if v.Kind() == KindArray {
			n, err := arrayJoinByteLenBoundedWithState(v.Array(), sep, step, state)
			if err != nil {
				return 0, err
			}
			total = saturatingAdd(total, n)
			continue
		}
		if v.Kind() == KindNil {
			continue
		}
		n, err := v.StringByteLenBounded(step)
		if err != nil {
			return 0, err
		}
		total = saturatingAdd(total, n)
	}
	return total, nil
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
		secs, err := int64OperandForDomain(val, method)
		if err != nil {
			return 0, err
		}
		return durationSecondsToTimeDuration(secs, method)
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
		secs, err := int64OperandForDomain(val, method)
		if err != nil {
			return 0, err
		}
		neg, ok := subInt64Checked(0, secs)
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

// concatenableWithString reports whether val has a string form meaningful
// enough to render into a concatenation.
//
// The accepted set mirrors the kinds Value.String renders meaningfully, minus
// nil (which renders as empty) and the composites (which render their contents).
// Everything else falls through String's default and renders as a placeholder
// such as "<object>", "<block>", or "<User instance>", so concatenating it
// produced text that reads like output instead of reporting a mistake --
// "Hello, " + name silently became "Hello, " when name was nil.
//
// It is an allowlist because the rejected set is the larger and more
// open-ended one: a new kind should have to opt into concatenation deliberately
// rather than inherit it and render as a placeholder.
func concatenableWithString(val Value) bool {
	switch val.Kind() {
	case KindString, KindInt, KindFloat, KindBool, KindSymbol,
		KindMoney, KindDuration, KindTime, KindRange, KindRegex, KindEnumValue:
		return true
	default:
		return false
	}
}

func addValues(left, right Value) (Value, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt:
		if l, lok := left.CompactInt(); lok {
			if r, rok := right.CompactInt(); rok {
				if sum, ok := addInt64Checked(l, r); ok {
					return NewInt(sum), nil
				}
			}
		}
		// Compact overflow or a big operand: promote to arbitrary precision.
		return addIntValuesBig(left, right), nil
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
		secs, err := int64OperandForDomain(right, "duration addition")
		if err != nil {
			return NewNil(), err
		}
		sum, ok := addInt64Checked(left.Duration().Seconds(), secs)
		if !ok {
			return NewNil(), int64RangeError("duration addition")
		}
		return NewDuration(durationFromSeconds(sum)), nil
	case right.Kind() == KindDuration && (left.Kind() == KindInt || left.Kind() == KindFloat):
		secs, err := int64OperandForDomain(left, "duration addition")
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
		// Concatenation renders the other operand, which is the idiom the docs
		// and examples use ("total: " + count). It is restricted to operands
		// that have a meaningful string form: nil renders as empty, so
		// "Hello, " + name silently dropped a missing name, and a container
		// renders as its inspect form, so [1] + "a" produced "[1]a" from two
		// values that cannot sensibly concatenate.
		if !concatenableWithString(left) || !concatenableWithString(right) {
			return NewNil(), fmt.Errorf("unsupported addition operands")
		}
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

// shovelValues implements the array shovel operator `array << value`,
// appending the single value to the receiver in place and returning the
// receiver, exactly as Ruby's Array#<< (and Array#push) do. Every alias of the
// array observes the growth, so `values << x` as a bare statement accumulates;
// reassignment is no longer required. Callers with an Execution charge the
// potential backing reallocation first via arrayReserveInPlaceGrowth.
func shovelValues(left, right Value) (Value, error) {
	if left.Kind() != KindArray {
		return NewNil(), fmt.Errorf("unsupported shovel operands")
	}
	left.SetArrayElems(append(left.Array(), right))
	return left, nil
}

// intersectValues implements the array intersection operator `array & other`,
// returning the elements common to both arrays with duplicates removed and the
// left array's order preserved, mirroring Ruby's Array#&. exec meters the
// composite membership probes; nil compares unmetered.
func intersectValues(exec *Execution, left, right Value) (Value, error) {
	if left.Kind() != KindArray || right.Kind() != KindArray {
		return NewNil(), fmt.Errorf("unsupported intersection operands")
	}
	// The scalar element precharge lives here rather than at the operator
	// site so reduce(:&) and symbol-proc forwarding pay it too.
	if exec != nil {
		if err := exec.chargeValueElementKeySteps(left.Array(), right.Array()); err != nil {
			return NewNil(), err
		}
	}
	out, err := intersectArrayValues(exec, left.Array(), right.Array())
	if err != nil {
		return NewNil(), err
	}
	return NewArray(out), nil
}

func subtractValues(exec *Execution, left, right Value) (Value, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt:
		if l, lok := left.CompactInt(); lok {
			if r, rok := right.CompactInt(); rok {
				if diff, ok := subInt64Checked(l, r); ok {
					return NewInt(diff), nil
				}
			}
		}
		return subIntValuesBig(left, right), nil
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
		secs, err := int64OperandForDomain(right, "duration subtraction")
		if err != nil {
			return NewNil(), err
		}
		diff, ok := subInt64Checked(left.Duration().Seconds(), secs)
		if !ok {
			return NewNil(), int64RangeError("duration subtraction")
		}
		return NewDuration(durationFromSeconds(diff)), nil
	case left.Kind() == KindArray && right.Kind() == KindArray:
		// The scalar element precharge lives here rather than at the operator
		// site so reduce(:-) and symbol-proc forwarding pay it too.
		if exec != nil {
			if err := exec.chargeValueElementKeySteps(left.Array(), right.Array()); err != nil {
				return NewNil(), err
			}
		}
		out, err := subtractArrayValues(exec, left.Array(), right.Array())
		if err != nil {
			return NewNil(), err
		}
		return NewArray(out), nil
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

// isFiniteFloat reports whether f can take part in duration scaling. NaN and
// the infinities are rejected upstream by the ordinary integer coercion so
// their long-standing error text is preserved.
func isFiniteFloat(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}

// durationScaleFloatPrec is the working precision for duration float scaling.
// It comfortably exceeds int64's 63 significant bits plus a float64 operand's
// 53, so the seconds operand keeps every bit rather than being rounded on its
// way into the arithmetic.
const durationScaleFloatPrec = 128

// roundedDurationSeconds converts a scaled duration back to the whole seconds a
// Duration stores, rounding to nearest with halves away from zero.
//
// A Duration's resolution is one second, so a fractional result has to land
// somewhere. Rounding is the only option that keeps scaling faithful: truncating
// collapses every factor below one to a zero duration, and a zero duration means
// "immediately" -- a retry with no backoff, a window with no span -- which makes
// truncation the most dangerous direction to round in.
func roundedDurationSeconds(scaled *big.Float, method string) (int64, error) {
	half := new(big.Float).SetPrec(durationScaleFloatPrec).SetFloat64(0.5)
	adjusted := new(big.Float).SetPrec(durationScaleFloatPrec)
	if scaled.Sign() < 0 {
		adjusted.Sub(scaled, half)
	} else {
		adjusted.Add(scaled, half)
	}
	// Int truncates toward zero, so the half added above turns truncation into
	// round-half-away-from-zero.
	whole, _ := adjusted.Int(nil)
	if !whole.IsInt64() {
		return 0, int64RangeError(method)
	}
	return whole.Int64(), nil
}

// durationSecondsAsBigFloat lifts a duration's seconds into the working
// precision without loss. Converting through float64 would silently drop low
// bits above 2^53, which made even `d * 1.0` change the duration.
func durationSecondsAsBigFloat(secs int64) *big.Float {
	return new(big.Float).SetPrec(durationScaleFloatPrec).SetInt64(secs)
}

func floatOperandAsBigFloat(f float64) *big.Float {
	return new(big.Float).SetPrec(durationScaleFloatPrec).SetFloat64(f)
}

// scaleDurationSeconds multiplies a duration's seconds by a numeric factor.
// An integer factor keeps the exact overflow-checked integer path; a float
// factor scales in float space and rounds, so 1.hour * 0.5 is 1800s rather than
// the 0s that truncating the factor to an integer produced.
func scaleDurationSeconds(secs int64, factor Value, method string) (int64, error) {
	if factor.Kind() == KindFloat && isFiniteFloat(factor.Float()) {
		scaled := durationSecondsAsBigFloat(secs)
		scaled.Mul(scaled, floatOperandAsBigFloat(factor.Float()))
		return roundedDurationSeconds(scaled, method)
	}
	// A non-finite factor keeps the existing coercion error ("cannot convert
	// NaN to integer"), so only finite fractional factors change behavior.
	n, err := int64OperandForDomain(factor, method)
	if err != nil {
		return 0, err
	}
	product, ok := mulInt64Checked(secs, n)
	if !ok {
		return 0, int64RangeError(method)
	}
	return product, nil
}

// divideDurationSeconds divides a duration's seconds by a numeric divisor,
// mirroring scaleDurationSeconds. A float divisor divides in float space, so
// 1.hour / 1.5 is 2400s and only a genuine zero divisor reports division by
// zero -- previously 0.5 truncated to 0 and reported one.
func divideDurationSeconds(secs int64, divisor Value, method string) (int64, error) {
	if divisor.Kind() == KindFloat && isFiniteFloat(divisor.Float()) {
		d := divisor.Float()
		if d == 0 {
			return 0, newTypedRuntimeError(runtimeErrorTypeZeroDiv, errors.New("division by zero"))
		}
		scaled := durationSecondsAsBigFloat(secs)
		scaled.Quo(scaled, floatOperandAsBigFloat(d))
		return roundedDurationSeconds(scaled, method)
	}
	// A non-finite divisor keeps the existing coercion error.
	n, err := int64OperandForDomain(divisor, method)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, newTypedRuntimeError(runtimeErrorTypeZeroDiv, errors.New("division by zero"))
	}
	quotient, ok := divInt64Checked(secs, n)
	if !ok {
		return 0, int64RangeError(method)
	}
	return quotient, nil
}

func multiplyValues(left, right Value) (Value, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt:
		if l, lok := left.CompactInt(); lok {
			if r, rok := right.CompactInt(); rok {
				if product, ok := mulInt64Checked(l, r); ok {
					return NewInt(product), nil
				}
			}
		}
		return mulIntValuesBig(left, right), nil
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		return NewFloat(left.Float() * right.Float()), nil
	case left.Kind() == KindDuration && (right.Kind() == KindInt || right.Kind() == KindFloat):
		product, err := scaleDurationSeconds(left.Duration().Seconds(), right, "duration multiplication")
		if err != nil {
			return NewNil(), err
		}
		return NewDuration(durationFromSeconds(product)), nil
	case right.Kind() == KindDuration && (left.Kind() == KindInt || left.Kind() == KindFloat):
		product, err := scaleDurationSeconds(right.Duration().Seconds(), left, "duration multiplication")
		if err != nil {
			return NewNil(), err
		}
		return NewDuration(durationFromSeconds(product)), nil
	case left.Kind() == KindMoney && right.Kind() == KindInt:
		factor, err := moneyIntOperand(right)
		if err != nil {
			return NewNil(), err
		}
		product, err := left.Money().MulInt(factor)
		if err != nil {
			return NewNil(), err
		}
		return NewMoney(product), nil
	case left.Kind() == KindInt && right.Kind() == KindMoney:
		factor, err := moneyIntOperand(left)
		if err != nil {
			return NewNil(), err
		}
		product, err := right.Money().MulInt(factor)
		if err != nil {
			return NewNil(), err
		}
		return NewMoney(product), nil
	default:
		return NewNil(), fmt.Errorf("unsupported multiplication operands")
	}
}

func powerValues(left, right Value) (Value, error) {
	if left.Kind() == KindInt && right.Kind() == KindInt {
		if exp, expOK := right.CompactInt(); expOK {
			if exp >= 0 {
				if base, baseOK := left.CompactInt(); baseOK {
					if result, ok := powInt64Checked(base, exp); ok {
						return NewInt(result), nil
					}
				}
				// Compact overflow or a big base: promote. Callers with an
				// execution context preflight the projected result size before
				// dispatching here (see checkIntPowerGuards).
				return powIntValuesBig(left, exp), nil
			}
			// Negative exponents keep the historical float fallthrough below.
		} else if result, handled, err := powerBigExponent(left, right); handled {
			return result, err
		}
	}
	switch {
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
		if intValueIsZero(right) {
			return NewNil(), newTypedRuntimeError(runtimeErrorTypeZeroDiv, errors.New("division by zero"))
		}
		if l, lok := left.CompactInt(); lok {
			if r, rok := right.CompactInt(); rok {
				if quotient, ok := floorDivIntChecked(l, r); ok {
					return NewInt(quotient), nil
				}
			}
		}
		// MinInt64 / -1 or a big operand: promote, keeping floor semantics.
		return floorDivIntValuesBig(left, right), nil
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
		quotient, err := divideDurationSeconds(left.Duration().Seconds(), right, "duration division")
		if err != nil {
			return NewNil(), err
		}
		return NewDuration(durationFromSeconds(quotient)), nil
	case left.Kind() == KindMoney && right.Kind() == KindInt:
		divisor, err := moneyIntOperand(right)
		if err != nil {
			return NewNil(), err
		}
		res, err := left.Money().DivInt(divisor)
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
		if intValueIsZero(right) {
			return NewNil(), zeroDivisionErrorf("modulo by zero")
		}
		l, lok := left.CompactInt()
		r, rok := right.CompactInt()
		if lok && rok {
			return NewInt(floorModInt(l, r)), nil
		}
		return floorModIntValuesBig(left, right), nil
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

func comparableBetween(method string, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("%s does not take keyword arguments", method)
	}
	if valueBlock(block) != nil {
		return NewNil(), fmt.Errorf("%s does not accept a block", method)
	}
	if len(args) != 2 {
		return NewNil(), fmt.Errorf("%s expects min and max", method)
	}
	lowerOrder, lowerOrdered, err := compareValueOrder(args[0], receiver)
	if err != nil {
		return NewNil(), err
	}
	if !lowerOrdered || lowerOrder > 0 {
		return NewBool(false), nil
	}
	upperOrder, upperOrdered, err := compareValueOrder(receiver, args[1])
	if err != nil {
		return NewNil(), err
	}
	return NewBool(upperOrdered && upperOrder <= 0), nil
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
		l, lok := left.CompactInt()
		r, rok := right.CompactInt()
		if !lok || !rok {
			return compareIntValuesBig(left, right), true, nil
		}
		switch {
		case l < r:
			return -1, true, nil
		case l > r:
			return 1, true, nil
		default:
			return 0, true, nil
		}
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		// Exactly one operand is an int here (int/int is handled above). A big
		// integer compares against the float exactly via big.Float, matching
		// Ruby; compact ints keep the historical float64 conversion.
		if left.Kind() == KindInt && left.IsBigInt() {
			order, ordered = compareIntFloatValues(left, right.Float(), true)
			return order, ordered, nil
		}
		if right.Kind() == KindInt && right.IsBigInt() {
			order, ordered = compareIntFloatValues(right, left.Float(), false)
			return order, ordered, nil
		}
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
		// One pass, not two. Comparing with < and then > scans the common prefix
		// twice, so the operator charge (which bills the shorter operand once)
		// covered half the work actually done.
		return strings.Compare(left.String(), right.String()), true, nil
	// Symbol includes Comparable in Ruby, so both <=> and the relational
	// operators order symbols. sort already did; the operators reported the
	// pair as incomparable, so <=> misdescribed what the language can do.
	case left.Kind() == KindSymbol && right.Kind() == KindSymbol:
		return compareOrderedStrings(left.String(), right.String()), true, nil
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

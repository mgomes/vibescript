package value

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// String returns the human-readable name of the ValueKind.
func (k ValueKind) String() string {
	switch k {
	case KindNil:
		return "nil"
	case KindBool:
		return "bool"
	case KindInt:
		return "int"
	case KindFloat:
		return "float"
	case KindString:
		return "string"
	case KindArray:
		return "array"
	case KindHash:
		return "hash"
	case KindFunction:
		return "function"
	case KindBuiltin:
		return "builtin"
	case KindMoney:
		return "money"
	case KindDuration:
		return "duration"
	case KindTime:
		return "time"
	case KindSymbol:
		return "symbol"
	case KindObject:
		return "object"
	case KindRange:
		return "range"
	case KindBlock:
		return "block"
	case KindEnum:
		return "enum"
	case KindEnumValue:
		return "enum value"
	case KindClass:
		return "class"
	case KindInstance:
		return "instance"
	default:
		return fmt.Sprintf("kind(%d)", int(k))
	}
}

// RuntimeStringer is the hook used by Value.String to format runtime-only
// kinds (function, builtin, block, enum, enum value, class, instance) whose
// payload types live in the vibes package. The vibes package installs this
// hook during initialization. If unset, those kinds fall back to a generic
// rendering of the underlying payload.
var RuntimeStringer func(v Value) (string, bool)

// RuntimeEqualer is the hook used by Value.Equal to compare runtime-only
// kinds whose payload types live in the vibes package. The vibes package
// installs this hook during initialization. If unset, equality for those
// kinds falls back to pointer identity of the underlying payload.
var RuntimeEqualer func(left, right Value) (bool, bool)

// String returns the string representation of v.
func (v Value) String() string {
	switch v.kind {
	case KindString:
		return v.data.(string)
	case KindNil:
		return ""
	case KindBool:
		if v.Bool() {
			return "true"
		}
		return "false"
	case KindInt:
		return strconv.FormatInt(v.Int(), 10)
	case KindFloat:
		return FormatFloat(v.Float())
	case KindSymbol:
		return v.data.(string)
	case KindMoney:
		return v.data.(Money).String()
	case KindDuration:
		return v.Duration().String()
	case KindTime:
		return v.data.(time.Time).Format(time.RFC3339Nano)
	case KindArray, KindHash:
		var buf strings.Builder
		state := newValueStringState()
		// Composite rendering is best-effort and unbounded here; callers that
		// must guard against hostile inputs (such as the CLI rendering a value
		// returned from an untrusted script) use StringBounded instead. The
		// unbounded path never reports the truncation sentinel, so the error is
		// always nil.
		_ = v.appendString(&buf, state, 0)
		return buf.String()
	case KindRange:
		r := v.data.(Range)
		if r.Exclusive {
			return fmt.Sprintf("%d...%d", r.Start, r.End)
		}
		return fmt.Sprintf("%d..%d", r.Start, r.End)
	default:
		if RuntimeStringer != nil {
			if s, ok := RuntimeStringer(v); ok {
				return s
			}
		}
		return fmt.Sprintf("<%v>", v.kind)
	}
}

// FormatFloat renders a float the way Vibescript displays it, matching Ruby's
// Float#to_s. Finite values use Go's shortest round-trippable form, while the
// IEEE special values render as Ruby spells them ("Infinity", "-Infinity",
// "NaN") instead of Go's "+Inf"/"-Inf"/"NaN".
func FormatFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	default:
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
}

// ErrStringRenderTruncated reports that a bounded rendering (StringBounded)
// stopped early because the formatted output would have exceeded the caller's
// byte budget. It lets host-facing rendering refuse to materialize an
// unbounded string for a large composite result instead of allocating until
// the process runs out of memory. Callers detect it with errors.Is.
var ErrStringRenderTruncated = errors.New("value: string rendering exceeded byte limit")

// StringBounded renders v like String but stops once the formatted output
// would exceed limit bytes, returning the partial output and
// ErrStringRenderTruncated. A non-positive limit means unbounded and behaves
// exactly like String. Rendering writes directly into a single growing buffer
// and checks the budget after each element, so a hostile composite cannot
// allocate intermediate per-element strings or a final joined buffer larger
// than roughly limit plus one element before the limit trips. Cycle handling
// is identical to String.
func (v Value) StringBounded(limit int) (string, error) {
	if limit <= 0 {
		return v.String(), nil
	}

	switch v.kind {
	case KindArray, KindHash:
		var buf strings.Builder
		state := newValueStringState()
		if err := v.appendString(&buf, state, limit); err != nil {
			return buf.String(), err
		}
		return buf.String(), nil
	default:
		s := v.String()
		if len(s) > limit {
			return s[:limit], ErrStringRenderTruncated
		}
		return s, nil
	}
}

type valueStringState struct {
	arrays map[SliceIdentity]struct{}
	maps   map[uintptr]struct{}
}

func newValueStringState() *valueStringState {
	return &valueStringState{
		arrays: make(map[SliceIdentity]struct{}),
		maps:   make(map[uintptr]struct{}),
	}
}

// appendString streams v's rendering into buf instead of building intermediate
// per-element slices and a final joined string. When limit is positive it stops
// and returns ErrStringRenderTruncated as soon as buf grows past the limit, so
// large composites trip the budget rather than allocating without bound. A
// non-positive limit renders the whole value.
func (v Value) appendString(buf *strings.Builder, state *valueStringState, limit int) error {
	// Check before descending so a deeply nested composite, whose opening
	// brackets are written before any element-level check runs, trips the
	// budget during the descent rather than after writing one delimiter per
	// level.
	if exceedsStringLimit(buf, limit) {
		return ErrStringRenderTruncated
	}
	switch v.kind {
	case KindArray:
		elems := v.data.([]Value)
		id := SliceIdentity{
			Ptr: reflect.ValueOf(elems).Pointer(),
			Len: len(elems),
			Cap: cap(elems),
		}
		if id.Ptr != 0 {
			if _, seen := state.arrays[id]; seen {
				buf.WriteString("<cycle>")
				return nil
			}
			state.arrays[id] = struct{}{}
			defer delete(state.arrays, id)
		}
		buf.WriteByte('[')
		for i, e := range elems {
			if i > 0 {
				buf.WriteString(", ")
			}
			if err := e.appendString(buf, state, limit); err != nil {
				return err
			}
			if exceedsStringLimit(buf, limit) {
				return ErrStringRenderTruncated
			}
		}
		buf.WriteByte(']')
		return nil
	case KindHash:
		entries := v.data.(map[string]Value)
		if len(entries) == 0 {
			buf.WriteString("{}")
			return nil
		}
		ptr := reflect.ValueOf(entries).Pointer()
		if ptr != 0 {
			if _, seen := state.maps[ptr]; seen {
				buf.WriteString("<cycle>")
				return nil
			}
			state.maps[ptr] = struct{}{}
			defer delete(state.maps, ptr)
		}
		buf.WriteByte('{')
		first := true
		for k, val := range entries {
			if !first {
				buf.WriteString(", ")
			}
			first = false
			// A hash key is an arbitrary string that may itself exceed the
			// budget (host-provided or generated under a raised memory quota),
			// so cap the key write to the remaining budget rather than copying
			// it whole before the value-level check runs.
			if err := appendBounded(buf, k, limit); err != nil {
				return err
			}
			buf.WriteString(": ")
			if err := val.appendString(buf, state, limit); err != nil {
				return err
			}
			if exceedsStringLimit(buf, limit) {
				return ErrStringRenderTruncated
			}
		}
		buf.WriteByte('}')
		return nil
	default:
		// A scalar element may be an arbitrarily large string, so cap its write
		// to the remaining budget instead of materializing the whole value in
		// the buffer before checking the limit.
		return appendBounded(buf, v.String(), limit)
	}
}

func exceedsStringLimit(buf *strings.Builder, limit int) bool {
	return limit > 0 && buf.Len() > limit
}

// appendBounded writes s into buf, but when limit is positive it copies only as
// many bytes as fit within the remaining budget and reports
// ErrStringRenderTruncated instead of materializing an arbitrarily large scalar
// (a long hash key or string element) in the buffer. A non-positive limit
// writes s in full and never reports truncation.
func appendBounded(buf *strings.Builder, s string, limit int) error {
	if limit <= 0 {
		buf.WriteString(s)
		return nil
	}
	remaining := limit - buf.Len()
	if remaining < 0 {
		remaining = 0
	}
	if len(s) > remaining {
		buf.WriteString(s[:remaining])
		return ErrStringRenderTruncated
	}
	buf.WriteString(s)
	return nil
}

// Truthy reports whether v is considered true in a boolean context.
func (v Value) Truthy() bool {
	switch v.kind {
	case KindNil:
		return false
	case KindBool:
		return v.Bool()
	case KindInt:
		return v.Int() != 0
	case KindFloat:
		return v.Float() != 0
	case KindString:
		return v.data.(string) != ""
	case KindArray:
		return len(v.data.([]Value)) > 0
	case KindHash:
		return len(v.data.(map[string]Value)) > 0
	case KindEnum, KindEnumValue, KindClass, KindInstance:
		return true
	default:
		return true
	}
}

// Equal reports whether v and other hold the same kind and value.
func (v Value) Equal(other Value) bool {
	return valuesEqual(v, other, make(map[valueEqualityPair]struct{}))
}

type valueEqualityPair struct {
	kind     ValueKind
	leftPtr  uintptr
	rightPtr uintptr
	leftLen  int
	rightLen int
}

func valuesEqual(v, other Value, seen map[valueEqualityPair]struct{}) bool {
	if v.kind != other.kind {
		return false
	}
	switch v.kind {
	case KindNil:
		return true
	case KindBool:
		return v.Bool() == other.Bool()
	case KindInt:
		return v.Int() == other.Int()
	case KindFloat:
		return v.Float() == other.Float()
	case KindString, KindSymbol:
		return v.data.(string) == other.data.(string)
	case KindMoney:
		return v.data.(Money) == other.data.(Money)
	case KindDuration:
		return v.Duration() == other.Duration()
	case KindTime:
		return v.data.(time.Time).Equal(other.data.(time.Time))
	case KindRange:
		return v.data.(Range) == other.data.(Range)
	case KindArray:
		left := v.Array()
		right := other.Array()
		if len(left) != len(right) {
			return false
		}
		leftID := SliceIdentity{
			Ptr: reflect.ValueOf(left).Pointer(),
			Len: len(left),
			Cap: cap(left),
		}
		rightID := SliceIdentity{
			Ptr: reflect.ValueOf(right).Pointer(),
			Len: len(right),
			Cap: cap(right),
		}
		if leftID.Ptr != 0 && leftID == rightID {
			return true
		}
		pair := valueEqualityPair{
			kind:     KindArray,
			leftPtr:  leftID.Ptr,
			rightPtr: rightID.Ptr,
			leftLen:  len(left),
			rightLen: len(right),
		}
		if pair.leftPtr != 0 || pair.rightPtr != 0 {
			if _, ok := seen[pair]; ok {
				return true
			}
			seen[pair] = struct{}{}
		}
		for i := range left {
			if !valuesEqual(left[i], right[i], seen) {
				return false
			}
		}
		return true
	case KindHash, KindObject:
		left := v.Hash()
		right := other.Hash()
		if len(left) != len(right) {
			return false
		}
		leftPtr := reflect.ValueOf(left).Pointer()
		rightPtr := reflect.ValueOf(right).Pointer()
		if leftPtr != 0 && leftPtr == rightPtr {
			return true
		}
		pair := valueEqualityPair{
			kind:     v.kind,
			leftPtr:  leftPtr,
			rightPtr: rightPtr,
			leftLen:  len(left),
			rightLen: len(right),
		}
		if pair.leftPtr != 0 || pair.rightPtr != 0 {
			if _, ok := seen[pair]; ok {
				return true
			}
			seen[pair] = struct{}{}
		}
		for key, leftValue := range left {
			rightValue, ok := right[key]
			if !ok {
				return false
			}
			if !valuesEqual(leftValue, rightValue, seen) {
				return false
			}
		}
		return true
	default:
		if RuntimeEqualer != nil {
			if result, ok := RuntimeEqualer(v, other); ok {
				return result
			}
		}
		return reflect.DeepEqual(v.data, other.data)
	}
}

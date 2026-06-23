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

// WriteStringTo streams the same bytes String would return for v directly into
// buf, without first materializing the rendered representation as a separate
// string. Callers that have already bounded the rendering against a quota (such
// as the sandbox's interpolation memory guard, which reserves the projected
// length before calling) use it to stream an aggregate straight into their
// builder instead of allocating the full rendering and then copying it, which
// would transiently hold both the temporary and the destination copy and could
// exceed a memory limit the projected length already passed. It delegates to the
// unified unbounded renderer, so writing into a strings.Builder never fails.
func (v Value) WriteStringTo(buf *strings.Builder) {
	_ = v.appendString(buf, newValueStringState(), 0)
}

// appendString streams v's rendering into buf instead of building intermediate
// per-element slices and a final joined string. When limit is positive, every
// write site routes through a bounded helper (appendBounded for strings,
// appendByteBounded for single delimiters), so the buffer never grows past the
// limit: the first write that would exceed the budget stops and returns
// ErrStringRenderTruncated. This keeps the StringBounded byte-budget contract
// intact for callers that consume the returned partial output, and large
// composites trip the budget rather than allocating without bound. A
// non-positive limit renders the whole value.
func (v Value) appendString(buf *strings.Builder, state *valueStringState, limit int) error {
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
				return appendBounded(buf, "<cycle>", limit)
			}
			state.arrays[id] = struct{}{}
			defer delete(state.arrays, id)
		}
		// The opening delimiter counts against the budget like any other byte, so
		// a nested composite whose parent already filled the cap trips the limit
		// here rather than emitting a result one or more bytes over the cap.
		if err := appendByteBounded(buf, '[', limit); err != nil {
			return err
		}
		for i, e := range elems {
			if i > 0 {
				// The element separator counts against the budget like any other
				// byte, so a packed array trips the limit on the separator rather
				// than emitting a result over the cap.
				if err := appendBounded(buf, ", ", limit); err != nil {
					return err
				}
			}
			if err := e.appendString(buf, state, limit); err != nil {
				return err
			}
		}
		// The closing delimiter still counts against the budget: an array that
		// fills the budget exactly with its elements must trip the limit rather
		// than emit a result one byte over the cap.
		return appendByteBounded(buf, ']', limit)
	case KindHash:
		entries := v.data.(map[string]Value)
		if len(entries) == 0 {
			return appendBounded(buf, "{}", limit)
		}
		ptr := reflect.ValueOf(entries).Pointer()
		if ptr != 0 {
			if _, seen := state.maps[ptr]; seen {
				return appendBounded(buf, "<cycle>", limit)
			}
			state.maps[ptr] = struct{}{}
			defer delete(state.maps, ptr)
		}
		// The opening delimiter counts against the budget like any other byte; see
		// the array opening delimiter above.
		if err := appendByteBounded(buf, '{', limit); err != nil {
			return err
		}
		first := true
		for k, val := range entries {
			if !first {
				// The entry separator counts against the budget like any other
				// byte, so a packed hash trips the limit on the separator rather
				// than emitting a result over the cap.
				if err := appendBounded(buf, ", ", limit); err != nil {
					return err
				}
			}
			first = false
			// A hash key is an arbitrary string that may itself exceed the
			// budget (host-provided or generated under a raised memory quota),
			// so cap the key write to the remaining budget rather than copying
			// it whole before the value-level check runs.
			if err := appendBounded(buf, k, limit); err != nil {
				return err
			}
			// The key/value separator counts against the budget too: a key that
			// fills the budget exactly must trip the limit here rather than let
			// ": " push the result past the cap.
			if err := appendBounded(buf, ": ", limit); err != nil {
				return err
			}
			if err := val.appendString(buf, state, limit); err != nil {
				return err
			}
		}
		// See the array closing delimiter above: the trailing brace counts
		// against the budget too.
		return appendByteBounded(buf, '}', limit)
	default:
		// A scalar element may be an arbitrarily large string, so cap its write
		// to the remaining budget instead of materializing the whole value in
		// the buffer before checking the limit.
		return appendBounded(buf, v.String(), limit)
	}
}

// appendByteBounded writes a single delimiter byte into buf, but when limit is
// positive it refuses to write past the budget and reports
// ErrStringRenderTruncated instead. Delimiters count against the cap like any
// other byte, so a composite that fills its budget exactly with its contents
// must trip the limit rather than emit a result one byte over the cap.
func appendByteBounded(buf *strings.Builder, b byte, limit int) error {
	if limit > 0 && buf.Len() >= limit {
		return ErrStringRenderTruncated
	}
	buf.WriteByte(b)
	return nil
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

// StringByteLen returns the number of bytes String would produce for v without
// materializing the rendered representation. Callers that must bound an
// allocation before it happens (such as the sandbox's interpolation memory
// guard) use it to reject an oversized rendering instead of building the string
// first and only then observing that it exceeded a quota. The byte count walks
// arrays and hashes with the same cycle detection String uses, so the projection
// matches the eventual output exactly.
func (v Value) StringByteLen() int {
	switch v.kind {
	case KindArray, KindHash:
		return v.stringByteLenWithState(newValueStringState())
	default:
		return len(v.String())
	}
}

func (v Value) stringByteLenWithState(state *valueStringState) int {
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
				return len(cycleMarker)
			}
			state.arrays[id] = struct{}{}
			defer delete(state.arrays, id)
		}
		// "[" + elements joined by ", " + "]".
		total := len(arrayOpen) + len(arrayClose)
		total += separatorBytes(len(elems))
		for _, e := range elems {
			total += e.stringByteLenWithState(state)
		}
		return total
	case KindHash:
		entries := v.data.(map[string]Value)
		if len(entries) == 0 {
			return len(hashOpen) + len(hashClose)
		}
		ptr := reflect.ValueOf(entries).Pointer()
		if ptr != 0 {
			if _, seen := state.maps[ptr]; seen {
				return len(cycleMarker)
			}
			state.maps[ptr] = struct{}{}
			defer delete(state.maps, ptr)
		}
		// "{" + entries joined by ", " + "}"; each entry is key + ": " + value.
		total := len(hashOpen) + len(hashClose)
		total += separatorBytes(len(entries))
		for k, val := range entries {
			total += len(k) + len(keyValueSeparator)
			total += val.stringByteLenWithState(state)
		}
		return total
	default:
		return len(v.String())
	}
}

// separatorBytes returns the bytes the ", " separators contribute when joining
// count elements: zero for fewer than two elements, otherwise two bytes per gap.
func separatorBytes(count int) int {
	if count < 2 {
		return 0
	}
	return (count - 1) * len(elementSeparator)
}

const (
	arrayOpen         = "["
	arrayClose        = "]"
	hashOpen          = "{"
	hashClose         = "}"
	elementSeparator  = ", "
	keyValueSeparator = ": "
	cycleMarker       = "<cycle>"
)

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

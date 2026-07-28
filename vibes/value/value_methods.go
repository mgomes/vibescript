package value

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
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
	case KindRegex:
		return "regex"
	case KindShape:
		return "shape"
	default:
		return fmt.Sprintf("kind(%d)", int(k))
	}
}

// RuntimeStringer is the hook used by Value.String to format runtime-only
// kinds (function, builtin, block, enum, enum value, class, instance) whose
// payload types live in the vibes package. The vibes package installs this
// hook during initialization. If unset, those kinds fall back to a generic
// rendering of the underlying payload.
// It is intended for the interpreter's internal use; hosts should not rely
// on it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
var RuntimeStringer func(v Value) (string, bool)

// RuntimeStringLen reports the byte length Value.String would return for a
// runtime-only kind, computed from the payload rather than by building the
// string. A projection that answers through RuntimeStringer allocates the very
// rendering it is meant to decide about, which defeats the guard.
// It is intended for the interpreter's internal use; hosts should not rely
// on it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
var RuntimeStringLen func(v Value) (int, bool)

// RuntimeStringAppender writes the bytes Value.String would return for a
// runtime-only kind straight into buf, so a rendering streamed into a caller's
// charged buffer never also exists as a temporary alongside it.
//
// limit is the total byte budget for buf, matching appendBounded: a
// non-positive limit writes everything, and otherwise the hook writes at most
// limit-buf.Len() bytes and reports truncated when it had more to write. This
// keeps precision-qualified formats -- format("%.1s", Huge::Member) -- from
// materializing a whole rendering to throw nearly all of it away.
// It is intended for the interpreter's internal use; hosts should not rely
// on it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
var RuntimeStringAppender func(v Value, buf *strings.Builder, limit int) (truncated, handled bool)

// RuntimeStringRuneLen reports the rune count Value.String would return for a
// runtime-only kind, counted from the payload rather than from a materialized
// rendering. Width-qualified formatting projects rune lengths, so this is the
// same guard RuntimeStringLen provides for the byte-length paths.
// It is intended for the interpreter's internal use; hosts should not rely
// on it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
var RuntimeStringRuneLen func(v Value) (int, bool)

// RuntimeEqualer is the hook used by Value.Equal to compare runtime-only
// kinds whose payload types live in the vibes package. The vibes package
// installs this hook during initialization. If unset, equality for those
// kinds falls back to pointer identity of the underlying payload.
// It is intended for the interpreter's internal use; hosts should not rely
// on it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
var RuntimeEqualer func(left, right Value) (bool, bool)

// RuntimeIdenticaler is the hook used by Value.Identical to compare
// runtime-only kinds by backing-storage identity. It differs from
// RuntimeEqualer because some runtime kinds (notably enums and enum values)
// define Equal as structural equivalence: two independently cloned enum
// members that share an owner script and name compare Equal, yet they do not
// share storage and so must not be Identical. The vibes package installs this
// hook during initialization. If unset, identity for those kinds falls back to
// the same comparison Value.Equal uses.
// It is intended for the interpreter's internal use; hosts should not rely
// on it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
var RuntimeIdenticaler func(left, right Value) (bool, bool)

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
		if bi, ok := v.data.(*big.Int); ok {
			// Base conversion is superlinear in the value's size; sandboxed
			// rendering paths preflight the digit count before reaching here
			// (see the bounded renderers and the runtime's rendering guards).
			return bi.Text(10)
		}
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
	case KindRegex:
		return v.data.(Regex).String()
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
		return v.data.(Range).String()
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
		// A big integer whose rendering provably exceeds the budget is refused
		// before the (superlinear) base conversion runs; the partial output is
		// empty because no digits were ever materialized.
		if bigIntRenderExceedsLimit(v, limit) {
			return "", ErrStringRenderTruncated
		}
		if RuntimeStringAppender != nil {
			var buf strings.Builder
			if truncated, handled := RuntimeStringAppender(v, &buf, limit); handled {
				if truncated {
					return buf.String(), ErrStringRenderTruncated
				}
				return buf.String(), nil
			}
		}
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
// It is intended for the interpreter's internal use; hosts should not call
// it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
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
		elems := v.Array()
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
		if typed := v.data.(*hashData).typedEntries; typed != nil {
			return v.appendStringTypedHash(buf, state, limit, typed)
		}
		entries := v.hashEntries()
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
		// the buffer before checking the limit. A big integer that provably
		// cannot fit the remaining budget is refused before the (superlinear)
		// base conversion ever runs.
		// The hook streams straight into buf and honors the budget itself, so
		// neither an unbounded write nor a truncated one materializes the
		// whole rendering first.
		if RuntimeStringAppender != nil {
			if truncated, handled := RuntimeStringAppender(v, buf, limit); handled {
				if truncated {
					return ErrStringRenderTruncated
				}
				return nil
			}
		}
		if limit > 0 && bigIntRenderExceedsLimit(v, limit-buf.Len()) {
			return ErrStringRenderTruncated
		}
		return appendBounded(buf, v.String(), limit)
	}
}

func (v Value) appendStringTypedHash(buf *strings.Builder, state *valueStringState, limit int, entries map[HashLookupKey]HashEntry) error {
	if len(entries) == 0 {
		return appendBounded(buf, "{}", limit)
	}
	id := HashIdentity(v)
	if id != 0 {
		if _, seen := state.maps[id]; seen {
			return appendBounded(buf, cycleMarker, limit)
		}
		state.maps[id] = struct{}{}
		defer delete(state.maps, id)
	}
	if err := appendByteBounded(buf, '{', limit); err != nil {
		return err
	}
	first := true
	if err := v.data.(*hashData).forEachTypedEntry(func(entry HashEntry) error {
		if !first {
			if err := appendBounded(buf, elementSeparator, limit); err != nil {
				return err
			}
		}
		first = false
		if err := appendStringHashEntryKey(buf, entry.Key, state, limit); err != nil {
			return err
		}
		if err := appendBounded(buf, keyValueSeparator, limit); err != nil {
			return err
		}
		return entry.Value.appendString(buf, state, limit)
	}); err != nil {
		return err
	}
	return appendByteBounded(buf, '}', limit)
}

func appendStringHashEntryKey(buf *strings.Builder, key Value, state *valueStringState, limit int) error {
	switch key.kind {
	case KindString, KindSymbol:
		return appendBounded(buf, key.String(), limit)
	default:
		return key.appendInspect(buf, state, limit)
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
// It is intended for the interpreter's internal use; hosts should not call
// it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (v Value) StringByteLen() int {
	switch v.kind {
	case KindArray, KindHash:
		return v.stringByteLenWithState(newValueStringState())
	default:
		if RuntimeStringLen != nil {
			if n, ok := RuntimeStringLen(v); ok {
				return n
			}
		}
		return len(v.String())
	}
}

// StringRuneLen returns the number of runes String would produce for v without
// materializing the rendered representation. It mirrors StringByteLen but counts
// display width in the unit fmt uses for string precision and padding.
// It is intended for the interpreter's internal use; hosts should not call
// it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (v Value) StringRuneLen() int {
	switch v.kind {
	case KindArray, KindHash:
		return v.stringRuneLenWithState(newValueStringState())
	default:
		if RuntimeStringRuneLen != nil {
			if n, ok := RuntimeStringRuneLen(v); ok {
				return n
			}
		}
		return utf8.RuneCountInString(v.String())
	}
}

func (v Value) stringByteLenWithState(state *valueStringState) int {
	switch v.kind {
	case KindArray:
		elems := v.Array()
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
		if typed := v.data.(*hashData).typedEntries; typed != nil {
			return v.typedHashStringByteLenWithState(state, typed)
		}
		entries := v.hashEntries()
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
		if RuntimeStringLen != nil {
			if n, ok := RuntimeStringLen(v); ok {
				return n
			}
		}
		return len(v.String())
	}
}

func (v Value) typedHashStringByteLenWithState(state *valueStringState, entries map[HashLookupKey]HashEntry) int {
	if len(entries) == 0 {
		return len(hashOpen) + len(hashClose)
	}
	id := HashIdentity(v)
	if id != 0 {
		if _, seen := state.maps[id]; seen {
			return len(cycleMarker)
		}
		state.maps[id] = struct{}{}
		defer delete(state.maps, id)
	}
	total := len(hashOpen) + len(hashClose)
	total += separatorBytes(len(entries))
	for _, entry := range entries {
		total += hashStringEntryKeyByteLenWithState(entry.Key, state) + len(keyValueSeparator)
		total += entry.Value.stringByteLenWithState(state)
	}
	return total
}

func hashStringEntryKeyByteLenWithState(key Value, state *valueStringState) int {
	switch key.kind {
	case KindString, KindSymbol:
		return len(key.String())
	default:
		return key.inspectByteLenWithState(state)
	}
}

func (v Value) stringRuneLenWithState(state *valueStringState) int {
	switch v.kind {
	case KindArray:
		elems := v.Array()
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
		total := len(arrayOpen) + len(arrayClose)
		total += separatorBytes(len(elems))
		for _, e := range elems {
			total += e.stringRuneLenWithState(state)
		}
		return total
	case KindHash:
		if typed := v.data.(*hashData).typedEntries; typed != nil {
			return v.typedHashStringRuneLenWithState(state, typed)
		}
		entries := v.hashEntries()
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
		total := len(hashOpen) + len(hashClose)
		total += separatorBytes(len(entries))
		for k, val := range entries {
			total += utf8.RuneCountInString(k) + len(keyValueSeparator)
			total += val.stringRuneLenWithState(state)
		}
		return total
	default:
		if RuntimeStringRuneLen != nil {
			if n, ok := RuntimeStringRuneLen(v); ok {
				return n
			}
		}
		return utf8.RuneCountInString(v.String())
	}
}

func (v Value) typedHashStringRuneLenWithState(state *valueStringState, entries map[HashLookupKey]HashEntry) int {
	if len(entries) == 0 {
		return len(hashOpen) + len(hashClose)
	}
	id := HashIdentity(v)
	if id != 0 {
		if _, seen := state.maps[id]; seen {
			return len(cycleMarker)
		}
		state.maps[id] = struct{}{}
		defer delete(state.maps, id)
	}
	total := len(hashOpen) + len(hashClose)
	total += separatorBytes(len(entries))
	for _, entry := range entries {
		total += hashStringEntryKeyRuneLenWithState(entry.Key, state) + len(keyValueSeparator)
		total += entry.Value.stringRuneLenWithState(state)
	}
	return total
}

func hashStringEntryKeyRuneLenWithState(key Value, state *valueStringState) int {
	switch key.kind {
	case KindString, KindSymbol:
		return utf8.RuneCountInString(key.String())
	default:
		var buf strings.Builder
		if err := key.appendInspect(&buf, state, 0); err != nil {
			return 0
		}
		return utf8.RuneCountInString(buf.String())
	}
}

// StringByteLenBounded reports the same byte count as StringByteLen but invokes
// step once per node visited during the projection walk, so a caller can charge
// a sandbox step budget against the traversal and abort it when step returns an
// error. The first error step reports stops the walk and is returned unchanged
// alongside the partial count.
//
// StringByteLen's cycle detection only collapses references that are currently
// on the recursion stack: a shared but acyclic graph (for example the result of
// repeatedly evaluating a = [a, a], where each level holds two references to the
// same child slice) is fully re-walked at every occurrence, so the traversal is
// exponential in the nesting depth even though the value's memory and its
// eventual rendering both stay bounded by the cycle marker. The memory quota
// alone cannot bound that work because it is checked only after the walk
// completes. Driving step from inside the walk lets the step quota trip during
// the traversal instead of letting it run unbounded.
// It is intended for the interpreter's internal use; hosts should not call
// it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (v Value) StringByteLenBounded(step func() error) (int, error) {
	switch v.kind {
	case KindArray, KindHash:
		return v.stringByteLenBoundedWithState(newValueStringState(), step)
	default:
		if err := step(); err != nil {
			return 0, err
		}
		// A big integer's projection performs the same superlinear base
		// conversion the rendering will; charge steps for it up front so the
		// step quota trips before the conversion runs.
		if err := chargeBigIntRenderSteps(v, step); err != nil {
			return 0, err
		}
		if RuntimeStringLen != nil {
			if n, ok := RuntimeStringLen(v); ok {
				return n, nil
			}
		}
		return len(v.String()), nil
	}
}

// StringRuneLenBounded reports the same rune count as StringRuneLen but invokes
// step once per node visited during the projection walk, matching
// StringByteLenBounded's sandbox accounting.
// It is intended for the interpreter's internal use; hosts should not call
// it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (v Value) StringRuneLenBounded(step func() error) (int, error) {
	switch v.kind {
	case KindArray, KindHash:
		return v.stringRuneLenBoundedWithState(newValueStringState(), step)
	default:
		if err := step(); err != nil {
			return 0, err
		}
		if err := chargeBigIntRenderSteps(v, step); err != nil {
			return 0, err
		}
		if RuntimeStringRuneLen != nil {
			if n, ok := RuntimeStringRuneLen(v); ok {
				return n, nil
			}
		}
		return utf8.RuneCountInString(v.String()), nil
	}
}

// StringByteLenBoundedUpTo reports String's byte length up to limit bytes. It
// stops as soon as it can prove the rendering would exceed limit, returning
// truncated as true and limit+1 as the count. Like StringByteLenBounded, it
// invokes step during aggregate walks so callers can charge sandbox work before
// materializing any rendering.
// It is intended for the interpreter's internal use; hosts should not call
// it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (v Value) StringByteLenBoundedUpTo(limit int, step func() error) (count int, truncated bool, err error) {
	if limit < 0 {
		n, err := v.StringByteLenBounded(step)
		return n, false, err
	}
	return v.stringByteLenBoundedUpToWithState(newValueStringState(), limit, step)
}

func (v Value) stringByteLenBoundedWithState(state *valueStringState, step func() error) (int, error) {
	if err := step(); err != nil {
		return 0, err
	}
	switch v.kind {
	case KindArray:
		elems := v.Array()
		id := SliceIdentity{
			Ptr: reflect.ValueOf(elems).Pointer(),
			Len: len(elems),
			Cap: cap(elems),
		}
		if id.Ptr != 0 {
			if _, seen := state.arrays[id]; seen {
				return len(cycleMarker), nil
			}
			state.arrays[id] = struct{}{}
			defer delete(state.arrays, id)
		}
		// "[" + elements joined by ", " + "]".
		total := len(arrayOpen) + len(arrayClose)
		total += separatorBytes(len(elems))
		for _, e := range elems {
			n, err := e.stringByteLenBoundedWithState(state, step)
			if err != nil {
				return 0, err
			}
			total += n
		}
		return total, nil
	case KindHash:
		if typed := v.data.(*hashData).typedEntries; typed != nil {
			return v.typedHashStringByteLenBoundedWithState(state, step, typed)
		}
		entries := v.hashEntries()
		if len(entries) == 0 {
			return len(hashOpen) + len(hashClose), nil
		}
		ptr := reflect.ValueOf(entries).Pointer()
		if ptr != 0 {
			if _, seen := state.maps[ptr]; seen {
				return len(cycleMarker), nil
			}
			state.maps[ptr] = struct{}{}
			defer delete(state.maps, ptr)
		}
		// "{" + entries joined by ", " + "}"; each entry is key + ": " + value.
		total := len(hashOpen) + len(hashClose)
		total += separatorBytes(len(entries))
		for k, val := range entries {
			total += len(k) + len(keyValueSeparator)
			n, err := val.stringByteLenBoundedWithState(state, step)
			if err != nil {
				return 0, err
			}
			total += n
		}
		return total, nil
	default:
		if err := chargeBigIntRenderSteps(v, step); err != nil {
			return 0, err
		}
		if RuntimeStringLen != nil {
			if n, ok := RuntimeStringLen(v); ok {
				return n, nil
			}
		}
		return len(v.String()), nil
	}
}

func (v Value) typedHashStringByteLenBoundedWithState(state *valueStringState, step func() error, entries map[HashLookupKey]HashEntry) (int, error) {
	if len(entries) == 0 {
		return len(hashOpen) + len(hashClose), nil
	}
	id := HashIdentity(v)
	if id != 0 {
		if _, seen := state.maps[id]; seen {
			return len(cycleMarker), nil
		}
		state.maps[id] = struct{}{}
		defer delete(state.maps, id)
	}
	total := len(hashOpen) + len(hashClose)
	total += separatorBytes(len(entries))
	for _, entry := range entries {
		n, err := hashStringEntryKeyByteLenBoundedWithState(entry.Key, state, step)
		if err != nil {
			return 0, err
		}
		total += n + len(keyValueSeparator)
		valueBytes, err := entry.Value.stringByteLenBoundedWithState(state, step)
		if err != nil {
			return 0, err
		}
		total += valueBytes
	}
	return total, nil
}

func hashStringEntryKeyByteLenBoundedWithState(key Value, state *valueStringState, step func() error) (int, error) {
	switch key.kind {
	case KindString, KindSymbol:
		if err := step(); err != nil {
			return 0, err
		}
		return len(key.String()), nil
	default:
		return key.inspectByteLenBoundedWithState(state, step)
	}
}

func (v Value) stringRuneLenBoundedWithState(state *valueStringState, step func() error) (int, error) {
	if err := step(); err != nil {
		return 0, err
	}
	switch v.kind {
	case KindArray:
		elems := v.Array()
		id := SliceIdentity{
			Ptr: reflect.ValueOf(elems).Pointer(),
			Len: len(elems),
			Cap: cap(elems),
		}
		if id.Ptr != 0 {
			if _, seen := state.arrays[id]; seen {
				return len(cycleMarker), nil
			}
			state.arrays[id] = struct{}{}
			defer delete(state.arrays, id)
		}
		total := len(arrayOpen) + len(arrayClose)
		total += separatorBytes(len(elems))
		for _, e := range elems {
			n, err := e.stringRuneLenBoundedWithState(state, step)
			if err != nil {
				return 0, err
			}
			total += n
		}
		return total, nil
	case KindHash:
		if typed := v.data.(*hashData).typedEntries; typed != nil {
			return v.typedHashStringRuneLenBoundedWithState(state, step, typed)
		}
		entries := v.hashEntries()
		if len(entries) == 0 {
			return len(hashOpen) + len(hashClose), nil
		}
		ptr := reflect.ValueOf(entries).Pointer()
		if ptr != 0 {
			if _, seen := state.maps[ptr]; seen {
				return len(cycleMarker), nil
			}
			state.maps[ptr] = struct{}{}
			defer delete(state.maps, ptr)
		}
		total := len(hashOpen) + len(hashClose)
		total += separatorBytes(len(entries))
		for k, val := range entries {
			total += utf8.RuneCountInString(k) + len(keyValueSeparator)
			n, err := val.stringRuneLenBoundedWithState(state, step)
			if err != nil {
				return 0, err
			}
			total += n
		}
		return total, nil
	default:
		if err := chargeBigIntRenderSteps(v, step); err != nil {
			return 0, err
		}
		if RuntimeStringRuneLen != nil {
			if n, ok := RuntimeStringRuneLen(v); ok {
				return n, nil
			}
		}
		return utf8.RuneCountInString(v.String()), nil
	}
}

func (v Value) typedHashStringRuneLenBoundedWithState(state *valueStringState, step func() error, entries map[HashLookupKey]HashEntry) (int, error) {
	if len(entries) == 0 {
		return len(hashOpen) + len(hashClose), nil
	}
	id := HashIdentity(v)
	if id != 0 {
		if _, seen := state.maps[id]; seen {
			return len(cycleMarker), nil
		}
		state.maps[id] = struct{}{}
		defer delete(state.maps, id)
	}
	total := len(hashOpen) + len(hashClose)
	total += separatorBytes(len(entries))
	for _, entry := range entries {
		n, err := hashStringEntryKeyRuneLenBoundedWithState(entry.Key, state, step)
		if err != nil {
			return 0, err
		}
		total += n + len(keyValueSeparator)
		valueRunes, err := entry.Value.stringRuneLenBoundedWithState(state, step)
		if err != nil {
			return 0, err
		}
		total += valueRunes
	}
	return total, nil
}

func hashStringEntryKeyRuneLenBoundedWithState(key Value, state *valueStringState, step func() error) (int, error) {
	switch key.kind {
	case KindString, KindSymbol:
		if err := step(); err != nil {
			return 0, err
		}
		return utf8.RuneCountInString(key.String()), nil
	default:
		if _, err := key.inspectByteLenBoundedWithState(state, step); err != nil {
			return 0, err
		}
		return hashStringEntryKeyRuneLenWithState(key, state), nil
	}
}

func (v Value) stringByteLenBoundedUpToWithState(state *valueStringState, limit int, step func() error) (int, bool, error) {
	if err := step(); err != nil {
		return 0, false, err
	}
	switch v.kind {
	case KindArray:
		elems := v.Array()
		id := SliceIdentity{
			Ptr: reflect.ValueOf(elems).Pointer(),
			Len: len(elems),
			Cap: cap(elems),
		}
		if id.Ptr != 0 {
			if _, seen := state.arrays[id]; seen {
				total, truncated := stringByteLenCappedAdd(0, len(cycleMarker), limit)
				return total, truncated, nil
			}
			state.arrays[id] = struct{}{}
			defer delete(state.arrays, id)
		}
		total, truncated := stringByteLenCappedAdd(0, len(arrayOpen)+len(arrayClose)+separatorBytes(len(elems)), limit)
		if truncated {
			return total, true, nil
		}
		for _, e := range elems {
			n, childTruncated, err := e.stringByteLenBoundedUpToWithState(state, limit-total, step)
			if err != nil {
				return 0, false, err
			}
			var addTruncated bool
			total, addTruncated = stringByteLenCappedAdd(total, n, limit)
			if childTruncated || addTruncated {
				return total, true, nil
			}
		}
		return total, false, nil
	case KindHash:
		if typed := v.data.(*hashData).typedEntries; typed != nil {
			return v.typedHashStringByteLenBoundedUpToWithState(state, limit, step, typed)
		}
		entries := v.hashEntries()
		if len(entries) == 0 {
			total, truncated := stringByteLenCappedAdd(0, len(hashOpen)+len(hashClose), limit)
			return total, truncated, nil
		}
		ptr := reflect.ValueOf(entries).Pointer()
		if ptr != 0 {
			if _, seen := state.maps[ptr]; seen {
				total, truncated := stringByteLenCappedAdd(0, len(cycleMarker), limit)
				return total, truncated, nil
			}
			state.maps[ptr] = struct{}{}
			defer delete(state.maps, ptr)
		}
		total, truncated := stringByteLenCappedAdd(0, len(hashOpen)+len(hashClose)+separatorBytes(len(entries)), limit)
		if truncated {
			return total, true, nil
		}
		for k, val := range entries {
			var keyTruncated bool
			total, keyTruncated = stringByteLenCappedAdd(total, len(k)+len(keyValueSeparator), limit)
			if keyTruncated {
				return total, true, nil
			}
			n, childTruncated, err := val.stringByteLenBoundedUpToWithState(state, limit-total, step)
			if err != nil {
				return 0, false, err
			}
			var addTruncated bool
			total, addTruncated = stringByteLenCappedAdd(total, n, limit)
			if childTruncated || addTruncated {
				return total, true, nil
			}
		}
		return total, false, nil
	default:
		// A big integer that provably exceeds the limit reports truncation
		// without paying for the base conversion; anything else is measured
		// exactly after charging steps for the conversion work.
		if bigIntRenderExceedsLimit(v, limit) {
			return limit + 1, true, nil
		}
		if err := chargeBigIntRenderSteps(v, step); err != nil {
			return 0, false, err
		}
		if RuntimeStringLen != nil {
			if n, ok := RuntimeStringLen(v); ok {
				total, truncated := stringByteLenCappedAdd(0, n, limit)
				return total, truncated, nil
			}
		}
		total, truncated := stringByteLenCappedAdd(0, len(v.String()), limit)
		return total, truncated, nil
	}
}

func (v Value) typedHashStringByteLenBoundedUpToWithState(state *valueStringState, limit int, step func() error, entries map[HashLookupKey]HashEntry) (int, bool, error) {
	if len(entries) == 0 {
		total, truncated := stringByteLenCappedAdd(0, len(hashOpen)+len(hashClose), limit)
		return total, truncated, nil
	}
	id := HashIdentity(v)
	if id != 0 {
		if _, seen := state.maps[id]; seen {
			total, truncated := stringByteLenCappedAdd(0, len(cycleMarker), limit)
			return total, truncated, nil
		}
		state.maps[id] = struct{}{}
		defer delete(state.maps, id)
	}
	total, truncated := stringByteLenCappedAdd(0, len(hashOpen)+len(hashClose)+separatorBytes(len(entries)), limit)
	if truncated {
		return total, true, nil
	}
	for _, entry := range entries {
		n, err := hashStringEntryKeyByteLenBoundedWithState(entry.Key, state, step)
		if err != nil {
			return 0, false, err
		}
		var keyTruncated bool
		total, keyTruncated = stringByteLenCappedAdd(total, n+len(keyValueSeparator), limit)
		if keyTruncated {
			return total, true, nil
		}
		valueBytes, childTruncated, err := entry.Value.stringByteLenBoundedUpToWithState(state, limit-total, step)
		if err != nil {
			return 0, false, err
		}
		var addTruncated bool
		total, addTruncated = stringByteLenCappedAdd(total, valueBytes, limit)
		if childTruncated || addTruncated {
			return total, true, nil
		}
	}
	return total, false, nil
}

func stringByteLenCappedAdd(total, n, limit int) (int, bool) {
	if n > limit-total {
		return limit + 1, true
	}
	return total + n, false
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
	default:
		return true
	}
}

// Eql reports whether v and other are equal under hash-key semantics: they
// must share the same kind and compare equal, so an Int never eql-matches a
// Float even when their numeric values coincide. It backs the Ruby-style
// `eql?` predicate. Because Equal already requires matching kinds (Vibescript
// `==` performs no cross-kind numeric coercion), Eql currently coincides with
// Equal; it exists as a distinct, documented contract aligned with hash-key
// equality rather than with broad value equivalence.
func (v Value) Eql(other Value) bool {
	if v.kind != other.kind {
		return false
	}
	var ctx EqualityContext
	if ctx.seen != nil {
		clear(ctx.seen)
	}
	return valuesEqualWithKinds(v, other, &ctx.seen, true)
}

// Identical reports whether v and other refer to the same object, backing the
// Ruby-style `equal?` predicate. Immutable value kinds (nil, bool, int, float,
// string, symbol, money, duration, time, range) are identical when they share
// the same kind and value, since the language exposes no distinct identities
// for equal immutables. Integers outside the int64 range are the exception:
// they are heap objects and compare by payload identity, matching Ruby, where
// bignums are separate objects. Mutable composites (array, hash, object) and
// runtime-only kinds (function, builtin, block, class, instance, enum, enum
// value) are identical only when they share the same backing storage, so two
// independently constructed composites with equal contents are not identical.
//
// NaN floats are the one immutable case where value equality is not enough:
// IEEE NaN != NaN, so deferring to Equal would make x.equal?(x) false for a NaN
// receiver and break reflexivity. Identity treats any two NaN floats as
// identical, keeping equal? reflexive while matching the value-identity model
// floats already follow.
//
// Empty arrays are the one principled exception: any two empty arrays report
// identical regardless of their backing storage. This is harmless because an
// empty array has no element storage to alias — appending to one never affects
// another — so they behave as a single value-like empty rather than as distinct
// mutable objects. Backing pointers alone cannot establish this, because an
// empty result preallocated with spare capacity (for example array.select
// starting from make([]Value, 0, len(arr))) carries its own non-zerobase pointer
// and a different capacity than a literal []; only a length check captures the
// contract. Empty hashes and objects stay distinct because every hash carries
// its own hashData wrapper (NewHash allocates a fresh one per call) and NewObject
// allocates a fresh backing map for nil input, so each empty composite has a
// distinct identity rather than collapsing onto a shared zero pointer.
func (v Value) Identical(other Value) bool {
	if v.kind != other.kind {
		return false
	}
	switch v.kind {
	case KindInt:
		// Compact integers keep value identity like every immutable scalar. A
		// big integer is a heap object, so identity is the payload pointer:
		// two independently produced big values with equal contents are equal
		// but not identical, matching Ruby, where bignums are separate objects
		// while fixnums are value-identical. A mixed pair is never identical
		// (the canonical invariant keeps the value spaces disjoint).
		vBig, vOK := v.data.(*big.Int)
		oBig, oOK := other.data.(*big.Int)
		if vOK || oOK {
			return vOK && oOK && vBig == oBig
		}
		return v.Int() == other.Int()
	case KindFloat:
		// Float identity is value identity in Vibescript: the language exposes no
		// distinct object for two floats with the same value, so 1.5.equal?(1.5)
		// is true. IEEE equality would break the identity contract's reflexivity
		// for NaN (NaN == NaN is false), so treat any two NaN floats as identical;
		// every other float defers to value equality.
		if math.IsNaN(v.Float()) || math.IsNaN(other.Float()) {
			return math.IsNaN(v.Float()) && math.IsNaN(other.Float())
		}
		return v.Float() == other.Float()
	case KindArray:
		// A KindArray payload is a *arrayData wrapper, so identity is the
		// wrapper's pointer rather than the current element backing. Identity
		// must survive in-place mutators (push may reallocate the element slice)
		// and must distinguish two independently constructed empty arrays:
		// mutating one never affects the other, so they are distinct objects,
		// exactly as two empty hashes are.
		return v.data.(*arrayData) == other.data.(*arrayData)
	case KindHash:
		// A KindHash payload is a *hashData wrapper, so identity is the wrapper's
		// pointer rather than its entry map. Two hashes that share an entry map but
		// carry different default metadata are distinct objects, and each NewHash
		// call allocates a fresh wrapper, so independently constructed hashes (even
		// empty ones) are never identical.
		return HashIdentity(v) == HashIdentity(other)
	case KindObject:
		// The tag is part of a bag's identity, not just of its rendering. Two
		// wrappers over one entry map that carry different provenance are
		// different objects: one is immutable and renders its string form,
		// the other is an ordinary bag. Leaving them identical here would also
		// contradict containment cloning, which gives them separate copies.
		if v.ObjectTag() != other.ObjectTag() {
			return false
		}
		left := v.data.(*objectData).entries
		right := other.data.(*objectData).entries
		return reflect.ValueOf(left).Pointer() == reflect.ValueOf(right).Pointer()
	case KindFunction, KindBuiltin, KindBlock, KindClass, KindInstance:
		// These runtime kinds already compare by backing-pointer identity in
		// Equal (via RuntimeEqualer), which is exactly the identity contract
		// equal? wants, so delegating keeps the two predicates consistent.
		return v.Equal(other)
	case KindEnum, KindEnumValue:
		// Enum and enum-value Equal is structural: two independently cloned
		// members that share an owner script and name compare Equal even though
		// they hold distinct backing storage (for example, a value cloned out to
		// the host and returned by a capability). equal? must report identity,
		// so consult RuntimeIdenticaler, which compares backing pointers.
		if RuntimeIdenticaler != nil {
			if result, ok := RuntimeIdenticaler(v, other); ok {
				return result
			}
		}
		return v.Equal(other)
	default:
		return v.Equal(other)
	}
}

// EqualityContext reuses the cycle-detection scratch used by Value equality.
// The zero value is ready to use. It is not safe for concurrent use.
// It is intended for the interpreter's internal use; hosts should not rely
// on it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
type EqualityContext struct {
	seen map[valueEqualityPair]struct{}
}

// Equal reports whether v and other hold the same kind and value.
func (v Value) Equal(other Value) bool {
	var ctx EqualityContext
	return ctx.Equal(v, other)
}

// Equal reports whether v and other hold the same kind and value.
func (c *EqualityContext) Equal(v, other Value) bool {
	if c.seen != nil {
		clear(c.seen)
	}
	return valuesEqual(v, other, &c.seen)
}

type valueEqualityPair struct {
	kind     ValueKind
	leftPtr  uintptr
	rightPtr uintptr
	leftLen  int
	rightLen int
}

// numericCrossKindEqual compares an int against a float exactly.
//
// The comparison runs through big.Float rather than converting the integer to
// float64, because a float64 cannot represent every int64 above 2^53 and the
// conversion would make distinct integers compare equal to the same float.
// NaN equals nothing, and neither infinity equals any integer.
func numericCrossKindEqual(v, other Value) bool {
	var intVal, floatVal Value
	switch {
	case v.kind == KindInt && other.kind == KindFloat:
		intVal, floatVal = v, other
	case v.kind == KindFloat && other.kind == KindInt:
		intVal, floatVal = other, v
	default:
		return false
	}

	f := floatVal.Float()
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return false
	}
	if f != math.Trunc(f) {
		// A float with a fractional part cannot equal an integer.
		return false
	}

	exactFloat := new(big.Float).SetFloat64(f)
	if bi, ok := intVal.data.(*big.Int); ok {
		return new(big.Float).SetInt(bi).Cmp(exactFloat) == 0
	}
	return new(big.Float).SetInt64(intVal.Int()).Cmp(exactFloat) == 0
}

func valuesEqual(v, other Value, seen *map[valueEqualityPair]struct{}) bool {
	return valuesEqualWithKinds(v, other, seen, false)
}

// valuesEqualWithKinds compares two values, optionally requiring their kinds
// to match at every level.
//
// strictKinds is what separates eql? from ==. == compares an int against a
// float numerically, and that has to hold wherever the pair appears, so a
// nested [1] == [1.0] is true. eql? is the kind-strict predicate, and its
// strictness has to hold just as recursively: checking only the outermost
// kind made [1].eql?([1.0]) true, because the elements went through the
// widened comparison.
func valuesEqualWithKinds(v, other Value, seen *map[valueEqualityPair]struct{}, strictKinds bool) bool {
	if v.kind != other.kind {
		// eql? is kind-strict at every level, not only at the outermost one:
		// checking the kinds once and then delegating here made
		// [1].eql?([1.0]) true, because the elements took the numeric path.
		if strictKinds {
			return false
		}
		// An int and a float compare numerically, so 1 == 1.0 holds, and that
		// has to hold wherever the pair appears -- including nested, so
		// [1] == [1.0] is true. It also matches <=>, which already reports
		// 1 <=> 1.0 as 0.
		return numericCrossKindEqual(v, other)
	}
	switch v.kind {
	case KindNil:
		return true
	case KindBool:
		return v.Bool() == other.Bool()
	case KindInt:
		if v.data != nil || other.data != nil {
			vBig, vOK := v.data.(*big.Int)
			oBig, oOK := other.data.(*big.Int)
			if vOK || oOK {
				// The canonical invariant keeps the compact and big value
				// spaces disjoint (a big payload never fits int64), so a mixed
				// pair is never equal and two big payloads compare exactly.
				return vOK && oOK && vBig.Cmp(oBig) == 0
			}
		}
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
	case KindRegex:
		// Two regex values are equal when they were written the same way:
		// same pattern source and same flags, mirroring Ruby's Regexp#==.
		// The compiled program is derived from those and does not participate.
		left := v.data.(Regex)
		right := other.data.(Regex)
		return left.Source == right.Source && left.Flags == right.Flags
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
			if equalityPairSeen(seen, pair) {
				return true
			}
		}
		for i := range left {
			if !valuesEqualWithKinds(left[i], right[i], seen, strictKinds) {
				return false
			}
		}
		return true
	case KindHash:
		leftLen := v.HashLen()
		rightLen := other.HashLen()
		if leftLen != rightLen {
			return false
		}
		leftPtr := HashIdentity(v)
		rightPtr := HashIdentity(other)
		if leftPtr != 0 && leftPtr == rightPtr {
			return true
		}
		pair := valueEqualityPair{
			kind:     v.kind,
			leftPtr:  leftPtr,
			rightPtr: rightPtr,
			leftLen:  leftLen,
			rightLen: rightLen,
		}
		if pair.leftPtr != 0 || pair.rightPtr != 0 {
			if equalityPairSeen(seen, pair) {
				return true
			}
		}
		leftTyped := v.HashHasTypedEntries()
		rightTyped := other.HashHasTypedEntries()
		if !leftTyped && !rightTyped {
			return hashMapsEqual(v.Hash(), other.Hash(), seen, strictKinds)
		}
		left := v.HashEntries()
		right := other.HashEntries()
		if !leftTyped || !rightTyped {
			return hashEntriesEqualByDisplayKey(left, right, seen, strictKinds)
		}
		if len(right) <= smallHashEqualityEntryLimit {
			return hashEntriesEqualByLookupKeyLinear(left, right, seen, strictKinds)
		}
		rightByKey, ok := hashEntriesByLookupKey(right)
		if !ok {
			return false
		}
		for _, leftEntry := range left {
			key, err := NewHashLookupKey(leftEntry.Key)
			if err != nil {
				return false
			}
			rightEntry, ok := rightByKey[key]
			if !ok {
				return false
			}
			if !valuesEqualWithKinds(leftEntry.Value, rightEntry.Value, seen, strictKinds) {
				return false
			}
		}
		return true
	case KindObject:
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
			if equalityPairSeen(seen, pair) {
				return true
			}
		}
		for key, leftValue := range left {
			rightValue, ok := right[key]
			if !ok {
				return false
			}
			if !valuesEqualWithKinds(leftValue, rightValue, seen, strictKinds) {
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

func hashMapsEqual(left, right map[string]Value, seen *map[valueEqualityPair]struct{}, strictKinds bool) bool {
	if len(left) != len(right) {
		return false
	}
	for key, leftValue := range left {
		rightValue, ok := right[key]
		if !ok {
			return false
		}
		if !valuesEqualWithKinds(leftValue, rightValue, seen, strictKinds) {
			return false
		}
	}
	return true
}

const smallHashEqualityEntryLimit = 8

func equalityPairSeen(seen *map[valueEqualityPair]struct{}, pair valueEqualityPair) bool {
	if *seen == nil {
		*seen = make(map[valueEqualityPair]struct{})
	}
	if _, ok := (*seen)[pair]; ok {
		return true
	}
	(*seen)[pair] = struct{}{}
	return false
}

func hashEntriesEqualByDisplayKey(left, right []HashEntry, seen *map[valueEqualityPair]struct{}, strictKinds bool) bool {
	if len(right) <= smallHashEqualityEntryLimit {
		return hashEntriesEqualByDisplayKeyLinear(left, right, seen, strictKinds)
	}
	leftByKey, ok := hashEntriesByDisplayKey(left)
	if !ok {
		return false
	}
	rightByKey, ok := hashEntriesByDisplayKey(right)
	if !ok {
		return false
	}
	if len(leftByKey) != len(rightByKey) {
		return false
	}
	for key, leftEntry := range leftByKey {
		rightEntry, ok := rightByKey[key]
		if !ok {
			return false
		}
		if !valuesEqualWithKinds(leftEntry.Value, rightEntry.Value, seen, strictKinds) {
			return false
		}
	}
	return true
}

func hashEntriesEqualByDisplayKeyLinear(left, right []HashEntry, seen *map[valueEqualityPair]struct{}, strictKinds bool) bool {
	if hashEntriesHaveDuplicateDisplayKey(left) || hashEntriesHaveDuplicateDisplayKey(right) {
		return false
	}
	for _, leftEntry := range left {
		key := HashDisplayKey(leftEntry.Key)
		found := false
		for _, rightEntry := range right {
			if HashDisplayKey(rightEntry.Key) != key {
				continue
			}
			if !valuesEqualWithKinds(leftEntry.Value, rightEntry.Value, seen, strictKinds) {
				return false
			}
			found = true
			break
		}
		if !found {
			return false
		}
	}
	return true
}

func hashEntriesHaveDuplicateDisplayKey(entries []HashEntry) bool {
	for i, entry := range entries {
		key := HashDisplayKey(entry.Key)
		for _, other := range entries[i+1:] {
			if HashDisplayKey(other.Key) == key {
				return true
			}
		}
	}
	return false
}

func hashEntriesEqualByLookupKeyLinear(left, right []HashEntry, seen *map[valueEqualityPair]struct{}, strictKinds bool) bool {
	for _, leftEntry := range left {
		leftKey, err := NewHashLookupKey(leftEntry.Key)
		if err != nil {
			return false
		}
		found := false
		for _, rightEntry := range right {
			rightKey, err := NewHashLookupKey(rightEntry.Key)
			if err != nil {
				return false
			}
			if rightKey != leftKey {
				continue
			}
			if !valuesEqualWithKinds(leftEntry.Value, rightEntry.Value, seen, strictKinds) {
				return false
			}
			found = true
			break
		}
		if !found {
			return false
		}
	}
	return true
}

func hashEntriesByDisplayKey(entries []HashEntry) (map[string]HashEntry, bool) {
	byKey := make(map[string]HashEntry, len(entries))
	for _, entry := range entries {
		key := HashDisplayKey(entry.Key)
		if _, exists := byKey[key]; exists {
			return nil, false
		}
		byKey[key] = entry
	}
	return byKey, true
}

func hashEntriesByLookupKey(entries []HashEntry) (map[HashLookupKey]HashEntry, bool) {
	byKey := make(map[HashLookupKey]HashEntry, len(entries))
	for _, entry := range entries {
		key, err := NewHashLookupKey(entry.Key)
		if err != nil {
			return nil, false
		}
		byKey[key] = entry
	}
	return byKey, true
}

package value

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"slices"
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
	// chargeBytes, when set, is invoked with each scalar payload's byte
	// length before the sizing walk scans it (escape counting reads the
	// whole payload), so a caller's byte budget can interrupt the sizing
	// pass instead of only being billed after it completes. nil leaves
	// sizing unmetered, as the rendering guards that charge separately
	// expect.
	chargeBytes func(bytes int) error
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
	remaining := max(limit-buf.Len(), 0)
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
	case KindRegex:
		// Sizing a regex must not render it: Regex.String escapes the source and
		// allocates the literal, so measuring performs the work being measured --
		// ahead of any charge the caller applies, and again when the value is
		// really rendered. StringLen walks the source instead. Handled here
		// rather than at each call site so a regex nested in an array or hash is
		// sized the same way.
		return v.data.(Regex).StringLen()
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
	case KindRegex:
		// See StringByteLen: sizing a regex must not render it. No step callback
		// here, so the source walk is unbilled -- the unmetered path.
		return v.data.(Regex).StringRuneLen()
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
	case KindRegex:
		// See StringByteLen: sizing a regex must not render it. This walker takes
		// no step callback, so the source walk is unbilled here -- it is the
		// unmetered path, used where no quota is in force.
		return v.data.(Regex).StringLen()
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
	case KindRegex:
		// See StringByteLen: sizing a regex must not render it. No step callback
		// here, so the source walk is unbilled -- the unmetered path.
		return v.data.(Regex).StringRuneLen()
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
	case KindRegex:
		// See StringByteLen: sizing a regex must not render it, and the source
		// walk is charged before it runs. array.join reaches this public entry
		// point rather than the recursive walker, so the charge belongs on both.
		if err := step(); err != nil {
			return 0, err
		}
		if err := chargeRegexSourceSteps(v, step); err != nil {
			return 0, err
		}
		return v.data.(Regex).StringLen(), nil
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
	case KindRegex:
		// See StringByteLen: sizing a regex must not render it, and the source
		// walk is charged before it runs. The per-node step comes first and
		// unconditionally: a source under one step's worth of bytes charges
		// nothing proportional, and without this an exhausted callback could not
		// abort the projection and a regex would cost one step less than every
		// other kind.
		if err := step(); err != nil {
			return 0, err
		}
		if err := chargeRegexSourceSteps(v, step); err != nil {
			return 0, err
		}
		return v.data.(Regex).StringRuneLen(), nil
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
	case KindRegex:
		// See StringByteLen: sizing a regex must not render it, and the source
		// walk StringLen performs is charged before it runs.
		if err := chargeRegexSourceSteps(v, step); err != nil {
			return 0, err
		}
		return v.data.(Regex).StringLen(), nil
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
	case KindRegex:
		// See StringByteLen: sizing a regex must not render it, and the source
		// walk is charged before it runs. No per-node step here: this walker
		// already charged one at its entry, and adding another billed the same
		// node twice, making a nested regex cost a step more than a nested
		// scalar. The public entry points do need their own, having none.
		if err := chargeRegexSourceSteps(v, step); err != nil {
			return 0, err
		}
		return v.data.(Regex).StringRuneLen(), nil
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
	case KindRegex:
		// See StringByteLen: sizing a regex must not render it, and the source
		// walk StringLen performs is charged before it runs.
		if err := chargeRegexSourceSteps(v, step); err != nil {
			return 0, false, err
		}
		total, truncated := stringByteLenCappedAdd(0, v.data.(Regex).StringLen(), limit)
		return total, truncated, nil
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
	return ctx.Eql(v, other)
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

// EqualityContext reuses the cycle-detection scratch used by Value equality
// and optionally carries a byte charge for the string payloads a comparison
// reads (see SetCharge). The zero value is ready to use and compares
// unmetered. It is not safe for concurrent use. It is intended for the
// interpreter's internal use; hosts should not rely on it, and it carries no
// compatibility promise (see docs/embedding-api-stability.md).
type EqualityContext struct {
	state equalityState
}

// equalityState threads the cycle-detection scratch, the optional byte
// charge, and the first charge failure through a recursive equality walk.
// The seen set inserts and never deletes within one comparison, so each
// distinct composite pair is walked — and its scalar payloads charged — once,
// which keeps shared DAGs linear with or without metering.
type equalityState struct {
	seen map[valueEqualityPair]struct{}
	// charge bills the bytes a scalar comparison is about to read. nil means
	// unmetered: hosts, tests, and Value.Equal keep their existing behavior.
	charge func(bytes int) error
	// reserveScratch validates the walk's cumulative transient scratch — the
	// key slices deterministic traversal sorts and the display renderings —
	// against the caller's memory budget before each allocation, together
	// with the top-level operands of the active comparison: the operands can
	// be temporaries no other root reaches, and the scratch coexists with
	// both compared graphs at its peak. nil means unvalidated.
	reserveScratch func(bytes int, left, right Value) error
	// rootLeft and rootRight are the active comparison's top-level operands,
	// handed to reserveScratch with every validation.
	rootLeft  Value
	rootRight Value
	// roundScratchAlloc maps a requested scratch allocation to the capacity
	// the allocator actually reserves for it (size-class rounding), so a
	// rendered display key is validated at its realized backing size. nil
	// reserves requests at their exact size.
	roundScratchAlloc func(bytes int) int
	// scratchHeld accumulates the scratch bytes this walk has allocated, so
	// nested map traversals validate their combined footprint.
	scratchHeld int
	// pendingCharge accumulates leaf bytes not yet handed to charge. The
	// walk flushes in batches and Equal/Eql settle the tail, so a caller
	// whose charge function rounds each invocation down (the runtime bills
	// whole 64-byte steps) cannot be walked past with many sub-granularity
	// reads: the aggregate is billed even when every leaf alone is free.
	pendingCharge int
	// err records the first charge failure. It is sticky: every comparison
	// after it returns false immediately, so a caller looping over many Equal
	// calls does O(1) work per call once the quota is gone and surfaces the
	// error via Err.
	err error
}

// Equal reports whether v and other hold the same kind and value.
func (v Value) Equal(other Value) bool {
	var ctx EqualityContext
	return ctx.Equal(v, other)
}

// Equal reports whether v and other hold the same kind and value.
func (c *EqualityContext) Equal(v, other Value) bool {
	if c.state.seen != nil {
		clear(c.state.seen)
	}
	// Scratch from prior walks on a reused context is dead; the validator
	// must see each walk's own footprint, not a scan's lifetime total.
	c.state.scratchHeld = 0
	c.state.rootLeft, c.state.rootRight = v, other
	eq := valuesEqual(v, other, &c.state)
	flushEqualityCharge(&c.state)
	if c.state.err != nil {
		return false
	}
	return eq
}

// Eql reports whether v and other are equal under hash-key semantics: kinds
// must match at every level (see Value.Eql). It shares the context's scratch,
// charge hook, and sticky error with Equal.
func (c *EqualityContext) Eql(v, other Value) bool {
	if c.state.seen != nil {
		clear(c.state.seen)
	}
	c.state.scratchHeld = 0
	c.state.rootLeft, c.state.rootRight = v, other
	eq := valuesEqualWithKinds(v, other, &c.state, true)
	flushEqualityCharge(&c.state)
	if c.state.err != nil {
		return false
	}
	return eq
}

// SetScratchReserver installs a validator for the walk's transient scratch
// allocations (the key slices deterministic map traversal sorts and the
// rendered display keys): it is invoked with the walk's cumulative scratch
// bytes and the active comparison's top-level operands before each
// allocation, and an error aborts the comparison like a charge failure. The
// operands accompany every validation because they can be temporaries no
// other root reaches, and the scratch coexists with both compared graphs at
// its peak. A nil reserver leaves allocations unvalidated.
func (c *EqualityContext) SetScratchReserver(reserve func(bytes int, left, right Value) error) {
	c.state.reserveScratch = reserve
}

// SetScratchAllocRounder installs the allocator projection applied when the
// walk reserves an allocation of known size — the rendered display key of a
// composite hash key: round receives the requested byte size and returns the
// capacity the allocator will actually reserve for it. Reserving the
// unrounded request would let a budget that sits between the request and the
// realized size-class capacity admit an allocation exceeding it. A nil
// rounder reserves requests at their exact size.
func (c *EqualityContext) SetScratchAllocRounder(round func(bytes int) int) {
	c.state.roundScratchAlloc = round
}

// SetCharge installs a byte charge invoked for the string and symbol payloads
// a comparison reads: scalar leaves that pass the length screen, and
// string-like hash keys whose text is hashed or compared. A charge failure
// makes the current and every subsequent comparison on this context answer
// false; callers observe the failure through Err. A nil charge restores
// unmetered comparison.
func (c *EqualityContext) SetCharge(charge func(bytes int) error) {
	c.state.charge = charge
}

// Err returns the first charge failure recorded on this context, or nil. Once
// non-nil, comparison answers from this context are meaningless and the
// caller must surface the error instead.
func (c *EqualityContext) Err() error {
	return c.state.err
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

func valuesEqual(v, other Value, state *equalityState) bool {
	return valuesEqualWithKinds(v, other, state, false)
}

// equalityChargeBatchBytes is the flush threshold for accumulated leaf
// charges: large enough to amortize the charge call, small enough that a
// spent quota stops the walk within one batch.
const equalityChargeBatchBytes = 4096

// chargeEqualityBytes accumulates n bytes of read work and invokes the
// installed charge in batches. Charging each leaf separately let a
// per-invocation rounding in the charge function (the runtime bills whole
// 64-byte steps) scan arbitrarily many short payloads for free; batching
// preserves the sub-granularity remainders across the walk. A failure is
// sticky, like a direct charge's.
func chargeEqualityBytes(state *equalityState, n int) bool {
	if state.charge == nil || n <= 0 {
		return true
	}
	state.pendingCharge += n
	if state.pendingCharge < equalityChargeBatchBytes {
		return true
	}
	pending := state.pendingCharge
	state.pendingCharge = 0
	if err := state.charge(pending); err != nil {
		state.err = err
		return false
	}
	return true
}

// flushEqualityCharge settles the pending tail at the end of a top-level
// comparison, so every call's charged total is byte-exact.
func flushEqualityCharge(state *equalityState) {
	pending := state.pendingCharge
	state.pendingCharge = 0
	if state.charge == nil || pending <= 0 || state.err != nil {
		return
	}
	if err := state.charge(pending); err != nil {
		state.err = err
	}
}

// chargeEqualityKeyText bills the text a hash key contributes to an equality
// walk, reporting whether the walk may continue. A string-like key bills its
// payload. An array key recurses: the typed-hash equality paths canonicalize
// it through NewHashLookupKey, which copies every nested string once per
// occurrence — shared subtrees included — so the charge follows the same
// traversal with a stack-scoped cycle guard, and carries a node budget so
// computing the charge cannot itself become the unbounded work (past the
// budget the cost saturates, tripping any bounded quota). Other kinds and
// unmetered walks are free.
func chargeEqualityKeyText(state *equalityState, key Value) bool {
	if state.charge == nil {
		return true
	}
	budget := equalityKeyCostNodeBudget
	bytes, _, _ := equalityKeyTextBytes(key, nil, &budget)
	return chargeEqualityBytes(state, bytes)
}

// equalityKeyCostNodeBudget bounds the key-cost walk; see chargeEqualityKeyText.
const equalityKeyCostNodeBudget = 1 << 16

// chargeDisplayKeyRender bills a composite key's rendered Inspect length,
// reporting the projected byte count for the caller's reservation and
// pregrown render. The display path writes each descendant payload once,
// unlike lookup-key canonicalization's per-level copies, so billing the
// canonical model here overcharged deeply nested singleton keys —
// Θ(depth·payload) for Θ(rendered) work — into spurious quota errors.
func chargeDisplayKeyRender(state *equalityState, key Value) (projected int, ok bool) {
	// The sizing pass itself scans every scalar payload (escape counting)
	// and visits every node, so it is billed from inside the walk: leaf
	// bytes through the sizing state's charge hook and a small structural
	// constant per node, letting a spent budget interrupt the sizing
	// instead of only being billed after it completes. The rendering pass
	// that follows re-reads everything it writes, billed as the projection
	// below.
	sizing := newValueStringState()
	sizing.chargeBytes = func(n int) error {
		if state.err != nil {
			return state.err
		}
		if !chargeEqualityBytes(state, n) {
			return state.err
		}
		return nil
	}
	projected, err := key.inspectByteLenBoundedWithState(sizing, func() error {
		if state.err != nil {
			return state.err
		}
		if !chargeEqualityBytes(state, displayKeyNodeBytes) {
			return state.err
		}
		return nil
	})
	if err != nil {
		if state.err == nil {
			state.err = err
		}
		return 0, false
	}
	return projected, chargeEqualityBytes(state, projected)
}

// displayKeyNodeBytes is the structural charge per node the display-key
// sizing walk visits, so composites of many payload-free nodes stay bounded
// by the byte budget too.
const displayKeyNodeBytes = 2

// equalityKeyTextBytes models the bytes NewHashLookupKey's canonicalization
// reads for key, in element order: each array level copies every child's
// complete encoding into its own canonical string, so a depth-d chain around
// one string costs Θ(d·len). It returns the bytes to charge and the
// subtree's encoding size; walkable reports whether canonicalization would
// read past this node (an unsupported element, a NaN float, or a cycle stops
// it immediately), and a spent node budget stops the walk with the cost
// saturated.
func equalityKeyTextBytes(key Value, onPath map[SliceIdentity]struct{}, budget *int) (int, int, bool) {
	if *budget <= 0 {
		return math.MaxInt / 2, math.MaxInt / 2, false
	}
	*budget--
	switch key.kind {
	case KindString, KindSymbol:
		// The canonical encoding wraps the payload in kind-and-length
		// framing, which ancestors copy like payload bytes; see the
		// runtime-side cost model for why empty strings must not encode
		// to zero.
		n := len(key.data.(string))
		enc := n + 16
		if enc < 0 {
			enc = math.MaxInt / 2
		}
		return n, enc, true
	case KindNil, KindBool, KindInt, KindRange:
		return 0, 16, true
	case KindFloat:
		return 0, 16, !math.IsNaN(key.Float())
	case KindArray:
		elems := key.Array()
		// Full slice-header identity, as HashKey's cycle guard uses:
		// overlapping reslices share a pointer but are distinct keys read in
		// full, so a pointer-only guard misread them as cycles.
		var id SliceIdentity
		if cap(elems) > 0 {
			id = SliceIdentity{Ptr: reflect.ValueOf(elems).Pointer(), Len: len(elems), Cap: cap(elems)}
		}
		if id.Ptr != 0 {
			if _, ok := onPath[id]; ok {
				return 0, 0, false
			}
			if onPath == nil {
				onPath = make(map[SliceIdentity]struct{})
			}
			onPath[id] = struct{}{}
		}
		charge, childEnc := 0, 0
		walkable := true
		for _, elem := range elems {
			c, e, ok := equalityKeyTextBytes(elem, onPath, budget)
			if charge += c; charge < 0 {
				charge = math.MaxInt / 2
			}
			if childEnc += e; childEnc < 0 {
				childEnc = math.MaxInt / 2
			}
			if !ok {
				walkable = false
				break
			}
		}
		if id.Ptr != 0 {
			delete(onPath, id)
		}
		if !walkable {
			// Canonicalization stops at the failing element, so this level's
			// string is never built: the prefix work already charged stands,
			// but no copy happens here and no encoding reaches an ancestor.
			// Propagating the partial encoding would grow the charge with
			// nesting depth for work never performed, turning the ordinary
			// unequal answer into a spurious quota error.
			return charge, 0, false
		}
		// This level's canonical string copies every child encoding again.
		if charge += childEnc; charge < 0 {
			charge = math.MaxInt / 2
		}
		enc := childEnc + 16
		if enc < 0 {
			enc = math.MaxInt / 2
		}
		return charge, enc, walkable
	default:
		return 0, 0, false
	}
}

func valuesEqualWithKinds(v, other Value, state *equalityState, strictKinds bool) bool {
	if state.err != nil {
		return false
	}
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
		left := v.data.(string)
		right := other.data.(string)
		// A length mismatch answers without reading either payload, so only
		// equal-length pairs are charged — the same rule the operator-level
		// charge applies, and the same work Go's == performs.
		if len(left) != len(right) {
			return false
		}
		if !chargeEqualityBytes(state, len(left)) {
			return false
		}
		return left == right
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
		// The source comparison reads its bytes like a string leaf, with the
		// same length screen.
		left := v.data.(Regex)
		right := other.data.(Regex)
		if len(left.Source) != len(right.Source) {
			return false
		}
		if !chargeEqualityBytes(state, len(left.Source)) {
			return false
		}
		if left.Source != right.Source {
			return false
		}
		// Flags is an exported, unrestricted string a host can size at
		// will; its comparison reads it like the source, under the same
		// length screen.
		if len(left.Flags) != len(right.Flags) {
			return false
		}
		if !chargeEqualityBytes(state, len(left.Flags)) {
			return false
		}
		return left.Flags == right.Flags
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
			if equalityPairSeen(state, pair) {
				return true
			}
		}
		for i := range left {
			if !valuesEqualWithKinds(left[i], right[i], state, strictKinds) {
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
			if equalityPairSeen(state, pair) {
				return true
			}
		}
		leftTyped := v.HashHasTypedEntries()
		rightTyped := other.HashHasTypedEntries()
		if !leftTyped && !rightTyped {
			return hashMapsEqual(v.Hash(), other.Hash(), state, strictKinds)
		}
		// HashEntries materializes a fresh entry slice per side, live for
		// the whole comparison; on a metered walk both copies are validated
		// before they exist, like every other scratch the walk allocates.
		if state.charge != nil && state.reserveScratch != nil {
			entryCopies := (v.HashLen() + other.HashLen()) * hashEntrySliceEntryBytes
			state.scratchHeld += entryCopies
			defer releaseScratchBytes(state, entryCopies)
			if err := state.reserveScratch(state.scratchHeld, state.rootLeft, state.rootRight); err != nil {
				state.err = err
				return false
			}
		}
		left := v.HashEntries()
		right := other.HashEntries()
		if !leftTyped || !rightTyped {
			return hashEntriesEqualByDisplayKey(left, right, state, strictKinds)
		}
		if len(right) <= smallHashEqualityEntryLimit {
			return hashEntriesEqualByLookupKeyLinear(left, right, state, strictKinds)
		}
		rightByKey, heldRight, ok := hashEntriesByLookupKey(right, state)
		defer releaseScratchBytes(state, heldRight)
		if !ok {
			return false
		}
		for _, leftEntry := range left {
			if !chargeEqualityKeyText(state, leftEntry.Key) {
				return false
			}
			key, err := NewHashLookupKey(leftEntry.Key)
			if err != nil {
				return false
			}
			rightEntry, ok := rightByKey[key]
			if !ok {
				return false
			}
			if !valuesEqualWithKinds(leftEntry.Value, rightEntry.Value, state, strictKinds) {
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
			if equalityPairSeen(state, pair) {
				return true
			}
		}
		if state.charge == nil {
			for key, leftValue := range left {
				rightValue, ok := right[key]
				if !ok {
					return false
				}
				if !valuesEqualWithKinds(leftValue, rightValue, state, strictKinds) {
					return false
				}
			}
			return true
		}
		keys, chargeOK := meteredMapKeys(left, state)
		if !chargeOK {
			return false
		}
		defer releaseKeySortScratch(state, len(keys))
		for _, key := range keys {
			rightValue, ok := right[key]
			if !ok {
				return false
			}
			if !valuesEqualWithKinds(left[key], rightValue, state, strictKinds) {
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

// keySortScratchEntryBytes approximates one entry of the key-sorting scratch
// slice: a string header plus slice-slot overhead.
const keySortScratchEntryBytes = 24

// releaseKeySortScratch retires a completed map traversal's contribution to
// the walk's live scratch accounting: a sibling map compared later must be
// validated against the slices actually alive, not every slice the walk ever
// allocated.
func releaseKeySortScratch(state *equalityState, entries int) {
	state.scratchHeld -= entries * keySortScratchEntryBytes
	if state.scratchHeld < 0 {
		state.scratchHeld = 0
	}
}

// sortedMapKeys returns a map's keys in sorted order, validating the scratch
// slice against the caller's budget first. Metered equality must traverse
// deterministically: with Go's randomized map iteration, identical unequal
// inputs under the same quota alternated between answering false (the
// mismatch visited first) and raising a limit error (a long equal entry
// charged first). Callers whose keys are not yet billed use meteredMapKeys,
// which additionally charges every key's bytes before the sort reads them.
func sortedMapKeys[V any](m map[string]V, state *equalityState) ([]string, bool) {
	if state.reserveScratch != nil {
		state.scratchHeld += len(m) * keySortScratchEntryBytes
		if err := state.reserveScratch(state.scratchHeld, state.rootLeft, state.rootRight); err != nil {
			state.err = err
			return nil, false
		}
	}
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	if state.charge == nil {
		slices.Sort(keys)
		return keys, true
	}
	// The sort rereads common prefixes Θ(log n) times per key, past what the
	// single linear key charge covers; measure the exact bytes each
	// comparison reads and bill them in batches from inside the comparator,
	// so a spent quota stops the scan within one batch of work — after a
	// charge failure the remaining comparisons decide on lengths alone, and
	// the garbage order is discarded with the error.
	const sortChargeBatchBytes = 4096
	pending := 0
	slices.SortFunc(keys, func(a, b string) int {
		if state.err != nil {
			return len(a) - len(b)
		}
		n := min(len(a), len(b))
		i := 0
		for i < n && a[i] == b[i] {
			i++
		}
		read := i + 1
		if read > n {
			read = n
		}
		if pending += read; pending >= sortChargeBatchBytes {
			chargeEqualityBytes(state, pending)
			pending = 0
		}
		if i < n {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
		return len(a) - len(b)
	})
	if state.err != nil {
		return nil, false
	}
	if pending > 0 {
		if !chargeEqualityBytes(state, pending) {
			return nil, false
		}
	}
	return keys, true
}

// meteredMapKeys is sortedMapKeys with the keys' total bytes billed up front:
// the lexicographic sort scans common prefixes repeatedly and the walk's
// later map probes hash the same payloads, so one total, charged before any
// of that work runs, covers the traversal proportionally.
func meteredMapKeys[V any](m map[string]V, state *equalityState) ([]string, bool) {
	total := 0
	for key := range m {
		total += len(key)
	}
	if !chargeEqualityBytes(state, total) {
		return nil, false
	}
	return sortedMapKeys(m, state)
}

func hashMapsEqual(left, right map[string]Value, state *equalityState, strictKinds bool) bool {
	if len(left) != len(right) {
		return false
	}
	// Unmetered comparison keeps the host API's allocation-free direct scan:
	// with no charges in play, traversal order cannot change the outcome.
	if state.charge == nil {
		for key, leftValue := range left {
			rightValue, ok := right[key]
			if !ok {
				return false
			}
			if !valuesEqualWithKinds(leftValue, rightValue, state, strictKinds) {
				return false
			}
		}
		return true
	}
	keys, ok := meteredMapKeys(left, state)
	if !ok {
		return false
	}
	defer releaseKeySortScratch(state, len(keys))
	for _, key := range keys {
		rightValue, ok := right[key]
		if !ok {
			return false
		}
		if !valuesEqualWithKinds(left[key], rightValue, state, strictKinds) {
			return false
		}
	}
	return true
}

const smallHashEqualityEntryLimit = 8

func equalityPairSeen(state *equalityState, pair valueEqualityPair) bool {
	if state.seen == nil {
		state.seen = make(map[valueEqualityPair]struct{})
	}
	if _, ok := state.seen[pair]; ok {
		return true
	}
	state.seen[pair] = struct{}{}
	return false
}

func hashEntriesEqualByDisplayKey(left, right []HashEntry, state *equalityState, strictKinds bool) bool {
	if len(right) <= smallHashEqualityEntryLimit {
		return hashEntriesEqualByDisplayKeyLinear(left, right, state, strictKinds)
	}
	leftByKey, heldLeft, ok := hashEntriesByDisplayKey(left, state)
	defer releaseScratchBytes(state, heldLeft)
	if !ok {
		return false
	}
	rightByKey, heldRight, ok := hashEntriesByDisplayKey(right, state)
	defer releaseScratchBytes(state, heldRight)
	if !ok {
		return false
	}
	if len(leftByKey) != len(rightByKey) {
		return false
	}
	if state.charge == nil {
		for key, leftEntry := range leftByKey {
			rightEntry, ok := rightByKey[key]
			if !ok {
				return false
			}
			if !valuesEqualWithKinds(leftEntry.Value, rightEntry.Value, state, strictKinds) {
				return false
			}
		}
		return true
	}
	// The display keys were billed while the maps were built; the sort
	// re-reads already-charged payloads only.
	displayKeys, chargeOK := sortedMapKeys(leftByKey, state)
	if !chargeOK {
		return false
	}
	defer releaseKeySortScratch(state, len(displayKeys))
	for _, key := range displayKeys {
		rightEntry, ok := rightByKey[key]
		if !ok {
			return false
		}
		if !valuesEqualWithKinds(leftByKey[key].Value, rightEntry.Value, state, strictKinds) {
			return false
		}
	}
	return true
}

// sortedEntryScratchBytes approximates one entry's structural share of the
// mixed-path sort scratch: a keyedHashEntry slot (string header, rendered
// flag, and the HashEntry copy), with slack for alignment. Rendered display
// strings are reserved separately at their realized capacity.
const sortedEntryScratchBytes = 128

// hashEntrySliceEntryBytes approximates one slot of the entry slices
// HashEntries materializes for a typed or mixed comparison: two Value
// structs per HashEntry.
const hashEntrySliceEntryBytes = 64

// releaseScratchBytes retires n bytes of walk scratch accounting.
func releaseScratchBytes(state *equalityState, n int) {
	state.scratchHeld -= n
	if state.scratchHeld < 0 {
		state.scratchHeld = 0
	}
}

// scratchAllocCapacity projects the bytes the allocator will actually
// reserve for a scratch allocation of the requested size. Without an
// installed rounder the request itself is the projection.
func (s *equalityState) scratchAllocCapacity(bytes int) int {
	if s.roundScratchAlloc == nil {
		return bytes
	}
	return s.roundScratchAlloc(bytes)
}

// renderDisplayKeyPregrown renders key's Inspect form into a builder pregrown
// to the projected length, so the rendering takes exactly one allocation of
// the capacity the caller reserved instead of a doubling growth that retains
// — and transiently allocates — more than was validated. projected must come
// from InspectByteLenBounded.
func renderDisplayKeyPregrown(key Value, projected int) string {
	var rendered strings.Builder
	rendered.Grow(projected)
	key.WriteInspectTo(&rendered)
	return rendered.String()
}

// keyedHashEntry pairs a hash entry with its rendered display key. The
// metered mixed-hash walk renders every key up front — under reservation —
// and reuses the rendering for the duplicate screen and each comparison; the
// unmetered walk leaves entries unrendered and renders on first use.
type keyedHashEntry struct {
	display  string
	rendered bool
	entry    HashEntry
}

func (k *keyedHashEntry) displayKey() string {
	if !k.rendered {
		k.display = HashDisplayKey(k.entry.Key)
		k.rendered = true
	}
	return k.display
}

// sortEntriesForMeteredWalk returns entries paired with their rendered
// display keys, ordered by that key when the walk is metered: a legacy hash
// materializes its entries in randomized map order, and a charged linear
// walk must not let iteration order decide between an answer and a quota
// error. Keys are billed as they render, the full live footprint — the pair
// slice and each rendering at its realized backing capacity — is validated
// as scratch, and the sort's comparisons charge in batches like the key
// sorter's. held reports the scratch bytes still accounted; the caller
// releases them when its walk over the returned slice finishes, since the
// renderings and slice stay live that long. The unmetered path returns the
// entries unsorted and unrendered.
func sortEntriesForMeteredWalk(entries []HashEntry, state *equalityState) (keyed []keyedHashEntry, held int, ok bool) {
	if state.charge == nil {
		keyed = make([]keyedHashEntry, len(entries))
		for i, entry := range entries {
			keyed[i].entry = entry
		}
		return keyed, 0, true
	}
	reserve := func(delta int) bool {
		if state.reserveScratch == nil {
			return true
		}
		state.scratchHeld += delta
		held += delta
		if err := state.reserveScratch(state.scratchHeld, state.rootLeft, state.rootRight); err != nil {
			state.err = err
			return false
		}
		return true
	}
	if !reserve(len(entries) * sortedEntryScratchBytes) {
		return nil, held, false
	}
	keyed = make([]keyedHashEntry, len(entries))
	for i, entry := range entries {
		display := ""
		switch entry.Key.Kind() {
		case KindString, KindSymbol:
			// HashDisplayKey aliases the key's own payload, which the
			// memory estimator already counts; only the string header is
			// new, covered by the structural share. The sort and probes
			// read the text, so its bytes are billed.
			display = HashDisplayKey(entry.Key)
			if !chargeEqualityBytes(state, len(display)) {
				return nil, held, false
			}
		default:
			// A composite key renders through Inspect, which can be
			// arbitrarily large; preflight the size, bill the rendered
			// length (see chargeDisplayKeyRender), and reserve the
			// rendering's realized capacity before it is built, not
			// after.
			projected, ok := chargeDisplayKeyRender(state, entry.Key)
			if !ok {
				return nil, held, false
			}
			if !reserve(state.scratchAllocCapacity(projected)) {
				return nil, held, false
			}
			display = renderDisplayKeyPregrown(entry.Key, projected)
		}
		keyed[i] = keyedHashEntry{display: display, rendered: true, entry: entry}
	}
	const sortChargeBatchBytes = 4096
	pending := 0
	slices.SortFunc(keyed, func(a, b keyedHashEntry) int {
		if state.err != nil {
			return len(a.display) - len(b.display)
		}
		n := min(len(a.display), len(b.display))
		i := 0
		for i < n && a.display[i] == b.display[i] {
			i++
		}
		read := i + 1
		if read > n {
			read = n
		}
		if pending += read; pending >= sortChargeBatchBytes {
			chargeEqualityBytes(state, pending)
			pending = 0
		}
		if i < n {
			if a.display[i] < b.display[i] {
				return -1
			}
			return 1
		}
		return len(a.display) - len(b.display)
	})
	if state.err != nil {
		return nil, held, false
	}
	if pending > 0 {
		if !chargeEqualityBytes(state, pending) {
			return nil, held, false
		}
	}
	return keyed, held, true
}

func hashEntriesEqualByDisplayKeyLinear(left, right []HashEntry, state *equalityState, strictKinds bool) bool {
	sortedLeft, heldLeft, ok := sortEntriesForMeteredWalk(left, state)
	defer releaseScratchBytes(state, heldLeft)
	if !ok {
		return false
	}
	sortedRight, heldRight, ok := sortEntriesForMeteredWalk(right, state)
	defer releaseScratchBytes(state, heldRight)
	if !ok {
		return false
	}
	return hashEntriesEqualByDisplayKeyLinearOrdered(sortedLeft, sortedRight, state, strictKinds)
}

func hashEntriesEqualByDisplayKeyLinearOrdered(left, right []keyedHashEntry, state *equalityState, strictKinds bool) bool {
	if hashEntriesHaveDuplicateDisplayKey(left, state) || hashEntriesHaveDuplicateDisplayKey(right, state) {
		return false
	}
	for l := range left {
		leftEntry := &left[l]
		key := leftEntry.displayKey()
		if !chargeEqualityBytes(state, len(key)) {
			return false
		}
		found := false
		for r := range right {
			rightEntry := &right[r]
			// Each probe compares rendered display strings; bill the probed
			// key's text. The metered walk pre-rendered every display key,
			// so reading its length performs no new work.
			if !chargeEqualityBytes(state, len(rightEntry.displayKey())) {
				return false
			}
			if rightEntry.displayKey() != key {
				continue
			}
			if !valuesEqualWithKinds(leftEntry.entry.Value, rightEntry.entry.Value, state, strictKinds) {
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

func hashEntriesHaveDuplicateDisplayKey(entries []keyedHashEntry, state *equalityState) bool {
	for i := range entries {
		key := entries[i].displayKey()
		if !chargeEqualityBytes(state, len(key)) {
			return false
		}
		for j := i + 1; j < len(entries); j++ {
			// The metered walk rendered every display key under reservation
			// before the entries arrived here; each pairwise probe reads the
			// rendered text, so its length is billed.
			if !chargeEqualityBytes(state, len(entries[j].displayKey())) {
				return false
			}
			if entries[j].displayKey() == key {
				return true
			}
		}
	}
	return false
}

func hashEntriesEqualByLookupKeyLinear(left, right []HashEntry, state *equalityState, strictKinds bool) bool {
	for _, leftEntry := range left {
		if !chargeEqualityKeyText(state, leftEntry.Key) {
			return false
		}
		leftKey, err := NewHashLookupKey(leftEntry.Key)
		if err != nil {
			return false
		}
		found := false
		for _, rightEntry := range right {
			if !chargeEqualityKeyText(state, rightEntry.Key) {
				return false
			}
			rightKey, err := NewHashLookupKey(rightEntry.Key)
			if err != nil {
				return false
			}
			if rightKey != leftKey {
				continue
			}
			if !valuesEqualWithKinds(leftEntry.Value, rightEntry.Value, state, strictKinds) {
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

// displayKeyIndexEntryBytes approximates one slot of a display-key index
// map: the string-header key, the HashEntry copy, and a bucket share.
// Rendered composite key strings are reserved separately at their realized
// capacity.
const displayKeyIndexEntryBytes = 128

// lookupKeyIndexEntryBytes approximates one slot of a lookup-key index map:
// the HashLookupKey struct, the HashEntry copy, and a bucket share. Canonical
// key text retained by array and big-integer keys is reserved separately.
const lookupKeyIndexEntryBytes = 160

// reserveHeldEqualityScratch adds delta to the walk's cumulative scratch and
// validates it, accumulating into held so the caller can release the bytes
// when it drops the structure they back.
func reserveHeldEqualityScratch(state *equalityState, held *int, delta int) bool {
	if state.reserveScratch == nil {
		return true
	}
	state.scratchHeld += delta
	*held += delta
	if err := state.reserveScratch(state.scratchHeld, state.rootLeft, state.rootRight); err != nil {
		state.err = err
		return false
	}
	return true
}

// hashEntriesByDisplayKey indexes entries by rendered display key. On a
// metered walk the map's own backing is reserved before it is allocated and
// each composite key's rendering is reserved at its realized capacity before
// it is built, since the map retains both for the caller's whole comparison;
// held reports the bytes still accounted, which the caller releases when it
// drops the map.
func hashEntriesByDisplayKey(entries []HashEntry, state *equalityState) (byKey map[string]HashEntry, held int, ok bool) {
	reserve := func(delta int) bool {
		return reserveHeldEqualityScratch(state, &held, delta)
	}
	if !reserve(len(entries) * displayKeyIndexEntryBytes) {
		return nil, held, false
	}
	byKey = make(map[string]HashEntry, len(entries))
	for _, entry := range entries {
		key := ""
		if state.charge == nil || entry.Key.Kind() == KindString || entry.Key.Kind() == KindSymbol {
			key = HashDisplayKey(entry.Key)
			// Building the map hashes the key text; bill its bytes.
			if !chargeEqualityBytes(state, len(key)) {
				return nil, held, false
			}
		} else {
			projected, ok := chargeDisplayKeyRender(state, entry.Key)
			if !ok {
				return nil, held, false
			}
			if !reserve(state.scratchAllocCapacity(projected)) {
				return nil, held, false
			}
			key = renderDisplayKeyPregrown(entry.Key, projected)
		}
		if _, exists := byKey[key]; exists {
			return nil, held, false
		}
		byKey[key] = entry
	}
	return byKey, held, true
}

// hashEntriesByLookupKey indexes entries by canonical lookup key. On a
// metered walk the map's backing and the canonical key text array and
// big-integer keys retain are reserved before they are materialized, since
// both stay live until the caller's scan finishes; held reports the bytes
// still accounted, which the caller releases when it drops the map.
func hashEntriesByLookupKey(entries []HashEntry, state *equalityState) (byKey map[HashLookupKey]HashEntry, held int, ok bool) {
	if !reserveHeldEqualityScratch(state, &held, len(entries)*lookupKeyIndexEntryBytes) {
		return nil, held, false
	}
	byKey = make(map[HashLookupKey]HashEntry, len(entries))
	for _, entry := range entries {
		if !chargeEqualityKeyText(state, entry.Key) {
			return nil, held, false
		}
		key, err := NewHashLookupKey(entry.Key)
		if err != nil {
			return nil, held, false
		}
		if !reserveHeldEqualityScratch(state, &held, key.ExtraPayloadBytes()) {
			return nil, held, false
		}
		byKey[key] = entry
	}
	return byKey, held, true
}

package runtime

import (
	"fmt"
	"strings"
)

// inspectMemberName is the method name exposed for Ruby-style Object#inspect on
// every core value kind. It is shared by the per-kind suggestion lists and
// builtin tables.
const inspectMemberName = "inspect"

// newInspectBuiltin returns the auto-invoked `inspect` builtin for a receiver
// kind. typeName names the receiver in the builtin's identifier and in argument
// errors (for example "string.inspect"). Every kind shares the same behavior:
// inspect takes no arguments and returns the receiver's debug rendering as a
// string. The rendering's byte length is projected before the string is built,
// together with the receiver's own footprint, so a large composite trips the
// memory quota instead of allocating past it even when the receiver is an
// ephemeral temporary. The projection walk is charged against the step quota so a
// deeply shared graph cannot burn unbounded CPU. The result is streamed into a
// builder pre-grown to the projected length, and the quota check charges that
// builder's realized backing capacity, so the allocation never overshoots the
// charged size the way a fresh zero-capacity builder's doubling growth would.
func newInspectBuiltin(typeName string) Value {
	name := typeName + "." + inspectMemberName
	return NewAutoBuiltin(name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(args) > 0 {
			return NewNil(), fmt.Errorf("%s does not take arguments", name)
		}
		if len(kwargs) > 0 {
			return NewNil(), fmt.Errorf("%s does not take keyword arguments", name)
		}
		if valueBlock(block) != nil {
			return NewNil(), fmt.Errorf("%s does not take a block", name)
		}
		payload, err := receiver.InspectByteLenBounded(exec.step)
		if err != nil {
			return NewNil(), err
		}
		// Render into a builder grown to exactly payload bytes so the realized
		// backing array is the one the quota check accounts for. Builder.Grow
		// reserves roundedAllocSize(payload) for that single reservation, whereas a
		// fresh zero-capacity builder filled byte by byte would double and round on
		// each overflow and could hold a backing array larger than payload at its
		// peak. projectedBuilderCap reproduces Grow's reservation (its size-class
		// rounding included), so charging it rather than the raw payload keeps the
		// realized allocation inside the memory quota, matching the interpolation
		// path (see appendInterpolatedValue).
		var builder strings.Builder
		// Charge the receiver alongside the inspected string: the receiver stays
		// live while the result materializes, so the peak holds both.
		// checkProjectedValueRendering deduplicates the receiver against the base,
		// so a named local contributes only its already-counted footprint while an
		// ephemeral receiver (for example `[big].inspect`) is charged in full
		// before its oversized rendering is built.
		if err := exec.checkProjectedValueRendering(receiver, projectedBuilderCap(&builder, payload)); err != nil {
			return NewNil(), err
		}
		// Grow only on a positive payload: InspectByteLen sums byte counts without
		// saturating, so a rendering larger than the int range (physically
		// unreachable but not statically excluded) could wrap negative, and Grow
		// panics on a negative count. WriteInspectTo then streams the rendering
		// straight into the pre-grown builder without triggering further growth.
		if payload > 0 {
			builder.Grow(payload)
		}
		receiver.WriteInspectTo(&builder)
		return NewString(builder.String()), nil
	})
}

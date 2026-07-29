package runtime

import (
	"fmt"
	"strings"
)

// to_s and inspect were missing on most value kinds even though every kind
// already renders correctly through interpolation and inside a container's
// inspect. The rendering existed; only the direct method did not, and which
// kinds had which did not correspond to anything -- duration, money, and time
// had to_s but not inspect, array and hash had inspect but not to_s.
//
// newToStringBuiltin serves the scalar kinds, whose rendering is bounded by
// the receiver itself, so it projects only for big integers and otherwise
// renders straight through. An aggregate cannot use it: a deeply nested array
// renders to arbitrarily many bytes, and rendering it unprojected would build
// that string before any quota saw it.
//
// This is newInspectBuiltin's accounting with the string rendering: the
// projection charges steps while it walks, the memory quota is checked against
// the reservation the builder will actually make, and the rendering streams
// into a builder grown exactly once.
func newAggregateToStringBuiltin(typeName, property string) Value {
	name := typeName + "." + property
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
		// StringByteLenBounded charges a step per node it visits, so projecting
		// a compact but exponentially shared graph cannot burn unbounded CPU
		// before the memory check runs.
		payload, err := receiver.StringByteLenBounded(exec.step)
		if err != nil {
			return NewNil(), err
		}
		if err := exec.chargeStringScan(payload); err != nil {
			return NewNil(), err
		}
		var builder strings.Builder
		// Charge the receiver alongside the rendered string: the receiver stays
		// live while the result materializes, so the peak holds both.
		if err := exec.checkProjectedValueRendering(receiver, projectedBuilderCap(&builder, payload)); err != nil {
			return NewNil(), err
		}
		// Grow only on a positive payload: the projection sums byte counts
		// without saturating, so a rendering larger than the int range could
		// wrap negative, and Grow panics on a negative count.
		if payload > 0 {
			builder.Grow(payload)
		}
		receiver.WriteStringTo(&builder)
		return NewString(builder.String()), nil
	})
}

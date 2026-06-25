package runtime

import "fmt"

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
// deeply shared graph cannot burn unbounded CPU.
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
		// Charge the receiver alongside the inspected string: the receiver stays
		// live while receiver.Inspect() materializes the result, so the peak holds
		// both. checkProjectedValueRendering deduplicates the receiver against the
		// base, so a named local contributes only its already-counted footprint
		// while an ephemeral receiver (for example `[big].inspect`) is charged in
		// full before its oversized rendering is built.
		if err := exec.checkProjectedValueRendering(receiver, payload); err != nil {
			return NewNil(), err
		}
		return NewString(receiver.Inspect()), nil
	})
}

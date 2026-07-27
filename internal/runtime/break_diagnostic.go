package runtime

// breakOutsideLoopMessage explains a break that has no loop, block, or lambda
// to leave.
//
// A break inside a block is no longer an error -- it terminates the call the
// block was passed to -- so the only remaining case is a break with nothing
// enclosing it at all, where "used outside of loop" is accurate.
func breakOutsideLoopMessage() string {
	return "break used outside of loop"
}

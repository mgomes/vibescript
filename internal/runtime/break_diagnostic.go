package runtime

// breakOutsideLoopMessage explains a break that has no loop to leave.
//
// Two situations reach here and they read very differently. A break inside a
// block is the common one, and "used outside of loop" describes a situation
// the author can see is untrue -- the break is plainly inside an each -- so it
// sends them looking for a missing end. The restriction is also hard to
// predict, because next and return both cross a block boundary and break does
// not, so naming the boundary is what ends the guessing.
//
// The block message names the loop forms rather than suggesting a collection
// method, because the same message covers a proc body (proc { break }), where
// advice about find or take_while would point at the wrong problem entirely.
func breakOutsideLoopMessage(blockDepth int) string {
	if blockDepth > 0 {
		return "break cannot cross a block boundary; break can only leave a for, while, or until loop"
	}
	return "break used outside of loop"
}

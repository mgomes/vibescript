package runtime

import "errors"

// break inside a block used to be rejected, so the most common early-exit
// idiom in a Ruby-flavored language did not work. The restriction was also
// asymmetric in a way nothing at the call site explained: within the same
// block body, next crossed the boundary and return crossed it, and break did
// not.
//
// Ruby's rule is that break terminates the call the block was passed to and
// makes it evaluate to the break value:
//
//	[1, 2, 3, 4].each { |n| break n if n > 3 }   # 4
//	def m; yield; end; m { break 5 }             # 5
//
// So the break is not absorbed by the block, nor by the driver looping over
// it, but by the boundary of the call that received the block. That is a
// single place rather than a change to each of the block-driving members, and
// it is why a driver aborting mid-iteration needs no per-member handling: the
// error propagates out of the driver exactly as any other error does, and the
// estimator's block-iteration regions unwind on their defers.

// absorbBlockBreak converts a break that escaped a block into the result of
// the call the block was passed to.
//
// It applies only when the call actually received a block, so a break that
// crosses an ordinary call boundary still reports rather than silently
// becoming that call's value.
func absorbBlockBreak(err error, block Value) (Value, bool) {
	if err == nil || block.IsNil() || !errors.Is(err, errLoopBreak) {
		return NewNil(), false
	}
	if breakVal, ok := loopBreakValue(err); ok {
		return breakVal, true
	}
	return NewNil(), true
}

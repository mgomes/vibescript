package runtime

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// repeatStringValue implements String#*, which builds a string by repeating
// the receiver, as in Ruby.
//
// Without it there was no way to build a separator, an indent, a table rule, or
// a progress bar at a computed width: the literal `"------"` is the only
// alternative and it does not scale. That matters more here than it would
// elsewhere, because producing text is what most scripts in this language do.
//
// The count comes from the script and the result is the product of it and the
// receiver, so the projected size is charged against the memory quota before
// anything is allocated, following the padding members.
func (exec *Execution) repeatStringValue(left, right Value) (Value, error) {
	count, err := valueToCount(right)
	if err != nil {
		if errors.Is(err, errNegativeCount) {
			return NewNil(), fmt.Errorf("negative argument for string repetition")
		}
		return NewNil(), fmt.Errorf("unsupported multiplication operands")
	}

	text := left.String()
	// An empty receiver repeats to an empty string whatever the count. Short
	// circuiting is not just an optimization: the loop below would otherwise
	// spin for a script-chosen number of iterations while allocating nothing,
	// so the memory quota could not stop it.
	if text == "" || count == 0 {
		return NewString(""), nil
	}

	// Saturating arithmetic keeps the projection from overflowing on a huge
	// count; the quota check rejects anything that large regardless.
	projected := saturatingMul(len(text), count)
	if err := exec.checkProjectedStringBytes(projected); err != nil {
		return NewNil(), err
	}

	var b strings.Builder
	// Only preallocate when the projection is exact; a saturated value means
	// the request overflowed int and would never fit in memory anyway.
	if projected < math.MaxInt {
		b.Grow(projected)
	}
	for range count {
		b.WriteString(text)
	}
	return NewString(b.String()), nil
}

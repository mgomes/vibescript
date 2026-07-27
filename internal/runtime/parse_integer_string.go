package runtime

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"

	"github.com/mgomes/vibescript/vibes/value"
)

// bigIntParseStepDigits is the number of decimal digits one step covers when
// converting an integer string beyond int64, matching the rate the JSON
// parser charges for the same conversion.
const bigIntParseStepDigits = 8

// parseIntegerString converts a base-10 integer string to an integer value,
// promoting a well-formed value beyond int64 to a big integer instead of
// rejecting it.
//
// The language's integers are arbitrary precision, and the surfaces that
// deliberately stay within 64 bits are indexes, counts, sizes, precisions,
// and the temporal and money types. A string conversion is none of those: it
// is a value conversion, and rejecting it broke the round trip through a
// value's own string form, so (2 ** 100).to_s.to_i failed.
//
// A malformed string is still rejected. Only the range limit is lifted.
func parseIntegerString(exec *Execution, s, name string) (Value, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err == nil {
		return NewInt(n), nil
	}
	// ParseInt reports ErrRange only for digits it parsed but could not fit,
	// so a syntax error still fails here rather than reaching big.Int, whose
	// accepted syntax is wider.
	if !errors.Is(err, strconv.ErrRange) {
		return NewNil(), fmt.Errorf("%s expects a base-10 integer string", name)
	}
	if exec != nil {
		if err := exec.stepN(1 + len(s)/bigIntParseStepDigits); err != nil {
			return NewNil(), err
		}
	}
	bi, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return NewNil(), fmt.Errorf("%s expects a base-10 integer string", name)
	}
	// Charged after materializing rather than projected first, as the JSON
	// parser does: a decimal digit carries under 4 bits, so the big integer
	// is smaller than the string that produced it, and that string is already
	// live and charged. There is no allocation here that the receiver has not
	// already paid for.
	val := value.AdoptBigInt(bi)
	if exec != nil {
		if err := exec.checkMemoryValue(val); err != nil {
			return NewNil(), err
		}
	}
	return val, nil
}

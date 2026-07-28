package runtime

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"

	"github.com/mgomes/vibescript/vibes/value"
)

// maxParsedIntegerDigits caps the digits a string conversion will convert.
//
// big.Int.SetString is superlinear, so the linear step charge below can pass
// well before the conversion is done: a 2,000,000-digit argument fits the
// default memory quota and spends about a quarter of the default step quota,
// yet occupies a worker for seconds. The parser caps integer literals at the
// same 100,000 digits for the same reason; this is that guard on the runtime
// path, where the input can come from outside the script.
const maxParsedIntegerDigits = 100_000

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
func parseIntegerString(exec *Execution, s, name string, source Value) (Value, error) {
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
	// Count digits, not bytes: a leading sign is not a digit, and the parser's
	// equivalent limit does not count one either, so including it would reject
	// a signed value with exactly the advertised number of digits.
	if digitCount(s) > maxParsedIntegerDigits {
		return NewNil(), guardLimitErrorf("%s exceeds the %d digit conversion limit", name, maxParsedIntegerDigits)
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
		// The source string is still live in this frame while the bignum
		// exists, and builtin call roots are invisible to the base estimator,
		// so charging the result alone let a ~100KB decimal string and its
		// ~42KB bignum each fit a 120KB quota while their combined peak did
		// not. Charge them together.
		if err := exec.checkMemoryWith(val, source); err != nil {
			return NewNil(), err
		}
	}
	return val, nil
}

// digitCount returns the length of s without a leading sign.
func digitCount(s string) int {
	if len(s) > 0 && (s[0] == '-' || s[0] == '+') {
		return len(s) - 1
	}
	return len(s)
}

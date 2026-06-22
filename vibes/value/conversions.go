package value

import (
	"fmt"
	"math"
)

// ValueToInt64 coerces an integer or floating-point Value to int64, truncating
// fractional floats toward zero. Non-finite floats (Infinity/-Infinity/NaN) are
// rejected rather than coerced to a garbage int64, mirroring Ruby's
// FloatDomainError. Finite floats outside the int64 range are likewise rejected.
// Any non-numeric kind returns an error.
func ValueToInt64(val Value) (int64, error) {
	switch val.Kind() {
	case KindInt:
		return val.Int(), nil
	case KindFloat:
		f := val.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, fmt.Errorf("cannot convert %s to integer", FormatFloat(f))
		}
		// float64(math.MaxInt64) rounds up to 2^63, so use 2^63 as the exclusive
		// upper bound to reject values that would overflow int64 on truncation.
		if f < float64(math.MinInt64) || f >= math.Exp2(63) {
			return 0, fmt.Errorf("float %s is out of integer range", FormatFloat(f))
		}
		return int64(f), nil
	default:
		return 0, fmt.Errorf("expected integer value")
	}
}

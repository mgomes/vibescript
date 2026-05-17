package value

import "fmt"

// ValueToInt64 coerces an integer or floating-point Value to int64,
// returning an error for any other kind.
func ValueToInt64(val Value) (int64, error) {
	switch val.Kind() {
	case KindInt:
		return val.Int(), nil
	case KindFloat:
		return int64(val.Float()), nil
	default:
		return 0, fmt.Errorf("expected integer value")
	}
}

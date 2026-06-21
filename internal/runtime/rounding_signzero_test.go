package runtime

import (
	"math"
	"testing"
)

// A float that underflows to zero when rounded must yield +0.0, matching Ruby,
// rather than carrying the sign of a tiny negative receiver into the result.
func TestFloatRoundDigitsUnderflowReturnsPositiveZero(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		num  float64
		mode roundMode
	}{
		{"ceil tiny negative", -1e-300, roundCeil},
		{"round tiny negative", -1e-300, roundNearest},
		{"round tiny positive", 1e-300, roundNearest},
		{"floor tiny positive", 1e-300, roundFloor},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := floatRoundDigits(tc.num, 5, tc.mode)
			if got != 0 {
				t.Fatalf("floatRoundDigits(%g, 5, %v) = %g, want 0", tc.num, tc.mode, got)
			}
			if math.Signbit(got) {
				t.Fatalf("floatRoundDigits(%g, 5, %v) = -0.0, want +0.0", tc.num, tc.mode)
			}
		})
	}
}

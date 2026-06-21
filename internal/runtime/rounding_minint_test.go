package runtime

import (
	"math"
	"math/big"
	"testing"
)

// A precision of math.MinInt cannot be negated without overflow. The rounding
// helpers must route it through the beyond-magnitude path (round collapses to
// 0) rather than wrapping to a bogus positive digit count and bucketing by
// 10^0. On 32-bit builds this is the reachable math.MinInt32 precision; using
// math.MinInt exercises the same guard on any platform.
func TestRoundMinIntPrecisionDoesNotOverflow(t *testing.T) {
	t.Parallel()

	if got, err := bigIntRound(big.NewInt(123), math.MinInt, roundNearest, "int.round"); err != nil || got != 0 {
		t.Fatalf("bigIntRound(123, MinInt, round) = (%d, %v), want (0, nil)", got, err)
	}
	if got, err := intRound(123, math.MinInt, roundNearest, "int.round"); err != nil || got != 0 {
		t.Fatalf("intRound(123, MinInt, round) = (%d, %v), want (0, nil)", got, err)
	}
}

package runtime

import (
	"math/big"
	"testing"
)

// decimalDigitCount derives digit counts from bit-length bounds and resolves
// the ambiguous band with comparisons instead of rendering the decimal text.
// Exhaustively cross-check it against the text length around every power-of-
// ten boundary in a wide range, plus power-of-two shapes whose bit counts sit
// on the float boundaries the bounds are computed from.
func TestDecimalDigitCountMatchesTextLength(t *testing.T) {
	t.Parallel()

	check := func(n *big.Int) {
		t.Helper()
		want := len(new(big.Int).Abs(n).Text(10))
		if n.Sign() == 0 {
			want = 1
		}
		if got := decimalDigitCount(n); got != want {
			t.Fatalf("decimalDigitCount(%s...) = %d; want %d", n.Text(10)[:min(20, len(n.Text(10)))], got, want)
		}
	}

	check(big.NewInt(0))
	one := big.NewInt(1)
	// Every power-of-ten boundary up to 10^2000: 10^k-1, 10^k, 10^k+1.
	for k := 1; k <= 2000; k++ {
		p := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(k)), nil)
		check(new(big.Int).Sub(p, one))
		check(p)
		check(new(big.Int).Add(p, one))
		check(new(big.Int).Neg(p))
		nm1 := new(big.Int).Sub(p, one)
		check(new(big.Int).Neg(nm1))
	}
	// Power-of-two shapes: exact bit lengths exercise the float bound math.
	for k := 1; k <= 5000; k += 7 {
		p := new(big.Int).Lsh(one, uint(k))
		check(p)
		check(new(big.Int).Sub(p, one))
		check(new(big.Int).Add(p, one))
	}
}

// The rounding behaviors that consume decimalDigitCount must stay exact for
// values straddling a power of ten (Ruby 3.4 oracle).
func TestBignumRoundingStraddlesPowersOfTen(t *testing.T) {
	t.Parallel()
	got := runSnippetString(t, `
    def run
      a = 10 ** 25
      [
        a.round(-26),
        a.round(-25),
        (a - 1).ceil(-25),
        (a - 1).floor(-25),
        (a + 1).round(-25),
        (a - 1).round(-25),
        (0 - (a - 1)).floor(-25),
        a.ceil(-26)
      ]
    end
  `)
	want := "[0, 10000000000000000000000000, 10000000000000000000000000, 0, 10000000000000000000000000, 10000000000000000000000000, -10000000000000000000000000, 100000000000000000000000000]"
	if got != want {
		t.Fatalf("power-of-ten straddling rounds = %s\nwant %s", got, want)
	}
}

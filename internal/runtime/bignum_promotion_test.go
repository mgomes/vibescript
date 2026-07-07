package runtime

import (
	"math"
	"strings"
	"testing"
)

// The expectations in this file are pinned against Ruby 3.4 (the semantics
// oracle for issue #919): floored division/modulo signs, exact big-vs-float
// comparisons, big-literal parsing, and the int64-only domain boundaries.

// runSnippetValue compiles src (which must define run) and returns run's value.
func runSnippetValue(t *testing.T, src string) Value {
	t.Helper()
	script := compileScript(t, src)
	return callFunc(t, script, "run", nil)
}

func runSnippetString(t *testing.T, src string) string {
	t.Helper()
	return runSnippetValue(t, src).String()
}

func TestBignumFloorDivisionModuloMatchesRuby(t *testing.T) {
	t.Parallel()
	// Ruby 3.4: a = 10**20; b = 3.
	cases := []struct {
		expr string
		want string
	}{
		{"pos_div_pos", "[33333333333333333333, 1]"},
		{"neg_div_pos", "[-33333333333333333334, 2]"},
		{"pos_div_neg", "[-33333333333333333334, -2]"},
		{"neg_div_neg", "[33333333333333333333, -1]"},
	}
	got := runSnippetString(t, `
    def run
      a = 10 ** 20
      b = 3
      na = -a
      nb = -b
      [[a / b, a % b], [na / b, na % b], [a / nb, a % nb], [na / nb, na % nb]]
    end
  `)
	want := "[[33333333333333333333, 1], [-33333333333333333334, 2], [-33333333333333333334, -2], [33333333333333333333, -1]]"
	if got != want {
		t.Fatalf("floor div/mod matrix = %s\nwant %s", got, want)
	}
	_ = cases
}

func TestBignumDivisionByZeroStillRaises(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def div_zero
      (10 ** 20) / 0
    end
    def mod_zero
      (10 ** 20) % 0
    end
  `)
	requireCallErrorContains(t, script, "div_zero", nil, CallOptions{}, "division by zero")
	requireCallErrorContains(t, script, "mod_zero", nil, CallOptions{}, "modulo by zero")
}

func TestBignumLiteralsParse(t *testing.T) {
	t.Parallel()
	got := runSnippetString(t, `
    def run
      [
        340282366920938463463374607431768211456,
        1_000_000_000_000_000_000_000_000,
        0x100000000000000000000000000000000,
        0b100000000000000000000000000000000000000000000000000000000000000000,
        0d18446744073709551616,
        9223372036854775808
      ]
    end
  `)
	want := "[340282366920938463463374607431768211456, 1000000000000000000000000, 340282366920938463463374607431768211456, 36893488147419103232, 18446744073709551616, 9223372036854775808]"
	if got != want {
		t.Fatalf("big literals = %s\nwant %s", got, want)
	}
}

func TestBignumRoundTripRenormalizesToCompact(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run
      x = 2 ** 100
      x - x
    end
    def down
      (9223372036854775807 + 1) - 1
    end
  `)
	zero := callFunc(t, script, "run", nil)
	if zero.IsBigInt() || zero.Int() != 0 {
		t.Fatalf("x - x = %v (big=%v); want compact 0", zero, zero.IsBigInt())
	}
	max := callFunc(t, script, "down", nil)
	if max.IsBigInt() || max.Int() != math.MaxInt64 {
		t.Fatalf("(MaxInt64+1)-1 = %v (big=%v); want compact MaxInt64", max, max.IsBigInt())
	}
}

func TestBignumUnaryMinus(t *testing.T) {
	t.Parallel()
	got := runSnippetString(t, `
    def run
      big = 2 ** 100
      min = -9223372036854775807 - 1
      [-big, -min, -(-big) == big]
    end
  `)
	want := "[-1267650600228229401496703205376, 9223372036854775808, true]"
	if got != want {
		t.Fatalf("unary minus = %s\nwant %s", got, want)
	}
}

func TestBignumExponentiation(t *testing.T) {
	t.Parallel()
	got := runSnippetString(t, `
    def run
      huge = 10 ** 20
      [2 ** 100, (10 ** 20) ** 0, 0 ** huge, 1 ** huge, (-1) ** huge, (-1) ** (huge + 1)]
    end
  `)
	// Ruby: 2**100; x**0 == 1; 0/1/-1 to a huge power stay 0/1/±1 (10^20 is even).
	want := "[1267650600228229401496703205376, 1, 0, 1, 1, -1]"
	if got != want {
		t.Fatalf("exponentiation = %s\nwant %s", got, want)
	}

	script := compileScript(t, `
    def too_large
      2 ** (10 ** 20)
    end
  `)
	// Ruby raises ArgumentError "exponent is too large" here.
	requireCallErrorContains(t, script, "too_large", nil, CallOptions{}, "exponent is too large")
}

func TestBignumComparisonsAreExact(t *testing.T) {
	t.Parallel()
	got := runSnippetString(t, `
    def run
      x = 10 ** 20
      big = 2 ** 100
      [
        (x + 1) == 1e20,          # false: no cross-kind ==
        (x + 1) > 1e20,           # true: exact, float conversion would say equal
        ((x + 1) <=> 1e20) == 1,
        (big <=> 2.0 ** 100) == 0,
        (1e20 <=> (x + 1)) == -1,
        big > 5,
        5 < big,
        -big < 5,
        big < (0.0 - 0.0) / 0.0,  # NaN: relational comparison is false
        (big <=> (0.0 - 0.0) / 0.0) == nil,
        big == big,
        big.eql?(big + 0),
        big.equal?(2 ** 100),     # separate objects, Ruby-style
        big.equal?(big)
      ]
    end
  `)
	want := "[false, true, true, true, true, true, true, true, false, true, true, true, false, true]"
	if got != want {
		t.Fatalf("comparisons = %s\nwant %s", got, want)
	}
}

func TestBignumSortMinMax(t *testing.T) {
	t.Parallel()
	got := runSnippetString(t, `
    def run
      big = 2 ** 100
      values = [big, 1, -big, 5.0, 9223372036854775807]
      [values.sort, values.min, values.max]
    end
  `)
	want := "[[-1267650600228229401496703205376, 1, 5, 9223372036854775807, 1267650600228229401496703205376], -1267650600228229401496703205376, 1267650600228229401496703205376]"
	if got != want {
		t.Fatalf("sort/min/max = %s\nwant %s", got, want)
	}
}

func TestBignumHashKeysAndAggregation(t *testing.T) {
	t.Parallel()
	got := runSnippetString(t, `
    def run
      big = 2 ** 100
      h = {}
      h[big] = "big"
      h[0] = "zero"
      [
        h[2 ** 100],
        h[0],
        h.size,
        [big, 2 ** 100, big + 1, 0, 0].uniq.size,
        [big, 2 ** 100, 1].tally[2 ** 100],
        [big, -big, 2 ** 100].group_by { |v| v }.size
      ]
    end
  `)
	want := `[big, zero, 2, 3, 2, 2]`
	if got != want {
		t.Fatalf("hash keys = %s\nwant %s", got, want)
	}
}

func TestBignumSumAndReducePromote(t *testing.T) {
	t.Parallel()
	got := runSnippetString(t, `
    def run
      [
        [9223372036854775807, 1].sum,
        [9223372036854775807, 9223372036854775807, 2].reduce(:+),
        [2 ** 64, 2 ** 64].sum - 2 ** 65
      ]
    end
  `)
	want := "[9223372036854775808, 18446744073709551616, 0]"
	if got != want {
		t.Fatalf("sum/reduce = %s\nwant %s", got, want)
	}
}

func TestBignumCaseAndRangeMembership(t *testing.T) {
	t.Parallel()
	got := runSnippetString(t, `
    def run
      big = 2 ** 100
      label = case big
              when 1..9223372036854775807 then "bounded"
              else "huge"
              end
      [
        label,
        (1..5) === big,
        (1..) === big,
        (..5) === big,
        (..5) === -big,
        (1..5).include?(big),
        (1..).include?(big),
        (..0).cover?(-big)
      ]
    end
  `)
	want := "[huge, false, true, false, true, false, true, true]"
	if got != want {
		t.Fatalf("range membership = %s\nwant %s", got, want)
	}
}

func TestBignumRangeEndpointsRejected(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def bad_end
      (1..(2 ** 100)).to_a
    end
    def bad_start
      ((0 - 2 ** 100)..5).to_a
    end
  `)
	requireCallErrorContains(t, script, "bad_end", nil, CallOptions{}, "range endpoints must fit in a 64-bit integer")
	requireCallErrorContains(t, script, "bad_start", nil, CallOptions{}, "range endpoints must fit in a 64-bit integer")
}

func TestBignumRejectedAtInt64Sites(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def index_site
      [1, 2, 3][2 ** 100]
    end
    def count_site
      [1, 2, 3].first(2 ** 100)
    end
    def duration_site
      5.seconds + 2 ** 100
    end
    def money_site
      money("1.00 USD") * (2 ** 100)
    end
    def time_site
      Time.now + 2 ** 100
    end
  `)
	requireCallErrorContains(t, script, "index_site", nil, CallOptions{}, "index must fit in a 64-bit integer")
	// array.first wraps every count-conversion failure in its own message,
	// exactly as it does for oversized float counts today; the important
	// property is a loud error rather than silent truncation.
	requireCallErrorContains(t, script, "count_site", nil, CallOptions{}, "array.first expects non-negative integer")
	requireCallErrorContains(t, script, "duration_site", nil, CallOptions{}, "duration addition result out of int64 range")
	requireCallErrorContains(t, script, "money_site", nil, CallOptions{}, "money arithmetic overflow")
	requireCallErrorContains(t, script, "time_site", nil, CallOptions{}, "time addition result out of int64 range")
}

func TestBignumTypedIntContractAcceptsBig(t *testing.T) {
	t.Parallel()
	got := runSnippetString(t, `
    def double(n: int) -> int
      n * 2
    end

    def run
      double(2 ** 100)
    end
  `)
	if got != "2535301200456458802993406410752" {
		t.Fatalf("typed int contract result = %s", got)
	}
}

func TestBignumInterpolation(t *testing.T) {
	t.Parallel()
	got := runSnippetString(t, `
    def run
      "value: #{2 ** 100}!"
    end
  `)
	if got != "value: 1267650600228229401496703205376!" {
		t.Fatalf("interpolation = %q", got)
	}
}

func TestBignumMixedFloatArithmetic(t *testing.T) {
	t.Parallel()
	// int(big) + float goes through best-effort float conversion (Ruby: may
	// lose precision), and huge magnitudes saturate to Infinity like #to_f.
	got := runSnippetString(t, `
    def run
      big = 10 ** 20
      huge = 10 ** 400
      [big + 0.5 == 1.0e20, big * 1.0 == 1.0e20, (huge * 1.0).infinite?, ((0 - huge) * 1.0).infinite?]
    end
  `)
	want := "[true, true, 1, -1]"
	if got != want {
		t.Fatalf("mixed float arithmetic = %s\nwant %s", got, want)
	}
}

func TestBignumNegativeExponentKeepsFloatFallthrough(t *testing.T) {
	t.Parallel()
	got := runSnippetValue(t, `
    def run
      2 ** -2
    end
  `)
	if got.Kind() != KindFloat || got.Float() != 0.25 {
		t.Fatalf("2 ** -2 = %v (kind %v); want the historical 0.25 float", got, got.Kind())
	}
}

func TestBignumParserRejectsMalformedBigLiteral(t *testing.T) {
	t.Parallel()
	engine := MustNewEngine(Config{})
	_, err := engine.Compile(`x = 0x`)
	if err == nil || !strings.Contains(err.Error(), "numeric literal") {
		t.Fatalf("malformed literal error = %v", err)
	}
}

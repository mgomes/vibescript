package runtime

import (
	"context"
	"math"
	"testing"
)

// TestOpenRangeSlicing pins the primary use of open-ended ranges: slicing
// arrays and strings from a bound to the receiver's edge.
func TestOpenRangeSlicing(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  a = [10, 20, 30, 40]
  s = "hello"
  [
    a[1..], a[..1], a[1...3], a[..-2], a[2..],
    s[2..], s[..1], s.byteslice(2..), s.byteslice(..1),
    a.slice(..1), a.values_at(2..), a.fill(0, 2..)
  ]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewArray([]Value{NewInt(20), NewInt(30), NewInt(40)}),
		NewArray([]Value{NewInt(10), NewInt(20)}),
		NewArray([]Value{NewInt(20), NewInt(30)}),
		NewArray([]Value{NewInt(10), NewInt(20), NewInt(30)}),
		NewArray([]Value{NewInt(30), NewInt(40)}),
		NewString("llo"),
		NewString("he"),
		NewString("llo"),
		NewString("he"),
		NewArray([]Value{NewInt(10), NewInt(20)}),
		NewArray([]Value{NewInt(30), NewInt(40)}),
		NewArray([]Value{NewInt(10), NewInt(20), NewInt(0), NewInt(0)}),
	})
}

// TestOpenRangeMembership pins case equality and cover?/include? for open
// ranges: a beginless range admits everything at or below its end, an endless
// range everything at or above its start.
func TestOpenRangeMembership(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  a = case 5
  when 3.. then "big"
  else "small"
  end
  b = case 2
  when ..3 then "low"
  else "high"
  end
  [
    a, b,
    (1..) === 100, (1..) === 0, (..0) === -5, (..0) === 1,
    (1...) === 1, (...5) === 5, (...5) === 4.9,
    (1..).cover?(7), (..9).include?(10), (1..).member?(1)
  ]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewString("big"), NewString("low"),
		NewBool(true), NewBool(false), NewBool(true), NewBool(false),
		NewBool(true), NewBool(false), NewBool(true),
		NewBool(true), NewBool(false), NewBool(true),
	})
}

// TestOpenRangeEndpointsAndClamp pins the finite endpoint reads and the
// open-sided clamp forms.
func TestOpenRangeEndpointsAndClamp(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  [
    (1..).first, (..9).last, (1..).exclude_end?, (1...).exclude_end?,
    5.clamp(1..), 5.clamp(..3), 0.clamp(2..), 7.clamp(..10),
    "#{1..}", "#{..5}", "#{1...}", "#{2..}"
  ]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewInt(1), NewInt(9), NewBool(false), NewBool(true),
		NewInt(5), NewInt(3), NewInt(2), NewInt(7),
		NewString("1.."), NewString("..5"), NewString("1..."), NewString("2.."),
	})
}

// TestOpenRangeIterationGuards pins that every genuinely unbounded
// enumeration path rejects open ranges up front instead of running into the
// sandbox quotas. Bounded windows anchored at the known endpoint — first(n)
// on an endless range — are allowed and covered by TestEndlessRangeFirstN.
func TestOpenRangeIterationGuards(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def endless_to_a
  (1..).to_a
end

def endless_each
  (1..).each do |x|
    x
  end
end

def beginless_size
  (..5).size
end

def endless_for
  for i in (1..)
    i
  end
end

def endless_rand
  rand(1..)
end

def endless_min
  (1..).min
end

def beginless_first
  (..5).first
end

def beginless_first_n
  (..5).first(3)
end

def endless_last
  (1..).last
end

def endless_last_n
  (1..).last(3)
end

def beginless_last_n
  (..5).last(3)
end

def endless_step
  (1..).step(2) do |x|
    x
  end
end`)

	cases := map[string]string{
		"endless_to_a":      "cannot iterate an endless range",
		"endless_each":      "cannot iterate an endless range",
		"beginless_size":    "cannot iterate a beginless range",
		"endless_for":       "cannot iterate an endless range",
		"endless_rand":      "rand range must be bounded",
		"endless_min":       "cannot iterate an endless range",
		"beginless_first":   "cannot get the first element of a beginless range",
		"beginless_first_n": "cannot get the first element of a beginless range",
		"endless_last":      "cannot get the last element of an endless range",
		"endless_last_n":    "cannot get the last element of an endless range",
		"beginless_last_n":  "cannot iterate a beginless range",
		"endless_step":      "cannot iterate an endless range",
	}
	for fn, want := range cases {
		requireCallErrorContains(t, script, fn, nil, CallOptions{}, want)
	}
}

// TestEndlessRangeFirstN pins that first(n) on an endless range is bounded
// work and succeeds: the leading window starts at the known endpoint, so the
// open end never matters. Exclusivity refers to the missing end and is
// irrelevant, and n = 0 returns an empty array, both matching Ruby.
func TestEndlessRangeFirstN(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  [(1..).first(3), (1...).first(3), (1..).first(0), (-2..).first(4), (5..).first]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)}),
		NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)}),
		NewArray([]Value{}),
		NewArray([]Value{NewInt(-2), NewInt(-1), NewInt(0), NewInt(1)}),
		NewInt(5),
	})
}

// TestEndlessRangeFirstNClampsAtInt64Ceiling pins the int64 edge: an endless
// range starting near MaxInt64 yields only the remaining representable
// integers rather than wrapping past MaxInt64, mirroring the clean
// terminal-value stop bounded iteration uses at the ceiling. (Ruby's bignums
// keep counting past 2^63-1; a 64-bit-integer runtime cannot.)
func TestEndlessRangeFirstNClampsAtInt64Ceiling(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  (9223372036854775800..).first(20)
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	want := make([]Value, 0, 8)
	for v := int64(9223372036854775800); ; v++ {
		want = append(want, NewInt(v))
		if v == math.MaxInt64 {
			break
		}
	}
	compareArrays(t, got, want)
}

// TestOpenRangeHashKeys pins that open ranges are distinct hash keys from any
// bounded range sharing an endpoint.
// A range is not a hash key: hash keys are strings and symbols only, and the
// open forms are rejected like every other kind.
func TestOpenRangeIsNotAHashKey(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  h = {}
  h[(1..)] = "endless"
  h.size
end`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{},
		"hash keys must be strings or symbols")
}

// TestBareMultilineRangeStatementSplit pins the statement-level newline rule
// end to end: x = 1.. on its own line is endless and the next line is a
// separate statement, while grouped forms keep multiline bounded endpoints.
func TestBareMultilineRangeStatementSplit(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  x = 1..
  y = 2
  grouped = [3..
    4]
  [x === 99, y, grouped[0].last]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{NewBool(true), NewInt(2), NewInt(4)})
}

// TestWhenValueGroupedMultilineRangeStaysBounded pins the adversarial-review
// finding: the when-value newline rule applies only at the value's own group
// depth, so a parenthesized or call-grouped bounded endpoint may still
// continue onto the next line inside a when clause.
func TestWhenValueGroupedMultilineRangeStaysBounded(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def id(x)
  x
end

def run
  a = case 7
  when (3..
    9) then "in"
  else "out"
  end
  b = case 7
  when id(3..
    9) then "in"
  else "out"
  end
  [a, b]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{NewString("in"), NewString("in")})
}

// TestOpenRangeFloatMembershipBeyondInt64 pins one-sided membership for float
// magnitudes past the int64 guard band, including infinities.
func TestOpenRangeFloatMembershipBeyondInt64(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  [
    (0..) === 1.0e19, (..0) === -1.0e19,
    (0..) === (1.0 / 0), (..0) === (1.0 / 0),
    (0..) === (-1.0 / 0), (..0) === (-1.0 / 0),
    (0..) === (0.0 / 0.0)
  ]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewBool(true), NewBool(true),
		NewBool(true), NewBool(false),
		NewBool(false), NewBool(true),
		NewBool(false),
	})
}

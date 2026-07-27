package runtime

import (
	"context"
	"math"
	"testing"
)

// A Duration scaled by a float used to truncate the factor to an integer, so
// every factor below one collapsed to a zero duration: 1.hour * 0.5 was 0s.
// That is the most dangerous direction to round in, because a zero duration
// reads as "immediately" — a retry with no backoff, a window with no span.
// These pin the scaled results so the truncation cannot come back.
func TestDurationFloatScalingRoundsInsteadOfTruncating(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def multiply(left, right)
      left * right
    end

    def divide(left, right)
      left / right
    end
    `)

	hour := NewDuration(durationFromSeconds(3600))
	tenDays := NewDuration(durationFromSeconds(864000))
	oneSecond := NewDuration(durationFromSeconds(1))

	cases := []struct {
		name string
		fn   string
		args []Value
		want int64
	}{
		// The regression: factors below one must not collapse to zero.
		{name: "half_hour", fn: "multiply", args: []Value{hour, NewFloat(0.5)}, want: 1800},
		{name: "almost_whole_hour", fn: "multiply", args: []Value{hour, NewFloat(0.99)}, want: 3564},
		{name: "tenth_of_ten_days", fn: "multiply", args: []Value{tenDays, NewFloat(0.1)}, want: 86400},

		// Factors above one must scale fully rather than losing the fraction.
		{name: "one_and_a_half_hours", fn: "multiply", args: []Value{hour, NewFloat(1.5)}, want: 5400},
		{name: "two_point_nine_hours", fn: "multiply", args: []Value{hour, NewFloat(2.9)}, want: 10440},

		// Scaling is commutative.
		{name: "float_on_the_left", fn: "multiply", args: []Value{NewFloat(0.5), hour}, want: 1800},

		// Division by a fractional divisor divides rather than reporting a
		// division by zero from the truncated divisor.
		{name: "divide_by_one_and_a_half", fn: "divide", args: []Value{hour, NewFloat(1.5)}, want: 2400},
		{name: "divide_by_half", fn: "divide", args: []Value{hour, NewFloat(0.5)}, want: 7200},

		// Rounds to nearest, half away from zero, at the one-second resolution.
		{name: "rounds_half_up", fn: "multiply", args: []Value{oneSecond, NewFloat(1.5)}, want: 2},
		{name: "rounds_down", fn: "multiply", args: []Value{oneSecond, NewFloat(1.4)}, want: 1},
		{name: "negative_rounds_away_from_zero", fn: "multiply", args: []Value{oneSecond, NewFloat(-1.5)}, want: -2},

		// Integer operands keep the exact integer path.
		{name: "int_factor_exact", fn: "multiply", args: []Value{hour, NewInt(2)}, want: 7200},
		{name: "int_divisor_exact", fn: "divide", args: []Value{hour, NewInt(2)}, want: 1800},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := script.Call(context.Background(), tc.fn, tc.args, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if got.Kind() != KindDuration {
				t.Fatalf("%s returned %v, want a duration", tc.name, got.Kind())
			}
			if secs := got.Duration().Seconds(); secs != tc.want {
				t.Fatalf("%s = %ds, want %ds", tc.name, secs, tc.want)
			}
		})
	}
}

// Float scaling must still reject the operands that cannot produce a duration,
// rather than silently yielding one.
func TestDurationFloatScalingRejectsUnusableOperands(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def multiply(left, right)
      left * right
    end

    def divide(left, right)
      left / right
    end
    `)

	hour := NewDuration(durationFromSeconds(3600))
	nan := NewFloat(math.NaN())
	inf := NewFloat(math.Inf(1))

	cases := []struct {
		name string
		fn   string
		args []Value
	}{
		{name: "nan_factor", fn: "multiply", args: []Value{hour, nan}},
		{name: "inf_factor", fn: "multiply", args: []Value{hour, inf}},
		{name: "nan_divisor", fn: "divide", args: []Value{hour, nan}},
		// A genuine zero divisor still reports division by zero; only a
		// truncated-to-zero fractional divisor should have stopped doing so.
		{name: "float_zero_divisor", fn: "divide", args: []Value{hour, NewFloat(0)}},
		{name: "int_zero_divisor", fn: "divide", args: []Value{hour, NewInt(0)}},
		// Scaling past the int64 second range is a range error, not a wrap.
		{name: "overflowing_factor", fn: "multiply", args: []Value{hour, NewFloat(1e18)}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := script.Call(context.Background(), tc.fn, tc.args, CallOptions{}); err == nil {
				t.Fatalf("%s: expected an error, got none", tc.name)
			}
		})
	}
}

package runtime

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// minStepsForStringOp binary-searches the smallest step quota at which one
// expression over a haystack of the given size completes.
func minStepsForStringOp(t *testing.T, expr string, bytes int) int {
	t.Helper()

	hay := NewString(strings.Repeat("ab", bytes/2))
	src := fmt.Sprintf("def run(s)\n  %s\nend", expr)

	lo, hi := 1, bytes+10000
	for lo < hi {
		mid := (lo + hi) / 2
		script := compileScriptWithConfig(t, Config{StepQuota: mid, MemoryQuotaBytes: Unlimited}, src)
		if _, err := script.Call(context.Background(), "run", []Value{hay}, CallOptions{}); err != nil {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// A string method's work grows with its receiver but it dispatches as one call,
// so charging a flat handful of steps let a script scan a host-supplied string
// of any size on a constant budget. Repeatedly upcasing an 800 KB string burned
// five minutes inside the default 1M-step profile before the quota fired
// (#1131). The charge is amortized at one step per stringScanBytesPerStep, so a
// receiver eight times longer costs meaningfully more rather than the same.
func TestStringOpsChargeStepsPerByte(t *testing.T) {
	t.Parallel()

	ops := []string{
		"s.upcase.length",
		"s.downcase.length",
		"s.reverse.length",
		"s.gsub(\"zzz\", \"y\").length",
		"s.sub(\"zzz\", \"y\").length",
		"s.split(\"z\").length",
		"s.scan(\"zzz\").length",
		"s.count(\"z\")",
		"s.index(\"zzz\").inspect",
		"s.rindex(\"zzz\").inspect",
		"s.include?(\"zzz\").inspect",
		"s.chars.length",
		"s.length",
	}

	const small, large = 8 << 10, 64 << 10
	for _, expr := range ops {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			atSmall := minStepsForStringOp(t, expr, small)
			atLarge := minStepsForStringOp(t, expr, large)

			// An 8x receiver charges 8x the scan. Require 4x so the bound tracks
			// proportionality rather than the exact rate, while staying far above
			// the flat charge this replaces.
			if atLarge < atSmall*4 {
				t.Errorf("%s cost %d steps over %d bytes and %d over %d; an eight times "+
					"longer receiver must cost meaningfully more, so the scan is still "+
					"charged as a constant", expr, atSmall, small, atLarge, large)
			}
		})
	}
}

// Methods whose work does not grow with the receiver stay exempt, so reading a
// length or emptiness off a large string does not consume the quota. The
// exemption list is what keeps the charge from taxing constant-cost accessors.
func TestConstantCostStringOpsStayUncharged(t *testing.T) {
	t.Parallel()

	exempt := []string{
		"s.bytesize", "s.empty?.inspect", "s.getbyte(0).inspect",
		// A byte-indexed slice returns a substring view and a symbol holds the
		// receiver's string header, so neither reads the receiver's length.
		"s.byteslice(0, 1).bytesize", "s.to_sym.inspect.bytesize", "s.intern.inspect.bytesize",
	}
	for _, expr := range exempt {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			atSmall := minStepsForStringOp(t, expr, 8<<10)
			atLarge := minStepsForStringOp(t, expr, 64<<10)
			if atLarge != atSmall {
				t.Errorf("%s cost %d steps over 8 KiB and %d over 64 KiB; a constant-cost "+
					"method must not be charged for its receiver's length", expr, atSmall, atLarge)
			}
		})
	}
}

// The amortized rate must leave ordinary short strings costing nothing extra: a
// receiver below the per-step byte budget rounds down to no charge at all, so
// two sub-budget sizes cost exactly the same.
func TestShortStringOpsPayNoScanCharge(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{"s.upcase.length", "s.index(\"zzz\").inspect", "s.split(\"z\").length"} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			tiny := minStepsForStringOp(t, expr, 2)
			nearBudget := minStepsForStringOp(t, expr, stringScanBytesPerStep-2)
			if tiny != nearBudget {
				t.Errorf("%s cost %d steps over 2 bytes and %d over %d; a receiver below "+
					"the per-step byte budget must round down to no scan charge, so short "+
					"strings are unaffected by the rate", expr, tiny, nearBudget,
					stringScanBytesPerStep-2)
			}
		})
	}
}

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

	// No expression here renders its result: rendering is charged for the bytes
	// it prints (see TestSymbolRenderingChargesForItsName), so an .inspect tail
	// would measure the tail rather than the method under test.
	exempt := []string{
		"s.bytesize", "s.empty?", "s.getbyte(0)",
		// A byte-indexed slice returns a substring view, a symbol holds the
		// receiver's string header without copying it, and replace ignores the
		// receiver and returns its argument.
		"s.byteslice(0, 1)", "s.to_sym", "s.intern", "s.replace(\"x\")",
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
			// Sized so the receiver and its arguments together stay under the
			// per-step budget: the charge covers both, so a receiver just below
			// the budget plus a needle crosses it legitimately.
			tiny := minStepsForStringOp(t, expr, 2)
			nearBudget := minStepsForStringOp(t, expr, stringScanBytesPerStep/2)
			if tiny != nearBudget {
				t.Errorf("%s cost %d steps over 2 bytes and %d over %d; a call whose "+
					"receiver and arguments together fall below the per-step byte budget "+
					"must round down to no scan charge, so short strings are unaffected "+
					"by the rate", expr, tiny, nearBudget, stringScanBytesPerStep/2)
			}
		})
	}
}

// A symbol built from a host-supplied string carries that whole string as its
// name, and rendering it scans every byte. InspectByteLenBounded steps per
// element, so a scalar symbol was charged one step no matter how long its name
// -- which let s.to_sym.inspect do the linear work the scan charge exists to
// bound, through a receiver the exemption list had just made free.
func TestSymbolRenderingChargesForItsName(t *testing.T) {
	t.Parallel()

	// Every way a value can be turned into text, not just the one that was
	// reported: inspect, interpolation, to_s, puts, and join each render the
	// name and each must charge for it. Fixing only the site in front of you is
	// how the previous round left interpolation open.
	renderings := []string{
		"s.to_sym.inspect.bytesize",
		"s.intern.inspect.bytesize",
		"\"#{s.to_sym}\".bytesize",
		"[s.to_sym].join(\",\").bytesize",
		"[s.to_sym].to_s.bytesize",
		"{a: s.to_sym}.to_s.bytesize",
	}
	for _, expr := range renderings {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			atSmall := minStepsForStringOp(t, expr, 8<<10)
			atLarge := minStepsForStringOp(t, expr, 64<<10)
			if atLarge < atSmall*4 {
				t.Errorf("%s cost %d steps over 8 KiB and %d over 64 KiB; rendering a "+
					"symbol must be charged for the name it prints, or exempting the "+
					"conversion reopens the unmetered scan", expr, atSmall, atLarge)
			}
		})
	}
}

// The direct-call fast path must be indistinguishable from member dispatch, so
// its scan charge lands after the arguments are evaluated. Charging first let a
// quota that trips on the receiver skip an argument's side effect on this path
// while the same call through dispatch still performed it.
//
// The argument raises, so which error surfaces says which ran first: the
// argument's own error means it was evaluated, a quota error means the charge
// preempted it. That needs no side channel, and a host-passed container is not
// one -- mutations a script makes to it are not visible to the caller.
func TestDirectStringCallsEvaluateArgumentsBeforeCharging(t *testing.T) {
	t.Parallel()

	calls := map[string]string{
		"index":  "s.index(boom())",
		"rindex": "s.rindex(boom())",
		"split":  "s.split(boom())",
		"slice":  "s.slice(boom(), 1)",
	}
	names := []string{"index", "rindex", "split", "slice"}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			src := "def boom()\n  raise \"ARGRAN\"\nend\ndef run(s)\n  " + calls[name] + "\nend"
			hay := NewString(strings.Repeat("ab", (64<<10)/2))

			// Quotas spanning far below the receiver scan's cost up to well above
			// it: the argument runs first at every one of them.
			for _, quota := range []int{40, 60, 200, 2000} {
				script := compileScriptWithConfig(t, Config{StepQuota: quota, MemoryQuotaBytes: Unlimited}, src)
				_, err := script.Call(context.Background(), "run", []Value{hay}, CallOptions{})
				if err == nil {
					t.Fatalf("%s did not raise at %d steps", calls[name], quota)
				}
				if !strings.Contains(err.Error(), "ARGRAN") {
					t.Errorf("%s failed with %q at %d steps rather than the argument's own "+
						"error; the fast path charged for the receiver before evaluating the "+
						"argument, which member dispatch does not do", calls[name], err, quota)
				}
			}
		})
	}
}

// format sizes its output per verb, and the hexadecimal branch measures a
// string or symbol input itself rather than going through the shared
// string-bytes projection, so it did not inherit that projection's charge.
// Every verb that consumes the whole input is covered here, not just the one
// that was reported.
func TestFormatChargesForTheInputItRenders(t *testing.T) {
	t.Parallel()

	verbs := []string{
		// The pattern itself is scanned for verbs and its literal text copied,
		// so a host-supplied format string costs its own length even with no
		// arguments. This was the worst case found: format(f) with a 512 KB
		// pattern ran for 1m46s inside the default profile and the quota never
		// fired at all.
		"format(s).bytesize",
		"format(\"%x\", s).bytesize",
		"format(\"%X\", s).bytesize",
		"format(\"%s\", s).bytesize",
		"format(\"%q\", s).bytesize",
		"format(\"%x\", s.to_sym).bytesize",
	}
	for _, expr := range verbs {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			atSmall := minStepsForStringOp(t, expr, 8<<10)
			atLarge := minStepsForStringOp(t, expr, 64<<10)
			if atLarge < atSmall*4 {
				t.Errorf("%s cost %d steps over 8 KiB and %d over 64 KiB; formatting must "+
					"charge for the input it renders", expr, atSmall, atLarge)
			}
		})
	}
}

// rindex without an explicit offset counts the receiver's runes to find where
// to start, which is a full scan of its own. It must not run before the charge,
// or an exhausted quota still pays for it.
func TestRindexDefaultOffsetScanRunsAfterCharging(t *testing.T) {
	t.Parallel()

	hay := NewString(strings.Repeat("ab", (256<<10)/2))
	src := "def run(s)\n  s.rindex(\"zzz\")\nend"

	// A quota far too small for the receiver scan: the call must fail without
	// having walked the receiver to compute its default offset.
	script := compileScriptWithConfig(t, Config{StepQuota: 8, MemoryQuotaBytes: Unlimited}, src)
	if _, err := script.Call(context.Background(), "run", []Value{hay}, CallOptions{}); err == nil {
		t.Fatal("rindex over a 256 KiB receiver completed on an 8-step quota")
	}

	// An explicit offset also skips computing the default it would replace.
	// That saving is not separately observable in steps -- the rune scan was
	// never charged on its own, which is the whole defect -- so this pins only
	// that supplying an offset does not cost more than the receiver charge plus
	// evaluating the extra argument.
	withDefault := minStepsForStringOp(t, "s.rindex(\"zzz\").inspect", 64<<10)
	withExplicit := minStepsForStringOp(t, "s.rindex(\"zzz\", 10).inspect", 64<<10)
	if withExplicit > withDefault+2 {
		t.Errorf("rindex cost %d steps with an explicit offset and %d with the default; "+
			"an explicit offset replaces the default rather than adding to it",
			withExplicit, withDefault)
	}
}

// A short receiver with a large argument moves just as many bytes as the
// reverse, so the charge covers string arguments copied into the result, not
// only the receiver.
func TestStringArgumentsAreChargedToo(t *testing.T) {
	t.Parallel()

	// The receiver is a small literal; the host-supplied string is the argument.
	ops := []string{
		"\"\".concat(s).bytesize",
		"\"\".prepend(s).bytesize",
		"\"ab\".insert(1, s).bytesize",
		"\"ab\".sub(\"a\", s).bytesize",
	}
	for _, expr := range ops {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			atSmall := minStepsForStringOp(t, expr, 8<<10)
			atLarge := minStepsForStringOp(t, expr, 64<<10)
			if atLarge < atSmall*4 {
				t.Errorf("%s cost %d steps over an 8 KiB argument and %d over 64 KiB; "+
					"bytes copied out of an argument must be charged like bytes copied "+
					"out of the receiver", expr, atSmall, atLarge)
			}
		})
	}
}

// The String % operator reaches the shared formatting path straight from the
// evaluator, without passing through the format builtin. Charging the pattern
// at the builtin alone left this entrance unmetered, so the charge belongs on
// the shared path both of them reach.
func TestFormatOperatorChargesLikeTheBuiltin(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{
		"(s % []).bytesize",
		"(\"%.65536s\" % [s]).bytesize",
		"format(\"%.65536s\", s).bytesize",
		"format(\"%.65536q\", s).bytesize",
	} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			atSmall := minStepsForStringOp(t, expr, 8<<10)
			atLarge := minStepsForStringOp(t, expr, 64<<10)
			if atLarge < atSmall*4 {
				t.Errorf("%s cost %d steps over 8 KiB and %d over 64 KiB; every entrance "+
					"to formatting must charge for the bytes it scans", expr, atSmall, atLarge)
			}
		})
	}
}

// minStepsForWidth binary-searches the smallest step quota at which a padding
// call of the given width completes. Padding scales with a number rather than
// with its inputs, so the size that has to vary is the width.
func minStepsForWidth(t *testing.T, expr string, width int) int {
	t.Helper()

	src := fmt.Sprintf("def run(s)\n  %s\nend", fmt.Sprintf(expr, width))
	lo, hi := 1, width/stringScanBytesPerStep+10000
	for lo < hi {
		mid := (lo + hi) / 2
		script := compileScriptWithConfig(t, Config{StepQuota: mid, MemoryQuotaBytes: Unlimited}, src)
		if _, err := script.Call(context.Background(), "run", []Value{NewString("")}, CallOptions{}); err != nil {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// Padding and repetition take their cost from a number rather than from their
// inputs: "".ljust(8_000_000, "x") and "x" * 8_000_000 each write eight million
// bytes from inputs of a few bytes, so a charge based on receiver and argument
// lengths sees nothing at all. They ran for 8.5s and 7.6s inside the default
// step profile, both completing without the quota ever firing.
func TestPaddingChargesForTheBytesItWrites(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{
		"\"\".ljust(%d, \"x\").bytesize",
		"\"\".rjust(%d, \"x\").bytesize",
		"\"\".center(%d, \"x\").bytesize",
		// String#* is the same shape: a short receiver and a script-chosen
		// count. It ran 7.6s inside the default profile and completed.
		"(\"x\" * %d).bytesize",
	} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			const small, large = 64 << 10, 512 << 10
			atSmall := minStepsForWidth(t, expr, small)
			atLarge := minStepsForWidth(t, expr, large)
			if atLarge < atSmall*4 {
				t.Errorf("%s cost %d steps at width %d and %d at width %d; padding must "+
					"charge for the bytes it writes, which its inputs do not bound",
					expr, atSmall, small, atLarge, large)
			}
		})
	}
}

// Counting runes walks the whole value however small the field that will hold
// them, so a precision or width cannot bound it. format("%1.1s", s) charged for
// precision 1 while scanning a multi-megabyte receiver in full.
func TestPrecisionWidthFormattingChargesTheFullScan(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{
		"format(\"%1.1s\", s).bytesize",
		"format(\"%2.2s\", s).bytesize",
	} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			atSmall := minStepsForStringOp(t, expr, 8<<10)
			atLarge := minStepsForStringOp(t, expr, 64<<10)
			if atLarge < atSmall*4 {
				t.Errorf("%s cost %d steps over 8 KiB and %d over 64 KiB; a narrow field "+
					"does not bound the rune count, so the traversal must be charged",
					expr, atSmall, atLarge)
			}
		})
	}
}

// A method that only matches its arguments against the receiver cannot inspect
// more of one than the receiver holds, so a large argument it never reads must
// not exhaust the quota: "a".start_with?("a", huge) returns on the first
// prefix. Charging every string argument in full made that call fail instead of
// answering true.
func TestComparisonArgumentsAreCappedByTheReceiver(t *testing.T) {
	t.Parallel()

	// The receiver is two bytes; the host-supplied string is a later argument
	// the method never needs to read.
	calls := []string{
		"\"ab\".start_with?(\"ab\", s).inspect",
		"\"ab\".end_with?(\"ab\", s).inspect",
		"\"ab\".include?(s).inspect",
		"\"ab\".index(s).inspect",
	}
	for _, expr := range calls {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			atSmall := minStepsForStringOp(t, expr, 8<<10)
			atLarge := minStepsForStringOp(t, expr, 512<<10)
			if atLarge != atSmall {
				t.Errorf("%s cost %d steps with an 8 KiB argument and %d with 512 KiB; a "+
					"two-byte receiver bounds what these can inspect, so a larger argument "+
					"must not cost more", expr, atSmall, atLarge)
			}
		})
	}

	// The cap must not reach methods that copy an argument into the result.
	for _, expr := range []string{"\"ab\".concat(s).bytesize", "\"ab\".sub(\"a\", s).bytesize"} {
		t.Run("copies "+expr, func(t *testing.T) {
			t.Parallel()
			atSmall := minStepsForStringOp(t, expr, 8<<10)
			atLarge := minStepsForStringOp(t, expr, 64<<10)
			if atLarge < atSmall*4 {
				t.Errorf("%s cost %d steps over 8 KiB and %d over 64 KiB; an argument "+
					"copied into the result is not bounded by the receiver", expr, atSmall, atLarge)
			}
		})
	}
}

// The receiver bounds only what a method compares against it. An argument the
// method preprocesses on its own -- a character set count parses byte by byte,
// a pattern match compiles the whole thing -- is not bounded by the receiver,
// so capping those at its length let "".count(s) parse a large argument for
// almost nothing. It ran 1.7s inside the default profile without the quota
// firing.
func TestPreprocessedArgumentsAreNotCappedByTheReceiver(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{
		"\"\".count(s)",
		"\"\".split(s).length",
	} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			atSmall := minStepsForStringOp(t, expr, 8<<10)
			atLarge := minStepsForStringOp(t, expr, 64<<10)
			if atLarge < atSmall*4 {
				t.Errorf("%s cost %d steps over 8 KiB and %d over 64 KiB; an argument the "+
					"method preprocesses is not bounded by the receiver and must not be "+
					"capped there", expr, atSmall, atLarge)
			}
		})
	}
}

// A format argument that is not itself a string is walked one step per node,
// which bounds its shape but not its size: a large string nested in a
// one-element array is a single node. The rendered payload is what gets
// materialized, so that is what must be charged.
func TestAggregateFormatArgumentsChargeRenderedBytes(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{
		"format(\"%s\", [s]).bytesize",
		"format(\"%s\", {a: s}).bytesize",
		// A width alongside a precision needs the value's rune count, which
		// traverses the whole nested string however narrow the field.
		"format(\"%1.1s\", [s]).bytesize",
		"format(\"%2.2s\", {a: s}).bytesize",
	} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			atSmall := minStepsForStringOp(t, expr, 8<<10)
			atLarge := minStepsForStringOp(t, expr, 64<<10)
			if atLarge < atSmall*4 {
				t.Errorf("%s cost %d steps over 8 KiB and %d over 64 KiB; the bytes an "+
					"aggregate renders to must be charged, not just its node count",
					expr, atSmall, atLarge)
			}
		})
	}
}

// A precision with no width is the case that genuinely does not scan: the
// projection walks the value only up to the precision's byte limit and stops,
// so the cost is bounded by the field rather than by the argument. Pinned as a
// control, so the charges above are not mistaken for a rule that every
// aggregate format must scale.
func TestPrecisionOnlyAggregateFormatStaysBounded(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{
		"format(\"%.4s\", [s]).bytesize",
		"format(\"%.4s\", {a: s}).bytesize",
	} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			atSmall := minStepsForStringOp(t, expr, 8<<10)
			atLarge := minStepsForStringOp(t, expr, 64<<10)
			if atLarge != atSmall {
				t.Errorf("%s cost %d steps over 8 KiB and %d over 64 KiB; a precision "+
					"without a width stops the walk at its own limit, so the argument's "+
					"size must not matter", expr, atSmall, atLarge)
			}
		})
	}
}

// The aggregate rune walk visits bytes but reports runes, so charging the rune
// count directly under-charged multibyte text by up to four times: the same
// 64 KiB of four-byte characters traverses as many bytes as 64 KiB of ASCII
// while counting a quarter as many runes. The count is scaled to its widest
// encoding, which never charges fewer steps than the bytes traversed deserve.
//
// This over-charges ASCII on the same path, and that is the deliberate side of
// the trade: the alternative is a second full traversal to learn the exact byte
// length, and under-charging a scan is what this metering exists to prevent.
func TestAggregateRuneWalkNeverUnderchargesMultibyte(t *testing.T) {
	t.Parallel()

	const bytes = 64 << 10
	texts := map[string]string{
		"ascii":     strings.Repeat("a", bytes),
		"four-byte": strings.Repeat("\U0001F600", bytes/4),
		"two-byte":  strings.Repeat("\u00e9", bytes/2),
	}
	for name, text := range texts {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			src := "def run(s)\n  format(\"%1.1s\", [s]).bytesize\nend"
			lo, hi := 1, 1<<20
			for lo < hi {
				mid := (lo + hi) / 2
				script := compileScriptWithConfig(t, Config{StepQuota: mid, MemoryQuotaBytes: Unlimited}, src)
				if _, err := script.Call(context.Background(), "run", []Value{NewString(text)}, CallOptions{}); err != nil {
					lo = mid + 1
				} else {
					hi = mid
				}
			}
			if floor := len(text) / stringScanBytesPerStep; lo < floor {
				t.Errorf("%s text of %d bytes cost %d steps, below the %d its traversal "+
					"deserves at one step per %d bytes; a rune count must not stand in for "+
					"a byte count", name, len(text), lo, floor, stringScanBytesPerStep)
			}
		})
	}
}

// A width writes bytes that neither the pattern nor the arguments account for:
// format("%1000000s", "") produces a megabyte from a few input bytes. The
// per-call output cap bounds one call, not a loop of them, so the projected
// output has to be charged the way padding and repetition are.
func TestFormatChargesForTheOutputItWrites(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{
		"format(\"%%%ds\", \"\").bytesize",
		"format(\"%%-%ds\", \"\").bytesize",
		"(\"%%%ds\" %% [\"\"]).bytesize",
	} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			const small, large = 8 << 10, 64 << 10
			atSmall := minStepsForWidth(t, expr, small)
			atLarge := minStepsForWidth(t, expr, large)
			if atLarge < atSmall*4 {
				t.Errorf("%s cost %d steps at width %d and %d at width %d; a width writes "+
					"bytes the inputs do not bound, so the projected output must be charged",
					expr, atSmall, small, atLarge, large)
			}
		})
	}
}

// Both entrances to a direct-call method must meter the same arguments. The
// fast path metered only the needle while dispatch metered every argument, so
// the two drifted by the size of the offset argument.
//
// The splatted form costs a few steps more for building its own argument array,
// so the assertion is that the gap does not grow with the offset: a metering
// difference scales with it, a construction difference does not.
func TestDirectStringCallsMeterEveryArgument(t *testing.T) {
	t.Parallel()

	hay := NewString(strings.Repeat("ab", (64<<10)/2))
	direct := "def run(s, bad)\n  s.index(\"x\", bad)\nend"
	splat := "def run(s, bad)\n  s.index(*[\"x\", bad])\nend"

	// A string in the offset position is invalid; reaching that error means
	// metering let the call through.
	minFor := func(source string, bad Value) int {
		lo, hi := 1, 1<<20
		for lo < hi {
			mid := (lo + hi) / 2
			script := compileScriptWithConfig(t, Config{StepQuota: mid, MemoryQuotaBytes: Unlimited}, source)
			_, err := script.Call(context.Background(), "run", []Value{hay, bad}, CallOptions{})
			if err != nil && strings.Contains(err.Error(), "quota") {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		return lo
	}

	gapFor := func(size int) int {
		bad := NewString(strings.Repeat("z", size))
		return minFor(splat, bad) - minFor(direct, bad)
	}

	small, large := gapFor(8<<10), gapFor(256<<10)
	if small != large {
		t.Errorf("the splatted form cost %d steps more than the direct one with an 8 KiB "+
			"offset and %d more with 256 KiB; a gap that grows with the argument means "+
			"the two entrances meter different things", small, large)
	}
}

// Every member of the capped set, exercised. Membership asserts that a method
// cannot inspect more of an argument than the receiver holds, and that is a
// claim about its implementation rather than its shape: count parses its
// character set byte by byte, casecmp? validates its argument's UTF-8 before
// comparing, and both looked like comparisons from the outside. Each was capped
// on that resemblance and each under-charged until it was reported.
//
// So the whole set is tested rather than the members someone happened to
// question: a two-byte receiver with a growing argument must cost the same at
// every size.
//
// Note what this does and does not prove. It shows the *charge* does not follow
// the argument, which is the property the cap claims. It cannot show the *work*
// does not, because unmetered work costs no steps by definition -- index and
// rindex passed this test while scanning whole needles through stringIsASCII,
// and only wall-clock revealed it. The other half of the claim rests on each
// implementation rejecting an oversized argument before reading it, which is
// reviewed per method rather than enforced here.
func TestCappedArgumentsAreActuallyBoundedByTheReceiver(t *testing.T) {
	t.Parallel()

	// One call per capped method, each passing the host string where the method
	// takes one.
	capped := map[string]string{
		"start_with?": "\"ab\".start_with?(s).inspect",
		"end_with?":   "\"ab\".end_with?(s).inspect",
		"include?":    "\"ab\".include?(s).inspect",
		"index":       "\"ab\".index(s).inspect",
		"rindex":      "\"ab\".rindex(s).inspect",
		"casecmp":     "\"ab\".casecmp(s).inspect",
		"partition":   "\"ab\".partition(s).length",
		"rpartition":  "\"ab\".rpartition(s).length",
		"slice":       "\"ab\".slice(s).inspect",
		"between?":    "\"ab\".between?(\"aa\", s).inspect",
	}
	names := make([]string, 0, len(capped))
	for name := range capped {
		names = append(names, name)
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			atSmall := minStepsForStringOp(t, capped[name], 8<<10)
			atLarge := minStepsForStringOp(t, capped[name], 512<<10)
			if atSmall != atLarge {
				t.Errorf("%s cost %d steps with an 8 KiB argument and %d with 512 KiB; a "+
					"method is only in the capped set if the receiver bounds what it can "+
					"inspect, so its cost must not follow the argument", capped[name],
					atSmall, atLarge)
			}
		})
	}
}

// casecmp? validates its argument's UTF-8 in full before comparing, so the
// receiver bounds nothing. It resembles casecmp, which stops at the shorter
// operand and stays capped -- the distinction is in the implementation, not the
// name.
func TestCasecmpPredicateChargesItsWholeArgument(t *testing.T) {
	t.Parallel()

	atSmall := minStepsForStringOp(t, "\"ab\".casecmp?(s).inspect", 8<<10)
	atLarge := minStepsForStringOp(t, "\"ab\".casecmp?(s).inspect", 64<<10)
	if atLarge < atSmall*4 {
		t.Errorf("casecmp? cost %d steps over an 8 KiB argument and %d over 64 KiB; it "+
			"scans the whole argument to validate it, so the receiver cannot cap the "+
			"charge", atSmall, atLarge)
	}
}

// A substitution can expand well past its inputs: replacing every byte of a
// fixed receiver with a growing replacement writes the product of the two, so
// neither the receiver nor the replacement bounds the result. The receiver here
// is a literal, so only the replacement grows.
func TestSubstitutionChargesForTheResultItBuilds(t *testing.T) {
	t.Parallel()

	const receiver = "\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\""
	for _, expr := range []string{
		receiver + ".gsub(\"a\", s).bytesize",
		receiver + ".sub(\"a\", s).bytesize",
	} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			atSmall := minStepsForStringOp(t, expr, 8<<10)
			atLarge := minStepsForStringOp(t, expr, 64<<10)
			if atLarge < atSmall*4 {
				t.Errorf("%s cost %d steps with an 8 KiB replacement and %d with 64 KiB; "+
					"a substitution must charge for what it writes, which its inputs do "+
					"not bound", expr, atSmall, atLarge)
			}
		})
	}
}

// A template's payload arrives inside its context hash rather than as a string
// argument, so the per-call charge never saw it: the render copied the whole
// value for the cost of a tiny receiver, and the quota never fired at all.
func TestTemplateChargesForItsRenderedContext(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{
		"\"{{v}}\".template({v: s}).bytesize",
		"\"{{a}}{{b}}\".template({a: s, b: s}).bytesize",
	} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			atSmall := minStepsForStringOp(t, expr, 8<<10)
			atLarge := minStepsForStringOp(t, expr, 64<<10)
			if atLarge < atSmall*4 {
				t.Errorf("%s cost %d steps over an 8 KiB context value and %d over 64 KiB; "+
					"a value reached through a context hash is copied just as a string "+
					"argument is", expr, atSmall, atLarge)
			}
		})
	}
}

// The block form of a substitution expands exactly as the replacement form
// does -- a block returning a large string writes it once per match -- but it
// returned ahead of the output charge, so the quota never fired.
func TestBlockSubstitutionChargesForItsOutput(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{
		"\"aaaa\".gsub(\"a\") { s }.bytesize",
		"\"aaaa\".sub(\"a\") { s }.bytesize",
	} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			atSmall := minStepsForStringOp(t, expr, 8<<10)
			atLarge := minStepsForStringOp(t, expr, 64<<10)
			if atLarge < atSmall*4 {
				t.Errorf("%s cost %d steps with an 8 KiB block result and %d with 64 KiB; "+
					"the block form writes what it returns just as the replacement form "+
					"does", expr, atSmall, atLarge)
			}
		})
	}
}

// Serializing descends into a structure and scans, escapes and copies every
// string it finds. Those strings are not arguments to the call, so the
// per-call charge never saw them: stringifying a hash holding a 512 KiB value
// ran for 1.8 seconds with the quota never firing.
func TestSerializationChargesForNestedStrings(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{
		"JSON.stringify({v: s}).bytesize",
		"JSON.stringify([s]).bytesize",
		"JSON.stringify({a: {b: [s]}}).bytesize",
	} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			atSmall := minStepsForStringOp(t, expr, 8<<10)
			atLarge := minStepsForStringOp(t, expr, 64<<10)
			if atLarge < atSmall*4 {
				t.Errorf("%s cost %d steps over an 8 KiB nested string and %d over 64 KiB; "+
					"a string reached through a structure is scanned and copied like any "+
					"other", expr, atSmall, atLarge)
			}
		})
	}
}

// A block that raises partway through has still copied the replacements it
// already returned. Charging once after the loop skipped those, and the error
// is rescuable, so a script could repeat the copying for nothing. The charge
// lands as each replacement is accepted instead.
func TestBlockSubstitutionChargesReplacementsBeforeARaise(t *testing.T) {
	t.Parallel()

	// Four matches; the block returns the host string for the first three and
	// then raises, so the raise is reached only after three full copies.
	src := "def run(s)\n" +
		"  n = 0\n" +
		"  \"aaaa\".gsub(\"a\") do |m|\n" +
		"    n = n + 1\n" +
		"    if n > 3\n" +
		"      raise \"stop\"\n" +
		"    end\n" +
		"    s\n" +
		"  end\n" +
		"end"

	// The call always raises, so measure the quota at which the raise is
	// reached: below it the copying trips the quota first.
	minToRaise := func(bytes int) int {
		hay := NewString(strings.Repeat("x", bytes))
		lo, hi := 1, 1<<20
		for lo < hi {
			mid := (lo + hi) / 2
			script := compileScriptWithConfig(t, Config{StepQuota: mid, MemoryQuotaBytes: Unlimited}, src)
			_, err := script.Call(context.Background(), "run", []Value{hay}, CallOptions{})
			if err != nil && strings.Contains(err.Error(), "quota") {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		return lo
	}

	atSmall, atLarge := minToRaise(8<<10), minToRaise(64<<10)
	if atLarge < atSmall*4 {
		t.Errorf("reaching the raise cost %d steps with an 8 KiB replacement and %d with "+
			"64 KiB; replacements copied before a later raise must be charged, or the "+
			"error can be rescued and the copying repeated for free", atSmall, atLarge)
	}
}

// Rendering a block result that overflows the output limit copies up to the
// limit before reporting it, and that error is rescuable: a script could return
// an oversized aggregate on every match and pay only the per-match step.
//
// The observable is which error surfaces. Once the render is charged, a quota
// below its cost fails on the quota and only a quota above it reaches the
// output-limit error; uncharged, the limit error arrives however small the
// quota. A rescuing loop cannot pin this, because a bare rescue swallows the
// quota error along with the limit error.
func TestTruncatedBlockRenderChargesWhatItRendered(t *testing.T) {
	t.Parallel()

	// Eight 512 KiB values render well past the 1 MiB output limit, so the
	// render always truncates.
	hay := NewString(strings.Repeat("x", 512<<10))
	src := "def run(s)\n  \"a\".gsub(\"a\") { [s, s, s, s, s, s, s, s] }.bytesize\nend"

	limitErrorAt := func(quota int) bool {
		script := compileScriptWithConfig(t, Config{StepQuota: quota, MemoryQuotaBytes: Unlimited}, src)
		_, err := script.Call(context.Background(), "run", []Value{hay}, CallOptions{})
		return err != nil && strings.Contains(err.Error(), "output exceeds limit")
	}

	// Far below the render's cost: the quota must stop it first.
	if limitErrorAt(64) {
		t.Error("a 64-step quota reached the output-limit error; the bytes rendered before " +
			"the limit was reported were not charged, so a rescued loop could repeat the " +
			"rendering indefinitely")
	}
	// Comfortably above it: the render completes and reports its own limit.
	if !limitErrorAt(maxRegexInputBytes/stringScanBytesPerStep + 10000) {
		t.Error("a quota above the render's charge did not reach the output-limit error; " +
			"the charge must not exceed the bytes actually rendered")
	}
}

// Operators never reach the string member wrapper, so every one that copies or
// scans its operands had to be charged separately. Concatenation copied a whole
// host-supplied string per evaluation and the comparisons scanned one, both for
// the flat evaluator cost -- and discarding the result kept the memory quota out
// of it, so a loop was bounded by nothing at all.
//
// eql? is in here too: it is a universal member rather than a string one, so it
// bypassed the wrapper the same way an operator does.
func TestStringOperatorsChargeForTheirOperands(t *testing.T) {
	t.Parallel()

	// Both operands are the host string, so the cost must follow its size.
	// Index syntax is here for the same reason an operator is: it never reaches
	// member dispatch, and indexing by rune walks the receiver to find the
	// offset.
	ops := []string{
		"s[0].bytesize",
		"s[0, 4].bytesize",
		"(s + s).bytesize",
		"(s == s).to_s.length",
		"(s != s).to_s.length",
		"(s < s).to_s.length",
		"(s > s).to_s.length",
		"(s <=> s).to_s.length",
		"s.eql?(s).to_s.length",
	}
	for _, expr := range ops {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			atSmall := minStepsForStringOp(t, expr, 8<<10)
			atLarge := minStepsForStringOp(t, expr, 64<<10)
			if atLarge < atSmall*4 {
				t.Errorf("%s cost %d steps over 8 KiB operands and %d over 64 KiB; an "+
					"operator copies or scans its operands just as a method does, and it "+
					"never passes through the member wrapper", expr, atSmall, atLarge)
			}
		})
	}
}

// The length rejection that keeps index and rindex from scanning an oversized
// needle must not change what they match. Invalid UTF-8 matches by rune through
// their fallback, where a one-byte invalid sequence and the three-byte
// replacement character are both a single RuneError and do match -- comparing
// bytes alone rejected that pair and silently changed the result.
//
// Nothing covered this pairing, which is why the regression reached review.
func TestOversizedNeedleRejectionPreservesRuneMatching(t *testing.T) {
	t.Parallel()

	// One invalid byte against the replacement character: three bytes against
	// one, but one rune against one.
	if got := stringRuneIndex("\xff", "\uFFFD", 0); got != 0 {
		t.Errorf("index of the replacement character in an invalid byte = %d, want 0; "+
			"the fallback matches by rune, so a needle with more bytes than the haystack "+
			"can still match", got)
	}
	if got := stringRuneRIndex("\xff", "\uFFFD", 1); got != 0 {
		t.Errorf("rindex of the replacement character in an invalid byte = %d, want 0", got)
	}

	// A needle past the widest encoding of the haystack cannot match either way.
	huge := strings.Repeat("x", 4<<20)
	if got := stringRuneIndex("ab", huge, 0); got != -1 {
		t.Errorf("index of a 4 MiB needle in a two-byte haystack = %d, want -1", got)
	}
	if got := stringRuneRIndex("ab", huge, 2); got != -1 {
		t.Errorf("rindex of a 4 MiB needle in a two-byte haystack = %d, want -1", got)
	}
}

// The cap on a method's argument and the guard on what it will read must agree.
// index and rindex admit a needle up to utf8.UTFMax times the receiver, because
// invalid UTF-8 matches by rune and a three-byte replacement character can match
// a one-byte invalid sequence -- then they scan that needle. Billing only the
// receiver's length under-metered exactly the case the guard exists to preserve.
func TestNeedleCapMatchesWhatTheGuardAdmits(t *testing.T) {
	t.Parallel()

	// A needle inside the guard's bound is read, so its bytes must be charged:
	// growing it up to that bound must cost more.
	const receiver = "\"aaaaaaaaaaaaaaaa\"" // 16 bytes, so the guard admits 64
	stepsFor := func(needleBytes int) int {
		needle := strings.Repeat("b", needleBytes)
		src := fmt.Sprintf("def run(s)\n  %s.index(%q).inspect\nend", receiver, needle)
		lo, hi := 1, 10000
		for lo < hi {
			mid := (lo + hi) / 2
			script := compileScriptWithConfig(t, Config{StepQuota: mid, MemoryQuotaBytes: Unlimited}, src)
			if _, err := script.Call(context.Background(), "run", []Value{NewString("")}, CallOptions{}); err != nil {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		return lo
	}

	// 16 bytes against 64: both inside the guard's bound, so both are read and
	// the larger must cost more. A cap of one times the receiver would bill them
	// the same.
	atBound, atReceiver := stepsFor(64), stepsFor(16)
	if atBound <= atReceiver {
		t.Errorf("a 64-byte needle cost %d steps and a 16-byte one %d against a 16-byte "+
			"receiver; the guard admits needles up to four times the receiver and scans "+
			"them, so the charge must follow to the same bound", atBound, atReceiver)
	}

	// Past the guard's bound nothing is read, so the charge stops growing.
	beyond, farBeyond := stepsFor(256), stepsFor(4096)
	if beyond != farBeyond {
		t.Errorf("a 256-byte needle cost %d steps and a 4096-byte one %d; past the guard's "+
			"bound the needle is rejected unread, so the charge must not follow it",
			beyond, farBeyond)
	}
}

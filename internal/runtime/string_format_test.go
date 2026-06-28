package runtime

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRubyStyleStringFormatting(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  [
    "%s:%03d" % ["id", 7],
    format("%.2f", 1.234),
    sprintf("%x", 255),
    format("%[2]s", "skip", "kept"),
    format("%[2]s%[1]s", "a", "b"),
    "%s" % :ok,
    format("%s:%s:%s", 5, true, nil),
    format("%[1]s%[1]d", 5),
    5 % 2
  ]
end

def bad_format
  format()
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewString("id:007"),
		NewString("1.23"),
		NewString("ff"),
		NewString("kept"),
		NewString("ba"),
		NewString("ok"),
		NewString("5:true:"),
		NewString("55"),
		NewInt(1),
	})
	requireCallErrorContains(t, script, "bad_format", nil, CallOptions{}, "format expects a format string")
}

func TestRubyStyleStringFormattingRejectsOversizedOutputBeforeFormatting(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 4096}, `def builtin_format
  format("%1000000s", "")
end

def operator_format
  "%1000000s" % ""
end`)

	requireCallRuntimeErrorType(t, script, "builtin_format", nil, CallOptions{}, runtimeErrorTypeLimit)
	requireCallRuntimeErrorType(t, script, "operator_format", nil, CallOptions{}, runtimeErrorTypeLimit)

	capped := compileScript(t, `def run
  format("%1048577s", "")
end`)
	requireCallErrorContains(t, capped, "run", nil, CallOptions{}, "format width exceeds limit")
}

func TestRubyStyleStringFormattingPreflightsExplicitIndexCursor(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 4 * maxFormatOutputBytes}, `def run(tiny, selected, huge)
  format("%[2]s%s", tiny, selected, huge)
end`)

	requireCallErrorContains(t, script, "run", []Value{
		NewString("tiny"),
		NewString("selected"),
		NewString(strings.Repeat("x", maxFormatOutputBytes)),
	}, CallOptions{}, "format output exceeds limit")
}

func TestRubyStyleStringFormattingPreflightsQuotedEscapes(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 4 * maxFormatOutputBytes}, `def run(text)
  format("%q", text)
end`)
	invalidBytes := strings.Repeat("\xc3", maxFormatOutputBytes/4)

	requireCallErrorContains(t, script, "run", []Value{
		NewString(invalidBytes),
	}, CallOptions{}, "format output exceeds limit")
}

func TestRubyStyleStringFormattingPreflightsHexSpacingFlag(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 4 * maxFormatOutputBytes}, `def run(text)
  format("% x", text)
end`)
	text := strings.Repeat("x", maxFormatOutputBytes/2)

	requireCallErrorContains(t, script, "run", []Value{
		NewString(text),
	}, CallOptions{}, "format output exceeds limit")
}

func TestRubyStyleStringFormattingPreflightsIntegerPrecision(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 4 * maxFormatOutputBytes}, `def run
  format("%.1048576d%.1048576d", 1, 1)
end`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "format output exceeds limit")
}

func TestRubyStyleStringFormattingPreflightsHexFloatPrecision(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 4 * maxFormatOutputBytes}, `def run
  format("%.1048576x%.1048576x", 1.5, 1.5)
end`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "format output exceeds limit")
}

func TestRubyStyleStringFormattingPreflightsFixedPointFloatMagnitude(t *testing.T) {
	t.Parallel()

	count := maxFormatOutputBytes/300 + 1
	values := make([]Value, count)
	for i := range values {
		values[i] = NewFloat(math.MaxFloat64)
	}

	_, err := formatStringValues(strings.Repeat("%f", count), values)
	if err == nil {
		t.Fatal("formatStringValues() succeeded, want output limit error")
	}
	if !strings.Contains(err.Error(), "format output exceeds limit") {
		t.Fatalf("formatStringValues() error = %v, want output limit", err)
	}
}

func TestRubyStyleStringFormattingRejectsUnusedOperandsBeforeFormatting(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 4 * maxFormatOutputBytes}, `def builtin_format(extra)
  format("", extra)
end

def operator_format(extra)
  "" % [extra]
end`)
	extra := NewString(strings.Repeat("x", maxFormatOutputBytes))

	requireCallErrorContains(t, script, "builtin_format", []Value{extra}, CallOptions{}, "unused operand")
	requireCallErrorContains(t, script, "operator_format", []Value{extra}, CallOptions{}, "unused operand")
}

func TestRubyStyleStringFormattingRejectsMissingOperandsBeforeFormatting(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def builtin_format
  format("%s")
end

def operator_format
  "%s %s" % "x"
end`)

	requireCallErrorContains(t, script, "builtin_format", nil, CallOptions{}, "missing operand")
	requireCallErrorContains(t, script, "operator_format", nil, CallOptions{}, "missing operand")
}

func TestRubyStyleStringFormattingRejectsMismatchedNumericOperandsBeforeFormatting(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 4 * maxFormatOutputBytes}, `def run(text)
  format("%d", text)
end`)

	requireCallErrorContains(t, script, "run", []Value{
		NewString(strings.Repeat("x", maxFormatOutputBytes)),
	}, CallOptions{}, "expects integer operand")
}

func TestRubyStyleStringFormattingPreflightsMultibyteStringPrecision(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 16 * maxFormatOutputBytes}, `def run(pattern, text)
  format(pattern, text)
end`)
	precision := maxFormatOutputBytes/utf8.UTFMax + 1
	pattern := NewString(fmt.Sprintf("%%.%ds", precision))
	text := NewString(strings.Repeat("🙂", precision))

	requireCallErrorContains(t, script, "run", []Value{
		pattern,
		text,
	}, CallOptions{}, "format output exceeds limit")
}

func TestRubyStyleStringFormattingProjectsPrecisionByRunes(t *testing.T) {
	t.Parallel()

	pattern := fmt.Sprintf("%%.%ds", maxFormatOutputBytes)
	text := strings.Repeat("x", maxFormatOutputBytes+1)
	got, err := formatStringValues(pattern, []Value{NewString(text)})
	if err != nil {
		t.Fatalf("formatStringValues() error = %v", err)
	}
	want := NewString(strings.Repeat("x", maxFormatOutputBytes))
	if !got.Equal(want) {
		t.Fatalf("formatStringValues() length = %d, want %d", len(got.String()), maxFormatOutputBytes)
	}
}

func TestRubyStyleStringFormattingCapsNormalizedPatternGrowth(t *testing.T) {
	pattern := strings.Repeat("x", 4*maxFormatOutputBytes)

	var err error
	alloc := allocBytes(func() {
		_, err = formatStringValues(pattern, nil)
	})
	if err == nil {
		t.Fatal("formatStringValues() succeeded, want output limit error")
	}
	if !strings.Contains(err.Error(), "format output exceeds limit") {
		t.Fatalf("formatStringValues() error = %v, want output limit", err)
	}
	if alloc > 2*maxFormatOutputBytes {
		t.Fatalf("formatStringValues() allocated %d bytes, want capped normalized buffer growth", alloc)
	}
}

func TestRubyStyleStringFormattingBoundsCompositeStringification(t *testing.T) {
	large := NewArray([]Value{NewString(strings.Repeat("x", maxFormatOutputBytes+64))})

	for _, tc := range []struct {
		pattern string
		want    string
	}{
		{pattern: "%.1s", want: "["},
		{pattern: "%.1q", want: fmt.Sprintf("%.1q", "[")},
	} {
		t.Run(tc.pattern, func(t *testing.T) {
			var got Value
			var err error
			alloc := allocBytes(func() {
				got, err = formatStringValues(tc.pattern, []Value{large})
			})
			if err != nil {
				t.Fatalf("formatStringValues() error = %v", err)
			}
			if !got.Equal(NewString(tc.want)) {
				t.Fatalf("formatStringValues() = %#v, want %q", got, tc.want)
			}
			if alloc > 256*1024 {
				t.Fatalf("formatStringValues() allocated %d bytes, want bounded composite rendering", alloc)
			}
		})
	}
}

func TestRubyStyleStringFormattingCapsCompositeProjectionWalk(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{StepQuota: 5_000, MemoryQuotaBytes: 64 << 20}, `def run
  a = [0]
  24.times { a = [a, a] }
  format("%.1s", a)
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if !got.Equal(NewString("[")) {
		t.Fatalf("run = %#v, want '['", got)
	}
}

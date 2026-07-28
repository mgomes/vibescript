package runtime

import (
	"context"
	"strings"
	"testing"
)

const crossingTimeSetup = `t = Time.parse("2026-07-27T14:30:45Z")` + "\n  "

func runCrossingExpr(t *testing.T, expr string) (Value, error) {
	t.Helper()
	script := compileScript(t, "def run()\n  "+crossingTimeSetup+expr+"\nend")
	return script.Call(context.Background(), "run", nil, CallOptions{})
}

// Time#strftime given a Go layout used to return the layout as data. That is
// the dangerous direction, because the Go reference layout is itself a date:
// "2006-01-02" survives a visual check, a String check, an ISO-8601 regex, and
// a length check, so a report could emit the same date on every row.
func TestStrftimeRejectsGoLayout(t *testing.T) {
	t.Parallel()

	for _, layout := range []string{
		"2006-01-02",
		"2006-01-02 15:04:05",
		"15:04",
		"Jan 2, 2006",
		"02/01/2006",
		"Mon Jan _2 15:04:05 2006",
	} {
		t.Run(layout, func(t *testing.T) {
			t.Parallel()
			_, err := runCrossingExpr(t, `t.strftime("`+layout+`")`)
			if err == nil {
				t.Fatalf("strftime(%q) was accepted, want it reported as a Go layout", layout)
			}
			if !strings.Contains(err.Error(), "is a Go layout") || !strings.Contains(err.Error(), "use format") {
				t.Fatalf("error = %v, want it to name the layout language and point at format", err)
			}
		})
	}
}

// Time#format given a percent format returned the format as data too. Less
// harmful, since "%Y-%m-%d" is obviously not a date, but still a wrong value
// rather than an error.
func TestFormatRejectsStrftimeFormat(t *testing.T) {
	t.Parallel()

	for _, format := range []string{"%Y-%m-%d", "%H:%M:%S", "%Y-%m-%dT%H:%M:%S"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			_, err := runCrossingExpr(t, `t.format("`+format+`")`)
			if err == nil {
				t.Fatalf("format(%q) was accepted, want it reported as a strftime format", format)
			}
			if !strings.Contains(err.Error(), "is a strftime format") || !strings.Contains(err.Error(), "use strftime") {
				t.Fatalf("error = %v, want it to name the format language and point at strftime", err)
			}
		})
	}
}

// Each method keeps working with its own format language.
func TestCorrectFormatLanguagesStillWork(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "format with a Go layout", expr: `t.format("2006-01-02 15:04")`, want: "2026-07-27 14:30"},
		{name: "strftime with percent directives", expr: `t.strftime("%Y-%m-%d %H:%M")`, want: "2026-07-27 14:30"},
		{name: "strftime mixing text and directives", expr: `t.strftime("Day %d of %B")`, want: "Day 27 of July"},
		{name: "format with a bare date layout", expr: `t.format("2006-01-02")`, want: "2026-07-27"},
		{name: "strftime with a literal percent", expr: `t.strftime("100%%")`, want: "100%"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := runCrossingExpr(t, tc.expr)
			if err != nil {
				t.Fatalf("%s: %v", tc.expr, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %q, want %q", tc.expr, got.String(), tc.want)
			}
		})
	}
}

// The check must not fire on ordinary literal text. Go reads a bare digit as a
// reference component, so a looser test would report "Section 3" as a Go
// layout and advise switching to format, which would be wrong.
func TestStrftimeAllowsLiteralTextWithDigits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		format string
		want   string
	}{
		{format: "Section 3", want: "Section 3"},
		{format: "Q1", want: "Q1"},
		{format: "Top 5 items", want: "Top 5 items"},
		{format: "2026 Report", want: "2026 Report"},
		{format: "hello", want: "hello"},
		{format: "", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.format, func(t *testing.T) {
			t.Parallel()
			got, err := runCrossingExpr(t, `t.strftime("`+tc.format+`")`)
			if err != nil {
				t.Fatalf("strftime(%q) was rejected: %v", tc.format, err)
			}
			if got.String() != tc.want {
				t.Fatalf("strftime(%q) = %q, want %q", tc.format, got.String(), tc.want)
			}
		})
	}
}

// Time#format has an optimized direct-call path alongside the member-dispatch
// one. Both must reject a crossed format, or the check is reachable only
// sometimes.
func TestFormatCrossingCaughtOnEveryDispatchPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
	}{
		{name: "direct call", expr: `t.format("%Y-%m-%d")`},
		{name: "through a variable holding the layout", expr: `layout = "%Y-%m-%d"` + "\n  " + `t.format(layout)`},
		{name: "inside a block", expr: `[1].map { |i| t.format("%Y-%m-%d") }`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := runCrossingExpr(t, tc.expr); err == nil {
				t.Fatalf("%s accepted a strftime format, want it rejected", tc.name)
			}
		})
	}
}

// Detection scans for a recognized directive instead of rendering the
// candidate. Rendering honors a directive's requested width, so classifying a
// format was itself an unbounded allocation -- `%1000000000N` allocated about a
// gigabyte purely to decide, which with no memory limit set exhausts the
// process where Time#format previously saw a tiny literal.
//
// The scanner is what makes that avoidable, so it is tested directly: the
// allocation itself is not observable as a pass or fail without a memory
// limit, and a timing assertion would be a race.
func TestStrftimeDirectiveScannerRecognizesWithoutRendering(t *testing.T) {
	t.Parallel()

	tests := []struct {
		format string
		want   bool
	}{
		{"%Y-%m-%d", true},
		{"%H:%M:%S", true},
		// A width does not need rendering to recognize.
		{"%1000000000N", true},
		{"%6N", true},
		{"%:z", true},
		// A literal percent is not a directive.
		{"100%% done", false},
		{"2006-01-02", false},
		{"plain text", false},
		{"", false},
		// An unknown letter after a percent is emitted verbatim, as in Ruby.
		{"%Q", false},
	}

	for _, tc := range tests {
		t.Run(tc.format, func(t *testing.T) {
			t.Parallel()
			if got := containsRecognizedStrftimeDirective(tc.format); got != tc.want {
				t.Fatalf("containsRecognizedStrftimeDirective(%q) = %v, want %v", tc.format, got, tc.want)
			}
		})
	}
}

// A wide directive is still classified as a crossed format, without rendering.
func TestWideDirectiveIsStillReportedAsCrossed(t *testing.T) {
	t.Parallel()
	if _, err := runCrossingExpr(t, `t.format("%1000000000N")`); err == nil {
		t.Fatalf("expected a wide strftime directive to be reported as a crossed format")
	}
}

// A literal percent that is not a directive must not make a Go layout look
// like a strftime format.
func TestGoLayoutWithLiteralPercentIsNotCrossed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want string
	}{
		{`t.format("100% done")`, "700% done"},
		{`t.strftime("100%% done")`, "100% done"},
		{`t.format("2006-01-02")`, "2026-07-27"},
	}

	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			got, err := runCrossingExpr(t, tc.expr)
			if err != nil {
				t.Fatalf("%s: %v", tc.expr, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %q, want %q", tc.expr, got.String(), tc.want)
			}
		})
	}
}

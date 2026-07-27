package runtime

import (
	"context"
	"strings"
	"testing"
)

const formatToStringClasses = `
class P
  def initialize(n)
    @n = n
  end
  def to_s
    "P<#{@n}>"
  end
end
class Plain
  def initialize()
    @x = 1
  end
end
`

// %s is defined as the to_s form, but format was the last direct string
// conversion that did not consult a class's to_s: interpolation and puts were
// connected in #1055 and this was left out, so one conversion still disagreed
// with the other two.
func TestFormatUsesUserDefinedToString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "bare conversion", expr: `format("%s", P.new(5))`, want: "P<5>"},
		{name: "surrounded by text", expr: `format("a %s b", P.new(1))`, want: "a P<1> b"},
		{name: "several arguments", expr: `format("%s and %s", P.new(1), P.new(2))`, want: "P<1> and P<2>"},
		// Width and padding are computed from the substituted value, which is
		// what proves the projection and render passes agree.
		{name: "left padded", expr: `format("%-8s|", P.new(3))`, want: "P<3>    |"},
		{name: "right padded", expr: `format("%8s|", P.new(3))`, want: "    P<3>|"},
		// A class without to_s keeps the placeholder.
		{name: "no to_s defined", expr: `format("%s", Plain.new())`, want: "<Plain instance>"},
		// Ordinary values are untouched.
		{name: "string argument", expr: `format("%s", "plain")`, want: "plain"},
		{name: "numeric verb", expr: `format("%d", 42)`, want: "42"},
		{name: "mixed kinds", expr: `format("%s=%d", P.new(1), 7)`, want: "P<1>=7"},
		// The point is that the three conversions now agree.
		{name: "agrees with interpolation", expr: `(format("%s", P.new(9)) == "#{P.new(9)}").to_s`, want: "true"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, formatToStringClasses+"\ndef run()\n  "+tc.expr+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.expr, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %q, want %q", tc.expr, got.String(), tc.want)
			}
		})
	}
}

// A raise inside to_s surfaces at the format call rather than being swallowed.
func TestFormatPropagatesToStringError(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    class Boom
      def to_s
        raise "to_s exploded"
      end
    end
    def run()
      format("%s", Boom.new())
    end
    `)
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected the raise inside to_s to surface")
	}
	if !strings.Contains(err.Error(), "to_s exploded") {
		t.Fatalf("error = %v, want it to name the raise", err)
	}
}

// The substituted string is what the projection charges, so an oversized to_s
// is still stopped by the memory quota.
func TestFormatToStringRespectsMemoryQuota(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 10_000_000, MemoryQuotaBytes: 128 * 1024}, `
    class Huge
      def to_s
        "x" * 400000
      end
    end
    def run()
      format("%s", Huge.new())
    end
    `)
	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
		t.Fatalf("expected the memory quota to stop an oversized to_s rendering")
	}
}

package runtime

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

const instanceToStringClasses = `
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
class Arity
  def to_s(base)
    "never"
  end
end
class Optional
  def to_s(base = 10)
    "opt#{base}"
  end
end
class NonString
  def to_s
    42
  end
end
class Priv
  def initialize()
    @z = 1
  end
  private
  def to_s
    "private-form"
  end
end
`

// Interpolation rendered <Class instance> even when the class defined to_s,
// so the documented agreement between interpolation and to_s did not hold and
// every log line built from a domain object silently lost its content.
func TestInterpolationCallsUserDefinedToString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "class with to_s", expr: `"#{P.new(5)}"`, want: "P<5>"},
		{name: "agrees with explicit call", expr: `P.new(5).to_s`, want: "P<5>"},
		{name: "repeated interpolation", expr: `"#{P.new(1)}#{P.new(2)}"`, want: "P<1>P<2>"},
		{name: "surrounded by text", expr: `"a#{P.new(7)}b"`, want: "aP<7>b"},
		// A to_s with an optional parameter is still callable with none.
		{name: "optional parameter", expr: `"#{Optional.new()}"`, want: "opt10"},
		// Ruby calls a private to_s from interpolation; a class that made it
		// private still meant it as the string form.
		{name: "private to_s", expr: `"#{Priv.new()}"`, want: "private-form"},
		// No to_s: the placeholder is still correct.
		{name: "no to_s defined", expr: `"#{Plain.new()}"`, want: "<Plain instance>"},
		// A to_s that cannot be called with zero arguments is not a
		// conversion method, so dispatching to it would raise where the
		// program used to render.
		{name: "to_s requiring an argument", expr: `"#{Arity.new()}"`, want: "<Arity instance>"},
		// Ruby falls back to the default rendering when to_s yields a
		// non-string rather than raising.
		{name: "to_s returning a non-string", expr: `"#{NonString.new()}"`, want: "<NonString instance>"},
		// Containers render their elements themselves, as in Ruby, where
		// "#{[p]}" shows the element's inspect form and not its to_s.
		{name: "instance nested in an array", expr: `"#{[P.new(5)]}"`, want: "[<P instance>]"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, instanceToStringClasses+"\ndef run()\n  "+tc.expr+"\nend")
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

// puts and print render the string form, so they follow to_s as interpolation
// does.
func TestOutputBuiltinsCallUserDefinedToString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "puts", body: `puts P.new(5)`, want: "P<5>\n"},
		{name: "print", body: `print(P.new(5))`, want: "P<5>"},
		{name: "puts without to_s", body: `puts Plain.new()`, want: "<Plain instance>\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			script := compileScriptWithConfig(t, Config{OutputWriter: &stdout},
				instanceToStringClasses+"\ndef run()\n  "+tc.body+"\nend")
			if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err != nil {
				t.Fatalf("%s: %v", tc.body, err)
			}
			if stdout.String() != tc.want {
				t.Fatalf("%s wrote %q, want %q", tc.body, stdout.String(), tc.want)
			}
		})
	}
}

// inspect is a separate rendering and must not follow to_s.
func TestInspectIgnoresUserDefinedToString(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	script := compileScriptWithConfig(t, Config{OutputWriter: &stdout},
		instanceToStringClasses+"\ndef run()\n  p(P.new(5))\nend")
	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err != nil {
		t.Fatalf("p: %v", err)
	}
	if strings.Contains(stdout.String(), "P<5>") {
		t.Fatalf("inspect rendered the to_s form: %q", stdout.String())
	}
}

// An error raised inside to_s propagates to the interpolation site instead of
// being swallowed into a placeholder.
func TestInterpolationPropagatesToStringError(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    class Boom
      def to_s
        raise "to_s exploded"
      end
    end
    def run()
      "#{Boom.new()}"
    end
    `)
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected the raise inside to_s to surface")
	}
	if !strings.Contains(err.Error(), "to_s exploded") {
		t.Fatalf("error = %v, want it to name the raise inside to_s", err)
	}
}

// A to_s that interpolates its own receiver recurses; the sandbox recursion
// limit must stop it rather than the host stack.
func TestSelfInterpolatingToStringHitsRecursionLimit(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    class Rec
      def to_s
        "#{self}"
      end
    end
    def run()
      "#{Rec.new()}"
    end
    `)
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected self-interpolating to_s to hit the recursion limit")
	}
	if !strings.Contains(err.Error(), "recursion depth") {
		t.Fatalf("error = %v, want the recursion depth limit", err)
	}
}

// The body of to_s runs user code, so it is charged against the step quota
// like any other call.
func TestInterpolatedToStringChargesStepQuota(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 200, MemoryQuotaBytes: 8 << 20}, `
    class Spin
      def to_s
        total = 0
        (1..500).each { |i| total = total + i }
        "#{total}"
      end
    end
    def run()
      "#{Spin.new()}"
    end
    `)
	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
		t.Fatalf("expected the step quota to stop a looping to_s")
	}
}

// An implicit to_s is still a call boundary. callOperatorFunction normalizes
// an escaping break or next but not retry, so a to_s running retry inside a
// rescue handler restarted the caller's rescue -- while an explicit obj.to_s
// in the same position reported, so the two disagreed.
func TestImplicitToStringStopsRetryAtTheBoundary(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    class R
      def to_s
        retry
      end
    end
    def run()
      count = 0
      begin
        count = count + 1
        raise "trigger"
      rescue => e
        "#{R.new()}"
      end
    end
    `)
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("retry inside an implicit to_s restarted the caller's rescue")
	}
	if !strings.Contains(err.Error(), "retry cannot cross call boundary") {
		t.Fatalf("error = %v, want the call-boundary message an explicit to_s produces", err)
	}
}

// break and next were already normalized and must stay so.
func TestImplicitToStringStopsLoopControlAtTheBoundary(t *testing.T) {
	t.Parallel()

	for _, control := range []string{"break", "next"} {
		t.Run(control, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, `
            class R
              def to_s
                `+control+`
              end
            end
            def run()
              for i in [1]
                "#{R.new()}"
              end
            end
            `)
			_, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err == nil {
				t.Fatalf("%s inside an implicit to_s escaped the boundary", control)
			}
			if !strings.Contains(err.Error(), "cannot cross call boundary") {
				t.Fatalf("%s error = %v, want the call-boundary message", control, err)
			}
		})
	}
}

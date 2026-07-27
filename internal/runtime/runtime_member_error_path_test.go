package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The member tables for string, array, hash, money, and the temporal kinds are
// pure functions that reported with fmt.Errorf. Such an error is not a
// *RuntimeError, which cost two things at once: no source position, so the
// report fell back to the start of the script, and no rescue, because a bare
// rescue requires errors.As(err, &RuntimeError). A misspelled method -- the
// most common authoring mistake -- was both mislocated and uncatchable.
func TestUnknownMemberErrorsCarryTheCallPosition(t *testing.T) {
	t.Parallel()

	// Each expression sits on line 4 so a fallback to the script start is
	// obvious rather than coincidentally correct.
	exprs := []string{
		`"x".nope`,
		`[1].nope`,
		`{a: 1}.nope`,
		`money("1.00 USD").nope`,
		`5.minutes.nope`,
		`(5).nope`,
		`(1..2).nope`,
	}

	for _, expr := range exprs {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  a = 1\n  b = 2\n  "+expr+"\nend")
			_, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err == nil {
				t.Fatalf("%s: expected an error", expr)
			}
			var runtimeErr *RuntimeError
			if !errors.As(err, &runtimeErr) {
				t.Fatalf("%s: error is not a *RuntimeError, so rescue cannot catch it: %v", expr, err)
			}
			if !strings.Contains(runtimeErr.CodeFrame, "line 4") {
				t.Fatalf("%s: expected the report to point at line 4, got:\n%s", expr, runtimeErr.CodeFrame)
			}
		})
	}
}

// The same errors must be catchable, which is the other half of routing them
// through the script error path.
func TestUnknownMemberErrorsAreRescuable(t *testing.T) {
	t.Parallel()

	exprs := []string{
		`"x".nope`,
		`[1].nope`,
		`{a: 1}.nope`,
		`money("1.00 USD").nope`,
		`5.minutes.nope`,
	}

	for _, expr := range exprs {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, `
    def run()
      begin
        `+expr+`
        "not reached"
      rescue => e
        "rescued"
      end
    end
    `)
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: expected the error to be rescued, got %v", expr, err)
			}
			if got.String() != "rescued" {
				t.Fatalf("%s: got %q, want \"rescued\"", expr, got.String())
			}
		})
	}
}

// Sandbox limits must stay outside rescue's reach. A script that could catch
// its own quota exhaustion would defeat the sandbox, so the positioning change
// has to preserve the limit classification rather than turning every error into
// an ordinary rescuable one.
func TestSandboxLimitsRemainUnrescuable(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{StepQuota: 50_000, MemoryQuotaBytes: 8 << 20}, `
    def run()
      begin
        i = 0
        while true
          i = i + 1
        end
      rescue => e
        "wrongly rescued"
      end
    end
    `)
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected the step quota to stop the script")
	}
	if !strings.Contains(err.Error(), "step quota exceeded") {
		t.Fatalf("expected a step quota error, got %v", err)
	}
}

package analyze

import (
	"testing"

	"github.com/mgomes/vibescript/internal/runtime"
)

func TestScriptReportsUnreachableInsideIfStatementExpression(t *testing.T) {
	t.Parallel()

	engine := runtime.MustNewEngine(runtime.Config{})
	script, err := engine.Compile(`
def run(flag)
  if flag
    return 1
    "unreachable"
  else
    2
  end.to_s
  3
end
`)
	if err != nil {
		t.Fatalf("Compile() error = %v, want nil", err)
	}

	warnings := Script(script)
	if len(warnings) != 1 {
		t.Fatalf("Script() warnings = %#v, want one unreachable statement warning", warnings)
	}
	if got, want := warnings[0].Function, "run"; got != want {
		t.Fatalf("Script() warning function = %q, want %q", got, want)
	}
	if got, want := warnings[0].Message, "unreachable statement"; got != want {
		t.Fatalf("Script() warning message = %q, want %q", got, want)
	}
}

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

func TestScriptReportsUnreachableAfterBreakNextAndRetry(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
	}{
		{
			name: "break",
			source: `
def run()
  while true
    break
    1
  end
end
`,
		},
		{
			name: "next",
			source: `
def run()
  for x in [1, 2]
    next
    x
  end
end
`,
		},
		{
			name: "retry",
			source: `
def run()
  begin
    1
  rescue
    retry
    2
  end
end
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			engine := runtime.MustNewEngine(runtime.Config{})
			script, err := engine.Compile(tc.source)
			if err != nil {
				t.Fatalf("Compile() error = %v, want nil", err)
			}

			warnings := Script(script)
			if len(warnings) != 1 {
				t.Fatalf("Script() warnings = %#v, want one unreachable statement warning", warnings)
			}
			if got, want := warnings[0].Message, "unreachable statement"; got != want {
				t.Fatalf("Script() warning message = %q, want %q", got, want)
			}
		})
	}
}

package runtime

import (
	"context"
	"testing"
)

func TestRescueModifierExpressionSemantics(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def assignment_fallback
  x = 1 / 0 rescue "fallback"
  x
end

def success_path
  10 rescue fail_if_called()
end

def fail_if_called
  raise "fallback called"
end

def nested_expression
  (1 / 0 rescue 2) + 3
end
`)

	if got := callScript(t, context.Background(), script, "assignment_fallback", nil, CallOptions{}); !got.Equal(NewString("fallback")) {
		t.Fatalf("assignment_fallback() = %s, want fallback", got)
	}
	if got := callScript(t, context.Background(), script, "success_path", nil, CallOptions{}); !got.Equal(NewInt(10)) {
		t.Fatalf("success_path() = %s, want 10", got)
	}
	if got := callScript(t, context.Background(), script, "nested_expression", nil, CallOptions{}); !got.Equal(NewInt(5)) {
		t.Fatalf("nested_expression() = %s, want 5", got)
	}
}

func TestRescueModifierFallbackCanReraiseCurrentError(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def reraise_current
  raise
end

def run
  1 / 0 rescue reraise_current()
end
`)

	requireCallRuntimeErrorType(t, script, "run", nil, CallOptions{}, runtimeErrorTypeZeroDiv)
}

func TestRescueModifierDoesNotRescueLimitErrors(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{StepQuota: 30}, `
def spin
  while true
  end
end

def run
  spin() rescue "fallback"
end
`)

	requireCallRuntimeErrorType(t, script, "run", nil, CallOptions{}, runtimeErrorTypeLimit)
}

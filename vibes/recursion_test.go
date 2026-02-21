package vibes

import (
	"context"
	"errors"
	"testing"
)

func TestRecursionLimitExceeded(t *testing.T) {
	script := compileScriptWithConfig(t, Config{RecursionLimit: 3}, `def recurse(n)
  if n <= 0
    "done"
  else
    recurse(n - 1)
  end
end`)

	err := callScriptErr(t, context.Background(), script, "recurse", []Value{NewInt(5)}, CallOptions{})

	var re *RuntimeError
	if !errors.As(err, &re) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}
	requireErrorContains(t, err, "recursion depth exceeded (limit 3)")
}

func TestRecursionLimitAllowsWithinBound(t *testing.T) {
	script := compileScriptWithConfig(t, Config{RecursionLimit: 5}, `def recurse(n)
  if n <= 0
    0
  else
    recurse(n - 1) + 1
  end
end`)

	result := callScript(t, context.Background(), script, "recurse", []Value{NewInt(4)}, CallOptions{})
	if result.Kind() != KindInt || result.Int() != 4 {
		t.Fatalf("unexpected result: %v", result)
	}
}

func TestRecursionLimitDefaultApplies(t *testing.T) {
	script := compileScriptDefault(t, `def recurse(n)
  if n <= 0
    "done"
  else
    recurse(n - 1)
  end
end`)

	err := callScriptErr(t, context.Background(), script, "recurse", []Value{NewInt(100)}, CallOptions{})
	requireErrorContains(t, err, "recursion depth exceeded")
}

func TestMutualRecursionRespectsLimit(t *testing.T) {
	script := compileScriptWithConfig(t, Config{RecursionLimit: 4}, `def a(n)
  if n <= 0
    "done"
  else
    b(n - 1)
  end
end

def b(n)
  if n <= 0
    "done"
  else
    a(n - 1)
  end
end`)

	err := callScriptErr(t, context.Background(), script, "a", []Value{NewInt(10)}, CallOptions{})
	requireErrorContains(t, err, "recursion depth exceeded")
}

func TestRecursionLimitWinsOverStepQuota(t *testing.T) {
	script := compileScriptWithConfig(t, Config{RecursionLimit: 3, StepQuota: 1_000_000}, `def spin(n)
  if n <= 0
    0
  else
    1 + spin(n - 1)
  end
end`)

	err := callScriptErr(t, context.Background(), script, "spin", []Value{NewInt(50)}, CallOptions{})
	requireErrorContains(t, err, "recursion depth exceeded (limit 3)")
}

func TestRecursionLimitNoLeakAfterError(t *testing.T) {
	script := compileScriptWithConfig(t, Config{RecursionLimit: 4}, `def ping(n)
  if n <= 0
    "ok"
  else
    ping(n - 1)
  end
end`)

	// First call exceeds the limit.
	_, _ = script.Call(context.Background(), "ping", []Value{NewInt(10)}, CallOptions{})

	// Second call within the limit should still succeed.
	result := callScript(t, context.Background(), script, "ping", []Value{NewInt(3)}, CallOptions{})
	if result.Kind() != KindString || result.String() != "ok" {
		t.Fatalf("unexpected result: %v", result)
	}
}

func TestRecursionLimitWithWhileLoopFrames(t *testing.T) {
	script := compileScriptWithConfig(t, Config{RecursionLimit: 4, StepQuota: 1_000_000}, `def recurse(n)
  while n > 0
    n = n - 1
  end
  recurse(1)
end`)

	err := callScriptErr(t, context.Background(), script, "recurse", []Value{NewInt(3)}, CallOptions{})
	requireErrorContains(t, err, "recursion depth exceeded (limit 4)")
}

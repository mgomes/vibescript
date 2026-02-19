package vibes

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRecursionLimitExceeded(t *testing.T) {
	engine := MustNewEngine(Config{
		RecursionLimit: 3,
	})

	script, err := engine.Compile(`def recurse(n)
  if n <= 0
    "done"
  else
    recurse(n - 1)
  end
end`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	_, err = script.Call(context.Background(), "recurse", []Value{NewInt(5)}, CallOptions{})
	if err == nil {
		t.Fatalf("expected recursion depth error")
	}

	var re *RuntimeError
	if !errors.As(err, &re) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}
	if !strings.Contains(err.Error(), "recursion depth exceeded (limit 3)") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRecursionLimitAllowsWithinBound(t *testing.T) {
	engine := MustNewEngine(Config{
		RecursionLimit: 5,
	})

	script, err := engine.Compile(`def recurse(n)
  if n <= 0
    0
  else
    recurse(n - 1) + 1
  end
end`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	result, err := script.Call(context.Background(), "recurse", []Value{NewInt(4)}, CallOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 4 {
		t.Fatalf("unexpected result: %v", result)
	}
}

func TestRecursionLimitDefaultApplies(t *testing.T) {
	engine := MustNewEngine(Config{})

	script, err := engine.Compile(`def recurse(n)
  if n <= 0
    "done"
  else
    recurse(n - 1)
  end
end`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	_, err = script.Call(context.Background(), "recurse", []Value{NewInt(100)}, CallOptions{})
	if err == nil {
		t.Fatalf("expected recursion depth error at default limit")
	}
	if !strings.Contains(err.Error(), "recursion depth exceeded") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMutualRecursionRespectsLimit(t *testing.T) {
	engine := MustNewEngine(Config{RecursionLimit: 4})

	script, err := engine.Compile(`def a(n)
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
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	_, err = script.Call(context.Background(), "a", []Value{NewInt(10)}, CallOptions{})
	if err == nil {
		t.Fatalf("expected recursion depth error")
	}
	if !strings.Contains(err.Error(), "recursion depth exceeded") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRecursionLimitWinsOverStepQuota(t *testing.T) {
	engine := MustNewEngine(Config{
		RecursionLimit: 3,
		StepQuota:      1_000_000,
	})

	script, err := engine.Compile(`def spin(n)
  if n <= 0
    0
  else
    1 + spin(n - 1)
  end
end`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	_, err = script.Call(context.Background(), "spin", []Value{NewInt(50)}, CallOptions{})
	if err == nil {
		t.Fatalf("expected recursion depth error")
	}
	if !strings.Contains(err.Error(), "recursion depth exceeded (limit 3)") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRecursionLimitNoLeakAfterError(t *testing.T) {
	engine := MustNewEngine(Config{RecursionLimit: 4})

	script, err := engine.Compile(`def ping(n)
  if n <= 0
    "ok"
  else
    ping(n - 1)
  end
end`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	// First call exceeds the limit.
	_, _ = script.Call(context.Background(), "ping", []Value{NewInt(10)}, CallOptions{})

	// Second call within the limit should still succeed.
	result, err := script.Call(context.Background(), "ping", []Value{NewInt(3)}, CallOptions{})
	if err != nil {
		t.Fatalf("unexpected error on second call: %v", err)
	}
	if result.Kind() != KindString || result.String() != "ok" {
		t.Fatalf("unexpected result: %v", result)
	}
}

func TestRecursionLimitWithWhileLoopFrames(t *testing.T) {
	engine := MustNewEngine(Config{
		RecursionLimit: 4,
		StepQuota:      1_000_000,
	})

	script, err := engine.Compile(`def recurse(n)
  while n > 0
    n = n - 1
  end
  recurse(1)
end`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	_, err = script.Call(context.Background(), "recurse", []Value{NewInt(3)}, CallOptions{})
	if err == nil {
		t.Fatalf("expected recursion depth error")
	}
	if !strings.Contains(err.Error(), "recursion depth exceeded (limit 4)") {
		t.Fatalf("unexpected error: %v", err)
	}
}

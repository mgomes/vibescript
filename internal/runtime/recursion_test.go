package runtime

import (
	"context"
	"errors"
	"testing"
)

func TestRecursionLimit(t *testing.T) {
	t.Parallel()

	const simpleRecurse = `def recurse(n)
  if n <= 0
    "done"
  else
    recurse(n - 1)
  end
end`

	const sumRecurse = `def recurse(n)
  if n <= 0
    0
  else
    recurse(n - 1) + 1
  end
end`

	const mutualRecurse = `def a(n)
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
end`

	const spinRecurse = `def spin(n)
  if n <= 0
    0
  else
    1 + spin(n - 1)
  end
end`

	const whileFrameRecurse = `def recurse(n)
  while n > 0
    n = n - 1
  end
  recurse(1)
end`

	tests := []struct {
		name    string
		cfg     Config
		source  string
		fn      string
		args    []Value
		wantErr string // empty means success
		wantVal Value  // checked when wantErr is empty
	}{
		{
			name:    "exceeds_limit",
			cfg:     Config{RecursionLimit: 3},
			source:  simpleRecurse,
			fn:      "recurse",
			args:    []Value{NewInt(5)},
			wantErr: "recursion depth exceeded (limit 3)",
		},
		{
			name:    "within_bound_succeeds",
			cfg:     Config{RecursionLimit: 5},
			source:  sumRecurse,
			fn:      "recurse",
			args:    []Value{NewInt(4)},
			wantVal: NewInt(4),
		},
		{
			name:    "default_limit_applies",
			cfg:     Config{},
			source:  simpleRecurse,
			fn:      "recurse",
			args:    []Value{NewInt(100)},
			wantErr: "recursion depth exceeded",
		},
		{
			name:    "mutual_recursion_respects_limit",
			cfg:     Config{RecursionLimit: 4},
			source:  mutualRecurse,
			fn:      "a",
			args:    []Value{NewInt(10)},
			wantErr: "recursion depth exceeded",
		},
		{
			name:    "wins_over_step_quota",
			cfg:     Config{RecursionLimit: 3, StepQuota: 1_000_000},
			source:  spinRecurse,
			fn:      "spin",
			args:    []Value{NewInt(50)},
			wantErr: "recursion depth exceeded (limit 3)",
		},
		{
			name:    "while_loop_frames_counted",
			cfg:     Config{RecursionLimit: 4, StepQuota: 1_000_000},
			source:  whileFrameRecurse,
			fn:      "recurse",
			args:    []Value{NewInt(3)},
			wantErr: "recursion depth exceeded (limit 4)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithConfig(t, tc.cfg, tc.source)
			if tc.wantErr != "" {
				err := callScriptErr(t, context.Background(), script, tc.fn, tc.args, CallOptions{})
				var re *RuntimeError
				if !errors.As(err, &re) {
					t.Fatalf("expected RuntimeError, got %T", err)
				}
				requireErrorContains(t, err, tc.wantErr)
				return
			}
			result := callScript(t, context.Background(), script, tc.fn, tc.args, CallOptions{})
			if result.Kind() != tc.wantVal.Kind() {
				t.Fatalf("unexpected result kind: got %v, want %v", result.Kind(), tc.wantVal.Kind())
			}
			if result.Kind() == KindInt && result.Int() != tc.wantVal.Int() {
				t.Fatalf("unexpected result: got %v, want %v", result.Int(), tc.wantVal.Int())
			}
		})
	}
}

func TestRecursionLimitNoLeakAfterError(t *testing.T) {
	t.Parallel()

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

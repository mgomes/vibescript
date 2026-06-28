package runtime

import (
	"context"
	"errors"
	"testing"
)

func TestKernelLoopBreakAndNext(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def break_value
  x = 0
  value = loop do
    x = x + 1
    if x == 3
      break :done
    end
  end
  [x, value]
end

def next_skips_iteration
  x = 0
  out = []
  loop do
    x = x + 1
    if x == 2
      next
    end
    out = out.push(x)
    if x == 4
      break out
    end
  end
end

def blockless
  loop()
end`)

	got := callScript(t, context.Background(), script, "break_value", nil, CallOptions{})
	compareArrays(t, got, []Value{NewInt(3), NewSymbol("done")})

	got = callScript(t, context.Background(), script, "next_skips_iteration", nil, CallOptions{})
	compareArrays(t, got, []Value{NewInt(1), NewInt(3), NewInt(4)})

	requireCallErrorContains(t, script, "blockless", nil, CallOptions{}, "loop requires a block")
}

func TestKernelLoopBraceBlockBareBreak(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  loop { break }
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindNil {
		t.Fatalf("run = %#v, want nil", got)
	}
}

func TestKernelLoopStepQuotaAndCancellation(t *testing.T) {
	t.Parallel()

	spinScript := compileScriptWithConfig(t, Config{StepQuota: 40}, `def run
  loop do
  end
end`)
	requireCallRuntimeErrorType(t, spinScript, "run", nil, CallOptions{}, runtimeErrorTypeLimit)

	var cancel context.CancelFunc
	engine := MustNewEngine(Config{StepQuota: 10_000_000})
	engine.builtins["cancel_now"] = NewBuiltin("cancel_now", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		cancel()
		return NewNil(), nil
	})
	cancelScript := compileScriptWithEngine(t, engine, `def run
  loop do
    cancel_now()
  end
end`)
	ctx, cancelFunc := context.WithCancel(context.Background())
	cancel = cancelFunc
	_, err := cancelScript.Call(ctx, "run", nil, CallOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("loop after cancellation error = %v, want context.Canceled", err)
	}
}

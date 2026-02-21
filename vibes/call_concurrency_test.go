package vibes

import (
	"context"
	"testing"
	"time"
)

type blockingSync struct {
	entered chan struct{}
	release chan struct{}
}

func (s *blockingSync) value() Value {
	return NewObject(map[string]Value{
		"wait": NewBuiltin("sync.wait", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			select {
			case s.entered <- struct{}{}:
			default:
			}
			select {
			case <-s.release:
			case <-exec.ctx.Done():
				return NewNil(), exec.ctx.Err()
			}
			return NewNil(), nil
		}),
	})
}

func noopSyncValue() Value {
	return NewObject(map[string]Value{
		"wait": NewBuiltin("sync.wait", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return NewNil(), nil
		}),
	})
}

type callResult struct {
	value Value
	err   error
}

func TestScriptCallOverlappingCallsKeepFunctionEnvIsolated(t *testing.T) {
	script := compileScriptDefault(t, `def helper
  tenant
end

def run
  sync.wait()
  helper
end`)

	barrier := &blockingSync{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	firstDone := make(chan callResult, 1)
	go func() {
		val, callErr := script.Call(ctx, "run", nil, CallOptions{
			Globals: map[string]Value{
				"sync":   barrier.value(),
				"tenant": NewString("first"),
			},
		})
		firstDone <- callResult{value: val, err: callErr}
	}()

	select {
	case <-barrier.entered:
	case <-ctx.Done():
		t.Fatalf("first call did not reach sync.wait: %v", ctx.Err())
	}

	second, err := script.Call(ctx, "run", nil, CallOptions{
		Globals: map[string]Value{
			"sync":   noopSyncValue(),
			"tenant": NewString("second"),
		},
	})
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	if second.Kind() != KindString || second.String() != "second" {
		t.Fatalf("unexpected second call result: %#v", second)
	}

	close(barrier.release)

	select {
	case first := <-firstDone:
		if first.err != nil {
			t.Fatalf("first call failed: %v", first.err)
		}
		if first.value.Kind() != KindString || first.value.String() != "first" {
			t.Fatalf("first call leaked globals from another invocation: %#v", first.value)
		}
	case <-ctx.Done():
		t.Fatalf("first call did not complete: %v", ctx.Err())
	}
}

func TestScriptCallOverlappingCallsKeepClassVarsIsolated(t *testing.T) {
	script := compileScriptDefault(t, `class Counter
  @@count = 0

  def self.bump
    @@count = @@count + 1
  end

  def self.count
    @@count
  end
end

def run
  sync.wait()
  Counter.bump
  Counter.count
end`)

	barrier := &blockingSync{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	firstDone := make(chan callResult, 1)
	go func() {
		val, callErr := script.Call(ctx, "run", nil, CallOptions{
			Globals: map[string]Value{
				"sync": barrier.value(),
			},
		})
		firstDone <- callResult{value: val, err: callErr}
	}()

	select {
	case <-barrier.entered:
	case <-ctx.Done():
		t.Fatalf("first call did not reach sync.wait: %v", ctx.Err())
	}

	second, err := script.Call(ctx, "run", nil, CallOptions{
		Globals: map[string]Value{
			"sync": noopSyncValue(),
		},
	})
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	if second.Kind() != KindInt || second.Int() != 1 {
		t.Fatalf("unexpected second call counter: %#v", second)
	}

	close(barrier.release)

	select {
	case first := <-firstDone:
		if first.err != nil {
			t.Fatalf("first call failed: %v", first.err)
		}
		if first.value.Kind() != KindInt || first.value.Int() != 1 {
			t.Fatalf("first call observed shared class var state: %#v", first.value)
		}
	case <-ctx.Done():
		t.Fatalf("first call did not complete: %v", ctx.Err())
	}
}

func TestScriptCallRebindsEscapedFunctionsToCurrentCallEnv(t *testing.T) {
	script := compileScriptDefault(t, `def format_tenant(value)
  tenant + "-" + value
end

def export_fn
  format_tenant
end

def run_with(fn, value)
  fn(value)
end`)

	exported, err := script.Call(context.Background(), "export_fn", nil, CallOptions{
		Globals: map[string]Value{
			"tenant": NewString("first"),
		},
	})
	if err != nil {
		t.Fatalf("export_fn failed: %v", err)
	}
	if exported.Kind() != KindFunction {
		t.Fatalf("expected function result, got %#v", exported)
	}

	result, err := script.Call(context.Background(), "run_with", []Value{exported, NewString("value")}, CallOptions{
		Globals: map[string]Value{
			"tenant": NewString("second"),
		},
	})
	if err != nil {
		t.Fatalf("run_with failed: %v", err)
	}
	if result.Kind() != KindString || result.String() != "second-value" {
		t.Fatalf("escaped function used stale call env: %#v", result)
	}
}

func TestScriptCallRebindingDoesNotMutateSharedArgMaps(t *testing.T) {
	script := compileScriptDefault(t, `def format_tenant(value)
  tenant + "-" + value
end

def export_fn
  format_tenant
end

def run(ctx)
  sync.wait()
  ctx.fn("value")
end`)

	exported, err := script.Call(context.Background(), "export_fn", nil, CallOptions{
		Globals: map[string]Value{
			"tenant": NewString("bootstrap"),
		},
	})
	if err != nil {
		t.Fatalf("export_fn failed: %v", err)
	}
	if exported.Kind() != KindFunction {
		t.Fatalf("expected function result, got %#v", exported)
	}

	sharedCtx := NewObject(map[string]Value{
		"fn": exported,
	})

	barrier := &blockingSync{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	firstDone := make(chan callResult, 1)
	go func() {
		val, callErr := script.Call(ctx, "run", []Value{sharedCtx}, CallOptions{
			Globals: map[string]Value{
				"sync":   barrier.value(),
				"tenant": NewString("first"),
			},
		})
		firstDone <- callResult{value: val, err: callErr}
	}()

	select {
	case <-barrier.entered:
	case <-ctx.Done():
		t.Fatalf("first call did not reach sync.wait: %v", ctx.Err())
	}

	second, err := script.Call(ctx, "run", []Value{sharedCtx}, CallOptions{
		Globals: map[string]Value{
			"sync":   noopSyncValue(),
			"tenant": NewString("second"),
		},
	})
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	if second.Kind() != KindString || second.String() != "second-value" {
		t.Fatalf("unexpected second call result: %#v", second)
	}

	close(barrier.release)

	select {
	case first := <-firstDone:
		if first.err != nil {
			t.Fatalf("first call failed: %v", first.err)
		}
		if first.value.Kind() != KindString || first.value.String() != "first-value" {
			t.Fatalf("shared arg map mutation leaked env across calls: %#v", first.value)
		}
	case <-ctx.Done():
		t.Fatalf("first call did not complete: %v", ctx.Err())
	}
}

func TestScriptCallPreservesForeignFunctionEnv(t *testing.T) {
	engine := MustNewEngine(Config{})

	producer, err := engine.Compile(`def helper(value)
  "foreign-" + value
end

def wrapper(value)
  helper(value)
end

def export_fn
  wrapper
end`)
	if err != nil {
		t.Fatalf("compile producer failed: %v", err)
	}

	consumer, err := engine.Compile(`def run_with(fn, value)
  fn(value)
end`)
	if err != nil {
		t.Fatalf("compile consumer failed: %v", err)
	}

	foreignFn, err := producer.Call(context.Background(), "export_fn", nil, CallOptions{})
	if err != nil {
		t.Fatalf("export_fn failed: %v", err)
	}
	if foreignFn.Kind() != KindFunction {
		t.Fatalf("expected exported function, got %#v", foreignFn)
	}

	result, err := consumer.Call(context.Background(), "run_with", []Value{foreignFn, NewString("value")}, CallOptions{})
	if err != nil {
		t.Fatalf("consumer call failed: %v", err)
	}
	if result.Kind() != KindString || result.String() != "foreign-value" {
		t.Fatalf("foreign function env was not preserved: %#v", result)
	}
}

func TestScriptCallRebindsEscapedClassValuesToCurrentCall(t *testing.T) {
	script := compileScriptDefault(t, `class Bucket
  @@count = 0

  def self.bump
    @@count = @@count + 1
  end

  def self.snapshot
    { tenant: tenant, count: @@count }
  end
end

def export_class
  Bucket.bump
  Bucket
end

def run_with(klass)
  klass.bump
  klass.snapshot
end`)

	exportedClass, err := script.Call(context.Background(), "export_class", nil, CallOptions{
		Globals: map[string]Value{
			"tenant": NewString("first"),
		},
	})
	if err != nil {
		t.Fatalf("export_class failed: %v", err)
	}
	if exportedClass.Kind() != KindClass {
		t.Fatalf("expected class result, got %#v", exportedClass)
	}

	result, err := script.Call(context.Background(), "run_with", []Value{exportedClass}, CallOptions{
		Globals: map[string]Value{
			"tenant": NewString("second"),
		},
	})
	if err != nil {
		t.Fatalf("run_with failed: %v", err)
	}
	if result.Kind() != KindHash {
		t.Fatalf("expected hash result, got %#v", result)
	}
	got := result.Hash()
	if tenant := got["tenant"]; tenant.Kind() != KindString || tenant.String() != "second" {
		t.Fatalf("escaped class used stale env: %#v", tenant)
	}
	if count := got["count"]; count.Kind() != KindInt || count.Int() != 1 {
		t.Fatalf("escaped class reused stale class var state: %#v", count)
	}
}

func TestScriptCallRebindsEscapedInstancesToCurrentCallState(t *testing.T) {
	script := compileScriptDefault(t, `class Bucket
  @@count = 0

  def initialize(name)
    @name = name
    @@count = @@count + 1
  end

  def report
    { tenant: tenant, count: @@count, name: @name }
  end
end

def export_instance(name)
  Bucket.new(name)
end

def run_with(bucket)
  bucket.report
end`)

	exportedInstance, err := script.Call(context.Background(), "export_instance", []Value{NewString("seed")}, CallOptions{
		Globals: map[string]Value{
			"tenant": NewString("first"),
		},
	})
	if err != nil {
		t.Fatalf("export_instance failed: %v", err)
	}
	if exportedInstance.Kind() != KindInstance {
		t.Fatalf("expected instance result, got %#v", exportedInstance)
	}

	result, err := script.Call(context.Background(), "run_with", []Value{exportedInstance}, CallOptions{
		Globals: map[string]Value{
			"tenant": NewString("second"),
		},
	})
	if err != nil {
		t.Fatalf("run_with failed: %v", err)
	}
	if result.Kind() != KindHash {
		t.Fatalf("expected hash result, got %#v", result)
	}
	got := result.Hash()
	if tenant := got["tenant"]; tenant.Kind() != KindString || tenant.String() != "second" {
		t.Fatalf("escaped instance used stale env: %#v", tenant)
	}
	if count := got["count"]; count.Kind() != KindInt || count.Int() != 0 {
		t.Fatalf("escaped instance reused stale class var state: %#v", count)
	}
	if name := got["name"]; name.Kind() != KindString || name.String() != "seed" {
		t.Fatalf("escaped instance lost ivars during rebinding: %#v", name)
	}
}

func TestScriptCallRebindingPreservesHashAndObjectKindsForAliasedMaps(t *testing.T) {
	script := compileScriptDefault(t, `def run(a, b)
  [a, b]
end`)

	shared := map[string]Value{"x": NewInt(1)}
	hashVal := NewHash(shared)
	objectVal := NewObject(shared)

	result, err := script.Call(context.Background(), "run", []Value{objectVal, hashVal}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindArray || len(result.Array()) != 2 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.Array()[0].Kind() != KindObject {
		t.Fatalf("expected first value to stay object, got %v", result.Array()[0].Kind())
	}
	if result.Array()[1].Kind() != KindHash {
		t.Fatalf("expected second value to stay hash, got %v", result.Array()[1].Kind())
	}
}

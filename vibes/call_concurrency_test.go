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
	engine := NewEngine(Config{})
	script, err := engine.Compile(`def helper
  tenant
end

def run
  sync.wait()
  helper
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

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
	engine := NewEngine(Config{})
	script, err := engine.Compile(`class Counter
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
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

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

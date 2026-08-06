package runtime

import (
	"context"
	goruntime "runtime"
	"sync/atomic"
	"testing"
	"time"
)

// TestNestedTasksShareOneConcurrencyBudget pins that a call tree cannot
// multiply its worker count by nesting Tasks calls inside task functions.
//
// A group validates its own max against the host limit, but each nested task
// runs through Script.Call, which started a fresh group with a fresh
// allowance: concurrency compounded as max^depth. Four levels of max:4 peaked
// at 144 worker goroutines against a host cap of 64, and every child call also
// carried a full step and memory quota (#54).
//
// Not parallel: it counts process-wide goroutines.
func TestNestedTasksShareOneConcurrencyBudget(t *testing.T) {
	script := compileScriptDefault(t, `def leaf(n)
  total = 0
  i = 0
  while i < 20000
    total = total + i
    i = i + 1
  end
  total
end

def level2(n)
  Tasks.map([1, 2, 3, 4], max: 4, with: :leaf)
  n
end

def level1(n)
  Tasks.map([1, 2, 3, 4], max: 4, with: :level2)
  n
end

def level0(n)
  Tasks.map([1, 2, 3, 4], max: 4, with: :level1)
  n
end

def run()
  Tasks.map([1, 2, 3, 4], max: 4, with: :level0)
  "done"
end`)

	stop := make(chan struct{})
	var peak atomic.Int64
	sampling := make(chan struct{})
	go func() {
		close(sampling)
		for {
			select {
			case <-stop:
				return
			default:
				if n := int64(goruntime.NumGoroutine()); n > peak.Load() {
					peak.Store(n)
				}
				time.Sleep(time.Millisecond)
			}
		}
	}()
	<-sampling
	// Baseline after the sampler exists, so it is not counted as a worker.
	base := goruntime.NumGoroutine()

	result, err := script.Call(context.Background(), "run", nil, CallOptions{})
	close(stop)
	if err != nil {
		t.Fatalf("nested tasks failed: %v", err)
	}
	if result.Kind() != KindString || result.String() != "done" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if workers := int(peak.Load()) - base; workers > defaultMaxTaskConcurrency {
		t.Fatalf("nested tasks held %d worker goroutines, want at most the host cap %d",
			workers, defaultMaxTaskConcurrency)
	}
}

// gateProbeCapability records whether the spawned task observed the gate as
// already open. open_gate is what the spawning block performs right after
// spawn; await_gate is what the spawned task calls first.
type gateProbeCapability struct {
	gate     chan struct{}
	ranEarly *atomic.Bool
}

func (c gateProbeCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	gate, ranEarly := c.gate, c.ranEarly
	return map[string]Value{
		"probe": NewObject(map[string]Value{
			"open_gate": NewBuiltin("probe.open_gate", func(*Execution, Value, []Value, map[string]Value, Value) (Value, error) {
				select {
				case <-gate:
				default:
					close(gate)
				}
				return NewNil(), nil
			}),
			"await_gate": NewBuiltin("probe.await_gate", func(*Execution, Value, []Value, map[string]Value, Value) (Value, error) {
				select {
				case <-gate:
				default:
					// The block has not reached open_gate yet, so this task
					// was run during spawn rather than deferred to a wait.
					ranEarly.Store(true)
				}
				return NewNil(), nil
			}),
		}),
	}, nil
}

// TestStarvedTaskGroupKeepsSpawnNonblocking pins that a group the shared
// budget could not staff still returns from spawn before running the task.
// Running it at spawn time would deadlock any block that performs a host
// action the task waits on, which is the common shape: spawn, then set
// something up, then await the handle.
//
// The budget is 1, so the outer group takes it and the nested group is
// starved — that is the path under test.
func TestStarvedTaskGroupKeepsSpawnNonblocking(t *testing.T) {
	t.Parallel()

	var ranEarly atomic.Bool
	script := compileScriptWithConfig(t, Config{MaxTaskConcurrency: 1}, `def worker(n)
  probe.await_gate()
  n
end

def nested(n)
  Tasks.run(max: 1) do |tasks|
    a = tasks.spawn(:worker, 1)
    probe.open_gate()
    a.value
  end
end

def run()
  Tasks.map([1], max: 1, with: :nested)
end`)

	if _, err := script.Call(context.Background(), "run", nil,
		callOptionsWithCapabilities(gateProbeCapability{gate: make(chan struct{}), ranEarly: &ranEarly})); err != nil {
		t.Fatalf("nested tasks failed: %v", err)
	}
	if ranEarly.Load() {
		t.Fatal("a starved group ran its task during spawn, so a block that acts after spawn would deadlock")
	}
}

package runtime

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

type taskBlockingProbe struct {
	started   chan struct{}
	release   chan struct{}
	afterWait chan struct{}
}

func (probe *taskBlockingProbe) value() Value {
	return NewObject(map[string]Value{
		"wait": NewBuiltin("probe.wait", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			select {
			case probe.started <- struct{}{}:
			default:
			}
			select {
			case <-probe.release:
				return NewNil(), nil
			case <-exec.Context().Done():
				return NewNil(), exec.Context().Err()
			}
		}),
		"after_wait": NewBuiltin("probe.after_wait", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if probe.afterWait != nil {
				select {
				case probe.afterWait <- struct{}{}:
				default:
				}
			}
			return NewNil(), nil
		}),
	})
}

type taskFanoutProbe struct {
	started   chan struct{}
	release   chan struct{}
	active    atomic.Int64
	maxActive atomic.Int64
}

func (probe *taskFanoutProbe) value() Value {
	return NewObject(map[string]Value{
		"enter": NewBuiltin("probe.enter", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			active := probe.active.Add(1)
			probe.recordMax(active)
			defer probe.active.Add(-1)

			select {
			case probe.started <- struct{}{}:
			default:
			}
			select {
			case <-probe.release:
				return NewNil(), nil
			case <-exec.Context().Done():
				return NewNil(), exec.Context().Err()
			}
		}),
	})
}

func (probe *taskFanoutProbe) recordMax(active int64) {
	for {
		current := probe.maxActive.Load()
		if active <= current {
			return
		}
		if probe.maxActive.CompareAndSwap(current, active) {
			return
		}
	}
}

func TestTasksRunAutoWaitsAtScopeExit(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		script := compileScriptDefault(t, `def wait_task()
  probe.wait()
  7
end

def run()
  Tasks.run do |tasks|
    tasks.spawn(:wait_task)
    "ready"
  end
end`)

		probe := &taskBlockingProbe{
			started: make(chan struct{}, 1),
			release: make(chan struct{}),
		}
		done := make(chan callResult, 1)
		var wg sync.WaitGroup
		wg.Go(func() {
			val, err := script.Call(context.Background(), "run", nil, CallOptions{
				Globals: map[string]Value{"probe": probe.value()},
			})
			done <- callResult{value: val, err: err}
		})

		select {
		case <-probe.started:
		case result := <-done:
			if result.err != nil {
				t.Fatalf("run returned before task entered probe: %v", result.err)
			}
			t.Fatalf("run returned before auto-wait: %s", result.value.String())
		}

		synctest.Wait()
		select {
		case result := <-done:
			if result.err != nil {
				t.Fatalf("run returned before release with error: %v", result.err)
			}
			t.Fatalf("run returned before release: %s", result.value.String())
		default:
		}

		close(probe.release)
		result := <-done
		wg.Wait()
		if result.err != nil {
			t.Fatalf("run failed: %v", result.err)
		}
		if result.value.Kind() != KindString || result.value.String() != "ready" {
			t.Fatalf("run result = %s, want ready", result.value.String())
		}
	})
}

func TestTasksMapReturnsOrderedResults(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def score_user(user)
  user * 10
end

def run()
  Tasks.map([3, 1, 2], max: 2, with: :score_user)
end`)

	result := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if result.Kind() != KindArray {
		t.Fatalf("run returned %s, want array", result.Kind())
	}
	want := []int64{30, 10, 20}
	for i, value := range result.Array() {
		if value.Kind() != KindInt || value.Int() != want[i] {
			t.Fatalf("result[%d] = %s, want %d", i, value.String(), want[i])
		}
	}
}

func TestTasksMapUsesDefaultConcurrencyAndHostCap(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		script := compileScriptWithConfig(t, Config{
			DefaultTaskConcurrency: 2,
			MaxTaskConcurrency:     3,
		}, `def gated(item)
  probe.enter()
  item
end

def run()
  Tasks.map([1, 2, 3, 4, 5], with: :gated)
end`)

		probe := &taskFanoutProbe{
			started: make(chan struct{}, 5),
			release: make(chan struct{}),
		}
		done := make(chan callResult, 1)
		var wg sync.WaitGroup
		wg.Go(func() {
			val, err := script.Call(context.Background(), "run", nil, CallOptions{
				Globals: map[string]Value{"probe": probe.value()},
			})
			done <- callResult{value: val, err: err}
		})

		for range 2 {
			select {
			case <-probe.started:
			case result := <-done:
				if result.err != nil {
					t.Fatalf("run returned before two tasks entered probe: %v", result.err)
				}
				t.Fatalf("run returned before two tasks entered probe: %s", result.value.String())
			}
		}

		synctest.Wait()
		select {
		case <-probe.started:
			t.Fatalf("more than default task concurrency entered probe before release")
		default:
		}
		if got := probe.maxActive.Load(); got != 2 {
			t.Fatalf("max active tasks = %d, want 2", got)
		}

		close(probe.release)
		result := <-done
		wg.Wait()
		if result.err != nil {
			t.Fatalf("run failed: %v", result.err)
		}
	})
}

func TestTasksRejectMaxOverHostCap(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{
		DefaultTaskConcurrency: 2,
		MaxTaskConcurrency:     2,
	}, `def unit()
  1
end

def run()
  Tasks.map([1], max: 3, with: :unit)
end`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "Tasks.map max 3 exceeds host maximum 2")
}

func TestTaskValueWaitsAndReturnsResult(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def add_one(value)
  value + 1
end

def run()
  Tasks.run do |tasks|
    task = tasks.spawn(:add_one, 41)
    task.value
  end
end`)

	result := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if result.Kind() != KindInt || result.Int() != 42 {
		t.Fatalf("run returned %s, want 42", result.String())
	}
}

func TestTaskFailureReportsAtScopeExit(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def fail_task()
  raise "boom"
end

def run()
  Tasks.run do |tasks|
    tasks.spawn(:fail_task)
    "ignored"
  end
end`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "task fail_task failed")
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "boom")
}

func TestCloneTaskGlobalsCreatesIndependentMutableSnapshots(t *testing.T) {
	t.Parallel()
	globals := map[string]Value{
		"shared": NewHash(map[string]Value{
			"values": NewArray([]Value{NewInt(1)}),
		}),
	}

	first := cloneTaskGlobals(globals)
	second := cloneTaskGlobals(globals)

	firstValues := first["shared"].Hash()["values"].Array()
	firstValues[0] = NewInt(99)
	if got := second["shared"].Hash()["values"].Array()[0]; got.Kind() != KindInt || got.Int() != 1 {
		t.Fatalf("second cloned global value = %s, want 1", got.String())
	}
	if got := globals["shared"].Hash()["values"].Array()[0]; got.Kind() != KindInt || got.Int() != 1 {
		t.Fatalf("original global value = %s, want 1", got.String())
	}
}

func TestTasksCloneInheritedMutableGlobalsForEachJob(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def mark_global(item)
  if item == 1
    shared[:one] = item
  else
    shared[:two] = item
  end
  shared.size
end

def run()
  Tasks.map([1, 2], max: 1, with: :mark_global)
end`)

	shared := NewHash(map[string]Value{})
	result := callScript(t, context.Background(), script, "run", nil, CallOptions{
		Globals: map[string]Value{"shared": shared},
	})
	if result.Kind() != KindArray {
		t.Fatalf("run result = %s, want array", result.Kind())
	}
	for i, value := range result.Array() {
		if value.Kind() != KindInt || value.Int() != 1 {
			t.Fatalf("result[%d] = %s, want 1", i, value.String())
		}
	}
	if len(shared.Hash()) != 0 {
		t.Fatalf("host global shared hash = %s, want unchanged empty hash", shared.String())
	}
}

func TestTasksInheritedGlobalsPreserveAliasesWithinJob(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def alias_probe(item)
  left[:item] = item
  [right[:item], left.size, right.size]
end

def run()
  Tasks.map([7], max: 1, with: :alias_probe)
end`)

	shared := NewHash(map[string]Value{})
	result := callScript(t, context.Background(), script, "run", nil, CallOptions{
		Globals: map[string]Value{
			"left":  shared,
			"right": shared,
		},
	})
	if result.Kind() != KindArray || len(result.Array()) != 1 {
		t.Fatalf("run result = %s, want single result array", result.String())
	}
	compareArrays(t, result.Array()[0], []Value{NewInt(7), NewInt(1), NewInt(1)})
	if len(shared.Hash()) != 0 {
		t.Fatalf("host global shared hash = %s, want unchanged empty hash", shared.String())
	}
}

func TestNestedTasksInheritLazyGlobals(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def read_shared(item)
  shared[:seed] + item
end

def spawn_read(item)
  Tasks.run(max: 1) do |tasks|
    tasks.spawn(:read_shared, item).value
  end
end

def run()
  Tasks.map([1, 2], max: 1, with: :spawn_read)
end`)

	result := callScript(t, context.Background(), script, "run", nil, CallOptions{
		Globals: map[string]Value{
			"shared": NewHash(map[string]Value{"seed": NewInt(10)}),
		},
	})
	compareArrays(t, result, []Value{NewInt(11), NewInt(12)})
}

func TestTaskRetainedResultsCountTowardParentMemoryQuota(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{
		StepQuota:        1_000_000,
		MemoryQuotaBytes: 64 * 1024,
	}, `def payload(size)
  items = []
  for i in 1..size
    items = items.push("payload-" + i)
  end
  items
end

def run(count, size)
  Tasks.run(max: 1) do |tasks|
    handles = []
    if count > 0
      for i in 1..count
        handles = handles.push(tasks.spawn(:payload, size))
      end
    end
    handles.size
  end
end`)

	result := callScript(t, context.Background(), script, "run", []Value{NewInt(0), NewInt(120)}, CallOptions{})
	if result.Kind() != KindInt || result.Int() != 0 {
		t.Fatalf("run(0, 120) = %s, want 0", result.String())
	}

	err := callScriptErr(t, context.Background(), script, "run", []Value{NewInt(8), NewInt(120)}, CallOptions{})
	requireRuntimeErrorType(t, err, runtimeErrorTypeLimit)
}

func TestTasksMapReportsWorkerFailureWhileEnqueueIsBlocked(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		script := compileScriptDefault(t, `def fail_when_released(item)
  probe.wait()
  raise "boom"
end

def run()
  Tasks.map([1, 2, 3], max: 1, with: :fail_when_released)
end`)

		probe := &taskBlockingProbe{
			started: make(chan struct{}, 1),
			release: make(chan struct{}),
		}
		done := make(chan callResult, 1)
		var wg sync.WaitGroup
		wg.Go(func() {
			val, err := script.Call(context.Background(), "run", nil, CallOptions{
				Globals: map[string]Value{"probe": probe.value()},
			})
			done <- callResult{value: val, err: err}
		})

		select {
		case <-probe.started:
		case result := <-done:
			if result.err != nil {
				t.Fatalf("run returned before task entered probe: %v", result.err)
			}
			t.Fatalf("run returned before task entered probe: %s", result.value.String())
		}

		synctest.Wait()
		select {
		case result := <-done:
			if result.err != nil {
				t.Fatalf("run returned before release with error: %v", result.err)
			}
			t.Fatalf("run returned before release: %s", result.value.String())
		default:
		}

		close(probe.release)
		result := <-done
		wg.Wait()
		requireErrorContains(t, result.err, "task fail_when_released failed")
		requireErrorContains(t, result.err, "boom")
		if result.err != nil && strings.Contains(result.err.Error(), "context canceled") {
			t.Fatalf("run error = %v, want task failure before cancellation", result.err)
		}
	})
}

func TestTaskInputsMustBeDataOnly(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def identity(value)
  value
end

def run()
  Tasks.run do |tasks|
    tasks.spawn(:identity, identity)
  end
end`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "tasks.spawn argument 1 must be data-only")
}

func TestTaskResultsMustBeDataOnly(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def identity(value)
  value
end

def returns_callable()
  identity
end

def run()
  Tasks.run do |tasks|
    task = tasks.spawn(:returns_callable)
    task.value
  end
end`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "task returns_callable return value must be data-only")
}

func TestTaskHandleCannotBeUsedAfterScopeExit(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def identity(value)
  value
end

def run()
  task = Tasks.run do |tasks|
    tasks.spawn(:identity, 1)
  end
  task.value
end`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "task handle cannot be used after task scope exits")
}

func TestNewEngineRejectsDefaultTaskConcurrencyAboveCap(t *testing.T) {
	t.Parallel()
	_, err := NewEngine(Config{
		DefaultTaskConcurrency: 3,
		MaxTaskConcurrency:     2,
	})
	requireErrorContains(t, err, "default task concurrency cannot exceed max task concurrency")
}

func TestNewEngineUsesLowerHostCapAsImplicitDefaultTaskConcurrency(t *testing.T) {
	t.Parallel()
	engine, err := NewEngine(Config{
		MaxTaskConcurrency: 2,
	})
	if err != nil {
		t.Fatalf("NewEngine(Config{MaxTaskConcurrency: 2}) failed: %v", err)
	}
	if got := engine.config.DefaultTaskConcurrency; got != 2 {
		t.Fatalf("default task concurrency = %d, want 2", got)
	}
}

func TestTasksMapRejectsInvalidFunctionKeyword(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def run()
  Tasks.map([1], with: 1)
end`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "Tasks.map with function name must be a symbol or string")
}

func TestTasksSpawnPassesKeywordArguments(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def describe(value, suffix)
  value + "-" + suffix
end

def run()
  Tasks.run do |tasks|
    task = tasks.spawn(:describe, "task", suffix: "done")
    task.value
  end
end`)

	result := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if result.Kind() != KindString || result.String() != "task-done" {
		t.Fatalf("run returned %s, want task-done", result.String())
	}
}

func TestTasksWaitIsExplicitBarrier(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		script := compileScriptDefault(t, `def wait_task()
  probe.wait()
  1
end

def run()
  Tasks.run do |tasks|
    tasks.spawn(:wait_task)
    tasks.wait
    probe.after_wait()
    "after"
  end
end`)

		probe := &taskBlockingProbe{
			started:   make(chan struct{}, 1),
			release:   make(chan struct{}),
			afterWait: make(chan struct{}, 1),
		}
		done := make(chan callResult, 1)
		var wg sync.WaitGroup
		wg.Go(func() {
			val, err := script.Call(context.Background(), "run", nil, CallOptions{
				Globals: map[string]Value{"probe": probe.value()},
			})
			done <- callResult{value: val, err: err}
		})

		select {
		case <-probe.started:
		case result := <-done:
			if result.err != nil {
				t.Fatalf("run returned before task entered probe: %v", result.err)
			}
			t.Fatalf("run returned before task entered probe: %s", result.value.String())
		}

		synctest.Wait()
		select {
		case <-probe.afterWait:
			t.Fatalf("tasks.wait allowed block body to continue before task completed")
		default:
		}
		select {
		case result := <-done:
			if result.err != nil {
				t.Fatalf("run returned before release with error: %v", result.err)
			}
			t.Fatalf("run returned before release: %s", result.value.String())
		default:
		}

		close(probe.release)
		result := <-done
		wg.Wait()
		if result.err != nil {
			t.Fatalf("run failed: %v", result.err)
		}
		select {
		case <-probe.afterWait:
		default:
			t.Fatalf("run returned without executing code after tasks.wait")
		}
		if result.value.Kind() != KindString || result.value.String() != "after" {
			t.Fatalf("run result = %s, want after", result.value.String())
		}
	})
}

func TestTasksMapRejectsUnknownKeyword(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def identity(value)
  value
end

def run()
  Tasks.map([1], with: :identity, limit: 1)
end`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "Tasks.map unknown keyword argument limit")
}

func TestTasksRunRejectsMissingBlock(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def run()
  Tasks.run()
end`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "Tasks.run requires a block")
}

func TestTasksRunRejectsInvalidMax(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "string",
			source: `def run()
  Tasks.run(max: "4") do |tasks|
    nil
  end
end`,
			want: "Tasks.run max must be an integer",
		},
		{
			name: "zero",
			source: `def run()
  Tasks.run(max: 0) do |tasks|
    nil
  end
end`,
			want: "Tasks.run max must be at least 1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, tc.source)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

func TestTasksMapCanUseStringFunctionName(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def double(value)
  value * 2
end

def run()
  Tasks.map([2, 4], with: "double")
end`)

	result := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if result.Kind() != KindArray {
		t.Fatalf("run returned %s, want array", result.Kind())
	}
	want := []int64{4, 8}
	for i, value := range result.Array() {
		if value.Kind() != KindInt || value.Int() != want[i] {
			t.Fatalf("result[%d] = %s, want %d", i, value.String(), want[i])
		}
	}
}

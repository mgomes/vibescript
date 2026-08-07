package runtime

import (
	"context"
	goruntime "runtime"
	"sync"
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

// TestMapReservesOnlyForItsItems pins that a map reserves for the work it has
// rather than the ceiling it was allowed. A slot held by a worker that will
// never receive a job is a slot a nested group cannot have, and a starved
// nested group defers its child until a wait -- so a parent that blocks on a
// signal the child produces never reaches the wait that would run it.
//
// The parent here waits on the gate before awaiting the handle, which is the
// shape that deadlocks when the nested group gets no worker.
func TestMapReservesOnlyForItsItems(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MaxTaskConcurrency: 2}, `def opener(n)
  probe.open_gate()
  n
end

def nested(n)
  Tasks.run(max: 1) do |tasks|
    a = tasks.spawn(:opener, 1)
    probe.await_gate()
    a.value
  end
end

def run()
  Tasks.map([1], max: 2, with: :nested)
end`)

	var starved atomic.Bool
	done := make(chan error, 1)
	go func() {
		_, err := script.Call(context.Background(), "run", nil,
			callOptionsWithCapabilities(blockingGateCapability{gate: make(chan struct{}), starved: &starved}))
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("nested tasks failed: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("deadlock: the nested group was starved by an idle sibling slot, so its child never ran")
	}
	if starved.Load() {
		t.Fatal("the parent waited out its timeout: the nested group was starved by an idle sibling slot")
	}
}

// TestRunGroupHoldsOnlyTheSlotsItUses pins the same property for Tasks.run,
// which unlike map has no item count when its group is created: a group must
// hold slots for the tasks it actually spawned, not for the max it was allowed.
//
// A run group that reserved its full max up front spent the whole host limit
// on one spawned task, leaving a nested group with no worker. The nested group
// defers its child until a wait, so a parent that blocks on a signal the child
// produces never reaches the wait that would run it.
func TestRunGroupHoldsOnlyTheSlotsItUses(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MaxTaskConcurrency: 2}, `def opener(n)
  probe.open_gate()
  n
end

def nested(n)
  Tasks.run(max: 1) do |tasks|
    a = tasks.spawn(:opener, 1)
    probe.await_gate()
    a.value
  end
end

def run()
  Tasks.run(max: 2) do |tasks|
    a = tasks.spawn(:nested, 1)
    a.value
  end
end`)

	var starved atomic.Bool
	done := make(chan error, 1)
	go func() {
		_, err := script.Call(context.Background(), "run", nil,
			callOptionsWithCapabilities(blockingGateCapability{gate: make(chan struct{}), starved: &starved}))
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("nested tasks failed: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("deadlock: the nested group was starved by slots the outer group reserved and never used")
	}
	if starved.Load() {
		t.Fatal("the parent waited out its timeout: the outer group held a slot for a task it never spawned")
	}
}

// TestCompletedTaskFreesItsWorkerForTheNextSpawn pins that a group reuses the
// worker an awaited task left idle instead of reserving another.
//
// Reserving per job ever spawned rather than per outstanding job meant a group
// that spawned, awaited, then spawned again held two slots for one task's worth
// of concurrency, and the second slot was one a nested group could not have.
func TestCompletedTaskFreesItsWorkerForTheNextSpawn(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MaxTaskConcurrency: 2}, `def quick(n)
  n
end

def opener(n)
  probe.open_gate(n)
  n
end

def nested(n)
  Tasks.run(max: 1) do |tasks|
    a = tasks.spawn(:opener, n)
    probe.await_gate(n)
    a.value
  end
end

def run()
  Tasks.run(max: 2) do |tasks|
    first = tasks.spawn(:quick, 1)
    first.value
    second = tasks.spawn(:nested, 2)
    second.value
  end
end`)

	assertTaskTreeMakesProgress(t, script, "the first task's idle worker was not reused, so the nested group was starved")
}

// TestReleasedSlotsReachGroupsWaitingForThem pins that capacity returned to the
// shared pool is offered to a group holding work no worker could take.
//
// A starved group runs its deferred jobs only when something waits on them, so
// a parent that blocks on a signal its child produces never reaches the wait
// that would run it. If a sibling's slot came back and nothing handed it over,
// the tree deadlocked with capacity to spare.
func TestReleasedSlotsReachGroupsWaitingForThem(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MaxTaskConcurrency: 3}, `def opener(n)
  probe.open_gate(n)
  n
end

def nested(n)
  Tasks.run(max: 1) do |tasks|
    a = tasks.spawn(:opener, n)
    probe.await_gate(n)
    a.value
  end
end

def run()
  Tasks.map([1, 2], max: 2, with: :nested)
end`)

	assertTaskTreeMakesProgress(t, script, "a sibling's released slot never reached the group waiting for it")
}

// assertTaskTreeMakesProgress runs the script against per-tree gates and fails
// if any parent waits out its gate, which is what starvation looks like from
// inside the script.
func assertTaskTreeMakesProgress(t *testing.T, script *Script, starvedMsg string) {
	t.Helper()

	var starved atomic.Bool
	gates := keyedGateCapability{mu: &sync.Mutex{}, gates: map[int64]chan struct{}{}, starved: &starved}
	done := make(chan error, 1)
	go func() {
		_, err := script.Call(context.Background(), "run", nil, callOptionsWithCapabilities(gates))
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("nested tasks failed: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("deadlock: %s", starvedMsg)
	}
	if starved.Load() {
		t.Fatalf("a parent waited out its gate: %s", starvedMsg)
	}
}

// keyedGateCapability gives each task tree its own gate, so one tree's child
// cannot satisfy another tree's parent and hide a starved group.
type keyedGateCapability struct {
	mu      *sync.Mutex
	gates   map[int64]chan struct{}
	starved *atomic.Bool
}

func (c keyedGateCapability) gate(key int64) chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	gate, ok := c.gates[key]
	if !ok {
		gate = make(chan struct{})
		c.gates[key] = gate
	}
	return gate
}

// open closes a gate under the lock, so concurrent openers cannot both decide
// it is still open and close it twice.
func (c keyedGateCapability) open(key int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	gate, ok := c.gates[key]
	if !ok {
		gate = make(chan struct{})
		c.gates[key] = gate
	}
	select {
	case <-gate:
	default:
		close(gate)
	}
}

func (c keyedGateCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"probe": NewObject(map[string]Value{
			"open_gate": NewBuiltin("probe.open_gate", func(_ *Execution, _ Value, args []Value, _ map[string]Value, _ Value) (Value, error) {
				c.open(args[0].Int())
				return NewNil(), nil
			}),
			"await_gate": NewBuiltin("probe.await_gate", func(_ *Execution, _ Value, args []Value, _ map[string]Value, _ Value) (Value, error) {
				select {
				case <-c.gate(args[0].Int()):
				case <-time.After(15 * time.Second):
					// The child never ran, so it never opened this gate.
					c.starved.Store(true)
				}
				return NewNil(), nil
			}),
		}),
	}, nil
}

// blockingGateCapability makes await_gate genuinely wait, so a child that is
// never scheduled deadlocks its parent rather than silently proceeding. The
// wait is bounded so a regression fails the test instead of hanging the suite.
type blockingGateCapability struct {
	gate    chan struct{}
	starved *atomic.Bool
}

func (c blockingGateCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	gate, starved := c.gate, c.starved
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
				case <-time.After(15 * time.Second):
					// The child never ran, so it never opened the gate.
					starved.Store(true)
				}
				return NewNil(), nil
			}),
		}),
	}, nil
}

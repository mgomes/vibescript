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

// TestWaitingOnOneHandleDoesNotRunUnrelatedWork pins that a waiter runs only
// the work it is waiting for.
//
// A starved group runs its queue on whatever waits for it. Draining the whole
// queue there trapped the parent inside a later job: if that job waits on a
// host action the parent performs once the first result is in hand, the parent
// never returns to perform it.
func TestWaitingOnOneHandleDoesNotRunUnrelatedWork(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MaxTaskConcurrency: 2}, `def quick(n)
  n
end

def waiter(n)
  probe.await_gate(n)
  n
end

def nested(n)
  Tasks.run(max: 2) do |tasks|
    a = tasks.spawn(:quick, 1)
    b = tasks.spawn(:waiter, 7)
    a.value
    probe.open_gate(7)
    b.value
  end
end

def run()
  Tasks.map([1, 2], max: 2, with: :nested)
end`)

	assertTaskTreeMakesProgress(t, script,
		"awaiting the first handle ran the second job, so the gate it waits on was never opened")
}

// TestInlineWorkCountsAgainstTheGroupMax pins that a job run inline on a
// waiter still occupies its group's concurrency, so a slot freed elsewhere
// cannot start a second job beside it.
//
// The starved group here is max:1 and holds two jobs, so its parent runs the
// first inline. A sibling finishing at that moment returns its slot, and if the
// inline job is not counted the group looks idle and takes that slot for its
// second job, running two tasks at once in a group whose max says one.
func TestInlineWorkCountsAgainstTheGroupMax(t *testing.T) {
	t.Parallel()

	var peak, running atomic.Int64
	// Only the starved group's tasks touch the probe, so what it counts is that
	// group's concurrency rather than the tree's.
	script := compileScriptWithConfig(t, Config{MaxTaskConcurrency: 2}, `def busy(n)
  probe.enter(n)
  probe.leave(n)
  n
end

def nested(n)
  Tasks.run(max: 1) do |tasks|
    a = tasks.spawn(:busy, 1)
    b = tasks.spawn(:busy, 2)
    a.value
    b.value
  end
end

def holder(n)
  probe.await_started()
  n
end

def fanout(n)
  if n == 1
    nested(n)
  else
    holder(n)
  end
end

def run()
  Tasks.map([1, 2], max: 2, with: :fanout)
end`)

	// The sibling holds its slot until the inline job is running, so the slot
	// it returns lands exactly while that job is in flight. Without it the
	// sibling finishes first, the group is never starved, and the path under
	// test is not reached at all.
	probe := concurrencyProbeCapability{
		peak: &peak, running: &running,
		started: make(chan struct{}), mu: &sync.Mutex{},
	}
	if _, err := script.Call(context.Background(), "run", nil,
		callOptionsWithCapabilities(probe)); err != nil {
		t.Fatalf("nested tasks failed: %v", err)
	}
	if got := peak.Load(); got > 1 {
		t.Fatalf("a max:1 group ran %d tasks at once; inline work must count against the max", got)
	}
}

// concurrencyProbeCapability records how many tasks are inside the probe at
// once, which is what a group's max bounds, and lets one task wait until
// another has entered.
type concurrencyProbeCapability struct {
	peak    *atomic.Int64
	running *atomic.Int64
	started chan struct{}
	mu      *sync.Mutex
}

func (c concurrencyProbeCapability) markStarted() {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.started:
	default:
		close(c.started)
	}
}

func (c concurrencyProbeCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	peak, running := c.peak, c.running
	return map[string]Value{
		"probe": NewObject(map[string]Value{
			"enter": NewBuiltin("probe.enter", func(*Execution, Value, []Value, map[string]Value, Value) (Value, error) {
				now := running.Add(1)
				for {
					seen := peak.Load()
					if now <= seen || peak.CompareAndSwap(seen, now) {
						break
					}
				}
				c.markStarted()
				// Long enough for a slot returned meanwhile to be handed on and
				// overlap this task, which is the race under test.
				time.Sleep(50 * time.Millisecond)
				return NewNil(), nil
			}),
			"leave": NewBuiltin("probe.leave", func(*Execution, Value, []Value, map[string]Value, Value) (Value, error) {
				running.Add(-1)
				return NewNil(), nil
			}),
			"await_started": NewBuiltin("probe.await_started", func(*Execution, Value, []Value, map[string]Value, Value) (Value, error) {
				select {
				case <-c.started:
				case <-time.After(15 * time.Second):
				}
				return NewNil(), nil
			}),
		}),
	}, nil
}

// TestSiblingScopesOnOneExecutionShareThePool pins that a scope opened inside
// another scope's block draws from the same pool.
//
// The pool rides on the context the group hands to the calls it drives, but a
// block that opens a second scope runs on the SAME execution, whose context was
// never replaced. That scope saw no pool and started its own, putting both
// scopes' workers on the host at once and reopening the multiplication the
// shared pool exists to stop (#54).
//
// Not parallel: it counts process-wide goroutines.
func TestSiblingScopesOnOneExecutionShareThePool(t *testing.T) {
	script := compileScriptWithConfig(t, Config{MaxTaskConcurrency: 4}, `def leaf(n)
  total = 0
  i = 0
  while i < 20000
    total = total + i
    i = i + 1
  end
  total
end

def run()
  Tasks.run(max: 4) do |tasks|
    a = tasks.spawn(:leaf, 1)
    b = tasks.spawn(:leaf, 2)
    inner = Tasks.map([1, 2, 3, 4], max: 4, with: :leaf)
    a.value + b.value + inner.length
  end
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
	base := goruntime.NumGoroutine()

	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err != nil {
		close(stop)
		t.Fatalf("sibling scopes failed: %v", err)
	}
	close(stop)
	if workers := int(peak.Load()) - base; workers > 4 {
		t.Fatalf("two scopes on one execution held %d workers, want at most the host cap %d", workers, 4)
	}
}

// TestAWaiterMayUseSpareGroupConcurrency pins that a waiter runs queued work
// when the group is under its own max, rather than only when it is idle.
//
// The waiter's goroutine belongs to a slot already counted and is blocked, so
// borrowing it adds no concurrency. Refusing while any job was running deadlocked
// the shape where that running job waits on something the parent does only once
// the deferred handle is in hand.
func TestAWaiterMayUseSpareGroupConcurrency(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MaxTaskConcurrency: 2}, `def blocker(n)
  probe.await_gate(n)
  n
end

def opener(n)
  probe.open_gate(n)
  n
end

def nested(n)
  Tasks.run(max: 2) do |tasks|
    a = tasks.spawn(:blocker, 1)
    b = tasks.spawn(:opener, 2)
    b.value
    probe.open_gate(1)
    a.value
  end
end

def run()
  Tasks.map([1], max: 1, with: :nested)
end`)

	assertTaskTreeMakesProgress(t, script,
		"a waiter refused to run queued work while the group was under its own max")
}

// TestWaitingRunsTheAwaitedJobNotTheQueueHead pins that a waiter runs the job
// it is waiting for rather than whatever happens to be first in the queue.
//
// Popping the head ran an unrelated job on the waiter's behalf, which deadlocks
// when the awaited handle sits behind one that is itself waiting for something
// the waiter only does once it has that result.
//
// The sibling holds the second slot until the end, so the nested group is
// starved for the whole window and everything it runs goes through the waiter.
func TestWaitingRunsTheAwaitedJobNotTheQueueHead(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MaxTaskConcurrency: 2}, `def blocker(n)
  probe.await_gate(n)
  n
end

def quick(n)
  n
end

def holder(n)
  probe.await_gate(7)
  n
end

def nested(n)
  Tasks.run(max: 2) do |tasks|
    a = tasks.spawn(:blocker, 1)
    b = tasks.spawn(:quick, 2)
    b.value
    probe.open_gate(1)
    a.value
    probe.open_gate(7)
    n
  end
end

def fanout(n)
  if n == 1
    nested(n)
  else
    holder(n)
  end
end

def run()
  Tasks.map([1, 2], max: 2, with: :fanout)
end`)

	assertTaskTreeMakesProgress(t, script,
		"awaiting the second handle ran the blocking job queued ahead of it")
}

// TestTheRootGoroutineRunsNoTaskWork pins that queued work never runs on a
// goroutine that holds no slot.
//
// The root goroutine is not a task, so running a job there is one more script
// on the host than the pool allows. A scope opened after the pool is spent was
// exactly that case: waiting on it ran its job inline on the root, alongside
// the tasks already holding every slot.
//
// Not parallel: it counts concurrent tasks process-wide.
func TestTheRootGoroutineRunsNoTaskWork(t *testing.T) {
	var peak, running atomic.Int64
	script := compileScriptWithConfig(t, Config{MaxTaskConcurrency: 2}, `def busy(n)
  probe.enter(n)
  probe.leave(n)
  n
end

def run()
  Tasks.run(max: 2) do |tasks|
    a = tasks.spawn(:busy, 1)
    b = tasks.spawn(:busy, 2)
    inner = Tasks.map([3], max: 1, with: :busy)
    a.value + b.value + inner.length
  end
end`)

	probe := concurrencyProbeCapability{
		peak: &peak, running: &running,
		started: make(chan struct{}), mu: &sync.Mutex{},
	}
	if _, err := script.Call(context.Background(), "run", nil, callOptionsWithCapabilities(probe)); err != nil {
		t.Fatalf("nested scopes failed: %v", err)
	}
	if got := peak.Load(); got > 2 {
		t.Fatalf("%d tasks ran at once against a host cap of 2; the root goroutine holds no slot", got)
	}
}

// TestAFreedSlotReachesAStarvedChildBeforeALocalSibling pins which waiting
// group a freed slot goes to.
//
// Every waiting group's work is unrunnable by definition, so the choice decides
// whether the tree makes progress. A nested child is what a blocked parent is
// waiting on; a sibling of that parent is waiting on nothing in particular.
// Handing the slot to the sibling deadlocks while handing it to the child does
// not, and both fit the same cap: the child runs beside the blocked parent,
// finishes, and the sibling runs after the parent it unblocked has gone.
//
// The ordering is forced rather than raced. The quick task holds its slot until
// the parent signals that the child is queued, so the slot is freed with both
// groups waiting and the choice is the only thing under test.
func TestAFreedSlotReachesAStarvedChildBeforeALocalSibling(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MaxTaskConcurrency: 2}, `def child(n)
  probe.open_gate(9)
  n
end

def holder(n)
  Tasks.run(max: 1) do |tasks|
    c = tasks.spawn(:child, 1)
    probe.open_gate(5)
    probe.await_gate(9)
    c.value
  end
end

def quick(n)
  probe.await_gate(5)
  n
end

def sibling(n)
  probe.await_gate(9)
  n
end

def run()
  Tasks.run(max: 2) do |tasks|
    a = tasks.spawn(:holder, 1)
    q = tasks.spawn(:quick, 2)
    b = tasks.spawn(:sibling, 3)
    q.value
    a.value
    b.value
  end
end`)

	assertTaskTreeMakesProgress(t, script,
		"the freed slot went to a local sibling while the nested child that unblocks everything stayed queued")
}

// TestStarvedGroupsAreOfferedSlotsInRegistrationOrder pins that a freed slot
// goes to whoever has waited longest, rather than to whichever group a map
// happened to yield first.
//
// Which waiting group gets a slot decides whether the tree makes progress, so
// the order has to be an order. This checks the property directly, because the
// scripts that depend on it deadlock only for particular interleavings and so
// make flaky end-to-end tests.
func TestStarvedGroupsAreOfferedSlotsInRegistrationOrder(t *testing.T) {
	t.Parallel()

	budget := newTaskConcurrencyBudget(1)
	first := &taskGroup{budget: budget, max: 1}
	second := &taskGroup{budget: budget, max: 1}
	third := &taskGroup{budget: budget, max: 1}

	for _, group := range []*taskGroup{first, second, third} {
		budget.markStarved(group)
	}
	// Registering again must not move a group to the back of the queue, but it
	// must still advance the generation: a group stays listed after its queue
	// drains, so a repeat registration is how new work announces itself.
	genBefore := budget.starvedGeneration()
	budget.markStarved(second)
	if budget.starvedGeneration() == genBefore {
		t.Fatal("re-registering a listed group did not advance the wakeup generation")
	}

	if got := budget.starved; len(got) != 3 || got[0] != first || got[1] != second || got[2] != third {
		t.Fatalf("waiting groups are not in registration order: %v", got)
	}

	budget.forget(second)
	if got := budget.starved; len(got) != 2 || got[0] != first || got[1] != third {
		t.Fatalf("forgetting a group disturbed the order of the rest: %v", got)
	}
}

package runtime

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
)

func registerTaskBuiltins(engine *Engine) {
	engine.builtinsMu.Lock()
	defer engine.builtinsMu.Unlock()

	engine.builtins["Tasks"] = NewObject(map[string]Value{
		"run": NewBuiltin("Tasks.run", builtinTasksRun),
		"map": NewBuiltin("Tasks.map", builtinTasksMap),
	})
}

// latchGroupTaskExhaustion transfers a worker's exhaustion into this
// (parent) execution's latch as errors cross the task boundary on the parent
// goroutine. The group learns of worker exhaustion through a trusted
// out-of-band channel — the worker execution's own latch, observed by the
// task machinery when the worker call returns — never by inspecting error
// values, which a stateful adapter could forge or replay. Without the
// transfer, an adapter that discarded the task error (cap.swallow {
// Tasks.map(...) }) let the call succeed past a genuine kill; with firstErr
// alone, an ordinary failure arriving first shadowed a concurrent kill.
//
// An observed exhaustion also becomes the returned error when err is nil.
// A worker publishes its exhaustion and its group error under separate lock
// windows, so a concurrent spawn can read a nil group error while the
// exhaustion is already visible; returning nil there would let the spawn
// keep cloning and enqueuing jobs — each with a fresh worker budget — after
// the quota kill. The same rule makes a group whose only failure was
// swallowed inside the worker (leaving firstErr nil) still surface the
// termination instead of completing normally against a latched parent.
func (exec *Execution) latchGroupTaskExhaustion(group *taskGroup, err error) error {
	if ex := group.exhaustion(); ex != nil {
		_ = exec.latchExhaustion(ex)
		if err == nil {
			err = ex
		}
	}
	return err
}

func builtinTasksRun(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) != 0 {
		return NewNil(), fmt.Errorf("Tasks.run does not take positional arguments")
	}
	if err := ensureBlock(block, "Tasks.run"); err != nil {
		return NewNil(), err
	}
	max, err := taskConcurrency(exec, "Tasks.run", kwargs, map[string]struct{}{
		"max": {},
	})
	if err != nil {
		return NewNil(), err
	}

	group := newTaskGroup(exec, max, true)
	exec.pushTaskGroup(group)
	defer exec.popTaskGroup()
	defer group.releaseRetainedResults()

	result, blockErr := exec.CallBlock(block, []Value{group.managerValue()})
	if blockErr != nil {
		group.cancel()
		_ = exec.latchGroupTaskExhaustion(group, group.closeAndWait())
		return NewNil(), blockErr
	}
	if err := exec.latchGroupTaskExhaustion(group, group.closeAndWait()); err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemoryValue(result); err != nil {
		return NewNil(), err
	}
	return result, nil
}

func builtinTasksMap(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("Tasks.map does not accept blocks")
	}
	if len(args) != 1 {
		return NewNil(), fmt.Errorf("Tasks.map expects one array argument")
	}
	if args[0].Kind() != KindArray {
		return NewNil(), fmt.Errorf("Tasks.map expects an array")
	}
	functionName, err := taskRequiredFunctionKeyword("Tasks.map", kwargs)
	if err != nil {
		return NewNil(), err
	}
	max, err := taskConcurrency(exec, "Tasks.map", kwargs, map[string]struct{}{
		"max":  {},
		"with": {},
	})
	if err != nil {
		return NewNil(), err
	}

	items := args[0].Array()
	if len(items) == 0 {
		return NewArray(nil), nil
	}

	group := newTaskGroup(exec, max, false)
	exec.pushTaskGroup(group)
	defer exec.popTaskGroup()
	defer group.releaseRetainedResults()

	handles := make([]*taskHandle, len(items))
	for i, item := range items {
		handle, err := group.spawnUnary(exec, functionName, item)
		if err != nil {
			group.cancel()
			_ = exec.latchGroupTaskExhaustion(group, group.closeAndWait())
			return NewNil(), err
		}
		handles[i] = handle
	}

	if err := exec.latchGroupTaskExhaustion(group, group.closeAndWait()); err != nil {
		return NewNil(), err
	}

	results := make([]Value, len(handles))
	for i, handle := range handles {
		result, err := handle.result()
		if err != nil {
			return NewNil(), err
		}
		results[i] = result
	}
	result := NewArray(results)
	if err := exec.checkMemoryValue(result); err != nil {
		return NewNil(), err
	}
	return result, nil
}

// maxInlineTaskDepth bounds how many task jobs may nest on one goroutine.
//
// A job only runs inline when the shared pool is spent, and each inline level
// is a whole nested Execution stacked on the submitting goroutine's Go stack,
// so the count is both the signal and the cost. Nothing else bounded it: the
// nested Execution carries a fresh recursion cap, step quota and memory quota,
// so a task function that opens another starved group per level grew the host
// stack by a fixed amount forever, and added a quota to the sandbox's total
// with every level. Measured at 20 Go frames and 13 KiB of goroutine stack per
// level, which reaches Go's 1 GB stack limit — a fatal, unrecoverable error —
// in about 76,000 levels.
//
// The recursion budget an inline job inherits from its caller does not subsume
// this. That budget shrinks by the frames each level holds, but a task function
// whose body is just the nested call holds exactly one frame, and the budget
// floors at one rather than at zero so that a level with nothing left can still
// run a leaf. A one-frame function per level therefore sits on that floor
// forever: with the budget inherited and this count removed, the same script
// still reached 300 levels and 301 nested Executions. The budget bounds what a
// chain may do; the count bounds how long it may be.
//
// Sixteen is chosen against the cost, which is not the stack. Stack is the
// cheap part: a level is about 20 frames and 13 KiB, so even sixty-four levels
// is under 1,400 frames, nowhere near the fatal threshold. What each level
// really costs is a whole Execution with its own memory quota, live at the same
// time as every other level's -- measured at 148 MiB retained on one goroutine
// for a 64-deep chain against an 8 MiB per-execution quota. A slot-holding
// goroutine carries one such chain, so live memory is bounded by the pool times
// one more than this number times MemoryQuotaBytes, which on the defaults is
// roughly 17 GiB. Raising this raises that in proportion; it is a multiplier on
// the host's memory quota, not spare stack headroom.
//
// Only the inline factor is this constant's to bound. The pool factor is
// pre-existing and already bounded, because a slotted level holds its slot for
// as long as its child runs and so a slotted chain cannot outrun
// MaxTaskConcurrency; a purely slotted chain multiplies live memory exactly the
// same way, 145 MiB at 63 levels against the same 8 MiB quota. Inline execution
// is the factor that had nothing bounding it, because it exists precisely to
// run without a slot.
//
// Sixteen rather than eight because eight refused a shape that is not abuse: a
// binary divide-and-conquer, which nests one level per split, was refused at
// 4,096 leaves under the defaults, while sixteen carries it past 65,536 -- more
// than a step quota lets a script reach anyway. Sixteen rather than more
// because nothing measured needs it and the memory multiplier is linear in it.
const maxInlineTaskDepth = 16

type taskGroup struct {
	script               *Script
	ctx                  context.Context
	cancel               context.CancelFunc
	opts                 CallOptions
	globals              map[string]Value
	detachedGlobals      bool
	inheritedLazyGlobals *taskLazyGlobals
	// inlineDepth is how many task jobs are already stacked on the goroutine
	// that opened this scope, so a job this group runs inline lands at
	// inlineDepth+1. A job that gets a slot starts at zero instead: it runs on
	// a new goroutine with a stack of its own.
	inlineDepth int
	// callerRecursion is the call depth the execution that opened this scope
	// had left, which a job run inline continues rather than restarts. Zero
	// when that execution had no cap to share.
	callerRecursion int
	// budget is the call tree's shared pool, max the ceiling this group may
	// draw from it, and running the jobs currently on a slot. Each running job
	// holds exactly one slot and returns it when it finishes.
	budget  *taskConcurrencyBudget
	max     int
	running int
	// deferred holds jobs for a group the shared pool could not staff. They
	// run inline when something waits on them (see drainDeferred), not when
	// they are spawned: spawning must stay nonblocking, or a block that
	// performs a host action right after spawn would never reach it while the
	// task waiting on that action ran first.
	deferred []*taskJob

	tasks sync.WaitGroup

	mu       sync.Mutex
	closed   bool
	firstErr error
	// firstExhaustion separately retains the first authenticated budget
	// exhaustion any worker reported. firstErr keeps first-failure
	// reporting semantics, but an ordinary failure recorded just before a
	// concurrent worker's quota kill must not discard the kill: the parent
	// latches from here regardless of arrival order.
	firstExhaustion error

	retainedResults map[*taskHandle]Value
	jobPayloads     map[*taskJob]struct{}
}

type taskJob struct {
	functionName string
	args         []Value
	inlineArgs   [1]Value
	inlineArg    bool
	kwargs       map[string]Value
	handle       *taskHandle
}

type taskHandle struct {
	group *taskGroup
	done  chan struct{}

	value Value
	err   error
}

func newTaskGroup(exec *Execution, max int, detachRootGlobals bool) *taskGroup {
	ctx, cancel := context.WithCancel(exec.Context())
	inheritedLazyGlobals := taskLazyGlobalsFromContext(exec.Context())
	globals := exec.callOptions.Globals
	detachedGlobals := false
	if inheritedLazyGlobals != nil {
		inheritedLazyGlobals = inheritedLazyGlobals.snapshotForNestedTasks()
	} else {
		globals = taskGlobalsFromRoot(exec.root, exec.callOptions.Globals)
		if detachRootGlobals {
			globals = cloneTaskGlobals(globals)
			detachedGlobals = true
		}
	}
	// Every group in a call tree draws its workers from one pool. A group
	// validates its own max against the host limit, but nothing bounded the
	// tree: each nested task runs through Script.Call, which starts a fresh
	// group with a fresh allowance, so concurrency compounded as max^depth --
	// four levels of max:4 peaked at 144 workers against a host cap of 64,
	// each child also holding a full step and memory quota (#54). Sharing the
	// pool through the group context caps the whole tree instead.
	// The context carries the pool into the calls this group drives, but a
	// scope that opens another scope on the SAME execution never had its
	// context replaced, so a Tasks.run block that calls Tasks.run directly saw
	// no pool and started a second one, putting both scopes' workers on the
	// host at once. The groups this execution already has open are the other
	// half of the answer.
	budget := taskBudgetFromContext(exec.Context())
	if budget == nil {
		budget = exec.enclosingTaskBudget()
	}
	if budget == nil {
		budget = newTaskConcurrencyBudget(exec.hostTaskConcurrencyLimit())
	}
	ctx = contextWithTaskBudget(ctx, budget)
	// The memory chain needs no publishing here: exec.Context() already carries
	// this execution's node, so the group's context inherits it along with
	// everything else, and every job this group runs links to it. Publishing it
	// only here was the bug -- a group is not the only way a nested call is
	// made, and a capability adapter re-entering the script got a context
	// without it.
	group := &taskGroup{
		script:               taskScript(exec),
		ctx:                  ctx,
		cancel:               cancel,
		opts:                 exec.callOptions,
		globals:              globals,
		detachedGlobals:      detachedGlobals,
		inheritedLazyGlobals: inheritedLazyGlobals,
		budget:               budget,
		max:                  max,
		inlineDepth:          inlineTaskDepthFromContext(exec.Context()),
		callerRecursion:      exec.remainingRecursionBudget(),
	}
	return group
}

// taskConcurrencyBudget is the worker allotment one call tree shares. Nested
// groups draw from the same pool, so a script cannot multiply its concurrency
// by nesting Tasks calls inside task functions.
type taskConcurrencyBudget struct {
	mu        sync.Mutex
	remaining int
	// starved holds the groups that have work no worker can take. A slot
	// returned to the pool is offered to them before it sits idle, because a
	// starved group only runs its deferred jobs when something waits on them
	// -- and a parent blocked on a host signal its child produces never
	// reaches that wait.
	// starved is kept in registration order, oldest first, so a freed slot goes
	// to whoever has waited longest instead of to whichever key Go's map
	// iteration happens to yield. Selection order decides whether the tree makes
	// progress: with three slots and two nested children, handing the slot to
	// the child that is itself blocked leaves every slot waiting on the one
	// that was never started. Waiting longest is not a guarantee of unblocking
	// anything, but it is an order rather than a coin flip, and it cannot
	// starve a group indefinitely.
	starved []*taskGroup
	// starvedGen advances on every registration, so a goroutine that found
	// nothing can tell a group that registered while it looked from one that
	// was already there and had nothing to give.
	starvedGen uint64
}

func newTaskConcurrencyBudget(limit int) *taskConcurrencyBudget {
	return &taskConcurrencyBudget{remaining: max(limit, 1)}
}

// reserveOne takes a single worker from the pool, and reports false when the
// tree has spent its allowance. A group with no worker runs its jobs inline on
// the goroutine that waits for them (see enqueue and drainDeferred), so a
// starved nesting level degrades to serial execution instead of deadlocking on
// a jobs channel nothing drains -- and the pool stays a hard cap rather than
// one the deepest levels can creep past a worker at a time.
func (b *taskConcurrencyBudget) reserveOne() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.remaining < 1 {
		return false
	}
	b.remaining--
	return true
}

// markStarved records that a group has deferred work waiting for capacity.
func (b *taskConcurrencyBudget) markStarved(group *taskGroup) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// The generation advances even when the group is already listed. A group
	// stays registered after its queue drains, so a repeat registration is how
	// new work announces itself: suppressing the bump there let a finishing
	// worker sample the generation, see the stale entry with an empty queue,
	// and release its slot while the enqueue it raced with was left waiting.
	b.starvedGen++
	if slices.Contains(b.starved, group) {
		return
	}
	b.starved = append(b.starved, group)
}

// starvedGeneration reports how many registrations the pool has seen.
func (b *taskConcurrencyBudget) starvedGeneration() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.starvedGen
}

func (b *taskConcurrencyBudget) forget(group *taskGroup) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if i := slices.Index(b.starved, group); i >= 0 {
		// Cleared before the length drops, so the entry does not keep a whole
		// task group reachable from the pool for the rest of the tree's life.
		b.starved = slices.Delete(b.starved, i, i+1)
	}
}

// release returns a worker to the pool.
//
// It does not start anything. A slot that frees while another group is waiting
// is handed over by the goroutine that freed it, which takes that group's work
// and runs it rather than spawning a successor (see takeStarvedJob): starting
// one would briefly run two goroutines against one slot. Nothing is lost by
// releasing quietly, because a group that cannot reserve retries after it
// registers, so a slot returned here is one no waiting group had claimed.
func (b *taskConcurrencyBudget) release(n int) {
	if n < 1 {
		return
	}
	b.mu.Lock()
	b.remaining += n
	b.mu.Unlock()
}

// takeStarvedJob moves a job from a group waiting for capacity onto the slot
// the caller already holds, and reports the group it belongs to. The slot
// changes hands without changing count: the caller's group gives it up and the
// starved group takes it.
//
// A group whose queue has drained is left registered rather than forgotten
// here. Forgetting it raced with its own re-registration: a group can queue new
// work and register again between the moment its queue reads as empty and the
// moment it would be removed, and deleting that fresh registration leaves the
// new job waiting with capacity idle. A stale entry costs one skipped iteration;
// the group is forgotten when its scope closes.
func (b *taskConcurrencyBudget) takeStarvedJob(from *taskGroup) (*taskGroup, *taskJob) {
	b.mu.Lock()
	waiting := make([]*taskGroup, 0, len(b.starved))
	for _, group := range b.starved {
		// The caller's own queue is not a candidate here: it is consulted
		// afterwards, and letting it win would undo the point of asking other
		// groups first.
		if group == from {
			continue
		}
		waiting = append(waiting, group)
	}
	b.mu.Unlock()

	for _, group := range waiting {
		job, _ := group.takeQueuedJob()
		if job == nil {
			continue
		}
		from.mu.Lock()
		from.running--
		from.mu.Unlock()
		return group, job
	}
	return nil, nil
}

type taskSlotKey struct{}

// contextWithTaskSlot marks the calls a job drives as running on a pool slot,
// so a wait inside one of them knows it may run queued work on its goroutine.
// It propagates to nested scopes, which is right: their blocks run on the same
// slot-backed goroutine.
func contextWithTaskSlot(ctx context.Context) context.Context {
	return context.WithValue(ctx, taskSlotKey{}, true)
}

// taskSlotFromContext reports whether the waiting goroutine holds a pool slot.
// The root goroutine does not, so nothing may be run on it: a script that waits
// there is waiting for capacity to reach the group, not for permission to add
// some.
func taskSlotFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	slot, _ := ctx.Value(taskSlotKey{}).(bool)
	return slot
}

type taskInlineDepthKey struct{}

// contextWithInlineTaskDepth publishes how many task jobs are stacked on the
// goroutine this call runs on, so a scope opened inside the call knows how deep
// its own inline work would land. It rides as a context value for the same
// reason the pool does: it has to survive the cancel contexts nested calls layer
// on top, and it has to cross the Execution boundary a job's call opens.
//
// Setting the depth a context already carries returns that context unchanged,
// so the common case -- a slotted job with nothing stacked above it, publishing
// the zero it already reads as -- adds no wrapper of its own.
func contextWithInlineTaskDepth(ctx context.Context, depth int) context.Context {
	if depth == inlineTaskDepthFromContext(ctx) {
		return ctx
	}
	return context.WithValue(ctx, taskInlineDepthKey{}, depth)
}

func inlineTaskDepthFromContext(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	depth, _ := ctx.Value(taskInlineDepthKey{}).(int)
	return depth
}

type taskBudgetKey struct{}

// contextWithTaskBudget publishes the pool to everything the group drives.
// It rides as a context value rather than a wrapper type so it survives the
// cancel contexts nested calls layer on top.
func contextWithTaskBudget(ctx context.Context, budget *taskConcurrencyBudget) context.Context {
	if budget == nil {
		return ctx
	}
	return context.WithValue(ctx, taskBudgetKey{}, budget)
}

func taskBudgetFromContext(ctx context.Context) *taskConcurrencyBudget {
	if ctx == nil {
		return nil
	}
	budget, _ := ctx.Value(taskBudgetKey{}).(*taskConcurrencyBudget)
	return budget
}

// enclosingTaskBudget reports the pool of the innermost scope this execution
// still has open, if any.
func (exec *Execution) enclosingTaskBudget() *taskConcurrencyBudget {
	for i := len(exec.activeTaskGroups) - 1; i >= 0; i-- {
		if budget := exec.activeTaskGroups[i].budget; budget != nil {
			return budget
		}
	}
	return nil
}

// hostTaskConcurrencyLimit reports the ceiling a whole call tree shares.
func (exec *Execution) hostTaskConcurrencyLimit() int {
	if exec == nil || exec.engine == nil {
		return defaultMaxTaskConcurrency
	}
	return exec.engine.config.MaxTaskConcurrency
}

func taskScript(exec *Execution) *Script {
	if ctx := exec.currentModuleContext(); ctx != nil && ctx.script != nil {
		return ctx.script
	}
	return exec.script
}

func taskGlobalsFromRoot(root *Env, globals map[string]Value) map[string]Value {
	if len(globals) == 0 {
		return nil
	}
	out := make(map[string]Value, len(globals))
	for name, original := range globals {
		if val, ok := rootBindingValue(root, name); ok {
			// A still-lazy host global has not been read (let alone written) by
			// the parent, so the pristine host value is its current state. Hand
			// tasks the original source instead of forcing a materialization the
			// parent never needed; the task-side lazy machinery still deep-copies
			// it before any task code can touch it.
			if _, lazy := lazyValue(val); !lazy {
				out[name] = val
				continue
			}
		}
		out[name] = original
	}
	return out
}

func rootBindingValue(root *Env, name string) (Value, bool) {
	if root == nil {
		return Value{}, false
	}
	return root.getOwn(name)
}

func (group *taskGroup) managerValue() Value {
	return NewObject(map[string]Value{
		"spawn": NewBuiltin("tasks.spawn", group.builtinSpawn),
		"wait":  NewAutoBuiltin("tasks.wait", group.builtinWait),
	})
}

func (group *taskGroup) builtinSpawn(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("tasks.spawn does not accept blocks")
	}
	if len(args) == 0 {
		return NewNil(), fmt.Errorf("tasks.spawn requires a function name")
	}
	functionName, err := taskFunctionName("tasks.spawn", args[0])
	if err != nil {
		return NewNil(), err
	}
	handle, err := group.spawn(exec, functionName, args[1:], kwargs)
	if err != nil {
		return NewNil(), err
	}
	return handle.valueObject(), nil
}

func (group *taskGroup) builtinWait(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) != 0 {
		return NewNil(), fmt.Errorf("tasks.wait does not take positional arguments")
	}
	if len(kwargs) != 0 {
		return NewNil(), fmt.Errorf("tasks.wait does not accept keyword arguments")
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("tasks.wait does not accept blocks")
	}
	if group.isClosed() {
		return NewNil(), fmt.Errorf("task manager cannot be used after task scope exits")
	}
	if err := exec.latchGroupTaskExhaustion(group, group.wait()); err != nil {
		return NewNil(), err
	}
	return NewNil(), nil
}

func (group *taskGroup) spawn(exec *Execution, functionName string, args []Value, kwargs map[string]Value) (*taskHandle, error) {
	if group.isClosed() {
		return nil, fmt.Errorf("task manager cannot be used after task scope exits")
	}
	if err := exec.latchGroupTaskExhaustion(group, group.err()); err != nil {
		return nil, err
	}
	taskArgs, err := cloneTaskArgs("tasks.spawn", args)
	if err != nil {
		return nil, err
	}
	taskKwargs, err := cloneTaskKwargs("tasks.spawn", kwargs)
	if err != nil {
		return nil, err
	}

	return group.enqueue(exec, functionName, taskArgs, Value{}, false, taskKwargs)
}

func (group *taskGroup) spawnUnary(exec *Execution, functionName string, arg Value) (*taskHandle, error) {
	if group.isClosed() {
		return nil, fmt.Errorf("task manager cannot be used after task scope exits")
	}
	if err := exec.latchGroupTaskExhaustion(group, group.err()); err != nil {
		return nil, err
	}

	taskArg, err := cloneTaskValue("Tasks.map item", arg)
	if err != nil {
		return nil, err
	}

	return group.enqueue(exec, functionName, nil, taskArg, true, nil)
}

func (group *taskGroup) enqueue(exec *Execution, functionName string, taskArgs []Value, inlineArg Value, hasInlineArg bool, taskKwargs map[string]Value) (*taskHandle, error) {
	// The spawn entry check predates the payload clone, and a worker can
	// publish its exhaustion — or an ordinary failure — while the clone
	// runs. Re-observe before admission, so at most one clone's worth of
	// work races the publication and no job is enqueued after an observed
	// kill; a job that slips past a concurrent cancellation instead dies in
	// runJob's context check before any worker budget is spent.
	if err := exec.latchGroupTaskExhaustion(group, group.err()); err != nil {
		return nil, err
	}
	ctx := exec.Context()

	handle := &taskHandle{
		group: group,
		done:  make(chan struct{}),
		value: NewNil(),
	}
	job := &taskJob{
		functionName: functionName,
		args:         taskArgs,
		inlineArg:    hasInlineArg,
		kwargs:       taskKwargs,
		handle:       handle,
	}
	if hasInlineArg {
		job.inlineArgs[0] = inlineArg
	}

	group.retainJobPayload(job)
	if err := exec.checkMemory(); err != nil {
		group.releaseJobPayload(job)
		return nil, err
	}

	if err := group.ctx.Err(); err != nil {
		group.releaseJobPayload(job)
		if groupErr := group.err(); groupErr != nil {
			err = groupErr
		}
		handle.complete(NewNil(), err)
		return nil, exec.latchGroupTaskExhaustion(group, err)
	}
	if err := ctx.Err(); err != nil {
		group.releaseJobPayload(job)
		handle.complete(NewNil(), err)
		return nil, err
	}

	group.tasks.Add(1)
	// A group the shared pool could not staff defers its work rather than
	// starting a goroutine the pool did not allow. The job runs inline at the
	// next wait; spawning stays nonblocking either way.
	if !group.startJob(job) {
		group.mu.Lock()
		group.deferred = append(group.deferred, job)
		group.mu.Unlock()
		// Registered before the retry, so a slot released between the failed
		// reservation and this point cannot be missed: release only skips a
		// group it cannot see, and staffDeferred re-registers if it loses the
		// race for the slot again.
		group.budget.markStarved(group)
		group.staffDeferred()
	}
	return handle, nil
}

// startJob takes a slot from the shared pool and runs the job on it, reporting
// false when the pool or this group's own ceiling has nothing to give.
//
// A slot is held by a running job and returned the moment it finishes, rather
// than by a worker that outlives the work. max is a ceiling, not a count, so a
// pool of workers sized to it held capacity for tasks that were never spawned
// and kept holding it after the ones that were had finished: against a host
// limit of 2, a Tasks.run(max: 2) that spawned one task starved every nested
// group for its whole lifetime, and a group that ran two tasks concurrently
// went on holding both slots once they were done.
//
// Tying the slot to the job instead means a group holds exactly the
// concurrency it is using at that instant, and the pool is a hard cap because
// nothing runs script code without a slot.
func (group *taskGroup) startJob(job *taskJob) bool {
	group.mu.Lock()
	defer group.mu.Unlock()
	if group.closed || group.running >= group.max || !group.budget.reserveOne() {
		return false
	}
	group.running++
	go group.runSlottedJob(job)
	return true
}

// staffDeferred starts queued work on capacity the pool still has. It runs
// right after a group registers as starved, closing the window where a slot was
// returned between the failed reservation and the registration. The goroutine
// it starts occupies a slot the pool actually had, so it adds no concurrency
// beyond the cap.
func (group *taskGroup) staffDeferred() {
	for {
		group.mu.Lock()
		if group.closed || len(group.deferred) == 0 || group.running >= group.max {
			group.mu.Unlock()
			return
		}
		if !group.budget.reserveOne() {
			group.mu.Unlock()
			// Still holding work, so stay registered for the next release.
			group.budget.markStarved(group)
			return
		}
		job := group.deferred[0]
		group.deferred = group.deferred[1:]
		group.running++
		go group.runSlottedJob(job)
		group.mu.Unlock()
	}
}

// runSlottedJob runs queued work on one slot until there is none left, then
// returns the slot. Deferred work run inline by a waiter goes through
// runInlineJob instead: it never took a slot, because the goroutine running it
// is one the pool already counted.
func (group *taskGroup) runSlottedJob(job *taskJob) {
	for job != nil {
		// Zero, not the depth the group inherited: this goroutine is new and its
		// Go stack starts empty, whatever was stacked on the goroutine that
		// queued the work. Successive jobs here are a loop, not a nesting.
		group.runJob(job, 0)
		group, job = group.nextQueuedJob()
	}
}

// runInlineJob runs a job on the goroutine that is waiting for it, one level
// deeper on that goroutine's Go stack than the job that submitted it.
//
// Past maxInlineTaskDepth the job is failed rather than run. Refusing to run it
// at all is not an option -- the waiter would never be released and the tree
// would deadlock, which is the whole reason inline execution exists -- so the
// limit is reported through the handle, and travels out as an ordinary task
// failure that cancels the group and surfaces to the script.
//
// A group that is already dead is handed to runJob regardless of depth, which
// reports the cancellation and runs nothing. The depth is why this job may not
// run here, but it is not why the group is finished, and a host timeout that
// happened to land on a deep level should not be reported as a nesting limit.
func (group *taskGroup) runInlineJob(job *taskJob) {
	depth := group.inlineDepth + 1
	if depth > maxInlineTaskDepth && group.ctx.Err() == nil {
		group.failJob(job, guardLimitErrorf(
			"task nesting exceeds the inline depth limit %d; the concurrency budget is spent, so this task would run on its caller's stack",
			maxInlineTaskDepth))
		return
	}
	group.runJob(job, depth)
}

// failJob completes a job that never ran, with the group bookkeeping and the
// failure wrapping runJob would have given it.
func (group *taskGroup) failJob(job *taskJob, err error) {
	defer group.tasks.Done()
	defer group.releaseJobPayload(job)

	taskErr := fmt.Errorf("task %s failed: %w", job.functionName, err)
	group.recordErr(taskErr)
	job.handle.complete(NewNil(), taskErr)
}

// takeQueuedJob pops one queued job for a slot that is being handed to this
// group, and reports whether the queue is now empty. It raises running because
// the incoming slot is this group's for the duration of that job.
func (group *taskGroup) takeQueuedJob() (*taskJob, bool) {
	group.mu.Lock()
	defer group.mu.Unlock()
	if len(group.deferred) == 0 || group.running >= group.max {
		return nil, len(group.deferred) == 0
	}
	job := group.deferred[0]
	group.deferred = group.deferred[1:]
	group.running++
	return job, len(group.deferred) == 0
}

// nextQueuedJob finds the next job for the slot the caller holds: a group
// waiting for capacity first, then this group's own queue. It returns nil once
// there is nothing left, having given the slot back to the pool.
//
// Work moves to the slot rather than a goroutine being started for it. The
// finishing goroutine has not unwound yet, so starting a successor from its
// tail would run two goroutines against one slot, which is one more script at
// once than the pool allowed.
//
// A group waiting for capacity comes first because it may be the only thing
// that can unblock the rest. Its work is queued precisely because nothing can
// run it, while this group's own queue will be served by any of its slots that
// frees. Preferring the local queue deadlocked the shape where a running task
// waits on a nested child it cannot reach: the freed slot started a sibling
// that waited on the same child, and the child stayed queued behind both.
func (group *taskGroup) nextQueuedJob() (*taskGroup, *taskJob) {
	for {
		// Sampled before looking, so the check after releasing distinguishes a
		// group that registered while this goroutine searched from one that was
		// already registered with nothing to give. Groups whose queues have
		// drained stay registered, so the set being non-empty proves nothing
		// and retrying on that alone would spin.
		gen := group.budget.starvedGeneration()
		if owner, job := group.budget.takeStarvedJob(group); job != nil {
			return owner, job
		}

		group.mu.Lock()
		// Queued work still runs after close: closing stops new spawns, and
		// work already admitted has to run or the wait for it never returns.
		if len(group.deferred) > 0 && group.running <= group.max {
			job := group.deferred[0]
			group.deferred = group.deferred[1:]
			group.mu.Unlock()
			return group, job
		}
		group.running--
		group.mu.Unlock()
		group.budget.release(1)

		// A group can register as starved between the snapshot above and this
		// release, and its own retry fails while this slot is still held, so
		// letting the slot go quiet there would leave it waiting with nothing
		// to wake it. Re-taking the slot and looking again closes that window;
		// losing the race for it is fine, because whoever won is doing this.
		if group.budget.starvedGeneration() == gen || !group.budget.reserveOne() {
			return nil, nil
		}
		group.mu.Lock()
		group.running++
		group.mu.Unlock()
	}
}

// runJob runs one job's call. inlineDepth is how many jobs will be stacked on
// the running goroutine once this one starts: zero on a slot, and the caller's
// depth plus one when a waiter runs the job on its own goroutine.
func (group *taskGroup) runJob(job *taskJob, inlineDepth int) {
	defer group.tasks.Done()
	defer group.releaseJobPayload(job)

	if err := group.ctx.Err(); err != nil {
		group.recordErr(err)
		job.handle.complete(NewNil(), err)
		return
	}

	// Both are published even when they are zero, because group.ctx carries the
	// depth and budget of the goroutine that opened this scope: a slotted job
	// that did not clear them would hand its own nested scopes figures
	// belonging to a stack it is not running on. A slotted job clears them
	// precisely because its goroutine starts with an empty stack, so it is
	// entitled to the host's whole limit however deep its submitter was.
	ctx := contextWithInlineTaskDepth(contextWithTaskSlot(group.ctx), inlineDepth)
	inheritedRecursion := 0
	if inlineDepth > 0 {
		inheritedRecursion = group.callerRecursion
	}
	ctx = contextWithRecursionBudget(ctx, inheritedRecursion)
	opts := group.callOptionsForJob(job)
	var workerExhaustion error
	result, err := group.script.callWithLazyTaskGlobals(ctx, job.functionName, job.callArgs(), opts, group.lazyGlobalsForJob(), &workerExhaustion)
	if workerExhaustion != nil {
		workerExhaustion = fmt.Errorf("task %s failed: %w", job.functionName, workerExhaustion)
	}
	group.recordExhaustion(workerExhaustion)
	if err != nil {
		taskErr := fmt.Errorf("task %s failed: %w", job.functionName, err)
		group.recordErr(taskErr)
		job.handle.complete(NewNil(), taskErr)
		return
	}
	if err := group.ctx.Err(); err != nil {
		group.recordErr(err)
		job.handle.complete(NewNil(), err)
		return
	}

	result, err = cloneTaskResult(job.functionName, result)
	if err != nil {
		taskErr := fmt.Errorf("task %s failed: %w", job.functionName, err)
		group.recordErr(taskErr)
		job.handle.complete(NewNil(), taskErr)
		return
	}
	job.handle.complete(result, nil)
}

func (group *taskGroup) callOptionsForJob(job *taskJob) CallOptions {
	opts := group.opts
	opts.Globals = nil
	opts.Keywords = job.kwargs
	return opts
}

func (job *taskJob) callArgs() []Value {
	if job.inlineArg {
		return job.inlineArgs[:1]
	}
	return job.args
}

func (group *taskGroup) lazyGlobalsForJob() *taskLazyGlobals {
	if group.inheritedLazyGlobals != nil {
		return group.inheritedLazyGlobals.fork()
	}
	return newTaskLazyGlobals(group.globals, false, group.detachedGlobals)
}

// drainDeferred runs the jobs a starved group could not put on a slot, on the
// goroutine that is waiting for them, and stops as soon as done is closed. A
// nil done drains everything, which is what closing the scope wants.
//
// Stopping at done matters because a waiter is only entitled to the work it is
// waiting for: running the rest would trap a parent inside an unrelated job
// that is itself waiting on a host action the parent performs once this wait
// returns.
//
// drainDeferred runs queued work on the goroutine that is waiting for it.
//
// With a target it runs that handle's own job and nothing else. Popping the
// queue head instead ran an unrelated job on the waiter's behalf, which
// deadlocks when the awaited handle is behind one that is itself waiting for
// something the waiter only does once it has its result. With no target it
// drains the queue, which is what closing the scope wants.
//
// A group under its own max may run work here even when it has a job on a slot:
// the waiter's goroutine belongs to a slot already counted and is blocked, so
// borrowing it adds no concurrency, while refusing would deadlock a parent
// whose running child waits on something that parent only does once the
// deferred handle is in hand. Above the max it must refuse, which is what the
// group's own limit means.
//
// Only a waiter that is itself on a slot may do this. The root goroutine holds
// no slot, so running a job there would put one more script on the host than
// the pool allows, which is the whole point of the pool; it waits for capacity
// to reach the group instead (see taskSlotFromContext).
func (group *taskGroup) drainDeferred(target *taskHandle) {
	for {
		if target != nil {
			select {
			case <-target.done:
				return
			default:
			}
		}
		group.mu.Lock()
		job, ok := group.takeDeferredLocked(target)
		if !ok {
			group.mu.Unlock()
			return
		}
		// Counted while it runs, even though it is on a borrowed goroutine: a
		// slot released elsewhere would otherwise see room in this group and
		// hand it a second job to run beside this one, past the max the group
		// was given.
		group.running++
		group.mu.Unlock()

		group.runInlineJob(job)

		group.mu.Lock()
		group.running--
		group.mu.Unlock()
	}
}

// takeDeferredLocked removes the job a waiter may run: the target's own, or the
// head of the queue when there is no target. It reports false when the group is
// at its max, when the queue is empty, or when the target is not queued at all
// because something else already has it.
func (group *taskGroup) takeDeferredLocked(target *taskHandle) (*taskJob, bool) {
	if group.running >= group.max || len(group.deferred) == 0 {
		return nil, false
	}
	index := 0
	if target != nil {
		index = -1
		for i, job := range group.deferred {
			if job.handle == target {
				index = i
				break
			}
		}
		if index < 0 {
			return nil, false
		}
	}
	job := group.deferred[index]
	group.deferred = append(group.deferred[:index], group.deferred[index+1:]...)
	return job, true
}

// drainQueueIfSlotBacked runs the whole queue on the waiting goroutine, but
// only when that goroutine holds a slot. On the root goroutine it waits
// instead: the queue runs when capacity reaches the group, and running it here
// would put one more script on the host than the pool allows.
func (group *taskGroup) drainQueueIfSlotBacked() {
	if taskSlotFromContext(group.ctx) {
		group.drainDeferred(nil)
	}
}

func (group *taskGroup) wait() error {
	group.drainQueueIfSlotBacked()
	group.tasks.Wait()
	return group.err()
}

func (group *taskGroup) closeAndWait() error {
	group.drainQueueIfSlotBacked()
	group.mu.Lock()
	group.closed = true
	group.mu.Unlock()

	group.tasks.Wait()
	group.cancel()
	if group.budget != nil {
		group.budget.forget(group)
	}
	return group.err()
}

func (group *taskGroup) isClosed() bool {
	group.mu.Lock()
	defer group.mu.Unlock()
	return group.closed
}

func (group *taskGroup) recordErr(err error) {
	if err == nil {
		return
	}
	group.mu.Lock()
	if group.firstErr == nil {
		group.firstErr = err
		group.cancel()
	}
	group.mu.Unlock()
}

// recordExhaustion retains the first worker exhaustion reported through the
// trusted out-of-band channel (the worker execution's own latch, observed by
// runJob when the worker call returns). Error values are never inspected for
// credentials: a stateful adapter inside a worker could replay a stale one.
func (group *taskGroup) recordExhaustion(exhausted error) {
	if exhausted == nil {
		return
	}
	group.mu.Lock()
	if group.firstExhaustion == nil {
		group.firstExhaustion = exhausted
	}
	group.mu.Unlock()
}

// exhaustion returns the first authenticated worker exhaustion the group
// recorded, independent of which failure won firstErr.
func (group *taskGroup) exhaustion() error {
	group.mu.Lock()
	defer group.mu.Unlock()
	return group.firstExhaustion
}

func (group *taskGroup) err() error {
	group.mu.Lock()
	defer group.mu.Unlock()
	return group.firstErr
}

func (group *taskGroup) retainResult(handle *taskHandle, result Value) {
	group.mu.Lock()
	if group.retainedResults == nil {
		group.retainedResults = make(map[*taskHandle]Value)
	}
	group.retainedResults[handle] = result
	group.mu.Unlock()
}

func (group *taskGroup) retainJobPayload(job *taskJob) {
	group.mu.Lock()
	if group.jobPayloads == nil {
		group.jobPayloads = make(map[*taskJob]struct{})
	}
	group.jobPayloads[job] = struct{}{}
	group.mu.Unlock()
}

func (group *taskGroup) releaseJobPayload(job *taskJob) {
	group.mu.Lock()
	delete(group.jobPayloads, job)
	group.mu.Unlock()
}

func (group *taskGroup) jobPayloadMemory(est *memoryEstimator) int {
	group.mu.Lock()
	defer group.mu.Unlock()

	total := 0
	for job := range group.jobPayloads {
		total += job.argsMemory(est)
		total += est.hash(job.kwargs)
	}
	return total
}

func (job *taskJob) argsMemory(est *memoryEstimator) int {
	if job.inlineArg {
		return saturatingAdd(sliceStructuralBytes(job.inlineArgs[:1]), est.value(job.inlineArgs[0]))
	}
	return est.slice(job.args)
}

func (group *taskGroup) retainedResultMemory(est *memoryEstimator) int {
	group.mu.Lock()
	defer group.mu.Unlock()

	total := 0
	for _, result := range group.retainedResults {
		total += est.value(result)
	}
	return total
}

func (group *taskGroup) retainedSnapshotMemory(est *memoryEstimator) int {
	total := 0
	if group.detachedGlobals && len(group.globals) > 0 {
		total += est.hash(group.globals)
	}
	if group.inheritedLazyGlobals != nil {
		total += group.inheritedLazyGlobals.retainedSourceMemory(est)
	}
	return total
}

func (group *taskGroup) releaseRetainedResults() {
	group.mu.Lock()
	for handle := range group.retainedResults {
		handle.value = NewNil()
	}
	group.retainedResults = nil
	group.mu.Unlock()
}

func (handle *taskHandle) valueObject() Value {
	return NewObject(map[string]Value{
		"value": NewAutoBuiltin("task.value", handle.builtinValue),
	})
}

func (handle *taskHandle) builtinValue(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) != 0 {
		return NewNil(), fmt.Errorf("task.value does not take positional arguments")
	}
	if len(kwargs) != 0 {
		return NewNil(), fmt.Errorf("task.value does not accept keyword arguments")
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("task.value does not accept blocks")
	}
	if handle.group.isClosed() {
		return NewNil(), fmt.Errorf("task handle cannot be used after task scope exits")
	}
	result, err := handle.wait(exec.Context())
	return result, exec.latchGroupTaskExhaustion(handle.group, handle.substituteRootCause(err))
}

// substituteRootCause replaces a handle's own cancellation with the failure
// that caused it.
//
// When one task fails its siblings are canceled, correctly and promptly, and
// each of those reports "context canceled" -- the mechanism rather than the
// reason. Which of the two an author sees depended only on the order the
// handles happened to be read, which is arbitrary from where they sit: reading
// the canceled sibling first reported the cancellation and lost the real
// cause entirely.
//
// Only a pure cancellation is substituted, and only when the group recorded a
// non-cancellation failure. A group canceled from outside (a caller's context
// ending) still reports the cancellation, because there is no other cause.
func (handle *taskHandle) substituteRootCause(err error) error {
	if err == nil || !errors.Is(err, context.Canceled) {
		return err
	}
	rootCause := handle.group.err()
	if rootCause == nil || errors.Is(rootCause, context.Canceled) {
		return err
	}
	return rootCause
}

func (handle *taskHandle) wait(ctx context.Context) (Value, error) {
	if taskSlotFromContext(ctx) {
		handle.group.drainDeferred(handle)
	}
	select {
	case <-handle.done:
		return handle.result()
	case <-ctx.Done():
		return NewNil(), ctx.Err()
	}
}

func (handle *taskHandle) result() (Value, error) {
	handle.group.drainDeferred(handle)
	<-handle.done
	return handle.value, handle.err
}

func (handle *taskHandle) complete(result Value, err error) {
	if err == nil {
		handle.group.retainResult(handle, result)
	}
	handle.value = result
	handle.err = err
	close(handle.done)
}

func taskConcurrency(exec *Execution, method string, kwargs map[string]Value, allowed map[string]struct{}) (int, error) {
	for key := range kwargs {
		if _, ok := allowed[key]; !ok {
			return 0, fmt.Errorf("%s unknown keyword argument %s", method, key)
		}
	}

	max := exec.engine.config.DefaultTaskConcurrency
	if rawMax, ok := kwargs["max"]; ok {
		if rawMax.Kind() != KindInt {
			return 0, fmt.Errorf("%s max must be an integer", method)
		}
		requested, compact := rawMax.CompactInt()
		if !compact {
			return 0, fmt.Errorf("%s max must fit in a 64-bit integer", method)
		}
		if requested < 1 {
			return 0, fmt.Errorf("%s max must be at least 1", method)
		}
		if requested > int64(exec.engine.config.MaxTaskConcurrency) {
			return 0, fmt.Errorf("%s max %d exceeds host maximum %d", method, requested, exec.engine.config.MaxTaskConcurrency)
		}
		max = int(requested)
	}
	return max, nil
}

func taskRequiredFunctionKeyword(method string, kwargs map[string]Value) (string, error) {
	rawFunction, ok := kwargs["with"]
	if !ok {
		return "", fmt.Errorf("%s requires with:", method)
	}
	return taskFunctionName(method+" with", rawFunction)
}

func taskFunctionName(method string, val Value) (string, error) {
	switch val.Kind() {
	case KindString, KindSymbol:
		name := val.String()
		if name == "" {
			return "", fmt.Errorf("%s function name cannot be empty", method)
		}
		return name, nil
	default:
		return "", fmt.Errorf("%s function name must be a symbol or string", method)
	}
}

func cloneTaskArgs(method string, args []Value) ([]Value, error) {
	if len(args) == 0 {
		return nil, nil
	}
	out := make([]Value, len(args))
	for i, arg := range args {
		label := fmt.Sprintf("%s argument %d", method, i+1)
		cloned, err := cloneTaskValue(label, arg)
		if err != nil {
			return nil, err
		}
		out[i] = cloned
	}
	return out, nil
}

func cloneTaskKwargs(method string, kwargs map[string]Value) (map[string]Value, error) {
	if len(kwargs) == 0 {
		return nil, nil
	}
	out := make(map[string]Value, len(kwargs))
	for name, val := range kwargs {
		label := fmt.Sprintf("%s keyword %s", method, name)
		cloned, err := cloneTaskValue(label, val)
		if err != nil {
			return nil, err
		}
		out[name] = cloned
	}
	return out, nil
}

func cloneTaskResult(functionName string, result Value) (Value, error) {
	if taskImmutableDataValue(result) {
		return result, nil
	}
	return cloneTaskValue(fmt.Sprintf("task %s return value", functionName), result)
}

func taskImmutableDataValue(val Value) bool {
	switch val.Kind() {
	case KindNil, KindBool, KindInt, KindFloat, KindString, KindMoney, KindDuration, KindTime, KindSymbol, KindRange, KindRegex:
		return true
	default:
		return false
	}
}

func cloneTaskGlobals(globals map[string]Value) map[string]Value {
	if len(globals) == 0 {
		return nil
	}
	cloner := newTaskGlobalCloner()
	out := make(map[string]Value, len(globals))
	for name, val := range globals {
		out[name] = cloner.clone(val)
	}
	return out
}

type taskLazyGlobals struct {
	values          map[string]Value
	detachedValues  bool
	strictValidated bool
	cloner          *taskGlobalCloner
	rebinder        *callFunctionRebinder
	root            *Env
	clones          map[string]Value
}

func newTaskLazyGlobals(values map[string]Value, strictValidated, detachedValues bool) *taskLazyGlobals {
	if len(values) == 0 {
		return nil
	}
	return &taskLazyGlobals{
		values:          values,
		detachedValues:  detachedValues,
		strictValidated: strictValidated,
		cloner:          newTaskGlobalCloner(),
		clones:          make(map[string]Value),
	}
}

func (globals *taskLazyGlobals) len() int {
	if globals == nil {
		return 0
	}
	return len(globals.values)
}

func (globals *taskLazyGlobals) fork() *taskLazyGlobals {
	if globals == nil {
		return nil
	}
	return newTaskLazyGlobals(globals.values, globals.strictValidated, globals.detachedValues)
}

func (globals *taskLazyGlobals) snapshotForNestedTasks() *taskLazyGlobals {
	if globals == nil {
		return nil
	}
	values, detached := globals.valuesForFork()
	return newTaskLazyGlobals(values, globals.strictValidated && !detached, globals.detachedValues || detached)
}

func (globals *taskLazyGlobals) materialize(name string) Value {
	if clone, ok := globals.clones[name]; ok {
		return clone
	}
	source := globals.values[name]
	var cloned Value
	if globals.rebinder != nil {
		// Always take the full rebind walk here, never the inbound data-only
		// fast path. Task-global sources can be the parent call's materialized
		// clones, which parent script code may mutate concurrently with a
		// spawned task (tasks.spawn does not block the parent), so a
		// call-entry data-only verdict for these sources could go stale before
		// this materialization runs. The rebinder walk classifies every value
		// as it visits it, so a block or capability inserted after entry is
		// still re-rooted and revoked correctly.
		cloned = globals.rebinder.rebindValue(source)
	} else {
		cloned = globals.cloner.clone(source)
	}
	globals.clones[name] = cloned
	return cloned
}

func (globals *taskLazyGlobals) ensureStrictValidated() error {
	if globals.strictValidated {
		return nil
	}
	if err := validateStrictGlobals(globals.valuesForValidation()); err != nil {
		return err
	}
	globals.strictValidated = true
	return nil
}

func (globals *taskLazyGlobals) valuesForValidation() map[string]Value {
	if len(globals.clones) == 0 {
		return globals.values
	}
	out := make(map[string]Value, len(globals.values))
	for name, val := range globals.values {
		if clone, ok := globals.clones[name]; ok {
			out[name] = clone
			continue
		}
		out[name] = val
	}
	return out
}

func (globals *taskLazyGlobals) retainedCloneMemory(est *memoryEstimator) int {
	if globals == nil || len(globals.clones) == 0 {
		return 0
	}
	return est.hash(globals.clones)
}

func (globals *taskLazyGlobals) retainedSourceMemory(est *memoryEstimator) int {
	if globals == nil || !globals.detachedValues || len(globals.values) == 0 {
		return 0
	}
	return est.hash(globals.values)
}

func (globals *taskLazyGlobals) valuesForFork() (map[string]Value, bool) {
	if len(globals.clones) == 0 && !globals.hasCurrentBindings() {
		return globals.values, false
	}
	cloner := newTaskGlobalCloner()
	out := make(map[string]Value, len(globals.values))
	for name := range globals.values {
		out[name] = cloner.clone(globals.currentValueForFork(name))
	}
	return out, true
}

func (globals *taskLazyGlobals) hasCurrentBindings() bool {
	if globals.root == nil {
		return false
	}
	for name := range globals.values {
		if val, ok := globals.rootValue(name); ok {
			if _, lazy := lazyValue(val); !lazy {
				if globals.isUnchangedEagerEnum(name, val) {
					continue
				}
				return true
			}
		}
	}
	return false
}

func (globals *taskLazyGlobals) isUnchangedEagerEnum(name string, val Value) bool {
	source, ok := globals.values[name]
	if !ok || source.Kind() != KindEnum || val.Kind() != KindEnum {
		return false
	}
	return enumDefsEqual(valueEnum(source), valueEnum(val))
}

func (globals *taskLazyGlobals) currentValueForFork(name string) Value {
	if val, ok := globals.rootValue(name); ok {
		if _, lazy := lazyValue(val); !lazy {
			return val
		}
	}
	return globals.materialize(name)
}

func (globals *taskLazyGlobals) rootValue(name string) (Value, bool) {
	return rootBindingValue(globals.root, name)
}

type taskLazyGlobalBinding struct {
	globals *taskLazyGlobals
	name    string
}

func (binding taskLazyGlobalBinding) materialize() Value {
	return binding.globals.materialize(binding.name)
}

type taskLazyGlobalsContext struct {
	context.Context
	globals *taskLazyGlobals
}

func contextWithTaskLazyGlobals(ctx context.Context, globals *taskLazyGlobals) context.Context {
	if globals == nil {
		return ctx
	}
	return taskLazyGlobalsContext{Context: ctx, globals: globals}
}

func taskLazyGlobalsFromContext(ctx context.Context) *taskLazyGlobals {
	if ctx == nil {
		return nil
	}
	taskCtx, ok := ctx.(taskLazyGlobalsContext)
	if !ok {
		return nil
	}
	return taskCtx.globals
}

type taskGlobalCloner struct {
	// seenArrays is keyed on the source array's wrapper identity so aliases of
	// one mutable array clone to one shared object while distinct arrays
	// (including independent empties) clone to distinct objects.
	seenArrays    map[uintptr]Value
	seenMaps      map[uintptr]map[string]Value
	seenInstances map[*Instance]Value
}

func newTaskGlobalCloner() *taskGlobalCloner {
	return &taskGlobalCloner{
		seenArrays:    make(map[uintptr]Value),
		seenMaps:      make(map[uintptr]map[string]Value),
		seenInstances: make(map[*Instance]Value),
	}
}

func (cloner *taskGlobalCloner) clone(val Value) Value {
	switch val.Kind() {
	case KindArray:
		items := val.Array()
		id := arrayIdentity(val)
		if clone, seen := cloner.seenArrays[id]; seen {
			return clone
		}
		clonedItems := make([]Value, len(items))
		clonedArray := NewArray(clonedItems)
		cloner.seenArrays[id] = clonedArray
		for i, item := range items {
			clonedItems[i] = cloner.clone(item)
		}
		return clonedArray
	case KindHash:
		entries := val.Hash()
		ptr := reflect.ValueOf(entries).Pointer()
		if clone, seen := cloner.seenMaps[ptr]; seen {
			return cloner.rebuildHash(val, clone)
		}
		clonedEntries := make(map[string]Value, len(entries))
		cloner.seenMaps[ptr] = clonedEntries
		for key, item := range entries {
			clonedEntries[key] = cloner.clone(item)
		}
		return cloner.rebuildHash(val, clonedEntries)
	case KindObject:
		entries := val.Hash()
		ptr := reflect.ValueOf(entries).Pointer()
		if clone, seen := cloner.seenMaps[ptr]; seen {
			return retagClonedObject(val, clone)
		}
		clonedEntries := make(map[string]Value, len(entries))
		cloner.seenMaps[ptr] = clonedEntries
		for key, item := range entries {
			clonedEntries[key] = cloner.clone(item)
		}
		return retagClonedObject(val, clonedEntries)
	case KindInstance:
		inst := valueInstance(val)
		if inst == nil {
			return val
		}
		if clone, seen := cloner.seenInstances[inst]; seen {
			return clone
		}
		clonedIvars := make(map[string]Value, len(inst.Ivars))
		cloned := NewInstance(&Instance{Class: inst.Class, Ivars: clonedIvars})
		cloner.seenInstances[inst] = cloned
		for name, item := range inst.Ivars {
			clonedIvars[name] = cloner.clone(item)
		}
		return cloned
	default:
		// A KindBlock default proc is left as-is. Task globals are materialized per
		// worker through the inbound rebinder (see materialize), which re-roots a
		// same-script proc's captured environment onto the worker's own call before
		// the proc ever runs, so its missing-key lookup reads the worker's globals,
		// capabilities, and per-call function clones rather than the parent's.
		// A foreign proc (from another script) keeps its own script's environment
		// under both the rebinder and this cloner, matching cross-script
		// containment. Cloning the captured environment here would be redundant
		// with the re-rooting and could not produce a meaningful isolated copy.
		return val
	}
}

// rebuildHash wraps cloned entries in a hash carrying the cloned Ruby-style
// default metadata of src. A hash's default value and default proc are reachable
// state inherited by a task, so they must be cloned like entries rather than
// dropped, which would make a missing-key lookup in the task return nil instead
// of the configured default. A hash with no default produces a plain hash.
func (cloner *taskGlobalCloner) rebuildHash(src Value, clonedEntries map[string]Value) Value {
	defaultValue := hashDefaultValue(src)
	defaultProc := hashDefaultProc(src)
	if defaultValue.IsNil() && defaultProc.IsNil() {
		return NewHash(clonedEntries)
	}
	if !defaultValue.IsNil() {
		defaultValue = cloner.clone(defaultValue)
	}
	if !defaultProc.IsNil() {
		defaultProc = cloner.clone(defaultProc)
	}
	return NewHashWithDefault(clonedEntries, defaultValue, defaultProc)
}

func cloneTaskValue(label string, val Value) (Value, error) {
	if taskImmutableDataValue(val) {
		return val, nil
	}
	if err := validateCapabilityDataOnlyValue(label, val); err != nil {
		return NewNil(), err
	}
	return deepCloneValueForContainment(val), nil
}

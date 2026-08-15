package runtime

import (
	"context"
	"fmt"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// taskDepthMemorySample is what one nesting level reports about itself. The
// goroutine and stack counts are not incidental: they are what prove which
// nesting shape a test actually exercised, so an assertion about depth cannot
// pass because the runtime quietly stopped nesting the way the test intended.
type taskDepthMemorySample struct {
	goroutine       int64
	stackExecutions int
}

// taskDepthMemoryProbe is a capability the script calls once per nesting level.
// It returns whether to nest again, so a regression is bounded by the probe
// rather than by the host running out of memory.
type taskDepthMemoryProbe struct {
	mu      *sync.Mutex
	guard   int64
	deepest *int64
	samples map[int64]taskDepthMemorySample
}

func (p taskDepthMemoryProbe) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"probe": NewObject(map[string]Value{
			"nest_again": NewBuiltin("probe.nest_again", func(_ *Execution, _ Value, args []Value, _ map[string]Value, _ Value) (Value, error) {
				depth := args[0].Int()
				sample := taskDepthMemorySample{
					goroutine:       taskProbeGoroutineID(),
					stackExecutions: taskProbeStackExecutions(),
				}
				p.mu.Lock()
				p.samples[depth] = sample
				if depth > *p.deepest {
					*p.deepest = depth
				}
				p.mu.Unlock()
				return NewBool(depth < p.guard), nil
			}),
		}),
	}, nil
}

// taskProbeGoroutineID parses the calling goroutine's id from its stack header.
// There is no supported accessor and the id is only compared for equality, to
// tell a slotted chain (a goroutine per level) from an inline one.
func taskProbeGoroutineID() int64 {
	var buf [64]byte
	n := goruntime.Stack(buf[:], false)
	header := strings.TrimPrefix(string(buf[:n]), "goroutine ")
	if idx := strings.IndexByte(header, ' '); idx >= 0 {
		header = header[:idx]
	}
	id, _ := strconv.ParseInt(header, 10, 64)
	return id
}

// taskProbeStackExecutions counts nested Executions on the calling goroutine by
// counting callWithLazyTaskGlobals frames, the same way the inline depth probe
// does. One means nothing below this level ran inline.
func taskProbeStackExecutions() int {
	pcs := make([]uintptr, 4096)
	for {
		n := goruntime.Callers(0, pcs)
		if n == len(pcs) {
			pcs = make([]uintptr, 2*len(pcs))
			continue
		}
		count := 0
		frames := goruntime.CallersFrames(pcs[:n])
		for {
			frame, more := frames.Next()
			if strings.Contains(frame.Function, "callWithLazyTaskGlobals") {
				count++
			}
			if !more {
				break
			}
		}
		return count
	}
}

// taskDepthMemoryScript allocates a payload per nesting level and holds it live
// across the nested call, so the chain's live set grows by one payload per
// level. The payload is built by doubling because the language has no string
// repeat operator, and built rather than embedded so each level holds a
// distinct allocation rather than a shared constant.
const taskDepthMemoryScript = `def payload(k)
  s = "abcdefghabcdefgh"
  i = 0
  while i < k
    s = s + s
    i = i + 1
  end
  s
end

def step(n)
  buf = payload(%d)
  if probe.nest_again(n)
    Tasks.map([n + 1], max: 1, with: :step)
  end
  buf.size
end

def run()
  Tasks.map([0], max: 1, with: :step)
  "done"
end`

// runTaskDepthMemoryProbe drives the nesting script and reports the deepest
// level reached with every level's sample.
func runTaskDepthMemoryProbe(t *testing.T, cfg Config, doublings int, guard int64) (int64, map[int64]taskDepthMemorySample, error) {
	t.Helper()

	script := compileScriptWithConfig(t, cfg, fmt.Sprintf(taskDepthMemoryScript, doublings))
	samples := map[int64]taskDepthMemorySample{}
	var deepest int64
	probe := taskDepthMemoryProbe{mu: &sync.Mutex{}, guard: guard, deepest: &deepest, samples: samples}

	_, err := script.Call(context.Background(), "run", nil, callOptionsWithCapabilities(probe))
	return deepest, samples, err
}

// summarizeTaskDepthShape reports the distinct goroutines the chain ran on and
// the most nested Executions found on any single one.
func summarizeTaskDepthShape(samples map[int64]taskDepthMemorySample, deepest int64) (goroutines, maxOnOneStack int) {
	seen := map[int64]bool{}
	for depth := int64(0); depth <= deepest; depth++ {
		s, ok := samples[depth]
		if !ok {
			continue
		}
		seen[s.goroutine] = true
		if s.stackExecutions > maxOnOneStack {
			maxOnOneStack = s.stackExecutions
		}
	}
	return len(seen), maxOnOneStack
}

const (
	// taskDepthQuota is the per-execution memory quota these tests configure.
	taskDepthQuota = 8 << 20
	// taskDepthDoublings builds a 2 MiB payload from a 16-byte seed, so four
	// levels already hold more than the quota between them.
	taskDepthDoublings = 17
	taskDepthPayload   = 2 << 20
	// taskDepthMaxLevels is the deepest a chain holding taskDepthPayload per
	// level can honestly reach under taskDepthQuota, with a level of slack for
	// the estimator's own overhead.
	taskDepthMaxLevels = taskDepthQuota/taskDepthPayload + 1
)

// TestSlottedTaskNestingCannotMultiplyTheMemoryQuota pins that the host's
// memory quota bounds a chain of nested tasks rather than each level of it.
//
// Every nested level runs on an Execution of its own, and each Execution read
// MemoryQuotaBytes fresh from the engine config, so the sandbox's live memory
// multiplied with nesting depth while every individual level looked permitted.
// Measured before the fix: 63 levels each holding 2 MiB reached the probe's
// guard with no error at all, 130 MiB live against an 8 MiB quota -- 16x what
// the host configured, refused by nothing.
//
// The pool is what bounds this shape's length, so it is bounded but wildly
// larger than the configured quota. Depth is the axis: a level holds its worker
// slot for as long as its child runs, so a slotted chain cannot outrun
// MaxTaskConcurrency, and the multiplier is the pool times the quota.
func TestSlottedTaskNestingCannotMultiplyTheMemoryQuota(t *testing.T) {
	t.Parallel()

	const (
		guard   = 63
		workers = 64
	)

	deepest, samples, err := runTaskDepthMemoryProbe(t,
		Config{MaxTaskConcurrency: workers, MemoryQuotaBytes: taskDepthQuota, StepQuota: Unlimited},
		taskDepthDoublings, guard)

	goroutines, maxOnOneStack := summarizeTaskDepthShape(samples, deepest)

	// The conditions that make the defect possible, asserted so the test cannot
	// pass because nesting stopped for some unrelated reason. Without these, a
	// runtime that refused to nest at all would satisfy every check below.
	if deepest < 2 {
		t.Fatalf("only reached depth %d, too shallow to have nested at all", deepest)
	}
	if maxOnOneStack != 1 {
		t.Fatalf("max %d nested Executions on one goroutine, want 1: levels ran inline, so this is not the slotted shape under test", maxOnOneStack)
	}
	if goroutines < int(deepest)+1 {
		t.Fatalf("%d levels ran on only %d goroutines, so they were not each slotted onto a worker of their own", deepest+1, goroutines)
	}

	if err == nil {
		t.Fatalf("a chain of %d slotted levels each holding %d MiB reached the probe guard against a %d MiB quota with no error: roughly %d MiB live, refused by nothing",
			deepest+1, taskDepthPayload>>20, taskDepthQuota>>20, ((deepest+1)*taskDepthPayload)>>20)
	}
	if !strings.Contains(err.Error(), "memory quota exceeded") {
		t.Fatalf("nesting stopped at depth %d with an unrelated error, so the memory quota is not what stopped it: %v", deepest, err)
	}
	if deepest > taskDepthMaxLevels {
		t.Fatalf("reached depth %d holding roughly %d MiB against a %d MiB quota; the quota is still being handed out per level",
			deepest, ((deepest+1)*taskDepthPayload)>>20, taskDepthQuota>>20)
	}
}

// TestInlineTaskNestingCannotMultiplyTheMemoryQuota pins the same bound for the
// shape that runs on its caller's goroutine.
//
// The inline and slotted shapes were byte for byte identical at equal depth
// before the fix -- both held exactly 34 MiB at depth 16 -- so the
// multiplication follows nesting rather than inlining, and the inline depth cap
// added earlier cannot be what fixes it. That cap bounded this shape at 16
// levels, which is still 4.5x an 8 MiB quota, and it stopped the chain for a
// reason that has nothing to do with memory.
//
// This test therefore also pins which limit does the stopping: a failure naming
// the inline depth limit means memory is still unbounded here and the chain
// merely ran out of permitted nesting.
func TestInlineTaskNestingCannotMultiplyTheMemoryQuota(t *testing.T) {
	t.Parallel()

	// One worker, taken by the outer level, so every nested level is starved
	// and runs on the goroutine waiting for it.
	const (
		guard   = 63
		workers = 1
	)

	deepest, samples, err := runTaskDepthMemoryProbe(t,
		Config{MaxTaskConcurrency: workers, MemoryQuotaBytes: taskDepthQuota, StepQuota: Unlimited},
		taskDepthDoublings, guard)

	goroutines, maxOnOneStack := summarizeTaskDepthShape(samples, deepest)

	if deepest < 2 {
		t.Fatalf("only reached depth %d, too shallow to have nested at all", deepest)
	}
	if goroutines != 1 {
		t.Fatalf("%d levels ran on %d goroutines, want 1: they were slotted, so this is not the inline shape under test", deepest+1, goroutines)
	}
	if maxOnOneStack < int(deepest)+1 {
		t.Fatalf("only %d nested Executions on the goroutine for %d levels, so the levels did not stack inline as this test intends", maxOnOneStack, deepest+1)
	}

	if err == nil {
		t.Fatalf("a chain of %d inline levels each holding %d MiB against a %d MiB quota was refused by nothing",
			deepest+1, taskDepthPayload>>20, taskDepthQuota>>20)
	}
	if strings.Contains(err.Error(), "inline depth limit") {
		t.Fatalf("the inline depth limit stopped this at depth %d, not the memory quota: live memory still multiplies with depth up to that cap: %v", deepest, err)
	}
	if !strings.Contains(err.Error(), "memory quota exceeded") {
		t.Fatalf("nesting stopped at depth %d with an unrelated error: %v", deepest, err)
	}
	if deepest > taskDepthMaxLevels {
		t.Fatalf("reached depth %d holding roughly %d MiB against a %d MiB quota; the quota is still being handed out per level",
			deepest, ((deepest+1)*taskDepthPayload)>>20, taskDepthQuota>>20)
	}
}

// TestFlatTaskMapIsNotChargedForItsSiblings is the over-correction guard, and
// it is the test that has to keep passing rather than the one that has to start.
//
// Bounding depth by summing a chain is one small step from bounding width by
// summing a whole tree, and that step would refuse ordinary programs: measured
// on this shape, summing the workers charges 256 MiB for a script whose real
// peak is 4 MiB. Width is already bounded by MaxTaskConcurrency, which a host
// sets deliberately; a sibling must not be charged for what its siblings hold.
//
// Every worker here allocates a payload that is comfortably within the quota on
// its own but far outside it summed across the map.
func TestFlatTaskMapIsNotChargedForItsSiblings(t *testing.T) {
	t.Parallel()

	const (
		workers = 32
		// 1 MiB each: 32 MiB summed across the map, against an 8 MiB quota.
		doublings = 16
	)

	script := compileScriptWithConfig(t,
		Config{MaxTaskConcurrency: workers, MemoryQuotaBytes: taskDepthQuota, StepQuota: Unlimited},
		fmt.Sprintf(`def payload(k)
  s = "abcdefghabcdefgh"
  i = 0
  while i < k
    s = s + s
    i = i + 1
  end
  s
end

def step(n)
  payload(%d).size
end

def run()
  Tasks.map((0...%d).to_a, max: %d, with: :step)
  "done"
end`, doublings, workers, workers))

	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err != nil {
		t.Fatalf("a flat map of %d workers each holding 1 MiB was refused under an %d MiB quota, so siblings are being charged for one another and ordinary concurrency is now bounded by the map's width: %v",
			workers, taskDepthQuota>>20, err)
	}
}

// sharedGlobalNestScript reads one shared global at every nesting level, so the
// levels allocate nothing of their own and whatever the chain is charged is
// charged for structure they all share.
const sharedGlobalNestScript = `def step(n)
  seen = shared_blob.size
  if n < %d
    Tasks.map([n + 1], max: 1, with: :step)
  end
  seen
end

def run()
  Tasks.map([0], max: 1, with: :step)
  "done"
end`

// smallestPassingQuotaForSharedGlobalChain bisects the smallest memory quota a
// chain of the given depth passes under. Pass/fail is monotone in the quota, so
// the boundary is exact and any change in what the chain is charged moves it.
func smallestPassingQuotaForSharedGlobalChain(t *testing.T, levels int, shared Value) int {
	t.Helper()

	const upper = 64 << 20
	opts := CallOptions{Globals: map[string]Value{"shared_blob": shared}}
	run := func(quota int) error {
		script := compileScriptWithConfig(t,
			Config{MaxTaskConcurrency: 64, MemoryQuotaBytes: quota, StepQuota: Unlimited},
			fmt.Sprintf(sharedGlobalNestScript, levels))
		_, err := script.Call(context.Background(), "run", nil, opts)
		return err
	}

	if err := run(upper); err != nil {
		t.Fatalf("chain of %d levels failed even under a %d MiB quota: %v", levels, upper>>20, err)
	}
	lo, hi := 1, upper
	for lo < hi {
		mid := lo + (hi-lo)/2
		if run(mid) == nil {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

// TestSharedGlobalIsNotChargedOncePerNestingLevel pins that structure a level
// shares with its ancestors is not charged again for every level of depth.
//
// The memory quota is a deduplicating walk of a reachable graph, and every level
// can reach the globals, but each level walks its own graph. Summing whole
// per-level estimates therefore counts one shared global once per level:
// measured at 17x for a 4 MiB global read at seventeen levels, 68 MiB charged
// for 4 MiB of real memory. A chain bound built on that sum would refuse
// scripts like this one, whose levels allocate nothing of their own.
//
// The property asserted is that the charge does not grow with depth, which is
// what separates a marginal accounting from a naive sum. It is asserted by
// bisecting the smallest quota each depth passes under rather than by picking
// one quota, because that boundary moves the moment anything is charged per
// level.
//
// The chain is charged a constant extra copy of the inherited globals, because
// the outermost execution and the first task level both charge them and the
// first level's baseline is taken before it materializes its own. That is a
// fixed 2x on the globals, not a multiplier on depth: measured at 2.01 MiB for
// one level and 2.03 MiB for thirty-two against a 1 MiB global, where a naive
// sum would have needed 33 MiB.
func TestSharedGlobalIsNotChargedOncePerNestingLevel(t *testing.T) {
	t.Parallel()

	const sharedSize = 1 << 20
	shared := NewString(strings.Repeat("s", sharedSize))

	shallow := smallestPassingQuotaForSharedGlobalChain(t, 1, shared)
	deep := smallestPassingQuotaForSharedGlobalChain(t, 32, shared)

	t.Logf("smallest passing quota: 1 level = %d bytes, 32 levels = %d bytes", shallow, deep)

	// Thirty-two levels may cost a little more than one -- each level does hold
	// its own frames -- but nothing close to a copy of the global per level.
	// Half the global is far below the 31 extra copies a naive sum would need
	// and far above the few hundred bytes per level actually observed.
	if growth := deep - shallow; growth > sharedSize/2 {
		t.Fatalf("going from 1 to 32 nesting levels raised the required quota by %d bytes against a shared global of %d bytes: the global is being charged per level rather than once for the chain (a naive sum would need %d bytes)",
			growth, sharedSize, 32*sharedSize)
	}
	// The other side of the same rule: the chain must still be charged for the
	// global at all. A bound that charged nothing would pass the check above
	// for the wrong reason.
	if shallow < sharedSize {
		t.Fatalf("a chain reading a %d byte global passed under a quota of only %d bytes, so the global is not being charged at all", sharedSize, shallow)
	}
}

// TestMemoryChainLimitIsTheTightestInTheChain pins that a nested engine cannot
// widen the bound it runs under, and that a level's ceiling is decided on the
// chain's fixed limits rather than on whatever is left of them.
func TestMemoryChainLimitIsTheTightestInTheChain(t *testing.T) {
	t.Parallel()

	outer := newMemoryChain(nil, 1000)
	ctx := contextWithMemoryChain(context.Background(), outer)

	var looser memoryChain
	if !looser.initForCall(ctx, 5000) {
		t.Fatalf("a call under a bounded caller must get an active chain node")
	}
	if looser.limit != 1000 {
		t.Fatalf("a looser callee resolved to limit %d, want the caller's tighter 1000: re-entering a more permissive engine would be the way out of the sandbox", looser.limit)
	}

	var tighter memoryChain
	if !tighter.initForCall(ctx, 200) {
		t.Fatalf("a call under a bounded caller must get an active chain node")
	}
	if tighter.limit != 200 {
		t.Fatalf("a tighter callee resolved to limit %d, want its own 200", tighter.limit)
	}

	// An engine with no bound of its own still belongs to its caller's chain.
	var unlimited memoryChain
	if !unlimited.initForCall(ctx, Unlimited) {
		t.Fatalf("an unlimited callee under a bounded caller must still join the chain, or re-entering it would escape the caller's bound")
	}
	if unlimited.limit != 1000 {
		t.Fatalf("an unlimited callee resolved to limit %d, want the caller's 1000", unlimited.limit)
	}

	// With no caller and no bound there is nothing to enforce.
	var free memoryChain
	if free.initForCall(context.Background(), Unlimited) {
		t.Fatalf("an unlimited call with no bounded caller must not build a chain node")
	}
}

// TestMemoryChainNeverRunsBackwards pins that publishing cannot credit the
// chain. The marginal is a difference between two independent graph walks, so a
// level whose graph shrank below the baseline it entered with would otherwise
// hand its ancestors allowance the host never granted.
func TestMemoryChainNeverRunsBackwards(t *testing.T) {
	t.Parallel()

	root := newMemoryChain(nil, 1000)
	root.publishAndExceeds(900)

	child := newMemoryChain(root, 1000)
	if child.publishAndExceeds(-500) {
		t.Fatalf("a negative marginal should cost nothing, but the chain reported itself over its limit")
	}
	if got := root.marginal.Load(); got != 900 {
		t.Fatalf("root holds %d after a child published a negative marginal, want 900: a negative marginal credited the chain and handed out allowance the host never granted", got)
	}
	if !child.publishAndExceeds(200) {
		t.Fatalf("900 already held plus 200 published exceeds the limit of 1000, but the chain allowed it")
	}
}

// TestNestedTasksShareOneInheritedGlobalWithinTheQuota is an over-correction
// guard, and like the other guards here it passes on both sides of this change.
//
// It pins the case where the constant extra copy of the inherited globals has
// to fit: eight levels sharing one 3 MiB global under an 8 MiB quota, with no
// level allocating anything of its own. Anything that charges a level more than
// once for what it inherited fails here.
//
// It does not pin the pre-baseline guard in memoryExceeded. Nothing reaches
// that path today -- binding runs no metered allocation before the baseline
// seam -- so no test can drive it, and it is documented there as insurance for
// the next check site rather than as a fixed defect.
func TestNestedTasksShareOneInheritedGlobalWithinTheQuota(t *testing.T) {
	t.Parallel()

	const (
		levels     = 8
		quota      = 8 << 20
		sharedSize = 3 << 20
	)

	script := compileScriptWithConfig(t,
		Config{MaxTaskConcurrency: levels + 4, MemoryQuotaBytes: quota, StepQuota: Unlimited},
		fmt.Sprintf(sharedGlobalNestScript, levels))

	opts := CallOptions{Globals: map[string]Value{
		"shared_blob": NewString(strings.Repeat("s", sharedSize)),
	}}

	if _, err := script.Call(context.Background(), "run", nil, opts); err != nil {
		t.Fatalf("%d nested levels sharing one %d MiB global under a %d MiB quota were refused, though no level allocates anything of its own: a level is being charged more than once for what it inherited: %v",
			levels, sharedSize>>20, quota>>20, err)
	}
}

// TestUnlimitedChildEnforcesAnInheritedCeiling pins that an engine with no
// memory quota of its own still honors the chain it was re-entered from.
//
// A capability adapter can re-enter a script on a different engine. When that
// engine sets MemoryQuotaBytes: Unlimited, initForCall deliberately keeps it in
// the caller's chain -- but every memory check guards on memoryQuota before
// doing anything, some sixty of them, so all of those guards returned early and
// the node was never published to or enforced. Re-entering an unbounded engine
// was then the way out of a bounded caller's sandbox, the same hole the
// sleeping budget closes by keeping an inherited budget whatever the callee
// allows.
//
// The execution adopts the inherited ceiling as its own quota, which is what
// makes every one of those guards see an active bound.
func TestUnlimitedChildEnforcesAnInheritedCeiling(t *testing.T) {
	t.Parallel()

	const inheritedLimit = 512 << 10

	var (
		mu       sync.Mutex
		gotQuota int
		gotChain bool
	)
	probe := quotaObserverProbe{observe: func(exec *Execution) {
		mu.Lock()
		gotQuota = exec.memoryQuota
		gotChain = exec.memChain != nil
		mu.Unlock()
	}}

	engine := MustNewEngine(Config{MemoryQuotaBytes: Unlimited, StepQuota: Unlimited})
	script, err := engine.Compile(`def run()
  probe.observe()
  "done"
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	// The bounded caller this unlimited engine is re-entered from.
	parent := newMemoryChain(nil, inheritedLimit)
	ctx := contextWithMemoryChain(context.Background(), parent)

	if _, err := script.Call(ctx, "run", nil, callOptionsWithCapabilities(probe)); err != nil {
		t.Fatalf("call failed: %v", err)
	}

	if !gotChain {
		t.Fatalf("an unlimited engine re-entered from a bounded caller built no chain node, so the caller's ceiling does not reach it at all")
	}
	if gotQuota <= 0 {
		t.Fatalf("an unlimited engine re-entered from a bounded caller ran with memoryQuota %d: every memory check guards on that before consulting the chain, so the inherited ceiling is never enforced and re-entering an unbounded engine escapes the caller's sandbox", gotQuota)
	}
	if gotQuota != inheritedLimit {
		t.Fatalf("adopted quota %d, want the inherited ceiling %d", gotQuota, inheritedLimit)
	}
}

// quotaObserverProbe hands the running Execution to a Go callback so a test can
// read the bound it is actually running under.
type quotaObserverProbe struct {
	observe func(exec *Execution)
}

func (p quotaObserverProbe) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"probe": NewObject(map[string]Value{
			"observe": NewBuiltin("probe.observe", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
				p.observe(exec)
				return NewNil(), nil
			}),
		}),
	}, nil
}

// TestHardValueCheckPublishesToTheChain pins that the per-value check consults
// the chain, and that the soft probe beside it deliberately does not.
//
// checkMemoryWith delegated to memoryFitsWith, which compares only this
// execution's own quota. Every checkMemoryValue site therefore neither enforced
// the chain nor refreshed this level's published marginal, so a parent that
// allocated while a spawned worker was blocked left a stale total behind and
// its descendants were admitted against the figure from before the allocation.
//
// The ancestor here already holds nearly all of the ceiling, and this level's
// own quota is enormous, so only a check that consults the chain can refuse.
func TestHardValueCheckPublishesToTheChain(t *testing.T) {
	t.Parallel()

	const limit = 4096

	ancestor := newMemoryChain(nil, limit)
	ancestor.publishAndExceeds(limit - 512)

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	exec.memChainNode.parent = ancestor
	exec.memChainNode.limit = limit
	exec.memChainNode.descendantHeadroom.Store(noDescendantConstraint)
	exec.memChain = &exec.memChainNode
	exec.memBaselineSet = true

	// Comfortably larger than the 512 bytes the ancestor has left, and far
	// under this execution's own quota.
	big := NewString(strings.Repeat("x", 8192))

	if err := exec.checkMemoryWith(big); err == nil {
		t.Fatalf("a hard value check allocated 8 KiB with only %d bytes left on the chain and was not refused: the check consults only this execution's own quota, so the shared ceiling is unenforced on every checkMemoryValue site", 512)
	}

	// The other half of the rule, and it is about publishing rather than about
	// consulting. A probe reads the chain too -- answering against this
	// execution alone let a caller that allocates on the answer, and two of them
	// do, size against room the chain does not have. What a probe must not do is
	// *write*: it asks about a value that may never be built, so publishing its
	// estimate would let a hypothetical allocation refuse a sibling's real one.
	soft := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	soft.memChainNode.parent = ancestor
	soft.memChainNode.limit = limit
	soft.memChainNode.descendantHeadroom.Store(noDescendantConstraint)
	soft.memChain = &soft.memChainNode
	soft.memBaselineSet = true

	before := soft.memChain.marginal.Load()
	if soft.memoryFitsWith(big) {
		t.Fatalf("the probe admitted 8 KiB with %d bytes left on the chain: a probe that answers against this execution alone lets a caller allocating on its answer size against room the chain does not have", 512)
	}
	if got := soft.memChain.marginal.Load(); got != before {
		t.Fatalf("the probe published %d to the chain (was %d): consulting is required, writing is not, and a speculative figure can refuse a sibling's real allocation", got, before)
	}
}

// classHeavyNestScript builds a script whose every level clones a substantial
// set of class definitions into a root env of its own.
func classHeavyNestScript(classes, levels int) string {
	var b strings.Builder
	for i := range classes {
		fmt.Fprintf(&b, `class Thing%d
  def initialize(a)
    @a = a
  end
  def value()
    @a
  end
  def label()
    "thing-%d-with-a-reasonably-long-literal-payload-to-make-the-definition-cost-something"
  end
end

`, i, i)
	}
	fmt.Fprintf(&b, `def step(n)
  if n < %d
    Tasks.map([n + 1], max: 1, with: :step)
  end
  n
end

def run()
  Tasks.map([0], max: 1, with: :step)
  "done"
end`, levels)
	return b.String()
}

// TestFreshPerCallSetupIsChargedNotSubtracted pins that a level's own cloned
// definitions count against the chain.
//
// Script.Call builds a root env for every nested level and clones the call's
// classes, enums and capability bindings into it. Those are unique to the level
// rather than inherited, but the baseline was the whole entry estimate, so
// memoryExceeded subtracted them from every later contribution. Each level
// therefore got a free allowance the size of its own definitions -- measured at
// 17,398 bytes per level for the hundred-class script here, 1.06 MiB across a
// 64-deep chain -- and a definition-heavy chain could multiply that setup while
// every level stayed inside its individual quota. That is a smaller copy of the
// defect the chain exists to close.
//
// The levels allocate nothing of their own, so the setup is the only thing that
// can exhaust the quota here.
func TestFreshPerCallSetupIsChargedNotSubtracted(t *testing.T) {
	t.Parallel()

	const (
		classes = 100
		levels  = 63
		quota   = 256 << 10
	)

	script := compileScriptWithConfig(t,
		Config{MaxTaskConcurrency: levels + 2, MemoryQuotaBytes: quota, StepQuota: Unlimited},
		classHeavyNestScript(classes, levels))

	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("a %d-level chain of a %d-class script ran to completion under a %d KiB quota although each level clones its own copy of every definition: the per-call setup is being subtracted as if inherited, so each level got a free allowance the size of its own definitions",
			levels, classes, quota>>10)
	}
	if !strings.Contains(err.Error(), "memory quota exceeded") {
		t.Fatalf("chain stopped for an unrelated reason: %v", err)
	}
}

// chunkPayloadFn is a payload builder that grows an array of distinct strings.
// It avoids the doubling a string-concatenation payload needs, whose transient
// peak is half again the result and trips the quota before two levels can hold
// their payloads at once.
const chunkPayloadFn = `def payload(n)
  out = []
  i = 0
  while i < n
    out.push("p" + i.to_s + %q)
    i = i + 1
  end
  out
end
`

// ancestorGrowthProbe holds a child's payload live while its parent allocates,
// which is the ordering a chain that only walks toward the root cannot see.
type ancestorGrowthProbe struct {
	childHolding chan struct{}
	release      chan struct{}
	once         *sync.Once
	// bothHeld records that the parent finished its own allocation while the
	// child was still holding its payload. That is the state the ceiling is
	// supposed to make unreachable.
	bothHeld *bool
	mu       *sync.Mutex
}

func (p ancestorGrowthProbe) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{"probe": NewObject(map[string]Value{
		"child_holding": NewBuiltin("probe.child_holding", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
			p.once.Do(func() { close(p.childHolding) })
			// Cancellation is the normal exit here, not the exception: when the
			// ceiling refuses the parent, the group is canceled and nothing will
			// ever reach the release. Waiting only on the release parked this
			// child until its timeout and made a passing test take 30 seconds.
			select {
			case <-p.release:
			case <-exec.Context().Done():
			case <-time.After(30 * time.Second):
			}
			return NewNil(), nil
		}),
		"wait_for_child": NewBuiltin("probe.wait_for_child", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
			select {
			case <-p.childHolding:
			case <-time.After(30 * time.Second):
			}
			return NewNil(), nil
		}),
		"both_held": NewBuiltin("probe.both_held", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
			p.mu.Lock()
			*p.bothHeld = true
			p.mu.Unlock()
			return NewNil(), nil
		}),
	})}, nil
}

// TestAncestorGrowthIsCheckedAgainstLiveDescendants pins that the ceiling holds
// in both directions.
//
// A check summed from the checking level toward the root, so it saw every
// ancestor and no descendant. That is right when the deepest live level is the
// one growing, which is the shape the original measurements used. It is wrong
// when an ancestor grows while a descendant is still holding memory -- and that
// is the ordinary slotted shape, since a parent holds its slot for as long as
// its child runs and can allocate between its own checks. Measured before the
// fix: 7.15 MiB live across two levels against a 4 MiB ceiling, admitted.
//
// What a check adds is the largest total any live chain below it has published,
// a maximum over the paths below rather than a sum of them. A sum would make
// the width of a flat map aggregate, which is the thing this design exists not
// to do.
//
// The parent here waits until the child is holding its payload before
// allocating its own, so the two are live together by construction.
func TestAncestorGrowthIsCheckedAgainstLiveDescendants(t *testing.T) {
	t.Parallel()

	const (
		quota  = 4 << 20
		chunks = 500 // ~2.5 MiB per level: fits alone, not together
	)

	chunk := strings.Repeat("q", 5000)
	source := fmt.Sprintf(chunkPayloadFn, chunk) + fmt.Sprintf(`
def child(n)
  buf = payload(%d)
  probe.child_holding()
  buf.size
end

def run()
  Tasks.run(max: 4) do |tasks|
    t = tasks.spawn(:child, 1)
    probe.wait_for_child()
    mine = payload(%d)
    probe.both_held()
    t.value + mine.size
  end
end`, chunks, chunks)

	script := compileScriptWithConfig(t,
		Config{MaxTaskConcurrency: 8, MemoryQuotaBytes: quota, StepQuota: Unlimited}, source)

	var (
		mu       sync.Mutex
		bothHeld bool
	)
	probe := ancestorGrowthProbe{
		childHolding: make(chan struct{}),
		release:      make(chan struct{}),
		once:         &sync.Once{},
		bothHeld:     &bothHeld,
		mu:           &mu,
	}
	// Whatever happens, never leave the child parked: a refused parent never
	// reaches the release, and a test that hangs is worse than one that fails.
	defer close(probe.release)

	_, err := script.Call(context.Background(), "run", nil, callOptionsWithCapabilities(probe))

	mu.Lock()
	held := bothHeld
	mu.Unlock()

	if held {
		t.Fatalf("a parent finished allocating ~2.5 MiB while its child was still holding ~2.5 MiB, against a %d MiB ceiling: the check walks from the checking level toward the root, so an ancestor growing under a live descendant is admitted against a total that omits it",
			quota>>20)
	}
	if err == nil {
		t.Fatalf("the parent was never admitted, but nothing was reported either; a refused allocation must surface as an error")
	}
	if !strings.Contains(err.Error(), "memory quota exceeded") {
		t.Fatalf("stopped for an unrelated reason: %v", err)
	}
}

// TestFinishedDescendantsStopChargingTheirAncestor is the over-correction guard
// for the descendant accounting above.
//
// What is live below a node is kept as a high-water while its children run, so
// that an ancestor cannot grow into space a descendant already holds. Kept
// after they finish it would be a permanent over-charge: a deep chain that
// completed would go on refusing its parent's later work, which is memory that
// no longer exists.
//
// Here a chain runs and completes, and only then does the outermost level
// allocate something that fits on its own but would not fit beside the chain's
// high-water.
func TestFinishedDescendantsStopChargingTheirAncestor(t *testing.T) {
	t.Parallel()

	const (
		quota  = 8 << 20
		levels = 3
		nested = 200  // ~1 MiB per nested level
		after  = 1200 // ~6 MiB once the chain is gone
	)

	chunk := strings.Repeat("q", 5000)
	source := fmt.Sprintf(chunkPayloadFn, chunk) + fmt.Sprintf(`
def deep(n)
  buf = payload(%d)
  if n < %d
    Tasks.map([n + 1], max: 1, with: :deep)
  end
  buf.size
end

def run()
  Tasks.map([0], max: 1, with: :deep)
  later = payload(%d)
  later.size
end`, nested, levels, after)

	script := compileScriptWithConfig(t,
		Config{MaxTaskConcurrency: 16, MemoryQuotaBytes: quota, StepQuota: Unlimited}, source)

	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err != nil {
		t.Fatalf("allocating ~6 MiB under an %d MiB quota failed after a %d-level chain had already finished: the memory those levels held is gone, so continuing to charge for it refuses work the host allows: %v",
			quota>>20, levels, err)
	}
}

// TestRefusalNamesTheInheritedCeiling pins that a refusal reports the bound that
// actually stopped it.
//
// A looser callee under a tighter caller is refused at the caller's ceiling but
// used to report its own quota, so an 8 MiB engine announced an 8 MiB failure
// when the real bound was the caller's. That sends whoever reads it to tune the
// engine that was never the constraint.
func TestRefusalNamesTheInheritedCeiling(t *testing.T) {
	t.Parallel()

	const (
		ownQuota  = 64 << 20
		inherited = 1 << 20
	)

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: ownQuota}
	exec.memChainNode.parent = newMemoryChain(nil, inherited)
	exec.memChainNode.limit = inherited
	exec.memChainNode.descendantHeadroom.Store(noDescendantConstraint)
	exec.memChain = &exec.memChainNode

	err := exec.memoryQuotaExceededError()
	if err == nil {
		t.Fatalf("no error built")
	}
	if strings.Contains(err.Error(), fmt.Sprint(ownQuota)) {
		t.Fatalf("refusal named this execution's own quota of %d, which is not what stopped it; a host reading this tunes the wrong limit: %v", ownQuota, err)
	}
	if !strings.Contains(err.Error(), fmt.Sprint(inherited)) {
		t.Fatalf("refusal did not name the inherited ceiling of %d that actually bound it: %v", inherited, err)
	}
}

// TestUnlimitedCalleeRegistersAsALiveChild pins that every kind of callee
// registers with its parent, not just a bounded one.
//
// The unlimited branch of initForCall returned before registering, so a parent
// with only unlimited children believed it had none. It would then clear what
// was live below it, and that child's footprint never reached any ancestor --
// so the ceiling stopped applying to exactly the callee that has no bound of
// its own.
func TestUnlimitedCalleeRegistersAsALiveChild(t *testing.T) {
	t.Parallel()

	parent := newMemoryChain(nil, 4096)
	ctx := contextWithMemoryChain(context.Background(), parent)

	var unlimited memoryChain
	if !unlimited.initForCall(ctx, Unlimited) {
		t.Fatalf("an unlimited callee under a bounded caller must join the chain")
	}
	if got := parent.liveDescendants.Load(); got != 1 {
		t.Fatalf("parent counted %d live children after an unlimited callee joined, want 1: unregistered, it lets its parent clear what is live below and its own footprint reaches no ancestor", got)
	}

	// And a bounded one, so the check cannot pass by counting everything twice.
	var bounded memoryChain
	if !bounded.initForCall(ctx, 1024) {
		t.Fatalf("a bounded callee must join the chain")
	}
	if got := parent.liveDescendants.Load(); got != 2 {
		t.Fatalf("parent counted %d live children after two callees joined, want 2", got)
	}
}

// TestConstraintAppliesOnlyWhileSomethingBelowIsLive pins how a descendant
// constraint is retired, which is by being ignored rather than cleared.
//
// Clearing it was three separate races over this PR. The last was the subtlest:
// deciding a node was idle and then writing leaves a window in which a
// replacement child registers and publishes, and if that child's constraint is
// looser than the stale one it leaves the stored value untouched -- so the
// pending write still succeeds and wipes a constraint that a live child needs.
//
// There is no such window now because nothing decides-then-writes. A constraint
// is consulted only while something below is live, so a value left by a finished
// chain cannot bind anyone; and the first arrival of a new generation drops it,
// so it cannot ratchet down across a sequence of children. That drop is
// conditional on the exact value observed, because a sibling registering
// alongside may already have published something tighter and losing that race
// must leave the tighter value standing.
func TestConstraintAppliesOnlyWhileSomethingBelowIsLive(t *testing.T) {
	t.Parallel()

	const ceiling = 1 << 20
	parent := newMemoryChain(nil, ceiling)

	// A live child's constraint binds.
	child := newMemoryChain(parent, ceiling)
	child.register()
	parent.tightenHeadroom(4000)
	if got := parent.headroom(); got != 4000 {
		t.Fatalf("headroom %d with a live child constraining it to 4000", got)
	}

	// Once it retires, the stale value is ignored rather than cleared: nothing
	// below is live, so nothing below binds.
	child.release()
	if got := parent.headroom(); got != ceiling {
		t.Fatalf("headroom %d after the last child retired, want its own ceiling %d: a finished chain must stop binding its ancestor", got, ceiling)
	}
	if parent.descendantHeadroom.Load() != 4000 {
		t.Fatalf("release wrote to the descendant constraint; retiring must not write, or it races with a replacement registering")
	}

	// A new generation inherits what the last one left, and that is deliberate.
	// Dropping it on registration was the fourth spelling of the clearing race:
	// one registrant could observe the count rise from none, pause while a
	// second published something tighter, and clear that fresh value as stale.
	//
	// Carrying it is the conservative error. The inherited constraint is tighter
	// than the new generation needs, so it can refuse work that would have fit;
	// clearing a live one is under-constraint, which is how a ceiling gets
	// exceeded. The ratchet is bounded by the tightest path any generation
	// needed, and it applies only while something below is live.
	replacement := newMemoryChain(parent, ceiling)
	replacement.register()
	if got := parent.headroom(); got != 4000 {
		t.Fatalf("headroom %d for a new generation under a node a previous one constrained to 4000: carrying the tighter value is the safe direction, and dropping it on registration races with a sibling publishing", got)
	}

	// And a fresh child tightening further still binds.
	parent.tightenHeadroom(2500)
	if got := parent.headroom(); got != 2500 {
		t.Fatalf("headroom %d after a live child constrained it to 2500", got)
	}
}

// TestDescendantCeilingBindsAnAncestorsGrowth pins that a tighter ceiling below
// a node applies to that node's own allocations too.
//
// The chain carried the bytes a descendant held but not the ceiling those bytes
// ran under. A 4 MiB callee re-entered from an 8 MiB caller was therefore checked
// against 4 MiB when it published and against the caller's 8 MiB when the caller
// published, so the caller could grow the shared path past the callee's ceiling
// while the callee sat blocked. Both now travel as one number: the room a
// descendant leaves its ancestors is its ceiling less what it already holds.
func TestDescendantCeilingBindsAnAncestorsGrowth(t *testing.T) {
	t.Parallel()

	const (
		outer = 8 << 20
		inner = 4 << 20
	)

	parent := newMemoryChain(nil, outer)
	ctx := contextWithMemoryChain(context.Background(), parent)

	var child memoryChain
	if !child.initForCall(ctx, inner) {
		t.Fatalf("a bounded callee under a bounded caller must join the chain")
	}
	if child.limit != inner {
		t.Fatalf("callee resolved to limit %d, want its own tighter %d", child.limit, inner)
	}

	// The callee publishes 3 MiB, which fits its own 4 MiB ceiling, and blocks.
	if child.publishAndExceeds(3 << 20) {
		t.Fatalf("3 MiB under a 4 MiB ceiling must be admitted")
	}

	// The caller now grows by 2 MiB. The shared path is 5 MiB, inside the
	// caller's own 8 MiB but past the 4 MiB the callee runs under.
	if !parent.publishAndExceeds(2 << 20) {
		t.Fatalf("the caller grew the shared path to 5 MiB while a callee bounded at %d MiB held 3 MiB of it, and was admitted: the chain carried the descendant's bytes but not the ceiling they run under, so the tighter limit stopped applying the moment an ancestor was the one growing",
			inner>>20)
	}

	// And the constraint is released with the callee, or the caller would stay
	// bound by a callee that has finished.
	child.release()
	if got := parent.headroom(); got != outer {
		t.Fatalf("caller still bound at %d after its callee finished, want its own %d", got, outer)
	}
}

// TestRetirementKeepsAccountingForDescendantsThatOutliveIt pins what it means
// for a level to retire while something it started is still running.
//
// Retirement used to assume every descendant is awaited before its parent
// returns, and cleared descendant accounting unconditionally. With capability
// re-entry out of scope every descendant is in fact awaited, so the shape this
// exercises is not reachable from a script today -- it is pinned because the
// invariant is what keeps retirement write-free, and a write on the way out is
// what produced three of the four clearing races on this branch.
//
// The decision is that retirement hands the accounting on rather than dropping
// it: a level's own bytes go, because its own memory is gone, and what its
// descendants hold stays until they retire in their turn. That is why what is
// counted is live *descendants* rather than live children -- retirement is not
// ordered by depth, so a grandparent must not call itself idle while a
// great-grandchild is still allocating.
//
// This drives the ordering directly rather than racing it, and it names the
// new accounting, so it pins the invariant rather than running against the
// commit before it.
func TestRetirementKeepsAccountingForDescendantsThatOutliveIt(t *testing.T) {
	t.Parallel()

	const (
		outer = 8 << 20
		inner = 4 << 20
	)

	root := newMemoryChain(nil, outer)
	mid := newMemoryChain(root, outer)
	mid.register()
	// The asynchronous callee, under a tighter ceiling of its own.
	callee := newMemoryChain(mid, inner)
	callee.register()

	// It publishes and then blocks, holding 3 MiB.
	if callee.publishAndExceeds(3 << 20) {
		t.Fatalf("3 MiB under a 4 MiB ceiling must be admitted")
	}

	// The level that started it returns while it is still running.
	mid.release()

	if got := mid.liveDescendants.Load(); got != 1 {
		t.Fatalf("the retired level counts %d live descendants, want 1: its callee is still running", got)
	}
	if mid.marginal.Load() != 0 {
		t.Fatalf("a retired level must stop contributing its own bytes; its memory is gone")
	}

	// The root now grows. The path through the still-live callee is 2 + 3 MiB
	// against the callee's 4 MiB ceiling, so it must be refused even though the
	// level between them has retired.
	if !root.publishAndExceeds(2 << 20) {
		t.Fatalf("the root grew the shared path to 5 MiB while a callee bounded at %d MiB still held 3 MiB of it, and was admitted: retiring the level in between discarded the callee's accounting, so an ancestor stopped seeing a descendant that was still allocating",
			inner>>20)
	}

	// Once the callee retires too, nothing below binds the root any more.
	callee.release()
	if got := root.headroom(); got != outer {
		t.Fatalf("root still bound at %d after every descendant finished, want its own %d", got, outer)
	}
}

// TestMemoryBudgetSubtractsWhatAncestorsHold pins the number every sizing and
// reservation site now asks for.
//
// Those sites size an allocation before any check can refuse it -- a scratch
// reservation, a projected entry cap, a regex match table -- so the number they
// divide has to be what is actually left. Reading the execution's own quota was
// wrong twice over: it ignores a tighter ceiling inherited from a caller, and it
// ignores the part of that ceiling the caller is already using. A parent local
// held across a task call is not in the child's graph, so subtracting only the
// child's usage leaves the ancestor's share unaccounted and the buffer is sized
// against room the chain does not have.
//
// This names memoryBudgetBytes and so pins the invariant rather than running
// against the commit before it. The harm the category causes is a spike rather
// than a breach -- the allocation is met by a chain-aware check immediately
// afterwards -- which is exactly why it is not distinguishable by whether an
// error is raised, and why it is pinned on the number instead.
func TestMemoryBudgetSubtractsWhatAncestorsHold(t *testing.T) {
	t.Parallel()

	const (
		ceiling = 8 << 20
		held    = 3 << 20
	)

	root := newMemoryChain(nil, ceiling)
	root.publishAndExceeds(held)

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: ceiling}
	exec.memChainNode.parent = root
	exec.memChainNode.limit = ceiling
	exec.memChainNode.descendantHeadroom.Store(noDescendantConstraint)
	exec.memChain = &exec.memChainNode
	exec.memBaselineSet = true

	if got, want := exec.memoryBudgetBytes(), ceiling-held; got != want {
		t.Fatalf("budget %d with an ancestor holding %d of a %d ceiling, want %d: sizing against the ceiling lets a nested call build a buffer out of room its caller is already using",
			got, held, ceiling, want)
	}

	// A tighter local quota still binds when it is the smaller of the two.
	tight := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 20}
	tight.memChainNode.parent = root
	tight.memChainNode.limit = ceiling
	tight.memChainNode.descendantHeadroom.Store(noDescendantConstraint)
	tight.memChain = &tight.memChainNode
	tight.memBaselineSet = true
	if got := tight.memoryBudgetBytes(); got != 1<<20 {
		t.Fatalf("budget %d for an execution whose own quota is the tighter bound, want %d", got, 1<<20)
	}

	// With no chain at all there is nothing above to subtract.
	alone := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: ceiling}
	if got := alone.memoryBudgetBytes(); got != ceiling {
		t.Fatalf("budget %d for an execution with no chain, want its own quota %d", got, ceiling)
	}

	// An ancestor already over its ceiling leaves nothing, and the floor keeps
	// that from reading as a negative budget a caller would treat as huge.
	over := newMemoryChain(nil, ceiling)
	over.publishAndExceeds(ceiling + (1 << 20))
	starved := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: ceiling}
	starved.memChainNode.parent = over
	starved.memChainNode.limit = ceiling
	starved.memChainNode.descendantHeadroom.Store(noDescendantConstraint)
	starved.memChain = &starved.memChainNode
	starved.memBaselineSet = true
	if got := starved.memoryBudgetBytes(); got != 0 {
		t.Fatalf("budget %d under an ancestor already past the ceiling, want 0", got)
	}
}

// TestTaskPayloadCostsTwiceItsSize pins the price of not subtracting a task's
// arguments, so that the accepted over-charge is a recorded number rather than
// an emergent one.
//
// The group retains the cloned payload while the job runs and the worker binds
// it, and the baseline subtracts neither. For a composite that is the exact
// count -- call entry deep-copies it, so two graphs really are live. For a
// string it is an over-charge, because the backing is shared even though the
// header and Value slot are not.
//
// Subtracting the shared part was implemented and withdrawn: its correctness
// needs an enumeration of shared parts per kind, three measurements of that were
// wrong at three different granularities, and being wrong admits work past the
// ceiling. Its whole benefit was this test's difference from 1x.
//
// So this asserts roughly 2x and not less. If someone makes it 1x again they
// have re-entered that problem, and this comment is the argument they need to
// answer. If they make it much more than 2x, something else regressed.
func TestTaskPayloadCostsTwiceItsSize(t *testing.T) {
	t.Parallel()

	const payloadSize = 1 << 20

	payload := NewString(strings.Repeat("p", payloadSize))
	source := `def step(item)
  item.size
end

def run(payload)
  Tasks.map([payload], max: 1, with: :step)
  "done"
end`

	run := func(quota int) error {
		script := compileScriptWithConfig(t,
			Config{MaxTaskConcurrency: 4, MemoryQuotaBytes: quota, StepQuota: Unlimited}, source)
		_, err := script.Call(context.Background(), "run", []Value{payload}, CallOptions{})
		return err
	}

	const upper = 64 << 20
	if err := run(upper); err != nil {
		t.Fatalf("a single %d byte task item failed under a %d MiB quota: %v", payloadSize, upper>>20, err)
	}
	lo, hi := 1, upper
	for lo < hi {
		mid := lo + (hi-lo)/2
		if run(mid) == nil {
			hi = mid
		} else {
			lo = mid + 1
		}
	}

	if lo < payloadSize+payloadSize/2 {
		t.Fatalf("one %d byte task item needs only %d (%.2fx): the payload is being subtracted from the worker's baseline again, which charges once for what may be two live graphs",
			payloadSize, lo, float64(lo)/float64(payloadSize))
	}
	if lo > 3*payloadSize {
		t.Fatalf("one %d byte task item needs %d (%.2fx), well past the two copies this charges for", payloadSize, lo, float64(lo)/float64(payloadSize))
	}
	t.Logf("one %d byte task item needs %d bytes (%.2fx) -- the recorded price of not subtracting arguments", payloadSize, lo, float64(lo)/float64(payloadSize))
}

// TestScanTableReservationCoversAppendGrowth pins that the bytes reserved before
// the regexp engine builds its table cover the table it actually builds.
//
// FindAllStringSubmatchIndex appends into the outer slice, so its capacity
// overshoots the match count and every unused slot is a slice header.
// scanMatchBudget has always priced that growth; the pre-allocation reservation
// added later priced one slot per match instead, so the engine could build a
// table larger than the bytes charged for it and a parent allocating
// concurrently saw headroom that was not there.
//
// Both now price through one function, which is what keeps them from drifting
// again. The assertions below are the two halves of that: the shared figure must
// cover a grown table, and the row-only projection must not -- if it did, the
// shared figure would be unnecessary and the drift would not have mattered.
func TestScanTableReservationCoversAppendGrowth(t *testing.T) {
	t.Parallel()

	const groups = 2
	for _, matches := range []int{8, 512, 5000} {
		// A table that grew the way append grows one: more capacity than length.
		grown := make([][]int, matches, matches+matches/2)
		actual := actualRegexSubmatchIndexBytes(grown, groups)

		if reserved := worstCaseRegexSubmatchIndexBytes(matches, groups); reserved < actual {
			t.Fatalf("%d matches: reserved %d bytes before the engine ran but the table occupies %d: the engine builds a table larger than the bytes charged for it, so a parent allocating meanwhile sees headroom that is not there",
				matches, reserved, actual)
		}
		if rowsOnly := projectedRegexSubmatchIndexBytes(matches, groups); rowsOnly >= actual {
			t.Fatalf("%d matches: the row-only projection (%d) already covers a grown table (%d), so this test is not exercising the growth it exists for",
				matches, rowsOnly, actual)
		}
	}
}

// TestBaselineDoesNotSubtractArguments pins that a call's arguments are charged
// on both sides of a task boundary, deliberately.
//
// The group retains the cloned payload in jobPayloads for as long as the job
// runs, and the worker binds it, so subtracting it from the worker's baseline
// would leave one charge for what may be two live graphs. Whether it is one or
// two depends on the kind, and on which *parts* of the value: a composite is
// deep-copied at call entry, and even a string shares only its backing while its
// header and Value slot are the worker's own.
//
// Subtracting the shared part was implemented and withdrawn. Its correctness
// needs an enumeration of shared parts per kind, and being wrong about that
// admits work past the ceiling. Three measurements were wrong at three
// granularities -- which members cross, which kinds share, which parts of a
// sharing kind share -- so the correctness condition is finer than the
// instrument that was checking it. Its whole benefit was 33% on string payloads,
// with composites identical either way.
//
// So the over-charge stands, in the direction that refuses rather than admits.
// If this is ever revisited, the number to beat is in the PR body and the shape
// that would justify it is a large string payload under a tight quota.
func TestBaselineDoesNotSubtractArguments(t *testing.T) {
	t.Parallel()

	const (
		ceiling = 8 << 20
		count   = 256
	)

	newChild := func() *Execution {
		root := newMemoryChain(nil, ceiling)
		exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: ceiling}
		exec.memChainNode.parent = root
		exec.memChainNode.limit = ceiling
		exec.memChainNode.descendantHeadroom.Store(noDescendantConstraint)
		exec.memChain = &exec.memChainNode
		return exec
	}

	bare := newChild()
	bare.captureMemoryInheritedBaseline()
	want := bare.memBaseline

	// Every kind, because the withdrawn mechanism was wrong about kinds twice.
	for _, kind := range []struct {
		name  string
		build func(int) Value
	}{
		{"string", func(int) Value { return NewString(strings.Repeat("v", 64)) }},
		{"array", func(i int) Value { return NewArray([]Value{NewString(strings.Repeat("c", 64)), NewInt(int64(i))}) }},
		{"hash", func(int) Value { return NewHash(map[string]Value{"k": NewString(strings.Repeat("h", 64))}) }},
	} {
		args := make([]Value, count)
		for i := range args {
			args[i] = kind.build(i)
		}
		withArgs := newChild()
		withArgs.captureMemoryInheritedBaseline()
		if got := withArgs.memBaseline; got != want {
			t.Fatalf("%s: baseline is %d with arguments present and %d without; arguments must not move it, or a payload that may be two live graphs is charged once",
				kind.name, got, want)
		}
	}
}

// TestScanPublishesTheRootsItsBudgetSubtracted pins that a scan's sizing and its
// publication agree about what is live.
//
// The budget sizes the index table by subtracting the call roots -- receiver,
// arguments, block -- which live on the builtin's Go frame where the execution's
// own graph walk cannot see them. The reservation check then published the base
// graph and the reservation without those roots, so an ancestor saw the larger
// of two figures rather than their coexisting sum, and a nested scan was
// admitted against room the chain did not have.
//
// Both now read one scanRoots value. This asserts the property that value
// exists to guarantee: what the check publishes is not less than what the budget
// treated as already spent.
func TestScanPublishesTheRootsItsBudgetSubtracted(t *testing.T) {
	t.Parallel()

	const ceiling = 8 << 20

	held := make([]Value, 4096)
	for i := range held {
		held[i] = NewString(strings.Repeat("r", 256))
	}
	roots := scanRoots{receiver: NewString("subject"), args: []Value{NewArray(held)}, block: NewNil()}

	newExec := func() *Execution {
		parent := newMemoryChain(nil, ceiling)
		exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: ceiling}
		exec.memChainNode.parent = parent
		exec.memChainNode.limit = ceiling
		exec.memChainNode.descendantHeadroom.Store(noDescendantConstraint)
		exec.memChain = &exec.memChainNode
		exec.memBaselineSet = true
		return exec
	}

	// What the budget treats as already spent.
	sizing := newExec()
	spent := roots.liveBytes(sizing)

	// What the check publishes to the chain.
	publishing := newExec()
	if err := roots.check(publishing); err != nil {
		t.Fatalf("roots did not fit under an %d MiB ceiling: %v", ceiling>>20, err)
	}
	published := int(publishing.memChain.marginal.Load())

	if published < spent {
		t.Fatalf("the budget subtracted %d bytes as live but the check published only %d: the roots the sizing accounted for are invisible to the chain, so an ancestor sees the larger of two figures instead of their coexisting sum",
			spent, published)
	}

	// checkMemory is what this site published before, so the gap between the two
	// is the defect's size and the reason the assertion above has teeth.
	bare := newExec()
	if err := bare.checkMemory(); err != nil {
		t.Fatalf("bare check failed: %v", err)
	}
	bareBytes := int(bare.memChain.marginal.Load())
	if published <= bareBytes {
		t.Fatalf("publishing with roots (%d) is no larger than without (%d); this test is not exercising the difference it exists for", published, bareBytes)
	}
	t.Logf("budget treats %d bytes as spent; publishing with roots reports %d; the previous rootless publication reported %d, understating by %d",
		spent, published, bareBytes, published-bareBytes)
}

// bulkGlobalCapability binds a large host-supplied global into the call root.
//
// This is what distinguishes the finding below from the pre-registration
// residual next to it. That residual holds a level's own root env and cloned
// definitions, which the script's text bounds at roughly 745 bytes per class.
// What an adapter returns is host data, and nothing about the program bounds it.
type bulkGlobalCapability struct{ payload Value }

func (c bulkGlobalCapability) Bind(CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{"bulk": c.payload}, nil
}

// blockingBinderCapability blocks in Bind from the second bind onwards.
//
// The first bind is the outermost call's own, which has to finish for that call
// to reach the point where it spawns anything. The second is the nested level's,
// and that wait is the window: while it lasts, everything the adapter bound
// before it is on the level's call root and, before this fix, on no chain.
type blockingBinderCapability struct {
	binds    *atomic.Int32
	entered  chan struct{}
	release  chan struct{}
	enter    *sync.Once
	freed    *sync.Once
	bothHeld *bool
	mu       *sync.Mutex
}

func (c blockingBinderCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	if c.binds.Add(1) > 1 {
		c.enter.Do(func() { close(c.entered) })
		// Cancellation is the ordinary exit, not the exceptional one: when the
		// ceiling refuses the parent its group is canceled and nothing will ever
		// reach the release.
		select {
		case <-c.release:
		case <-binding.Context.Done():
		case <-time.After(30 * time.Second):
		}
	}
	return binderProbeGlobals(c.entered, c.release, c.freed, c.bothHeld, c.mu), nil
}

// TestOneAdaptersBindingsArePublishedBeforeTheNextCanBlock pins the fourth and
// last instance of the rule publishBeforeHostCode states.
//
// The level published its own setup once, before the whole binding routine, and
// checked again once the routine returned. With a single adapter that is
// enough. With several, the loop deep-copies the graph one adapter returns into
// the call root and then hands control to the next adapter, which may wait for
// as long as it likes -- and for the length of that wait the chain has seen only
// the setup marginal, so a nonblocking ancestor is admitted against a total
// missing every byte the first adapter supplied.
//
// The nested level binds two adapters: a host global, then a binder that blocks.
// Its parent waits until that binder is entered before allocating, so the two
// are live together by construction.
//
// Measured by difference rather than asserted as one outcome. The only thing
// that varies between the two rows is how much the first adapter returns -- the
// ceiling, the parent's own allocation, the nesting shape and the blocking
// adapter are identical. A test that only asserted the refusal could pass
// because this shape is refused for some unrelated reason; the small row is
// what makes the large row mean what it says, since the same script under the
// same ceiling with a 64-byte global has to be admitted.
func TestOneAdaptersBindingsArePublishedBeforeTheNextCanBlock(t *testing.T) {
	t.Parallel()

	const (
		quota  = 8 << 20
		chunks = 950 // ~4.5 MiB held by the parent
	)

	for _, tc := range []struct {
		name    string
		bulk    int
		refused bool
	}{
		// Big enough that the parent's allocation fits under the ceiling only
		// while the child's copy of it is off the chain.
		{name: "adapter_returns_2MiB", bulk: 2 << 20, refused: true},
		// Nothing to hide, so the same parent allocation must still be admitted.
		{name: "adapter_returns_64B", bulk: 64, refused: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			chunk := strings.Repeat("q", 5000)
			source := fmt.Sprintf(chunkPayloadFn, chunk) + fmt.Sprintf(`
def child(n)
  n
end

def run()
  Tasks.run(max: 4) do |tasks|
    tasks.spawn(:child, 1)
    probe.wait_for_binder()
    mine = payload(%d)
    probe.both_held()
    mine.size
  end
end`, chunks)

			script := compileScriptWithConfig(t,
				Config{MaxTaskConcurrency: 8, MemoryQuotaBytes: quota, StepQuota: Unlimited}, source)

			var (
				mu       sync.Mutex
				bothHeld bool
				binds    atomic.Int32
			)
			blocker := blockingBinderCapability{
				binds:    &binds,
				entered:  make(chan struct{}),
				release:  make(chan struct{}),
				enter:    &sync.Once{},
				freed:    &sync.Once{},
				bothHeld: &bothHeld,
				mu:       &mu,
			}
			// Whatever happens, never leave the nested binder parked.
			defer blocker.freed.Do(func() { close(blocker.release) })

			_, err := script.Call(context.Background(), "run", nil,
				callOptionsWithCapabilities(
					bulkGlobalCapability{payload: NewString(strings.Repeat("z", tc.bulk))},
					blocker))

			mu.Lock()
			held := bothHeld
			mu.Unlock()

			if !tc.refused {
				if !held {
					t.Fatalf("the parent was refused with only %d bytes bound by the adapter before the blocking one, so the refusal in the other row is not evidence that the adapter's graph reached the chain: %v", tc.bulk, err)
				}
				if err != nil {
					t.Fatalf("a run that must fit was stopped: %v", err)
				}
				return
			}
			if held {
				t.Fatalf("a parent finished allocating ~%d MiB against a %d MiB ceiling while its child held a %d MiB capability global and waited in the adapter bound after it: the level publishes once for the whole binding routine, so one adapter's bindings are invisible to the chain for as long as the next adapter blocks",
					(chunks*5000)>>20, quota>>20, tc.bulk>>20)
			}
			if err == nil {
				t.Fatalf("the parent was never admitted, but nothing was reported either; a refused allocation must surface as an error")
			}
			if !strings.Contains(err.Error(), "memory quota exceeded") {
				t.Fatalf("stopped for an unrelated reason: %v", err)
			}
		})
	}
}

// binderProbeGlobals is the probe both blocking binders below expose: the parent
// waits for the nested binder to be entered, allocates, and records that it got
// to the far side of its own allocation while the child was still holding.
func binderProbeGlobals(entered, release chan struct{}, freed *sync.Once, bothHeld *bool, mu *sync.Mutex) map[string]Value {
	return map[string]Value{"probe": NewObject(map[string]Value{
		"wait_for_binder": NewBuiltin("probe.wait_for_binder", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
			select {
			case <-entered:
			case <-exec.Context().Done():
			case <-time.After(30 * time.Second):
			}
			return NewNil(), nil
		}),
		"both_held": NewBuiltin("probe.both_held", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
			mu.Lock()
			*bothHeld = true
			mu.Unlock()
			// Free the blocked binder here rather than at the end of the test, so
			// the run this assertion is about finishes in its own time instead of
			// parking on the timeout above.
			freed.Do(func() { close(release) })
			return NewNil(), nil
		}),
	})}
}

// bulkContractCapability builds its contract map at the moment it is asked, and
// then blocks in Bind from the second bind onwards.
//
// It exists because a bound was asserted about this quantity and was wrong. The
// window between collecting an adapter's contracts and binding it was documented
// as bounded by how many methods the adapter declares in its Go source.
// CapabilityContractProvider bounds neither the cardinality of the map nor the
// length of its keys, so a host that generates it on demand -- as this does -- is
// not bounded by the program at all. The estimator charges
// capabilityContractsByName per entry and per key length, so what this retains is
// measured, and the chain has to see it before the bind that follows blocks.
type bulkContractCapability struct {
	count    int
	nameLen  int
	binds    *atomic.Int32
	entered  chan struct{}
	release  chan struct{}
	enter    *sync.Once
	freed    *sync.Once
	bothHeld *bool
	mu       *sync.Mutex
}

func (c bulkContractCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	out := make(map[string]CapabilityMethodContract, c.count)
	filler := strings.Repeat("n", c.nameLen)
	for i := range c.count {
		out["m"+strconv.Itoa(i)+filler] = CapabilityMethodContract{}
	}
	return out
}

func (c bulkContractCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	if c.binds.Add(1) > 1 {
		c.enter.Do(func() { close(c.entered) })
		select {
		case <-c.release:
		case <-binding.Context.Done():
		case <-time.After(30 * time.Second):
		}
	}
	return binderProbeGlobals(c.entered, c.release, c.freed, c.bothHeld, c.mu), nil
}

// TestAnAdaptersContractsArePublishedBeforeItsBindCanBlock pins the fifth
// instance of the publication rule, and the one that was a guard defect rather
// than a missing site.
//
// The publication had been moved to the top of the adapter loop, which satisfies
// "a publication above the call" for every host call in the body. But collecting
// an adapter's contracts happens after that publication and retains
// host-supplied data on the execution, and the adapter's own Bind then blocks
// with none of it published -- so a nonblocking ancestor allocates against a
// total omitting every byte of it. One adapter is enough; the previous finding
// needed two.
//
// The fix pairs each host call with its own publication inside a choke, so there
// is nowhere for retention to be inserted. This test is what says the pairing is
// doing work rather than merely existing.
//
// Measured by difference, like the finding before it. The same script, ceiling,
// nesting shape and blocking adapter run twice, and only the number of contracts
// the adapter declares changes: 2,000 keys of 1,000 characters must be refused,
// one key must still be admitted.
func TestAnAdaptersContractsArePublishedBeforeItsBindCanBlock(t *testing.T) {
	t.Parallel()

	const (
		quota   = 8 << 20
		chunks  = 900 // ~4.3 MiB held by the parent
		nameLen = 1000
	)

	for _, tc := range []struct {
		name      string
		contracts int
		refused   bool
	}{
		// 48 + n*(32 + 16 + 1000) as the estimator charges it: about 2 MiB, and
		// none of it fixed by the script's text or the adapter's.
		{name: "declares_2000_contracts", contracts: 2000, refused: true},
		// Nothing to hide, so the same parent allocation must still be admitted.
		{name: "declares_1_contract", contracts: 1, refused: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			chunk := strings.Repeat("q", 5000)
			source := fmt.Sprintf(chunkPayloadFn, chunk) + fmt.Sprintf(`
def child(n)
  n
end

def run()
  Tasks.run(max: 4) do |tasks|
    tasks.spawn(:child, 1)
    probe.wait_for_binder()
    mine = payload(%d)
    probe.both_held()
    mine.size
  end
end`, chunks)

			script := compileScriptWithConfig(t,
				Config{MaxTaskConcurrency: 8, MemoryQuotaBytes: quota, StepQuota: Unlimited}, source)

			var (
				mu       sync.Mutex
				bothHeld bool
				binds    atomic.Int32
			)
			adapter := bulkContractCapability{
				count:    tc.contracts,
				nameLen:  nameLen,
				binds:    &binds,
				entered:  make(chan struct{}),
				release:  make(chan struct{}),
				enter:    &sync.Once{},
				freed:    &sync.Once{},
				bothHeld: &bothHeld,
				mu:       &mu,
			}
			// Whatever happens, never leave the nested binder parked.
			defer adapter.freed.Do(func() { close(adapter.release) })

			_, err := script.Call(context.Background(), "run", nil, callOptionsWithCapabilities(adapter))

			mu.Lock()
			held := bothHeld
			mu.Unlock()

			if !tc.refused {
				if !held {
					t.Fatalf("the parent was refused with only %d contract declared, so the refusal in the other row is not evidence that the collected contracts reached the chain: %v", tc.contracts, err)
				}
				if err != nil {
					t.Fatalf("a run that must fit was stopped: %v", err)
				}
				return
			}
			if held {
				t.Fatalf("a parent finished allocating ~%d MiB against a %d MiB ceiling while its child held %d collected contracts (%d KiB by the estimator's own accounting) and waited in that same adapter's Bind: the contracts are retained after the level's publication, so they are invisible to the chain for as long as the bind blocks",
					(chunks*5000)>>20, quota>>20, tc.contracts, (estimatedMapBaseBytes+tc.contracts*(estimatedMapEntryBytes+estimatedStringHeaderBytes+nameLen))>>10)
			}
			if err == nil {
				t.Fatalf("the parent was never admitted, but nothing was reported either; a refused allocation must surface as an error")
			}
			if !strings.Contains(err.Error(), "memory quota exceeded") {
				t.Fatalf("stopped for an unrelated reason: %v", err)
			}
		})
	}
}

// contextEscapeCapability records the contexts this package hands to host code,
// which is every route by which a chain node could leave the runtime.
type contextEscapeCapability struct {
	mu       *sync.Mutex
	bindCtx  *context.Context
	execCtx  *context.Context
	reentry  func(*Execution) error
	observed *int
}

func (c contextEscapeCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	c.mu.Lock()
	*c.bindCtx = binding.Context
	c.mu.Unlock()
	return map[string]Value{"host": NewObject(map[string]Value{
		"observe": NewBuiltin("host.observe", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
			c.mu.Lock()
			*c.execCtx = exec.Context()
			*c.observed++
			c.mu.Unlock()
			if c.reentry != nil {
				if err := c.reentry(exec); err != nil {
					return NewNil(), err
				}
			}
			return NewNil(), nil
		}),
	})}, nil
}

// TestHostFacingContextsCarryNoChainNode is the guard the descope should have
// had, and it exists because removing a mechanism was verified less carefully
// than adding one would have been.
//
// This PR stopped publishing a level's own node onto its context, and the body
// concluded from that that a capability adapter re-entering the engine gets a
// fresh allowance. Those are different claims. A nested level still *inherited*
// its group's node through the context it was called with, and both
// CapabilityBinding.Context and Execution.Context() handed that context to host
// code unchanged -- so an adapter re-entering Script.Call linked its callee to
// the chain anyway, one level further up. The descope was verified by running
// the descoped shape and finding the surviving figures byte-identical, which is
// evidence that what remained worked and no evidence that nothing still
// inherited.
//
// "We stopped doing X" is a claim like any other. This is the test that fails if
// X resumes: it asserts on the contexts themselves, at every route out of the
// package, from a level that genuinely is on a chain.
func TestHostFacingContextsCarryNoChainNode(t *testing.T) {
	t.Parallel()

	const quota = 8 << 20

	script := compileScriptWithConfig(t,
		Config{MaxTaskConcurrency: 4, MemoryQuotaBytes: quota, StepQuota: Unlimited}, `
def child(n)
  host.observe()
  n
end

def run()
  Tasks.map([0], max: 1, with: :child)
  "done"
end`)

	var (
		mu       sync.Mutex
		bindCtx  context.Context
		execCtx  context.Context
		observed int
	)
	cap := contextEscapeCapability{mu: &mu, bindCtx: &bindCtx, execCtx: &execCtx, observed: &observed}

	if _, err := script.Call(context.Background(), "run", nil, callOptionsWithCapabilities(cap)); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	mu.Lock()
	gotBind, gotExec, saw := bindCtx, execCtx, observed
	mu.Unlock()

	// Without this, the assertions below would pass on a run where the nested
	// level never happened -- which is the way this guard would most plausibly
	// stop applying.
	if saw == 0 {
		t.Fatalf("the capability was never invoked from a nested level, so nothing was actually inspected")
	}
	if gotBind == nil || gotExec == nil {
		t.Fatalf("no host-facing context was captured (bind=%v exec=%v)", gotBind != nil, gotExec != nil)
	}
	if chain := memoryChainFromContext(gotBind); chain != nil {
		t.Fatalf("CapabilityBinding.Context carries a chain node with limit %d: an adapter re-entering Script.Call with it links its callee to this call's chain, so a re-entered engine inherits an ancestor's ceiling instead of the fresh allowance this PR documents",
			chain.limit)
	}
	if chain := memoryChainFromContext(gotExec); chain != nil {
		t.Fatalf("Execution.Context() carries a chain node with limit %d: same inheritance, by the route a builtin reaches rather than the one a binder does",
			chain.limit)
	}

	// Cancellation has to survive the hiding, or the fix trades a quota leak for
	// a callee that outlives its caller's cancellation.
	if gotBind.Done() == nil {
		t.Fatalf("the binding context can no longer be canceled: hiding the chain must hide one key, not detach the context from its parent")
	}
}

// TestCapabilityReEntryGetsAnAllowanceIndependentOfItsAncestors asserts the
// claim itself, rather than the absence of the key that used to break it.
//
// The PR body says a script re-entered through a capability adapter gets a fresh
// allowance, that this is what master does, and that the change is therefore
// strictly an improvement on that path. That claim is load-bearing for why the
// re-entry surface could be descoped at all, so it is worth a test that would
// fail if re-entry started inheriting again by any route, not only by the
// context key.
//
// The memory is held by the chain *root* rather than by the nested level, and
// that detail was found by the test failing to fail. A re-entry that inherits
// the context's node links to the node the task group published -- the nested
// level's parent -- so it becomes that level's sibling, and a sibling's bytes
// are deliberately not in its ancestor sum. Sizing the first attempt around what
// the nested level held therefore proved nothing: it passed on the commit where
// the leak was live.
//
// What the leak does share is the root's ceiling. So the root holds ~4.2 MiB
// across the task, and the re-entry allocates ~5.2 MiB: together over the 8 MiB
// ceiling, and alone comfortably inside it. The run completes only if the
// re-entry is accounted against an allowance of its own.
func TestCapabilityReEntryGetsAnAllowanceIndependentOfItsAncestors(t *testing.T) {
	t.Parallel()

	const (
		quota = 8 << 20
		held  = 800  // ~4.2 MiB held by the chain root across the task
		inner = 1000 // ~5.2 MiB inside the re-entered call
	)

	chunk := strings.Repeat("q", 5000)
	source := fmt.Sprintf(chunkPayloadFn, chunk) + fmt.Sprintf(`
def inner(n)
  payload(%d).size
end

def child(n)
  host.observe()
  n
end

def run()
  mine = payload(%d)
  Tasks.map([0], max: 1, with: :child)
  mine.size
end`, inner, held)

	script := compileScriptWithConfig(t,
		Config{MaxTaskConcurrency: 4, MemoryQuotaBytes: quota, StepQuota: Unlimited}, source)

	var (
		mu         sync.Mutex
		bindCtx    context.Context
		execCtx    context.Context
		observed   int
		reentryErr error
	)
	cap := contextEscapeCapability{
		mu: &mu, bindCtx: &bindCtx, execCtx: &execCtx, observed: &observed,
		reentry: func(exec *Execution) error {
			// Through the context the runtime handed out, which is the route an
			// adapter actually has.
			_, err := script.Call(exec.Context(), "inner", []Value{NewInt(0)}, CallOptions{})
			mu.Lock()
			reentryErr = err
			mu.Unlock()
			return err
		},
	}

	_, err := script.Call(context.Background(), "run", nil, callOptionsWithCapabilities(cap))

	mu.Lock()
	saw, rerr := observed, reentryErr
	mu.Unlock()

	if saw == 0 {
		t.Fatalf("the re-entry never ran, so this test asserted nothing")
	}
	if rerr != nil {
		t.Fatalf("a re-entered call allocating ~%d MiB under an %d MiB engine was refused while the chain root held ~%d MiB: it is being charged against its ancestors' ceiling instead of an allowance of its own, which is not what this PR claims and not what master does: %v",
			(inner*5000)>>20, quota>>20, (held*5000)>>20, rerr)
	}
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
}

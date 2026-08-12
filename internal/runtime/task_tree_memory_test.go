package runtime

import (
	"context"
	"fmt"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
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

	outer := &memoryChain{limit: 1000}
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

	root := &memoryChain{limit: 1000}
	root.publishAndExceeds(900)

	child := &memoryChain{parent: root, limit: 1000}
	if child.publishAndExceeds(-500) {
		t.Fatalf("a negative marginal should cost nothing, but the chain reported itself over its limit")
	}
	if got := child.total(); got != 900 {
		t.Fatalf("chain total %d after a negative publish, want 900: a negative marginal credited the chain and handed out allowance the host never granted", got)
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
	parent := &memoryChain{limit: inheritedLimit}
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

	ancestor := &memoryChain{limit: limit}
	ancestor.publishAndExceeds(limit - 512)

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	exec.memChainNode.parent = ancestor
	exec.memChainNode.limit = limit
	exec.memChain = &exec.memChainNode
	exec.memBaselineSet = true

	// Comfortably larger than the 512 bytes the ancestor has left, and far
	// under this execution's own quota.
	big := NewString(strings.Repeat("x", 8192))

	if err := exec.checkMemoryWith(big); err == nil {
		t.Fatalf("a hard value check allocated 8 KiB with only %d bytes left on the chain and was not refused: the check consults only this execution's own quota, so the shared ceiling is unenforced on every checkMemoryValue site", 512)
	}

	// The other half of the rule: the soft probe stays per-execution. It asks
	// about a value that may never be built, so publishing it would let a
	// hypothetical allocation refuse a concurrent sibling's real one.
	soft := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	soft.memChainNode.parent = ancestor
	soft.memChainNode.limit = limit
	soft.memChain = &soft.memChainNode
	soft.memBaselineSet = true

	if !soft.memoryFitsWith(big) {
		t.Fatalf("the soft probe consulted the chain; it must answer for this execution alone, or a speculative value that is never built can refuse a sibling's real allocation")
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
	exec.memChainNode.parent = &memoryChain{limit: inherited}
	exec.memChainNode.limit = inherited
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

// reentrantProbe re-enters the script through a capability, which is a nesting
// path that no task group creates.
type reentrantProbe struct {
	script **Script
	fn     string
}

func (p reentrantProbe) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{"probe": NewObject(map[string]Value{
		"reenter": NewBuiltin("probe.reenter", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
			// Re-entered with the running execution's own context, which is how
			// a capability adapter calls back into a script. No capabilities are
			// passed, so the inner call cannot recurse.
			if _, err := (*p.script).Call(exec.Context(), p.fn, nil, CallOptions{}); err != nil {
				return NewNil(), err
			}
			return NewNil(), nil
		}),
	})}, nil
}

// TestCapabilityReentryJoinsTheCallersChain pins that a call made through a
// capability is part of the chain it was made from.
//
// The chain node used to be published onto a context only by newTaskGroup. That
// is a side channel: a task group is not the only way a nested call is made. A
// capability adapter re-entering a script with exec.Context() handed the callee
// its grandparent's node, or none at all, so the callee started a fresh chain
// and got the host's whole allowance again -- the very defect this change
// exists to close, reachable by a path with no task in it.
//
// The node now travels on the execution's own context, the way the sleeping
// budget does, so it reaches everything the call drives.
//
// The outer level holds its payload across the re-entry, and the inner call
// allocates one of its own; either fits alone, and together they do not.
func TestCapabilityReentryJoinsTheCallersChain(t *testing.T) {
	t.Parallel()

	const (
		quota  = 4 << 20
		chunks = 500 // ~2.5 MiB each
	)

	chunk := strings.Repeat("q", 5000)
	source := fmt.Sprintf(chunkPayloadFn, chunk) + fmt.Sprintf(`
def inner()
  mine = payload(%d)
  mine.size
end

def run()
  held = payload(%d)
  probe.reenter()
  held.size
end`, chunks, chunks)

	script := compileScriptWithConfig(t,
		Config{MemoryQuotaBytes: quota, StepQuota: Unlimited}, source)

	holder := script
	_, err := script.Call(context.Background(), "run", nil,
		callOptionsWithCapabilities(reentrantProbe{script: &holder, fn: "inner"}))

	if err == nil {
		t.Fatalf("an outer call holding ~2.5 MiB re-entered the script through a capability, which allocated ~2.5 MiB more, and the %d MiB ceiling refused neither: the inner call started a chain of its own, so re-entry through a capability hands out the whole allowance again",
			quota>>20)
	}
	if !strings.Contains(err.Error(), "memory quota exceeded") {
		t.Fatalf("re-entry stopped for an unrelated reason: %v", err)
	}
}

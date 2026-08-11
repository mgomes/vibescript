package runtime

import (
	"context"
	"fmt"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
)

// inlineDepthProbe records what one nesting level costs the host goroutine and
// tells the script whether to nest again, so a regression is bounded by the
// probe rather than by the Go stack running out.
type inlineDepthProbe struct {
	mu       *sync.Mutex
	guard    int64
	deepest  *int64
	measured *map[int64]hostStackCost
}

// hostStackCost is what one nesting level costs on the goroutine running it.
// executions counts the nested Executions stacked below this point, each of
// which carries its own recursion cap, step quota and memory quota.
type hostStackCost struct {
	frames     int
	executions int
}

// measureHostStack counts the calling goroutine's Go frames. runtime.Callers is
// what does the counting: runtime.Stack stops at 100 frames, which is fewer than
// five nesting levels, so a stack that grows without bound reads as a flat one.
func measureHostStack() hostStackCost {
	pcs := make([]uintptr, 4096)
	for {
		n := goruntime.Callers(0, pcs)
		if n == len(pcs) {
			pcs = make([]uintptr, 2*len(pcs))
			continue
		}
		cost := hostStackCost{frames: n}
		frames := goruntime.CallersFrames(pcs[:n])
		for {
			frame, more := frames.Next()
			if strings.Contains(frame.Function, "callWithLazyTaskGlobals") {
				cost.executions++
			}
			if !more {
				break
			}
		}
		return cost
	}
}

func (p inlineDepthProbe) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"probe": NewObject(map[string]Value{
			"nest_again": NewBuiltin("probe.nest_again", func(_ *Execution, _ Value, args []Value, _ map[string]Value, _ Value) (Value, error) {
				depth := args[0].Int()
				cost := measureHostStack()
				p.mu.Lock()
				(*p.measured)[depth] = cost
				if depth > *p.deepest {
					*p.deepest = depth
				}
				p.mu.Unlock()
				return NewBool(depth < p.guard), nil
			}),
		}),
	}, nil
}

// TestInlineTaskNestingCannotGrowTheHostStack pins that task jobs cannot stack
// on one goroutine without bound.
//
// A group the shared pool cannot staff runs its job inline on the goroutine
// waiting for it, which is deliberate: the alternative is a deadlock. But the
// inline call enters Script.callWithLazyTaskGlobals, which builds a whole new
// Execution with a fresh recursion cap, step quota and memory quota, and the
// interpreter's recursion limit counts frames within one Execution and so never
// saw the boundary. A task function that opens another starved group per level
// therefore recursed across task boundaries forever: measured at 20 Go frames
// and one nested Execution per level, dead linear to 5,000 levels (100,018
// frames, 5,001 Executions, 64 MiB of goroutine stack) with no error, and Go's
// 1 GB stack limit -- a fatal error no recover can catch -- around 76,000.
//
// The host here allows one worker, which the outer level takes, so every nested
// level is starved and runs inline. That is the shape under test; it is also
// the cheapest way to reach it.
func TestInlineTaskNestingCannotGrowTheHostStack(t *testing.T) {
	t.Parallel()

	// Far enough past the limit that a regression is unmistakable, near enough
	// that a regression fails on this assertion rather than by exhausting the
	// host stack and taking the suite down with it.
	const guard = 100

	script := compileScriptWithConfig(t, Config{MaxTaskConcurrency: 1}, `def step(n)
  if probe.nest_again(n)
    Tasks.map([n + 1], max: 1, with: :step)
  end
  n
end

def run()
  Tasks.map([0], max: 1, with: :step)
end`)

	measured := map[int64]hostStackCost{}
	var deepest int64
	probe := inlineDepthProbe{mu: &sync.Mutex{}, guard: guard, deepest: &deepest, measured: &measured}

	_, err := script.Call(context.Background(), "run", nil, callOptionsWithCapabilities(probe))

	if int(deepest) > maxInlineTaskDepth {
		perLevel := (measured[deepest].frames - measured[0].frames) / int(deepest)
		t.Fatalf("inline task nesting reached depth %d against a limit of %d: %d Go frames and %d nested Executions on one goroutine, growing %d frames per level with nothing to stop it",
			deepest, maxInlineTaskDepth, measured[deepest].frames, measured[deepest].executions, perLevel)
	}
	if err == nil {
		t.Fatalf("nesting stopped at depth %d but the script was not told why; a refused inline job must surface as an error, not as a silent result", deepest)
	}
	if !strings.Contains(err.Error(), "inline depth limit") {
		t.Fatalf("nesting stopped at depth %d with an unrelated error, so the depth limit is not what stopped it: %v", deepest, err)
	}

	// The cost the limit exists to bound, asserted so the test fails if the
	// measurement itself stops working: a level that costs nothing would make
	// the depth assertion above pass for the wrong reason.
	if deepest < 2 {
		t.Fatalf("only reached depth %d, too shallow to have exercised inline nesting at all", deepest)
	}
	perLevel := (measured[deepest].frames - measured[0].frames) / int(deepest)
	if perLevel < 1 {
		t.Fatalf("a nesting level measured %d Go frames, so the probe is not seeing the host stack", perLevel)
	}
	if got := measured[deepest].executions - measured[0].executions; got != int(deepest) {
		t.Fatalf("depth %d put %d nested Executions on the goroutine, want one per level", deepest, got)
	}
}

// TestSlottedTaskNestingIsNotCappedByTheInlineLimit pins that the inline limit
// counts levels stacked on one goroutine, not levels of task nesting.
//
// A job that gets a slot runs on a new goroutine whose Go stack starts empty,
// so it costs the submitting goroutine nothing and must not be charged against
// the depth its submitter reached. Charging it would cap ordinary nested
// concurrency at the inline limit, which is a much tighter bound than the pool
// -- and the pool is what is supposed to bound this.
//
// Every level here holds a slot for as long as its child runs, so the pool's own
// ceiling already bounds the chain at the number of workers the host allows.
func TestSlottedTaskNestingIsNotCappedByTheInlineLimit(t *testing.T) {
	t.Parallel()

	const levels = maxInlineTaskDepth + 4
	script := compileScriptWithConfig(t, Config{MaxTaskConcurrency: levels + 2}, fmt.Sprintf(`def step(n)
  if n < %d
    Tasks.map([n + 1], max: 1, with: :step)
  end
  n
end

def run()
  Tasks.map([0], max: 1, with: :step)
  "done"
end`, levels))

	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err != nil {
		t.Fatalf("nesting %d levels of slotted tasks failed, so the inline limit is charging goroutines that hold their own stack: %v", levels, err)
	}
}

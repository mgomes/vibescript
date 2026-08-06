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

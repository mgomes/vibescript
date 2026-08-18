package runtime

import (
	"fmt"
	"os"
	"testing"
)

// maybeEnableEnvRecycleVerify turns on env-recycle verification when the
// VIBES_ENV_RECYCLE_VERIFY environment variable is set to "1". It is called from
// TestMain before any test runs so the flag is write-once: production code and
// tests only read it thereafter, keeping the package race-free under -race even
// with parallel tests. Running `VIBES_ENV_RECYCLE_VERIFY=1 go test` then executes
// the entire corpus with every recycled call frame poisoned, so any capture site
// the recycler wrongly judged dead panics on its next access.
func maybeEnableEnvRecycleVerify() {
	if os.Getenv("VIBES_ENV_RECYCLE_VERIFY") == "1" {
		envRecycleVerify = true
	}
}

// maybeEnableEstimatorVerify turns on the dormant-frame differential oracle when
// VIBES_ESTIMATOR_VERIFY=1 is set. Like maybeEnableEnvRecycleVerify it is called
// from TestMain before any test runs, so the flag is write-once and race-free.
// Running `VIBES_ESTIMATOR_VERIFY=1 go test` then checks every memory-quota base
// walk against the reference full-stack estimate and panics on any divergence.
func maybeEnableEstimatorVerify() {
	if os.Getenv("VIBES_ESTIMATOR_VERIFY") == "1" {
		estimatorVerify = true
	}
}

// maybeEnableBuiltinContractVerify turns on the builtin-contract verifier when
// VIBES_BUILTIN_CONTRACT_VERIFY=1 is set. Like the two above it is called from
// TestMain before any test runs, so the flag is write-once and race-free.
// Running `VIBES_BUILTIN_CONTRACT_VERIFY=1 go test ./...` walks the reachable
// graph across every dispatch of a builtin declaring non-mutation and panics if
// one changed it without advancing the epoch, which is how the whole corpus is
// made to stand behind those declarations rather than a reading of each body.
func maybeEnableBuiltinContractVerify() {
	if os.Getenv("VIBES_BUILTIN_CONTRACT_VERIFY") == "1" {
		builtinContractVerify = true
	}
}

// TestAcquireRecycleCallEnvPooling pins the production pooling contract of
// acquireCallEnv / recycleCallEnv directly: a reuse-eligible function's frame is
// returned to the free list and handed back to the next acquire (same pointer,
// reparented and cleared), while an ineligible function always allocates a fresh
// frame and never draws from the pool.
func TestAcquireRecycleCallEnvPooling(t *testing.T) {
	if envRecycleVerify {
		t.Skip("pool is bypassed under env-recycle verification")
	}

	exec := &Execution{}
	parentA := newEnv(nil)
	parentB := newEnv(nil)
	reusable := &ScriptFunction{Env: parentA, reuseCallEnv: true}

	first := exec.acquireCallEnv(reusable, 2)
	if first.parent != parentA {
		t.Fatalf("fresh acquire parent = %p, want %p", first.parent, parentA)
	}
	first.Define("scratch", NewInt(7))

	exec.recycleCallEnv(first)
	if len(exec.callEnvFreeList) != 1 {
		t.Fatalf("free list after recycle = %d, want 1", len(exec.callEnvFreeList))
	}
	// Recycling clears the frame immediately, so a pooled frame never pins the
	// heap payloads of its former locals until the next acquire.
	if _, ok := first.getOwn("scratch"); ok {
		t.Fatalf("recycled frame kept a stale binding before reuse")
	}
	if first.parent != nil {
		t.Fatalf("recycled frame kept parent %p, want nil", first.parent)
	}

	second := exec.acquireCallEnv(reusable, 2)
	if second != first {
		t.Fatalf("acquire did not reuse the recycled frame (%p vs %p)", second, first)
	}
	if len(exec.callEnvFreeList) != 0 {
		t.Fatalf("free list after reuse = %d, want 0", len(exec.callEnvFreeList))
	}
	if _, ok := second.getOwn("scratch"); ok {
		t.Fatalf("recycled frame kept a stale binding")
	}

	// A frame from a different function reparents to that function's Env.
	other := &ScriptFunction{Env: parentB, reuseCallEnv: true}
	exec.recycleCallEnv(second)
	third := exec.acquireCallEnv(other, 1)
	if third != second {
		t.Fatalf("cross-function acquire did not reuse the pooled frame")
	}
	if third.parent != parentB {
		t.Fatalf("reused frame parent = %p, want %p", third.parent, parentB)
	}

	// An ineligible function never draws from the pool, even when one is waiting.
	exec.recycleCallEnv(third)
	ineligible := &ScriptFunction{Env: parentA, reuseCallEnv: false}
	fresh := exec.acquireCallEnv(ineligible, 1)
	if fresh == third {
		t.Fatalf("ineligible function drew a frame from the pool")
	}
	if len(exec.callEnvFreeList) != 1 {
		t.Fatalf("free list after ineligible acquire = %d, want 1", len(exec.callEnvFreeList))
	}
}

// withEnvRecycleVerify runs fn with verification enabled and restores the prior
// value afterward. It must be called only from non-parallel tests: sequential
// top-level tests never run concurrently with any other test, so toggling the
// package global here does not race even under -race.
func withEnvRecycleVerify(t *testing.T, fn func()) {
	t.Helper()
	prev := envRecycleVerify
	envRecycleVerify = true
	defer func() { envRecycleVerify = prev }()
	fn()
}

// TestAcquireCallEnvNormalizesMapShape pins that a reused frame's storage shape
// matches a fresh one for the acquiring arity: a frame a larger function promoted
// to a values map is handed to a smaller function with the map dropped, so the
// small call binds inline and is not charged for a leftover empty map. Otherwise
// a call's memory footprint would depend on which functions ran before it.
func TestAcquireCallEnvNormalizesMapShape(t *testing.T) {
	if envRecycleVerify {
		t.Skip("pool is bypassed under env-recycle verification")
	}

	exec := &Execution{}
	parent := newEnv(nil)
	big := &ScriptFunction{Env: parent, reuseCallEnv: true}

	frame := exec.acquireCallEnv(big, inlineEnvBindingCapacity+2)
	for i := range inlineEnvBindingCapacity + 2 {
		frame.Define(fmt.Sprintf("v%d", i), NewInt(int64(i)))
	}
	if frame.values == nil {
		t.Fatalf("frame did not promote to a values map")
	}
	exec.recycleCallEnv(frame)

	small := &ScriptFunction{Env: parent, reuseCallEnv: true}
	reused := exec.acquireCallEnv(small, 2)
	if reused != frame {
		t.Fatalf("small acquire did not reuse the pooled frame")
	}
	if reused.values != nil {
		t.Fatalf("reused small frame kept a values map; its charge would depend on call history")
	}
	reused.Define("only", NewInt(1))
	if reused.values != nil {
		t.Fatalf("small function bound into a map instead of inline slots")
	}
	if reused.inlineLen != 1 {
		t.Fatalf("expected one inline binding, inlineLen = %d", reused.inlineLen)
	}

	// A large-capacity function reusing that now map-less frame gets a map
	// allocated up front, exactly as a fresh newEnvWithCapacity would, so its
	// binding layout and quota charge do not depend on the small call before it.
	exec.recycleCallEnv(reused)
	if reused.values != nil {
		t.Fatalf("recycled frame retained an allocated map")
	}
	big2 := exec.acquireCallEnv(big, inlineEnvBindingCapacity+2)
	if big2 != reused {
		t.Fatalf("large acquire did not reuse the pooled frame")
	}
	if big2.values == nil {
		t.Fatalf("large reuse of a map-less frame did not allocate a map to match a fresh frame")
	}
}

// TestRecycleCallEnvPoisonsUnderVerify pins the verification-mode contract: a
// recycled frame is poisoned and dropped rather than pooled, and touching it
// afterward panics.
func TestRecycleCallEnvPoisonsUnderVerify(t *testing.T) {
	withEnvRecycleVerify(t, func() {
		exec := &Execution{}
		fn := &ScriptFunction{Env: newEnv(nil), reuseCallEnv: true}
		env := exec.acquireCallEnv(fn, 1)
		env.Define("x", NewInt(1))

		exec.recycleCallEnv(env)
		if len(exec.callEnvFreeList) != 0 {
			t.Fatalf("verification mode pooled a frame; free list = %d, want 0", len(exec.callEnvFreeList))
		}
		if !env.poisoned {
			t.Fatalf("recycled frame was not poisoned under verification")
		}

		defer func() {
			if recover() == nil {
				t.Fatalf("accessing a poisoned frame did not panic")
			}
		}()
		env.Define("y", NewInt(2))
	})
}

// TestPoisonedEnvGuardsAllAccessPaths verifies the poison tripwire fires no
// matter how a stale reference to a recycled frame is used. Every access path a
// running program can take to a scope's own storage must route through a guarded
// primitive; this pins that so a newly added accessor that forgets the guard is
// caught. A missed path would let a wrongly recycled capturing frame be read or
// mutated in verification mode without panicking, defeating the whole safety net.
func TestPoisonedEnvGuardsAllAccessPaths(t *testing.T) {
	withEnvRecycleVerify(t, func() {
		cases := []struct {
			name   string
			access func(*Env)
		}{
			{"Get", func(e *Env) { e.Get("x") }},
			{"getBoundValue", func(e *Env) { e.getBoundValue("x", nil) }},
			{"getCallLocal", func(e *Env) { e.getCallLocal("x") }},
			{"getSkipping", func(e *Env) { e.getSkipping("x", nil) }},
			{"Define", func(e *Env) { e.Define("y", NewInt(2)) }},
			{"Assign", func(e *Env) { e.Assign("x", NewInt(2)) }},
			{"getOwn", func(e *Env) { e.getOwn("x") }},
			{"hasOwnBinding", func(e *Env) { e.hasOwnBinding("x") }},
			{"hasDynamic", func(e *Env) { e.hasDynamic("x") }},
			{"setDynamic", func(e *Env) { e.setDynamic("x", NewInt(3)) }},
			{"setExistingDynamic", func(e *Env) { e.setExistingDynamic("x", NewInt(3)) }},
			{"deleteDynamic", func(e *Env) { e.deleteDynamic("x") }},
			{"lookupBindingScope", func(e *Env) { e.lookupBindingScope("x") }},
			{"arrayAppendBuffer", func(e *Env) { e.arrayAppendBuffer("x") }},
			{"setArrayAppendBuffer", func(e *Env) { e.setArrayAppendBuffer("x", nil) }},
			{"lookupCallBlock", func(e *Env) { e.lookupCallBlock() }},
			{"setCallBlock", func(e *Env) { e.setCallBlock(NewNil()) }},
			{"rangeDynamicBindings", func(e *Env) { e.rangeDynamicBindings(func(string, Value) {}) }},
			{"rangeStaticBindings", func(e *Env) { e.rangeStaticBindings(func(string, Value) {}) }},
			{"visibleNames", func(e *Env) { e.visibleNames() }},
			{"CloneShallow", func(e *Env) { e.CloneShallow() }},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				env := newEnv(nil)
				env.Define("x", NewInt(1))
				env.poisoned = true
				defer func() {
					if recover() == nil {
						t.Fatalf("%s did not panic on a poisoned env", tc.name)
					}
				}()
				tc.access(env)
			})
		}
	})
}

// TestPoisonedParentEnvGuarded pins that chain walks catch a poisoned *ancestor*,
// not just the entry scope — the case where a closure captured a wrongly recycled
// frame as a parent and then reads a variable, yields, or settles an array. The
// entry scope is live; the poison sits one level up.
func TestPoisonedParentEnvGuarded(t *testing.T) {
	withEnvRecycleVerify(t, func() {
		cases := []struct {
			name   string
			access func(child *Env)
		}{
			{"Get", func(child *Env) { child.Get("x") }},
			{"Assign", func(child *Env) { child.Assign("x", NewInt(9)) }},
			{"lookupCallBlock", func(child *Env) { child.lookupCallBlock() }},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				parent := newEnv(nil)
				parent.Define("x", NewInt(1))
				parent.poisoned = true
				child := newEnv(parent)
				defer func() {
					if recover() == nil {
						t.Fatalf("%s did not panic on a poisoned parent env", tc.name)
					}
				}()
				tc.access(child)
			})
		}
	})
}

package runtime

import (
	"context"
	"strings"
	"testing"
)

// beginBaseWalk answers a check from one of four branches -- a nested throwaway
// walk, a block-iteration region walk, the builtin/task bypass, and the ordinary
// memoized walk -- and a registered output root has to be walked on all four.
//
// A root missing from one branch does not announce itself. The driver keeps
// working, the totals stay plausible, and only the checks that happen to land on
// that branch under-count, which is the exact hole output roots exist to close.
// The block drivers make that concrete: their callbacks run inside a region, so a
// root walked everywhere except beginRegionBaseWalk would be absent from the only
// branch that matters and every driver test would still pass.
//
// So this walks each branch deliberately, with an output holding a payload
// nothing else can reach, and requires each one's base to contain it.
func TestOutputRootsAreWalkedOnEveryBaseWalkBranch(t *testing.T) {
	t.Parallel()

	const payloadBytes = 256 * 1024

	newExecWithOutput := func(t *testing.T) (*Execution, func()) {
		t.Helper()
		exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
		exec.root = newEnv(nil)
		exec.envStack = append(exec.envStack, exec.root)
		// Held only by the output; nothing reachable from the execution refers to
		// it, so any branch that omits the root reports a base without it.
		out := []Value{NewString(strings.Repeat("z", payloadBytes))}
		exec.pushOutputWalkRoot(retainedValues(&out))
		return exec, func() { _ = exec.endOutputWalkRoot(nil) }
	}

	tests := []struct {
		name   string
		engage func(t *testing.T, exec *Execution) func()
	}{
		{
			name:   "memoized",
			engage: func(*testing.T, *Execution) func() { return func() {} },
		},
		{
			name: "builtin bypass",
			engage: func(_ *testing.T, exec *Execution) func() {
				exec.builtinDepth++
				return func() { exec.builtinDepth-- }
			},
		},
		{
			name: "block iteration region",
			engage: func(t *testing.T, exec *Execution) func() {
				scope := exec.beginBlockIterationRegion()
				if !exec.blockRegionBaseWalkEngaged(nil) {
					t.Fatal("region base walk did not engage; this case is not exercising beginRegionBaseWalk")
				}
				return scope.end
			},
		},
		{
			name: "nested session",
			engage: func(_ *testing.T, exec *Execution) func() {
				exec.baseWalkOpen = true
				return func() { exec.baseWalkOpen = false }
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			exec, unregister := newExecWithOutput(t)
			defer unregister()
			disengage := tc.engage(t, exec)
			defer disengage()

			s := exec.beginBaseWalk()
			base := s.base
			s.close()

			if base < payloadBytes {
				t.Fatalf("%s base walk reported %d bytes for an output holding a %d-byte payload nothing "+
					"else can reach; the registered root is not walked on this branch, so every check "+
					"answered here under-counts the driver's retained output", tc.name, base, payloadBytes)
			}
		})
	}
}

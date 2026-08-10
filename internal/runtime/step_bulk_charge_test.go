package runtime

import (
	"context"
	"testing"
)

// step runs its periodic cancellation and memory checks only when the counter
// lands exactly on a slow-path boundary, so a bulk charge that jumped over one
// without landing on it ran them zero times rather than the "single time" stepN
// documents. Charging 30 steps from a count of 1 crossed the boundary at 16,
// landed on 31, and returned nil under a canceled context. Every amortized
// charge in the tree is shaped this way, so a wide enough one could finish
// without ever observing cancellation (#1).
func TestBulkStepChargePollsACrossedSlowPathBoundary(t *testing.T) {
	t.Parallel()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	crossing := &Execution{ctx: canceled, steps: 1}
	// Lands on 2*period-1, one short of a boundary, having crossed the one at
	// period.
	if err := crossing.stepN(2*stepSlowPathPeriod - 2); err == nil {
		t.Errorf("charging %d steps from 1 crossed the boundary at %d and landed on %d without "+
			"observing a canceled context", 2*stepSlowPathPeriod-2, stepSlowPathPeriod, crossing.steps)
	}
	if crossing.steps != 2*stepSlowPathPeriod-1 {
		t.Errorf("the charge moved the counter to %d, want %d: a bulk charge must cost exactly what "+
			"the individual steps it stands in for cost", crossing.steps, 2*stepSlowPathPeriod-1)
	}
}

// The poll is owed only to a charge that crosses a boundary. One that stays
// inside a period must stay on the fast path, which is the amortization stepN
// exists for: making it poll unconditionally would put a reachable-graph walk
// behind every string scan and big-integer operation.
func TestBulkStepChargeInsideOnePeriodStaysOnTheFastPath(t *testing.T) {
	t.Parallel()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	inside := &Execution{ctx: canceled, steps: 1}
	if err := inside.stepN(2); err != nil {
		t.Errorf("charging 2 steps from 1 crosses no boundary and must not poll, got %v", err)
	}
}

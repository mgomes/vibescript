package runtime

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestSleepIsBoundedByTheHostMaximum pins that a script cannot park a worker
// for longer than the host allows.
//
// sleep honors context cancellation, but Script.Call substitutes
// context.Background() for a nil context, so an embedder relying on the
// engine's own quotas had nothing bounding wall-clock at all: the step and
// memory quotas do not advance while a worker sits in a timer, so one
// sleep(9223372036) parks it for centuries inside a sandbox that looks
// bounded (#29).
func TestSleepIsBoundedByTheHostMaximum(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  sleep(9223372036)
end`)
	// Driven under a deadline: unbounded, this call does not return for
	// centuries, so a regression has to fail the test rather than hang the
	// suite until the whole binary times out.
	done := make(chan error, 1)
	go func() {
		_, err := script.Call(context.Background(), "run", nil, CallOptions{})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a sleep past the host maximum must be rejected")
		}
		if !strings.Contains(err.Error(), "exceeds the") {
			t.Fatalf("sleep rejected for the wrong reason: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the call is still sleeping, so the host maximum did not bound it")
	}
}

// TestSleepWithinTheMaximumStillSleeps pins that the bound rejects only what
// is over it, so ordinary sleeps keep working.
func TestSleepWithinTheMaximumStillSleeps(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MaxSleepDuration: time.Second}, `def run()
  sleep(0.01)
end`)
	start := time.Now()
	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err != nil {
		t.Fatalf("a sleep inside the maximum must run: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 5*time.Millisecond {
		t.Fatalf("sleep returned after %s, so it did not sleep at all", elapsed)
	}
}

// TestSleepMaximumIsConfigurable pins that a host sets its own bound rather
// than inheriting one.
func TestSleepMaximumIsConfigurable(t *testing.T) {
	t.Parallel()

	src := `def run()
  sleep(30)
end`
	// 30s is inside the default minute and outside a tightened bound.
	tight := compileScriptWithConfig(t, Config{MaxSleepDuration: time.Second}, src)
	_, err := tight.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "exceeds the") {
		t.Fatalf("a tightened maximum must reject a 30s sleep, got %v", err)
	}
	if !strings.Contains(err.Error(), "1s") {
		t.Fatalf("the error must name the host's own maximum, got %v", err)
	}
}

// TestUnlimitedSleepMaximumOptsOut pins that a host can remove the bound.
//
// The sleep is not waited out: the context is canceled immediately, so a
// context error proves the duration was admitted, where the bound would have
// rejected it before the timer started.
func TestUnlimitedSleepMaximumOptsOut(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MaxSleepDuration: Unlimited}, `def run()
  sleep(9223372036)
end`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := script.Call(ctx, "run", nil, CallOptions{})
	if err == nil {
		t.Fatal("a canceled context must still end the call")
	}
	if strings.Contains(err.Error(), "exceeds the") {
		t.Fatalf("Unlimited must admit the duration, got %v", err)
	}
}

// TestSleepIsBudgetedAcrossTheCall pins that the bound limits the call rather
// than one statement.
//
// A per-statement limit bounds nothing on its own: a loop of individually
// permitted sleeps parks a worker for as long as it likes, and the step quota
// only advances between them (#29).
func TestSleepIsBudgetedAcrossTheCall(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MaxSleepDuration: 30 * time.Millisecond}, `def run()
  i = 0
  while i < 100
    sleep(0.01)
    i = i + 1
  end
  i
end`)
	done := make(chan error, 1)
	go func() {
		_, err := script.Call(context.Background(), "run", nil, CallOptions{})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("repeated permitted sleeps must exhaust the call's budget")
		}
		if !strings.Contains(err.Error(), "exceeds the") {
			t.Fatalf("rejected for the wrong reason: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the call is still sleeping, so the budget did not bound it")
	}
}

// TestSleepBudgetIsSharedWithTaskWorkers pins that the budget covers the whole
// call tree rather than each execution in it.
//
// A task worker runs on a fresh Execution, so a total kept per execution reset
// for every queued job: Tasks.map over a hundred items each sleeping the whole
// budget parked the host for a hundred times the bound while every individual
// sleep looked permitted (#29).
func TestSleepBudgetIsSharedWithTaskWorkers(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t,
		Config{MaxSleepDuration: 30 * time.Millisecond, MaxTaskConcurrency: 2},
		`def nap(n)
  sleep(0.01)
  n
end

def run()
  Tasks.map([1, 2, 3, 4, 5, 6, 7, 8, 9, 10], max: 1, with: :nap)
end`)

	done := make(chan error, 1)
	go func() {
		_, err := script.Call(context.Background(), "run", nil, CallOptions{})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ten workers each sleeping must exhaust the tree's shared budget")
		}
		if !strings.Contains(err.Error(), "exceeds the") {
			t.Fatalf("rejected for the wrong reason: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the tree is still sleeping, so the budget is not shared")
	}
}

// TestNestedEngineKeepsItsOwnTighterSleepBudget pins that a call re-entered on
// another engine is bound by that engine's limit, not by whatever the outer one
// allowed.
//
// A capability adapter can run a script on a different engine, and the callee's
// host set its bound for its own reasons. Inheriting the outer allowance let a
// loose engine lend a strict one permission its configuration refuses (#29).
func TestNestedEngineKeepsItsOwnTighterSleepBudget(t *testing.T) {
	t.Parallel()

	inner := compileScriptWithConfig(t, Config{MaxSleepDuration: time.Millisecond}, `def run()
  sleep(0.05)
end`)

	// The outer budget is generous enough to admit the inner sleep on its own,
	// so only the inner engine's own limit can reject it.
	outerBudget := &sleepBudget{limit: time.Second, remaining: time.Second}
	ctx := contextWithSleepBudget(context.Background(), outerBudget)

	done := make(chan error, 1)
	go func() {
		_, err := inner.Call(ctx, "run", nil, CallOptions{})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the inner engine's own maximum must bound its script")
		}
		if !strings.Contains(err.Error(), "exceeds the") {
			t.Fatalf("rejected for the wrong reason: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the inner call is still sleeping, so its own maximum did not bound it")
	}

	// The rejected sleep must not have been deducted from the outer allowance.
	if got := outerBudget.remaining; got != time.Second {
		t.Fatalf("a refused sleep spent %s of the outer budget", time.Second-got)
	}
}

// TestNestedEngineStillSpendsTheOuterBudget pins the other direction: a nested
// call that is within its own limit still draws on the budget above it, so a
// tree cannot escape the outer bound by re-entering repeatedly.
func TestNestedEngineStillSpendsTheOuterBudget(t *testing.T) {
	t.Parallel()

	inner := compileScriptWithConfig(t, Config{MaxSleepDuration: 10 * time.Millisecond}, `def run()
  sleep(0.005)
end`)

	// Room for one nested sleep and not two.
	outerBudget := &sleepBudget{limit: 7 * time.Millisecond, remaining: 7 * time.Millisecond}
	ctx := contextWithSleepBudget(context.Background(), outerBudget)

	if _, err := inner.Call(ctx, "run", nil, CallOptions{}); err != nil {
		t.Fatalf("a sleep inside both limits must run: %v", err)
	}
	if _, err := inner.Call(ctx, "run", nil, CallOptions{}); err == nil {
		t.Fatal("repeated nested calls must exhaust the outer budget")
	}
}

// TestUnlimitedCalleeStillSpendsAnInheritedBudget pins that an engine with no
// bound of its own does not let a bounded caller escape one.
//
// A capability adapter can re-enter an unbounded engine from a bounded one.
// Dropping the inherited budget because the callee is unlimited would make
// re-entry the way out of the outer host's sandbox (#29).
func TestUnlimitedCalleeStillSpendsAnInheritedBudget(t *testing.T) {
	t.Parallel()

	inner := compileScriptWithConfig(t, Config{MaxSleepDuration: Unlimited}, `def run()
  sleep(0.005)
end`)

	// Room for one nested sleep and not two.
	outerBudget := &sleepBudget{limit: 7 * time.Millisecond, remaining: 7 * time.Millisecond}
	ctx := contextWithSleepBudget(context.Background(), outerBudget)

	if _, err := inner.Call(ctx, "run", nil, CallOptions{}); err != nil {
		t.Fatalf("a sleep inside the inherited budget must run: %v", err)
	}
	if _, err := inner.Call(ctx, "run", nil, CallOptions{}); err == nil {
		t.Fatal("an unlimited callee must still spend the budget it inherited")
	}
}

// TestChainedBudgetsSpendAtomically pins that a refusal above leaves nothing
// deducted below.
//
// Task workers share a chained budget concurrently, so publishing the child's
// deduction before the parent accepted it would let one worker see capacity
// spent on a sleep that never happened.
func TestChainedBudgetsSpendAtomically(t *testing.T) {
	t.Parallel()

	parent := &sleepBudget{limit: 5 * time.Millisecond, remaining: 5 * time.Millisecond}
	child := &sleepBudget{limit: time.Second, remaining: time.Second, parent: parent}

	if child.spend(10 * time.Millisecond) {
		t.Fatal("a sleep the parent cannot afford must be refused")
	}
	if got := child.remaining; got != time.Second {
		t.Fatalf("the refused sleep spent %s of the child budget", time.Second-got)
	}
	if got := parent.remaining; got != 5*time.Millisecond {
		t.Fatalf("the refused sleep spent %s of the parent budget", 5*time.Millisecond-got)
	}

	if !child.spend(4 * time.Millisecond) {
		t.Fatal("a sleep both budgets can afford must be admitted")
	}
	if got := parent.remaining; got != time.Millisecond {
		t.Fatalf("parent has %s left, want 1ms: an admitted sleep must spend every level", got)
	}
}

// TestNonPositiveSleepNeverCreditsTheBudget pins the sign of the accumulator.
//
// spend subtracts the request from what is left, so a negative one would add
// allowance instead of consuming it, and a loop of them would mint an unbounded
// budget out of the bound meant to cap it. valueToSleepDuration rejects negative
// script durations, so the sleep builtin cannot reach this today; the guard is
// here so that a later caller computing a duration cannot quietly reopen it.
func TestNonPositiveSleepNeverCreditsTheBudget(t *testing.T) {
	t.Parallel()

	parent := &sleepBudget{limit: time.Second, remaining: time.Second}
	budget := &sleepBudget{limit: time.Second, remaining: time.Second, parent: parent}

	for _, duration := range []time.Duration{0, -time.Millisecond, -time.Hour} {
		if !budget.spend(duration) {
			t.Fatalf("a %s sleep costs nothing and must be admitted", duration)
		}
	}
	if got := budget.remaining; got != time.Second {
		t.Fatalf("budget holds %s, want 1s: a non-positive sleep must not move it", got)
	}
	if got := parent.remaining; got != time.Second {
		t.Fatalf("parent holds %s, want 1s: a non-positive sleep must not move it either", got)
	}

	if budget.spend(2 * time.Second) {
		t.Fatal("the budget was credited: a sleep past the limit must still be refused")
	}
}

// TestRefundReturnsUnusedTimeToEveryLevel pins that a refund credits the whole
// chain a spend was taken from, and never past a level's own limit.
func TestRefundReturnsUnusedTimeToEveryLevel(t *testing.T) {
	t.Parallel()

	parent := &sleepBudget{limit: time.Second, remaining: time.Second}
	budget := &sleepBudget{limit: 500 * time.Millisecond, remaining: 500 * time.Millisecond, parent: parent}

	if !budget.spend(400 * time.Millisecond) {
		t.Fatal("a sleep both budgets can afford must be admitted")
	}
	budget.refund(300 * time.Millisecond)

	if got := budget.remaining; got != 400*time.Millisecond {
		t.Fatalf("budget holds %s, want 400ms: the refund must return exactly what went unused", got)
	}
	if got := parent.remaining; got != 900*time.Millisecond {
		t.Fatalf("parent holds %s, want 900ms: a spend takes from every level, so a refund must return to every level", got)
	}

	budget.refund(time.Hour)
	if got := budget.remaining; got != 500*time.Millisecond {
		t.Fatalf("budget holds %s, want its 500ms limit: a refund must not leave more than the host granted", got)
	}
	if got := parent.remaining; got != time.Second {
		t.Fatalf("parent holds %s, want its 1s limit", got)
	}
}

// TestCanceledSleepKeepsWhatItDidNotUse pins that a sleep cut short by a
// sibling's failure gives its reservation back.
//
// The budget is spent before the sleep, since a sleep has to be refused before
// it happens rather than measured after. A worker canceled mid-sleep therefore
// left the whole reservation deducted for time the tree never spent, and because
// a task failure is rescuable, the script that carried on was refused sleeps its
// host would have allowed (#29).
func TestCanceledSleepKeepsWhatItDidNotUse(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t,
		Config{MaxSleepDuration: 700 * time.Millisecond, MaxTaskConcurrency: 4},
		`def nap(n)
  if n == 0
    raise "boom"
  end
  sleep(0.5)
  n
end

def run()
  begin
    Tasks.map([0, 1], max: 2, with: :nap)
  rescue
    nil
  end
  sleep(0.4)
  :finished
end`)

	done := make(chan Value, 1)
	failed := make(chan error, 1)
	go func() {
		result, err := script.Call(context.Background(), "run", nil, CallOptions{})
		if err != nil {
			failed <- err
			return
		}
		done <- result
	}()
	select {
	case result := <-done:
		if result.Kind() != KindSymbol || result.String() != "finished" {
			t.Fatalf("run returned %s, want :finished", result.String())
		}
	case err := <-failed:
		t.Fatalf("the sleep after the canceled one was refused, so its reservation was never returned: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the call is still running")
	}
}

// TestSharingIsDecidedOnLimitsNotOnWhatIsLeft pins that a tighter callee chains
// its own budget even when the inherited one is momentarily low.
//
// Sharing was decided on the inherited allowance, which falls as other calls
// reserve time and rises again when a canceled sleep refunds one. A callee that
// shared because the allowance happened to be under its limit kept sharing after
// a refund restored it, and could then sleep for the looser host's bound rather
// than its own (#29).
func TestSharingIsDecidedOnLimitsNotOnWhatIsLeft(t *testing.T) {
	t.Parallel()

	// A generous outer bound with almost nothing left of it, the state a
	// sibling call holding a long reservation produces.
	outer := &sleepBudget{limit: time.Hour, remaining: 5 * time.Millisecond}
	ctx := contextWithSleepBudget(context.Background(), outer)

	_, budget := sleepBudgetForCall(ctx, 10*time.Minute)
	if budget == outer {
		t.Fatal("a 10m callee shared an hour-long budget because little was left of it; the refund of a canceled sibling sleep then lets it sleep for the hour")
	}
	if budget.limit != 10*time.Minute {
		t.Fatalf("chained budget has limit %s, want the callee's own 10m", budget.limit)
	}
	if budget.parent != outer {
		t.Fatal("the chained budget must still spend the inherited one")
	}

	// A callee no looser than the chain still shares rather than chaining.
	if _, shared := sleepBudgetForCall(ctx, 2*time.Hour); shared != outer {
		t.Fatal("a callee looser than the inherited bound must share it, not chain a new one")
	}
}

// TestConcurrentSpendAndRefundConserveTheBudget pins that the chain's accounting
// survives concurrent workers, which is what the refund's locking is for.
//
// Run under -race, this also covers the lock order: refund takes the chain child
// to parent exactly as spend does, so the two cannot deadlock against each other.
func TestConcurrentSpendAndRefundConserveTheBudget(t *testing.T) {
	t.Parallel()

	parent := &sleepBudget{limit: time.Hour, remaining: time.Hour}
	budget := &sleepBudget{limit: time.Hour, remaining: time.Hour, parent: parent}

	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 200 {
				if budget.spend(time.Millisecond) {
					budget.refund(time.Millisecond)
				}
			}
		})
	}
	wg.Wait()

	if got := budget.remaining; got != time.Hour {
		t.Fatalf("budget holds %s, want the full hour back: every spend was refunded", got)
	}
	if got := parent.remaining; got != time.Hour {
		t.Fatalf("parent holds %s, want the full hour back", got)
	}
}

// TestSleepRejectionNamesWhatIsLeft pins that a refusal reports the allowance
// remaining, not just the configured limit.
//
// After earlier sleeps have spent part of the budget, naming the original limit
// beside a much smaller rejected duration reads as arithmetic that does not
// hold, and tells a script author nothing about why their sleep was refused.
func TestSleepRejectionNamesWhatIsLeft(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MaxSleepDuration: 60 * time.Millisecond}, `def run()
  sleep(0.05)
  sleep(0.02)
end`)
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatal("the second sleep must exhaust the budget")
	}
	// 20ms is well under the 60ms limit, so the limit alone explains nothing.
	if !strings.Contains(err.Error(), "10ms left") {
		t.Fatalf("the refusal must say what is left, got: %v", err)
	}
	if !strings.Contains(err.Error(), "60ms") {
		t.Fatalf("the refusal must still name the configured limit, got: %v", err)
	}
}

// TestQuotaProfilesScaleTheSleepingBudget pins that the sleeping bound moves
// with the profile ladder rather than being fixed under it.
//
// The CLI selects these profiles and runs the developer's own scripts, so the
// most generous rung has to lift the sleeping bound as it lifts steps and
// memory: a developer waiting on their own script is not a sandbox escape.
func TestQuotaProfilesScaleTheSleepingBudget(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		profile string
		want    time.Duration
	}{
		{profile: "low", want: time.Minute},
		{profile: "medium", want: 10 * time.Minute},
		{profile: "high", want: time.Hour},
		{profile: "xhigh", want: Unlimited},
	} {
		got, ok := QuotaProfileByName(tc.profile)
		if !ok {
			t.Fatalf("profile %q is missing", tc.profile)
		}
		if got.MaxSleepDuration != tc.want {
			t.Fatalf("profile %q allows %v of sleeping, want %v", tc.profile, got.MaxSleepDuration, tc.want)
		}
	}
}

// TestConfigSummaryReportsTheSleepingBudget pins that the advertised summary
// distinguishes engines that differ only in their sleeping bound, since hosts
// use it for diagnostics and configuration audits.
func TestConfigSummaryReportsTheSleepingBudget(t *testing.T) {
	t.Parallel()

	bounded, err := NewEngine(Config{MaxSleepDuration: time.Millisecond})
	if err != nil {
		t.Fatalf("bounded engine: %v", err)
	}
	unbounded, err := NewEngine(Config{MaxSleepDuration: Unlimited})
	if err != nil {
		t.Fatalf("unbounded engine: %v", err)
	}
	if bounded.ConfigSummary() == unbounded.ConfigSummary() {
		t.Fatalf("engines differing only in sleeping bound advertise the same summary: %s",
			bounded.ConfigSummary())
	}
	if !strings.Contains(bounded.ConfigSummary(), "sleep=1ms") {
		t.Fatalf("summary omits the configured bound: %s", bounded.ConfigSummary())
	}
	if !strings.Contains(unbounded.ConfigSummary(), "sleep=unlimited") {
		t.Fatalf("summary omits the unlimited bound: %s", unbounded.ConfigSummary())
	}
}

// bindSleepingCapability re-enters a second, unbounded engine from inside Bind,
// the way an adapter that resolves its surface by running a script would.
type bindSleepingCapability struct {
	inner *Script
	err   error
}

func (c *bindSleepingCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	_, c.err = c.inner.Call(binding.Context, "run", nil, CallOptions{})
	return map[string]Value{"probe": NewInt(1)}, nil
}

// TestCheckedCallBoundsSleepingInsideCapabilityBinding pins that the budget
// covers the static preflight, not only the execution after it.
//
// CheckedCall binds capabilities twice: once to resolve the globals the gate
// checks against, and again when the call it gates actually runs. The budget
// was established by Call, so the preflight bind ran without one, and an
// adapter that re-enters an unbounded engine on its binding context parked
// there indefinitely -- inside the API whose entire purpose is to refuse to
// execute anything it cannot vouch for (#29).
func TestCheckedCallBoundsSleepingInsideCapabilityBinding(t *testing.T) {
	t.Parallel()

	inner := compileScriptWithConfig(t, Config{MaxSleepDuration: Unlimited}, `def run()
  sleep(9223372036)
end`)
	adapter := &bindSleepingCapability{inner: inner}
	outer := compileScriptWithConfig(t, Config{MaxSleepDuration: 10 * time.Millisecond}, `def run()
  1
end`)

	done := make(chan error, 1)
	go func() {
		_, _, err := outer.CheckedCall(context.Background(), "run", nil,
			CallOptions{Capabilities: []CapabilityAdapter{adapter}})
		done <- err
	}()
	select {
	case <-done:
		if adapter.err == nil {
			t.Fatal("the nested call slept its whole duration: the preflight bind inherited no budget")
		}
		if !strings.Contains(adapter.err.Error(), "exceeds the") {
			t.Fatalf("the nested sleep failed for the wrong reason: %v", adapter.err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the preflight bind is still sleeping, so the gate did not bound it")
	}
}

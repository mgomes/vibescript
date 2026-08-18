package runtime

import (
	"context"
	"fmt"
	"testing"
)

// These tests pin the ephemeral-root reservation that closes issue #835: while a
// rest-destructuring block body runs, callBlock folds the marginal footprint of
// the iterator's Go-frame-only call roots (an ephemeral receiver such as
// make_hash().each) into the live baseline via reserveLoopScratch, so the body's
// per-statement and mutator-growth checks bound the COMBINED peak of the receiver
// plus whatever the body retains. Before the reservation, the receiver was
// counted only in the bind charge's one-time snapshot and the accumulator only by
// the body checks, so each view fit a quota the combined live footprint exceeded
// by up to ~2x -- and a script that dropped its accumulator on the final entry
// completed without any check ever observing the combined peak.

// ephemeralReceiverScenario returns a script whose run() iterates an ephemeral
// hash (reachable only from Hash#each's Go frame) with a nested-rest destructure,
// copying each value's tail into a retained outer accumulator. dropAcc adds the
// issue's evasion: releasing the accumulator on the final entry so no post-loop
// check can observe receiver and accumulator together. build() returns the same
// hash the loop iterates so tests can size quotas from its measured footprint.
func ephemeralReceiverScenario(keys, arrLen int, dropAcc bool) string {
	drop := ""
	if dropAcc {
		drop = fmt.Sprintf("\n    if acc.size == %d\n      acc = []\n    end", keys)
	}
	return fmt.Sprintf(`
def make_hash()
  h = {}
  i = 0
  while i < %d
    h["key_" + i.to_s] = (0..%d).to_a
    i = i + 1
  end
  h
end

def build()
  make_hash()
end

def run()
  acc = []
  make_hash().each do |(k, (h0, *t))|
    acc << t
    probe_entry(acc)%s
  end
  acc.size
end
`, keys, arrLen, drop)
}

// measureScenarioReceiverBytes builds the scenario's receiver under an ample
// quota and returns its estimated footprint, so the tests size their quotas from
// the real accounting rather than hand-derived constants.
// registerPureProbe installs a host builtin that neither mutates nor retains
// its arguments. Isolation would otherwise deep-copy an accumulator that still
// names tails from the iterated hash, doubling the live peak these tests size
// their quotas against.
func registerPureProbe(engine *Engine, name string, fn BuiltinFunc) {
	engine.registerHostBuiltin(name, DeclareNonRetaining(DeclareNonMutating(NewBuiltin(name, fn))))
}

func measureScenarioReceiverBytes(t *testing.T, source string) int {
	t.Helper()
	engine := MustNewEngine(Config{MemoryQuotaBytes: 1 << 30, StepQuota: 5_000_000})
	registerPureProbe(engine, "probe_entry", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		return NewNil(), nil
	})
	script, err := engine.Compile(source)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	receiver, err := script.Call(context.Background(), "build", nil, CallOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return newMemoryEstimator().value(receiver)
}

// TestEphemeralReceiverCombinedPeakTripsQuota pins that the combined footprint of
// an ephemeral receiver and a retained accumulator of destructured rest copies is
// rejected mid-loop once it would exceed the quota, even though the script drops
// the accumulator on the final entry so no post-loop check could catch it. The
// quota admits the receiver plus several tail copies (the loop must start), but
// the accumulator's legitimate combined need (~2x the receiver) exceeds it.
// Before the reservation this script completed, transiently holding ~2x the
// quota while both views (bind-charge snapshot; per-statement env walk) stayed
// under it.
func TestEphemeralReceiverCombinedPeakTripsQuota(t *testing.T) {
	t.Parallel()

	const keys = 8
	const arrLen = 600
	source := ephemeralReceiverScenario(keys, arrLen, true)
	receiverBytes := measureScenarioReceiverBytes(t, source)

	quota := receiverBytes + receiverBytes/2
	engine := MustNewEngine(Config{MemoryQuotaBytes: quota, StepQuota: 5_000_000})
	entries := 0
	registerPureProbe(engine, "probe_entry", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		entries++
		return NewNil(), nil
	})
	script, err := engine.Compile(source)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	_, err = script.Call(context.Background(), "run", nil, CallOptions{})
	requireErrorContains(t, err, "memory quota exceeded")
	requireRuntimeErrorType(t, err, runtimeErrorTypeLimit)
	if entries == 0 {
		t.Fatalf("loop was rejected before any entry ran; want the receiver and early copies admitted, with rejection only when the combined footprint would exceed the quota")
	}
	if entries >= keys {
		t.Fatalf("all %d entries ran; want the quota to trip mid-loop before the accumulator doubles the live footprint", keys)
	}
}

// TestEphemeralReceiverCombinedPeakWithinQuotaPasses pins both directions of the
// reservation's accounting: a combined footprint that genuinely fits (quota = 3x
// receiver, combined peak ~2.3x receiver) is admitted, and the body's own view of
// live memory during the loop -- exec.estimateMemoryUsage(), the walk every
// per-statement check reads -- observes the combined footprint (receiver plus
// accumulator, well above 1.5x receiver) while never exceeding the quota. Before
// the reservation the body view topped out near the accumulator alone (~1x
// receiver), blind to the ephemeral receiver it coexisted with.
func TestEphemeralReceiverCombinedPeakWithinQuotaPasses(t *testing.T) {
	t.Parallel()

	const keys = 8
	const arrLen = 600
	source := ephemeralReceiverScenario(keys, arrLen, false)
	receiverBytes := measureScenarioReceiverBytes(t, source)

	quota := 3 * receiverBytes
	engine := MustNewEngine(Config{MemoryQuotaBytes: quota, StepQuota: 5_000_000})
	peakBodyView := 0
	registerPureProbe(engine, "probe_entry", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		if view := exec.estimateMemoryUsage(); view > peakBodyView {
			peakBodyView = view
		}
		return NewNil(), nil
	})
	script, err := engine.Compile(source)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	result, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("combined footprint fits the quota, want the scenario admitted: %v", err)
	}
	if got := result.Int(); got != keys {
		t.Fatalf("run() = %d, want %d", got, keys)
	}
	if peakBodyView < receiverBytes+receiverBytes/2 {
		t.Fatalf("peak body view %d never observed the combined footprint (receiver %d + accumulator); the ephemeral receiver is invisible to body checks", peakBodyView, receiverBytes)
	}
	if peakBodyView > quota {
		t.Fatalf("peak body view %d exceeded the quota %d; the reservation over-charges the combined footprint", peakBodyView, quota)
	}
}

// TestEnvRootedReceiverNotDoubleChargedByReservation pins that the reservation
// deduplicates an environment-reachable receiver instead of charging it twice.
// The receiver is a function argument (an env root), so its marginal beyond the
// base walk is ~0 and the reservation adds nothing: a quota that fits receiver +
// accumulator with modest slack must admit the loop. A reservation that walked
// the receiver without deduplicating against the base would see ~3x the receiver
// and reject it.
func TestEnvRootedReceiverNotDoubleChargedByReservation(t *testing.T) {
	t.Parallel()

	const keys = 8
	const arrLen = 600
	buildSource := ephemeralReceiverScenario(keys, arrLen, false)
	receiverBytes := measureScenarioReceiverBytes(t, buildSource)

	source := buildSource + `
def run_rooted(h)
  acc = []
  h.each do |(k, (h0, *t))|
    acc << t
    probe_entry(acc)
  end
  acc.size
end
`
	quota := 2*receiverBytes + receiverBytes/2
	engine := MustNewEngine(Config{MemoryQuotaBytes: quota, StepQuota: 5_000_000})
	registerPureProbe(engine, "probe_entry", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		return NewNil(), nil
	})
	script, err := engine.Compile(source)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	receiver, err := script.Call(context.Background(), "build", nil, CallOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	result, err := script.Call(context.Background(), "run_rooted", []Value{receiver}, CallOptions{})
	if err != nil {
		t.Fatalf("env-rooted receiver + accumulator fit the quota, want the loop admitted: %v", err)
	}
	if got := result.Int(); got != keys {
		t.Fatalf("run_rooted() = %d, want %d", got, keys)
	}
}

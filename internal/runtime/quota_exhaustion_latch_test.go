package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestRescueLoopCannotOutliveStepQuota pins the core of #1136: a loop that
// rescues the quota error absorbed the signal that the budget was gone, so the
// script ran forever burning a fresh operation's worth of work per iteration.
// With exhaustion latched and unrescuable the loop terminates the moment the
// quota first trips; the test completing at all is the point.
func TestRescueLoopCannotOutliveStepQuota(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 200, MemoryQuotaBytes: Unlimited}, `
    def run()
      while true
        begin
          while true
          end
        rescue(LimitError)
          nil
        end
      end
    end
    `)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "step quota exceeded")
}

// TestRescueLoopCannotOutliveMemoryQuota is the strongest case: the memory
// quota measures the live reachable graph, so before the latch a loop that
// rescued the error and dropped the oversized value genuinely recovered and
// could exceed its budget forever, one allocation at a time. The step quota is
// unlimited here so only the memory latch can end the loop.
func TestRescueLoopCannotOutliveMemoryQuota(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: Unlimited, MemoryQuotaBytes: 256 << 10}, `
    def run()
      while true
        begin
          s = "x" * 1000000
        rescue(LimitError)
          s = nil
        end
      end
    end
    `)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "memory quota exceeded")
}

// TestTypedRescueDoesNotCatchGenuineExhaustion covers every clause type that
// used to swallow a real quota kill: bare rescue and StandardError already
// refused limit errors, but LimitError, RuntimeError, Error, and unions
// containing LimitError all matched. None may match once the budget is gone.
func TestTypedRescueDoesNotCatchGenuineExhaustion(t *testing.T) {
	t.Parallel()

	clauses := []string{
		"rescue(LimitError)",
		"rescue(RuntimeError)",
		"rescue(Error)",
		"rescue(AssertionError | LimitError)",
	}
	for _, clause := range clauses {
		t.Run(clause, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithConfig(t, Config{StepQuota: 60, MemoryQuotaBytes: Unlimited}, `
    def run()
      begin
        while true
        end
      `+clause+`
        "rescued"
      end
    end
    `)

			requireCallErrorContains(t, script, "run", nil, CallOptions{}, "step quota exceeded")
		})
	}

	t.Run("memory", func(t *testing.T) {
		t.Parallel()
		script := compileScriptWithConfig(t, Config{StepQuota: Unlimited, MemoryQuotaBytes: 256 << 10}, `
    def run()
      begin
        s = "x" * 1000000
      rescue(LimitError)
        "rescued"
      end
    end
    `)

		requireCallErrorContains(t, script, "run", nil, CallOptions{}, "memory quota exceeded")
	})
}

// TestFormatOutputCapRemainsRescuable pins the documented boundary between
// latched exhaustion and per-operation guards: format's fixed output cap
// describes one rejected rendering, not a spent budget, so rescue catches it
// and the function keeps working — unrescuability is promised only for the
// step quota, the memory quota, and string.scan's output cap.
func TestFormatOutputCapRemainsRescuable(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: Unlimited, MemoryQuotaBytes: Unlimited}, `
    def run()
      result = begin
        format("%2000000s", "x")
        "rendered"
      rescue(LimitError)
        "rescued"
      end
      i = 0
      while i < 100
        i = i + 1
      end
      result + "-" + i.to_s
    end
    `)

	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "rescued-100" {
		t.Fatalf("run() = %q, want %q", got.String(), "rescued-100")
	}
}

// TestForgedLimitErrorRemainsRescuable pins the boundary of the latch: a
// script-raised LimitError describes nothing about the budget, so it stays
// rescuable and the function keeps working afterwards — proof that no latch
// was set by the raise or the rescue.
func TestForgedLimitErrorRemainsRescuable(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 100_000}, `
    def run()
      result = begin
        raise LimitError, "synthetic"
      rescue(LimitError)
        "rescued"
      end
      i = 0
      while i < 100
        i = i + 1
      end
      result + "-" + i.to_s
    end
    `)

	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "rescued-100" {
		t.Fatalf("run() = %q, want %q", got.String(), "rescued-100")
	}
}

// TestStdlibGuardLimitsRemainRescuable pins that stdlib input guards — one
// rejected operation, not an exhausted sandbox — do not latch: the guard is
// rescued and the script continues with its remaining budget.
func TestStdlibGuardLimitsRemainRescuable(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 100_000}, `
    def run()
      result = begin
        random_id(2000)
      rescue(LimitError)
        "guarded"
      end
      i = 0
      while i < 100
        i = i + 1
      end
      result + "-" + i.to_s
    end
    `)

	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "guarded-100" {
		t.Fatalf("run() = %q, want %q", got.String(), "guarded-100")
	}
}

// TestRecursionLimitRemainsRescuableAndDoesNotLatch pins that the recursion
// cap stays a rescuable LimitError: the stack unwinds during propagation, so
// continuing with less depth is a legitimate recovery, unlike a spent budget.
func TestRecursionLimitRemainsRescuableAndDoesNotLatch(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 1_000_000}, `
    def recurse()
      recurse()
    end

    def run()
      result = begin
        recurse()
      rescue(LimitError)
        "limit"
      end
      i = 0
      while i < 100
        i = i + 1
      end
      result + "-" + i.to_s
    end
    `)

	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "limit-100" {
		t.Fatalf("run() = %q, want %q", got.String(), "limit-100")
	}
}

// TestEnsureCannotMaskMemoryExhaustion pins the ensure semantics under the
// latch. An ensure body's error replaces the propagating error, so before the
// latch this ensure downgraded a memory-quota kill into an ordinary rescuable
// RuntimeError("masking"). Now the ensure body's first statement charge
// re-raises the latched error, and the host sees the genuine exhaustion.
func TestEnsureCannotMaskMemoryExhaustion(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: Unlimited, MemoryQuotaBytes: 256 << 10}, `
    def run()
      begin
        s = "x" * 1000000
      ensure
        raise "masking"
      end
    end
    `)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "memory quota exceeded")

	// The diagnostics must keep pointing at the allocation that exhausted
	// the quota (line 3), not at the ensure statement the latch prevented
	// from running.
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatal("expected the exhaustion to surface")
	}
	if strings.Contains(err.Error(), "raise \"masking\"") {
		t.Fatalf("error diagnostics point at the unexecuted ensure statement: %v", err)
	}
}

// TestRetryCannotRerunAfterExhaustion pins that retry gives no re-entry after
// a genuine quota kill: the rescue clause never matches, so its retry is
// unreachable and the error propagates.
func TestRetryCannotRerunAfterExhaustion(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 200, MemoryQuotaBytes: Unlimited}, `
    def run()
      begin
        while true
        end
      rescue(LimitError)
        retry
      end
    end
    `)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "step quota exceeded")
}

// TestRescueModifierDoesNotAbsorbMemoryQuota extends the existing step-quota
// modifier test to the memory quota, which used to be recoverable: the
// modifier form must refuse a genuine exhaustion exactly like the statement
// form.
func TestRescueModifierDoesNotAbsorbMemoryQuota(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: Unlimited, MemoryQuotaBytes: 256 << 10}, `
    def blow()
      "x" * 1000000
    end

    def run()
      blow() rescue "fallback"
    end
    `)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "memory quota exceeded")
}

// TestScanOutputLimitLatches pins that the string.scan output cap — the one
// errOutputLimitExceeded site — latches like the quotas it stands beside: the
// projected output table is budget the script does not have.
func TestScanOutputLimitLatches(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: Unlimited, MemoryQuotaBytes: Unlimited}, `
    def run()
      s = "a" * 500000
      begin
        s.scan("a")
      rescue(LimitError)
        "rescued"
      end
    end
    `)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "scan output exceeds limit")
}

// TestMemoryExhaustionLatchesExecution is the adapter-swallow protection in
// miniature: after a tripped memory check, step() must fail with the same
// latched error even though the step quota itself has plenty of room and the
// periodic slow path would not have re-walked yet — so a capability adapter
// that swallowed the original error cannot let evaluation continue.
func TestMemoryExhaustionLatchesExecution(t *testing.T) {
	t.Parallel()

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1}
	requireErrorIs(t, exec.checkMemory(), errMemoryQuotaExceeded)
	requireErrorIs(t, exec.step(), errMemoryQuotaExceeded)
	requireErrorIs(t, exec.step(), errMemoryQuotaExceeded)
}

// TestCheckStepBudgetForLatches pins that the pre-flight latches like the
// per-element loop it stands in for: it fires exactly when that loop was
// guaranteed to exhaust the quota, so afterwards every charge must fail.
func TestCheckStepBudgetForLatches(t *testing.T) {
	t.Parallel()

	exec := &Execution{ctx: context.Background(), quota: 10}
	exec.steps = 5
	requireErrorIs(t, exec.checkStepBudgetFor(100), errStepQuotaExceeded)
	requireErrorIs(t, exec.step(), errStepQuotaExceeded)
}

// swallowingStepCapability exposes a builtin that burns the step quota through
// the exported Step surface, ignores every error it returns, and reports
// success — the adapter misbehavior the latch exists to contain.
type swallowingStepCapability struct{}

func (swallowingStepCapability) Bind(CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"cap": NewObject(map[string]Value{
			"swallow": NewBuiltin("cap.swallow", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
				for range 10_000 {
					_ = exec.Step()
				}
				return NewString("ok"), nil
			}),
		}),
	}, nil
}

// TestAdapterCannotSwallowExhaustion pins the host-visible termination
// guarantee against a capability adapter that ignores quota errors from the
// exported Step surface and returns a value — from the script's final
// expression, so no later statement charge would ever consult the latch. The
// dispatch return path must surface the latched error instead of the result.
func TestAdapterCannotSwallowExhaustion(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 100, MemoryQuotaBytes: Unlimited}, `
    def run()
      cap.swallow()
    end
    `)

	_, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{swallowingStepCapability{}},
	})
	if err == nil {
		t.Fatal("a swallowed exhaustion returned success to the host")
	}
	requireErrorContains(t, err, "step quota exceeded")
}

// maskingStepCapability burns the step quota through the exported Step
// surface, discards the quota error, and reports its own unrelated failure —
// an adapter must not be able to downgrade a quota termination into a
// generic error.
type maskingStepCapability struct{}

func (maskingStepCapability) Bind(CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"cap": NewObject(map[string]Value{
			"mask": NewBuiltin("cap.mask", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
				for range 10_000 {
					_ = exec.Step()
				}
				return NewNil(), fmt.Errorf("adapter broke")
			}),
		}),
	}, nil
}

// TestAdapterCannotMaskExhaustionWithAnotherError pins the other half of the
// dispatch latch check: an adapter that swallows the quota error and returns
// an unrelated error of its own must still surface the exhaustion, because
// rescue is disabled by the latch and the host was promised a LimitError.
func TestAdapterCannotMaskExhaustionWithAnotherError(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 100, MemoryQuotaBytes: Unlimited}, `
    def run()
      cap.mask()
    end
    `)

	_, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{maskingStepCapability{}},
	})
	if err == nil {
		t.Fatal("a masked exhaustion returned success to the host")
	}
	requireErrorContains(t, err, "step quota exceeded")
}

// forgingStepCapability burns the step quota, discards the quota error, and
// returns a synthetic limit-classified error of its own — classification
// alone must not let an adapter substitute its message for the genuine
// termination.
type forgingStepCapability struct{}

func (forgingStepCapability) Bind(CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"cap": NewObject(map[string]Value{
			"forge": NewBuiltin("cap.forge", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
				for range 10_000 {
					_ = exec.Step()
				}
				return NewNil(), &RuntimeError{Type: runtimeErrorTypeLimit, Message: "synthetic limit"}
			}),
		}),
	}, nil
}

// TestAdapterCannotForgeLimitErrorOverExhaustion pins that a synthetic
// limit-classified adapter error does not mask the latched exhaustion: only
// an error that carries the actual latched message survives the dispatch
// check.
func TestAdapterCannotForgeLimitErrorOverExhaustion(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 100, MemoryQuotaBytes: Unlimited}, `
    def run()
      cap.forge()
    end
    `)

	_, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{forgingStepCapability{}},
	})
	if err == nil {
		t.Fatal("a forged limit error returned success to the host")
	}
	requireErrorContains(t, err, "step quota exceeded")
}

// echoingStepCapability burns the step quota and returns a fresh error whose
// text happens to embed the quota message — message matching must not be the
// thing that lets an error stand in for the latch.
type echoingStepCapability struct{}

func (echoingStepCapability) Bind(CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"cap": NewObject(map[string]Value{
			"echo": NewBuiltin("cap.echo", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
				var last error
				for range 10_000 {
					if err := exec.Step(); err != nil {
						last = err
					}
				}
				return NewNil(), fmt.Errorf("cleanup failed after %v", last) //nolint:errorlint // deliberately non-wrapping: the test pins that message text alone cannot stand in for the latch
			}),
		}),
	}, nil
}

// TestAdapterErrorEchoingQuotaMessageIsOverridden pins that an unrelated
// error whose text merely contains the quota message does not survive the
// dispatch check: the host receives the latched error itself, not the
// adapter's wrapper narrative.
func TestAdapterErrorEchoingQuotaMessageIsOverridden(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 100, MemoryQuotaBytes: Unlimited}, `
    def run()
      cap.echo()
    end
    `)

	_, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{echoingStepCapability{}},
	})
	if err == nil {
		t.Fatal("an echoed exhaustion returned success to the host")
	}
	requireErrorContains(t, err, "step quota exceeded")
	if strings.Contains(err.Error(), "cleanup failed") {
		t.Fatalf("error = %v, want the latched exhaustion rather than the adapter's wrapper", err)
	}
}

// TestTaskWorkerExhaustionIsNotRescuable pins the task boundary: a worker
// runs under its own execution, so its genuine quota kill reaches the parent
// as a wrapped error rather than through the parent's latch — and the rescue
// gate must refuse it by its unforgeable credential, or a surrounding
// rescue(LimitError) absorbs a genuine termination.
func TestTaskWorkerExhaustionIsNotRescuable(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 5_000, MemoryQuotaBytes: Unlimited}, `
    def spin(x)
      while true
      end
    end

    def run()
      begin
        Tasks.map([1], with: :spin)
      rescue(LimitError)
        "rescued"
      end
    end
    `)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "step quota exceeded")
	// The trusted channel carries the worker's diagnostics: the host error
	// must name the worker function and the task context, not only the
	// Tasks.map call site.
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "task spin failed")
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "at spin")
}

// TestTaskWorkerForgedLimitErrorRemainsRescuable pins the boundary of the
// task credential: a worker that raises LimitError itself was not quota
// killed, so the parent may still rescue it.
func TestTaskWorkerForgedLimitErrorRemainsRescuable(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 100_000, MemoryQuotaBytes: Unlimited}, `
    def fail(x)
      raise LimitError, "synthetic"
    end

    def run()
      begin
        Tasks.map([1], with: :fail)
      rescue(LimitError)
        "rescued"
      end
    end
    `)

	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "rescued" {
		t.Fatalf("run() = %q, want %q", got.String(), "rescued")
	}
}

// swallowingBlockCapability drives its block through CallBlock and reports
// success no matter what the block returned — the composition of the adapter
// and task attack surfaces.
type swallowingBlockCapability struct{}

func (swallowingBlockCapability) Bind(CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"cap": NewObject(map[string]Value{
			"swallow": NewBuiltin("cap.swallow", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, block Value) (Value, error) {
				_, _ = exec.CallBlock(block, nil)
				return NewString("ok"), nil
			}),
		}),
	}, nil
}

// TestAdapterCannotSwallowTaskExhaustion pins the composed case: a task
// worker exhausts inside an adapter-driven block, and the adapter discards
// the CallBlock error. The parent latch must have been set when the worker's
// authenticated exhaustion crossed the task boundary, so the dispatch check
// still surfaces it.
func TestAdapterCannotSwallowTaskExhaustion(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 5_000, MemoryQuotaBytes: Unlimited}, `
    def spin(x)
      while true
      end
    end

    def run()
      cap.swallow() { Tasks.map([1], with: :spin) }
    end
    `)

	_, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{swallowingBlockCapability{}},
	})
	if err == nil {
		t.Fatal("a swallowed task exhaustion returned success to the host")
	}
	requireErrorContains(t, err, "step quota exceeded")
}

// tamperingStepCapability lets its block exhaust the quota, shallow-copies
// the propagated RuntimeError — which preserves the unexported credential in
// Go — rewrites its exported fields, and returns the copy.
type tamperingStepCapability struct{}

func (tamperingStepCapability) Bind(CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"cap": NewObject(map[string]Value{
			"tamper": NewBuiltin("cap.tamper", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, block Value) (Value, error) {
				_, err := exec.CallBlock(block, nil)
				if re, ok := errors.AsType[*RuntimeError](err); ok {
					tampered := *re
					tampered.Type = "TypeError"
					tampered.Message = "tampered"
					return NewNil(), &tampered
				}
				return NewNil(), err
			}),
		}),
	}, nil
}

// TestAdapterCannotTamperAuthenticatedExhaustion pins that the credential
// authorizes only location data: the surfaced error's class and message are
// rebuilt from the latch, so a copied-and-rewritten RuntimeError cannot
// substitute its own metadata for the quota termination.
func TestAdapterCannotTamperAuthenticatedExhaustion(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 200, MemoryQuotaBytes: Unlimited}, `
    def run()
      cap.tamper() { while true do end }
    end
    `)

	_, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{tamperingStepCapability{}},
	})
	if err == nil {
		t.Fatal("a tampered exhaustion returned success to the host")
	}
	requireErrorContains(t, err, "step quota exceeded")
	requireRuntimeErrorType(t, err, runtimeErrorTypeLimit)
}

// TestGroupRetainsExhaustionBehindFirstError pins the group's separate
// exhaustion retention with the arrival order a script cannot produce
// deterministically (the first failure cancels the group, racing the
// spinner's kill): an ordinary failure wins the first-error slot, a worker's
// exhaustion arrives second through the trusted channel, and the parent must
// still latch so rescue refuses everything.
func TestGroupRetainsExhaustionBehindFirstError(t *testing.T) {
	t.Parallel()

	group := &taskGroup{cancel: func() {}}
	group.recordErr(errors.New("ordinary failure"))

	worker := &Execution{ctx: context.Background(), quota: 1}
	worker.steps = 1
	quotaErr := worker.step()
	requireErrorIs(t, quotaErr, errStepQuotaExceeded)
	group.recordErr(fmt.Errorf("task worker failed: %w", quotaErr))
	group.recordExhaustion(worker.exhausted)

	if group.exhaustion() == nil {
		t.Fatal("the group discarded a worker exhaustion reported after an ordinary failure")
	}
	requireErrorContains(t, group.err(), "ordinary failure")

	parent := &Execution{ctx: context.Background()}
	requireErrorContains(t, parent.latchGroupTaskExhaustion(group, group.err()), "ordinary failure")
	if parent.exhausted == nil {
		t.Fatal("the parent latch was not set from the group's retained exhaustion")
	}
	if parent.canRescueRuntimeError(errors.New("ordinary failure"), nil) {
		t.Fatal("a latched parent must refuse every rescue")
	}
}

// TestEnqueueReobservesExhaustionAfterClone pins the admission-side gap: the
// spawn entry check predates the payload clone, so a worker exhaustion
// published during the clone must be re-observed before the job is admitted
// to the queue — otherwise the enqueue spends a fresh worker budget after
// the kill.
func TestEnqueueReobservesExhaustionAfterClone(t *testing.T) {
	t.Parallel()

	group := &taskGroup{cancel: func() {}, jobs: make(chan *taskJob, 1)}
	worker := &Execution{ctx: context.Background(), quota: 1}
	worker.steps = 1
	requireErrorIs(t, worker.step(), errStepQuotaExceeded)
	group.recordExhaustion(fmt.Errorf("task work failed: %w", worker.exhausted))

	parent := &Execution{ctx: context.Background()}
	handle, err := group.enqueue(parent, "f", nil, NewInt(1), true, nil)
	if err == nil || handle != nil {
		t.Fatalf("enqueue = (%v, %v), want a refused admission after an observed exhaustion", handle, err)
	}
	requireErrorContains(t, err, "task work failed")
	if len(group.jobs) != 0 {
		t.Fatal("a refused admission must not enqueue a job")
	}
	if parent.exhausted == nil {
		t.Fatal("the parent latch was not set by the re-observation")
	}
}

// TestObservedExhaustionStopsSpawnsBeforeGroupError pins the publish window a
// worker leaves between recordExhaustion and recordErr: a spawn that reads a
// nil group error while the exhaustion is already visible must be refused,
// not allowed to keep cloning and enqueuing jobs — each with a fresh worker
// budget — after the quota kill.
func TestObservedExhaustionStopsSpawnsBeforeGroupError(t *testing.T) {
	t.Parallel()

	group := &taskGroup{cancel: func() {}}
	worker := &Execution{ctx: context.Background(), quota: 1}
	worker.steps = 1
	quotaErr := worker.step()
	requireErrorIs(t, quotaErr, errStepQuotaExceeded)
	// The worker has published its exhaustion but not yet its group error —
	// the window between runJob's two lock acquisitions.
	group.recordExhaustion(fmt.Errorf("task work failed: %w", worker.exhausted))

	parent := &Execution{ctx: context.Background()}
	err := parent.latchGroupTaskExhaustion(group, group.err())
	if err == nil {
		t.Fatal("a spawn observing the exhaustion behind a nil group error must be refused")
	}
	requireErrorContains(t, err, "task work failed")
	if parent.exhausted == nil {
		t.Fatal("the parent latch was not set from the observed exhaustion")
	}
}

// replayingCapability saves whatever error its block produces on the first
// call and returns the saved error on the second — the stale-credential
// replay a stateful adapter could attempt across Script.Calls.
type replayingCapability struct {
	saved *error
}

func (c replayingCapability) Bind(CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"cap": NewObject(map[string]Value{
			"replay": NewBuiltin("cap.replay", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, block Value) (Value, error) {
				if *c.saved != nil {
					return NewNil(), *c.saved
				}
				_, err := exec.CallBlock(block, nil)
				*c.saved = err
				return NewNil(), err
			}),
		}),
	}, nil
}

// TestStaleExhaustionCredentialIsRescuable pins the credential's execution
// binding: a marked error saved from an earlier call's genuine kill and
// replayed in a fresh call — whose budget is intact — authenticates against
// nothing, stays rescuable, and cannot manufacture a termination.
func TestStaleExhaustionCredentialIsRescuable(t *testing.T) {
	t.Parallel()

	var saved error
	cap := replayingCapability{saved: &saved}
	script := compileScriptWithConfig(t, Config{StepQuota: 5_000, MemoryQuotaBytes: Unlimited}, `
    def run()
      begin
        cap.replay() { while true do end }
      rescue(LimitError)
        "rescued"
      end
    end
    `)

	// First call: the block genuinely exhausts; the kill must reach the host.
	_, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{cap},
	})
	if err == nil {
		t.Fatal("the first call's genuine exhaustion returned success")
	}
	requireErrorContains(t, err, "step quota exceeded")
	if saved == nil {
		t.Fatal("the adapter failed to capture the propagated error")
	}

	// Second call: the adapter replays the stale credential without running
	// the block. The fresh execution's budget is intact, so the error is an
	// ordinary rescuable failure.
	got, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{cap},
	})
	if err != nil {
		t.Fatalf("a replayed stale credential terminated a healthy call: %v", err)
	}
	if got.String() != "rescued" {
		t.Fatalf("run() = %q, want %q", got.String(), "rescued")
	}
}

// TestRebuiltExhaustionMessageStaysCanonical pins the rebuilt error's shape:
// the programmatic Message must be the single-line quota message, not the
// task wrapper's rendering with the worker's frames embedded beside the
// separately copied frame fields.
func TestRebuiltExhaustionMessageStaysCanonical(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 5_000, MemoryQuotaBytes: Unlimited}, `
    def spin(x)
      while true
      end
    end

    def run()
      cap.swallow() { Tasks.map([1], with: :spin) }
    end
    `)

	_, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{swallowingBlockCapability{}},
	})
	if err == nil {
		t.Fatal("a swallowed task exhaustion returned success to the host")
	}
	var re *RuntimeError
	if errors.As(err, &re) {
		if strings.Contains(re.Message, "\n  at ") {
			t.Fatalf("Message embeds rendered frames: %q", re.Message)
		}
	}
	requireErrorContains(t, err, "quota exceeded")
}

// aggregatingStepCapability lets its block exhaust the quota and returns the
// propagated error joined behind an unrelated RuntimeError, the shape that
// used to make the first-match walk pick the unmarked branch and preserve
// the aggregate's synthetic metadata.
type aggregatingStepCapability struct{}

func (aggregatingStepCapability) Bind(CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"cap": NewObject(map[string]Value{
			"aggregate": NewBuiltin("cap.aggregate", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, block Value) (Value, error) {
				_, err := exec.CallBlock(block, nil)
				synthetic := &RuntimeError{Type: "TypeError", Message: "synthetic aggregate"}
				return NewNil(), errors.Join(synthetic, err)
			}),
		}),
	}, nil
}

// TestAdapterCannotAggregateAwayExhaustion pins the aggregate case: the
// marked RuntimeError sits on a later branch than an unrelated one, and the
// host must still receive the canonical quota termination rather than the
// aggregate's synthetic metadata.
func TestAdapterCannotAggregateAwayExhaustion(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 200, MemoryQuotaBytes: Unlimited}, `
    def run()
      cap.aggregate() { while true do end }
    end
    `)

	_, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{aggregatingStepCapability{}},
	})
	if err == nil {
		t.Fatal("an aggregated exhaustion returned success to the host")
	}
	requireErrorContains(t, err, "step quota exceeded")
	requireRuntimeErrorType(t, err, runtimeErrorTypeLimit)
	if strings.Contains(err.Error(), "synthetic aggregate") {
		t.Fatalf("error = %v, want the canonical termination rather than the aggregate", err)
	}
}

// propagatingStepCapability burns the quota through the exported Step
// surface and propagates the raw error it received — the well-behaved
// adapter, whose kill must still carry call-site diagnostics even though no
// wrapError ran before dispatch saw it.
type propagatingStepCapability struct{}

func (propagatingStepCapability) Bind(CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"cap": NewObject(map[string]Value{
			"burn": NewBuiltin("cap.burn", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
				for range 10_000 {
					if err := exec.Step(); err != nil {
						return NewNil(), err
					}
				}
				return NewString("ok"), nil
			}),
		}),
	}, nil
}

// TestStepExhaustionKeepsCallSiteFrames pins that a raw Step exhaustion —
// which never passed through wrapError — is rebuilt with the capability call
// site's frames rather than surfacing frameless.
func TestStepExhaustionKeepsCallSiteFrames(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 100, MemoryQuotaBytes: Unlimited}, `
    def run()
      cap.burn()
    end
    `)

	_, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{propagatingStepCapability{}},
	})
	if err == nil {
		t.Fatal("a propagated step exhaustion returned success")
	}
	requireErrorContains(t, err, "step quota exceeded")
	var re *RuntimeError
	if !errors.As(err, &re) {
		t.Fatalf("error type = %T, want *RuntimeError", err)
	}
	if len(re.Frames) == 0 && re.CodeFrame == "" {
		t.Fatalf("rebuilt exhaustion carries no diagnostics: %+v", re)
	}
}

// TestSwallowedBuiltinExhaustionKeepsItsSite pins the nested-dispatch case:
// exhaustion originating inside a builtin the adapter's block invoked must
// keep pointing at that builtin's call site even when the adapter swallows
// the error and the outer dispatch rebuilds — the innermost fallback's
// constructed diagnostics are snapshotted for it.
func TestSwallowedBuiltinExhaustionKeepsItsSite(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 100, MemoryQuotaBytes: Unlimited}, `
    def run(s)
      cap.swallow() { s.scan("a") }
    end
    `)

	payload := NewString(strings.Repeat("ab", 1<<12))
	_, err := script.Call(context.Background(), "run", []Value{payload}, CallOptions{
		Capabilities: []CapabilityAdapter{swallowingBlockCapability{}},
	})
	if err == nil {
		t.Fatal("a swallowed builtin exhaustion returned success")
	}
	requireErrorContains(t, err, "step quota exceeded")
	requireErrorContains(t, err, "s.scan")
}

// TestEnsureTriggeredExhaustionReplacesTheOriginal pins the other direction
// of the ensure rule: when the protected body fails with an ordinary error
// and the ensure body is where the quota first exhausts, the kill must reach
// the host rather than hiding behind the body's error.
func TestEnsureTriggeredExhaustionReplacesTheOriginal(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 500, MemoryQuotaBytes: Unlimited}, `
    def run()
      begin
        raise "original"
      ensure
        while true
        end
      end
    end
    `)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "step quota exceeded")
}

// cancelingStepCapability burns the quota through the exported Step surface,
// cancels the execution context, and reports success — the adapter that
// tries to downgrade its quota kill into a cancellation.
type cancelingStepCapability struct {
	cancelCall context.CancelFunc
}

func (c cancelingStepCapability) Bind(CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"cap": NewObject(map[string]Value{
			"burnAndCancel": NewBuiltin("cap.burnAndCancel", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
				for range 10_000 {
					_ = exec.Step()
				}
				c.cancelCall()
				return NewString("ok"), nil
			}),
		}),
	}, nil
}

// TestAdapterCannotDowngradeExhaustionToCancellation pins the ordering of the
// entry-call backstop: latched exhaustion is surfaced before the context
// check, so an adapter that also canceled the context still delivers the
// promised quota termination rather than context.Canceled.
func TestAdapterCannotDowngradeExhaustionToCancellation(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 100, MemoryQuotaBytes: Unlimited}, `
    def run()
      cap.burnAndCancel()
    end
    `)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := script.Call(ctx, "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{cancelingStepCapability{cancelCall: cancel}},
	})
	if err == nil {
		t.Fatal("a canceled-and-swallowed exhaustion returned success")
	}
	requireErrorContains(t, err, "step quota exceeded")
}

// TestSoftCapacityProbesDoNotLatch pins that the internal fits-style probes —
// here the comparison memo reservation, which falls back to memo-less
// comparison when the memo does not fit — never latch the execution: the
// comparison still answers and the call succeeds.
func TestSoftCapacityProbesDoNotLatch(t *testing.T) {
	t.Parallel()

	// The quota is large enough to compare two small arrays but too small for
	// the memo's ~90KB reservation, so ensureMemo's probe must decline without
	// ending the run (mirrors TestComparisonWithoutRoomForTheMemoStillAnswers,
	// which guards the same path against the latch regressing it).
	script := compileScriptWithConfig(t, Config{StepQuota: 100_000, MemoryQuotaBytes: 40 << 10}, `
    def run()
      a = [[1, 2], [3]]
      b = [[1, 2], [3]]
      (a <=> b).inspect
    end
    `)

	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "0" {
		t.Fatalf("run() = %q, want %q", got.String(), "0")
	}
}

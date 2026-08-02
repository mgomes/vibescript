package runtime

import (
	"context"
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

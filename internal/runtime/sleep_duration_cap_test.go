package runtime

import (
	"context"
	"strings"
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
		if !strings.Contains(err.Error(), "exceeds the host maximum") {
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
	if err == nil || !strings.Contains(err.Error(), "exceeds the host maximum") {
		t.Fatalf("a tightened maximum must reject a 30s sleep, got %v", err)
	}
	if !strings.Contains(err.Error(), "1s") {
		t.Fatalf("the error must name the host's own maximum, got %v", err)
	}
}

// TestUnlimitedSleepMaximumOptsOut pins that a host can remove the bound.
//
// The sleep is not waited out: the context is cancelled immediately, so a
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
		t.Fatal("a cancelled context must still end the call")
	}
	if strings.Contains(err.Error(), "exceeds the host maximum") {
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
		if !strings.Contains(err.Error(), "exceeds the host maximum") {
			t.Fatalf("rejected for the wrong reason: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the call is still sleeping, so the budget did not bound it")
	}
}

package main

import (
	"flag"
	"strings"
	"testing"

	"github.com/mgomes/vibescript/vibes"
)

// resolveQuotaFlags parses args through a fresh flag set with the quota flags
// registered, mirroring how the real commands wire them up.
func resolveQuotaFlags(t *testing.T, args []string) (quotaConfig, error) {
	t.Helper()
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(new(flagErrorSink))
	q := registerQuotaFlags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return q.resolve()
}

func TestQuotaFlagsDefaultToXHigh(t *testing.T) {
	t.Parallel()

	got, err := resolveQuotaFlags(t, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := quotaConfig{
		stepQuota:      vibes.ProfileXHigh.StepQuota,
		memoryQuota:    vibes.ProfileXHigh.MemoryQuotaBytes,
		recursionLimit: vibes.ProfileXHigh.RecursionLimit,
	}
	if got != want {
		t.Fatalf("default quota = %+v, want xhigh %+v", got, want)
	}
}

func TestQuotaFlagsSelectProfile(t *testing.T) {
	t.Parallel()

	got, err := resolveQuotaFlags(t, []string{"-profile", "low"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.stepQuota != vibes.ProfileLow.StepQuota || got.memoryQuota != vibes.ProfileLow.MemoryQuotaBytes || got.recursionLimit != vibes.ProfileLow.RecursionLimit {
		t.Fatalf("profile low = %+v, want %+v", got, vibes.ProfileLow)
	}
}

func TestQuotaFlagsOverridesLayerOnProfile(t *testing.T) {
	t.Parallel()

	got, err := resolveQuotaFlags(t, []string{"-profile", "low", "-step-quota", "-1", "-recursion-limit", "5000"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.stepQuota != vibes.Unlimited {
		t.Fatalf("step-quota override = %d, want unlimited", got.stepQuota)
	}
	if got.recursionLimit != 5000 {
		t.Fatalf("recursion-limit override = %d, want 5000", got.recursionLimit)
	}
	// Memory was not overridden, so it keeps the profile's value.
	if got.memoryQuota != vibes.ProfileLow.MemoryQuotaBytes {
		t.Fatalf("memory quota = %d, want profile low %d", got.memoryQuota, vibes.ProfileLow.MemoryQuotaBytes)
	}
}

func TestQuotaFlagsUnknownProfile(t *testing.T) {
	t.Parallel()

	_, err := resolveQuotaFlags(t, []string{"-profile", "gigantic"})
	if err == nil {
		t.Fatal("resolve unknown profile err = nil, want error")
	}
	if !strings.Contains(err.Error(), "unknown quota profile") {
		t.Fatalf("err = %v, want unknown-profile message", err)
	}
}

// TestRunCommandProfileEnforcement is an end-to-end check that the selected
// profile actually reaches execution: a step-heavy loop runs under the default
// xhigh profile but trips the step quota under low.
func TestRunCommandProfileEnforcement(t *testing.T) {
	script := `
def count(n)
  i = 0
  while i < n
    i = i + 1
  end
  i
end

puts count(2000000)
`
	path := writeVibeScript(t, script)

	out, err := captureStdout(t, func() error {
		return runCommand([]string{path})
	})
	if err != nil {
		t.Fatalf("default (xhigh) run err = %v, want nil", err)
	}
	if got := strings.TrimSpace(out); got != "2000000" {
		t.Fatalf("default run stdout = %q, want 2000000", got)
	}

	_, err = captureStdout(t, func() error {
		return runCommand([]string{"-profile", "low", path})
	})
	if err == nil {
		t.Fatal("low-profile run err = nil, want step quota exceeded")
	}
	if !strings.Contains(err.Error(), "step quota exceeded") {
		t.Fatalf("low-profile run err = %v, want step quota exceeded", err)
	}
}

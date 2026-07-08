package runtime

import (
	"context"
	"testing"
)

func TestResolveQuotaSemantics(t *testing.T) {
	t.Parallel()

	const def = 42
	tests := []struct {
		name  string
		value int
		want  int
	}{
		{"zero selects default", 0, def},
		{"positive is explicit", 7, 7},
		{"unlimited disables", Unlimited, 0},
		{"any negative disables", -99, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveQuota(tt.value, def); got != tt.want {
				t.Fatalf("resolveQuota(%d, %d) = %d, want %d", tt.value, def, got, tt.want)
			}
		})
	}
}

func TestNewEngineDistinguishesUnlimitedFromDefault(t *testing.T) {
	t.Parallel()

	defaults := MustNewEngine(Config{})
	if got := defaults.config.MemoryQuotaBytes; got != defaultMemoryQuotaBytes {
		t.Fatalf("zero MemoryQuotaBytes resolved to %d, want default %d", got, defaultMemoryQuotaBytes)
	}
	if got := defaults.config.StepQuota; got != defaultStepQuota {
		t.Fatalf("zero StepQuota resolved to %d, want default %d", got, defaultStepQuota)
	}

	unlimited := MustNewEngine(Config{StepQuota: Unlimited, MemoryQuotaBytes: Unlimited, RecursionLimit: Unlimited})
	if got := unlimited.config.MemoryQuotaBytes; got != 0 {
		t.Fatalf("unlimited MemoryQuotaBytes resolved to %d, want 0 (disabled)", got)
	}
	if got := unlimited.config.StepQuota; got != 0 {
		t.Fatalf("unlimited StepQuota resolved to %d, want 0 (disabled)", got)
	}
	if got := unlimited.config.RecursionLimit; got != 0 {
		t.Fatalf("unlimited RecursionLimit resolved to %d, want 0 (disabled)", got)
	}
}

// TestUnlimitedQuotaRunsUnboundedWork proves the sentinel actually lifts the
// step and memory ceilings: a loop that far exceeds the default step quota
// completes only because the quotas are disabled.
func TestUnlimitedQuotaRunsUnboundedWork(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{StepQuota: Unlimited, MemoryQuotaBytes: Unlimited})
	script, err := engine.CompileSnippet(`
def sum_to(n)
  total = 0
  i = 0
  while i < n
    total = total + i
    i = i + 1
  end
  total
end

sum_to(200000)
`, "__main__")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := script.Call(context.Background(), "__main__", nil, CallOptions{}); err != nil {
		t.Fatalf("call under unlimited quotas: %v", err)
	}
}

func TestQuotaProfileByName(t *testing.T) {
	t.Parallel()

	for _, want := range quotaProfiles {
		got, ok := QuotaProfileByName(want.Name)
		if !ok {
			t.Fatalf("profile %q not found", want.Name)
		}
		if got != want {
			t.Fatalf("profile %q = %+v, want %+v", want.Name, got, want)
		}
	}

	if got, ok := QuotaProfileByName("  XHigh "); !ok || got != ProfileXHigh {
		t.Fatalf("case-insensitive trimmed lookup = %+v ok=%t, want xhigh", got, ok)
	}
	if _, ok := QuotaProfileByName("nonexistent"); ok {
		t.Fatalf("unknown profile reported as found")
	}
}

func TestQuotaProfileLadderIsOrdered(t *testing.T) {
	t.Parallel()

	// xhigh is the top rung: unlimited steps and memory, finite recursion.
	if ProfileXHigh.StepQuota != Unlimited || ProfileXHigh.MemoryQuotaBytes != Unlimited {
		t.Fatalf("xhigh must be unlimited on steps and memory, got %+v", ProfileXHigh)
	}
	for _, p := range quotaProfiles {
		if p.RecursionLimit <= 0 {
			t.Fatalf("profile %q leaves recursion uncapped (%d); no profile may", p.Name, p.RecursionLimit)
		}
	}

	// The bounded rungs ascend in every dimension.
	bounded := []QuotaProfile{ProfileLow, ProfileMedium, ProfileHigh}
	for i := 1; i < len(bounded); i++ {
		prev, cur := bounded[i-1], bounded[i]
		if cur.StepQuota <= prev.StepQuota || cur.MemoryQuotaBytes <= prev.MemoryQuotaBytes || cur.RecursionLimit <= prev.RecursionLimit {
			t.Fatalf("profile %q does not exceed %q on every dimension", cur.Name, prev.Name)
		}
	}
}

func TestQuotaProfileApplyTo(t *testing.T) {
	t.Parallel()

	cfg := Config{StrictEffects: true, MaxSourceBytes: 123}
	ProfileHigh.ApplyTo(&cfg)

	if cfg.StepQuota != ProfileHigh.StepQuota || cfg.MemoryQuotaBytes != ProfileHigh.MemoryQuotaBytes || cfg.RecursionLimit != ProfileHigh.RecursionLimit {
		t.Fatalf("ApplyTo did not set quota fields: %+v", cfg)
	}
	if !cfg.StrictEffects || cfg.MaxSourceBytes != 123 {
		t.Fatalf("ApplyTo clobbered non-quota fields: %+v", cfg)
	}
}

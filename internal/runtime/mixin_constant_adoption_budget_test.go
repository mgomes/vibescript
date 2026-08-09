package runtime

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// mixinConstantAdoptionSource builds one module of constants and a run of
// bodyless classes that include it, so class-body initialization performs
// classes*constants constant adoptions from a source that spells each constant
// once.
func mixinConstantAdoptionSource(classes, constants int) string {
	var b strings.Builder
	b.WriteString("module Limits\n")
	for i := range constants {
		fmt.Fprintf(&b, "  CONSTANT_NUMBER_%06d = %d\n", i, i)
	}
	b.WriteString("end\n")
	for i := range classes {
		fmt.Fprintf(&b, "class Holder%06d\n  include Limits\nend\n", i)
	}
	b.WriteString("\ndef run\n  1\nend\n")
	return b.String()
}

// TestMixinConstantAdoptionChargesSteps pins that adopting a module's constants
// into an including class is metered work. Every class with an included module
// runs class-body initialization even with no body of its own, and the adoption
// loops used to charge nothing at all: 10,000 adoptions completed under a
// 5,000-step quota (#23).
func TestMixinConstantAdoptionChargesSteps(t *testing.T) {
	t.Parallel()

	const (
		classes   = 100
		constants = 100
		quota     = 5_000
	)
	script := compileScriptWithConfig(t,
		Config{StepQuota: quota, MemoryQuotaBytes: Unlimited},
		mixinConstantAdoptionSource(classes, constants))

	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("%d constant adoptions completed under a %d step quota; the adoption is not charged", classes*constants, quota)
	}
	requireErrorContains(t, err, "step quota exceeded")
}

// TestMixinConstantAdoptionStopsAtTheMemoryQuota pins that the adoption stops
// allocating once the entries it has added fill the memory quota, instead of
// building the whole classes*constants map population and only then meeting a
// check. The entries are permanent class constants, so a 139KB script that
// adopted 1.2M of them allocated 263MB before the first check ran; scaling the
// class count now leaves the allocation flat because both runs stop at the same
// quota (#23).
func TestMixinConstantAdoptionStopsAtTheMemoryQuota(t *testing.T) {
	adoptionBytes := func(classes int) uint64 {
		script := compileScriptWithConfig(t, Config{StepQuota: Unlimited},
			mixinConstantAdoptionSource(classes, 4000))
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := script.Call(context.Background(), "run", nil, CallOptions{})
		runtime.ReadMemStats(&after)
		if err == nil {
			t.Fatalf("%d classes adopting 4000 constants each stayed within the memory quota", classes)
		}
		requireErrorContains(t, err, "memory quota exceeded")
		return after.TotalAlloc - before.TotalAlloc
	}

	few := adoptionBytes(100)
	many := adoptionBytes(400)
	if estimatorVerify {
		// The oracle recomputes a full reference walk on every check, so under it
		// the allocation total measures the verification rather than the
		// adoption: it grows with the checks the fix added and with the graph
		// they walk. The rejections above still run here; only their cost is
		// unreadable.
		t.Logf("estimator oracle enabled: skipping the scaling comparison (%d and %d bytes)", few, many)
		return
	}
	// Four times the classes adopt four times the constants, but both runs are
	// rejected by the same quota, so both stop having allocated the same amount.
	// Unmetered, the second run allocated proportionally more.
	if limit := 2 * few; many > limit {
		t.Fatalf("400 including classes allocated %d bytes, want at most %d (%d bytes for 100 classes)", many, limit, few)
	}
}

// TestOrdinaryMixinConstantsStayWithinDefaultQuotas pins that the metering
// above leaves a normal mixin alone: its constants are adopted, readable both
// scoped and through an included method, and cost nothing a default-profile
// call notices.
func TestOrdinaryMixinConstantsStayWithinDefaultQuotas(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
module Limits
  MAX = 9
  LABEL = "limit"
end

class Config
  include Limits

  def describe
    "#{LABEL}:#{MAX}"
  end
end

def run
  [Config::MAX, Config::LABEL, Config.new.describe]
end
`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{}).Array()
	want := []Value{NewInt(9), NewString("limit"), NewString("limit:9")}
	if len(got) != len(want) {
		t.Fatalf("run returned %d values, want %d", len(got), len(want))
	}
	for i, w := range want {
		if !got[i].Equal(w) {
			t.Fatalf("run[%d] = %v, want %v", i, got[i], w)
		}
	}
}

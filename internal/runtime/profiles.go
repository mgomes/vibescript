package runtime

import (
	"strings"
	"time"
)

// QuotaProfile is a named bundle of the execution quotas: step, memory,
// recursion, and the time a call may spend sleeping. Profiles let a host or the CLI select a coherent budget by
// name instead of tuning each quota independently. A profile's quota values use
// the same conventions as Config: a positive value is an explicit limit and
// Unlimited disables that quota.
//
// The ladder runs low -> medium -> high -> xhigh. The lower rungs model a
// constrained embedded sandbox budget; xhigh means "run it like a normal
// interpreter" — unlimited steps and memory. No profile leaves recursion
// uncapped, deliberately: the interpreter recurses on the host Go stack, so an
// unbounded recursion would crash the process with an uncatchable stack
// overflow instead of a clean "recursion depth exceeded" error. Every profile
// therefore keeps a finite recursion cap, high enough to be irrelevant to any
// real program yet low enough to fail cleanly on runaway recursion.
type QuotaProfile struct {
	Name             string
	StepQuota        int
	MemoryQuotaBytes int
	RecursionLimit   int
	MaxSleepDuration time.Duration
}

// The named quota profiles. Values are deliberately generous relative to the
// embedding-API defaults: the CLI, which selects these, runs the developer's
// own scripts and is not a sandbox.
var (
	ProfileLow    = QuotaProfile{Name: "low", StepQuota: 1_000_000, MemoryQuotaBytes: 16 << 20, RecursionLimit: 256, MaxSleepDuration: time.Minute}
	ProfileMedium = QuotaProfile{Name: "medium", StepQuota: 20_000_000, MemoryQuotaBytes: 128 << 20, RecursionLimit: 1_000, MaxSleepDuration: 10 * time.Minute}
	ProfileHigh   = QuotaProfile{Name: "high", StepQuota: 200_000_000, MemoryQuotaBytes: 512 << 20, RecursionLimit: 4_000, MaxSleepDuration: time.Hour}
	// A developer waiting on their own script is not a sandbox escape, so the
	// most generous rung lifts the sleeping bound as it lifts steps and memory.
	ProfileXHigh = QuotaProfile{Name: "xhigh", StepQuota: Unlimited, MemoryQuotaBytes: Unlimited, RecursionLimit: 10_000, MaxSleepDuration: Unlimited}
)

// quotaProfiles lists the profiles in ascending order of generosity. Lookup and
// name enumeration derive from this slice so the two never drift.
var quotaProfiles = []QuotaProfile{ProfileLow, ProfileMedium, ProfileHigh, ProfileXHigh}

// QuotaProfileByName returns the profile with the given name, matched
// case-insensitively, and reports whether one was found.
func QuotaProfileByName(name string) (QuotaProfile, bool) {
	target := strings.ToLower(strings.TrimSpace(name))
	for _, profile := range quotaProfiles {
		if profile.Name == target {
			return profile, true
		}
	}
	return QuotaProfile{}, false
}

// QuotaProfileNames returns the profile names in ascending order of generosity,
// for help text and error messages.
func QuotaProfileNames() []string {
	names := make([]string, len(quotaProfiles))
	for i, profile := range quotaProfiles {
		names[i] = profile.Name
	}
	return names
}

// ApplyTo sets the three quota fields on cfg from the profile, leaving every
// other Config field untouched. Callers layer explicit per-quota overrides on
// top after applying a profile.
func (p QuotaProfile) ApplyTo(cfg *Config) {
	cfg.StepQuota = p.StepQuota
	cfg.MemoryQuotaBytes = p.MemoryQuotaBytes
	cfg.RecursionLimit = p.RecursionLimit
}

package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/mgomes/vibescript/vibes"
)

// defaultQuotaProfile is the profile the execution commands select when the
// user does not pass -profile. The CLI runs the developer's own scripts on
// their own machine — it is not a sandbox — so it defaults to the most generous
// rung: unlimited steps and memory, with a finite recursion cap.
const defaultQuotaProfile = "xhigh"

// quotaConfig carries resolved quota values from flag parsing to engine
// construction, using the same conventions as vibes.Config: a positive value is
// an explicit limit, vibes.Unlimited disables the quota, and zero selects the
// engine's built-in default.
type quotaConfig struct {
	stepQuota      int
	memoryQuota    int
	recursionLimit int
}

// applyTo writes the resolved quota values onto cfg, leaving every other field
// untouched.
func (q quotaConfig) applyTo(cfg *vibes.Config) {
	cfg.StepQuota = q.stepQuota
	cfg.MemoryQuotaBytes = q.memoryQuota
	cfg.RecursionLimit = q.recursionLimit
}

// quotaFlags binds the -profile selector and the per-quota override flags to a
// flag set. Resolve turns the parsed values into a quotaConfig.
type quotaFlags struct {
	fs             *flag.FlagSet
	profile        string
	stepQuota      int
	memoryQuota    int
	recursionLimit int
}

// registerQuotaFlags adds -profile and the raw -step-quota/-memory-quota/
// -recursion-limit override flags to fs and returns the binding used to resolve
// them after fs.Parse.
func registerQuotaFlags(fs *flag.FlagSet) *quotaFlags {
	q := &quotaFlags{fs: fs}
	fs.StringVar(&q.profile, "profile", defaultQuotaProfile,
		fmt.Sprintf("execution quota profile: %s", strings.Join(vibes.QuotaProfileNames(), ", ")))
	fs.IntVar(&q.stepQuota, "step-quota", 0, "override the profile's step quota (-1 = unlimited)")
	fs.IntVar(&q.memoryQuota, "memory-quota", 0, "override the profile's memory quota in bytes (-1 = unlimited)")
	fs.IntVar(&q.recursionLimit, "recursion-limit", 0,
		"override the profile's recursion limit (-1 = unlimited, which can crash on infinite recursion)")
	return q
}

// resolve selects the named profile and layers any explicitly-set override
// flags on top of it. An override flag left unset keeps the profile's value; a
// flag set to -1 requests unlimited, and a positive value is an explicit limit.
func (q *quotaFlags) resolve() (quotaConfig, error) {
	profile, ok := vibes.QuotaProfileByName(q.profile)
	if !ok {
		return quotaConfig{}, fmt.Errorf("unknown quota profile %q (choose one of: %s)",
			q.profile, strings.Join(vibes.QuotaProfileNames(), ", "))
	}

	resolved := quotaConfig{
		stepQuota:      profile.StepQuota,
		memoryQuota:    profile.MemoryQuotaBytes,
		recursionLimit: profile.RecursionLimit,
	}
	if flagWasSet(q.fs, "step-quota") {
		resolved.stepQuota = q.stepQuota
	}
	if flagWasSet(q.fs, "memory-quota") {
		resolved.memoryQuota = q.memoryQuota
	}
	if flagWasSet(q.fs, "recursion-limit") {
		resolved.recursionLimit = q.recursionLimit
	}
	return resolved, nil
}

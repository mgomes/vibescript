package main

import (
	"fmt"
	"strings"

	"github.com/mgomes/vibescript/vibes"
	"github.com/urfave/cli/v3"
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

// quotaFlagValues carries parsed values together with whether each override
// was explicitly supplied. An explicit zero must remain distinct from an
// unset override because zero selects the engine's built-in default.
type quotaFlagValues struct {
	profile        string
	stepQuota      int
	stepQuotaSet   bool
	memoryQuota    int
	memoryQuotaSet bool
	recursionLimit int
	recursionSet   bool
}

func newQuotaFlagValues() quotaFlagValues {
	return quotaFlagValues{profile: defaultQuotaProfile}
}

func newQuotaFlags(values *quotaFlagValues) []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:        "profile",
			Value:       values.profile,
			Destination: &values.profile,
			Usage:       fmt.Sprintf("execution quota profile: %s", strings.Join(vibes.QuotaProfileNames(), ", ")),
		},
		&cli.IntFlag{
			Name:        "step-quota",
			Usage:       "override the profile's step quota (-1 = unlimited)",
			HideDefault: true,
			Destination: &values.stepQuota,
		},
		&cli.IntFlag{
			Name:        "memory-quota",
			Usage:       "override the profile's memory quota in bytes (-1 = unlimited)",
			HideDefault: true,
			Destination: &values.memoryQuota,
		},
		&cli.IntFlag{
			Name:        "recursion-limit",
			Usage:       "override the profile's recursion limit (-1 = unlimited, which can crash on infinite recursion)",
			HideDefault: true,
			Destination: &values.recursionLimit,
		},
	}
}

func resolveCommandQuota(command *cli.Command, values *quotaFlagValues) (quotaConfig, error) {
	values.stepQuotaSet = command.IsSet("step-quota")
	values.memoryQuotaSet = command.IsSet("memory-quota")
	values.recursionSet = command.IsSet("recursion-limit")
	return values.resolve()
}

// resolve selects the named profile and layers any explicitly-set override
// flags on top of it. An override flag left unset keeps the profile's value; a
// flag set to -1 requests unlimited, and a positive value is an explicit limit.
func (q quotaFlagValues) resolve() (quotaConfig, error) {
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
	if q.stepQuotaSet {
		resolved.stepQuota = q.stepQuota
	}
	if q.memoryQuotaSet {
		resolved.memoryQuota = q.memoryQuota
	}
	if q.recursionSet {
		resolved.recursionLimit = q.recursionLimit
	}
	return resolved, nil
}

package runtime

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// quotaRead matches any read of an execution's own memory quota.
var quotaRead = regexp.MustCompile(`(?:\w+\.)*exec\.memoryQuota\b`)

// quotaMeteringGuard matches the one form every read may safely take: the guard
// that asks whether metering is on at all.
var quotaMeteringGuard = regexp.MustCompile(`exec\.memoryQuota\s*(?:<=|>)\s*0`)

// enclosingFunc reports the name of the function a line sits in.
var funcHeader = regexp.MustCompile(`^func (?:\([^)]*\) )?(\w+)`)

// quotaReaders are the only functions permitted to read the local quota.
//
// Every entry is a function that *resolves* the quota into the bound in force,
// or the single choke point that applies it. There are deliberately no probes
// here, and that absence is the point.
//
// There were three, justified as "soft probes with a fallback". Two of them were
// wrong: their callers allocated on the answer -- an output map, a 90 KiB
// comparison memo -- so the answer was a final admission whatever the function
// was named, and an allowlisted bypass is worse than an unguarded one because it
// has been inspected and blessed. The classification came from reading the
// function rather than asking what its caller does next.
//
// So the judgement was removed instead of repeated. A probe consulting
// memoryBudgetBytes is chain-aware and needs no exemption, which is available
// because that function only *reads* the chain -- the original justification for
// keeping probes local confused consulting the chain with publishing to it, and
// only publishing carries the speculative-refusal hazard. Consulting can make a
// probe answer "no" more often, never "yes", so it is safe by construction.
//
// Membership is now checkable without judging caller behavior: does this
// function turn the quota into the bound in force, or apply that bound? If a new
// entry is proposed on any other grounds, it is a bypass.
var quotaReaders = map[string]string{
	// Resolvers: the two functions that turn the local quota into the number
	// everything else is supposed to be using.
	"effectiveMemoryLimit": "resolves the local quota against the inherited ceiling",
	"memoryBudgetBytes":    "resolves what is left of that ceiling after ancestors",
	// The single choke point every refusal goes through.
	"memoryExceeded": "applies both bounds",
	// Where an inheriting execution adopts the ceiling as its own quota.
	"newExecutionForCall": "adopts the inherited ceiling",
}

// TestOnlyResolvedLimitsAreReadFromTheQuota pins that the local memory quota is
// read only where it is turned into the bound actually in force.
//
// Two categories of defect came out of this field being read directly. Hard
// refusals compared against it and so never consulted or published to the chain:
// two were reported and twenty existed. Then budgets -- a scratch reservation, a
// projected entry cap, a match table -- sized allocations from it, which is
// worse, because the buffer is built before any check can refuse it; two of
// those were reported and six existed.
//
// Both times the reported sites were a fraction of the category, so the category
// is what is enforced. A read of exec.memoryQuota outside the functions that
// resolve it is a bypass of one kind or the other, and the fix is to ask for
// effectiveMemoryLimit (a share of the bound in force) or memoryBudgetBytes
// (how much may still be allocated) rather than to add a name here.
//
// The metering guard is exempt everywhere: asking whether a quota is set at all
// is not a bound to compare against.
func TestOnlyResolvedLimitsAreReadFromTheQuota(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	var offenders []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}

		fn := ""
		for i, line := range strings.Split(string(data), "\n") {
			if m := funcHeader.FindStringSubmatch(line); m != nil {
				fn = m[1]
			}
			code := line
			if idx := strings.Index(code, "//"); idx >= 0 {
				code = code[:idx]
			}
			if !quotaRead.MatchString(code) {
				continue
			}
			// Strip the permitted guard form and see if a read survives it.
			if !quotaRead.MatchString(quotaMeteringGuard.ReplaceAllString(code, "")) {
				continue
			}
			if _, ok := quotaReaders[fn]; ok {
				continue
			}
			offenders = append(offenders, name+":"+strconv.Itoa(i+1)+" in "+fn+"(): "+strings.TrimSpace(line))
		}
	}

	if len(offenders) > 0 {
		t.Fatalf("these read the execution's own memory quota instead of the bound in force, so they ignore a tighter ceiling inherited from a caller and the part of it that caller already holds:\n%s",
			strings.Join(offenders, "\n"))
	}
}

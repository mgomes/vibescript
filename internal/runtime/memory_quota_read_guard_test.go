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
// Every entry is a function that turns the quota into the bound callers are
// meant to use, or the single choke point that applies it. There are
// deliberately no probes here, and that absence is the point.
//
// There were three, justified as "soft probes with a fallback". Two of them were
// wrong: their callers allocated on the answer -- an output map, a 90 KiB
// comparison memo -- so the answer was a final admission whatever the function
// was named, and an allowlisted bypass is worse than an unguarded one because it
// has been inspected and blessed. The classification came from reading the
// function rather than asking what its caller does next.
//
// So the judgement was removed instead of repeated. Membership is checkable
// without judging caller behavior: does this function resolve the quota into a
// budget, or apply that budget? If a new entry is proposed on any other
// grounds, it is a bypass.
var quotaReaders = map[string]string{
	// Resolves the quota into the room an allocation may still be sized against.
	"memoryBudgetBytes": "resolves how much may still be allocated",
	// The single choke point every refusal goes through.
	"memoryExceeded": "applies the bound",
	// Reports the bound in the refusal message.
	"memoryQuotaExceededError": "names the bound that refused",
}

// TestOnlyResolvedLimitsAreReadFromTheQuota pins that the local memory quota is
// read only where it is turned into the bound callers apply.
//
// Two categories of defect came out of this field being read directly. Hard
// refusals compared against it inline rather than going through the one check:
// two were reported and twenty existed. Then budgets -- a scratch reservation, a
// projected entry cap, a match table -- sized allocations from it, which is
// worse, because the buffer is built before any check can refuse it; two of
// those were reported and six existed.
//
// Both times the reported sites were a fraction of the category, so the category
// is what is enforced. A read of exec.memoryQuota outside the functions that
// resolve it is a bypass of one kind or the other, and the fix is to ask
// memoryExceeded (is this refused) or memoryBudgetBytes (how much may still be
// allocated) rather than to add a name here.
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
		t.Fatalf("these read the execution's own memory quota instead of going through the functions that resolve and apply it, which is how a refusal gets bypassed and how a buffer gets sized before anything can refuse it:\n%s",
			strings.Join(offenders, "\n"))
	}
}

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

// hostCapabilityInterfaces are the interfaces a host implements to supply a
// capability to a call. Every method on them is host code, invoked by this
// package while a level is setting itself up, and free to block for as long as
// the host likes.
var hostCapabilityInterfaces = []string{"CapabilityAdapter", "CapabilityContractProvider"}

// interfaceMethod matches a method line inside an interface declaration.
var interfaceMethod = regexp.MustCompile(`^\s+(\w+)\(`)

// executionInScope matches a function that has a level to publish.
var executionInScope = regexp.MustCompile(`\bexec\b|\*Execution\b`)

// hostBindWithoutALevel are the functions that invoke host capability code with
// no level to publish, so the rule has nothing for them to do.
//
// Membership is verified rather than trusted. The guard fails if one of these
// ever gains an Execution, which is exactly the moment the exemption stops being
// true -- an exemption that keeps holding after its premise is gone is worse
// than no rule, because it has been inspected and blessed.
var hostBindWithoutALevel = map[string]string{
	"checkOptionGlobals": "the static gate binds adapters before any execution exists",
}

// hostCapabilityMethods reads the host capability surface out of the source
// instead of listing it here, so that a method added to either interface is
// governed from the moment it exists rather than the next time someone
// remembers this test.
func hostCapabilityMethods(t *testing.T) []string {
	t.Helper()

	data, err := os.ReadFile(filepath.Clean("capabilities.go"))
	if err != nil {
		t.Fatalf("read capabilities.go: %v", err)
	}
	lines := strings.Split(string(data), "\n")

	var methods []string
	for _, iface := range hostCapabilityInterfaces {
		header := "type " + iface + " interface {"
		start := -1
		for i, line := range lines {
			if strings.HasPrefix(line, header) {
				start = i
				break
			}
		}
		if start < 0 {
			t.Fatalf("interface %s is no longer declared in capabilities.go: this guard governs the methods a host can implement, and it cannot find them", iface)
		}
		before := len(methods)
		for _, line := range lines[start+1:] {
			if strings.HasPrefix(line, "}") {
				break
			}
			if m := interfaceMethod.FindStringSubmatch(line); m != nil {
				methods = append(methods, m[1])
			}
		}
		if len(methods) == before {
			t.Fatalf("interface %s declares no methods the guard can recognize, so host code on it would go ungoverned", iface)
		}
	}
	return methods
}

// TestHostCapabilityCodeRunsOnlyAfterPublishing pins the rule that four
// findings on this branch have each been one instance of.
//
// A level is invisible to its ancestors until it publishes, so nothing it has
// allocated may be left unpublished across an operation that can block. Host
// capability code is that operation on the setup path: an adapter's Bind is
// free to wait, and while it waits every ancestor on the chain is allocating
// against whatever total this level published last.
//
// The first three instances were fixed where they were reported -- publish
// before a binder, publish for capability-free jobs too, register before the
// setup allocations. The fourth was the adapter loop publishing once for the
// whole loop: one adapter's globals were deep-copied into the call root and the
// next adapter could then block with none of it published. That one differs from
// the pre-registration residual in the way that decides whether a bound is an
// acceptable answer -- the residual holds the level's own root env and cloned
// definitions, fixed by the script's text at roughly 745 bytes per class, while
// an adapter's globals are host-supplied data that nothing about the program
// bounds.
//
// So the rule is enforced rather than applied a fourth time: every invocation of
// host capability code, from a function that has a level, must have a
// publishBeforeHostCode above it.
//
// How this fails when it stops applying, which is the question worth asking of
// any guard: it fails if it scans nothing, if the host surface it governs stops
// being declared where it reads it, if no invocation of host code is found at
// all, if no invocation is actually guarded, if an exempt function gains a
// level, or if publishBeforeHostCode itself stops publishing. Passing while
// protecting nothing takes all six of those staying true.
func TestHostCapabilityCodeRunsOnlyAfterPublishing(t *testing.T) {
	t.Parallel()

	methods := hostCapabilityMethods(t)
	hostMethod := make(map[string]bool, len(methods))
	for _, m := range methods {
		hostMethod[m] = true
	}
	hostCall := regexp.MustCompile(`\.(` + strings.Join(methods, "|") + `)\(`)

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	var (
		offenders          []string
		scanned            int
		invoked            int
		guarded            int
		seenExempt         = map[string]bool{}
		exemptHasLevel     = map[string]string{}
		publisherPublishes bool
	)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		scanned++

		fn := ""
		published := false
		for i, line := range strings.Split(string(data), "\n") {
			if m := funcHeader.FindStringSubmatch(line); m != nil {
				fn = m[1]
				published = false
				if _, exempt := hostBindWithoutALevel[fn]; exempt {
					seenExempt[fn] = true
				}
			} else if line == "}" {
				// A closing brace in column zero ends a top-level declaration.
				// Without this, everything between one function and the next is
				// still attributed to the previous one, and the exemption check
				// below would read a neighbour's signature as evidence about the
				// exempt function.
				fn = ""
			}
			code := line
			if idx := strings.Index(code, "//"); idx >= 0 {
				code = code[:idx]
			}
			if _, exempt := hostBindWithoutALevel[fn]; exempt && executionInScope.MatchString(code) {
				exemptHasLevel[fn] = name + ":" + strconv.Itoa(i+1) + ": " + strings.TrimSpace(line)
			}
			if strings.Contains(code, "publishBeforeHostCode(") {
				published = true
			}
			if fn == "publishBeforeHostCode" && strings.Contains(code, "checkMemory()") {
				publisherPublishes = true
			}
			if !hostCall.MatchString(code) {
				continue
			}
			// A function implementing one of these methods is downstream of an
			// invocation that has already published, not a fresh entry into host
			// code.
			if hostMethod[fn] {
				continue
			}
			invoked++
			if _, exempt := hostBindWithoutALevel[fn]; exempt {
				continue
			}
			if published {
				guarded++
				continue
			}
			offenders = append(offenders, name+":"+strconv.Itoa(i+1)+" in "+fn+"(): "+strings.TrimSpace(line))
		}
	}

	if len(offenders) > 0 {
		t.Fatalf("these hand control to host capability code without publishing what this level holds first, so an adapter that blocks leaves everything allocated since the last publication invisible to the chain for as long as it waits:\n%s",
			strings.Join(offenders, "\n"))
	}
	if scanned == 0 {
		t.Fatalf("scanned no package files, so this guard proved nothing")
	}
	if invoked == 0 {
		t.Fatalf("scanned %d files and found no invocation of host capability code (%s): the guard no longer recognizes the form host code is invoked in, so it governs nothing",
			scanned, strings.Join(methods, ", "))
	}
	// Before the count below, so that an exemption that has stopped being true
	// is named as that rather than as the coverage collapse it causes.
	for fn, why := range hostBindWithoutALevel {
		if !seenExempt[fn] {
			t.Fatalf("%s is exempted here as %q but no longer exists: a stale exemption silently widens what this guard permits", fn, why)
		}
		if where := exemptHasLevel[fn]; where != "" {
			t.Fatalf("%s is exempted here as %q, but it now has an execution to publish, at %s: the premise of the exemption is gone and the exemption has to go with it",
				fn, why, where)
		}
	}
	if guarded == 0 {
		t.Fatalf("found %d invocations of host capability code and none of them is preceded by publishBeforeHostCode: every one is either exempt or unrecognized, so this guard is passing without constraining anything",
			invoked)
	}
	if !publisherPublishes {
		t.Fatalf("publishBeforeHostCode no longer publishes: it does not reach checkMemory, so every call site above satisfies this guard while telling the chain nothing")
	}
}

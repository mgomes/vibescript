package runtime

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

// hostCallChokes are the only functions permitted to invoke host capability
// code, and each exists to do nothing but publish and then invoke.
//
// This is the difference between the rule and the previous guard. The rule is
// that nothing a level has allocated may be unpublished when host code runs.
// The previous guard asked whether a publication appeared *above* the call,
// which a publication at the top of a loop body satisfies for every call in it
// while data accumulates in between -- and that gap was the fifth finding on
// this property. "Is anything retained between the publication and the call" is
// not decidable at an arbitrary call site, so the call sites are removed
// instead: host code is reachable only through these two, and inside them the
// publication must be adjacent to the call, which is decidable by reading lines.
var hostCallChokes = map[string]string{
	"hostCapabilityContracts": "publishes, then asks the adapter for its contracts",
	"bindHostCapability":      "publishes, then hands control to the adapter",
}

// betweenPublishAndCall matches the only lines allowed between a choke's
// publication and its host call: the publication's own error return, closing
// braces, blanks. Anything else -- an assignment, another call, a declaration --
// is retention the host call would run in front of.
var betweenPublishAndCall = regexp.MustCompile(`^(\}|return .*|)$`)

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

// TestHostCapabilityCodeRunsOnlyAfterPublishing pins the rule that five
// findings on this branch have each been one instance of.
//
// The rule: nothing a level has allocated may be left unpublished when host code
// runs, because a level is invisible to its ancestors until it publishes and
// host code is free to block for as long as it likes. While it blocks, every
// ancestor on the chain allocates against whatever total this level published
// last.
//
// The first four instances widened where the rule applies -- publish before a
// binder, publish for capability-free jobs too, register before the setup
// allocations, publish between adapters. The fifth was different in kind, and it
// is the one that shaped this test: the rule was right and the *guard* checked
// something weaker. It asked whether a publication appeared **above** each
// invocation, which one publication at the top of a loop body satisfies for
// every call in that body -- while an adapter's declared contracts accumulated
// on the execution in between, and the bind after them blocked with none of it
// published.
//
// "Is anything retained between the publication and the call" is not decidable
// at an arbitrary call site. So the call sites are removed rather than analyzed:
// host code is reachable only through hostCallChokes, each of which does nothing
// but publish and invoke, and inside them the publication must be adjacent to
// the call. Both halves are decidable by reading lines, and together they say
// what the rule says.
//
// That also retires an asserted bound. The unpublished window was documented as
// bounded by the adapter's Go source; CapabilityContractProvider bounds neither
// the map's cardinality nor its key lengths, so it was host data all along. This
// shape leaves nothing between publication and call to bound.
//
// How this fails when it stops applying, which is the question worth asking of
// any guard: it fails if it scans nothing, if the host surface it governs stops
// being declared where it reads it, if no invocation of host code is found at
// all, if no invocation is actually guarded, if a choke stops publishing or
// stops publishing adjacently, if a choke disappears, if an exempt function
// gains a level, or if publishBeforeHostCode itself stops publishing.
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
		seenChoke          = map[string]bool{}
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

		lines := strings.Split(string(data), "\n")
		fn := ""
		publishedAt := -1
		for i, line := range lines {
			if m := funcHeader.FindStringSubmatch(line); m != nil {
				fn = m[1]
				publishedAt = -1
				if _, exempt := hostBindWithoutALevel[fn]; exempt {
					seenExempt[fn] = true
				}
				if _, choke := hostCallChokes[fn]; choke {
					seenChoke[fn] = true
				}
			} else if line == "}" {
				// A closing brace in column zero ends a top-level declaration.
				// Without this, everything between one function and the next is
				// still attributed to the previous one.
				fn = ""
				publishedAt = -1
			}
			code := stripComment(line)
			if _, exempt := hostBindWithoutALevel[fn]; exempt && executionInScope.MatchString(code) {
				exemptHasLevel[fn] = name + ":" + strconv.Itoa(i+1) + ": " + strings.TrimSpace(line)
			}
			if strings.Contains(code, "publishBeforeHostCode(") {
				publishedAt = i
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
			where := name + ":" + strconv.Itoa(i+1) + " in " + fn + "(): " + strings.TrimSpace(line)
			if _, choke := hostCallChokes[fn]; !choke {
				offenders = append(offenders, where+"\n    -- host code invoked outside a choke; route it through one of "+chokeNames()+" so nothing can be inserted between the publication and the call")
				continue
			}
			if publishedAt < 0 {
				offenders = append(offenders, where+"\n    -- no publication before it in this function")
				continue
			}
			if between := retainedBetween(lines[publishedAt+1 : i]); between != "" {
				offenders = append(offenders, where+"\n    -- "+strconv.Itoa(i-publishedAt-1)+" line(s) between the publication and the call, including "+between+"; a choke must publish and then invoke with nothing in between")
				continue
			}
			guarded++
		}
	}

	if len(offenders) > 0 {
		t.Fatalf("these let host capability code run while something this level allocated is unpublished, so an adapter that blocks hides it from the chain for as long as it waits:\n%s",
			strings.Join(offenders, "\n"))
	}
	if scanned == 0 {
		t.Fatalf("scanned no package files, so this guard proved nothing")
	}
	if invoked == 0 {
		t.Fatalf("scanned %d files and found no invocation of host capability code (%s): the guard no longer recognizes the form host code is invoked in, so it governs nothing",
			scanned, strings.Join(methods, ", "))
	}
	for fn, why := range hostBindWithoutALevel {
		if !seenExempt[fn] {
			t.Fatalf("%s is exempted here as %q but no longer exists: a stale exemption silently widens what this guard permits", fn, why)
		}
		if where := exemptHasLevel[fn]; where != "" {
			t.Fatalf("%s is exempted here as %q, but it now has an execution to publish, at %s: the premise of the exemption is gone and the exemption has to go with it",
				fn, why, where)
		}
	}
	for fn, why := range hostCallChokes {
		if !seenChoke[fn] {
			t.Fatalf("%s is named here as the choke that %s, but no longer exists: host code would be reachable through a site this guard is not checking", fn, why)
		}
	}
	if guarded < len(hostCallChokes) {
		t.Fatalf("found %d guarded invocations of host capability code across %d chokes: at least one choke no longer invokes the host surface, so it is not the pairing this guard thinks it is checking",
			guarded, len(hostCallChokes))
	}
	if !publisherPublishes {
		t.Fatalf("publishBeforeHostCode no longer publishes: it does not reach checkMemory, so every call site above satisfies this guard while telling the chain nothing")
	}
}

// stripComment removes a trailing line comment so the scan reads code only.
func stripComment(line string) string {
	if idx := strings.Index(line, "//"); idx >= 0 {
		return line[:idx]
	}
	return line
}

// retainedBetween reports the first line between a publication and a host call
// that could have retained something, or "" when nothing could have.
func retainedBetween(lines []string) string {
	for _, line := range lines {
		if betweenPublishAndCall.MatchString(strings.TrimSpace(stripComment(line))) {
			continue
		}
		return strconv.Quote(strings.TrimSpace(line))
	}
	return ""
}

// chokeNames lists the permitted chokes for a failure message.
func chokeNames() string {
	names := make([]string, 0, len(hostCallChokes))
	for fn := range hostCallChokes {
		names = append(names, fn+"()")
	}
	sort.Strings(names)
	return strings.Join(names, " or ")
}

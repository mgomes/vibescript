package runtime

import (
	"fmt"
	"regexp/syntax"
	goruntime "runtime"
	"strings"
	"testing"
)

func TestRegexCacheReusesCompiledPattern(t *testing.T) {
	t.Parallel()

	cache := newRegexCache(2, compiledRegexCacheInstructionBudget)
	first, err := cache.compile("ID-[0-9]+")
	if err != nil {
		t.Fatalf("regexCache.compile(valid) error = %v, want nil", err)
	}
	second, err := cache.compile("ID-[0-9]+")
	if err != nil {
		t.Fatalf("regexCache.compile(cached) error = %v, want nil", err)
	}
	if first != second {
		t.Fatalf("regexCache.compile(cached) returned different regexp pointers")
	}
}

func TestRegexCacheEvictsLeastRecentlyUsedPattern(t *testing.T) {
	t.Parallel()

	cache := newRegexCache(2, compiledRegexCacheInstructionBudget)
	if _, err := cache.compile("first"); err != nil {
		t.Fatalf("regexCache.compile(first) error = %v, want nil", err)
	}
	if _, err := cache.compile("second"); err != nil {
		t.Fatalf("regexCache.compile(second) error = %v, want nil", err)
	}
	if _, err := cache.compile("third"); err != nil {
		t.Fatalf("regexCache.compile(third) error = %v, want nil", err)
	}
	if _, ok := cache.entries["first"]; ok {
		t.Fatalf("regexCache retained least recently used pattern")
	}
	if _, ok := cache.entries["second"]; !ok {
		t.Fatalf("regexCache evicted second pattern, want retained")
	}
	if _, ok := cache.entries["third"]; !ok {
		t.Fatalf("regexCache evicted third pattern, want retained")
	}
}

func TestRegexCacheDoesNotStoreInvalidPattern(t *testing.T) {
	t.Parallel()

	cache := newRegexCache(2, compiledRegexCacheInstructionBudget)
	if _, err := cache.compile("["); err == nil {
		t.Fatalf("regexCache.compile(invalid) error = nil, want non-nil")
	}
	if len(cache.entries) != 0 {
		t.Fatalf("regexCache.compile(invalid) cached %d entries, want 0", len(cache.entries))
	}
}

// TestRegexCacheRejectsOversizedPrograms pins the per-pattern compiled-size
// guard. The pattern-length limit does not bound compiled memory: Go expands
// counted repeats, so a pattern well inside 16 KiB compiles to a program of
// arbitrary size (a 4 KiB pattern measured about 52 MiB). Sizing before
// compiling keeps that program from ever being built.
func TestRegexCacheRejectsOversizedPrograms(t *testing.T) {
	t.Parallel()

	oversized := "(?:" + strings.Repeat("a?", 2000) + "){300}"
	if len(oversized) > maxRegexPatternSize {
		t.Fatalf("the probe pattern (%d bytes) must stay inside the length guard", len(oversized))
	}
	cache := newRegexCache(compiledRegexCacheCapacity, compiledRegexCacheInstructionBudget)
	if _, err := cache.compile(oversized); err == nil {
		t.Fatal("a pattern compiling past the instruction cap must be rejected")
	} else if !strings.Contains(err.Error(), "instructions") {
		t.Fatalf("err = %v, want a compiled-size guard error", err)
	}

	// An ordinary pattern of similar length still compiles.
	ordinary := strings.Repeat("(a|b)", 300)
	if _, err := cache.compile(ordinary); err != nil {
		t.Fatalf("an ordinary pattern of %d bytes must still compile: %v", len(ordinary), err)
	}
}

// TestRegexCacheEvictsOnInstructionBudget pins that eviction tracks retained
// program size, not just entry count: filling the cache with large-but-legal
// programs previously accumulated far past any memory quota, in a
// process-global cache shared across engines and calls.
func TestRegexCacheEvictsOnInstructionBudget(t *testing.T) {
	t.Parallel()

	// Each of these compiles well under the per-pattern cap, but a handful
	// together exceed the cache budget.
	heavy := func(i int) string {
		return fmt.Sprintf("(?:z%d)(?:%s){80}", i, strings.Repeat("a?", 500))
	}
	cache := newRegexCache(compiledRegexCacheCapacity, compiledRegexCacheInstructionBudget)
	for i := range 8 {
		if _, err := cache.compile(heavy(i)); err != nil {
			t.Fatalf("heavy pattern %d must compile: %v", i, err)
		}
	}
	cache.mu.Lock()
	cost, entries := cache.cost, cache.lru.Len()
	cache.mu.Unlock()

	if entries >= 8 {
		t.Fatalf("cache kept %d entries, want the budget to have evicted some", entries)
	}
	if cost > compiledRegexCacheInstructionBudget {
		t.Fatalf("cached cost %d exceeds the budget %d", cost, compiledRegexCacheInstructionBudget)
	}
}

// TestRegexCacheKeepsOrdinaryPatterns pins that the budget does not disturb
// normal use: realistic patterns are orders of magnitude below the limits, so
// a full working set stays cached.
func TestRegexCacheKeepsOrdinaryPatterns(t *testing.T) {
	t.Parallel()

	cache := newRegexCache(compiledRegexCacheCapacity, compiledRegexCacheInstructionBudget)
	for i := range compiledRegexCacheCapacity {
		if _, err := cache.compile(fmt.Sprintf(`^user-%d-[a-z]+@[a-z]+\.[a-z]{2,}$`, i)); err != nil {
			t.Fatalf("ordinary pattern %d failed: %v", i, err)
		}
	}
	cache.mu.Lock()
	entries := cache.lru.Len()
	cache.mu.Unlock()
	if entries != compiledRegexCacheCapacity {
		t.Fatalf("cache kept %d ordinary patterns, want all %d", entries, compiledRegexCacheCapacity)
	}
}

// TestRegexSizingDoesNotExpandThePattern pins that the guard bounds the work
// it is guarding against. Sizing by compiling first builds the very program
// the limit exists to prevent: for this pattern the compile allocates about
// 258 MiB and takes tens of milliseconds, all before the limit could reject
// it, so repeated cache misses would exhaust the host regardless of what the
// cache retains. Estimating from the parse tree costs about a megabyte.
func TestRegexSizingDoesNotExpandThePattern(t *testing.T) {
	// Not parallel: MemStats.TotalAlloc is process-wide, so a concurrent
	// test's allocations would be attributed to the sizing pass.
	oversized := "(?:" + strings.Repeat("a?", 2000) + "){300}"

	var before, after goruntime.MemStats
	goruntime.GC()
	goruntime.ReadMemStats(&before)
	cost, err := compiledRegexCost(oversized)
	goruntime.ReadMemStats(&after)
	if err != nil {
		t.Fatalf("sizing a valid pattern failed: %v", err)
	}
	if cost <= maxCompiledRegexInstructions {
		t.Fatalf("cost = %d, want the estimate to exceed the cap %d", cost, maxCompiledRegexInstructions)
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	// The expansion this replaces allocates hundreds of megabytes.
	if limit := uint64(16 << 20); allocated > limit {
		t.Fatalf("sizing allocated %.2f MiB, want under %.2f MiB — it is expanding the pattern",
			float64(allocated)/(1<<20), float64(limit)/(1<<20))
	}
}

// TestRegexCostTracksCompiledSize pins that the estimate stays close to the
// program it predicts for the shapes that matter. It is an approximation, so
// what is pinned is the absence of a large multiplicative under-estimate: a
// pattern that expands must be predicted as expanding.
func TestRegexCostTracksCompiledSize(t *testing.T) {
	t.Parallel()

	for _, pattern := range []string{
		`^[a-z]+@[a-z]+\.[a-z]{2,}$`,
		strings.Repeat("(a|b)", 300),
		`a{5}b{10}`,
		"(?:" + strings.Repeat("a?", 200) + "){50}",
		"(?:" + strings.Repeat("a?", 500) + "){80}",
	} {
		parsed, err := syntax.Parse(pattern, syntax.Perl)
		if err != nil {
			t.Fatalf("parse %.20q: %v", pattern, err)
		}
		prog, err := syntax.Compile(parsed.Simplify())
		if err != nil {
			t.Fatalf("compile %.20q: %v", pattern, err)
		}
		estimated, err := compiledRegexCost(pattern)
		if err != nil {
			t.Fatalf("size %.20q: %v", pattern, err)
		}
		actual := len(prog.Inst)
		if estimated < actual/2 {
			t.Errorf("estimate %d for %.20q under-predicts the actual %d by more than half",
				estimated, pattern, actual)
		}
	}
}

// TestRegexCostHandlesRepeatBounds pins the two repeat shapes the estimate
// originally got wrong in opposite directions. A bounded range compiles a
// branch alongside each optional copy, so ignoring those under-counted
// a{0,1000} chains by nearly half and let a program twice the cap through. A
// {0} repeat is discarded body and all by simplification, so costing its child
// saturated the estimate and rejected a valid pattern over a body that never
// compiles.
func TestRegexCostHandlesRepeatBounds(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		pattern      string
		wantRejected bool
	}{
		"bounded range charges its branches": {
			pattern:      strings.Repeat("a{0,1000}", 99),
			wantRejected: true,
		},
		"discarded zero repeat stays cheap": {
			pattern:      "(?:" + strings.Repeat("a{1000}", 101) + "){0}",
			wantRejected: false,
		},
		// Simplify collapses (?:a*)* to a*, so a stack of the same
		// quantifier compiles to one; charging each wrapper saturated the
		// estimate and rejected a program of about 2,000 instructions.
		"identical nested quantifiers collapse": {
			pattern:      "(?:" + strings.Repeat("(?:", 101) + "a*" + strings.Repeat(")*", 101) + "){1000}",
			wantRejected: false,
		},
		// Mixed quantifiers are not idempotent and Simplify keeps both, so
		// collapsing them under-counted this program threefold and admitted
		// a pattern well past the cap.
		"mixed nested quantifiers are charged": {
			pattern:      strings.Repeat("(?:(?:a+)?){1000}", 99),
			wantRejected: true,
		},
		// Simplify collapses only when greediness matches too, so an
		// alternating greedy/lazy chain keeps every level.
		"alternating greediness is charged": {
			pattern:      alternatingGreedinessPattern(),
			wantRejected: true,
		},
		// A star over a nullable operand compiles as (operand+)?, emitting a
		// second branch that charging one per star missed.
		"nullable star charges both branches": {
			pattern:      strings.Repeat("(?:(?:a?)*){1000}", 33),
			wantRejected: true,
		},
		// Counted syntax aliases the simple operators: {0,} is a star, so a
		// nullable operand needs the two-branch charge; {0,} nested in a star
		// is an idempotent pair that collapses; and {0} discards its body.
		// Testing re.Op directly missed all three rewrites.
		"counted star alias charges both branches": {
			pattern:      strings.Repeat("(?:(?:a?){0,}){1000}", 33),
			wantRejected: true,
		},
		"counted star alias collapses": {
			pattern:      strings.Repeat("(?:(?:a{0,})*){1000}", 26),
			wantRejected: false,
		},
		"counted zero operand is an empty match": {
			pattern:      "(?:" + strings.Repeat("(?:(?:a{1000}){0})*", 34) + "){1000}",
			wantRejected: false,
		},
		// An open upper bound emits exactly Min copies with the last one
		// quantified, so charging Min+1 billed the operand an extra time and
		// rejected a program inside the cap.
		"open-ended repeat charges Min copies": {
			pattern:      "(?:" + strings.Repeat("a{1000}", 80) + "){1,}",
			wantRejected: false,
		},
		// Quantifiers over an empty match are removed whatever the operator
		// or greediness, so a deep chain around one compiles to nothing.
		"quantifier chain over an empty match collapses": {
			pattern:      emptyMatchQuantifierCyclePattern(),
			wantRejected: false,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			parsed, err := syntax.Parse(tc.pattern, syntax.Perl)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			prog, err := syntax.Compile(parsed.Simplify())
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			actual := len(prog.Inst)

			estimated, err := compiledRegexCost(tc.pattern)
			if err != nil {
				t.Fatalf("size: %v", err)
			}
			rejected := estimated > maxCompiledRegexInstructions
			if rejected != tc.wantRejected {
				t.Fatalf("estimate %d (actual %d) rejected=%v, want rejected=%v",
					estimated, actual, rejected, tc.wantRejected)
			}
			// Whichever way it goes, the estimate must not understate a
			// program by a large factor.
			if !rejected && estimated < actual/2 {
				t.Fatalf("estimate %d under-predicts the actual %d by more than half", estimated, actual)
			}
		})
	}
}

// alternatingGreedinessPattern builds 101 nested stars that alternate between
// greedy and lazy, wrapped in a counted repeat.
func alternatingGreedinessPattern() string {
	var b strings.Builder
	for range 101 {
		b.WriteString("(?:")
	}
	b.WriteString("a")
	for i := range 101 {
		if i%2 == 0 {
			b.WriteString(")*")
		} else {
			b.WriteString(")*?")
		}
	}
	return "(?:" + b.String() + "){1000}"
}

// emptyMatchQuantifierCyclePattern builds a deep cycle of *, lazy +, and ?
// around an empty group, wrapped in a counted repeat.
func emptyMatchQuantifierCyclePattern() string {
	var b strings.Builder
	for range 101 {
		b.WriteString("(?:")
	}
	b.WriteString("(?:)")
	ops := []string{")*", ")+?", ")?"}
	for i := range 101 {
		b.WriteString(ops[i%3])
	}
	return "(?:" + b.String() + "){1000}"
}

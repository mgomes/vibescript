package runtime

import (
	"fmt"
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

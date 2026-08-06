package runtime

import (
	"container/list"
	"regexp"
	"regexp/syntax"
	"sync"
)

const (
	compiledRegexCacheCapacity = 64

	// compiledRegexInstructionBytes approximates the heap a single compiled
	// program instruction retains. Measured at roughly 45 bytes across
	// pattern shapes spanning four orders of magnitude in size; rounded up so
	// the budgets below describe a ceiling rather than a typical case.
	compiledRegexInstructionBytes = 64

	// maxCompiledRegexInstructions caps one compiled program, and
	// compiledRegexCacheInstructionBudget caps every cached program together.
	//
	// The pattern-length guard alone does not bound compiled memory: Go
	// expands counted repeats, so a pattern well inside the 16 KiB limit
	// compiles to an arbitrarily large program. Measured amplification runs
	// from about 2,200x at 408 bytes to about 13,500x at 4 KiB — a 4 KiB
	// pattern retains roughly 52 MiB. Filling the 64-entry cache with those
	// held gigabytes, outside MemoryQuotaBytes and beyond the call that
	// compiled them, in a process-global cache shared across engines and
	// tenants (#52).
	//
	// Instruction count is the budgeted quantity because it tracks retained
	// bytes closely and is knowable before compiling. Real patterns sit far
	// below these limits: an email-shaped pattern compiles to 13
	// instructions and 300 chained alternations to 902, against a per-pattern
	// cap of 100,000 (about 6 MiB) and a cache holding 250,000 (about 16 MiB).
	maxCompiledRegexInstructions        = 100_000
	compiledRegexCacheInstructionBudget = 250_000
)

var compiledRegexps = newRegexCache(compiledRegexCacheCapacity, compiledRegexCacheInstructionBudget)

type regexCache struct {
	mu       sync.Mutex
	capacity int
	// budget caps the summed instruction count of the cached programs, so
	// eviction tracks retained memory rather than only entry count.
	budget  int
	cost    int
	lru     *list.List
	entries map[string]*list.Element
}

type regexCacheEntry struct {
	pattern string
	re      *regexp.Regexp
	cost    int
}

func newRegexCache(capacity, budget int) *regexCache {
	if capacity < 1 {
		capacity = 1
	}
	if budget < 1 {
		budget = 1
	}
	return &regexCache{
		capacity: capacity,
		budget:   budget,
		lru:      list.New(),
		entries:  make(map[string]*list.Element, capacity),
	}
}

func compileCachedRegex(pattern string) (*regexp.Regexp, error) {
	return compiledRegexps.compile(pattern)
}

// compiledRegexCost reports how many program instructions pattern compiles to,
// without building the regexp itself. Sizing first lets an oversized pattern be
// rejected before its program is ever materialized, so the guard bounds the
// peak rather than only what is retained afterwards.
func compiledRegexCost(pattern string) (int, error) {
	parsed, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return 0, err
	}
	prog, err := syntax.Compile(parsed.Simplify())
	if err != nil {
		return 0, err
	}
	return len(prog.Inst), nil
}

func (c *regexCache) compile(pattern string) (*regexp.Regexp, error) {
	c.mu.Lock()
	if elem := c.entries[pattern]; elem != nil {
		c.lru.MoveToFront(elem)
		entry := elem.Value.(regexCacheEntry)
		c.mu.Unlock()
		return entry.re, nil
	}
	c.mu.Unlock()

	cost, err := compiledRegexCost(pattern)
	if err != nil {
		// Sizing parses the same syntax regexp.Compile does, so a parse error
		// here is the error the caller would have gotten from compiling.
		return nil, err
	}
	if cost > maxCompiledRegexInstructions {
		return nil, guardLimitErrorf(
			"regex compiles to %d instructions, exceeding limit %d (about %d MiB)",
			cost, maxCompiledRegexInstructions,
			maxCompiledRegexInstructions*compiledRegexInstructionBytes>>20,
		)
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if elem := c.entries[pattern]; elem != nil {
		c.lru.MoveToFront(elem)
		return elem.Value.(regexCacheEntry).re, nil
	}

	elem := c.lru.PushFront(regexCacheEntry{pattern: pattern, re: re, cost: cost})
	c.entries[pattern] = elem
	c.cost += cost
	c.evictLocked()
	return re, nil
}

// evictLocked drops least-recently-used entries until the cache is within both
// its entry count and its instruction budget. The most recent entry is kept
// even when it alone exceeds the budget: it is under the per-pattern cap, and
// the caller is about to use it.
func (c *regexCache) evictLocked() {
	for c.lru.Len() > 1 && (c.lru.Len() > c.capacity || c.cost > c.budget) {
		evicted := c.lru.Back()
		if evicted == nil {
			return
		}
		c.lru.Remove(evicted)
		entry := evicted.Value.(regexCacheEntry)
		delete(c.entries, entry.pattern)
		c.cost -= entry.cost
	}
}

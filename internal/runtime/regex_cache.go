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

// compiledRegexCost estimates how many program instructions pattern would
// compile to, from the parsed syntax alone.
//
// Parsing keeps a counted repeat as a single node with its bounds; expansion
// into the repeated concatenation happens in Simplify, and materializing the
// program happens in Compile. Both are the work this guard exists to prevent,
// so neither may run before the limit is enforced — estimating from the parse
// tree bounds the peak rather than measuring a program already built.
//
// The estimate walks the tree, multiplying a subtree's cost by its repeat
// bounds, and saturates at budget so a deeply nested repeat stops the walk
// instead of computing an enormous exact total. It over-approximates rather
// than under: every node is charged at least one instruction, which is the
// safe direction for a guard.
func compiledRegexCost(pattern string) (int, error) {
	parsed, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return 0, err
	}
	return estimateRegexProgramSize(parsed, maxCompiledRegexInstructions+1), nil
}

// estimateRegexProgramSize returns the estimated instruction count for re,
// clamped at budget.
func estimateRegexProgramSize(re *syntax.Regexp, budget int) int {
	if re == nil {
		return 0
	}
	switch re.Op {
	case syntax.OpLiteral:
		// One instruction per rune, and at least one for an empty literal.
		return clampRegexCost(max(len(re.Rune), 1), budget)
	case syntax.OpConcat:
		total := 0
		for _, sub := range re.Sub {
			total = saturatingAdd(total, estimateRegexProgramSize(sub, budget))
			if total >= budget {
				return budget
			}
		}
		return clampRegexCost(max(total, 1), budget)
	case syntax.OpAlternate:
		// Each arm plus a branch instruction per arm.
		total := len(re.Sub)
		for _, sub := range re.Sub {
			total = saturatingAdd(total, estimateRegexProgramSize(sub, budget))
			if total >= budget {
				return budget
			}
		}
		return clampRegexCost(max(total, 1), budget)
	case syntax.OpCapture:
		return clampRegexCost(saturatingAdd(2, estimateRegexProgramSize(re.Sub0[0], budget)), budget)
	case syntax.OpStar, syntax.OpPlus, syntax.OpQuest:
		// Nested quantifiers collapse: Simplify rewrites (?:a*)* to a*, so a
		// stack of them compiles to one. Charging each wrapper turned a chain
		// of them into a saturated estimate and rejected valid patterns.
		return clampRegexCost(saturatingAdd(1, estimateRegexProgramSize(unwrapIdempotentQuantifiers(re.Op, re.Sub0[0]), budget)), budget)
	case syntax.OpRepeat:
		// Simplification discards a {0} repeat body and all, so costing the
		// child would reject valid patterns over a body that never compiles.
		if re.Max == 0 {
			return clampRegexCost(1, budget)
		}
		// The expansion repeats the subtree once per bound. Copies past the
		// required minimum are optional, and each one compiles a branch
		// instruction alongside its copy; omitting those under-counted a
		// bounded range like a{0,1000} by nearly half. An open upper bound
		// instead adds a star tail over one more copy.
		inner := estimateRegexProgramSize(re.Sub0[0], budget)
		reps := re.Max
		optional := 0
		if reps < 0 {
			reps = re.Min + 1
		} else {
			optional = reps - re.Min
		}
		total := saturatingMul(inner, max(reps, 1))
		total = saturatingAdd(total, optional)
		return clampRegexCost(saturatingAdd(1, total), budget)
	default:
		// Character classes, anchors, empty and no-match nodes are all a
		// single instruction.
		return clampRegexCost(1, budget)
	}
}

func clampRegexCost(cost, budget int) int {
	if cost > budget {
		return budget
	}
	return cost
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

// unwrapIdempotentQuantifiers collapses a chain of nested quantifiers that
// repeat the SAME operator, which is the only combination Simplify rewrites:
// (?:a*)* becomes a*, so charging each wrapper over-counts a pattern that
// compiles small. Mixed quantifiers are left alone — (?:a+)? keeps both
// instructions, and collapsing them under-counted a program by the number of
// levels, which is the direction a guard must never be wrong in.
func unwrapIdempotentQuantifiers(parentOp syntax.Op, re *syntax.Regexp) *syntax.Regexp {
	switch parentOp {
	case syntax.OpStar, syntax.OpPlus, syntax.OpQuest:
	default:
		return re
	}
	for re != nil && re.Op == parentOp {
		re = re.Sub0[0]
	}
	return re
}

package runtime

// This file wires the value package's equality byte charge (see
// EqualityContext.SetCharge) to the execution's step quota, so comparisons
// that reach string payloads through arrays and hashes are bounded the same
// way a top-level `s == t` is (#1135).

// stringScanChargeFunc returns the execution's equality byte charge bound as
// a plain function, cached on the execution so the hot `==` path does not
// allocate a method value per comparison. A nil execution returns nil, which
// the equality context treats as unmetered.
func (exec *Execution) stringScanChargeFunc() func(int) error {
	if exec == nil {
		return nil
	}
	if exec.stringScanCharge == nil {
		exec.stringScanCharge = exec.chargeEqualityScanBytes
	}
	return exec.stringScanCharge
}

// chargeEqualityScanBytes bills equality-walk bytes at the string-scan rate,
// carrying the sub-step remainder on the execution. Rounding each invocation
// down let a probe loop flush a sub-step tail per candidate and never bill
// the aggregate — a set operation could compare hundreds of distinct small
// composites in quadratic time for zero steps. Carrying the remainder settles
// whole steps as tails accumulate, so the unbilled residue stays under one
// step per execution rather than one per probe.
func (exec *Execution) chargeEqualityScanBytes(n int) error {
	if n <= 0 {
		return nil
	}
	exec.equalityScanResidue += n
	steps := exec.equalityScanResidue / stringScanBytesPerStep
	if steps <= 0 {
		return nil
	}
	exec.equalityScanResidue -= steps * stringScanBytesPerStep
	return exec.stepN(steps)
}

// bindEqualityMetering installs the execution's byte charge, scratch
// validator, and allocator rounder on ctx, so equality contexts embedded in
// longer-lived structures (the set helpers) meter exactly like
// meteredEquality's. The rounder makes rendered display keys reserve the
// capacity the allocator realizes for the pregrown builder, not the
// projected length; see projectedBuilderCap for why the size-class gap
// matters.
func (exec *Execution) bindEqualityMetering(ctx *EqualityContext) {
	ctx.SetCharge(exec.stringScanChargeFunc())
	ctx.SetScratchReserver(exec.equalityScratchValidatorFunc())
	ctx.SetScratchReleaser(exec.equalityScratchReleaseFunc())
	ctx.SetScratchAllocRounder(roundedAllocSize)
}

// meteredEquality returns an EqualityContext that bills the string payloads a
// comparison reads at the string-scan rate and validates the walk's transient
// scratch against the memory quota. Loops that probe many candidates build
// one context, reuse it per element, and must surface Err before trusting a
// negative answer.
func (exec *Execution) meteredEquality() EqualityContext {
	var ctx EqualityContext
	exec.bindEqualityMetering(&ctx)
	return ctx
}

// hashKeySortScratchEntryBytes approximates one entry of a key-sorting
// scratch slice: a string header plus slice-slot overhead.
const hashKeySortScratchEntryBytes = 24

// Pricing walk scratch means estimating the reachable graph, and every check a
// builtin drives is uncached by construction (see beginBaseWalk), so pricing
// before each allocation made a compared one-entry hash pay a whole-graph walk
// to place 24 bytes of key slice: a membership probe over n small hashes ran n
// uncached walks while charging n steps, turning an O(n) scan into O(n²) host
// work. Scratch is therefore repriced once per granule instead.
//
// What made batching hazardous was not the granule but the fact that walk
// scratch was Go-local and never reserved: whatever the validator declined to
// price was invisible to every other check too, so an execution could sit
// arbitrarily far over its quota with nothing able to notice. Deciding when to
// look, from a cached usage figure, could not close that — only one of the
// estimator entry points records such a figure, and the others (call-root
// admission, projected-byte checks, the build accumulators) advance usage
// without it, so the figure is stale exactly when it matters.
//
// So the scratch is reserved for as long as the walk holds it. Every base walk
// counts reservedScratchBytes through estimateScalarBase, which means the
// periodic step check and all of those other estimators account for it without
// knowing it exists. The granule then decides only how often the validator
// spends a dedicated walk of its own, and can be sized from the configured
// quota alone: what goes unpriced is still counted.
const equalityScratchQuotaShift = 8

// equalityScratchGranule returns the walk scratch that may accumulate between
// the validator's own memory checks. It is a fraction of the configured quota
// rather than a fixed byte count, so a host configuring well below the default
// profile gets a proportionate granule rather than one sized for a quota it
// never asked for.
func (exec *Execution) equalityScratchGranule() int {
	if exec.memoryQuota <= 0 {
		return 0
	}
	// A share of the bound actually in force, so the granule -- and therefore the
	// scratch held between checks -- stays proportionate under an inherited
	// ceiling rather than to a local quota the chain will not honor.
	return exec.effectiveMemoryLimit() >> equalityScratchQuotaShift
}

// syncEqualityScratchReservation moves the execution's reserved scratch to the
// walk's currently held total, so the reservation tracks what the walk actually
// holds rather than what it briefly held during a check.
//
// One reservation is tracked per execution, which assumes comparisons on an
// execution do not nest. They do not today: the walk is pure Go and never
// re-enters evaluation, and concurrent tasks each get their own execution. If
// that ever changes, an inner walk would retire the outer's reservation early
// and the outer would restore it at its next allocation.
func (exec *Execution) syncEqualityScratchReservation(held int) {
	if held < 0 {
		held = 0
	}
	switch {
	case held > exec.equalityScratchReserved:
		exec.equalityScratchReserved += exec.reserveLoopScratch(held - exec.equalityScratchReserved)
	case held < exec.equalityScratchReserved:
		exec.releaseLoopScratch(exec.equalityScratchReserved - held)
		exec.equalityScratchReserved = held
	}
}

// equalityScratchValidatorFunc returns the cached scratch validator. It keeps
// the execution's scratch reservation in step with the walk's held total, and
// spends a memory check of its own once the unpriced remainder reaches a
// granule. The compared operands ride along as extra roots for that check: they
// can be temporaries (a host capability's return, a set probe's stored element)
// that no execution root reaches, yet both graphs coexist with the scratch at
// its peak.
func (exec *Execution) equalityScratchValidatorFunc() func(int, Value, Value) error {
	if exec == nil {
		return nil
	}
	if exec.equalityScratchCheck == nil {
		exec.equalityScratchCheck = func(bytes int, left, right Value) error {
			if exec.memoryQuota <= 0 {
				return nil
			}
			if bytes <= 0 {
				// A comparison boundary. The reservation and the granule mark
				// both belong to the walk that opened them, and a walk whose
				// first allocation matches the last total priced is otherwise
				// indistinguishable from a continuation of it.
				exec.retireEqualityScratch()
				return nil
			}
			exec.syncEqualityScratchReservation(bytes)
			if bytes-exec.equalityScratchPriced < exec.equalityScratchGranule() {
				return nil
			}
			exec.equalityScratchPriced = bytes
			return exec.checkMemoryWith(left, right)
		}
	}
	return exec.equalityScratchCheck
}

// equalValues is the one-shot metered comparison behind `==`, `!=`, and case
// equality: the boolean answer is only meaningful when the error is nil.
// The context is pooled on the execution: the walk's traversal closures leak
// the context's state, so a stack-scoped context would heap-allocate on every
// comparison. Taking the context out of the pool for the walk keeps any
// re-entrant comparison on a fresh context, and a context that surfaced an
// error is dropped rather than repooled because its charge failure is sticky.
func (exec *Execution) equalValues(left, right Value) (bool, error) {
	var ctx *EqualityContext
	if exec != nil && exec.equalityCtx != nil {
		ctx = exec.equalityCtx
		exec.equalityCtx = nil
	} else {
		// A nil execution (the static checker's speculative comparisons)
		// compares unmetered on a throwaway context.
		fresh := exec.meteredEquality()
		ctx = &fresh
	}
	eq := ctx.Equal(left, right)
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if exec != nil {
		exec.equalityCtx = ctx
	}
	return eq, nil
}

// equalityScratchReleaseFunc returns the cached walk-end callback: it retires
// the reservation the validator holds against a comparison's scratch, and the
// granule mark that belongs to the same walk. A nil execution reserves nothing
// and needs no callback.
func (exec *Execution) equalityScratchReleaseFunc() func() {
	if exec == nil {
		return nil
	}
	if exec.equalityScratchRelease == nil {
		exec.equalityScratchRelease = exec.retireEqualityScratch
	}
	return exec.equalityScratchRelease
}

// retireEqualityScratch releases the walk's reservation and forgets what it
// priced, so neither outlives the comparison that justified them.
func (exec *Execution) retireEqualityScratch() {
	exec.syncEqualityScratchReservation(0)
	exec.equalityScratchPriced = 0
}
